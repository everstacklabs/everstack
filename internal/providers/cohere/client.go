package cohere

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	providerutil "github.com/everstacklabs/everstack/internal/providers"
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

// Provider implements the Cohere LLM provider
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Cohere provider
func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		cfg:    cfg,
		client: cli,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "cohere"
}

// Wire types for Cohere API v2
type cohereMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []cohereToolCall `json:"tool_calls,omitempty"`
	ToolPlan   string           `json:"tool_plan,omitempty"`
}

type cohereToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function cohereToolCallFunc `json:"function"`
}

type cohereToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type cohereToolDef struct {
	Type     string            `json:"type"`
	Function cohereToolFuncDef `json:"function"`
}

type cohereToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type cohereChatReq struct {
	Model       string          `json:"model"`
	Messages    []cohereMessage `json:"messages"`
	Tools       []cohereToolDef `json:"tools,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	P           *float64        `json:"p,omitempty"`
	K           *int            `json:"k,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Thinking    *cohereThinking `json:"thinking,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type cohereThinking struct {
	Type        string `json:"type,omitempty"`
	TokenBudget *int   `json:"token_budget,omitempty"`
}

type cohereChatResp struct {
	ID           string      `json:"id"`
	FinishReason string      `json:"finish_reason,omitempty"`
	Content      interface{} `json:"content,omitempty"`
	Text         string      `json:"text,omitempty"`
	Response     string      `json:"response,omitempty"`
	Message      struct {
		Role      string           `json:"role"`
		Content   interface{}      `json:"content"`
		ToolCalls []cohereToolCall `json:"tool_calls,omitempty"`
		ToolPlan  string           `json:"tool_plan,omitempty"`
	} `json:"message"`
	Usage struct {
		Tokens struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"tokens"`
		BilledUnits struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"billed_units"`
	} `json:"usage"`
}

// Chat sends a chat completion request to Cohere
func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	messages := convertMessages(req.Messages)
	tools := convertTools(req.Tools)

	cohereReq := cohereChatReq{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
	}

	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		cohereReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		p := req.Sampling.TopP
		cohereReq.P = &p
	}
	if req.Sampling.TopK != nil {
		k := *req.Sampling.TopK
		cohereReq.K = &k
	}
	if req.Sampling.MaxTokens > 0 {
		cohereReq.MaxTokens = req.Sampling.MaxTokens
	}
	cohereReq.Thinking = cohereThinkingFor(req.Sampling)

	body, _ := json.Marshal(cohereReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/v2/chat", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "cohere", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMsg := "cohere error: " + string(bodyBytes)
		return gw.ChatCompletionResponse{}, fmt.Errorf("%s", errorMsg)
	}

	var cohereResp cohereChatResp
	if err := json.NewDecoder(resp.Body).Decode(&cohereResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	contentText := firstNonEmpty(
		extractContentText(cohereResp.Message.Content),
		extractContentText(cohereResp.Content),
		cohereResp.Text,
		cohereResp.Response,
	)

	// Convert tool calls from response
	var toolCalls []gw.ToolCall
	for _, tc := range cohereResp.Message.ToolCalls {
		toolCalls = append(toolCalls, gw.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: gw.ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	if strings.TrimSpace(contentText) == "" && len(toolCalls) == 0 {
		return gw.ChatCompletionResponse{}, fmt.Errorf("cohere response contained no assistant content")
	}

	msg := gw.Message{
		Role:      gw.RoleAssistant,
		Content:   []gw.ContentPart{{Type: "text", Text: strPtr(contentText)}},
		ToolCalls: toolCalls,
	}

	finishReason := normalizeFinishReason(cohereResp.FinishReason)
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	choice := gw.Choice{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason,
	}

	usage := gw.Usage{
		PromptTokens:     cohereResp.Usage.Tokens.InputTokens,
		CompletionTokens: cohereResp.Usage.Tokens.OutputTokens,
		TotalTokens:      cohereResp.Usage.Tokens.InputTokens + cohereResp.Usage.Tokens.OutputTokens,
	}
	if usage.TotalTokens == 0 {
		usage.PromptTokens = cohereResp.Usage.BilledUnits.InputTokens
		usage.CompletionTokens = cohereResp.Usage.BilledUnits.OutputTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return gw.NewChatResponse(cohereResp.ID, req.Model, []gw.Choice{choice}, usage), nil
}

// ChatStream implements streaming for Cohere
func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	messages := convertMessages(req.Messages)
	tools := convertTools(req.Tools)

	cohereReq := cohereChatReq{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}

	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		cohereReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		p := req.Sampling.TopP
		cohereReq.P = &p
	}
	if req.Sampling.TopK != nil {
		k := *req.Sampling.TopK
		cohereReq.K = &k
	}
	if req.Sampling.MaxTokens > 0 {
		cohereReq.MaxTokens = req.Sampling.MaxTokens
	}
	cohereReq.Thinking = cohereThinkingFor(req.Sampling)

	body, _ := json.Marshal(cohereReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/v2/chat", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Monitor rate limits
	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "cohere", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		errorMsg := "cohere stream error: " + string(bodyBytes)
		return fmt.Errorf("%s", errorMsg)
	}

	// Parse SSE stream
	reader := bufio.NewReader(resp.Body)
	var responseID, responseModel string
	var inputTokens, outputTokens int

	// Track in-flight tool calls during streaming
	type pendingToolCall struct {
		id      string
		name    string
		argsBuf strings.Builder
	}
	var pendingTC *pendingToolCall

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// SSE format: "data: {...}"
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// Handle different event types
		eventType, _ := event["type"].(string)
		switch eventType {
		case "message-start":
			if id, ok := event["id"].(string); ok {
				responseID = id
			}
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if msg, ok := delta["message"].(map[string]interface{}); ok {
					if model, ok := msg["model"].(string); ok {
						responseModel = model
					}
				}
			}

		case "content-delta":
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if msg, ok := delta["message"].(map[string]interface{}); ok {
					if content, ok := msg["content"].(map[string]interface{}); ok {
						if text, ok := content["text"].(string); ok && text != "" {
							delta := gw.ChoiceDelta{
								Index: 0,
								Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(text)),
							}
							chunk := gw.NewChatChunk(responseID, firstNonEmpty(responseModel, req.Model), []gw.ChoiceDelta{delta})
							if err := onChunk(chunk); err != nil {
								return err
							}
						}
					}
				}
			}

		case "tool-call-start":
			// Begin accumulating a new tool call
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if msg, ok := delta["message"].(map[string]interface{}); ok {
					if tc, ok := msg["tool_calls"].(map[string]interface{}); ok {
						tcID, _ := tc["id"].(string)
						fnName := ""
						if fn, ok := tc["function"].(map[string]interface{}); ok {
							fnName, _ = fn["name"].(string)
						}
						pendingTC = &pendingToolCall{id: tcID, name: fnName}
					}
				}
			}

		case "tool-call-delta":
			// Accumulate tool call arguments
			if pendingTC != nil {
				if delta, ok := event["delta"].(map[string]interface{}); ok {
					if msg, ok := delta["message"].(map[string]interface{}); ok {
						if tc, ok := msg["tool_calls"].(map[string]interface{}); ok {
							if fn, ok := tc["function"].(map[string]interface{}); ok {
								if args, ok := fn["arguments"].(string); ok {
									pendingTC.argsBuf.WriteString(args)
								}
							}
						}
					}
				}
			}

		case "tool-call-end":
			// Emit completed tool call as a chunk
			if pendingTC != nil {
				tc := gw.ToolCall{
					ID:   pendingTC.id,
					Type: "function",
					Function: gw.ToolCallFunction{
						Name:      pendingTC.name,
						Arguments: pendingTC.argsBuf.String(),
					},
				}
				delta := gw.ChoiceDelta{
					Index: 0,
					Delta: gw.Message{
						Role:      gw.RoleAssistant,
						ToolCalls: []gw.ToolCall{tc},
					},
					FinishReason: "tool_calls",
				}
				chunk := gw.NewChatChunk(responseID, firstNonEmpty(responseModel, req.Model), []gw.ChoiceDelta{delta})
				if err := onChunk(chunk); err != nil {
					return err
				}
				pendingTC = nil
			}

		case "message-end":
			if id, ok := event["id"].(string); ok && responseID == "" {
				responseID = id
			}

			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if usage, ok := delta["usage"].(map[string]interface{}); ok {
					if tokens, ok := usage["tokens"].(map[string]interface{}); ok {
						if input, ok := tokens["input_tokens"].(float64); ok {
							inputTokens = int(input)
						}
						if output, ok := tokens["output_tokens"].(float64); ok {
							outputTokens = int(output)
						}
					}
				}
			}

			if inputTokens > 0 || outputTokens > 0 {
				usage := &gw.Usage{
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					TotalTokens:      inputTokens + outputTokens,
				}
				finalChunk := gw.ChatResponseChunk{
					ID:    responseID,
					Model: firstNonEmpty(responseModel, req.Model),
					Usage: usage,
				}
				if err := onChunk(finalChunk); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func cohereThinkingFor(sampling gw.SamplingParams) *cohereThinking {
	if sampling.ReasoningBudget != nil {
		return &cohereThinking{TokenBudget: sampling.ReasoningBudget}
	}
	if sampling.ReasoningEnabled != nil {
		if *sampling.ReasoningEnabled {
			return &cohereThinking{Type: "enabled"}
		}
		return &cohereThinking{Type: "disabled"}
	}
	switch strings.ToLower(strings.TrimSpace(sampling.ReasoningEffort)) {
	case "none":
		return &cohereThinking{Type: "disabled"}
	case "minimal", "low", "medium", "high", "xhigh", "max":
		// Cohere v2 exposes reasoning as a thinking mode rather than graded
		// effort. Any non-none normalized level enables the model's reasoning.
		return &cohereThinking{Type: "enabled"}
	default:
		return nil
	}
}

// convertMessages transforms gateway messages to Cohere v2 format.
func convertMessages(msgs []gw.Message) []cohereMessage {
	out := make([]cohereMessage, 0, len(msgs))
	for _, m := range msgs {
		var textContent string
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				textContent = *c.Text
				break
			}
		}

		cm := cohereMessage{}
		switch m.Role {
		case gw.RoleSystem:
			cm.Role = "system"
			cm.Content = textContent
		case gw.RoleUser:
			cm.Role = "user"
			cm.Content = textContent
		case gw.RoleAssistant:
			cm.Role = "assistant"
			cm.Content = textContent
			for _, tc := range m.ToolCalls {
				cm.ToolCalls = append(cm.ToolCalls, cohereToolCall{
					ID:   tc.ID,
					Type: firstNonEmpty(tc.Type, "function"),
					Function: cohereToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		case gw.RoleFunction, gw.RoleTool:
			cm.Role = "tool"
			cm.ToolCallID = m.ToolCallID
			cm.Content = textContent
		default:
			cm.Role = "user"
			cm.Content = textContent
		}

		out = append(out, cm)
	}
	return out
}

// convertTools transforms gateway tool definitions to Cohere v2 format.
func convertTools(tools []gw.ToolDefinition) []cohereToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]cohereToolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, cohereToolDef{
			Type: firstNonEmpty(t.Type, "function"),
			Function: cohereToolFuncDef{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

// extractContentText handles Cohere's polymorphic content field (string, array, or object).
func extractContentText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			switch v := item.(type) {
			case string:
				parts = append(parts, v)
			case map[string]interface{}:
				if text, ok := v["text"].(string); ok {
					parts = append(parts, text)
				}
			default:
				parts = append(parts, fmt.Sprintf("%v", item))
			}
		}
		return strings.Join(parts, "")
	case map[string]interface{}:
		if text, ok := c["text"].(string); ok {
			return text
		}
		return fmt.Sprintf("%v", c)
	default:
		if content == nil {
			return ""
		}
		return fmt.Sprintf("%v", content)
	}
}

func strPtr(s string) *string { return &s }

func normalizeFinishReason(reason string) string {
	switch strings.ToLower(reason) {
	case "complete", "stop", "end_turn":
		return "stop"
	case "max_tokens", "length":
		return "length"
	case "tool_call", "tool_calls":
		return "tool_calls"
	case "content_filter", "safety":
		return "content_filter"
	default:
		return "stop"
	}
}

// firstNonEmpty returns the first non-empty string
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Embed generates embeddings using Cohere
func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	cohereReq := map[string]interface{}{
		"texts":      []string{req.Input},
		"model":      req.Model,
		"input_type": "search_document",
	}

	body, _ := json.Marshal(cohereReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/v2/embed", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	var cohereResp struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cohereResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	if len(cohereResp.Embeddings) > 0 {
		return gw.EmbeddingsResponse{Embedding: cohereResp.Embeddings[0]}, nil
	}

	return gw.EmbeddingsResponse{Embedding: []float64{}}, nil
}

// SupportsModel checks if a model is supported
func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return true
	}
	for _, m := range p.cfg.SupportedModels {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}
