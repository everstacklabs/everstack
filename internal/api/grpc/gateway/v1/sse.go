package v1

import (
	"net/http"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Optional: expose an SSE streaming handler that uses the same router.
// Mount this under an HTTP mux when you want SSE streaming.
func (s *Server) SSEChatHandler() (string, http.Handler) {
	if s.providersFor(s.ctx) == nil {
		s.bootstrapFromConfig()
	}
	path := "/v1/chat/stream"
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server-side gate for streaming
		if s.feat != nil && !s.feat.Gateway.EnableStreaming {
			http.Error(w, "streaming is disabled by server policy", http.StatusPreconditionFailed)
			return
		}
		ctx := r.Context()
		if err := s.EnsureProvidersForRequest(ctx); err != nil {
			http.Error(w, "failed to load providers for tenant", http.StatusInternalServerError)
			return
		}
		bundle := s.providersFor(ctx)
		if bundle == nil || bundle.router == nil {
			http.Error(w, "no providers configured for this tenant", http.StatusPreconditionFailed)
			return
		}
		msg := strings.TrimSpace(r.URL.Query().Get("message"))
		if msg == "" {
			msg = "Hello"
		}
		model := s.defaultModel()
		if model == "" {
			http.Error(w, "no default model configured - please configure a default provider", http.StatusPreconditionFailed)
			return
		}
		sender := gw.NewSSEStreamSender(w)
		req := gw.ChatCompletionRequest{Model: model, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(msg))}, Stream: true}
		_ = gw.HandleChatStream(ctx, bundle.router, req, func(c gw.ChatResponseChunk) error { return sender.Send(&c) })
	})
	return path, h
}
