package mistral

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

// Provider implements the Mistral AI LLM provider (OpenAI-compatible)
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Mistral provider
func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		cfg:    cfg,
		client: cli,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "mistral"
}

// --- Wire types (OpenAI-compatible) ---

type mMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []mToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type mToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function mToolCallFunc `json:"function"`
}

type mToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type mTool struct {
	Type     string       `json:"type"`
	Function mToolFuncDef `json:"function"`
}

type mToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type mChatReq struct {
	Model           string     `json:"model"`
	Messages        []mMessage `json:"messages"`
	Tools           []mTool    `json:"tools,omitempty"`
	Temperature     *float64   `json:"temperature,omitempty"`
	TopP            *float64   `json:"top_p,omitempty"`
	MaxTokens       int        `json:"max_tokens,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	Stream          bool       `json:"stream,omitempty"`
}

type mChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []mToolCall     `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// --- Conversion helpers ---

func convertMessages(msgs []gw.Message) []mMessage {
	out := make([]mMessage, 0, len(msgs))
	for _, m := range msgs {
		mm := mMessage{Role: string(m.Role)}

		if m.ToolCallID != "" {
			mm.ToolCallID = m.ToolCallID
			mm.Role = "tool"
			mm.Content = mistralOutboundContent(m.Content)
			out = append(out, mm)
			continue
		}

		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				mm.ToolCalls = append(mm.ToolCalls, mToolCall{
					ID: tc.ID, Type: tc.Type,
					Function: mToolCallFunc{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
				})
			}
		}

		mm.Content = mistralOutboundContent(m.Content)
		out = append(out, mm)
	}
	return out
}

func mistralOutboundContent(parts []gw.ContentPart) json.RawMessage {
	hasNativeChunks := false
	for _, part := range parts {
		if part.ProviderJSON != nil && json.Valid(*part.ProviderJSON) {
			hasNativeChunks = true
			break
		}
	}

	if !hasNativeChunks {
		var text strings.Builder
		for _, part := range parts {
			if part.Type == "text" && part.Text != nil {
				text.WriteString(*part.Text)
			}
		}
		encoded, _ := json.Marshal(text.String())
		return encoded
	}

	chunks := make([]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		if part.ProviderJSON != nil && json.Valid(*part.ProviderJSON) {
			chunks = append(chunks, append(json.RawMessage(nil), (*part.ProviderJSON)...))
			continue
		}
		if part.Type == "text" && part.Text != nil {
			encoded, _ := json.Marshal(map[string]string{
				"type": "text",
				"text": *part.Text,
			})
			chunks = append(chunks, encoded)
		}
	}
	encoded, _ := json.Marshal(chunks)
	return encoded
}

func mistralContentParts(content json.RawMessage, preserveStreamText bool) []gw.ContentPart {
	if len(content) == 0 || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		return nil
	}

	var plain string
	if err := json.Unmarshal(content, &plain); err == nil {
		part := gw.Text(plain)
		if preserveStreamText {
			native, _ := json.Marshal(map[string]string{
				"type": "text",
				"text": plain,
			})
			part.ProviderJSON = rawMessagePointer(native)
		}
		return []gw.ContentPart{part}
	}

	var chunks []json.RawMessage
	if err := json.Unmarshal(content, &chunks); err != nil {
		return nil
	}

	parts := make([]gw.ContentPart, 0, len(chunks))
	for _, chunk := range chunks {
		var metadata struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(chunk, &metadata); err != nil {
			continue
		}
		native := append(json.RawMessage(nil), chunk...)
		part := gw.ContentPart{
			Type:         metadata.Type,
			ProviderJSON: &native,
		}
		if metadata.Type == "text" {
			text := metadata.Text
			part.Text = &text
		}
		parts = append(parts, part)
	}
	return parts
}

func rawMessagePointer(value json.RawMessage) *json.RawMessage {
	cloned := append(json.RawMessage(nil), value...)
	return &cloned
}

func convertTools(tools []gw.ToolDefinition) []mTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]mTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, mTool{
			Type: t.Type,
			Function: mToolFuncDef{
				Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			},
		})
	}
	return out
}

// --- Chat (non-streaming) ---

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	mReq := mChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		mReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		topP := req.Sampling.TopP
		mReq.TopP = &topP
	}
	if req.Sampling.MaxTokens > 0 {
		mReq.MaxTokens = req.Sampling.MaxTokens
	}
	mReq.ReasoningEffort = req.Sampling.ReasoningEffort

	body, _ := json.Marshal(mReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "mistral",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", false,
		"tools", len(mReq.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "mistral", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("mistral error: %s", string(bodyBytes))
	}

	var mResp mChatResp
	if err := json.NewDecoder(resp.Body).Decode(&mResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var contentParts []gw.ContentPart
	var toolCalls []gw.ToolCall
	if len(mResp.Choices) > 0 {
		contentParts = mistralContentParts(mResp.Choices[0].Message.Content, false)
		for _, tc := range mResp.Choices[0].Message.ToolCalls {
			toolCalls = append(toolCalls, gw.ToolCall{
				ID: tc.ID, Type: tc.Type,
				Function: gw.ToolCallFunction{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
	}

	msg := gw.NewMessage(gw.RoleAssistant, contentParts...)
	msg.ToolCalls = toolCalls

	usage := gw.Usage{
		PromptTokens:     mResp.Usage.PromptTokens,
		CompletionTokens: mResp.Usage.CompletionTokens,
		TotalTokens:      mResp.Usage.TotalTokens,
	}

	return gw.NewChatResponse(mResp.ID, mResp.Model, []gw.Choice{{Index: 0, Message: msg}}, usage), nil
}

// --- Chat (streaming) ---

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	mReq := mChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   true,
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		mReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		topP := req.Sampling.TopP
		mReq.TopP = &topP
	}
	if req.Sampling.MaxTokens > 0 {
		mReq.MaxTokens = req.Sampling.MaxTokens
	}
	mReq.ReasoningEffort = req.Sampling.ReasoningEffort

	body, _ := json.Marshal(mReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "mistral",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", true,
		"tools", len(mReq.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "mistral", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mistral stream error: %s", string(bodyBytes))
	}

	type pendingToolCall struct {
		ID, Type, Name string
		Arguments      strings.Builder
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
						Content   json.RawMessage `json:"content"`
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

				parts := mistralContentParts(ch.Delta.Content, true)
				msg := gw.NewMessage(gw.RoleAssistant, parts...)
				if ch.FinishReason != "" && len(pendingCalls) > 0 {
					for _, pc := range pendingCalls {
						msg.ToolCalls = append(msg.ToolCalls, gw.ToolCall{
							ID: pc.ID, Type: pc.Type,
							Function: gw.ToolCallFunction{Name: pc.Name, Arguments: pc.Arguments.String()},
						})
					}
					pendingCalls = make(map[int]*pendingToolCall)
				}

				// Mistral commonly sends finish_reason on a terminal frame with
				// no content. Forward that frame so the runtime can distinguish
				// stop, length, and tool-call completion reliably.
				if len(parts) > 0 || len(msg.ToolCalls) > 0 || ch.FinishReason != "" {
					chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
						Index: ch.Index, Delta: msg, FinishReason: ch.FinishReason,
					})
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

// mistralFinalText accepts both the normal string content and the chunked
// response used by adjustable-reasoning models. Thinking chunks are
// intentionally omitted from Everstack's user-facing assistant text.
func mistralFinalText(content json.RawMessage) string {
	var final strings.Builder
	for _, part := range mistralContentParts(content, false) {
		if part.Type == "text" && part.Text != nil {
			final.WriteString(*part.Text)
		}
	}
	return final.String()
}

// Embed generates embeddings using Mistral
func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	mistralReq := map[string]interface{}{
		"model": req.Model,
		"input": []string{req.Input},
	}

	body, _ := json.Marshal(mistralReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	var mistralResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&mistralResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	if len(mistralResp.Data) > 0 {
		return gw.EmbeddingsResponse{Embedding: mistralResp.Data[0].Embedding}, nil
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
