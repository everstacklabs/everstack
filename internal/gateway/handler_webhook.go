package gateway

import (
	"net/http"
)

// WebhookHandler delegates webhook requests to a sandbox webhook router.
// This absorbs the old WebhookRouter by delegating to any http.Handler
// that can serve /wh/{path} requests.
type WebhookHandler struct {
	handler http.Handler
}

// NewWebhookHandler creates a webhook handler wrapping an existing handler.
// The inner handler should process requests with the webhook path already
// set on the URL (i.e., the /wh/ prefix is restored before forwarding).
func NewWebhookHandler(handler http.Handler) *WebhookHandler {
	return &WebhookHandler{handler: handler}
}

// ServeHTTP forwards the request to the underlying webhook handler.
// The request URL path is restored to include the /wh/ prefix so the
// inner handler sees the same path it would on the main API router.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, webhookPath string) {
	// Restore the /wh/ prefix so the inner handler can parse it
	r.URL.Path = "/wh/" + webhookPath
	h.handler.ServeHTTP(w, r)
}
