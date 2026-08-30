package contextkeys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/everstacklabs/everstack/pkg/ctxkeys"
	"google.golang.org/grpc/metadata"
)

// Context key types for user identification
const (
	// UserIDKey stores the extracted user ID in context
	UserIDKey key = iota + 100 // Start at 100 to avoid conflicts with existing keys
	// APIKeyHashKey stores the hashed API key in context
	APIKeyHashKey
	// TenantIDKey stores the tenant/organization ID in context
	TenantIDKey
	// UserRoleKey stores the caller's org-level role (owner/admin/member/viewer)
	// in the active tenant, resolved once at session-auth time. Consumed by the
	// authorization layer to bridge session membership into per-resource checks.
	UserRoleKey
)

// ExtractUserID extracts the user ID from the verified request context.
//
// SECURITY (audit finding H6): identity must come only from authenticated
// middleware, never from a client-controlled header. The previous
// implementation fell back to x-user-id / x-mf-user-id gRPC metadata, which a
// caller could set to impersonate any user (the REST gateway forwards x-user-id
// for sticky-key selection, so it is fully client-controlled). That fallback is
// removed. Resolution order is now:
//  1. The user id installed in context by an authenticated interceptor.
//  2. A deterministic id derived from the authenticated API key hash.
//  3. "anonymous".
func ExtractUserID(ctx context.Context, apiKeyHash string) string {
	// 1. Verified context value.
	if userID, ok := ctx.Value(UserIDKey).(string); ok && userID != "" {
		return userID
	}

	// 2. Deterministic id from the authenticated API key hash.
	if apiKeyHash != "" {
		// Use first 12 chars of hash for readability
		prefix := apiKeyHash
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		return "user_" + prefix
	}

	// 3. Ultimate fallback.
	return "anonymous"
}

// ExtractTenantID extracts the tenant/organization ID from the verified request
// context only.
//
// SECURITY (audit finding H6): the tenant boundary must never be derived from a
// client-controlled header. The previous implementation fell back to x-org-id /
// x-everstack-org-id gRPC metadata, which is the documented tenant-isolation P0
// pattern (a caller could read another tenant's data by setting the header when
// no tenant was yet in context). That fallback is removed; callers that reach
// here with no tenant in context get "" and must fail closed.
func ExtractTenantID(ctx context.Context) string {
	if tenantID := GetTenantID(ctx); tenantID != "" {
		return tenantID
	}

	if tenantID, ok := ctx.Value(TenantIDKey).(string); ok && tenantID != "" {
		return tenantID
	}

	return ""
}

// ExtractAPIKeyHash extracts or computes the API key hash from context/headers.
func ExtractAPIKeyHash(ctx context.Context) string {
	// 1. Try to get from context (if already stored)
	if hash, ok := ctx.Value(APIKeyHashKey).(string); ok && hash != "" {
		return hash
	}

	// 2. Try to get API key from headers and hash it
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		apiKeyHeaders := []string{"x-evs-api-key", "x-mf-api-key", "x-everstack-api-key"}
		for _, header := range apiKeyHeaders {
			if values := md.Get(header); len(values) > 0 && values[0] != "" {
				return hashString(values[0])
			}
		}
	}

	return ""
}

// WithUserID returns a new context with the user ID stored.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// WithAPIKeyHash returns a new context with the API key hash stored.
func WithAPIKeyHash(ctx context.Context, hash string) context.Context {
	return context.WithValue(ctx, APIKeyHashKey, hash)
}

// WithAuthenticatedAPIKey installs the identity derived from a successfully
// validated tenant API key. Call this only after key verification succeeds.
func WithAuthenticatedAPIKey(ctx context.Context, tenantID, hash string) context.Context {
	if tenantID != "" {
		ctx = WithTenantID(ctx, tenantID)
	}
	if hash != "" {
		ctx = WithAPIKeyHash(ctx, hash)
	}
	return WithTenantAuthenticated(ctx)
}

// WithTenantID returns a new context with the tenant ID stored.
// Delegates to pkg/ctxkeys for cross-module compatibility.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return ctxkeys.WithTenantID(ctx, tenantID)
}

// GetUserID retrieves the user ID from context.
func GetUserID(ctx context.Context) string {
	if userID, ok := ctx.Value(UserIDKey).(string); ok {
		return userID
	}
	return ""
}

// WithUserRole returns a new context with the caller's active-tenant role
// (owner/admin/member/viewer) stored. Set once at session-auth time.
func WithUserRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, UserRoleKey, role)
}

// GetUserRole retrieves the caller's active-tenant role from context, or "".
func GetUserRole(ctx context.Context) string {
	if role, ok := ctx.Value(UserRoleKey).(string); ok {
		return role
	}
	return ""
}

// GetAuthenticatedUserID returns the concrete user identity populated by
// either API-key/JWT authentication or the Admin UI's cookie-session
// middleware. It does not synthesize an anonymous fallback, which makes it
// suitable for audit actors and explicit user allowlists.
func GetAuthenticatedUserID(ctx context.Context) string {
	if userID := GetUserID(ctx); userID != "" {
		return userID
	}
	return CloudUserIDFromContext(ctx)
}

// GetAPIKeyHash retrieves the API key hash from context.
func GetAPIKeyHash(ctx context.Context) string {
	if hash, ok := ctx.Value(APIKeyHashKey).(string); ok {
		return hash
	}
	return ""
}

// GetTenantID retrieves the tenant ID from context.
// Delegates to pkg/ctxkeys for cross-module compatibility.
func GetTenantID(ctx context.Context) string {
	return ctxkeys.TenantIDFromContext(ctx)
}

// HasVerifiedTenantPrincipal reports whether the context carries a tenant
// identity bound to authentication evidence produced by Everstack middleware.
//
// TenantAuthenticated alone is not sufficient evidence: the legacy
// same-origin compatibility path sets that flag and may copy x-tenant-id from
// the request. Requiring either a validated API-key hash or a validated user
// identity keeps callers from turning a bare tenant header into entitlement
// authority.
func HasVerifiedTenantPrincipal(ctx context.Context) bool {
	if !IsTenantAuthenticated(ctx) || GetTenantID(ctx) == "" {
		return false
	}

	return GetAPIKeyHash(ctx) != "" || GetAuthenticatedUserID(ctx) != ""
}

// hashString creates a SHA256 hash of the input string.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ExtractRequestContext extracts all user/tenant context from the request.
// This is a convenience function that extracts userID, apiKeyHash, and tenantID.
func ExtractRequestContext(ctx context.Context) (userID, apiKeyHash, tenantID string) {
	apiKeyHash = ExtractAPIKeyHash(ctx)
	userID = ExtractUserID(ctx, apiKeyHash)
	tenantID = ExtractTenantID(ctx)
	return
}

// WithRequestContext enriches context with user, API key hash, and tenant ID.
func WithRequestContext(ctx context.Context, userID, apiKeyHash, tenantID string) context.Context {
	if userID != "" {
		ctx = WithUserID(ctx, userID)
	}
	if apiKeyHash != "" {
		ctx = WithAPIKeyHash(ctx, apiKeyHash)
	}
	if tenantID != "" {
		ctx = WithTenantID(ctx, tenantID)
	}
	return ctx
}

// WithAPIKeyHashSecret stores a per-tenant HMAC secret in the context.
// Delegates to pkg/ctxkeys for cross-module compatibility.
func WithAPIKeyHashSecret(ctx context.Context, secret string) context.Context {
	return ctxkeys.WithAPIKeyHashSecret(ctx, secret)
}

// APIKeyHashSecretFromContext retrieves the per-tenant HMAC secret, or "".
// Delegates to pkg/ctxkeys for cross-module compatibility.
func APIKeyHashSecretFromContext(ctx context.Context) string {
	return ctxkeys.APIKeyHashSecretFromContext(ctx)
}

// WithInternalCall marks a context as an internal call.
// Internal calls (like embedding generation for semantic cache) should not be
// tracked in health metrics or counted as user requests.
func WithInternalCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, InternalCall, true)
}

// IsInternalCall checks if the context is marked as an internal call.
func IsInternalCall(ctx context.Context) bool {
	if v, ok := ctx.Value(InternalCall).(bool); ok {
		return v
	}
	return false
}
