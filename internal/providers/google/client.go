package google

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

// Provider implements the Google (Gemini) LLM provider
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Google provider
func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		cfg:    cfg,
		client: cli,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "gemini"
}

// --- Wire types for Google Gemini API ---

type geminiPart struct {
	Text         string                  `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResp *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations,omitempty"`
}

type geminiFuncDecl struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type geminiChatReq struct {
	Contents         []geminiContent        `json:"contents"`
	Tools            []geminiToolDecl       `json:"tools,omitempty"`
	GenerationConfig map[string]interface{} `json:"generationConfig,omitempty"`
}

type geminiChatResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string              `json:"text,omitempty"`
				FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		TotalTokenCount         int `json:"totalTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
		ThoughtTokenCount       int `json:"thoughtTokenCount,omitempty"`
	} `json:"usageMetadata"`
}

// --- Conversion helpers ---

func convertMessages(msgs []gw.Message) []geminiContent {
	out := make([]geminiContent, 0, len(msgs))
	for _, m := range msgs {
		role := convertRole(string(m.Role))

		// Tool response messages
		if m.ToolCallID != "" {
			var textContent string
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil {
					textContent = *c.Text
					break
				}
			}
			// Parse tool call ID to extract function name (stored as "funcName" in our system)
			// Gemini needs the function name, not an ID. We extract it from the content or use a fallback.
			// The tool_call_id in our system is an opaque ID. We need the function name from context.
			// For now, use the tool_call_id as a best-effort function name.
			var respData map[string]interface{}
			if err := json.Unmarshal([]byte(textContent), &respData); err != nil {
				respData = map[string]interface{}{"result": textContent}
			}
			out = append(out, geminiContent{
				Role: "function",
				Parts: []geminiPart{{
					FunctionResp: &geminiFunctionResponse{
						Name:     m.ToolCallID, // Best-effort: may need mapping
						Response: respData,
					},
				}},
			})
			continue
		}

		// Assistant message with tool calls
		if len(m.ToolCalls) > 0 {
			var parts []geminiPart
			// Add text part if present
			for _, c := range m.Content {
				if c.Type == "text" && c.Text != nil && *c.Text != "" {
					parts = append(parts, geminiPart{Text: *c.Text})
					break
				}
			}
			// Add function call parts
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			out = append(out, geminiContent{Role: role, Parts: parts})
			continue
		}

		// Regular text message
		var textContent string
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				textContent = *c.Text
				break
			}
		}
		out = append(out, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: textContent}},
		})
	}
	return out
}

func convertTools(tools []gw.ToolDefinition) []geminiToolDecl {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFuncDecl, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, geminiFuncDecl{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}
}

// --- Chat (non-streaming) ---

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	genConfig := make(map[string]interface{})
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		genConfig["temperature"] = req.Sampling.Temperature
	}
	if req.Sampling.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.Sampling.MaxTokens
	}
	applySamplingWindow(genConfig, req.Sampling)
	applyThinkingConfig(genConfig, req.Sampling)

	geminiReq := geminiChatReq{
		Contents:         convertMessages(req.Messages),
		Tools:            convertTools(req.Tools),
		GenerationConfig: genConfig,
	}

	body, _ := json.Marshal(geminiReq)
	url := fmt.Sprintf("%s/models/%s:generateContent", p.cfg.BaseURL, req.Model)

	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("x-goog-api-key", p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "google",
		"endpoint", url,
		"model", req.Model,
		"stream", false,
		"tools", len(req.Tools),
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "google", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("google error: %s", string(bodyBytes))
	}

	var geminiResp geminiChatResp
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var content string
	var toolCalls []gw.ToolCall
	if len(geminiResp.Candidates) > 0 {
		for i, part := range geminiResp.Candidates[0].Content.Parts {
			if part.Text != "" {
				content += part.Text
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, gw.ToolCall{
					ID:   fmt.Sprintf("call_%d", i),
					Type: "function",
					Function: gw.ToolCallFunction{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
	}

	msg := gw.NewMessage(gw.RoleAssistant, gw.Text(content))
	msg.ToolCalls = toolCalls

	usage := gw.Usage{
		PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
		CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
	}

	if geminiResp.UsageMetadata.CachedContentTokenCount > 0 {
		usage.PromptDetails = &gw.TokenDetails{
			CachedTokens: geminiResp.UsageMetadata.CachedContentTokenCount,
			TextTokens:   geminiResp.UsageMetadata.PromptTokenCount - geminiResp.UsageMetadata.CachedContentTokenCount,
		}
	}
	if geminiResp.UsageMetadata.ThoughtTokenCount > 0 {
		if usage.CompletionDetails == nil {
			usage.CompletionDetails = &gw.TokenDetails{}
		}
		usage.CompletionDetails.ReasoningTokens = geminiResp.UsageMetadata.ThoughtTokenCount
		usage.CompletionDetails.TextTokens = geminiResp.UsageMetadata.CandidatesTokenCount - geminiResp.UsageMetadata.ThoughtTokenCount
	}

	return gw.NewChatResponse(fmt.Sprintf("google-%d", time.Now().Unix()), req.Model, []gw.Choice{{Index: 0, Message: msg}}, usage), nil
}

// --- Chat (streaming) ---

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	genConfig := make(map[string]interface{})
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		genConfig["temperature"] = req.Sampling.Temperature
	}
	if req.Sampling.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.Sampling.MaxTokens
	}
	applySamplingWindow(genConfig, req.Sampling)
	applyThinkingConfig(genConfig, req.Sampling)

	geminiReq := geminiChatReq{
		Contents:         convertMessages(req.Messages),
		Tools:            convertTools(req.Tools),
		GenerationConfig: genConfig,
	}

	body, _ := json.Marshal(geminiReq)
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.cfg.BaseURL, req.Model)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("x-goog-api-key", p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", "google",
		"endpoint", url,
		"model", req.Model,
		"stream", true,
		"tools", len(req.Tools),
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, "google", req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("google stream error: %s", string(bodyBytes))
	}

	reader := bufio.NewReader(resp.Body)
	chunkID := fmt.Sprintf("google-%d", time.Now().Unix())

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

			var partial geminiChatResp
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}

			chunk := gw.NewChatChunk(chunkID, req.Model, nil)

			if len(partial.Candidates) > 0 {
				for i, part := range partial.Candidates[0].Content.Parts {
					// Text content
					if part.Text != "" {
						chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
							Index: 0,
							Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(part.Text)),
						})
					}
					// Function call
					if part.FunctionCall != nil {
						argsJSON, _ := json.Marshal(part.FunctionCall.Args)
						msg := gw.NewMessage(gw.RoleAssistant, gw.Text(""))
						msg.ToolCalls = []gw.ToolCall{{
							ID:   fmt.Sprintf("call_%d", i),
							Type: "function",
							Function: gw.ToolCallFunction{
								Name:      part.FunctionCall.Name,
								Arguments: string(argsJSON),
							},
						}}
						chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
							Index:        0,
							Delta:        msg,
							FinishReason: "tool_calls",
						})
					}
				}
			}

			// Add usage from chunk if available
			if partial.UsageMetadata.TotalTokenCount > 0 {
				chunk.Usage = &gw.Usage{
					PromptTokens:     partial.UsageMetadata.PromptTokenCount,
					CompletionTokens: partial.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      partial.UsageMetadata.TotalTokenCount,
				}
				if partial.UsageMetadata.CachedContentTokenCount > 0 {
					chunk.Usage.PromptDetails = &gw.TokenDetails{
						CachedTokens: partial.UsageMetadata.CachedContentTokenCount,
					}
				}
				if partial.UsageMetadata.ThoughtTokenCount > 0 {
					chunk.Usage.CompletionDetails = &gw.TokenDetails{
						ReasoningTokens: partial.UsageMetadata.ThoughtTokenCount,
					}
				}
			}

			if len(chunk.Choices) > 0 || chunk.Usage != nil {
				if err := onChunk(chunk); err != nil {
					return err
				}
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

// Embed generates embeddings using Google
func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	geminiReq := map[string]interface{}{
		"content": map[string]interface{}{
			"parts": []map[string]interface{}{{"text": req.Input}},
		},
	}

	body, _ := json.Marshal(geminiReq)
	url := fmt.Sprintf("%s/models/%s:embedContent", p.cfg.BaseURL, req.Model)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("x-goog-api-key", p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	var geminiResp struct {
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	return gw.EmbeddingsResponse{Embedding: geminiResp.Embedding.Values}, nil
}

// applySamplingWindow adds the nucleus and top-k controls Gemini accepts under
// generationConfig. Both are camelCase there, unlike the OpenAI-shaped APIs.
func applySamplingWindow(genConfig map[string]interface{}, sampling gw.SamplingParams) {
	if sampling.TopP > 0 || sampling.TopPConfigured {
		genConfig["topP"] = sampling.TopP
	}
	if sampling.TopK != nil {
		genConfig["topK"] = *sampling.TopK
	}
}

func applyThinkingConfig(config map[string]interface{}, sampling gw.SamplingParams) {
	thinking := make(map[string]interface{})
	if sampling.ReasoningEffort != "" {
		thinking["thinkingLevel"] = strings.ToLower(strings.TrimSpace(sampling.ReasoningEffort))
	} else if sampling.ReasoningBudget != nil {
		thinking["thinkingBudget"] = *sampling.ReasoningBudget
	} else if sampling.ReasoningEnabled != nil {
		if *sampling.ReasoningEnabled {
			thinking["thinkingBudget"] = -1
		} else {
			thinking["thinkingBudget"] = 0
		}
	}
	if len(thinking) > 0 {
		config["thinkingConfig"] = thinking
	}
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

// convertRole converts OpenAI-style roles to Gemini roles
func convertRole(role string) string {
	switch role {
	case "system", "user":
		return "user"
	case "assistant":
		return "model"
	default:
		return "user"
	}
}
