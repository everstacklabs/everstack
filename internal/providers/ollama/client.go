package ollama

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
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/openai_compatible"
)

// Provider implements the Ollama local LLM provider
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Ollama provider
func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		cfg:    cfg,
		client: cli,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "ollama"
}

// --- Wire types for Ollama API ---

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	// Images are raw base64 strings without a data: prefix
	// (https://github.com/ollama/ollama/blob/main/docs/api.md).
	Images []string `json:"images,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunc `json:"function"`
}

type ollamaToolCallFunc struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ollamaTool struct {
	Type     string            `json:"type"`
	Function ollamaToolFuncDef `json:"function"`
}

type ollamaToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type ollamaChatReq struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Tools    []ollamaTool           `json:"tools,omitempty"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaChatResp struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

func (r ollamaChatResp) usage() gw.Usage {
	return gw.Usage{
		PromptTokens:     r.PromptEvalCount,
		CompletionTokens: r.EvalCount,
		TotalTokens:      r.PromptEvalCount + r.EvalCount,
	}
}

// finishReason maps Ollama's done_reason onto OpenAI finish reasons.
func finishReason(doneReason string, sawToolCalls bool) string {
	if sawToolCalls {
		return "tool_calls"
	}
	if doneReason == "" {
		return "stop"
	}
	return doneReason
}

// chatOptions maps sampling parameters onto Ollama's options object.
func chatOptions(s gw.SamplingParams) map[string]interface{} {
	opts := map[string]interface{}{}
	if s.Temperature > 0 || s.TemperatureConfigured {
		opts["temperature"] = s.Temperature
	}
	if n := openai_compatible.MaxOutputTokens(s); n > 0 {
		opts["num_predict"] = n
	}
	if len(s.Stop) > 0 {
		opts["stop"] = s.Stop
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

// imageBase64 extracts the raw base64 payload of a data: URL. Ollama cannot
// fetch remote URLs, and the gateway must not fetch them on a caller's behalf.
func imageBase64(url string) (string, error) {
	if !strings.HasPrefix(url, "data:") {
		return "", fmt.Errorf("ollama accepts only base64 data: image URLs, not remote URLs")
	}
	_, payload, ok := strings.Cut(url, ",")
	if !ok || payload == "" {
		return "", fmt.Errorf("ollama image data URL has no payload")
	}
	return payload, nil
}

// --- Conversion helpers ---

func convertMessages(msgs []gw.Message) ([]ollamaMessage, error) {
	out := make([]ollamaMessage, 0, len(msgs))
	for _, m := range msgs {
		om := ollamaMessage{Role: string(m.Role)}

		// Ollama uses role "tool" for tool responses
		if m.ToolCallID != "" {
			om.Role = "tool"
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					om.Content = *c.Text
					break
				}
			}
			out = append(out, om)
			continue
		}

		// Assistant message with tool calls
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				// Ollama expects arguments as a map, not a JSON string
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				om.ToolCalls = append(om.ToolCalls, ollamaToolCall{
					Function: ollamaToolCallFunc{Name: tc.Function.Name, Arguments: args},
				})
			}
		}

		for _, c := range m.Content {
			switch {
			case c.Type == "text" && c.Text != nil && om.Content == "":
				om.Content = *c.Text
			case c.Type == "image_url" && c.ImageURL != nil:
				b64, err := imageBase64(*c.ImageURL)
				if err != nil {
					return nil, err
				}
				om.Images = append(om.Images, b64)
			}
		}
		out = append(out, om)
	}
	return out, nil
}

func convertTools(tools []gw.ToolDefinition) []ollamaTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ollamaTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, ollamaTool{
			Type: t.Type,
			Function: ollamaToolFuncDef{
				Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			},
		})
	}
	return out
}

// --- Chat (non-streaming) ---

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	msgs, err := convertMessages(req.Messages)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	ollamaReq := ollamaChatReq{
		Model:    req.Model,
		Messages: msgs,
		Tools:    convertTools(req.Tools),
		Stream:   false,
		Options:  chatOptions(req.Sampling),
	}

	body, _ := json.Marshal(ollamaReq)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second) // Ollama can be slower
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.cfg.APIKey))
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "ollama",
		"endpoint", p.cfg.BaseURL+"/api/chat",
		"model", req.Model,
		"stream", false,
		"tools", len(ollamaReq.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return gw.ChatCompletionResponse{}, openai_compatible.UpstreamError("ollama", resp)
	}

	var ollamaResp ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	msg := gw.NewMessage(gw.RoleAssistant, gw.Text(ollamaResp.Message.Content))

	// Convert Ollama tool calls to gateway format
	for i, tc := range ollamaResp.Message.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Function.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, gw.ToolCall{
			ID:   fmt.Sprintf("call_%d", i),
			Type: "function",
			Function: gw.ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	return gw.NewChatResponse(fmt.Sprintf("ollama-%s", ollamaResp.CreatedAt), ollamaResp.Model, []gw.Choice{{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason(ollamaResp.DoneReason, len(msg.ToolCalls) > 0),
	}}, ollamaResp.usage()), nil
}

// --- Chat (streaming) ---
// Ollama uses NDJSON: each line is a complete JSON object when stream=true.
func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	msgs, err := convertMessages(req.Messages)
	if err != nil {
		return err
	}
	ollamaReq := ollamaChatReq{
		Model:    req.Model,
		Messages: msgs,
		Tools:    convertTools(req.Tools),
		Stream:   true,
		Options:  chatOptions(req.Sampling),
	}

	body, _ := json.Marshal(ollamaReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.cfg.APIKey))
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "ollama",
		"endpoint", p.cfg.BaseURL+"/api/chat",
		"model", req.Model,
		"stream", true,
		"tools", len(ollamaReq.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return openai_compatible.UpstreamError("ollama", resp)
	}

	// Ollama streams NDJSON: one JSON object per line.
	reader := bufio.NewReader(resp.Body)
	chunkID := fmt.Sprintf("ollama-%d", time.Now().Unix())
	sawToolCalls := false

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(line)
			if s == "" {
				if err != nil {
					break
				}
				continue
			}

			var partial ollamaChatResp
			if jsonErr := json.Unmarshal([]byte(s), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}

			// Handle tool calls in streaming (Ollama sends them in a single message)
			if len(partial.Message.ToolCalls) > 0 {
				var toolCalls []gw.ToolCall
				for i, tc := range partial.Message.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Function.Arguments)
					toolCalls = append(toolCalls, gw.ToolCall{
						ID:   fmt.Sprintf("call_%d", i),
						Type: "function",
						Function: gw.ToolCallFunction{
							Name:      tc.Function.Name,
							Arguments: string(argsJSON),
						},
					})
				}
				msg := gw.NewMessage(gw.RoleAssistant, gw.Text(""))
				msg.ToolCalls = toolCalls
				sawToolCalls = true
				// The finish reason rides on the done frame, once.
				chunk := gw.NewChatChunk(chunkID, partial.Model, []gw.ChoiceDelta{{
					Index: 0, Delta: msg,
				}})
				if err := onChunk(chunk); err != nil {
					return err
				}
			}

			// Handle text content
			text := partial.Message.Content
			if text != "" {
				delta := gw.ChoiceDelta{
					Index: 0,
					Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(text)),
				}
				chunk := gw.NewChatChunk(chunkID, partial.Model, []gw.ChoiceDelta{delta})
				if err := onChunk(chunk); err != nil {
					return err
				}
			}

			// Ollama signals end with "done": true
			if partial.Done {
				final := gw.NewChatChunk(chunkID, partial.Model, []gw.ChoiceDelta{{
					Index:        0,
					Delta:        gw.NewMessage(gw.RoleAssistant, gw.Text("")),
					FinishReason: finishReason(partial.DoneReason, sawToolCalls),
				}})
				usage := partial.usage()
				final.Usage = &usage
				return onChunk(final)
			}
		}
		if err != nil {
			if err == io.EOF {
				return openai_compatible.ErrStreamTruncated("ollama")
			}
			return err
		}
	}
	return openai_compatible.ErrStreamTruncated("ollama")
}

// Embed generates embeddings using Ollama
func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	ollamaReq := map[string]interface{}{
		"model":  req.Model,
		"prompt": req.Input,
	}

	body, _ := json.Marshal(ollamaReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/api/embeddings", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.cfg.APIKey))
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return gw.EmbeddingsResponse{}, openai_compatible.UpstreamError("ollama", resp)
	}

	var ollamaResp struct {
		Embedding []float64 `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	vec, err := openai_compatible.NonEmptyEmbedding("ollama", ollamaResp.Embedding)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	return gw.EmbeddingsResponse{Embedding: vec}, nil
}

// SupportsModel checks if a model is supported
func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return true // Ollama supports any locally available model
	}
	for _, m := range p.cfg.SupportedModels {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}
