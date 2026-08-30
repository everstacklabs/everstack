package eval_runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// CodeScorerInput is the JSON payload written to stdin of the scorer script.
type CodeScorerInput struct {
	Input          interface{} `json:"input"`
	Output         interface{} `json:"output"`
	ExpectedOutput interface{} `json:"expected_output"`
	Context        string      `json:"context"`
	Metadata       interface{} `json:"metadata"`
}

// runCodeScorer executes a user-supplied script (Python/JS/TS) in-process via exec.CommandContext.
// The script receives CodeScorerInput on stdin and must write {"score": <number>, "reason": "..."} to stdout.
func (r *Runner) runCodeScorer(ctx context.Context, cfg ScoreConfig, input CodeScorerInput) (*ScoreResult, error) {
	lang := strings.ToLower(cfg.ScorerLanguage)
	var cmdName string
	var cmdArgs []string

	switch lang {
	case "python":
		cmdName = "python3"
		cmdArgs = []string{"-c", cfg.ScorerCode}
	case "javascript", "js":
		cmdName = "node"
		cmdArgs = []string{"-e", cfg.ScorerCode}
	case "typescript", "ts":
		// Use tsx or ts-node if available, fall back to node
		cmdName = "npx"
		cmdArgs = []string{"tsx", "-e", cfg.ScorerCode}
	default:
		return nil, fmt.Errorf("unsupported scorer language: %s", lang)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, cmdName, cmdArgs...)

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal code scorer input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if len(stderrStr) > 500 {
			stderrStr = stderrStr[:500] + "..."
		}
		logger.WithFields("scorer", cfg.Name, "language", lang, "stderr", stderrStr).
			WithError(err).Warn("eval_runner: code scorer execution failed")
		return nil, fmt.Errorf("code scorer failed: %w (stderr: %s)", err, stderrStr)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, fmt.Errorf("code scorer produced no output")
	}

	value, reason, err := parseScoreResponse(output, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse code scorer output: %w", err)
	}

	return &ScoreResult{
		Name:   cfg.Name,
		Value:  value,
		Reason: reason,
	}, nil
}
