package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	http_util "github.com/everstacklabs/everstack/internal/api/http"
)

// M2MAuthInterceptorForPrefixes enforces presence of M2M credentials
// (Authorization: Bearer <token> or x-everstack-api-key) only for methods
// whose fully-qualified procedure starts with any of the provided prefixes.
// Example prefix: "/everstack.health.v1.HealthService/".
func M2MAuthInterceptorForPrefixes(prefixes []string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Enforce only if the method matches a configured public prefix
			procedure := req.Spec().Procedure
			if !hasAnyPrefix(procedure, prefixes) {
				return next(ctx, req)
			}

			if !hasM2MCredentials(req.Header()) {
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing M2M credentials"))
			}
			return next(ctx, req)
		}
	}
}

// M2MAuthInterceptor enforces M2M credentials when match(procedure) is true.
func M2MAuthInterceptor(match func(procedure string) bool) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !match(req.Spec().Procedure) {
				return next(ctx, req)
			}
			if !hasM2MCredentials(req.Header()) {
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing M2M credentials"))
			}
			return next(ctx, req)
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func hasM2MCredentials(h http.Header) bool {
	if key := h.Get(http_util.EverstackApiKey); key != "" {
		return true
	}
	if auth := h.Get(http_util.Authorization); strings.HasPrefix(auth, "Bearer ") && len(auth) > len("Bearer ") {
		return true
	}
	// Check for HMAC-signed M2M credentials (service token or instance ID + signature)
	hasIdentity := h.Get(HeaderServiceToken) != "" || h.Get(HeaderInstanceID) != ""
	hasSignature := h.Get(HeaderSignature) != ""
	if hasIdentity && hasSignature {
		return true
	}
	return false
}
