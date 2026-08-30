package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/metrics"
)

// MessagesHandler mirrors the TS messagesHandler: parses request JSON and headers,
// constructs per-request overrides, routes via router, and supports SSE when stream=true.
// It now includes fast-path optimizations for reduced latency.
func MessagesHandler(router *Router, defaultModel string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Start latency tracking
		tracker := metrics.StartLatencyTracker()
		defer tracker.RecordTotal()

		// Read body with pooled buffer for fast-path
		bodyBytes, err := readBodyFastPath(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		// Parse JSON body using fast-path JSON parser
		var body map[string]interface{}
		if err := fastpath.Unmarshal(bodyBytes, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		// Build normalized request
		req, err := buildChatRequestFromBody(ctx, body, r.Header, defaultModel)
		if err != nil {
			// RouterError -> 400, else 500
			if _, ok := err.(RouterError); ok {
				writeJSONError(w, http.StatusBadRequest, err.Error())
			} else {
				writeJSONError(w, http.StatusInternalServerError, "Something went wrong")
			}
			return
		}

		// Fast-path: Check caches for non-streaming requests
		if !req.Stream {
			if engine := fastpath.EngineFromContext(ctx); engine != nil && engine.IsEnabled() {
				// 1. Check exact cache first (fastest)
				tracker.StartStage(metrics.StageCacheLookup)
				if cached, found := engine.GetCachedResponse(&chatRequestAdapter{req}); found {
					tracker.EndStage(metrics.StageCacheLookup)
					engine.RecordFastPathRequest()
					w.Header().Set("X-Cache", "HIT")
					w.Header().Set("X-Cache-Type", "exact")
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write(cached.Response)
					return
				}
				tracker.EndStage(metrics.StageCacheLookup)

				// 2. Check semantic cache (similarity-based)
				if engine.IsSemanticCacheEnabled() {
					queryText := extractQueryText(req)
					if queryText != "" {
						tracker.StartStage(metrics.StageSemanticLookup)
						if cached, found := engine.GetSemanticCachedResponse(queryText); found {
							tracker.EndStage(metrics.StageSemanticLookup)
							engine.RecordFastPathRequest()
							w.Header().Set("X-Cache", "HIT")
							w.Header().Set("X-Cache-Type", "semantic")
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusOK)
							w.Write(cached.Response)
							return
						}
						tracker.EndStage(metrics.StageSemanticLookup)
					}
				}
			}
		}

		// Streaming path via SSE if requested
		if req.Stream {
			// Use pooled SSE sender for fast-path
			sender := NewPooledSSEStreamSender(w)
			defer sender.Close()

			tracker.StartStage(metrics.StageStreaming)
			if err := HandleChatStream(ctx, router, req, func(c ChatResponseChunk) error { return sender.Send(&c) }); err != nil {
				tracker.EndStage(metrics.StageStreaming)
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
			tracker.EndStage(metrics.StageStreaming)
			return
		}

		// Unary path
		resp, err := HandleChat(ctx, router, req)
		if err != nil {
			if _, ok := err.(RouterError); ok {
				writeJSONError(w, http.StatusBadRequest, err.Error())
			} else {
				writeJSONError(w, http.StatusInternalServerError, "Something went wrong")
			}
			return
		}

		// Fast-path: Cache the response for future requests
		if engine := fastpath.EngineFromContext(ctx); engine != nil && engine.IsEnabled() {
			go func() {
				respBytes, err := fastpath.Marshal(resp)
				if err == nil {
					cachedResp := &cache.CachedResponse{
						Response:     respBytes,
						Model:        resp.Model,
						OutputTokens: resp.Usage.CompletionTokens,
					}
					// Store in exact cache
					engine.CacheResponse(&chatRequestAdapter{req}, cachedResp)

					// Also store in semantic cache for similarity matching
					if engine.IsSemanticCacheEnabled() {
						queryText := extractQueryText(req)
						if queryText != "" {
							engine.CacheSemanticResponse(queryText, cachedResp)
						}
					}
				}
			}()
		}

		w.Header().Set("X-Cache", "MISS")
		writeJSON(w, http.StatusOK, resp)
	})
}

// readBodyFastPath reads the request body efficiently.
// For small bodies, it avoids allocations by using a pooled buffer.
func readBodyFastPath(r *http.Request) ([]byte, error) {
	// For now, use standard io.ReadAll
	// In Phase 2, we can add buffer pooling for large bodies
	return io.ReadAll(r.Body)
}

// chatRequestAdapter adapts ChatCompletionRequest to the cache.ChatRequest interface.
type chatRequestAdapter struct {
	req ChatCompletionRequest
}

func (a *chatRequestAdapter) GetModel() string        { return a.req.Model }
func (a *chatRequestAdapter) GetTemperature() float64 { return a.req.Sampling.Temperature }
func (a *chatRequestAdapter) GetMaxTokens() int       { return a.req.Sampling.MaxTokens }
func (a *chatRequestAdapter) GetTopP() float64        { return a.req.Sampling.TopP }
func (a *chatRequestAdapter) GetStream() bool         { return a.req.Stream }
func (a *chatRequestAdapter) GetMessages() []cache.Message {
	msgs := make([]cache.Message, len(a.req.Messages))
	for i := range a.req.Messages {
		msgs[i] = &messageAdapter{a.req.Messages[i]}
	}
	return msgs
}

// messageAdapter adapts Message to the cache.Message interface.
type messageAdapter struct {
	msg Message
}

func (m *messageAdapter) GetRole() string { return string(m.msg.Role) }
func (m *messageAdapter) GetContent() string {
	// Concatenate all text content parts
	var content string
	for _, part := range m.msg.Content {
		if part.Type == "text" && part.Text != nil {
			content += *part.Text
		}
	}
	return content
}

// extractQueryText extracts the text content from the last user message.
// This is used as the key for semantic cache lookups.
func extractQueryText(req ChatCompletionRequest) string {
	// Find the last user message
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleUser {
			// Concatenate all text content parts
			var content string
			for _, part := range msg.Content {
				if part.Type == "text" && part.Text != nil {
					content += *part.Text
				}
			}
			return content
		}
	}
	return ""
}

func buildChatRequestFromBody(ctx context.Context, body map[string]interface{}, hdr http.Header, defaultModel string) (ChatCompletionRequest, error) {
	// Extract model if present; else from header override; else default
	headers := make(map[string]string)
	for k, vv := range hdr {
		if len(vv) > 0 {
			headers[k] = vv[0]
		}
	}
	overrides := ConstructConfigFromHeaders(headers)

	// Build messages array
	var messages []Message
	if rawMsgs, ok := body["messages"].([]interface{}); ok {
		for _, rm := range rawMsgs {
			if m, ok := rm.(map[string]interface{}); ok {
				role := RoleUser
				if rv, ok := m["role"].(string); ok && rv != "" {
					role = MessageRole(rv)
				}
				var parts []ContentPart
				if rawParts, ok := m["content"].([]interface{}); ok {
					for _, rp := range rawParts {
						if p, ok := rp.(map[string]interface{}); ok {
							if t, ok := p["type"].(string); ok {
								switch t {
								case "text":
									if txt, ok := p["text"].(string); ok {
										parts = append(parts, Text(txt))
									}
								case "image_url":
									if u, ok := p["image_url"].(string); ok {
										parts = append(parts, ImageURL(u))
									}
								}
							}
						}
					}
				}
				messages = append(messages, NewMessage(role, parts...))
			}
		}
	}

	model := defaultModel
	if mv, ok := body["model"].(string); ok && mv != "" {
		model = mv
	}
	if ov, ok := overrides["model"]; ok && ov != "" {
		model = ov
	}
	if model == "" {
		return ChatCompletionRequest{}, RouterError{Message: "no model specified"}
	}

	stream := false
	if sv, ok := body["stream"].(bool); ok {
		stream = sv
	} else {
		if feat, _ := ctx.Value(contextkeys.FeaturesConfig).(*validator.FeaturesConfig); feat != nil && feat.Gateway.EnableStreaming {
			stream = true
		}
	}

	// Sampling overrides from body
	sampling := SamplingParams{}
	if sam, ok := body["sampling"].(map[string]interface{}); ok {
		if v, ok := sam["temperature"].(float64); ok {
			sampling.Temperature = v
		}
		if v, ok := sam["top_p"].(float64); ok {
			sampling.TopP = v
		}
		if v, ok := sam["max_tokens"].(float64); ok {
			sampling.MaxTokens = int(v)
		}
	}
	if tv, ok := overrides["temperature"]; ok {
		if f, err := parseFloat(tv); err == nil {
			sampling.Temperature = f
		}
	}

	return ChatCompletionRequest{Model: model, Messages: messages, Sampling: sampling, Stream: stream}, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"status": "failure", "message": msg})
}

func parseFloat(s string) (float64, error) {
	var f float64
	err := json.Unmarshal([]byte(s), &f)
	if err == nil {
		return f, nil
	}
	return 0, err
}
