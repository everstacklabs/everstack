package vertex_ai

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
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
)

const vertexScope = "https://www.googleapis.com/auth/cloud-platform"

type Provider struct {
	cfg         Config
	client      *http.Client
	tokenSource oauth2.TokenSource
}

type vertexPart struct {
	Text         string                  `json:"text,omitempty"`
	FunctionCall *vertexFunctionCall     `json:"functionCall,omitempty"`
	FunctionResp *vertexFunctionResponse `json:"functionResponse,omitempty"`
}

type vertexFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type vertexFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type vertexContent struct {
	Role  string       `json:"role"`
	Parts []vertexPart `json:"parts"`
}

type vertexToolDecl struct {
	FunctionDeclarations []vertexFuncDecl `json:"functionDeclarations,omitempty"`
}

type vertexFuncDecl struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type vertexRequest struct {
	Contents         []vertexContent        `json:"contents"`
	Tools            []vertexToolDecl       `json:"tools,omitempty"`
	GenerationConfig map[string]interface{} `json:"generationConfig,omitempty"`
}

type vertexResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string              `json:"text,omitempty"`
				FunctionCall *vertexFunctionCall `json:"functionCall,omitempty"`
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

func NewProvider(cfg Config) (*Provider, error) {
	cli := cfg.HTTPClient
	if cli == nil {
		cli = clientx.Default()
	}
	if cli.Transport == nil {
		cli.Transport = http.DefaultTransport
	}
	cli.Transport = attrs.NewInstrumentedTransport(cli.Transport, "vertex-ai")

	ts := cfg.TokenSource
	if ts == nil {
		var err error
		ts, err = buildTokenSource(cfg.Credentials)
		if err != nil {
			return nil, err
		}
	}

	return &Provider{cfg: cfg, client: cli, tokenSource: ts}, nil
}

func (p *Provider) Name() string { return "vertex-ai" }

func (p *Provider) SupportsModel(model string) bool {
	if len(p.cfg.SupportedModels) == 0 {
		return strings.Contains(strings.ToLower(model), "gemini") || strings.Contains(strings.ToLower(model), "claude") || strings.Contains(strings.ToLower(model), "llama") || strings.Contains(strings.ToLower(model), "mistral")
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
	body, endpoint, err := p.buildRequest(req, false)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	if err := p.setHeaders(ctx, httpReq.Header); err != nil {
		return gw.ChatCompletionResponse{}, err
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields("provider", p.Name(), "endpoint", endpoint, "model", req.Model, "stream", false, "tools", len(req.Tools), "correlation_id", cid).Info("provider request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()
	ratelimit.GlobalMonitor.Update(ratelimit.ParseHeaders(resp.Header, p.Name(), req.Model))
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return gw.ChatCompletionResponse{}, fmt.Errorf("vertex-ai error: %s", string(bodyBytes))
	}
	var vr vertexResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return gw.ChatCompletionResponse{}, err
	}
	return toGatewayResponse(vr, req.Model), nil
}

func (p *Provider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	body, endpoint, err := p.buildRequest(req, true)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if err := p.setHeaders(ctx, httpReq.Header); err != nil {
		return err
	}

	cid := correlation.GetCorrelationID(ctx)
	logger.WithFields("provider", p.Name(), "endpoint", endpoint, "model", req.Model, "stream", true, "tools", len(req.Tools), "correlation_id", cid).Info("provider stream request issued")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	ratelimit.GlobalMonitor.Update(ratelimit.ParseHeaders(resp.Header, p.Name(), req.Model))
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vertex-ai stream error: %s", string(bodyBytes))
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
			var partial vertexResponse
			if jsonErr := json.Unmarshal([]byte(data), &partial); jsonErr != nil {
				if err != nil {
					break
				}
				continue
			}
			chunk := chunkFromVertex(partial, req.Model)
			if len(chunk.Choices) == 0 && chunk.Usage == nil {
				continue
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
	return gw.EmbeddingsResponse{}, gw.ErrNotSupported{Operation: "embed", Provider: p.Name()}
}

func buildTokenSource(creds string) (oauth2.TokenSource, error) {
	creds = strings.TrimSpace(creds)
	if creds == "" {
		defaultCreds, err := googleoauth.FindDefaultCredentials(context.Background(), vertexScope)
		if err != nil {
			return nil, fmt.Errorf("load default vertex credentials: %w", err)
		}
		return defaultCreds.TokenSource, nil
	}
	if strings.HasPrefix(creds, "{") {
		googleCreds, err := googleoauth.CredentialsFromJSON(context.Background(), []byte(creds), vertexScope)
		if err != nil {
			return nil, fmt.Errorf("parse vertex credentials json: %w", err)
		}
		return googleCreds.TokenSource, nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: creds, TokenType: "Bearer"}), nil
}

func (p *Provider) setHeaders(ctx context.Context, h http.Header) error {
	tok, err := p.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("vertex token: %w", err)
	}
	h.Set("Authorization", "Bearer "+tok.AccessToken)
	h.Set("Content-Type", "application/json")
	return nil
}

func (p *Provider) buildRequest(req gw.ChatCompletionRequest, stream bool) ([]byte, string, error) {
	genConfig := make(map[string]interface{})
	if req.Sampling.Temperature > 0 {
		genConfig["temperature"] = req.Sampling.Temperature
	}
	if req.Sampling.MaxCompletionTokens > 0 {
		genConfig["maxOutputTokens"] = req.Sampling.MaxCompletionTokens
	} else if req.Sampling.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.Sampling.MaxTokens
	}
	if req.Sampling.TopP > 0 {
		genConfig["topP"] = req.Sampling.TopP
	}
	if req.Sampling.TopK != nil {
		genConfig["topK"] = *req.Sampling.TopK
	}
	vr := vertexRequest{Contents: convertMessages(req.Messages), Tools: convertTools(req.Tools), GenerationConfig: genConfig}
	body, _ := json.Marshal(vr)
	endpoint, err := buildEndpoint(p.cfg.BaseURL, req.Model, stream)
	return body, endpoint, err
}

func convertMessages(msgs []gw.Message) []vertexContent {
	out := make([]vertexContent, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == gw.RoleSystem {
			continue
		}
		role := convertRole(string(m.Role))
		if m.ToolCallID != "" {
			textContent := textFromParts(m.Content)
			var respData map[string]interface{}
			if err := json.Unmarshal([]byte(textContent), &respData); err != nil {
				respData = map[string]interface{}{"result": textContent}
			}
			out = append(out, vertexContent{Role: "function", Parts: []vertexPart{{FunctionResp: &vertexFunctionResponse{Name: m.ToolCallID, Response: respData}}}})
			continue
		}
		if len(m.ToolCalls) > 0 {
			var parts []vertexPart
			if text := textFromParts(m.Content); text != "" {
				parts = append(parts, vertexPart{Text: text})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, vertexPart{FunctionCall: &vertexFunctionCall{Name: tc.Function.Name, Args: args}})
			}
			out = append(out, vertexContent{Role: role, Parts: parts})
			continue
		}
		out = append(out, vertexContent{Role: role, Parts: []vertexPart{{Text: textFromParts(m.Content)}}})
	}
	return out
}

func convertTools(tools []gw.ToolDefinition) []vertexToolDecl {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]vertexFuncDecl, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, vertexFuncDecl{Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters})
	}
	return []vertexToolDecl{{FunctionDeclarations: decls}}
}

func buildEndpoint(baseURL string, model string, stream bool) (string, error) {
	root := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if root == "" || strings.Contains(root, "{project}") || strings.Contains(root, "{location}") {
		return "", fmt.Errorf("vertex-ai base_url must be a concrete Vertex AI project/location root, got %q", baseURL)
	}
	modelPath := resolveModelPath(model)
	if stream {
		return root + "/" + modelPath + ":streamGenerateContent?alt=sse", nil
	}
	return root + "/" + modelPath + ":generateContent", nil
}

func resolveModelPath(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, "publishers/") {
		return model
	}
	if strings.Contains(model, "/models/") {
		return model
	}
	publisher := "google"
	lower := strings.ToLower(model)
	switch {
	case strings.HasPrefix(lower, "claude"):
		publisher = "anthropic"
	case strings.Contains(lower, "llama"):
		publisher = "meta"
	case strings.Contains(lower, "mistral") || strings.Contains(lower, "codestral"):
		publisher = "mistralai"
	}
	return "publishers/" + publisher + "/models/" + model
}

func toGatewayResponse(resp vertexResponse, model string) gw.ChatCompletionResponse {
	chunk := chunkFromVertex(resp, model)
	message := gw.Message{Role: gw.RoleAssistant}
	finishReason := "stop"
	if len(chunk.Choices) > 0 {
		message = chunk.Choices[0].Delta
		if len(message.ToolCalls) > 0 {
			finishReason = "tool_calls"
		}
	}
	usage := gw.Usage{}
	if chunk.Usage != nil {
		usage = *chunk.Usage
	}
	return gw.ChatCompletionResponse{ID: fmt.Sprintf("vertex-%d", time.Now().UnixNano()), Created: time.Now().UTC(), Model: model, Choices: []gw.Choice{{Index: 0, Message: message, FinishReason: finishReason}}, Usage: usage}
}

func chunkFromVertex(resp vertexResponse, model string) gw.ChatResponseChunk {
	chunk := gw.ChatResponseChunk{ID: fmt.Sprintf("vertex-%d", time.Now().UnixNano()), Created: time.Now().UTC(), Model: model}
	if len(resp.Candidates) > 0 {
		var content string
		var toolCalls []gw.ToolCall
		for i, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				content += part.Text
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, gw.ToolCall{ID: fmt.Sprintf("call_%d", i), Type: "function", Function: gw.ToolCallFunction{Name: part.FunctionCall.Name, Arguments: string(argsJSON)}})
			}
		}
		msg := gw.Message{Role: gw.RoleAssistant, ToolCalls: toolCalls}
		if content != "" {
			msg.Content = []gw.ContentPart{gw.Text(content)}
		}
		chunk.Choices = []gw.ChoiceDelta{{Index: 0, Delta: msg}}
	}
	if resp.UsageMetadata.TotalTokenCount > 0 {
		chunk.Usage = &gw.Usage{PromptTokens: resp.UsageMetadata.PromptTokenCount, CompletionTokens: resp.UsageMetadata.CandidatesTokenCount, TotalTokens: resp.UsageMetadata.TotalTokenCount}
		if resp.UsageMetadata.CachedContentTokenCount > 0 {
			chunk.Usage.PromptDetails = &gw.TokenDetails{CachedTokens: resp.UsageMetadata.CachedContentTokenCount, TextTokens: resp.UsageMetadata.PromptTokenCount - resp.UsageMetadata.CachedContentTokenCount}
		}
		if resp.UsageMetadata.ThoughtTokenCount > 0 {
			chunk.Usage.CompletionDetails = &gw.TokenDetails{ReasoningTokens: resp.UsageMetadata.ThoughtTokenCount, TextTokens: resp.UsageMetadata.CandidatesTokenCount - resp.UsageMetadata.ThoughtTokenCount}
		}
	}
	return chunk
}

func convertRole(role string) string {
	if role == string(gw.RoleAssistant) {
		return "model"
	}
	return "user"
}

func textFromParts(parts []gw.ContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if part.Text != nil {
			b.WriteString(*part.Text)
		}
	}
	return b.String()
}
