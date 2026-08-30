package azure_openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	cfg    Config
	client *http.Client
}

type azureChatMessage struct {
	Role       string             `json:"role"`
	Content    []azureContentPart `json:"content,omitempty"`
	ToolCalls  []azureToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

type azureContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL any    `json:"image_url,omitempty"`
}

type azureToolCall struct {
	ID       string                `json:"id"`
	Type     string                `json:"type"`
	Function azureToolCallFunction `json:"function"`
}

type azureToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type azureTool struct {
	Type     string           `json:"type"`
	Function azureToolFuncDef `json:"function"`
}

type azureToolFuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type azureChatRequest struct {
	Messages            []azureChatMessage     `json:"messages"`
	Tools               []azureTool            `json:"tools,omitempty"`
	ToolChoice          interface{}            `json:"tool_choice,omitempty"`
	Temperature         *float64               `json:"temperature,omitempty"`
	TopP                *float64               `json:"top_p,omitempty"`
	MaxTokens           int                    `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
	Stop                []string               `json:"stop,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	StreamOptions       *azureStreamOptions    `json:"stream_options,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	ReasoningEffort     string                 `json:"reasoning_effort,omitempty"`
	FrequencyPenalty    float64                `json:"frequency_penalty,omitempty"`
	PresencePenalty     float64                `json:"presence_penalty,omitempty"`
	Seed                *int64                 `json:"seed,omitempty"`
	Verbosity           string                 `json:"verbosity,omitempty"`
}

type azureStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type azureStreamToolCall struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function azureToolCallFunction `json:"function"`
}

type azureTokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`
	TextTokens      int `json:"text_tokens,omitempty"`
}

type azureChatChoice struct {
	Index   int `json:"index"`
	Message struct {
		Role      string          `json:"role"`
		Content   interface{}     `json:"content"`
		ToolCalls []azureToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type azureChatResponse struct {
	ID      string            `json:"id"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []azureChatChoice `json:"choices"`
	Usage   struct {
		PromptTokens            int                `json:"prompt_tokens"`
		CompletionTokens        int                `json:"completion_tokens"`
		TotalTokens             int                `json:"total_tokens"`
		PromptTokensDetails     *azureTokenDetails `json:"prompt_tokens_details,omitempty"`
		CompletionTokensDetails *azureTokenDetails `json:"completion_tokens_details,omitempty"`
	} `json:"usage"`
}

func NewProvider(cfg Config) *Provider {
	cli := cfg.HTTPClient
	if cli == nil {
		cli = clientx.Default()
	}
	if cli.Transport == nil {
		cli.Transport = http.DefaultTransport
	}
	cli.Transport = attrs.NewInstrumentedTransport(cli.Transport, "azure-openai")
	return &Provider{cfg: cfg, client: cli}
}

func (p *Provider) Name() string { return "azure-openai" }

func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "text-embedding-") || strings.HasPrefix(model, "o")
	}
	model = strings.ToLower(model)
	for _, m := range p.cfg.SupportedModels {
		if strings.ToLower(m) == model {
			return true
		}
	}
	return false
}

func (p *Provider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return gw.ChatCompletionResponse{}, fmt.Errorf("azure-openai api key not provided")
	}
	if req.Stream {
		return gw.ChatCompletionResponse{}, gw.ErrNotImplemented("chat_stream_unary")
	}

	payload := toAzureChatRequest(req)
	buf, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(ctx, providerutil.ChatRequestTimeout(req.Sampling, 12*time.Second))
	defer cancel()

	endpoint, err := p.chatEndpoint(req.Model)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	p.setHeaders(httpReq.Header)

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"provider", p.Name(),
		"endpoint", endpoint,
		"model", req.Model,
		"stream", false,
		"correlation_id", cid,
	).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.Name(), req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, errors.New("azure-openai chat error: " + string(b))
	}

	var azureResp azureChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&azureResp); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	return toGatewayChatResponse(azureResp), nil
}

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return fmt.Errorf("azure-openai api key not provided")
	}

	payload := toAzureChatRequest(req)
	payload.Stream = true
	payload.StreamOptions = &azureStreamOptions{IncludeUsage: true}
	buf, _ := json.Marshal(payload)

	endpoint, err := p.chatEndpoint(req.Model)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	p.setHeaders(httpReq.Header)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rateLimitInfo := ratelimit.ParseHeaders(resp.Header, p.Name(), req.Model)
	ratelimit.GlobalMonitor.Update(rateLimitInfo)

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return errors.New("azure-openai chat stream error: " + string(b))
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "event:") {
				if err != nil {
					break
				}
				continue
			}
			if !strings.HasPrefix(s, "data:") {
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
				Created int64  `json:"created"`
				Model   string `json:"model"`
				Choices []struct {
					Index int `json:"index"`
					Delta struct {
						Content   interface{}           `json:"content"`
						ToolCalls []azureStreamToolCall `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens            int                `json:"prompt_tokens"`
					CompletionTokens        int                `json:"completion_tokens"`
					TotalTokens             int                `json:"total_tokens"`
					PromptTokensDetails     *azureTokenDetails `json:"prompt_tokens_details,omitempty"`
					CompletionTokensDetails *azureTokenDetails `json:"completion_tokens_details,omitempty"`
				} `json:"usage,omitempty"`
			}
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}

			chunk := gw.ChatResponseChunk{ID: partial.ID, Created: time.Unix(partial.Created, 0).UTC(), Model: partial.Model}
			if partial.Usage != nil {
				chunk.Usage = &gw.Usage{
					PromptTokens:     partial.Usage.PromptTokens,
					CompletionTokens: partial.Usage.CompletionTokens,
					TotalTokens:      partial.Usage.TotalTokens,
				}
				if partial.Usage.PromptTokensDetails != nil {
					chunk.Usage.PromptDetails = &gw.TokenDetails{
						CachedTokens:    partial.Usage.PromptTokensDetails.CachedTokens,
						ReasoningTokens: partial.Usage.PromptTokensDetails.ReasoningTokens,
						AudioTokens:     partial.Usage.PromptTokensDetails.AudioTokens,
						TextTokens:      partial.Usage.PromptTokensDetails.TextTokens,
					}
				}
				if partial.Usage.CompletionTokensDetails != nil {
					chunk.Usage.CompletionDetails = &gw.TokenDetails{
						CachedTokens:    partial.Usage.CompletionTokensDetails.CachedTokens,
						ReasoningTokens: partial.Usage.CompletionTokensDetails.ReasoningTokens,
						AudioTokens:     partial.Usage.CompletionTokensDetails.AudioTokens,
						TextTokens:      partial.Usage.CompletionTokensDetails.TextTokens,
					}
				}
			}

			for _, ch := range partial.Choices {
				var parts []gw.ContentPart
				switch v := ch.Delta.Content.(type) {
				case string:
					if v != "" {
						parts = append(parts, gw.Text(v))
					}
				case []interface{}:
					for _, pv := range v {
						if pm, ok := pv.(map[string]interface{}); ok {
							if t, ok := pm["type"].(string); ok && t == "text" {
								if txt, ok := pm["text"].(string); ok {
									parts = append(parts, gw.Text(txt))
								}
							}
						}
					}
				}

				var toolCalls []gw.ToolCall
				for _, tc := range ch.Delta.ToolCalls {
					toolCalls = append(toolCalls, gw.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: gw.ToolCallFunction{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				}

				chunk.Choices = append(chunk.Choices, gw.ChoiceDelta{
					Index: ch.Index,
					Delta: gw.Message{
						Role:      gw.RoleAssistant,
						Content:   parts,
						ToolCalls: toolCalls,
					},
					FinishReason: ch.FinishReason,
				})
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
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return gw.EmbeddingsResponse{}, fmt.Errorf("azure-openai api key not provided")
	}
	payload := struct {
		Input string `json:"input"`
	}{Input: req.Input}
	buf, _ := json.Marshal(payload)

	endpoint, err := p.embeddingsEndpoint(req.Model)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	p.setHeaders(hreq.Header)

	resp, err := p.client.Do(hreq)
	if err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return gw.EmbeddingsResponse{}, errors.New("azure-openai embeddings error: " + string(b))
	}

	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage *struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return gw.EmbeddingsResponse{}, err
	}
	if len(parsed.Data) == 0 {
		return gw.EmbeddingsResponse{}, errors.New("no embedding returned")
	}

	result := gw.EmbeddingsResponse{Embedding: parsed.Data[0].Embedding, Model: req.Model}
	if parsed.Usage != nil {
		result.Usage = &gw.Usage{
			PromptTokens: parsed.Usage.PromptTokens,
			TotalTokens:  parsed.Usage.TotalTokens,
		}
	}
	return result, nil
}

func toAzureChatRequest(req gw.ChatCompletionRequest) azureChatRequest {
	msgs := make([]azureChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := azureChatMessage{Role: string(m.Role)}
		if len(m.Content) > 0 {
			parts := make([]azureContentPart, 0, len(m.Content))
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					if c.Text != nil {
						parts = append(parts, azureContentPart{Type: "text", Text: *c.Text})
					}
				case "image_url":
					if c.ImageURL != nil {
						parts = append(parts, azureContentPart{Type: "image_url", ImageURL: map[string]string{"url": *c.ImageURL}})
					}
				}
			}
			msg.Content = parts
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, azureToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: azureToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		msgs = append(msgs, msg)
	}

	maxTokens := req.Sampling.MaxTokens
	maxCompletionTokens := req.Sampling.MaxCompletionTokens
	requiresMaxCompletionTokens := strings.HasPrefix(req.Model, "o1") || strings.HasPrefix(req.Model, "o3-mini") || strings.HasPrefix(req.Model, "gpt-5")
	if requiresMaxCompletionTokens {
		if maxCompletionTokens == 0 && maxTokens > 0 {
			maxCompletionTokens = maxTokens
		}
		maxTokens = 0
	}

	azureReq := azureChatRequest{
		Messages:            msgs,
		Temperature:         optionalTemperature(req.Sampling),
		TopP:                optionalTopP(req.Sampling),
		MaxTokens:           maxTokens,
		MaxCompletionTokens: maxCompletionTokens,
		Stop:                req.Sampling.Stop,
		Metadata:            req.Metadata,
		ReasoningEffort:     req.Sampling.ReasoningEffort,
		FrequencyPenalty:    req.Sampling.FrequencyPenalty,
		PresencePenalty:     req.Sampling.PresencePenalty,
		Seed:                req.Sampling.Seed,
		Verbosity:           req.Sampling.Verbosity,
	}
	for _, t := range req.Tools {
		azureReq.Tools = append(azureReq.Tools, azureTool{
			Type: t.Type,
			Function: azureToolFuncDef{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	if req.ToolChoice != nil {
		azureReq.ToolChoice = req.ToolChoice
	}
	return azureReq
}

func optionalTemperature(sampling gw.SamplingParams) *float64 {
	if !sampling.TemperatureConfigured && sampling.Temperature == 0 {
		return nil
	}
	value := sampling.Temperature
	return &value
}

func optionalTopP(sampling gw.SamplingParams) *float64 {
	if !sampling.TopPConfigured && sampling.TopP == 0 {
		return nil
	}
	value := sampling.TopP
	return &value
}

func (p *Provider) chatEndpoint(model string) (string, error) {
	return p.deploymentEndpoint(model, "chat/completions")
}

func (p *Provider) embeddingsEndpoint(model string) (string, error) {
	return p.deploymentEndpoint(model, "embeddings")
}

func (p *Provider) deploymentEndpoint(model string, suffix string) (string, error) {
	baseURL := strings.TrimSpace(strings.TrimRight(p.cfg.BaseURL, "/"))
	if baseURL == "" || strings.Contains(baseURL, "{resource}") {
		return "", fmt.Errorf("azure-openai base_url must point to an Azure OpenAI resource, got %q", p.cfg.BaseURL)
	}
	deployment := strings.TrimSpace(model)
	if deployment == "" {
		return "", fmt.Errorf("azure-openai model/deployment is required")
	}
	version := strings.TrimSpace(p.cfg.APIVersion)
	if version == "" {
		version = DefaultSpec().APIVersion
	}
	return fmt.Sprintf("%s/openai/deployments/%s/%s?api-version=%s", baseURL, url.PathEscape(deployment), suffix, url.QueryEscape(version)), nil
}

func (p *Provider) setHeaders(h http.Header) {
	h.Set("api-key", p.cfg.APIKey)
	h.Set("Content-Type", "application/json")
}

func toGatewayChatResponse(azureResp azureChatResponse) gw.ChatCompletionResponse {
	choices := make([]gw.Choice, 0, len(azureResp.Choices))
	for _, c := range azureResp.Choices {
		var parts []gw.ContentPart
		switch v := c.Message.Content.(type) {
		case string:
			if v != "" {
				parts = append(parts, gw.Text(v))
			}
		case []interface{}:
			for _, pv := range v {
				if pm, ok := pv.(map[string]interface{}); ok && pm["type"] == "text" {
					if txt, ok := pm["text"].(string); ok {
						parts = append(parts, gw.Text(txt))
					}
				}
			}
		}

		toolCalls := make([]gw.ToolCall, 0, len(c.Message.ToolCalls))
		for _, tc := range c.Message.ToolCalls {
			toolCalls = append(toolCalls, gw.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: gw.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		choices = append(choices, gw.Choice{
			Index: c.Index,
			Message: gw.Message{
				Role:      gw.RoleAssistant,
				Content:   parts,
				ToolCalls: toolCalls,
			},
			FinishReason: c.FinishReason,
		})
	}

	usage := gw.Usage{
		PromptTokens:     azureResp.Usage.PromptTokens,
		CompletionTokens: azureResp.Usage.CompletionTokens,
		TotalTokens:      azureResp.Usage.TotalTokens,
	}
	if azureResp.Usage.PromptTokensDetails != nil {
		usage.PromptDetails = &gw.TokenDetails{
			CachedTokens:    azureResp.Usage.PromptTokensDetails.CachedTokens,
			ReasoningTokens: azureResp.Usage.PromptTokensDetails.ReasoningTokens,
			AudioTokens:     azureResp.Usage.PromptTokensDetails.AudioTokens,
			TextTokens:      azureResp.Usage.PromptTokensDetails.TextTokens,
		}
	}
	if azureResp.Usage.CompletionTokensDetails != nil {
		usage.CompletionDetails = &gw.TokenDetails{
			CachedTokens:    azureResp.Usage.CompletionTokensDetails.CachedTokens,
			ReasoningTokens: azureResp.Usage.CompletionTokensDetails.ReasoningTokens,
			AudioTokens:     azureResp.Usage.CompletionTokensDetails.AudioTokens,
			TextTokens:      azureResp.Usage.CompletionTokensDetails.TextTokens,
		}
	}

	return gw.ChatCompletionResponse{
		ID:      azureResp.ID,
		Created: time.Unix(azureResp.Created, 0).UTC(),
		Model:   azureResp.Model,
		Choices: choices,
		Usage:   usage,
	}
}
