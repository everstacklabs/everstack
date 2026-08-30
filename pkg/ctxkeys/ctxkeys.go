// Package ctxkeys provides context key helpers for cross-module use.
// Internal packages should continue using internal/lib/context_keys;
// this package is for external modules (e.g. the cloud repo) that need
// to share context values with the public repo's middleware and handlers.
package ctxkeys

import "context"

type key int

const (
	// TenantAuthenticated marks a request as already authenticated by the
	// tenant middleware (cloud session validated). The API key interceptor
	// should skip its own auth check when this is set.
	TenantAuthenticated key = iota

	tenantIDKey
	apiKeyHashSecretKey
	sharedGatewayModeKey
	cloudUserIDKey
)

// WithTenantID stores the tenant/organization ID in context.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantIDKey, tenantID)
}

// TenantIDFromContext retrieves the tenant ID from context, or "".
func TenantIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(tenantIDKey).(string)
	return s
}

// WithAPIKeyHashSecret stores a per-tenant HMAC secret for API key hashing.
func WithAPIKeyHashSecret(ctx context.Context, secret string) context.Context {
	return context.WithValue(ctx, apiKeyHashSecretKey, secret)
}

// APIKeyHashSecretFromContext retrieves the API key hash secret, or "".
func APIKeyHashSecretFromContext(ctx context.Context) string {
	s, _ := ctx.Value(apiKeyHashSecretKey).(string)
	return s
}

// WithSharedGatewayMode marks the gateway as running in shared (multi-tenant) mode.
func WithSharedGatewayMode(ctx context.Context, shared bool) context.Context {
	return context.WithValue(ctx, sharedGatewayModeKey, shared)
}

// IsSharedGatewayMode returns true if the gateway is in shared mode.
func IsSharedGatewayMode(ctx context.Context) bool {
	v, _ := ctx.Value(sharedGatewayModeKey).(bool)
	return v
}

// WithCloudUserID stores the cloud-authenticated user ID in context.
// Set by the tenant middleware after validating the cloud session.
func WithCloudUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, cloudUserIDKey, userID)
}

// CloudUserIDFromContext retrieves the cloud user ID from context, or "".
func CloudUserIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(cloudUserIDKey).(string)
	return s
}

// WithTenantAuthenticated marks a request as already authenticated by the
// tenant middleware. The API key interceptor should skip its own auth check.
func WithTenantAuthenticated(ctx context.Context) context.Context {
	return context.WithValue(ctx, TenantAuthenticated, true)
}

// IsTenantAuthenticated returns true if the request was already authenticated
// by the tenant middleware.
func IsTenantAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(TenantAuthenticated).(bool)
	return v
}
