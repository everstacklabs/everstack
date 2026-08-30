package anthropic

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

type Provider struct {
	baseURL string
	apiKey  string
	version string
	client  *http.Client
	models  map[string]struct{}
}

func NewProvider(cfg Config) *Provider {
	spec := DefaultSpec()
	base := cfg.BaseURL
	if base == "" {
		base = spec.BaseURL
	}
	// Normalize base: trim "/" and any trailing "/v1"
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = strings.TrimSuffix(base, "/v1")
	}
	ver := cfg.Version
	if ver == "" {
		ver = spec.APIVersion
	}
	m := make(map[string]struct{})
	for _, v := range cfg.Models {
		m[strings.ToLower(v)] = struct{}{}
	}

	// Create HTTP client with instrumented transport for automatic tracing
	cli := clientx.Default()
	if cli.Transport == nil {
		cli.Transport = http.DefaultTransport
	}
	cli.Transport = attrs.NewInstrumentedTransport(cli.Transport, "anthropic")

	return &Provider{baseURL: base, apiKey: cfg.APIKey, version: ver, client: cli, models: m}
}

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) SupportsModel(model string) bool {
	_, ok := p.models[strings.ToLower(model)]
	return ok
}

// --- wire types ---

// cacheControl marks a block as eligible for Anthropic prompt caching.
// Stamping `cache_control: {"type": "ephemeral"}` on a content block,
// tool definition, or message tells the API to cache everything up to
// that point as a prefix; subsequent requests with the same prefix get
// `cache_read_input_tokens` charged at ~10% the regular rate.
//
// Anthropic allows up to 4 cache breakpoints per request. We use three
// in toClaude when the caller signals via req.Metadata["cache_control"]:
//   - Last tool definition (caches all tool defs)
//   - First user message's trailing content (caches system prompt that's
//     merged-as-user in this client today)
//   - Last assistant message's trailing content (caches conversation
//     prefix up to the latest user turn)
type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type msg struct {
	Role    string        `json:"role"`
	Content []interface{} `json:"content"`
}

type claudeContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type claudeToolUseContent struct {
	Type         string        `json:"type"` // "tool_use"
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Input        interface{}   `json:"input"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type claudeToolResultContent struct {
	Type         string        `json:"type"` // "tool_result"
	ToolUseID    string        `json:"tool_use_id"`
	Content      string        `json:"content"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type claudeTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	CacheControl *cacheControl          `json:"cache_control,omitempty"`
}

type messagesReq struct {
	Model        string                `json:"model"`
	Messages     []msg                 `json:"messages"`
	Tools        []claudeTool          `json:"tools,omitempty"`
	ToolChoice   interface{}           `json:"tool_choice,omitempty"`
	Stream       bool                  `json:"stream,omitempty"`
	MaxTokens    int                   `json:"max_tokens,omitempty"`
	Temperature  *float64              `json:"temperature,omitempty"`
	TopP         *float64              `json:"top_p,omitempty"`
	TopK         *int                  `json:"top_k,omitempty"`
	OutputConfig *claudeOutputConfig   `json:"output_config,omitempty"`
	Thinking     *claudeThinkingConfig `json:"thinking,omitempty"`
}

type claudeOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type claudeThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type usageBlock struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

type messagesResp struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      usageBlock     `json:"usage"`
}

func (p *Provider) messagesURL() string {
	b := strings.TrimRight(p.baseURL, "/")
	return b + "/v1/messages"
}

func (p *Provider) toClaude(req gw.ChatCompletionRequest) (messagesReq, error) {
	if p.apiKey == "" {
		return messagesReq{}, errors.New("anthropic api key not provided")
	}
	out := messagesReq{Model: req.Model, Stream: req.Stream}
	switch {
	case req.Sampling.MaxTokens > 0:
		out.MaxTokens = req.Sampling.MaxTokens
	case req.Sampling.MaxCompletionTokens > 0:
		out.MaxTokens = req.Sampling.MaxCompletionTokens
	default:
		out.MaxTokens = 8192 // Anthropic API requires max_tokens
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		out.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		topP := req.Sampling.TopP
		out.TopP = &topP
	}
	if req.Sampling.TopK != nil {
		topK := *req.Sampling.TopK
		out.TopK = &topK
	}
	if req.Sampling.ReasoningEffort != "" {
		out.OutputConfig = &claudeOutputConfig{Effort: req.Sampling.ReasoningEffort}
	}
	if req.Sampling.ReasoningBudget != nil && *req.Sampling.ReasoningBudget > 0 {
		out.Thinking = &claudeThinkingConfig{
			Type:         "enabled",
			BudgetTokens: *req.Sampling.ReasoningBudget,
		}
	} else if req.Sampling.ReasoningEnabled != nil {
		if *req.Sampling.ReasoningEnabled {
			out.Thinking = &claudeThinkingConfig{Type: "adaptive"}
		} else {
			out.Thinking = &claudeThinkingConfig{Type: "disabled"}
		}
	}

	// Convert tools
	for _, t := range req.Tools {
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out.Tools = append(out.Tools, claudeTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		switch tc := req.ToolChoice.(type) {
		case string:
			switch tc {
			case "auto":
				out.ToolChoice = map[string]string{"type": "auto"}
			case "none":
				// Anthropic doesn't have a "none" tool_choice; omit tools instead
				out.Tools = nil
				out.ToolChoice = nil
			case "required":
				out.ToolChoice = map[string]string{"type": "any"}
			}
		case map[string]interface{}:
			// OpenAI format: {"type": "function", "function": {"name": "..."}}
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok {
					out.ToolChoice = map[string]string{"type": "tool", "name": name}
				}
			}
		}
	}

	// Convert messages
	for _, m := range req.Messages {
		role := string(m.Role)

		// Handle tool result messages: Anthropic expects these as user messages
		// with tool_result content blocks
		if role == "tool" {
			toolCallID := m.ToolCallID
			var resultText string
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					resultText += *c.Text
				}
			}
			content := []interface{}{
				claudeToolResultContent{
					Type:      "tool_result",
					ToolUseID: toolCallID,
					Content:   resultText,
				},
			}
			// Merge with previous user message if possible to avoid
			// consecutive user messages (Anthropic rejects them)
			if len(out.Messages) > 0 && out.Messages[len(out.Messages)-1].Role == "user" {
				out.Messages[len(out.Messages)-1].Content = append(
					out.Messages[len(out.Messages)-1].Content, content...)
			} else {
				out.Messages = append(out.Messages, msg{Role: "user", Content: content})
			}
			continue
		}

		if role == "assistant" {
			var parts []interface{}
			// Add text content
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					parts = append(parts, claudeContent{Type: "text", Text: *c.Text})
				}
			}
			// Add tool_use blocks from ToolCalls
			for _, tc := range m.ToolCalls {
				var input interface{}
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						// If arguments aren't valid JSON, wrap as string
						input = tc.Function.Arguments
					}
				} else {
					input = map[string]interface{}{}
				}
				parts = append(parts, claudeToolUseContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			if len(parts) > 0 {
				out.Messages = append(out.Messages, msg{Role: "assistant", Content: parts})
			}
		} else {
			// user or system → user
			var parts []interface{}
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					parts = append(parts, claudeContent{Type: "text", Text: *c.Text})
				}
			}
			if len(parts) > 0 {
				out.Messages = append(out.Messages, msg{Role: "user", Content: parts})
			}
		}
	}

	// Apply cache_control breakpoints when the caller opts in via
	// req.Metadata["cache_control"] = "ephemeral". Anthropic allows up
	// to 4 breakpoints; we stamp three places where stable prefixes
	// commonly live:
	//   1. Last tool definition (caches every tool def as a prefix block).
	//   2. First user message's trailing content (in this client, the
	//      system prompt is merged into the first user message).
	//   3. Last assistant message's trailing content (caches the
	//      conversation history up through the most recent assistant turn).
	// Each subsequent request with the same prefix gets the cached
	// portion charged as cache_read_input_tokens at ~10% of fresh.
	if cc, ok := req.Metadata["cache_control"]; ok {
		ccStr, _ := cc.(string)
		if ccStr == "ephemeral" {
			applyCacheBreakpoints(&out)
		}
	}
	return out, nil
}

// applyCacheBreakpoints stamps cache_control: ephemeral on the last
// tool definition, the trailing block of the first user message, and
// the trailing block of the last assistant message. Safe to call on
// any messagesReq — no-ops if a section is empty.
func applyCacheBreakpoints(out *messagesReq) {
	mark := &cacheControl{Type: "ephemeral"}

	// 1. Last tool definition.
	if n := len(out.Tools); n > 0 {
		out.Tools[n-1].CacheControl = mark
	}

	// 2. First user message's trailing content block. The system
	// prompt is merged into the first user message in this client, so
	// caching the trailing block of that message captures system +
	// initial user setup as a single cache prefix.
	for i := range out.Messages {
		if out.Messages[i].Role != "user" {
			continue
		}
		stampLastBlock(out.Messages[i].Content, mark)
		break
	}

	// 3. Last assistant message's trailing content block. Caches the
	// conversation history including the most recent assistant turn so
	// the next user turn benefits from prefix caching.
	for i := len(out.Messages) - 1; i >= 0; i-- {
		if out.Messages[i].Role != "assistant" {
			continue
		}
		stampLastBlock(out.Messages[i].Content, mark)
		break
	}
}

// stampLastBlock sets CacheControl on the last content block in a
// slice, regardless of which concrete type that block is. The slice
// elements are interface{} (claudeContent / claudeToolUseContent /
// claudeToolResultContent) so we type-switch.
func stampLastBlock(content []interface{}, mark *cacheControl) {
	if len(content) == 0 {
		return
	}
	idx := len(content) - 1
	switch v := content[idx].(type) {
	case claudeContent:
		v.CacheControl = mark
		content[idx] = v
	case claudeToolUseContent:
		v.CacheControl = mark
		content[idx] = v
	case claudeToolResultContent:
		v.CacheControl = mark
		content[idx] = v
	}
}

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	wire, err := p.toClaude(req)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	b, _ := json.Marshal(wire)
	// Apply a per-request timeout for the unary call
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL(), bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", p.version)

	// Observability: log outgoing provider request
	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "anthropic",
		"endpoint", p.messagesURL(),
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
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "anthropic", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("anthropic chat error: %s", string(body))
	}
	var out messagesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var parts []gw.ContentPart
	var toolCalls []gw.ToolCall
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, gw.Text(block.Text))
			}
		case "tool_use":
			args := string(block.Input)
			if args == "null" || args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, gw.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: gw.ToolCallFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}

	finishReason := "stop"
	if out.StopReason == "tool_use" {
		finishReason = "tool_calls"
	} else if out.StopReason == "max_tokens" {
		finishReason = "length"
	}

	choice := gw.Choice{
		Index:        0,
		Message:      gw.Message{Role: gw.RoleAssistant, Content: parts, ToolCalls: toolCalls},
		FinishReason: finishReason,
	}
	// Anthropic's `input_tokens` field counts ONLY fresh, non-cached input
	// for this call. The cached portion lands in `cache_read_input_tokens`
	// + `cache_creation_input_tokens` but those tokens are still part of
	// the prompt sent to the model and still consume context-window space.
	//
	// OpenAI's convention (which the gw.Usage struct follows) is that
	// `prompt_tokens` is the TOTAL input — cached + fresh — with the
	// cached subset reported separately under prompt_tokens_details.
	// Normalise Anthropic to that shape so context-window gauges and
	// cross-provider rollups match reality. Without this, sessions with
	// cache_control: ephemeral (the default agent path) under-report
	// prompt size by the cached portion, which can be most of the prompt.
	cachedTokens := out.Usage.CacheReadInputTokens + out.Usage.CacheCreationInputTokens
	totalInput := out.Usage.InputTokens + cachedTokens
	usage := gw.Usage{
		PromptTokens:     totalInput,
		CompletionTokens: out.Usage.OutputTokens,
		TotalTokens:      totalInput + out.Usage.OutputTokens,
	}

	if cachedTokens > 0 {
		usage.PromptDetails = &gw.TokenDetails{
			CachedTokens:     cachedTokens,
			CacheReadTokens:  out.Usage.CacheReadInputTokens,
			CacheWriteTokens: out.Usage.CacheCreationInputTokens,
		}
	}

	return gw.NewChatResponse(out.ID, req.Model, []gw.Choice{choice}, usage), nil
}

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	// Send streaming request to Anthropic and parse SSE events
	wire, err := p.toClaude(req)
	if err != nil {
		return err
	}
	wire.Stream = true
	b, _ := json.Marshal(wire)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.messagesURL(), bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", p.version)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Capture and monitor rate limit information for streaming
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "anthropic", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic chat error: %s", string(body))
	}

	reader := bufio.NewReader(resp.Body)
	var id, model string
	var inputTokens, outputTokens int
	var cacheReadInputTokens, cacheCreationInputTokens int
	var stopReason string

	// Track streaming tool calls: index → accumulated data
	type streamingToolCall struct {
		ID       string
		Name     string
		ArgsJSON strings.Builder
	}
	var activeToolCalls []streamingToolCall
	var currentBlockIndex int
	var currentBlockIsToolUse bool

	// Track initial input tokens from message_start
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(line)
			if s == "" { // event separator
				continue
			}
			if strings.HasPrefix(s, "event:") {
				// ignore label; data JSON has type
				continue
			}
			if !strings.HasPrefix(s, "data:") {
				// ignore anything else
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
			if payload == "[DONE]" { // safety
				return nil
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				// ignore malformed chunks
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "message_start":
				if msg, ok := m["message"].(map[string]any); ok {
					if v, ok := msg["id"].(string); ok {
						id = v
					}
					if v, ok := msg["model"].(string); ok {
						model = v
					}
					// Extract input tokens from message_start
					if usage, ok := msg["usage"].(map[string]any); ok {
						if v, ok := usage["input_tokens"].(float64); ok {
							inputTokens = int(v)
						}
						// Extract cache tokens from message_start
						if v, ok := usage["cache_read_input_tokens"].(float64); ok {
							cacheReadInputTokens = int(v)
						}
						if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
							cacheCreationInputTokens = int(v)
						}
					}
				}

			case "content_block_start":
				idx, _ := m["index"].(float64)
				currentBlockIndex = int(idx)
				currentBlockIsToolUse = false
				if cb, ok := m["content_block"].(map[string]any); ok {
					if cbType, _ := cb["type"].(string); cbType == "tool_use" {
						currentBlockIsToolUse = true
						tcID, _ := cb["id"].(string)
						tcName, _ := cb["name"].(string)
						// Ensure slice is large enough
						for len(activeToolCalls) <= currentBlockIndex {
							activeToolCalls = append(activeToolCalls, streamingToolCall{})
						}
						activeToolCalls[currentBlockIndex] = streamingToolCall{
							ID:   tcID,
							Name: tcName,
						}
					}
				}

			case "content_block_delta":
				if delta, ok := m["delta"].(map[string]any); ok {
					deltaType, _ := delta["type"].(string)
					switch deltaType {
					case "text_delta":
						if txt, ok := delta["text"].(string); ok && txt != "" {
							d := gw.ChoiceDelta{Index: 0, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(txt))}
							if err := onChunk(gw.NewChatChunk(id, firstNonEmpty(model, req.Model), []gw.ChoiceDelta{d})); err != nil {
								return err
							}
						}
					case "input_json_delta":
						if partialJSON, ok := delta["partial_json"].(string); ok {
							idx, _ := m["index"].(float64)
							i := int(idx)
							if i < len(activeToolCalls) {
								activeToolCalls[i].ArgsJSON.WriteString(partialJSON)
							}
						}
					}
				}

			case "content_block_stop":
				// If a tool_use block just finished, emit it as a streaming delta
				if currentBlockIsToolUse && currentBlockIndex < len(activeToolCalls) {
					tc := activeToolCalls[currentBlockIndex]
					args := tc.ArgsJSON.String()
					if args == "" {
						args = "{}"
					}
					d := gw.ChoiceDelta{
						Index: 0,
						Delta: gw.Message{
							Role: gw.RoleAssistant,
							ToolCalls: []gw.ToolCall{{
								ID:   tc.ID,
								Type: "function",
								Function: gw.ToolCallFunction{
									Name:      tc.Name,
									Arguments: args,
								},
							}},
						},
					}
					if err := onChunk(gw.NewChatChunk(id, firstNonEmpty(model, req.Model), []gw.ChoiceDelta{d})); err != nil {
						return err
					}
					currentBlockIsToolUse = false
				}

			case "message_delta":
				// Extract stop_reason
				if deltaObj, ok := m["delta"].(map[string]any); ok {
					if sr, ok := deltaObj["stop_reason"].(string); ok {
						stopReason = sr
					}
				}

				// Extract usage from message_delta (includes output_tokens)
				if usageData, ok := m["usage"].(map[string]any); ok {
					if v, ok := usageData["output_tokens"].(float64); ok {
						outputTokens = int(v)
					}
				}

				// Determine finish reason
				finishReason := "stop"
				if stopReason == "tool_use" {
					finishReason = "tool_calls"
				} else if stopReason == "max_tokens" {
					finishReason = "length"
				}

				// Send finish reason chunk
				finishDelta := gw.ChoiceDelta{
					Index:        0,
					Delta:        gw.Message{Role: gw.RoleAssistant},
					FinishReason: finishReason,
				}
				if err := onChunk(gw.NewChatChunk(id, firstNonEmpty(model, req.Model), []gw.ChoiceDelta{finishDelta})); err != nil {
					return err
				}

				// Send a final chunk with usage data if available.
				// See the non-streaming branch above for why we add cached
				// tokens into PromptTokens: Anthropic's input_tokens is the
				// fresh-only count, but cached tokens still occupy the
				// context window, so we normalise to OpenAI's "total input"
				// semantic for cross-provider consistency.
				if inputTokens > 0 || outputTokens > 0 {
					cachedTokens := cacheReadInputTokens + cacheCreationInputTokens
					totalInput := inputTokens + cachedTokens
					usage := &gw.Usage{
						PromptTokens:     totalInput,
						CompletionTokens: outputTokens,
						TotalTokens:      totalInput + outputTokens,
					}
					if cachedTokens > 0 {
						usage.PromptDetails = &gw.TokenDetails{
							CachedTokens:     cachedTokens,
							CacheReadTokens:  cacheReadInputTokens,
							CacheWriteTokens: cacheCreationInputTokens,
						}
					}
					finalChunk := gw.ChatResponseChunk{
						ID:      id,
						Created: time.Now(),
						Model:   firstNonEmpty(model, req.Model),
						Choices: []gw.ChoiceDelta{},
						Usage:   usage,
					}
					if err := onChunk(finalChunk); err != nil {
						return err
					}
				}
			case "message_stop":
				return nil
			case "error":
				if errObj, ok := m["error"].(map[string]any); ok {
					if msg, ok := errObj["message"].(string); ok {
						return fmt.Errorf("anthropic stream error: %s", msg)
					}
				}
				return fmt.Errorf("anthropic stream error")
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{Embedding: nil}, gw.ErrNotImplemented("embeddings")
}

// helper
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
