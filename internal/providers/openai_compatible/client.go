package openai_compatible

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	providerutil "github.com/everstacklabs/everstack/internal/providers"
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
)

// Provider implements an OpenAI-compatible LLM provider
type Provider struct {
	name   string
	cfg    Config
	client *http.Client

	// unaryClient serves non-streaming calls. It omits the shared transport's
	// ResponseHeaderTimeout, which would otherwise cap total generation time,
	// since a unary upstream withholds response headers until the completion
	// is finished.
	unaryClient *http.Client
}

// Config holds OpenAI-compatible provider configuration
type Config struct {
	ProviderName    string
	APIKey          string
	BaseURL         string
	AuthHeaderName  string
	AuthHeaderValue string
	SupportedModels []string
}

// NewProvider creates a new OpenAI-compatible provider
func NewProvider(cfg Config) *Provider {
	cli := clientx.Default()
	return &Provider{
		name:        cfg.ProviderName,
		cfg:         cfg,
		client:      cli,
		unaryClient: clientx.Unary(),
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return p.name
}

// Wire types for OpenAI-compatible API
type openaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCalls  []gw.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type openaiChatReq struct {
	Tools             []gw.ToolDefinition `json:"tools,omitempty"`
	ToolChoice        interface{}         `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls,omitempty"`
	Stop              []string            `json:"stop,omitempty"`
	User              string              `json:"user,omitempty"`
	Model             string              `json:"model"`
	Messages          []openaiMessage     `json:"messages"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	MaxTokens         int                 `json:"max_tokens,omitempty"`
	ReasoningEffort   string              `json:"reasoning_effort,omitempty"`
	FrequencyPenalty  float64             `json:"frequency_penalty,omitempty"`
	PresencePenalty   float64             `json:"presence_penalty,omitempty"`
	TopK              *int                `json:"top_k,omitempty"`
	Seed              *int64              `json:"seed,omitempty"`
}

type openaiChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []gw.ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage wireUsage `json:"usage"`
}

// wireUsage is the usage block of an OpenAI-compatible response.
type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

// toUsage maps wire usage onto the gateway's OpenAI semantics, where
// reasoning tokens are part of CompletionTokens.
//
// Providers disagree on that. OpenAI counts reasoning inside
// completion_tokens (total = prompt + completion). xAI counts it outside:
// its documented responses report completion_tokens 9, reasoning_tokens 110
// and total_tokens 151 for a 32-token prompt
// (https://docs.x.ai/developers/rest-api-reference/inference/responses), and
// bills reasoning at the completion rate
// (https://docs.x.ai/developers/advanced-api-usage/prompt-caching/usage-and-pricing).
// Reading completion_tokens alone would bill xAI reasoning at zero, so the
// reasoning count is folded in when total_tokens shows it was excluded.
func (w wireUsage) toUsage() gw.Usage {
	u := gw.Usage{
		PromptTokens:     w.PromptTokens,
		CompletionTokens: w.CompletionTokens,
		TotalTokens:      w.TotalTokens,
	}
	if w.PromptTokensDetails != nil {
		u.PromptDetails = CachedPromptDetails(w.PromptTokensDetails.CachedTokens)
	}
	if w.CompletionTokensDetails == nil || w.CompletionTokensDetails.ReasoningTokens <= 0 {
		return u
	}
	reasoning := w.CompletionTokensDetails.ReasoningTokens
	if w.TotalTokens == w.PromptTokens+w.CompletionTokens+reasoning {
		u.CompletionTokens += reasoning
	}
	u.CompletionDetails = &gw.TokenDetails{ReasoningTokens: reasoning}
	return u
}

func optionalTopP(sampling gw.SamplingParams) *float64 {
	if !sampling.TopPConfigured && sampling.TopP == 0 {
		return nil
	}
	value := sampling.TopP
	return &value
}

func convertMessages(msgs []gw.Message) []openaiMessage {
	messages := make([]openaiMessage, 0, len(msgs))
	for _, m := range msgs {
		messages = append(messages, openaiMessage{
			Role:       string(m.Role),
			Content:    WireContent(m.Content),
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	return messages
}

// Chat sends a chat completion request (OpenAI-compatible format)
func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	messages := convertMessages(req.Messages)

	openaiReq := openaiChatReq{
		Model:    req.Model,
		Messages: messages,
	}

	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		openaiReq.Temperature = &temperature
	}
	openaiReq.MaxTokens = MaxOutputTokens(req.Sampling)
	openaiReq.Tools = req.Tools
	openaiReq.ToolChoice = req.ToolChoice
	openaiReq.ParallelToolCalls = req.ParallelToolCalls
	openaiReq.Stop = req.Sampling.Stop
	if user, ok := req.Metadata["user"].(string); ok {
		openaiReq.User = user
	}
	openaiReq.TopP = optionalTopP(req.Sampling)
	openaiReq.ReasoningEffort = req.Sampling.ReasoningEffort
	openaiReq.TopK = req.Sampling.TopK
	openaiReq.Seed = req.Sampling.Seed
	openaiReq.FrequencyPenalty = req.Sampling.FrequencyPenalty
	openaiReq.PresencePenalty = req.Sampling.PresencePenalty

	body, _ := json.Marshal(openaiReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling))
	defer cancel()

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))

	// Set auth header
	authValue := p.cfg.AuthHeaderValue
	if authValue == "" {
		authValue = "Bearer " + p.cfg.APIKey
	} else {
		authValue = strings.ReplaceAll(authValue, "{api_key}", p.cfg.APIKey)
	}

	headerName := p.cfg.AuthHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}

	httpReq.Header.Set(headerName, authValue)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", p.name,
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", req.Model,
		"stream", false,
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.unaryClient.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.name, req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		return gw.ChatCompletionResponse{}, UpstreamError(p.name, resp)
	}

	var openaiResp openaiChatResp
	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	var content string
	if len(openaiResp.Choices) > 0 {
		content = openaiResp.Choices[0].Message.Content
	}

	choice := gw.Choice{
		Index:   0,
		Message: gw.NewMessage(gw.RoleAssistant, gw.Text(content)),
	}

	if len(openaiResp.Choices) > 0 {
		choice.Message.ToolCalls = openaiResp.Choices[0].Message.ToolCalls
		choice.FinishReason = openaiResp.Choices[0].FinishReason
		if len(choice.Message.ToolCalls) > 0 && (choice.FinishReason == "" || choice.FinishReason == "stop") {
			choice.FinishReason = "tool_calls"
		}
	}
	usage := openaiResp.Usage.toUsage()

	return gw.NewChatResponse(openaiResp.ID, openaiResp.Model, []gw.Choice{choice}, usage), nil
}

// openaiStreamResp is the wire type for OpenAI-compatible streaming chunks.
type openaiStreamResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []gw.ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage,omitempty"`
}

// ChatStream implements streaming for OpenAI-compatible providers (OpenRouter, HuggingFace, etc.).
func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	messages := convertMessages(req.Messages)

	type streamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	type streamReq struct {
		openaiChatReq
		Stream        bool           `json:"stream"`
		StreamOptions *streamOptions `json:"stream_options,omitempty"`
	}
	oReq := streamReq{
		openaiChatReq: openaiChatReq{
			Model:    req.Model,
			Messages: messages,
		},
		Stream: true,
		// Ask for the final usage chunk: without it a platform-key stream
		// can only be billed on an estimate.
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		oReq.Temperature = &temperature
	}
	oReq.MaxTokens = MaxOutputTokens(req.Sampling)
	oReq.Tools = req.Tools
	oReq.ToolChoice = req.ToolChoice
	oReq.ParallelToolCalls = req.ParallelToolCalls
	oReq.Stop = req.Sampling.Stop
	if user, ok := req.Metadata["user"].(string); ok {
		oReq.User = user
	}
	oReq.TopP = optionalTopP(req.Sampling)
	oReq.ReasoningEffort = req.Sampling.ReasoningEffort
	oReq.TopK = req.Sampling.TopK
	oReq.Seed = req.Sampling.Seed
	oReq.FrequencyPenalty = req.Sampling.FrequencyPenalty
	oReq.PresencePenalty = req.Sampling.PresencePenalty

	resp, err := p.sendStream(ctx, oReq, req.Model)
	if err == nil && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) {
		// An upstream that rejects stream_options gets one retry without
		// it; the billing seam then settles an estimate for the stream.
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(strings.ToLower(string(bodyBytes)), "stream_options") {
			return gw.NewUpstreamError(p.name, resp.StatusCode, bodyBytes)
		}
		oReq.StreamOptions = nil
		resp, err = p.sendStream(ctx, oReq, req.Model)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.name, req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		return UpstreamError(p.name, resp)
	}

	sawFinish := false
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

			var partial openaiStreamResp
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}

			chunk := gw.NewChatChunk(partial.ID, partial.Model, nil)
			if partial.Usage != nil {
				usage := partial.Usage.toUsage()
				chunk.Usage = &usage
			}
			for _, ch := range partial.Choices {
				if ch.FinishReason != "" {
					sawFinish = true
				}
				if ch.Delta.Content != "" || len(ch.Delta.ToolCalls) > 0 || ch.FinishReason != "" {
					delta := gw.ChoiceDelta{
						Index:        ch.Index,
						Delta:        gw.NewMessage(gw.RoleAssistant, gw.Text(ch.Delta.Content)),
						FinishReason: ch.FinishReason,
					}
					delta.Delta.ToolCalls = ch.Delta.ToolCalls
					chunk.Choices = append(chunk.Choices, delta)
				}
			}
			if err := onChunk(chunk); err != nil {
				return err
			}
		}
		if err != nil {
			if err == io.EOF {
				if !sawFinish {
					// Cut off before any finish_reason: an aborted stream,
					// not a completed one. Servers that omit [DONE] but did
					// send a finish_reason still end cleanly.
					return ErrStreamTruncated(p.name)
				}
				return nil
			}
			return err
		}
	}
	if !sawFinish {
		return ErrStreamTruncated(p.name)
	}
	return nil
}

// Embed generates embeddings (OpenAI-compatible format)
func (p *Provider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	openaiReq := map[string]interface{}{
		"model": req.Model,
		"input": req.Input,
	}

	body, _ := json.Marshal(openaiReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/embeddings", bytes.NewReader(body))

	authValue := p.cfg.AuthHeaderValue
	if authValue == "" {
		authValue = "Bearer " + p.cfg.APIKey
	} else {
		authValue = strings.ReplaceAll(authValue, "{api_key}", p.cfg.APIKey)
	}

	headerName := p.cfg.AuthHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}

	httpReq.Header.Set(headerName, authValue)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return gw.EmbeddingsResponse{}, UpstreamError(p.name, resp)
	}

	var openaiResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	var first []float64
	if len(openaiResp.Data) > 0 {
		first = openaiResp.Data[0].Embedding
	}
	vec, err := NonEmptyEmbedding(p.name, first)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	return gw.EmbeddingsResponse{Embedding: vec}, nil
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

// sendStream issues one streaming chat request with payload.
func (p *Provider) sendStream(ctx context.Context, payload any, model string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	authValue := p.cfg.AuthHeaderValue
	if authValue == "" {
		authValue = "Bearer " + p.cfg.APIKey
	} else {
		authValue = strings.ReplaceAll(authValue, "{api_key}", p.cfg.APIKey)
	}
	headerName := p.cfg.AuthHeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	httpReq.Header.Set(headerName, authValue)
	httpReq.Header.Set("Content-Type", "application/json")

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", p.name,
		"endpoint", p.cfg.BaseURL+"/chat/completions",
		"model", model,
		"stream", true,
		"correlation_id", cid,
	).Info("provider stream request issued")

	return p.client.Do(httpReq)
}
