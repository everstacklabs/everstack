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
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunc `json:"function"`
}

type ollamaToolCallFunc struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ollamaTool struct {
	Type     string              `json:"type"`
	Function ollamaToolFuncDef   `json:"function"`
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
	Done bool `json:"done"`
}

// --- Conversion helpers ---

func convertMessages(msgs []gw.Message) []ollamaMessage {
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
			if c.Type == "text" && c.Text != nil {
				om.Content = *c.Text
				break
			}
		}
		out = append(out, om)
	}
	return out
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
	ollamaReq := ollamaChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   false,
	}

	if req.Sampling.Temperature > 0 {
		ollamaReq.Options = map[string]interface{}{
			"temperature": req.Sampling.Temperature,
		}
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("ollama error: %s", string(bodyBytes))
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

	usage := gw.Usage{
		PromptTokens:     0,
		CompletionTokens: 0,
		TotalTokens:      0,
	}

	return gw.NewChatResponse(fmt.Sprintf("ollama-%s", ollamaResp.CreatedAt), ollamaResp.Model, []gw.Choice{{Index: 0, Message: msg}}, usage), nil
}

// --- Chat (streaming) ---
// Ollama uses NDJSON: each line is a complete JSON object when stream=true.
func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	ollamaReq := ollamaChatReq{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
		Stream:   true,
	}
	if req.Sampling.Temperature > 0 {
		ollamaReq.Options = map[string]interface{}{
			"temperature": req.Sampling.Temperature,
		}
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama stream error: %s", string(bodyBytes))
	}

	// Ollama streams NDJSON: one JSON object per line.
	reader := bufio.NewReader(resp.Body)
	chunkID := fmt.Sprintf("ollama-%d", time.Now().Unix())

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
				chunk := gw.NewChatChunk(chunkID, partial.Model, []gw.ChoiceDelta{{
					Index: 0, Delta: msg, FinishReason: "tool_calls",
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
				return nil
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

	var ollamaResp struct {
		Embedding []float64 `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	return gw.EmbeddingsResponse{Embedding: ollamaResp.Embedding}, nil
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
