// Package requesthost carries the HTTP request Host into Connect handlers.
//
// Connect hands a handler only the request headers, and net/http keeps Host
// out of the header map. Cookie helpers need the Host to decide whether a
// plain-HTTP request to a loopback, private or Tailscale address may receive a
// non-Secure session cookie (local development, port-forward, tailnet admin).
// Without it they must assume a public host and set Secure, which a browser on
// plain HTTP then refuses to store.
package requesthost

import (
	"context"
	"net/http"
)

type contextKey struct{}

// Middleware records r.Host in the request context. Wrap Connect handlers
// that set cookies with it. The value comes from the real request, never from
// a client-supplied header.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, r.Host)))
	})
}

// Request builds a minimal *http.Request for cookie helpers from a Connect
// handler's context and headers. Host is empty when Middleware did not run,
// which cookie helpers treat as a public host.
func Request(ctx context.Context, header http.Header) *http.Request {
	host, _ := ctx.Value(contextKey{}).(string)
	return &http.Request{Header: header.Clone(), Host: host}
}
