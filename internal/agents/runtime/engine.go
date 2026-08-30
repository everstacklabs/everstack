package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	validator "github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// TurnInput contains everything needed to execute a single turn.
type TurnInput struct {
	AgentID      string
	SessionID    string
	Model        string
	SystemPrompt string
	Tools        []string
	Config       map[string]interface{} // temperature, max_tokens, etc.
	// Conversation history from previous turns
	PreviousTurns []TurnHistory
	// Current user input
	UserInput string
}

// TurnHistory represents a previous turn's messages for context.
type TurnHistory struct {
	UserInput       string `json:"user_input"`
	AssistantOutput string `json:"assistant_output"`
}

// TurnResult contains the output of a single turn execution.
//
// PromptTokens is inclusive (cached + fresh). CacheReadTokens and
// CacheWriteTokens are non-overlapping subsets so callers can compute
// fresh = PromptTokens − CacheReadTokens − CacheWriteTokens for the
// cost breakdown.
type TurnResult struct {
	AssistantOutput  string          `json:"assistant_output"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
	PromptTokens     int32           `json:"prompt_tokens"`
	CompletionTokens int32           `json:"completion_tokens"`
	TotalTokens      int32           `json:"total_tokens"`
	CacheReadTokens  int32           `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int32           `json:"cache_write_tokens,omitempty"`
	LatencyMs        int64           `json:"latency_ms"`
	FinishReason     string          `json:"finish_reason"`
	Error            string          `json:"error,omitempty"`
}

// ProviderSource resolves the provider bundle for a request. Implementations
// must return tenant-scoped registry/router instances and fail closed when the
// request has no valid tenant identity.
type ProviderSource interface {
	ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error)
}

// Engine executes agent turns by routing to LLM providers via the gateway.
type Engine struct {
	registry       *gw.Registry
	router         *gw.Router
	toolLoop       *toolloop.LoopManager
	providerSource ProviderSource
}

// NewEngine creates a new agent runtime engine reusing gateway infrastructure.
func NewEngine(registry *gw.Registry, router *gw.Router, toolLoop *toolloop.LoopManager) *Engine {
	return &Engine{
		registry: registry,
		router:   router,
		toolLoop: toolLoop,
	}
}

// SetProviderSource configures request-scoped provider resolution. It is
// intended to be called once during server wiring, before requests are served.
// When set, the engine never falls back to its startup provider pointers.
func (e *Engine) SetProviderSource(source ProviderSource) {
	e.providerSource = source
}

// ProvidersForContext returns the provider bundle for ctx. A configured
// ProviderSource is authoritative: falling back to startup pointers after a
// tenant lookup failure could route a request with another tenant's API keys.
func (e *Engine) ProvidersForContext(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	if e == nil {
		return nil, nil, fmt.Errorf("agent runtime engine is not initialized")
	}
	if e.providerSource != nil {
		registry, router, err := e.providerSource.ProviderBundleForRequest(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve providers for request: %w", err)
		}
		if registry == nil || router == nil {
			return nil, nil, fmt.Errorf("provider bundle unavailable for request")
		}
		return registry, router, nil
	}
	if e.registry == nil || e.router == nil {
		return nil, nil, fmt.Errorf("provider bundle is not initialized")
	}
	return e.registry, e.router, nil
}

// ExecuteTurn runs a single conversation turn against an LLM provider.
func (e *Engine) ExecuteTurn(ctx context.Context, input *TurnInput) (*TurnResult, error) {
	start := time.Now()

	// Build messages array
	var messages []gw.Message

	// System prompt
	if input.SystemPrompt != "" {
		messages = append(messages, gw.Message{
			Role: gw.RoleSystem,
			Content: []gw.ContentPart{{
				Type: "text",
				Text: strPtr(input.SystemPrompt),
			}},
		})
	}

	// Previous turns as conversation history
	for _, turn := range input.PreviousTurns {
		if turn.UserInput != "" {
			messages = append(messages, gw.Message{
				Role: gw.RoleUser,
				Content: []gw.ContentPart{{
					Type: "text",
					Text: strPtr(turn.UserInput),
				}},
			})
		}
		if turn.AssistantOutput != "" {
			messages = append(messages, gw.Message{
				Role: gw.RoleAssistant,
				Content: []gw.ContentPart{{
					Type: "text",
					Text: strPtr(turn.AssistantOutput),
				}},
			})
		}
	}

	// Current user input
	messages = append(messages, gw.Message{
		Role: gw.RoleUser,
		Content: []gw.ContentPart{{
			Type: "text",
			Text: strPtr(input.UserInput),
		}},
	})

	// Build sampling params from agent config.
	sampling := samplingParamsFromConfig(input.Config)

	// Build chat request
	req := gw.ChatCompletionRequest{
		Model:    input.Model,
		Messages: messages,
		Sampling: sampling,
	}

	// Resolve provider via the request-scoped router.
	_, router, err := e.ProvidersForContext(ctx)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		return &TurnResult{
			LatencyMs: latency,
			Error:     fmt.Sprintf("failed to resolve model %s: %v", input.Model, err),
		}, nil
	}
	provider, resolvedModel, err := router.ResolveWithContext(ctx, input.Model)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		return &TurnResult{
			LatencyMs: latency,
			Error:     fmt.Sprintf("failed to resolve model %s: %v", input.Model, err),
		}, nil
	}
	if resolvedModel.ModelName != "" {
		req.Model = resolvedModel.ModelName
	}

	// Type assert to ChatProvider
	chatProvider, ok := provider.(gw.ChatProvider)
	if !ok {
		latency := time.Since(start).Milliseconds()
		return &TurnResult{
			LatencyMs: latency,
			Error:     fmt.Sprintf("provider for model %s does not support chat", input.Model),
		}, nil
	}

	logger.WithFields(
		"agent_id", input.AgentID,
		"session_id", input.SessionID,
		"model", input.Model,
		"message_count", len(messages),
	).Debug("executing agent turn")

	// Call LLM provider
	resp, err := chatProvider.Chat(ctx, req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return &TurnResult{
			LatencyMs: latency,
			Error:     fmt.Sprintf("LLM call failed: %v", err),
		}, nil
	}

	result := &TurnResult{
		PromptTokens:     int32(resp.Usage.PromptTokens),
		CompletionTokens: int32(resp.Usage.CompletionTokens),
		TotalTokens:      int32(resp.Usage.TotalTokens),
		LatencyMs:        latency,
		ToolCalls:        []byte("[]"),
	}
	if resp.Usage.PromptDetails != nil {
		result.CacheReadTokens = int32(resp.Usage.PromptDetails.CacheReadTokens)
		result.CacheWriteTokens = int32(resp.Usage.PromptDetails.CacheWriteTokens)
	}

	// Extract assistant output from first choice
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.FinishReason = choice.FinishReason

		// A provider may preserve its native reasoning chunks as multiple
		// content parts. Concatenate every user-facing text part in order.
		result.AssistantOutput = messageText(choice.Message)

		// Extract tool calls if present
		if len(choice.Message.ToolCalls) > 0 {
			tcJSON, err := json.Marshal(choice.Message.ToolCalls)
			if err == nil {
				result.ToolCalls = tcJSON
			}
		}
	}

	logger.WithFields(
		"agent_id", input.AgentID,
		"session_id", input.SessionID,
		"latency_ms", latency,
		"total_tokens", result.TotalTokens,
		"finish_reason", result.FinishReason,
	).Debug("agent turn completed")

	return result, nil
}

// ExecuteTurnWithToolLoop runs a full agentic loop (LLM -> tools -> repeat) synchronously
// for a single user message. Unlike ExecuteTurn which makes a single LLM call and ignores
// tool calls, this method executes the same agentic loop used by streaming mode, ensuring
// tool calls are actually executed and results fed back to the LLM.
// Returns a TurnResult with the final assistant output after all tool iterations complete.
func (e *Engine) ExecuteTurnWithToolLoop(ctx context.Context, input *TurnInput) (*TurnResult, error) {
	start := time.Now()

	// Build sampling params from agent config.
	sampling := samplingParamsFromConfig(input.Config)

	agentMode := "primary"
	if mode, ok := stringConfigValue(input.Config, "mode"); ok {
		agentMode = strings.ToLower(strings.TrimSpace(mode))
	}
	taskPermissionMode := "ask"
	if mode, ok := stringConfigValue(input.Config, "task_permission_mode"); ok {
		taskPermissionMode = strings.ToLower(strings.TrimSpace(mode))
	}
	workingDirectory := ""
	if wd, ok := stringConfigValue(input.Config, "working_directory"); ok {
		workingDirectory = strings.TrimSpace(wd)
	}
	var maxSteps int32
	if ms, ok := numericConfigValue(input.Config, "max_steps"); ok && ms > 0 {
		maxSteps = int32(ms)
	}

	// Build LoopInput from TurnInput
	loopInput := &LoopInput{
		TenantID:           "", // Extracted from context by callers
		AgentID:            input.AgentID,
		SessionID:          input.SessionID,
		Model:              input.Model,
		SystemPrompt:       input.SystemPrompt,
		Tools:              input.Tools,
		Sampling:           sampling,
		UserInput:          input.UserInput,
		AgentMode:          agentMode,
		TaskPermissionMode: taskPermissionMode,
		WorkingDirectory:   workingDirectory,
		MaxSteps:           maxSteps,
	}

	// Determine loop limits from agent config.
	// 0 = unlimited tool calls per turn (no artificial cap)
	var maxToolCallsPerTurn int32
	if mtc, ok := numericConfigValue(input.Config, "max_tool_calls_per_turn"); ok && mtc > 0 {
		maxToolCallsPerTurn = int32(mtc)
	}
	maxHistoryMessages := int32(100)
	if mhm, ok := numericConfigValue(input.Config, "max_history_messages"); ok && mhm > 0 {
		maxHistoryMessages = int32(mhm)
	}
	var sessionTokenBudget int64
	if budget, ok := numericConfigValue(input.Config, "token_budget"); ok && budget > 0 {
		sessionTokenBudget = int64(budget)
	}

	maxIterations := int32(25)
	if maxSteps > 0 {
		maxIterations = maxSteps
	}
	loopConfig := LoopConfig{
		MaxIterations:       maxIterations,
		MaxToolCallsPerTurn: maxToolCallsPerTurn,
		MaxHistoryMessages:  maxHistoryMessages,
		EnableStreaming:     false, // Non-streaming mode
		TurnTimeout:         5 * time.Minute,
		SessionTokenBudget:  sessionTokenBudget,
	}

	// Create a no-op emitter (no sinks attached, events are discarded)
	emitter := NewEmitter()
	defer emitter.Close()

	// Create and run the loop
	loop := NewLoop(e, e.toolLoop, emitter, loopConfig)

	// Build initial state with conversation history
	state := &LoopState{}
	if input.SystemPrompt != "" {
		state.Messages = append(state.Messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(input.SystemPrompt)}},
		})
	}
	for _, turn := range input.PreviousTurns {
		if turn.UserInput != "" {
			state.Messages = append(state.Messages, gw.Message{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(turn.UserInput)}},
			})
		}
		if turn.AssistantOutput != "" {
			state.Messages = append(state.Messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(turn.AssistantOutput)}},
			})
		}
	}

	// Loop.Run appends system prompt and user input again, but only if messages is empty
	// for system prompt. We already added it, so clear SystemPrompt from LoopInput to
	// prevent duplication (Loop.Run checks len(state.Messages) == 0 before adding system prompt).
	// User input is always appended by Loop.Run so we leave that in LoopInput.

	finalState, err := loop.Run(ctx, state, loopInput)
	latency := time.Since(start).Milliseconds()

	result := &TurnResult{
		LatencyMs: latency,
		ToolCalls: []byte("[]"),
	}

	if err != nil {
		result.Error = err.Error()
		result.FinishReason = "error"
		return result, nil
	}

	result.AssistantOutput = finalState.LastAssistantText
	result.FinishReason = finalState.FinishReason
	result.PromptTokens = int32(finalState.TurnUsage.PromptTokens)
	result.CompletionTokens = int32(finalState.TurnUsage.CompletionTokens)
	result.TotalTokens = int32(finalState.TurnUsage.TotalTokens)
	result.CacheReadTokens = int32(finalState.TurnUsage.CacheReadTokens)
	result.CacheWriteTokens = int32(finalState.TurnUsage.CacheWriteTokens)

	// Extract tool calls from the final state
	if len(finalState.Messages) > 0 {
		for i := len(finalState.Messages) - 1; i >= 0; i-- {
			if len(finalState.Messages[i].ToolCalls) > 0 {
				if data, jsonErr := json.Marshal(finalState.Messages[i].ToolCalls); jsonErr == nil {
					result.ToolCalls = data
				}
				break
			}
		}
	}

	return result, nil
}

// ResolveProvider resolves a ChatProvider once, to be reused across loop iterations.
func (e *Engine) ResolveProvider(ctx context.Context, model string) (gw.ChatProvider, string, error) {
	_, router, err := e.ProvidersForContext(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve model %s: %w", model, err)
	}
	provider, resolvedModel, err := router.ResolveWithContext(ctx, model)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve model %s: %w", model, err)
	}
	chatProvider, ok := provider.(gw.ChatProvider)
	if !ok {
		return nil, "", fmt.Errorf("provider for model %s does not support chat", model)
	}
	return chatProvider, resolvedModel.ModelName, nil
}

// BuildChatRequest constructs a ChatCompletionRequest from components.
func (e *Engine) BuildChatRequest(model string, messages []gw.Message, tools []gw.ToolDefinition, sampling gw.SamplingParams) gw.ChatCompletionRequest {
	req := gw.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
		Sampling: sampling,
	}
	if len(tools) > 0 {
		req.Tools = tools
	}
	return req
}

func samplingParamsFromConfig(config map[string]interface{}) gw.SamplingParams {
	// Default for agent calls. This avoids truncated tool-call JSON with
	// providers that otherwise choose a smaller output limit.
	sampling := gw.SamplingParams{MaxTokens: 16384}
	if config == nil {
		return sampling
	}
	if temp, ok := numericConfigValue(config, "temperature"); ok {
		sampling.Temperature = temp
	}
	if maxTokens, ok := numericConfigValue(config, "max_tokens"); ok {
		sampling.MaxTokens = int(maxTokens)
	}
	if topP, ok := numericConfigValue(config, "top_p"); ok {
		sampling.TopP = topP
	}
	if effort, ok := stringConfigValue(config, "reasoning_effort"); ok {
		sampling.ReasoningEffort = strings.TrimSpace(effort)
	}
	if budget, ok := numericConfigValue(config, "reasoning_budget_tokens"); ok && budget >= 0 {
		value := int(budget)
		sampling.ReasoningBudget = &value
	}
	if enabled, ok := config["reasoning_enabled"].(bool); ok {
		sampling.ReasoningEnabled = &enabled
	}
	return sampling
}

func numericConfigValue(config map[string]interface{}, key string) (float64, bool) {
	if config == nil {
		return 0, false
	}
	v, ok := config[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

func stringConfigValue(config map[string]interface{}, key string) (string, bool) {
	if config == nil {
		return "", false
	}
	v, ok := config[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// ExecuteTurnStream runs a single LLM call with streaming, emitting text deltas via callback.
// Chunks are emitted immediately while simultaneously accumulated into the full response.
func (e *Engine) ExecuteTurnStream(ctx context.Context, provider gw.ChatProvider, req gw.ChatCompletionRequest, onChunk func(textDelta string) error) (*gw.ChatCompletionResponse, error) {
	var accumulated gw.ChatCompletionResponse
	var textBuilder strings.Builder
	var toolCalls []gw.ToolCall
	var providerParts []gw.ContentPart

	err := provider.ChatStream(ctx, req, func(chunk gw.ChatResponseChunk) error {
		// Accumulate model and ID
		if chunk.Model != "" {
			accumulated.Model = chunk.Model
		}
		if chunk.ID != "" {
			accumulated.ID = chunk.ID
		}

		for _, delta := range chunk.Choices {
			// Accumulate text content
			if delta.Delta.Content != nil {
				text := ""
				for _, part := range delta.Delta.Content {
					if part.Text != nil {
						text += *part.Text
					}
					if part.ProviderJSON != nil {
						native := append(json.RawMessage(nil), (*part.ProviderJSON)...)
						cloned := part
						cloned.ProviderJSON = &native
						providerParts = append(providerParts, cloned)
					}
				}
				if text != "" {
					textBuilder.WriteString(text)
					if err := onChunk(text); err != nil {
						return err
					}
				}
			}

			// Accumulate tool calls by ID matching
			for _, tc := range delta.Delta.ToolCalls {
				if tc.ID != "" {
					// Tool call with ID: find existing or create new
					found := false
					for idx := range toolCalls {
						if toolCalls[idx].ID == tc.ID {
							if tc.Type != "" {
								toolCalls[idx].Type = tc.Type
							}
							toolCalls[idx].Function.Name += tc.Function.Name
							toolCalls[idx].Function.Arguments += tc.Function.Arguments
							found = true
							break
						}
					}
					if !found {
						toolCalls = append(toolCalls, gw.ToolCall{
							ID:   tc.ID,
							Type: tc.Type,
							Function: gw.ToolCallFunction{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						})
					}
				} else if len(toolCalls) > 0 {
					// No ID: append to the most recent tool call (argument streaming)
					last := len(toolCalls) - 1
					toolCalls[last].Function.Name += tc.Function.Name
					toolCalls[last].Function.Arguments += tc.Function.Arguments
				}
			}

			// Capture finish reason
			if delta.FinishReason != "" {
				if len(accumulated.Choices) == 0 {
					accumulated.Choices = append(accumulated.Choices, gw.Choice{})
				}
				accumulated.Choices[0].FinishReason = delta.FinishReason
			}
		}

		// Accumulate usage if present
		if chunk.Usage != nil {
			accumulated.Usage = *chunk.Usage
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Build final response from accumulated data
	if len(accumulated.Choices) == 0 {
		accumulated.Choices = append(accumulated.Choices, gw.Choice{})
	}

	finalText := textBuilder.String()
	if len(providerParts) > 0 {
		accumulated.Choices[0].Message.Content = providerParts
	} else if finalText != "" {
		accumulated.Choices[0].Message.Content = []gw.ContentPart{{
			Type: "text",
			Text: strPtr(finalText),
		}}
	}
	accumulated.Choices[0].Message.Role = gw.RoleAssistant
	if len(toolCalls) > 0 {
		accumulated.Choices[0].Message.ToolCalls = toolCalls
	}

	return &accumulated, nil
}

// GetToolLoop returns the engine's tool loop manager.
func (e *Engine) GetToolLoop() *toolloop.LoopManager {
	return e.toolLoop
}

// GetRegistry returns the engine's gateway registry.
func (e *Engine) GetRegistry() *gw.Registry {
	return e.registry
}

// GetRouter returns the engine's gateway router.
func (e *Engine) GetRouter() *gw.Router {
	return e.router
}

// FallbackConfig holds agent-level model fallback configuration.
// Nested under the existing agent config JSONB column as config.fallback.
type FallbackConfig struct {
	Enabled     bool
	Models      []string // Ordered fallback model names
	MaxAttempts int      // Retries per model (default: 1)
	BackoffMs   int      // Backoff between retries (default: 0)
}

// ParseFallbackConfig extracts fallback configuration from the agent config map.
// Returns nil if fallback is not configured or not enabled.
func ParseFallbackConfig(config map[string]interface{}) *FallbackConfig {
	if config == nil {
		return nil
	}
	fbRaw, ok := config["fallback"]
	if !ok {
		return nil
	}
	fbMap, ok := fbRaw.(map[string]interface{})
	if !ok {
		return nil
	}

	enabled, _ := fbMap["enabled"].(bool)
	if !enabled {
		return nil
	}

	cfg := &FallbackConfig{
		Enabled:     true,
		MaxAttempts: 1,
		BackoffMs:   0,
	}

	if models, ok := fbMap["models"].([]interface{}); ok {
		for _, m := range models {
			if ms, ok := m.(string); ok && ms != "" {
				cfg.Models = append(cfg.Models, ms)
			}
		}
	}
	if len(cfg.Models) == 0 {
		return nil
	}

	if maxAttempts, ok := fbMap["max_attempts"].(float64); ok && maxAttempts > 0 {
		cfg.MaxAttempts = int(maxAttempts)
	}
	if backoffMs, ok := fbMap["backoff_ms"].(float64); ok && backoffMs >= 0 {
		cfg.BackoffMs = int(backoffMs)
	}

	return cfg
}

// ParseAPIType extracts the API type from the agent config map.
// Returns "chat_completions" (default) or "responses".
func ParseAPIType(config map[string]interface{}) string {
	if config == nil {
		return "chat_completions"
	}
	if apiType, ok := config["api_type"].(string); ok {
		if apiType == "responses" {
			return "responses"
		}
	}
	return "chat_completions"
}

// FallbackConfigFromGateway extracts the gateway-level fallback configuration
// from context and converts it to an agent FallbackConfig. Returns nil if no
// gateway config is present or fallback is not enabled.
func FallbackConfigFromGateway(ctx context.Context) *FallbackConfig {
	gwCfg, _ := ctx.Value(contextkeys.GatewayConfig).(*validator.GatewayConfig)
	if gwCfg == nil || !gwCfg.LoadBalancer.Fallback.Enabled {
		return nil
	}

	seen := make(map[string]bool)
	var models []string

	// Default model first (if enabled)
	if gwCfg.LoadBalancer.Fallback.Default.Enabled && gwCfg.LoadBalancer.Fallback.Default.Model != "" {
		m := gwCfg.LoadBalancer.Fallback.Default.Model
		seen[strings.ToLower(m)] = true
		models = append(models, m)
	}

	// Factors sorted by priority ascending (lowest = highest priority)
	factors := make([]validator.FallbackFactorConfig, len(gwCfg.LoadBalancer.Fallback.Factors))
	copy(factors, gwCfg.LoadBalancer.Fallback.Factors)
	sort.Slice(factors, func(i, j int) bool { return factors[i].Priority < factors[j].Priority })

	maxAttempts := 0
	backoffMs := 0
	pickedRetry := false

	for _, f := range factors {
		// Pick retry params from the first factor that defines them
		if !pickedRetry && (f.MaxAttempts > 0 || f.BackoffMs > 0) {
			maxAttempts = f.MaxAttempts
			backoffMs = f.BackoffMs
			pickedRetry = true
		}
		for _, fm := range f.Models {
			if fm.Model == "" {
				continue
			}
			key := strings.ToLower(fm.Model)
			if seen[key] {
				continue
			}
			seen[key] = true
			models = append(models, fm.Model)
		}
	}

	if len(models) == 0 {
		return nil
	}

	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	return &FallbackConfig{
		Enabled:     true,
		Models:      models,
		MaxAttempts: maxAttempts,
		BackoffMs:   backoffMs,
	}
}

func strPtr(s string) *string {
	return &s
}
