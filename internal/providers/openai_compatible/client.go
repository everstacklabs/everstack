package openai_compatible

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

// Provider implements an OpenAI-compatible LLM provider
type Provider struct {
	name   string
	cfg    Config
	client *http.Client
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
		name:   cfg.ProviderName,
		cfg:    cfg,
		client: cli,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return p.name
}

// Wire types for OpenAI-compatible API
type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatReq struct {
	Model            string          `json:"model"`
	Messages         []openaiMessage `json:"messages"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	FrequencyPenalty float64         `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64         `json:"presence_penalty,omitempty"`
	TopK             *int            `json:"top_k,omitempty"`
	Seed             *int64          `json:"seed,omitempty"`
}

type openaiChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func optionalTopP(sampling gw.SamplingParams) *float64 {
	if !sampling.TopPConfigured && sampling.TopP == 0 {
		return nil
	}
	value := sampling.TopP
	return &value
}

// Chat sends a chat completion request (OpenAI-compatible format)
func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	messages := make([]openaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		var textContent string
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				textContent = *c.Text
				break
			}
		}
		messages = append(messages, openaiMessage{
			Role:    string(m.Role),
			Content: textContent,
		})
	}

	openaiReq := openaiChatReq{
		Model:    req.Model,
		Messages: messages,
	}

	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		openaiReq.Temperature = &temperature
	}
	if req.Sampling.MaxTokens > 0 {
		openaiReq.MaxTokens = req.Sampling.MaxTokens
	}
	openaiReq.TopP = optionalTopP(req.Sampling)
	openaiReq.ReasoningEffort = req.Sampling.ReasoningEffort
	openaiReq.TopK = req.Sampling.TopK
	openaiReq.Seed = req.Sampling.Seed
	openaiReq.FrequencyPenalty = req.Sampling.FrequencyPenalty
	openaiReq.PresencePenalty = req.Sampling.PresencePenalty

	body, _ := json.Marshal(openaiReq)
	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
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

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.name, req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("%s error: %s", p.name, string(bodyBytes))
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

	usage := gw.Usage{
		PromptTokens:     openaiResp.Usage.PromptTokens,
		CompletionTokens: openaiResp.Usage.CompletionTokens,
		TotalTokens:      openaiResp.Usage.TotalTokens,
	}

	return gw.NewChatResponse(openaiResp.ID, openaiResp.Model, []gw.Choice{choice}, usage), nil
}

// openaiStreamResp is the wire type for OpenAI-compatible streaming chunks.
type openaiStreamResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// ChatStream implements streaming for OpenAI-compatible providers (OpenRouter, HuggingFace, etc.).
func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	messages := make([]openaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		var textContent string
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != nil {
				textContent = *c.Text
				break
			}
		}
		messages = append(messages, openaiMessage{
			Role:    string(m.Role),
			Content: textContent,
		})
	}

	type streamReq struct {
		openaiChatReq
		Stream bool `json:"stream"`
	}
	oReq := streamReq{
		openaiChatReq: openaiChatReq{
			Model:    req.Model,
			Messages: messages,
		},
		Stream: true,
	}
	if req.Sampling.Temperature > 0 || req.Sampling.TemperatureConfigured {
		temperature := req.Sampling.Temperature
		oReq.Temperature = &temperature
	}
	if req.Sampling.MaxTokens > 0 {
		oReq.MaxTokens = req.Sampling.MaxTokens
	}
	oReq.TopP = optionalTopP(req.Sampling)
	oReq.ReasoningEffort = req.Sampling.ReasoningEffort
	oReq.TopK = req.Sampling.TopK
	oReq.Seed = req.Sampling.Seed
	oReq.FrequencyPenalty = req.Sampling.FrequencyPenalty
	oReq.PresencePenalty = req.Sampling.PresencePenalty

	body, _ := json.Marshal(oReq)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))

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
		"stream", true,
		"correlation_id", cid,
	).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.name, req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s stream error: %s", p.name, string(bodyBytes))
	}

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
				chunk.Usage = &gw.Usage{
					PromptTokens:     partial.Usage.PromptTokens,
					CompletionTokens: partial.Usage.CompletionTokens,
					TotalTokens:      partial.Usage.TotalTokens,
				}
			}
			for _, ch := range partial.Choices {
				if ch.Delta.Content != "" {
					delta := gw.ChoiceDelta{
						Index:        ch.Index,
						Delta:        gw.NewMessage(gw.RoleAssistant, gw.Text(ch.Delta.Content)),
						FinishReason: ch.FinishReason,
					}
					chunk.Choices = append(chunk.Choices, delta)
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

	var openaiResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	if len(openaiResp.Data) > 0 {
		return gw.EmbeddingsResponse{Embedding: openaiResp.Data[0].Embedding}, nil
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
