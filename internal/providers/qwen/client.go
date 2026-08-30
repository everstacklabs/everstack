package qwen

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

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	providerutil "github.com/everstacklabs/everstack/internal/providers"
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

type Provider struct {
	cfg    Config
	client *http.Client
}

func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		cfg:    cfg,
		client: cli,
	}
}

func (p *Provider) Name() string {
	return "qwen"
}

// --- Wire types (OpenAI-compatible) ---

type qwenMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []qwenToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type qwenToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function qwenToolCallFunc `json:"function"`
}

type qwenToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type qwenTool struct {
	Type     string          `json:"type"`
	Function qwenToolFuncDef `json:"function"`
}

type qwenToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type qwenChatReq struct {
	Model          string        `json:"model"`
	Messages       []qwenMessage `json:"messages"`
	Tools          []qwenTool    `json:"tools,omitempty"`
	Temperature    *float64      `json:"temperature,omitempty"`
	TopP           *float64      `json:"top_p,omitempty"`
	TopK           *int          `json:"top_k,omitempty"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	EnableThinking *bool         `json:"enable_thinking,omitempty"`
	ThinkingBudget *int          `json:"thinking_budget,omitempty"`
	Stream         bool          `json:"stream,omitempty"`
}

type qwenChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []qwenToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// --- Message conversion ---

func convertMessages(msgs []gw.Message) []qwenMessage {
	out := make([]qwenMessage, 0, len(msgs))
	for _, m := range msgs {
		qm := qwenMessage{
			Role: string(m.Role),
		}

		// Tool response message
		if m.ToolCallID != "" {
			qm.ToolCallID = m.ToolCallID
			qm.Role = "tool"
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					qm.Content = *c.Text
					break
				}
			}
			out = append(out, qm)
			continue
		}

		// Assistant message with tool calls
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				qm.ToolCalls = append(qm.ToolCalls, qwenToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: qwenToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
			// Assistant messages with tool calls may also have text content
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					qm.Content = *c.Text
					break
				}
			}
			out = append(out, qm)
			continue
		}

		// Regular text message
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				qm.Content = *c.Text
				break
			}
		}
		out = append(out, qm)
	}
	return out
}

func convertTools(tools []gw.ToolDefinition) []qwenTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]qwenTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, qwenTool{
			Type: t.Type,
			Function: qwenToolFuncDef{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

// --- Chat (non-streaming) ---

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	qwenReq := qwenChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	}

	if qwenReasoningRequiresStreaming(req.Model) && qwenReasoningRequested(req.Sampling) {
		return gw.ChatCompletionResponse{}, fmt.Errorf(
			"qwen model %q supports reasoning only with streaming requests",
			req.Model,
		)
	}
	applyQwenSampling(&qwenReq, req.Sampling)
	if qwenReasoningRequiresStreaming(req.Model) && qwenReq.EnableThinking == nil {
		// These open Qwen models default to thinking, but Alibaba rejects
		// thinking during synchronous calls. Disable it only for the affected
		// models; newer commercial models retain their provider default.
		enabled := false
		qwenReq.EnableThinking = &enabled
	}

	body, _ := json.Marshal(qwenReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 30*time.Second))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "qwen",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", false,
		"tools", len(qwenReq.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "qwen", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("qwen error: %s", string(bodyBytes))
	}

	var qwenResp qwenChatResp
	if err := json.NewDecoder(resp.Body).Decode(&qwenResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var content string
	var toolCalls []gw.ToolCall
	if len(qwenResp.Choices) > 0 {
		content = qwenResp.Choices[0].Message.Content
		for _, tc := range qwenResp.Choices[0].Message.ToolCalls {
			toolCalls = append(toolCalls, gw.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: gw.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	msg := gw.NewMessage(gw.RoleAssistant, gw.Text(content))
	msg.ToolCalls = toolCalls

	choice := gw.Choice{
		Index:   0,
		Message: msg,
	}

	usage := gw.Usage{
		PromptTokens:     qwenResp.Usage.PromptTokens,
		CompletionTokens: qwenResp.Usage.CompletionTokens,
		TotalTokens:      qwenResp.Usage.TotalTokens,
	}

	return gw.NewChatResponse(qwenResp.ID, qwenResp.Model, []gw.Choice{choice}, usage), nil
}

// --- Chat (streaming) ---

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	qReq := qwenChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   true,
	}
	applyQwenSampling(&qReq, req.Sampling)

	body, _ := json.Marshal(qReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "qwen",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", true,
		"tools", len(qReq.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "qwen", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qwen stream error: %s", string(bodyBytes))
	}

	// Track in-flight tool calls being accumulated across chunks.
	type pendingToolCall struct {
		ID        string
		Type      string
		Name      string
		Arguments strings.Builder
	}
	pendingCalls := make(map[int]*pendingToolCall)

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(line)
			if s == "" || !strings.HasPrefix(s, "data:") {
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
				Model   string `json:"model"`
				Choices []struct {
					Index int `json:"index"`
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id,omitempty"`
							Type     string `json:"type,omitempty"`
							Function struct {
								Name      string `json:"name,omitempty"`
								Arguments string `json:"arguments,omitempty"`
							} `json:"function"`
						} `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage,omitempty"`
			}
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}

			chunk := gw.NewChatChunk(partial.ID, partial.Model, nil)
			if partial.Usage != nil {
				chunk.Usage = &gw.Usage{
					PromptTokens:     partial.Usage.PromptTokens,
					CompletionTokens: partial.Usage.CompletionTokens,
					TotalTokens:      partial.Usage.TotalTokens,
				}
			}

			for _, ch := range partial.Choices {
				// Accumulate streaming tool call deltas
				for _, tc := range ch.Delta.ToolCalls {
					pc, exists := pendingCalls[tc.Index]
					if !exists {
						pc = &pendingToolCall{}
						pendingCalls[tc.Index] = pc
					}
					if tc.ID != "" {
						pc.ID = tc.ID
					}
					if tc.Type != "" {
						pc.Type = tc.Type
					}
					if tc.Function.Name != "" {
						pc.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						pc.Arguments.WriteString(tc.Function.Arguments)
					}
				}

				// Emit text content chunks
				if ch.Delta.Content != "" {
					delta := gw.ChoiceDelta{
						Index:        ch.Index,
						Delta:        gw.NewMessage(gw.RoleAssistant, gw.Text(ch.Delta.Content)),
						FinishReason: ch.FinishReason,
					}
					chunk.Choices = append(chunk.Choices, delta)
				}

				// When finish_reason is "tool_calls" or "stop" and we have pending calls,
				// emit them as completed tool calls.
				if ch.FinishReason != "" && len(pendingCalls) > 0 {
					var toolCalls []gw.ToolCall
					for _, pc := range pendingCalls {
						toolCalls = append(toolCalls, gw.ToolCall{
							ID:   pc.ID,
							Type: pc.Type,
							Function: gw.ToolCallFunction{
								Name:      pc.Name,
								Arguments: pc.Arguments.String(),
							},
						})
					}

					msg := gw.NewMessage(gw.RoleAssistant, gw.Text(""))
					msg.ToolCalls = toolCalls

					delta := gw.ChoiceDelta{
						Index:        ch.Index,
						Delta:        msg,
						FinishReason: ch.FinishReason,
					}
					chunk.Choices = append(chunk.Choices, delta)
					pendingCalls = make(map[int]*pendingToolCall)
				}
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

func applyQwenSampling(request *qwenChatReq, sampling gw.SamplingParams) {
	if sampling.Temperature > 0 || sampling.TemperatureConfigured {
		temperature := sampling.Temperature
		request.Temperature = &temperature
	}
	if sampling.TopP > 0 || sampling.TopPConfigured {
		topP := sampling.TopP
		request.TopP = &topP
	}
	if sampling.TopK != nil {
		topK := *sampling.TopK
		request.TopK = &topK
	}
	if sampling.MaxTokens > 0 {
		request.MaxTokens = sampling.MaxTokens
	}

	if sampling.ReasoningBudget != nil {
		enabled := true
		request.EnableThinking = &enabled
		request.ThinkingBudget = sampling.ReasoningBudget
		return
	}
	if sampling.ReasoningEnabled != nil {
		request.EnableThinking = sampling.ReasoningEnabled
		return
	}
	switch strings.ToLower(strings.TrimSpace(sampling.ReasoningEffort)) {
	case "none":
		enabled := false
		request.EnableThinking = &enabled
		return
	case "minimal", "low", "medium", "high", "xhigh", "max":
		enabled := true
		request.EnableThinking = &enabled
		return
	}
}

func qwenReasoningRequiresStreaming(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "qwen3-235b-a22b", "qwen3-32b":
		return true
	default:
		return false
	}
}

func qwenReasoningRequested(sampling gw.SamplingParams) bool {
	if sampling.ReasoningBudget != nil {
		return true
	}
	if sampling.ReasoningEnabled != nil {
		return *sampling.ReasoningEnabled
	}
	switch strings.ToLower(strings.TrimSpace(sampling.ReasoningEffort)) {
	case "", "none":
		return false
	default:
		return true
	}
}

// --- Embeddings ---

func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	qwenReq := map[string]interface{}{
		"model": req.Model,
		"input": []string{req.Input},
	}

	body, _ := json.Marshal(qwenReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.EmbeddingsResponse{}, fmt.Errorf("qwen embeddings error: %s", string(bodyBytes))
	}

	var qwenResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&qwenResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	if len(qwenResp.Data) > 0 {
		return gw.EmbeddingsResponse{Embedding: qwenResp.Data[0].Embedding}, nil
	}

	return gw.EmbeddingsResponse{Embedding: []float64{}}, nil
}

// --- Model support ---

// qwenTTSModelPrefixes lists prefixes for DashScope-native TTS models that
// should always be accepted by the Qwen provider regardless of the configured
// chat/embedding model list.
var qwenTTSModelPrefixes = []string{
	"qwen3-tts-",
	"qwen2.5-tts-",
	"qwen-tts",
	"qwen-voice-",
	"cosyvoice-",
}

func (p *Provider) SupportsModel(model string) bool {
	lower := strings.ToLower(model)
	// Always accept known TTS models — these use the DashScope-native
	// endpoint and are not part of the OpenAI-compatible model list.
	for _, prefix := range qwenTTSModelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
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
