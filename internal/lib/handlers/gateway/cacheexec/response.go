package cacheexec

import (
	"context"
	"fmt"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

// ExecuteResponseWithCache executes a unary Responses API request with cache lookup/store.
func ExecuteResponseWithCache(
	ctx context.Context,
	req gw.CreateResponseRequest,
	execute func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error),
) (gw.CreateResponseResponse, bool, error) {
	resp, outcome, err := ExecuteResponseWithCacheOutcome(ctx, req, execute)
	return resp, outcome.Hit, err
}

// ExecuteResponseWithCacheOutcome executes a unary Responses API request with cache metadata.
func ExecuteResponseWithCacheOutcome(
	ctx context.Context,
	req gw.CreateResponseRequest,
	execute func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error),
) (gw.CreateResponseResponse, CacheOutcome, error) {
	outcome := CacheOutcome{}

	// Streaming responses are not cached.
	if req.Stream {
		resp, err := execute(ctx, req)
		return resp, outcome, err
	}

	engine := fastpath.EngineFromContext(ctx)
	if engine != nil && engine.IsEnabled() {
		adapter := &responseRequestAdapter{req: req}

		// 1) Exact cache lookup.
		if cached, found := engine.GetCachedResponseWithContext(ctx, adapter); found {
			var cachedResp gw.CreateResponseResponse
			if err := fastpath.Unmarshal(cached.Response, &cachedResp); err == nil {
				engine.RecordFastPathRequest()
				outcome.Hit = true
				outcome.HitType = "exact"
				return cachedResp, outcome, nil
			}
		}

		// 2) Semantic cache lookup.
		if engine.IsSemanticCacheEnabled() {
			queryText := extractResponseQueryText(req)
			if queryText != "" {
				if cached, found := engine.GetSemanticCachedResponseWithContext(ctx, queryText); found {
					var cachedResp gw.CreateResponseResponse
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
		return gw.CreateResponseResponse{}, outcome, err
	}

	// 4) Best-effort cache store for terminal unary responses.
	if engine != nil && engine.IsEnabled() && (resp.Status == "" || resp.Status == "completed") {
		if respBytes, marshalErr := fastpath.Marshal(resp); marshalErr == nil {
			cachedResp := &cache.CachedResponse{
				Response:     respBytes,
				Model:        resp.Model,
				OutputTokens: responseOutputTokens(resp.Usage),
			}
			adapter := &responseRequestAdapter{req: req}
			engine.CacheResponseWithContext(ctx, adapter, cachedResp)
			outcome.Stored = true

			if engine.IsSemanticCacheEnabled() {
				queryText := extractResponseQueryText(req)
				if queryText != "" {
					go engine.CacheSemanticResponseWithContext(context.Background(), queryText, cachedResp)
					outcome.SemanticStored = true
				}
			}
		}
	}

	return resp, outcome, nil
}

type responseRequestAdapter struct {
	req gw.CreateResponseRequest
}

func (a *responseRequestAdapter) GetModel() string        { return a.req.Model }
func (a *responseRequestAdapter) GetTemperature() float64 { return a.req.Temperature }
func (a *responseRequestAdapter) GetMaxTokens() int       { return a.req.MaxOutputTokens }
func (a *responseRequestAdapter) GetTopP() float64        { return a.req.TopP }
func (a *responseRequestAdapter) GetStream() bool         { return a.req.Stream }

func (a *responseRequestAdapter) GetMessages() []cache.Message {
	msgs := make([]cache.Message, 0, len(a.req.Input)+4)

	if a.req.Instructions != "" {
		msgs = append(msgs, &cacheMessage{role: string(gw.RoleSystem), content: a.req.Instructions})
	}
	if a.req.PreviousResponseID != "" {
		msgs = append(msgs, &cacheMessage{role: "previous_response_id", content: a.req.PreviousResponseID})
	}
	for _, input := range a.req.Input {
		content := responseInputText(input)
		if content == "" {
			continue
		}
		role := string(input.Role)
		if role == "" {
			role = "input"
		}
		msgs = append(msgs, &cacheMessage{role: role, content: content})
	}
	for _, t := range a.req.Tools {
		msgs = append(msgs, &cacheMessage{
			role:    "tool",
			content: fmt.Sprintf("%s:%s", t.Type, t.Function.Name),
		})
	}
	for _, t := range a.req.BuiltinTools {
		msgs = append(msgs, &cacheMessage{
			role:    "builtin_tool",
			content: t.Type,
		})
	}
	if tc := toolChoiceKey(a.req.ToolChoice); tc != "" {
		msgs = append(msgs, &cacheMessage{role: "tool_choice", content: tc})
	}
	return msgs
}

type cacheMessage struct {
	role    string
	content string
}

func (m *cacheMessage) GetRole() string    { return m.role }
func (m *cacheMessage) GetContent() string { return m.content }

func responseInputText(input gw.ResponseInput) string {
	if input.Type == "item_reference" {
		return input.ItemID
	}
	var b strings.Builder
	for _, part := range input.Content {
		if part.Type == "text" && part.Text != nil {
			b.WriteString(*part.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func extractResponseQueryText(req gw.CreateResponseRequest) string {
	for i := len(req.Input) - 1; i >= 0; i-- {
		input := req.Input[i]
		if input.Type != "message" || input.Role != gw.RoleUser {
			continue
		}
		if text := responseInputText(input); text != "" {
			return text
		}
	}
	return ""
}

func responseOutputTokens(usage *gw.ResponseUsage) int {
	if usage == nil {
		return 0
	}
	return usage.OutputTokens
}

func toolChoiceKey(toolChoice interface{}) string {
	if toolChoice == nil {
		return ""
	}
	return fmt.Sprintf("%v", toolChoice)
}
