package gateway

import (
	"context"
)

// ProxyRequest represents a raw proxy operation to a provider base URL.
// Useful when passing through provider-specific payloads that the gateway
// does not normalize.
type ProxyRequest struct {
	Provider string                 `json:"provider"`
	Method   string                 `json:"method"`
	Path     string                 `json:"path"`
	Headers  map[string]string      `json:"headers,omitempty"`
	Body     map[string]interface{} `json:"body,omitempty"`
}

// ProxyResponse is a raw provider response passthrough.
type ProxyResponse struct {
	Status  int                    `json:"status"`
	Headers map[string][]string    `json:"headers"`
	Body    map[string]interface{} `json:"body"`
}

// HandleProxy is a placeholder for future raw proxying support.
func HandleProxy(_ context.Context, _ ProxyRequest) (ProxyResponse, error) {
	// Implement once provider HTTP clients are wired.
	return ProxyResponse{}, nil
}
