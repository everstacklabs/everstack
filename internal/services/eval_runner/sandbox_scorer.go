package eval_runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// SandboxScorerConfig configures sandbox-based scoring from eval_config JSONB.
type SandboxScorerConfig struct {
	Image          string `json:"image"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MemoryMB       int64  `json:"memory_mb"`
}

// SandboxScorer delegates scoring (code and LLM judge) to isolated sandboxes.
type SandboxScorer struct {
	manager *sandbox.SandboxManager

	mu       sync.Mutex
	sessions map[string]bool // sessionID -> created
}

// NewSandboxScorer creates a new sandbox scorer.
func NewSandboxScorer(manager *sandbox.SandboxManager) *SandboxScorer {
	return &SandboxScorer{
		manager:  manager,
		sessions: make(map[string]bool),
	}
}

// ScoreInSandbox runs a scorer inside a sandbox container.
// For code scorers: writes the script + input, executes, parses stdout.
// For LLM judge scorers: writes a wrapper script that calls the gateway endpoint.
func (s *SandboxScorer) ScoreInSandbox(ctx context.Context, namespace, tenantID string, cfg ScoreConfig, input CodeScorerInput) (*ScoreResult, error) {
	sessionID := fmt.Sprintf("eval-%s-%s", namespace, cfg.ID)

	if err := s.ensureSandbox(ctx, sessionID, tenantID, cfg); err != nil {
		return nil, fmt.Errorf("sandbox setup failed: %w", err)
	}

	if cfg.ScorerCode != "" && cfg.ScorerLanguage != "" {
		return s.runCodeInSandbox(ctx, sessionID, cfg, input)
	}
	if cfg.EvalPrompt != "" {
		return s.runJudgeInSandbox(ctx, sessionID, cfg, input)
	}

	return nil, fmt.Errorf("score config %s has neither code nor eval prompt", cfg.ID)
}

func (s *SandboxScorer) ensureSandbox(ctx context.Context, sessionID, tenantID string, cfg ScoreConfig) error {
	s.mu.Lock()
	created := s.sessions[sessionID]
	s.mu.Unlock()

	if created {
		return nil
	}

	sbxCfg := sandbox.SandboxConfig{
		Enabled:        true,
		Image:          defaultSandboxImage(cfg.ScorerLanguage),
		CPULimit:       1,
		MemoryMB:       512,
		TimeoutSeconds: 60,
		NetworkMode:    "whitelist",
		AllowedHosts:   []string{"localhost", "127.0.0.1"},
		Name:           sessionID,
	}

	_, err := s.manager.GetOrCreate(ctx, sessionID, tenantID, sbxCfg)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.sessions[sessionID] = true
	s.mu.Unlock()

	return nil
}

func (s *SandboxScorer) runCodeInSandbox(ctx context.Context, sessionID string, cfg ScoreConfig, input CodeScorerInput) (*ScoreResult, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	lang := strings.ToLower(cfg.ScorerLanguage)
	var cmd []string

	switch lang {
	case "python":
		// Write script to file, pipe input via stdin using echo
		script := strings.ReplaceAll(cfg.ScorerCode, "'", "'\\''")
		cmd = []string{"sh", "-c", fmt.Sprintf("echo '%s' | python3 -c '%s'",
			strings.ReplaceAll(string(inputJSON), "'", "'\\''"), script)}
	case "javascript", "js":
		script := strings.ReplaceAll(cfg.ScorerCode, "'", "'\\''")
		cmd = []string{"sh", "-c", fmt.Sprintf("echo '%s' | node -e '%s'",
			strings.ReplaceAll(string(inputJSON), "'", "'\\''"), script)}
	default:
		return nil, fmt.Errorf("unsupported sandbox language: %s", lang)
	}

	result, err := s.manager.Exec(ctx, sessionID, sandbox.ExecRequest{
		Command: cmd,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		stderr := result.Stderr
		if len(stderr) > 500 {
			stderr = stderr[:500] + "..."
		}
		return nil, fmt.Errorf("code scorer exited %d: %s", result.ExitCode, stderr)
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return nil, fmt.Errorf("code scorer produced no output")
	}

	value, reason, err := parseScoreResponse(output, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sandbox scorer output: %w", err)
	}

	return &ScoreResult{
		Name:   cfg.Name,
		Value:  value,
		Reason: reason,
	}, nil
}

func (s *SandboxScorer) runJudgeInSandbox(ctx context.Context, sessionID string, cfg ScoreConfig, input CodeScorerInput) (*ScoreResult, error) {
	inputJSON, _ := json.Marshal(input.Input)
	outputJSON, _ := json.Marshal(input.Output)
	expectedJSON, _ := json.Marshal(input.ExpectedOutput)

	userPrompt := buildJudgePrompt(cfg.EvalPrompt, input.Input, input.Output, input.ExpectedOutput, input.Metadata, input.Context)

	gatewayURL := evalRunnerGatewayURL()

	// This judge runs *inside* the sandbox, which executes untrusted code, so
	// it must never be handed the process-local internalauth token: anything
	// that reaches the sandbox is disclosed to whatever is running there. It
	// needs a real, revocable, tenant-scoped credential.
	//
	// It previously authenticated by sending `Sec-Fetch-Site: same-origin`,
	// which the gateway accepted as proof of a first-party call. That let any
	// sandboxed code reach the gateway unauthenticated and name any tenant it
	// liked. Fail closed instead of silently keeping that path alive.
	apiKey := strings.TrimSpace(os.Getenv("MF_EVAL_RUNNER_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("sandbox judge scorer requires a gateway credential: set MF_EVAL_RUNNER_API_KEY. " +
			"It previously relied on a Sec-Fetch-Site same-origin bypass that has been removed because it allowed " +
			"unauthenticated cross-tenant access")
	}
	judgeHeaders, err := json.Marshal(map[string]string{
		"Content-Type":  "application/json",
		"x-evs-api-key": apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("build judge request headers: %w", err)
	}

	// Python wrapper script that calls the gateway
	script := fmt.Sprintf(`
import json, urllib.request, sys

payload = {
    "model": %q,
    "messages": [
        {"role": "system", "content": "You are an expert evaluation judge. Analyze the provided data and return a JSON object with exactly two fields:\n- \"score\": a numeric value representing your assessment\n- \"reason\": a brief explanation of your score\n\nReturn ONLY valid JSON."},
        {"role": "user", "content": %s}
    ],
    "stream": False,
    "response_format": {"type": "json_object"}
}

req = urllib.request.Request(
    %q + "/v1/chat/completions",
    data=json.dumps(payload).encode(),
    headers=json.loads(%q),
    method="POST"
)
resp = urllib.request.urlopen(req, timeout=120)
body = json.loads(resp.read())
content = body["choices"][0]["message"]["content"]
print(content)
`, cfg.EvalModel, mustMarshalString(userPrompt), gatewayURL, string(judgeHeaders))

	result, err := s.manager.Exec(ctx, sessionID, sandbox.ExecRequest{
		Command: []string{"python3", "-c", script},
		Timeout: 120 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox judge exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("sandbox judge exited %d: %s", result.ExitCode, result.Stderr)
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return nil, fmt.Errorf("sandbox judge produced no output")
	}

	value, reason, err := parseScoreResponse(output, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sandbox judge output: %w", err)
	}

	// Suppress unused variable warnings
	_ = inputJSON
	_ = outputJSON
	_ = expectedJSON

	return &ScoreResult{
		Name:   cfg.Name,
		Value:  value,
		Reason: reason,
	}, nil
}

// DestroyRunSandboxes cleans up all sandboxes for a given run.
func (s *SandboxScorer) DestroyRunSandboxes(ctx context.Context, runID string) {
	prefix := fmt.Sprintf("eval-%s-", runID)
	if err := s.manager.DestroyBySessionPrefix(ctx, prefix); err != nil {
		logger.WithFields("eval_run_id", runID).WithError(err).Warn("eval_runner: sandbox prefix cleanup failed")
	}

	s.mu.Lock()
	for sid := range s.sessions {
		if strings.HasPrefix(sid, prefix) {
			delete(s.sessions, sid)
		}
	}
	s.mu.Unlock()
}

func defaultSandboxImage(language string) string {
	switch strings.ToLower(language) {
	case "python":
		return "python:3.11-slim"
	case "javascript", "js", "typescript", "ts":
		return "node:20-slim"
	default:
		return "python:3.11-slim"
	}
}

func mustMarshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// parseSandboxConfig extracts SandboxScorerConfig from eval_config JSONB.
func parseSandboxConfig(evalConfig []byte) SandboxScorerConfig {
	var cfg SandboxScorerConfig
	if len(evalConfig) == 0 {
		return cfg
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(evalConfig, &raw); err != nil {
		return cfg
	}
	sbx, ok := raw["sandbox"]
	if !ok {
		return cfg
	}
	b, err := json.Marshal(sbx)
	if err != nil {
		return cfg
	}
	json.Unmarshal(b, &cfg)
	return cfg
}
