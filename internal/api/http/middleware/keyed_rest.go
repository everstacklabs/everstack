package middleware

import (
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/gorilla/mux"
)

// WithKeyedRESTValidation covers raw REST, WebSocket and SSE routes registered
// before the grpc-gateway catch-all. Their session/SSH paths stay in their own
// handlers; a presented gateway key must pass the same surface checks as /v1.
func (i *APIKeyInterceptor) WithKeyedRESTValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") && !strings.HasPrefix(r.URL.Path, "/openai/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		// The grpc-gateway catch-alls have their own compatibility/auth chain.
		// Leave their existing Bearer transport rules to that chain.
		if route := mux.CurrentRoute(r); route != nil {
			template, _ := route.GetPathTemplate()
			if template == "/v1" || template == "/openai/v1" {
				next.ServeHTTP(w, r)
				return
			}
			// The Anthropic Messages translator hands the request to that same
			// authenticated /v1 chain, which checks the key and its surfaces
			// and whose refusals the translator turns into Anthropic's error
			// shape. Answering here would return an OpenAI-shaped error.
			if template == "/v1/messages" || template == "/v1/messages/count_tokens" {
				next.ServeHTTP(w, r)
				return
			}
		}
		original := r
		if extractAPIKeyFromHeaders(r.Header) == "" {
			bearer := strings.TrimSpace(strings.TrimPrefix(r.Header.Get(common.Authorization), "Bearer "))
			if !strings.HasPrefix(r.Header.Get(common.Authorization), "Bearer ") || !apikey.HasIssuedKeyPrefix(bearer) {
				next.ServeHTTP(w, r)
				return
			}
			r = r.Clone(r.Context())
			r.Header.Set(common.EverstackApiKey, bearer)
		} else {
			// Explicit API-key headers win over an SDK's Bearer placeholder.
			r = r.Clone(r.Context())
		}
		r.Header.Del(common.Authorization)
		// Preserve the original credential transport for OpenAI response shaping
		// and raw protocol handlers. Only the verified context crosses this guard.
		validated := i.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, verified *http.Request) {
			next.ServeHTTP(w, original.WithContext(verified.Context()))
		}))
		validated.ServeHTTP(w, r)
	})
}
