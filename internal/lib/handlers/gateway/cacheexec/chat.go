package cacheexec

import (
	"context"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

// CacheOutcome describes cache behavior for a single execution attempt.
type CacheOutcome struct {
	Hit            bool
	HitType        string // "exact" | "semantic"
	Stored         bool
	SemanticStored bool
}

// ExecuteChatWithCache executes a unary chat request with FastPath cache lookup/store.
//
// Returns:
//   - response: chat completion response
//   - cacheHit: true if served from exact/semantic cache
//   - err: execution error (provider/cache decode errors fall back to provider call)
func ExecuteChatWithCache(
	ctx context.Context,
	req gw.ChatCompletionRequest,
	execute func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error),
) (gw.ChatCompletionResponse, bool, error) {
	resp, outcome, err := ExecuteChatWithCacheOutcome(ctx, req, execute)
	return resp, outcome.Hit, err
}

// ExecuteChatWithCacheOutcome executes a unary chat request with cache metadata.
func ExecuteChatWithCacheOutcome(
	ctx context.Context,
	req gw.ChatCompletionRequest,
	execute func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error),
) (gw.ChatCompletionResponse, CacheOutcome, error) {
	outcome := CacheOutcome{}

	// Do not cache streaming requests.
	if req.Stream {
		resp, err := execute(ctx, req)
		return resp, outcome, err
	}

	engine := fastpath.EngineFromContext(ctx)
	if engine != nil && engine.IsEnabled() {
		adapter := &chatRequestAdapter{req: req}

		// 1) Exact cache lookup.
		if cached, found := engine.GetCachedResponseWithContext(ctx, adapter); found {
			var cachedResp gw.ChatCompletionResponse
			if err := fastpath.Unmarshal(cached.Response, &cachedResp); err == nil {
				engine.RecordFastPathRequest()
				outcome.Hit = true
				outcome.HitType = "exact"
				return cachedResp, outcome, nil
			}
		}

		// 2) Semantic cache lookup.
		if engine.IsSemanticCacheEnabled() {
			queryText := extractQueryText(req)
			if queryText != "" {
				if cached, found := engine.GetSemanticCachedResponseWithContext(ctx, queryText); found {
					var cachedResp gw.ChatCompletionResponse
					if err := fastpath.Unmarshal(cached.Response, &cachedResp); err == nil {
						engine.RecordFastPathRequest()
						outcome.Hit = true
						outcome.HitType = "semantic"
						return cachedResp, outcome, nil
					}
				}
			}
		}
	}

	// 3) Cache miss -> provider execution.
	resp, err := execute(ctx, req)
	if err != nil {
		return gw.ChatCompletionResponse{}, outcome, err
	}

	// 4) Best-effort cache store.
	if engine != nil && engine.IsEnabled() {
		if respBytes, marshalErr := fastpath.Marshal(resp); marshalErr == nil {
			cachedResp := &cache.CachedResponse{
				Response:     respBytes,
				Model:        resp.Model,
				OutputTokens: resp.Usage.CompletionTokens,
			}
			adapter := &chatRequestAdapter{req: req}
			engine.CacheResponseWithContext(ctx, adapter, cachedResp)
			outcome.Stored = true

			// Semantic cache can involve heavier work; store asynchronously.
			if engine.IsSemanticCacheEnabled() {
				queryText := extractQueryText(req)
				if queryText != "" {
					go engine.CacheSemanticResponseWithContext(context.Background(), queryText, cachedResp)
					outcome.SemanticStored = true
				}
			}
		}
	}

	return resp, outcome, nil
}

// chatRequestAdapter adapts gw.ChatCompletionRequest to cache.ChatRequest.
type chatRequestAdapter struct {
	req gw.ChatCompletionRequest
}

func (a *chatRequestAdapter) GetModel() string        { return a.req.Model }
func (a *chatRequestAdapter) GetTemperature() float64 { return a.req.Sampling.Temperature }
func (a *chatRequestAdapter) GetMaxTokens() int       { return a.req.Sampling.MaxTokens }
func (a *chatRequestAdapter) GetTopP() float64        { return a.req.Sampling.TopP }
func (a *chatRequestAdapter) GetStream() bool         { return a.req.Stream }
func (a *chatRequestAdapter) GetMessages() []cache.Message {
	msgs := make([]cache.Message, len(a.req.Messages))
	for i := range a.req.Messages {
		msgs[i] = &chatMessageAdapter{msg: a.req.Messages[i]}
	}
	return msgs
}

type chatMessageAdapter struct {
	msg gw.Message
}

func (m *chatMessageAdapter) GetRole() string {
	return string(m.msg.Role)
}

func (m *chatMessageAdapter) GetContent() string {
	// Concatenate text content parts.
	content := ""
	for _, part := range m.msg.Content {
		if part.Type == "text" && part.Text != nil {
			content += *part.Text
		}
	}
	return content
}

// extractQueryText gets the last user text message for semantic cache lookup.
func extractQueryText(req gw.ChatCompletionRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != gw.RoleUser {
			continue
		}
		content := ""
		for _, part := range msg.Content {
			if part.Type == "text" && part.Text != nil {
				content += *part.Text
			}
		}
		return content
	}
	return ""
}
