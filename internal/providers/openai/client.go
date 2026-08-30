package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	providerutil "github.com/everstacklabs/everstack/internal/providers"
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
)

// Provider implements gw.Provider for OpenAI models.
type Provider struct {
	cfg    Config
	client *http.Client
}

func NewProvider(cfg Config) *Provider {
	cli := cfg.HTTPClient
	if cli == nil {
		cli = clientx.Default()
	}

	// Wrap the HTTP client with instrumented transport for automatic tracing
	if cli.Transport == nil {
		cli.Transport = http.DefaultTransport
	}
	cli.Transport = attrs.NewInstrumentedTransport(cli.Transport, "openai")

	// BaseURL should be provided by bootstrap via provider catalog; do not hardcode here.
	return &Provider{cfg: cfg, client: cli}
}

func (p *Provider) Name() string { return "openai" }

func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "text-embedding-")
	}
	model = strings.ToLower(model)
	for _, m := range p.cfg.SupportedModels {
		if strings.ToLower(m) == model {
			return true
		}
	}
	return false
}

// sanitizeMetadata coerces per-call metadata into OpenAI's contract: string
// keys → string values (max 16 pairs, keys ≤64 chars, values ≤512). Non-string
// values are stringified (JSON for objects/arrays) so a caller can never break
// the request with a nested value — the exact shape OpenAI rejects
// ("expected a string, but got an object"). Returns nil for empty input.
func sanitizeMetadata(m map[string]interface{}) map[string]interface{} {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if len(out) >= 16 {
			break
		}
		if v == nil {
			continue
		}
		if len(k) > 64 {
			k = k[:64]
		}
		var s string
		switch t := v.(type) {
		case string:
			s = t
		case float64, float32, bool, int, int32, int64:
			s = fmt.Sprintf("%v", t)
		default:
			b, _ := json.Marshal(t)
			s = string(b)
		}
		if len(s) > 512 {
			s = s[:512]
		}
		out[k] = s
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// OpenAI Chat API payloads
type oaChatMessage struct {
	Role       string          `json:"role"`
	Content    []oaContentPart `json:"content,omitempty"`
	ToolCalls  []oaToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type oaContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL any    `json:"image_url,omitempty"`
}

type oaToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function oaToolCallFunction `json:"function"`
}

type oaToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaTool struct {
	Type     string        `json:"type"`
	Function oaToolFuncDef `json:"function"`
}

type oaToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type oaChatRequest struct {
	Model               string                 `json:"model"`
	Messages            []oaChatMessage        `json:"messages"`
	Tools               []oaTool               `json:"tools,omitempty"`
	ToolChoice          interface{}            `json:"tool_choice,omitempty"`
	Temperature         *float64               `json:"temperature,omitempty"`
	TopP                *float64               `json:"top_p,omitempty"`
	MaxTokens           int                    `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
	Stop                []string               `json:"stop,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	StreamOptions       *oaStreamOptions       `json:"stream_options,omitempty"`
	Store               bool                   `json:"store,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	ReasoningEffort     string                 `json:"reasoning_effort,omitempty"`
	FrequencyPenalty    *float64               `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64               `json:"presence_penalty,omitempty"`
	Seed                *int64                 `json:"seed,omitempty"`
	Verbosity           string                 `json:"verbosity,omitempty"`
}

type oaStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// oaStreamToolCall represents a tool call delta in a streaming response.
// Unlike oaToolCall, it includes an Index field used by OpenAI to correlate
// argument-continuation chunks with their originating tool call.
type oaStreamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function oaToolCallFunction `json:"function"`
}

type oaChatChoice struct {
	Index   int `json:"index"`
	Message struct {
		Role      string       `json:"role"`
		Content   interface{}  `json:"content"` // string or []parts or null
		ToolCalls []oaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

// oaTokenDetails represents OpenAI's detailed token breakdown
type oaTokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
	TextTokens      int `json:"text_tokens,omitempty"`
}

type oaChatResponse struct {
	ID      string         `json:"id"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []oaChatChoice `json:"choices"`
	Usage   struct {
		PromptTokens            int             `json:"prompt_tokens"`
		CompletionTokens        int             `json:"completion_tokens"`
		TotalTokens             int             `json:"total_tokens"`
		PromptTokensDetails     *oaTokenDetails `json:"prompt_tokens_details,omitempty"`
		CompletionTokensDetails *oaTokenDetails `json:"completion_tokens_details,omitempty"`
	} `json:"usage"`
}

// Responses API types (for /v1/responses endpoint used by codex models)

// oaResponsesInputItem represents a message in the Responses API input array.
// Supports type="message", type="function_call", and type="function_call_output".
type oaResponsesInputItem struct {
	Role      string      `json:"role,omitempty"`
	Content   interface{} `json:"content,omitempty"` // string or []oaResponsesContentPart
	Type      string      `json:"type,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	Name      string      `json:"name,omitempty"`      // function_call: tool name
	Arguments string      `json:"arguments,omitempty"` // function_call: JSON arguments
	Output    string      `json:"output,omitempty"`
}

// oaResponsesContentPart represents a content part in the Responses API.
type oaResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// oaResponsesTool represents a tool definition for the Responses API.
type oaResponsesTool struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type oaResponsesRequest struct {
	Model           string                 `json:"model"`
	Input           interface{}            `json:"input"`                  // string or []oaResponsesInputItem
	Instructions    string                 `json:"instructions,omitempty"` // system prompt
	MaxOutputTokens int                    `json:"max_output_tokens,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	TopP            *float64               `json:"top_p,omitempty"`
	Tools           []oaResponsesTool      `json:"tools,omitempty"`
	Stream          bool                   `json:"stream,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Reasoning       *oaReasoningConfig     `json:"reasoning,omitempty"`
	Text            *oaResponsesTextConfig `json:"text,omitempty"`
}

// oaResponsesTextConfig carries the Responses API's text options. Verbosity
// lives here rather than at the top level, unlike Chat Completions.
type oaResponsesTextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

func responsesTextConfig(sampling gw.SamplingParams) *oaResponsesTextConfig {
	if sampling.Verbosity == "" {
		return nil
	}
	return &oaResponsesTextConfig{Verbosity: sampling.Verbosity}
}

type oaReasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

// oaResponsesOutputContent represents a content item within a Responses API output message.
type oaResponsesOutputContent struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

// oaResponsesOutputItem represents an item in the Responses API output array.
type oaResponsesOutputItem struct {
	ID      string                     `json:"id"`
	Type    string                     `json:"type"` // "message", "function_call"
	Role    string                     `json:"role,omitempty"`
	Content []oaResponsesOutputContent `json:"content,omitempty"`
	// Function call fields
	Name      string `json:"name,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaResponsesUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	} `json:"output_tokens_details,omitempty"`
}

type oaResponsesResponse struct {
	ID         string                  `json:"id"`
	CreatedAt  float64                 `json:"created_at"`
	Model      string                  `json:"model"`
	Status     string                  `json:"status"` // "completed", "failed", "incomplete"
	Output     []oaResponsesOutputItem `json:"output"`
	OutputText string                  `json:"output_text"`
	Usage      oaResponsesUsage        `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// isResponsesModel returns true if the model should use /v1/responses.
func (p *Provider) isResponsesModel(model string) bool {
	m := strings.ToLower(model)
	// Check explicit config list first
	for _, cm := range p.cfg.ResponsesModels {
		if strings.ToLower(cm) == m {
			return true
		}
	}
	// Known responses-only model patterns: codex models
	if strings.Contains(m, "-codex") {
		return true
	}
	return false
}

func optionalTemperature(sampling gw.SamplingParams) *float64 {
	if !sampling.TemperatureConfigured && sampling.Temperature == 0 {
		return nil
	}
	value := sampling.Temperature
	return &value
}

func optionalTopP(sampling gw.SamplingParams) *float64 {
	if !sampling.TopPConfigured && sampling.TopP == 0 {
		return nil
	}
	value := sampling.TopP
	return &value
}

func optionalFrequencyPenalty(sampling gw.SamplingParams) *float64 {
	if !sampling.FrequencyConfigured && sampling.FrequencyPenalty == 0 {
		return nil
	}
	value := sampling.FrequencyPenalty
	return &value
}

func optionalPresencePenalty(sampling gw.SamplingParams) *float64 {
	if !sampling.PresenceConfigured && sampling.PresencePenalty == 0 {
		return nil
	}
	value := sampling.PresencePenalty
	return &value
}

// messagesToResponsesInput converts gateway messages to Responses API input format.
// It extracts system messages as instructions and returns the remaining messages as input items.
func messagesToResponsesInput(messages []gw.Message) (instructions string, input []oaResponsesInputItem) {
	validFunctionCalls := make(map[string]struct{})

	for _, msg := range messages {
		if msg.Role == gw.RoleSystem {
			// Extract system messages as instructions
			for _, part := range msg.Content {
				if part.Type == "text" && part.Text != nil {
					if instructions != "" {
						instructions += "\n"
					}
					instructions += *part.Text
				}
			}
			continue
		}

		// OpenAI Responses API does not accept role=tool messages.
		// Tool outputs must be encoded as function_call_output items.
		if msg.Role == gw.RoleTool || msg.Role == gw.RoleFunction {
			toolOutput := messageTextForResponses(msg.Content)
			if msg.ToolCallID != "" {
				if _, ok := validFunctionCalls[msg.ToolCallID]; !ok {
					// Drop orphaned function_call_output entries to avoid invalid
					// Responses API payloads ("missing input[n].name").
					logger.WithFields("tool_call_id", msg.ToolCallID).
						Warn("responses input: skipping orphaned function_call_output without prior function_call")
					continue
				}
				input = append(input, oaResponsesInputItem{
					Type:   "function_call_output",
					CallID: msg.ToolCallID,
					Output: toolOutput,
				})
			} else {
				// Fallback for malformed history: keep payload valid even when
				// tool_call_id is missing.
				input = append(input, oaResponsesInputItem{
					Role:    string(gw.RoleUser),
					Type:    "message",
					Content: toolOutput,
				})
			}
			continue
		}

		item := oaResponsesInputItem{
			Role: string(msg.Role),
			Type: "message",
			// Responses API rejects null content; keep a valid empty string when
			// a message contains no text parts (e.g. assistant tool-call turns).
			Content: "",
		}

		// Build content parts
		var parts []oaResponsesContentPart
		for _, part := range msg.Content {
			if part.Type == "text" && part.Text != nil {
				parts = append(parts, oaResponsesContentPart{
					Type: "input_text",
					Text: *part.Text,
				})
			}
		}

		if len(parts) == 1 {
			// Single text part — use string shorthand
			item.Content = parts[0].Text
		} else if len(parts) > 1 {
			item.Content = parts
		}

		input = append(input, item)

		// Emit function_call items for assistant messages with tool calls.
		// The Responses API requires these so that subsequent function_call_output
		// items can reference the call_id.
		if msg.Role == gw.RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				name := strings.TrimSpace(tc.Function.Name)
				if callID == "" || name == "" {
					logger.WithFields("call_id", tc.ID, "tool_name", tc.Function.Name).
						Warn("responses input: skipping malformed function_call missing id or name")
					continue
				}
				validFunctionCalls[callID] = struct{}{}
				input = append(input, oaResponsesInputItem{
					Type:      "function_call",
					CallID:    callID,
					Name:      name,
					Arguments: tc.Function.Arguments,
				})
			}
		}
	}
	return instructions, input
}

func messageTextForResponses(parts []gw.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	var chunks []string
	for _, part := range parts {
		if part.Type == "text" && part.Text != nil {
			chunks = append(chunks, *part.Text)
		}
	}
	return strings.Join(chunks, "\n")
}

func toOAChatRequest(req gw.ChatCompletionRequest) oaChatRequest {
	msgs := make([]oaChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := oaChatMessage{Role: string(m.Role)}

		// Convert content parts
		if len(m.Content) > 0 {
			parts := make([]oaContentPart, 0, len(m.Content))
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					if c.Text != nil {
						parts = append(parts, oaContentPart{Type: "text", Text: *c.Text})
					}
				case "image_url":
					if c.ImageURL != nil {
						parts = append(parts, oaContentPart{Type: "image_url", ImageURL: map[string]string{"url": *c.ImageURL}})
					}
				}
			}
			msg.Content = parts
		}

		// Convert tool calls (from assistant messages)
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, oaToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: oaToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		// Convert tool_call_id (for tool response messages)
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}

		msgs = append(msgs, msg)
	}

	// Set store=true if metadata is provided (required by OpenAI API)
	store := len(req.Metadata) > 0

	// Determine whether to use max_tokens or max_completion_tokens
	maxTokens := req.Sampling.MaxTokens
	maxCompletionTokens := req.Sampling.MaxCompletionTokens

	// Models that require max_completion_tokens instead of max_tokens
	// o1 models, o3-mini models, and gpt-5 models
	requiresMaxCompletionTokens := strings.HasPrefix(req.Model, "o1") ||
		strings.HasPrefix(req.Model, "o3-mini") ||
		strings.HasPrefix(req.Model, "gpt-5")

	if requiresMaxCompletionTokens {
		// If max_completion_tokens is not set but max_tokens is, use max_tokens as max_completion_tokens
		if maxCompletionTokens == 0 && maxTokens > 0 {
			maxCompletionTokens = maxTokens
		}
		// Ensure max_tokens is not sent for these models
		maxTokens = 0
	}

	oaReq := oaChatRequest{
		Model:               req.Model,
		Messages:            msgs,
		Temperature:         optionalTemperature(req.Sampling),
		TopP:                optionalTopP(req.Sampling),
		MaxTokens:           maxTokens,
		MaxCompletionTokens: maxCompletionTokens,
		Stop:                req.Sampling.Stop,
		Stream:              req.Stream,
		Store:               store,
		Metadata:            sanitizeMetadata(req.Metadata),
		ReasoningEffort:     req.Sampling.ReasoningEffort,
		FrequencyPenalty:    optionalFrequencyPenalty(req.Sampling),
		PresencePenalty:     optionalPresencePenalty(req.Sampling),
		Seed:                req.Sampling.Seed,
		Verbosity:           req.Sampling.Verbosity,
	}

	// Convert tools
	for _, t := range req.Tools {
		oaReq.Tools = append(oaReq.Tools, oaTool{
			Type: t.Type,
			Function: oaToolFuncDef{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		oaReq.ToolChoice = req.ToolChoice
	}

	return oaReq
}

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return gw.ChatCompletionResponse{}, fmt.Errorf("openai api key not provided")
	}
	if req.Stream {
		return gw.ChatCompletionResponse{}, gw.ErrNotImplemented("chat_stream_unary")
	}

	// Route Responses API models or explicit api_type=responses to /v1/responses
	if req.UseResponsesAPI || p.isResponsesModel(req.Model) {
		return p.chatViaResponses(ctx, req)
	}

	payload := toOAChatRequest(req)
	buf, _ := json.Marshal(payload)

	// Apply a per-request timeout for the unary call to avoid long hangs
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	// Observability: log outgoing provider request
	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "openai",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", false,
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	// Capture and monitor rate limit information
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "openai", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, errors.New("openai chat error: " + string(b))
	}
	var oa oaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&oa); err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	return p.toGatewayChatResponse(oa), nil
}

// chatViaResponses handles unary requests for models that use /v1/responses (e.g. codex).
func (p *Provider) chatViaResponses(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	instructions, input := messagesToResponsesInput(req.Messages)

	payload := oaResponsesRequest{
		Model:        req.Model,
		Input:        input,
		Instructions: instructions,
		Temperature:  optionalTemperature(req.Sampling),
		TopP:         optionalTopP(req.Sampling),
		Text:         responsesTextConfig(req.Sampling),
		Metadata:     sanitizeMetadata(req.Metadata),
	}
	// Determine max output tokens
	if req.Sampling.MaxCompletionTokens > 0 {
		payload.MaxOutputTokens = req.Sampling.MaxCompletionTokens
	} else if req.Sampling.MaxTokens > 0 {
		payload.MaxOutputTokens = req.Sampling.MaxTokens
	}
	if req.Sampling.ReasoningEffort != "" {
		payload.Reasoning = &oaReasoningConfig{Effort: req.Sampling.ReasoningEffort}
	}
	// Convert tools
	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, oaResponsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	buf, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	endpoint := p.cfg.BaseURL + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "openai",
		"endpoint", endpoint,
		"model", req.Model,
		"stream", false,
		"correlation_id", cid,
	).Info("provider request issued (responses)")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "openai", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, errors.New("openai responses error: " + string(b))
	}

	var oa oaResponsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&oa); err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	return p.responsesToGatewayResponse(oa), nil
}

// responsesToGatewayResponse converts a Responses API response to the gateway format.
func (p *Provider) responsesToGatewayResponse(oa oaResponsesResponse) gw.ChatCompletionResponse {
	// Extract text content from output items
	var parts []gw.ContentPart
	var toolCalls []gw.ToolCall

	for _, item := range oa.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					parts = append(parts, gw.Text(content.Text))
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, gw.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: gw.ToolCallFunction{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	// If no parts from output items, fall back to output_text
	if len(parts) == 0 && oa.OutputText != "" {
		parts = append(parts, gw.Text(oa.OutputText))
	}

	finishReason := "stop"
	if oa.Status == "incomplete" {
		finishReason = "length"
	} else if oa.Status == "failed" {
		finishReason = "error"
	} else if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	choices := []gw.Choice{{
		Index:        0,
		Message:      gw.Message{Role: gw.RoleAssistant, Content: parts, ToolCalls: toolCalls},
		FinishReason: finishReason,
	}}

	reasoningTokens := 0
	if oa.Usage.OutputTokensDetails != nil {
		reasoningTokens = oa.Usage.OutputTokensDetails.ReasoningTokens
	}

	usage := gw.Usage{
		PromptTokens:     oa.Usage.InputTokens,
		CompletionTokens: oa.Usage.OutputTokens,
		TotalTokens:      oa.Usage.TotalTokens,
	}
	if reasoningTokens > 0 {
		usage.CompletionDetails = &gw.TokenDetails{
			ReasoningTokens: reasoningTokens,
		}
	}

	return gw.ChatCompletionResponse{
		ID:      oa.ID,
		Created: time.Unix(int64(oa.CreatedAt), 0).UTC(),
		Model:   oa.Model,
		Choices: choices,
		Usage:   usage,
	}
}

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return fmt.Errorf("openai api key not provided")
	}

	// Route Responses API models or explicit api_type=responses to /v1/responses streaming
	if req.UseResponsesAPI || p.isResponsesModel(req.Model) {
		return p.chatStreamViaResponses(ctx, req, onChunk)
	}

	payload := toOAChatRequest(req)
	payload.Stream = true
	payload.StreamOptions = &oaStreamOptions{IncludeUsage: true}
	buf, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Capture and monitor rate limit information for streaming
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "openai", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("openai chat stream error: " + string(b))
	}
	// OpenAI streams SSE events prefixed by "data: ", ending with [DONE].
	// Use bufio.Reader (same as Anthropic/Cohere) for correct buffering across reads.
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "event:") {
				// Empty line (event separator) or event label — skip.
				if err != nil {
					break
				}
				continue
			}
			if !strings.HasPrefix(s, "data:") {
				if err != nil {
					break
				}
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
			if data == "[DONE]" {
				return nil
			}

			var partial struct {
				ID      string `json:"id"`
				Created int64  `json:"created"`
				Model   string `json:"model"`
				Choices []struct {
					Index int `json:"index"`
					Delta struct {
						Content   interface{}        `json:"content"`
						ToolCalls []oaStreamToolCall `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens            int             `json:"prompt_tokens"`
					CompletionTokens        int             `json:"completion_tokens"`
					TotalTokens             int             `json:"total_tokens"`
					PromptTokensDetails     *oaTokenDetails `json:"prompt_tokens_details,omitempty"`
					CompletionTokensDetails *oaTokenDetails `json:"completion_tokens_details,omitempty"`
				} `json:"usage,omitempty"`
			}
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				// Skip malformed chunks (matches Anthropic/Cohere behavior).
				if err != nil {
					break
				}
				continue
			}
			chunk := gw.ChatResponseChunk{ID: partial.ID, Created: time.Unix(partial.Created, 0).UTC(), Model: partial.Model}

			// Extract usage if present (typically in final chunk)
			if partial.Usage != nil {
				chunk.Usage = &gw.Usage{
					PromptTokens:     partial.Usage.PromptTokens,
					CompletionTokens: partial.Usage.CompletionTokens,
					TotalTokens:      partial.Usage.TotalTokens,
				}
				if partial.Usage.PromptTokensDetails != nil {
					chunk.Usage.PromptDetails = &gw.TokenDetails{
						CachedTokens:    partial.Usage.PromptTokensDetails.CachedTokens,
						CacheReadTokens: partial.Usage.PromptTokensDetails.CachedTokens,
						ReasoningTokens: partial.Usage.PromptTokensDetails.ReasoningTokens,
						AudioTokens:     partial.Usage.PromptTokensDetails.AudioTokens,
						TextTokens:      partial.Usage.PromptTokensDetails.TextTokens,
					}
				}
				if partial.Usage.CompletionTokensDetails != nil {
					chunk.Usage.CompletionDetails = &gw.TokenDetails{
						CachedTokens:    partial.Usage.CompletionTokensDetails.CachedTokens,
						ReasoningTokens: partial.Usage.CompletionTokensDetails.ReasoningTokens,
						AudioTokens:     partial.Usage.CompletionTokensDetails.AudioTokens,
						TextTokens:      partial.Usage.CompletionTokensDetails.TextTokens,
					}
				}
			}

			for _, ch := range partial.Choices {
				var parts []gw.ContentPart
				switch v := ch.Delta.Content.(type) {
				case string:
					if v != "" {
						parts = append(parts, gw.Text(v))
					}
				case []interface{}:
					for _, pv := range v {
						if pm, ok := pv.(map[string]interface{}); ok {
							if t, ok := pm["type"].(string); ok && t == "text" {
								if txt, ok := pm["text"].(string); ok {
									parts = append(parts, gw.Text(txt))
								}
							}
						}
					}
				}
				// Convert streaming tool call deltas to gateway ToolCall
				var toolCalls []gw.ToolCall
				for _, tc := range ch.Delta.ToolCalls {
					toolCalls = append(toolCalls, gw.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: gw.ToolCallFunction{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				}
				chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
					Index: ch.Index,
					Delta: gw.Message{
						Role:      gw.RoleAssistant,
						Content:   parts,
						ToolCalls: toolCalls,
					},
					FinishReason: ch.FinishReason,
				})
			}
			if err := onChunk(chunk); err != nil {
				return err
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
	return nil
}

// chatStreamViaResponses handles streaming requests for models that use /v1/responses.
// The Responses API uses typed SSE events instead of the chat completions data-only format.
func (p *Provider) chatStreamViaResponses(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	instructions, input := messagesToResponsesInput(req.Messages)

	payload := oaResponsesRequest{
		Model:        req.Model,
		Input:        input,
		Instructions: instructions,
		Temperature:  optionalTemperature(req.Sampling),
		TopP:         optionalTopP(req.Sampling),
		Text:         responsesTextConfig(req.Sampling),
		Stream:       true,
		Metadata:     sanitizeMetadata(req.Metadata),
	}
	if req.Sampling.MaxCompletionTokens > 0 {
		payload.MaxOutputTokens = req.Sampling.MaxCompletionTokens
	} else if req.Sampling.MaxTokens > 0 {
		payload.MaxOutputTokens = req.Sampling.MaxTokens
	}
	if req.Sampling.ReasoningEffort != "" {
		payload.Reasoning = &oaReasoningConfig{Effort: req.Sampling.ReasoningEffort}
	}
	for _, t := range req.Tools {
		payload.Tools = append(payload.Tools, oaResponsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	buf, _ := json.Marshal(payload)

	endpoint := p.cfg.BaseURL + "/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "openai", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("openai responses stream error: " + string(b))
	}

	// Bound the SSE read loop with an idle timeout. If no bytes arrive
	// for streamIdleTimeout we close the body to unblock the blocking
	// ReadString call and return an error. Without this, a hung
	// /v1/responses connection prevents LoggingMiddleware from emitting
	// provider.response.received and the tracing middleware's defer
	// span.End() from firing — leaving dashboard rows stuck on PENDING
	// and the provider span missing from traces.
	const streamIdleTimeout = 60 * time.Second
	idleTimer := time.AfterFunc(streamIdleTimeout, func() {
		_ = resp.Body.Close()
	})
	defer idleTimer.Stop()

	reader := bufio.NewReader(resp.Body)
	var responseID string
	var responseModel string
	var emittedTextDelta bool
	completedReceived := false

	toolCallsByID := make(map[string]*gw.ToolCall)
	toolCallOrder := make([]string, 0, 4)
	toolCallAliases := make(map[string]string)

	resolveToolCallID := func(id string) string {
		key := strings.TrimSpace(id)
		for key != "" {
			next, ok := toolCallAliases[key]
			if !ok || next == "" || next == key {
				break
			}
			key = next
		}
		return key
	}

	ensureToolCall := func(id string) *gw.ToolCall {
		key := resolveToolCallID(id)
		if key == "" {
			return nil
		}
		if existing, ok := toolCallsByID[key]; ok {
			return existing
		}
		tc := &gw.ToolCall{
			ID:   key,
			Type: "function",
		}
		toolCallsByID[key] = tc
		toolCallOrder = append(toolCallOrder, key)
		return tc
	}

	mergeToolCallAliases := func(fromID string, toID string) {
		from := resolveToolCallID(fromID)
		to := resolveToolCallID(toID)
		if from == "" || to == "" || from == to {
			return
		}

		toolCallAliases[from] = to
		fromCall := toolCallsByID[from]
		toCall := ensureToolCall(to)
		if fromCall != nil && toCall != nil {
			if toCall.Function.Name == "" && fromCall.Function.Name != "" {
				toCall.Function.Name = fromCall.Function.Name
			}
			if fromCall.Function.Arguments != "" &&
				(toCall.Function.Arguments == "" || len(fromCall.Function.Arguments) > len(toCall.Function.Arguments)) {
				toCall.Function.Arguments = fromCall.Function.Arguments
			}
			delete(toolCallsByID, from)
		}

		seen := make(map[string]struct{}, len(toolCallOrder))
		deduped := make([]string, 0, len(toolCallOrder))
		for _, id := range toolCallOrder {
			canonical := resolveToolCallID(id)
			if canonical == "" {
				continue
			}
			if _, ok := seen[canonical]; ok {
				continue
			}
			seen[canonical] = struct{}{}
			deduped = append(deduped, canonical)
		}
		toolCallOrder = deduped
	}

	emitTextChunk := func(text string) error {
		if text == "" {
			return nil
		}
		chunk := gw.ChatResponseChunk{
			ID:    responseID,
			Model: responseModel,
			Choices: []gw.ChoiceDelta{{
				Index: 0,
				Delta: gw.Message{
					Role:    gw.RoleAssistant,
					Content: []gw.ContentPart{gw.Text(text)},
				},
			}},
		}
		return onChunk(chunk)
	}

	emitToolCallsChunk := func(calls []gw.ToolCall) error {
		if len(calls) == 0 {
			return nil
		}
		chunk := gw.ChatResponseChunk{
			ID:    responseID,
			Model: responseModel,
			Choices: []gw.ChoiceDelta{{
				Index: 0,
				Delta: gw.Message{
					Role:      gw.RoleAssistant,
					ToolCalls: calls,
				},
			}},
		}
		return onChunk(chunk)
	}

	emitCompletionChunk := func(finishReason string, usage *gw.Usage) error {
		if finishReason == "" {
			finishReason = "stop"
		}
		chunk := gw.ChatResponseChunk{
			ID:    responseID,
			Model: responseModel,
			Choices: []gw.ChoiceDelta{{
				Index:        0,
				FinishReason: finishReason,
				Delta: gw.Message{
					Role: gw.RoleAssistant,
				},
			}},
		}
		if usage != nil {
			u := *usage
			chunk.Usage = &u
		}
		return onChunk(chunk)
	}

	messageText := func(msg gw.Message) string {
		var b strings.Builder
		for _, part := range msg.Content {
			if part.Text != nil {
				b.WriteString(*part.Text)
			}
		}
		return b.String()
	}

	mergeToolCalls := func(finalCalls []gw.ToolCall) []gw.ToolCall {
		mergedByID := make(map[string]gw.ToolCall, len(finalCalls)+len(toolCallsByID))
		order := make([]string, 0, len(finalCalls)+len(toolCallsByID))
		seenOrder := make(map[string]struct{}, len(finalCalls)+len(toolCallsByID))
		noIDCalls := make([]gw.ToolCall, 0, 1)

		for _, tc := range finalCalls {
			tc.ID = strings.TrimSpace(tc.ID)
			tc.Function.Name = strings.TrimSpace(tc.Function.Name)
			if tc.ID == "" {
				noIDCalls = append(noIDCalls, tc)
				continue
			}
			if tc.Function.Name == "" {
				logger.WithFields("call_id", tc.ID).Warn("responses stream: dropping final tool call with empty name")
				continue
			}
			mergedByID[tc.ID] = tc
			if _, seen := seenOrder[tc.ID]; !seen {
				order = append(order, tc.ID)
				seenOrder[tc.ID] = struct{}{}
			}
		}

		for _, id := range toolCallOrder {
			canonicalID := resolveToolCallID(id)
			streamed := toolCallsByID[canonicalID]
			if streamed == nil {
				continue
			}

			current, exists := mergedByID[canonicalID]
			if !exists {
				if strings.TrimSpace(streamed.Function.Name) == "" {
					logger.WithFields("call_id", canonicalID).
						Warn("responses stream: dropping streamed tool call with empty name")
					continue
				}
				mergedByID[canonicalID] = *streamed
				if _, seen := seenOrder[canonicalID]; !seen {
					order = append(order, canonicalID)
					seenOrder[canonicalID] = struct{}{}
				}
				continue
			}

			if current.Type == "" {
				current.Type = streamed.Type
			}
			if current.Function.Name == "" {
				current.Function.Name = streamed.Function.Name
			}
			if streamed.Function.Arguments != "" {
				if current.Function.Arguments == "" || len(streamed.Function.Arguments) > len(current.Function.Arguments) {
					current.Function.Arguments = streamed.Function.Arguments
				}
			}
			if strings.TrimSpace(current.Function.Name) == "" {
				logger.WithFields("call_id", canonicalID).
					Warn("responses stream: dropping merged tool call with empty name")
				delete(mergedByID, canonicalID)
				continue
			}
			mergedByID[canonicalID] = current
		}

		merged := make([]gw.ToolCall, 0, len(order)+len(noIDCalls))
		for _, id := range order {
			if tc, ok := mergedByID[id]; ok {
				merged = append(merged, tc)
			}
		}
		merged = append(merged, noIDCalls...)
		return merged
	}

	finalizeFromCompleted := func(completed *oaResponsesResponse) error {
		finalText := ""
		finalToolCalls := []gw.ToolCall{}
		finishReason := "stop"
		var usage *gw.Usage

		if completed != nil {
			resp := p.responsesToGatewayResponse(*completed)
			if responseID == "" {
				responseID = resp.ID
			}
			if responseModel == "" {
				responseModel = resp.Model
			}
			if len(resp.Choices) > 0 {
				finalText = messageText(resp.Choices[0].Message)
				finalToolCalls = resp.Choices[0].Message.ToolCalls
				if resp.Choices[0].FinishReason != "" {
					finishReason = resp.Choices[0].FinishReason
				}
			}
			u := resp.Usage
			usage = &u
		}

		mergedToolCalls := mergeToolCalls(finalToolCalls)
		if len(mergedToolCalls) > 0 && finishReason == "stop" {
			finishReason = "tool_calls"
		}

		if !emittedTextDelta && finalText != "" {
			if err := emitTextChunk(finalText); err != nil {
				return err
			}
			emittedTextDelta = true
		}

		if err := emitToolCallsChunk(mergedToolCalls); err != nil {
			return err
		}
		return emitCompletionChunk(finishReason, usage)
	}

	processEvent := func(eventName string, data string) (bool, error) {
		switch eventName {
		case "response.created", "response.in_progress":
			var envelope struct {
				ID       string `json:"id"`
				Model    string `json:"model"`
				Response *struct {
					ID    string `json:"id"`
					Model string `json:"model"`
				} `json:"response,omitempty"`
			}
			if err := json.Unmarshal([]byte(data), &envelope); err == nil {
				if envelope.ID != "" {
					responseID = envelope.ID
				}
				if envelope.Model != "" {
					responseModel = envelope.Model
				}
				if envelope.Response != nil {
					if envelope.Response.ID != "" {
						responseID = envelope.Response.ID
					}
					if envelope.Response.Model != "" {
						responseModel = envelope.Response.Model
					}
				}
			}
			return false, nil

		case "response.output_text.delta":
			var delta struct {
				Delta string `json:"delta"`
				Text  string `json:"text"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				return false, nil
			}
			text := delta.Delta
			if text == "" {
				text = delta.Text
			}
			if text == "" {
				return false, nil
			}
			emittedTextDelta = true
			return false, emitTextChunk(text)

		case "response.output_text.done":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return false, nil
			}
			if payload.Text == "" || emittedTextDelta {
				return false, nil
			}
			emittedTextDelta = true
			return false, emitTextChunk(payload.Text)

		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			var payload struct {
				ItemID         string `json:"item_id"`
				CallID         string `json:"call_id"`
				Name           string `json:"name"`
				FunctionName   string `json:"function_name"`
				Delta          string `json:"delta"`
				Arguments      string `json:"arguments"`
				ArgumentsDelta string `json:"arguments_delta"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return false, nil
			}
			itemID := strings.TrimSpace(payload.ItemID)
			callID := strings.TrimSpace(payload.CallID)
			if itemID != "" && callID != "" && itemID != callID {
				mergeToolCallAliases(itemID, callID)
			}
			targetID := callID
			if targetID == "" {
				targetID = itemID
			}
			tc := ensureToolCall(targetID)
			if tc == nil {
				return false, nil
			}
			name := strings.TrimSpace(payload.Name)
			if name == "" {
				name = strings.TrimSpace(payload.FunctionName)
			}
			if name != "" {
				tc.Function.Name = name
			}
			args := payload.Delta
			if args == "" {
				args = payload.ArgumentsDelta
			}
			if args != "" {
				tc.Function.Arguments += args
			}
			if payload.Arguments != "" {
				if tc.Function.Arguments == "" || len(payload.Arguments) >= len(tc.Function.Arguments) {
					tc.Function.Arguments = payload.Arguments
				}
			}
			return false, nil

		case "response.output_item.added", "response.output_item.done":
			var wrapped struct {
				Item oaResponsesOutputItem `json:"item"`
			}
			item := oaResponsesOutputItem{}
			if err := json.Unmarshal([]byte(data), &wrapped); err == nil && wrapped.Item.Type != "" {
				item = wrapped.Item
			} else if err := json.Unmarshal([]byte(data), &item); err != nil || item.Type == "" {
				return false, nil
			}
			if item.Type != "function_call" {
				return false, nil
			}
			itemID := strings.TrimSpace(item.ID)
			callID := strings.TrimSpace(item.CallID)
			if itemID != "" && callID != "" && itemID != callID {
				mergeToolCallAliases(itemID, callID)
			}
			if callID == "" {
				callID = itemID
			}
			tc := ensureToolCall(callID)
			if tc == nil {
				return false, nil
			}
			if item.Name != "" {
				tc.Function.Name = item.Name
			}
			if item.Arguments != "" {
				if tc.Function.Arguments == "" || len(item.Arguments) >= len(tc.Function.Arguments) {
					tc.Function.Arguments = item.Arguments
				}
			}
			return false, nil

		case "response.completed":
			var completed oaResponsesResponse
			if err := json.Unmarshal([]byte(data), &completed); err != nil || (completed.ID == "" && completed.Status == "" && len(completed.Output) == 0 && completed.OutputText == "") {
				var wrapped struct {
					Response oaResponsesResponse `json:"response"`
				}
				if werr := json.Unmarshal([]byte(data), &wrapped); werr != nil || (wrapped.Response.ID == "" && wrapped.Response.Status == "" && len(wrapped.Response.Output) == 0 && wrapped.Response.OutputText == "") {
					return false, nil
				}
				completed = wrapped.Response
			}
			if completed.ID == "" {
				completed.ID = responseID
			}
			if completed.Model == "" {
				completed.Model = responseModel
			}
			completedReceived = true
			return true, finalizeFromCompleted(&completed)

		case "response.failed":
			var failed struct {
				Error *struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
				Response *struct {
					Error *struct {
						Message string `json:"message"`
						Type    string `json:"type"`
					} `json:"error"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &failed); err == nil {
				errObj := failed.Error
				if errObj == nil && failed.Response != nil {
					errObj = failed.Response.Error
				}
				if errObj != nil {
					return false, fmt.Errorf("openai responses error: %s: %s", errObj.Type, errObj.Message)
				}
			}
			return false, errors.New("openai responses stream failed")

		case "error":
			return false, fmt.Errorf("openai responses stream error: %s", data)

		default:
			logger.WithFields("event", eventName).Debug("openai responses stream: unhandled event")
			return false, nil
		}
	}

	var currentEvent string
	dataLines := make([]string, 0, 2)
	flushEvent := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		return processEvent(currentEvent, data)
	}

	for {
		line, err := reader.ReadString('\n')
		// Reset idle deadline as soon as any data flows. Even SSE comment
		// keepalives count as "still alive."
		if len(line) > 0 {
			idleTimer.Reset(streamIdleTimeout)
		}
		if len(line) > 0 {
			s := strings.TrimRight(line, "\r\n")

			if strings.HasPrefix(s, ":") {
				// SSE comment line.
			} else if s == "" {
				done, processErr := flushEvent()
				if processErr != nil {
					return processErr
				}
				if done {
					return nil
				}
			} else if strings.HasPrefix(s, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(s, "event:"))
			} else if strings.HasPrefix(s, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
				if data != "" && data != "[DONE]" {
					dataLines = append(dataLines, data)
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				done, processErr := flushEvent()
				if processErr != nil {
					return processErr
				}
				if done {
					return nil
				}
				if !completedReceived {
					return finalizeFromCompleted(nil)
				}
				return nil
			}
			return err
		}
	}
}

func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return gw.EmbeddingsResponse{}, fmt.Errorf("openai api key not provided")
	}
	payload := struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{Model: req.Model, Input: req.Input}
	buf, _ := json.Marshal(payload)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/embeddings", bytes.NewReader(buf))
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	p.setHeaders(hreq.Header)
	resp, err := p.client.Do(hreq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.EmbeddingsResponse{}, errors.New("openai embeddings error: " + string(b))
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	if len(parsed.Data) == 0 {
		return gw.EmbeddingsResponse{}, errors.New("no embedding returned")
	}
	return gw.EmbeddingsResponse{Embedding: parsed.Data[0].Embedding}, nil
}

func (p *Provider) setHeaders(h http.Header) {
	h.Set("Authorization", "Bearer "+p.cfg.APIKey)
	if p.cfg.Organization != "" {
		h.Set("OpenAI-Organization", p.cfg.Organization)
	}
	if p.cfg.Project != "" {
		h.Set("OpenAI-Project", p.cfg.Project)
	}
	h.Set("Content-Type", "application/json")
}

func (p *Provider) toGatewayChatResponse(oa oaChatResponse) gw.ChatCompletionResponse {
	choices := make([]gw.Choice, 0, len(oa.Choices))
	for _, c := range oa.Choices {
		var parts []gw.ContentPart
		switch v := c.Message.Content.(type) {
		case string:
			if v != "" {
				parts = append(parts, gw.Text(v))
			}
		case []interface{}:
			for _, pv := range v {
				if pm, ok := pv.(map[string]interface{}); ok && pm["type"] == "text" {
					if txt, ok := pm["text"].(string); ok {
						parts = append(parts, gw.Text(txt))
					}
				}
			}
		}

		// Convert tool calls from response
		var toolCalls []gw.ToolCall
		for _, tc := range c.Message.ToolCalls {
			toolCalls = append(toolCalls, gw.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: gw.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		choices = append(choices, gw.Choice{
			Index:        c.Index,
			Message:      gw.Message{Role: gw.RoleAssistant, Content: parts, ToolCalls: toolCalls},
			FinishReason: c.FinishReason,
		})
	}

	usage := gw.Usage{
		PromptTokens:     oa.Usage.PromptTokens,
		CompletionTokens: oa.Usage.CompletionTokens,
		TotalTokens:      oa.Usage.TotalTokens,
	}

	// Parse detailed token breakdown if available.
	// OpenAI's `prompt_tokens_details.cached_tokens` is the cache-hit count.
	// OpenAI has no separate cache-write concept (caching is automatic), so
	// CacheWriteTokens stays 0 and CacheReadTokens carries the full cached
	// portion. PromptTokens is already inclusive of cached on OpenAI.
	if oa.Usage.PromptTokensDetails != nil {
		usage.PromptDetails = &gw.TokenDetails{
			CachedTokens:    oa.Usage.PromptTokensDetails.CachedTokens,
			CacheReadTokens: oa.Usage.PromptTokensDetails.CachedTokens,
			ReasoningTokens: oa.Usage.PromptTokensDetails.ReasoningTokens,
			AudioTokens:     oa.Usage.PromptTokensDetails.AudioTokens,
			TextTokens:      oa.Usage.PromptTokensDetails.TextTokens,
		}
	}
	if oa.Usage.CompletionTokensDetails != nil {
		usage.CompletionDetails = &gw.TokenDetails{
			CachedTokens:    oa.Usage.CompletionTokensDetails.CachedTokens,
			ReasoningTokens: oa.Usage.CompletionTokensDetails.ReasoningTokens,
			AudioTokens:     oa.Usage.CompletionTokensDetails.AudioTokens,
			TextTokens:      oa.Usage.CompletionTokensDetails.TextTokens,
		}
	}

	return gw.ChatCompletionResponse{
		ID:      oa.ID,
		Created: time.Unix(oa.Created, 0).UTC(),
		Model:   oa.Model,
		Choices: choices,
		Usage:   usage,
	}
}
