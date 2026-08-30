package deepseek

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
	return "deepseek"
}

// --- Wire types (OpenAI-compatible) ---

type dsMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []dsToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type dsToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function dsToolCallFunc `json:"function"`
}

type dsToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type dsTool struct {
	Type     string        `json:"type"`
	Function dsToolFuncDef `json:"function"`
}

type dsToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type dsChatReq struct {
	Model           string      `json:"model"`
	Messages        []dsMessage `json:"messages"`
	Tools           []dsTool    `json:"tools,omitempty"`
	Temperature     *float64    `json:"temperature,omitempty"`
	TopP            *float64    `json:"top_p,omitempty"`
	MaxTokens       int         `json:"max_tokens,omitempty"`
	ReasoningEffort string      `json:"reasoning_effort,omitempty"`
	Stream          bool        `json:"stream,omitempty"`
}

type dsChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string       `json:"content"`
			ToolCalls []dsToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// --- Conversion helpers ---

func convertMessages(msgs []gw.Message) []dsMessage {
	out := make([]dsMessage, 0, len(msgs))
	for _, m := range msgs {
		dm := dsMessage{Role: string(m.Role)}

		if m.ToolCallID != "" {
			dm.ToolCallID = m.ToolCallID
			dm.Role = "tool"
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					dm.Content = *c.Text
					break
				}
			}
			out = append(out, dm)
			continue
		}

		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				dm.ToolCalls = append(dm.ToolCalls, dsToolCall{
					ID: tc.ID, Type: tc.Type,
					Function: dsToolCallFunc{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
				})
			}
		}

		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				dm.Content = *c.Text
				break
			}
		}
		out = append(out, dm)
	}
	return out
}

func convertTools(tools []gw.ToolDefinition) []dsTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]dsTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, dsTool{
			Type: t.Type,
			Function: dsToolFuncDef{
				Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			},
		})
	}
	return out
}

// --- Chat (non-streaming) ---

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	dsReq := dsChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		dsReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		topP := req.Sampling.TopP
		dsReq.TopP = &topP
	}
	if req.Sampling.MaxTokens > 0 {
		dsReq.MaxTokens = req.Sampling.MaxTokens
	}
	dsReq.ReasoningEffort = req.Sampling.ReasoningEffort

	body, _ := json.Marshal(dsReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "deepseek",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", false,
		"tools", len(dsReq.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "deepseek", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("deepseek error: %s", string(bodyBytes))
	}

	var dsResp dsChatResp
	if err := json.NewDecoder(resp.Body).Decode(&dsResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var content string
	var toolCalls []gw.ToolCall
	if len(dsResp.Choices) > 0 {
		content = dsResp.Choices[0].Message.Content
		for _, tc := range dsResp.Choices[0].Message.ToolCalls {
			toolCalls = append(toolCalls, gw.ToolCall{
				ID: tc.ID, Type: tc.Type,
				Function: gw.ToolCallFunction{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
	}

	msg := gw.NewMessage(gw.RoleAssistant, gw.Text(content))
	msg.ToolCalls = toolCalls

	usage := gw.Usage{
		PromptTokens:     dsResp.Usage.PromptTokens,
		CompletionTokens: dsResp.Usage.CompletionTokens,
		TotalTokens:      dsResp.Usage.TotalTokens,
	}

	return gw.NewChatResponse(dsResp.ID, dsResp.Model, []gw.Choice{{Index: 0, Message: msg}}, usage), nil
}

// --- Chat (streaming) ---

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	dsReq := dsChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   true,
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		dsReq.Temperature = &temperature
	}
	if req.Sampling.TopP > 0 || req.Sampling.TopPConfigured {
		topP := req.Sampling.TopP
		dsReq.TopP = &topP
	}
	if req.Sampling.MaxTokens > 0 {
		dsReq.MaxTokens = req.Sampling.MaxTokens
	}
	dsReq.ReasoningEffort = req.Sampling.ReasoningEffort

	body, _ := json.Marshal(dsReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "deepseek",
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", true,
		"tools", len(dsReq.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "deepseek", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deepseek stream error: %s", string(bodyBytes))
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

				if ch.Delta.Content != "" {
					chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
						Index: ch.Index, Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(ch.Delta.Content)), FinishReason: ch.FinishReason,
					})
				}

				if ch.FinishReason != "" && len(pendingCalls) > 0 {
					var toolCalls []gw.ToolCall
					for _, pc := range pendingCalls {
						toolCalls = append(toolCalls, gw.ToolCall{
							ID: pc.ID, Type: pc.Type,
							Function: gw.ToolCallFunction{Name: pc.Name, Arguments: pc.Arguments.String()},
						})
					}
					msg := gw.NewMessage(gw.RoleAssistant, gw.Text(""))
					msg.ToolCalls = toolCalls
					chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
						Index: ch.Index, Delta: msg, FinishReason: ch.FinishReason,
					})
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

func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	deepseekReq := map[string]interface{}{
		"model": req.Model,
		"input": []string{req.Input},
	}

	body, _ := json.Marshal(deepseekReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	var deepseekResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&deepseekResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	if len(deepseekResp.Data) > 0 {
		return gw.EmbeddingsResponse{Embedding: deepseekResp.Data[0].Embedding}, nil
	}

	return gw.EmbeddingsResponse{Embedding: []float64{}}, nil
}

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
