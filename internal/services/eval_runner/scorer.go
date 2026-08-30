package eval_runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ScoreConfig holds the config needed to run an LLM judge or code scorer.
type ScoreConfig struct {
	ID             string                 `db:"id"`
	Name           string                 `db:"name"`
	DataType       string                 `db:"data_type"`
	EvalPrompt     string                 `db:"eval_prompt"`
	EvalModel      string                 `db:"eval_model"`
	MinValue       *float64               `db:"min_value"`
	MaxValue       *float64               `db:"max_value"`
	ScorerCode     string                 `db:"scorer_code"`
	ScorerLanguage string                 `db:"scorer_language"`
	UseSandbox     bool                   `db:"use_sandbox"`
	Slug           string                 `db:"slug"`
	ScorerType     string                 `db:"scorer_type"`
	OutputType     string                 `db:"output_type"`
	Messages       []ScoreConfigMessage   `db:"messages"`
	ModelParams    ScoreConfigModelParams `db:"model_params"`
	ChoiceScores   []ScoreConfigChoice    `db:"choice_scores"`
	UseCot         bool                   `db:"use_cot"`
	PassThreshold  *float64               `db:"pass_threshold"`
	DagDefinition  []byte                 `db:"dag_definition"`
}

type ScoreConfigMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ScoreConfigModelParams struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int32   `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	ToolChoice  *string  `json:"tool_choice,omitempty"`
}

type ScoreConfigChoice struct {
	Choice string  `json:"choice"`
	Score  float64 `json:"score"`
}

type scoreConfigRow struct {
	ID             string   `db:"id"`
	Name           string   `db:"name"`
	DataType       string   `db:"data_type"`
	EvalPrompt     string   `db:"eval_prompt"`
	EvalModel      string   `db:"eval_model"`
	MinValue       *float64 `db:"min_value"`
	MaxValue       *float64 `db:"max_value"`
	ScorerCode     string   `db:"scorer_code"`
	ScorerLanguage string   `db:"scorer_language"`
	UseSandbox     bool     `db:"use_sandbox"`
	Slug           string   `db:"slug"`
	ScorerType     string   `db:"scorer_type"`
	OutputType     string   `db:"output_type"`
	Messages       []byte   `db:"messages"`
	ModelParams    []byte   `db:"model_params"`
	ChoiceScores   []byte   `db:"choice_scores"`
	UseCot         bool     `db:"use_cot"`
	PassThreshold  *float64 `db:"pass_threshold"`
	DagDefinition  []byte   `db:"dag_definition"`
}

type judgeCallFunc func(ctx context.Context, model string, messages []map[string]interface{}, sampling map[string]interface{}, responseFormat map[string]interface{}) (string, error)

// ScoreResult holds the result of a single scorer invocation.
type ScoreResult struct {
	Name   string
	Value  interface{} // float64 for numeric, bool for boolean, string for categorical
	Reason string
	Error  string
}

// modelCache caches the resolved model per tenant for the duration of a run.
type modelCache struct {
	mu    sync.Mutex
	cache map[string]string // tenantID -> model
}

var evalModelCache = &modelCache{cache: make(map[string]string)}

func (mc *modelCache) get(tenantID string) (string, bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	m, ok := mc.cache[tenantID]
	return m, ok
}

func (mc *modelCache) set(tenantID, model string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.cache[tenantID] = model
}

// loadScoreConfigs queries the DB for the given config IDs.
func (r *Runner) loadScoreConfigs(ctx context.Context, tenantID string, configIDs []string) ([]ScoreConfig, error) {
	if len(configIDs) == 0 {
		return nil, nil
	}

	var rows []scoreConfigRow
	query := `
		SELECT id, name, data_type, eval_prompt, eval_model, min_value, max_value,
			scorer_code, scorer_language, use_sandbox, slug, scorer_type, output_type,
			messages, model_params, choice_scores, use_cot, pass_threshold,
			COALESCE(dag_definition, 'null'::jsonb) AS dag_definition
		FROM score_configs
		WHERE tenant_id = $1 AND id = ANY($2) AND is_archived = false
	`
	if err := r.db.SelectContext(ctx, &rows, query, tenantID, configIDs); err != nil {
		return nil, fmt.Errorf("failed to load score configs: %w", err)
	}

	configs := make([]ScoreConfig, 0, len(rows))
	for _, row := range rows {
		cfg, err := row.toScoreConfig()
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

func (r scoreConfigRow) toScoreConfig() (ScoreConfig, error) {
	var messages []ScoreConfigMessage
	if err := unmarshalScoreConfigJSON(r.Messages, []byte("[]"), &messages); err != nil {
		return ScoreConfig{}, fmt.Errorf("failed to unmarshal score config messages: %w", err)
	}

	var modelParams ScoreConfigModelParams
	if err := unmarshalScoreConfigJSON(r.ModelParams, []byte("{}"), &modelParams); err != nil {
		return ScoreConfig{}, fmt.Errorf("failed to unmarshal score config model params: %w", err)
	}

	var choiceScores []ScoreConfigChoice
	if err := unmarshalScoreConfigJSON(r.ChoiceScores, []byte("[]"), &choiceScores); err != nil {
		return ScoreConfig{}, fmt.Errorf("failed to unmarshal score config choice scores: %w", err)
	}

	return ScoreConfig{
		ID:             r.ID,
		Name:           r.Name,
		DataType:       r.DataType,
		EvalPrompt:     r.EvalPrompt,
		EvalModel:      r.EvalModel,
		MinValue:       r.MinValue,
		MaxValue:       r.MaxValue,
		ScorerCode:     r.ScorerCode,
		ScorerLanguage: r.ScorerLanguage,
		UseSandbox:     r.UseSandbox,
		Slug:           r.Slug,
		ScorerType:     r.ScorerType,
		OutputType:     r.OutputType,
		Messages:       messages,
		ModelParams:    modelParams,
		ChoiceScores:   choiceScores,
		UseCot:         r.UseCot,
		PassThreshold:  r.PassThreshold,
		DagDefinition:  r.DagDefinition,
	}, nil
}

func unmarshalScoreConfigJSON(data, fallback []byte, v interface{}) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		data = fallback
	}
	return json.Unmarshal(data, v)
}

// runScorer runs an LLM-judge scorer. If cfg.EvalModel names multiple models
// (comma-separated, e.g. "gpt-4o, claude-3-5-sonnet"), it runs each as a juror
// and ensembles the results — an LLM jury that reduces single-judge bias (mean
// for numeric, majority for boolean/categorical). A single model behaves as before.
func (r *Runner) runScorer(ctx context.Context, tenantID string, cfg ScoreConfig, input, output, expectedOutput, metadata interface{}, retrievedContext string) (*ScoreResult, error) {
	models := splitJuryModels(cfg.EvalModel)
	if len(models) <= 1 {
		single := ""
		if len(models) == 1 {
			single = models[0]
		}
		return r.runSingleJudge(ctx, tenantID, cfg, single, input, output, expectedOutput, metadata, retrievedContext)
	}
	var results []*ScoreResult
	for _, m := range models {
		res, err := r.runSingleJudge(ctx, tenantID, cfg, m, input, output, expectedOutput, metadata, retrievedContext)
		if err != nil {
			logger.WithFields("scorer", cfg.Name, "juror", m, "error", err.Error()).Warn("eval jury member failed; skipping")
			continue
		}
		results = append(results, res)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("all %d jury members failed for scorer %s", len(models), cfg.Name)
	}
	return aggregateJury(cfg, results), nil
}

// runSingleJudge executes one LLM judge against an item with a specific model
// (empty evalModel falls back to the tenant default).
func (r *Runner) runSingleJudge(ctx context.Context, tenantID string, cfg ScoreConfig, evalModel string, input, output, expectedOutput, metadata interface{}, retrievedContext string) (*ScoreResult, error) {
	model, err := r.resolveEvalModel(ctx, tenantID, evalModel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve eval model: %w", err)
	}

	reqPayload := map[string]interface{}{
		"type": "json_object",
	}
	// Sampling knobs must be nested under "sampling" (the gateway reads them
	// from the proto sampling field; top-level params are discarded).
	content, err := r.callJudge(ctx, model, buildJudgeMessages(cfg, input, output, expectedOutput, metadata, retrievedContext), buildJudgeSampling(cfg.ModelParams), reqPayload)
	if err != nil {
		return nil, err
	}

	value, reason, err := parseScoreResponse(content, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse judge score: %w", err)
	}

	return &ScoreResult{
		Name:   cfg.Name,
		Value:  value,
		Reason: reason,
	}, nil
}

func (r *Runner) callJudge(ctx context.Context, model string, messages []map[string]interface{}, sampling map[string]interface{}, responseFormat map[string]interface{}) (string, error) {
	if r != nil && r.judgeCall != nil {
		return r.judgeCall(ctx, model, messages, sampling, responseFormat)
	}
	return defaultJudgeGatewayCall(ctx, model, messages, sampling, responseFormat)
}

func defaultJudgeGatewayCall(ctx context.Context, model string, messages []map[string]interface{}, sampling map[string]interface{}, responseFormat map[string]interface{}) (string, error) {
	reqPayload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}
	if responseFormat != nil {
		reqPayload["response_format"] = responseFormat
	}
	if sampling != nil {
		reqPayload["sampling"] = sampling
	}

	body, _ := json.Marshal(reqPayload)
	url := evalRunnerGatewayURL()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build judge request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MF_EVAL_RUNNER_API_KEY"); key != "" {
		httpReq.Header.Set("x-evs-api-key", key)
		httpReq.Header.Set("x-mf-api-key", key) // legacy alias (rolling-deploy safe)
	} else {
		internalauth.SetHeader(httpReq.Header)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("judge request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("judge request failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var respMap map[string]interface{}
	if err := json.Unmarshal(respBody, &respMap); err != nil {
		return "", fmt.Errorf("failed to parse judge response: %w", err)
	}

	content := extractJudgeContent(respMap)
	if content == "" {
		return "", fmt.Errorf("empty response from judge")
	}
	return content, nil
}

// splitJuryModels splits a comma-separated EvalModel into trimmed model names.
func splitJuryModels(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// aggregateJury ensembles multiple judges' results into one score: mean for
// numeric, majority for boolean, mode for categorical/text.
func aggregateJury(cfg ScoreConfig, results []*ScoreResult) *ScoreResult {
	n := len(results)
	switch strings.ToLower(cfg.DataType) {
	case "boolean":
		trueVotes := 0
		for _, res := range results {
			if b, ok := res.Value.(bool); ok && b {
				trueVotes++
			}
		}
		return &ScoreResult{Name: cfg.Name, Value: trueVotes*2 >= n, Reason: juryReason(n, results)}
	case "categorical", "text", "string":
		counts := map[string]int{}
		best, bestN := "", 0
		for _, res := range results {
			s := fmt.Sprintf("%v", res.Value)
			counts[s]++
			if counts[s] > bestN {
				bestN, best = counts[s], s
			}
		}
		return &ScoreResult{Name: cfg.Name, Value: best, Reason: juryReason(n, results)}
	default: // numeric
		var sum float64
		cnt := 0
		for _, res := range results {
			if f, ok := toFloat64(res.Value); ok {
				sum += f
				cnt++
			}
		}
		mean := 0.0
		if cnt > 0 {
			mean = sum / float64(cnt)
		}
		return &ScoreResult{Name: cfg.Name, Value: mean, Reason: juryReason(n, results)}
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func juryReason(n int, results []*ScoreResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LLM jury of %d.", n)
	for i, res := range results {
		fmt.Fprintf(&b, " [%d] %v", i+1, res.Value)
		if res.Reason != "" {
			reason := res.Reason
			if len(reason) > 100 {
				reason = reason[:100] + "…"
			}
			fmt.Fprintf(&b, ": %s", reason)
		}
	}
	return b.String()
}

// buildJudgePrompt performs {{variable}} substitution on a prompt template.
// Supported vars: {{input}} {{output}} {{expected_output}} (alias {{expected}})
// {{context}} {{metadata}}.
func buildJudgePrompt(template string, input, output, expectedOutput, metadata interface{}, retrievedContext string) string {
	inputJSON, _ := json.Marshal(input)
	outputJSON, _ := json.Marshal(output)
	expectedJSON, _ := json.Marshal(expectedOutput)
	metadataJSON, _ := json.Marshal(metadata)

	contextStr := retrievedContext
	if contextStr == "" {
		contextStr = string(inputJSON)
	}

	result := template
	result = strings.ReplaceAll(result, "{{input}}", string(inputJSON))
	result = strings.ReplaceAll(result, "{{output}}", string(outputJSON))
	result = strings.ReplaceAll(result, "{{expected_output}}", string(expectedJSON))
	result = strings.ReplaceAll(result, "{{expected}}", string(expectedJSON))
	result = strings.ReplaceAll(result, "{{context}}", contextStr)
	result = strings.ReplaceAll(result, "{{metadata}}", string(metadataJSON))

	return result
}

// buildJudgeMessages builds the chat messages for an LLM-judge call: a
// format-enforcing system message followed by either the config's multi-part
// messages (Braintrust-style, with {{var}} substitution) or, for legacy configs,
// the single eval_prompt rendered as a user turn.
func buildJudgeMessages(cfg ScoreConfig, input, output, expectedOutput, metadata interface{}, retrievedContext string) []map[string]interface{} {
	msgs := []map[string]interface{}{
		{"role": "system", "content": judgeSystemPrompt(cfg)},
	}
	if len(cfg.Messages) > 0 {
		for _, m := range cfg.Messages {
			role := m.Role
			if role == "" {
				role = "user"
			}
			msgs = append(msgs, map[string]interface{}{
				"role":    role,
				"content": buildJudgePrompt(m.Content, input, output, expectedOutput, metadata, retrievedContext),
			})
		}
		return msgs
	}
	msgs = append(msgs, map[string]interface{}{
		"role":    "user",
		"content": buildJudgePrompt(cfg.EvalPrompt, input, output, expectedOutput, metadata, retrievedContext),
	})
	return msgs
}

// judgeSystemPrompt returns the output-format instruction for the judge, which
// differs by output type. For choice scorers the judge is asked (prompt-enforced)
// to pick exactly one configured label; for everything else it returns a score.
func judgeSystemPrompt(cfg ScoreConfig) string {
	if effectiveOutputType(cfg) == "choice" && len(cfg.ChoiceScores) > 0 {
		labels := make([]string, 0, len(cfg.ChoiceScores))
		for _, c := range cfg.ChoiceScores {
			labels = append(labels, c.Choice)
		}
		cot := ""
		if cfg.UseCot {
			cot = " First reason step by step about the data, then decide."
		}
		return fmt.Sprintf(`You are an expert evaluation judge. Choose exactly one option from: [%s].%s Return ONLY valid JSON with two fields: {"choice": "<one of the options, exactly as written>", "reasoning": "<brief explanation>"}. Do not include any other text or markdown formatting.`, strings.Join(labels, ", "), cot)
	}
	return `You are an expert evaluation judge. Analyze the provided data and return a JSON object with exactly two fields:
- "score": a numeric value representing your assessment
- "reason": a brief explanation of your score

Return ONLY valid JSON. Do not include any other text or markdown formatting.`
}

// buildJudgeSampling maps the config's model params to the gateway's nested
// "sampling" object. Returns nil when no params are set (so the request omits it).
func buildJudgeSampling(p ScoreConfigModelParams) map[string]interface{} {
	s := map[string]interface{}{}
	if p.Temperature != nil {
		s["temperature"] = *p.Temperature
	}
	if p.TopP != nil {
		s["top_p"] = *p.TopP
	}
	if p.MaxTokens != nil {
		s["max_tokens"] = *p.MaxTokens
	}
	if len(p.Stop) > 0 {
		s["stop"] = p.Stop
	}
	if len(s) == 0 {
		return nil
	}
	return s
}

// parseScoreResponse extracts score + reason from the JSON response.
func parseScoreResponse(content string, cfg ScoreConfig) (interface{}, string, error) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, "", fmt.Errorf("invalid JSON from judge: %w", err)
	}

	// Choice scorers: the judge returns {"choice": "<label>", "reasoning": ...};
	// map the chosen label to its configured 0..1 score. Guard on len>0 to stay
	// consistent with judgeSystemPrompt (which only emits the choice format when
	// choices exist) — a choice config with no choices falls through to numeric.
	if effectiveOutputType(cfg) == "choice" && len(cfg.ChoiceScores) > 0 {
		reason, _ := parsed["reasoning"].(string)
		if reason == "" {
			reason, _ = parsed["reason"].(string)
		}
		rawChoice, ok := parsed["choice"]
		if !ok {
			return nil, reason, fmt.Errorf("judge response missing 'choice' field")
		}
		choiceStr := strings.TrimSpace(fmt.Sprintf("%v", rawChoice))
		score, ok := choiceScoreFor(cfg.ChoiceScores, choiceStr)
		if !ok {
			return nil, reason, fmt.Errorf("judge chose %q which is not a configured choice", choiceStr)
		}
		return score, reason, nil
	}

	rawScore, ok := parsed["score"]
	if !ok {
		return nil, "", fmt.Errorf("judge response missing 'score' field")
	}

	reason, _ := parsed["reason"].(string)

	switch strings.ToUpper(cfg.DataType) {
	case "BOOLEAN":
		return coerceToBool(rawScore), reason, nil
	case "NUMERIC":
		val, err := coerceToFloat(rawScore)
		if err != nil {
			return nil, reason, fmt.Errorf("could not parse numeric score: %w", err)
		}
		if cfg.MinValue != nil && val < *cfg.MinValue {
			val = *cfg.MinValue
		}
		if cfg.MaxValue != nil && val > *cfg.MaxValue {
			val = *cfg.MaxValue
		}
		return val, reason, nil
	case "CATEGORICAL":
		s := fmt.Sprintf("%v", rawScore)
		return s, reason, nil
	default:
		// For llm_judge or any other type, try numeric first, fall back to raw
		val, err := coerceToFloat(rawScore)
		if err == nil {
			if cfg.MinValue != nil && val < *cfg.MinValue {
				val = *cfg.MinValue
			}
			if cfg.MaxValue != nil && val > *cfg.MaxValue {
				val = *cfg.MaxValue
			}
			return val, reason, nil
		}
		return rawScore, reason, nil
	}
}

// resolveEvalModel determines which model to use for scoring.
func (r *Runner) resolveEvalModel(ctx context.Context, tenantID, configModel string) (string, error) {
	if configModel != "" {
		return configModel, nil
	}

	if cached, ok := evalModelCache.get(tenantID); ok {
		return cached, nil
	}

	url := evalRunnerGatewayURL()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/v1/models", nil)
	if err != nil {
		return "", fmt.Errorf("failed to build models request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MF_EVAL_RUNNER_API_KEY"); key != "" {
		httpReq.Header.Set("x-evs-api-key", key)
		httpReq.Header.Set("x-mf-api-key", key) // legacy alias (rolling-deploy safe)
	} else {
		internalauth.SetHeader(httpReq.Header)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("models request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("models request failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var respMap map[string]interface{}
	if err := json.Unmarshal(respBody, &respMap); err != nil {
		return "", fmt.Errorf("failed to parse models response: %w", err)
	}

	data, ok := respMap["data"].([]interface{})
	if !ok || len(data) == 0 {
		return "", fmt.Errorf("no models available for tenant %s", tenantID)
	}

	first, ok := data[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid model entry format")
	}

	model, ok := first["id"].(string)
	if !ok || model == "" {
		return "", fmt.Errorf("model entry has no id")
	}

	logger.WithFields("tenant_id", tenantID, "model", model).Info("eval_runner: resolved eval model")
	evalModelCache.set(tenantID, model)
	return model, nil
}

// extractJudgeContent extracts the text content from a chat completion response.
func extractJudgeContent(resp map[string]interface{}) string {
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return ""
	}
	msg, ok := choice["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	// The gateway's /v1/chat/completions returns proto-JSON, where content may
	// be a plain string OR an array of content parts [{type,text}, ...].
	// Accept both so the judge parse never silently sees "".
	switch c := msg["content"].(type) {
	case string:
		return c
	case []interface{}:
		var b strings.Builder
		for _, part := range c {
			if pm, ok := part.(map[string]interface{}); ok {
				if txt, ok := pm["text"].(string); ok {
					b.WriteString(txt)
				}
			}
		}
		return b.String()
	}
	return ""
}

func coerceToBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case string:
		b, _ := strconv.ParseBool(val)
		return b
	default:
		return false
	}
}

func coerceToFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	case bool:
		if val {
			return 1.0, nil
		}
		return 0.0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}
