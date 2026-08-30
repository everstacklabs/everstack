package contextkeys

import (
	"context"

	"github.com/everstacklabs/everstack/pkg/ctxkeys"
)

// key is an unexported type to avoid collisions with other context keys.
// Use the exported constants below to store and retrieve values from context.
type key int

const (
	// GatewayConfig stores a pointer to validator.GatewayConfig in context.
	GatewayConfig key = iota
	// ServerConfig stores a pointer to validator.ServerConfig in context.
	ServerConfig
	// FeaturesConfig stores a pointer to validator.FeaturesConfig in context.
	FeaturesConfig
	// PrimaryDB stores *sqlx.DB for convenience in HTTP layer (legacy path).
	PrimaryDB
	// LicenseEnforcer stores a shared *LicenseEnforcer for HTTP/proxy reuse.
	LicenseEnforcer
	// InstanceManager stores *instance.Manager for gateway activation.
	InstanceManager
	// LicenseServiceURL stores the URL of the license service for activation calls.
	LicenseServiceURL
	// CatalogSync stores the catalog sync service for provider catalog updates.
	CatalogSync
	// EncryptionService stores the encryption service for API key encryption/decryption.
	EncryptionService
	// ProviderRepo stores the provider configuration repository.
	ProviderRepo
	// APIKeyRepo stores the provider API keys repository.
	APIKeyRepo
	// CatalogService stores the model catalog service for validation and lookups.
	CatalogService
	// LicenseMonitor stores the license monitor for usage tracking and feature gates.
	LicenseMonitor
	// InternalCall marks a context as an internal call (e.g., embedding for semantic cache)
	// When set to true, health tracking and certain metrics should be skipped.
	InternalCall
	// DeviceFingerprint stores the device fingerprint for usage tracking before activation.
	DeviceFingerprint
	// CloudPublicKey stores the base64-encoded ed25519 public key for verifying cloud callbacks.
	CloudPublicKey
	// RuntimeConfigService stores the runtime config service for hot-reload configuration.
	RuntimeConfigService
	// ToolLoopEnabled stores whether the tool loop feature is enabled.
	ToolLoopEnabled
	// IsolationAvailable stores whether Docker isolation backend is available for isolated functions.
	IsolationAvailable
	// APIKeyHashSecretKey stores a per-tenant HMAC secret for API key hashing.
	APIKeyHashSecretKey
	// SharedGatewayMode marks the gateway as running in shared (multi-tenant) mode.
	// When true, handlers should treat the instance as managed/cloud.
	SharedGatewayMode
	// TenantAuthenticated is kept for backward compatibility.
	// New code should use WithTenantAuthenticated/IsTenantAuthenticated
	// which delegate to pkg/ctxkeys for cross-module compatibility.
	TenantAuthenticated
	// BillingDB stores the *sqlx.DB that owns the billing.* schema: the platform
	// DB on cloud, the single collapsed DB on CE. This is DISTINCT from PrimaryDB,
	// which on cloud is the per-tenant gateway DB and does NOT have billing.* .
	// Wallet and sandbox-entitlement lookups from the gateway handler must use
	// this key, not PrimaryDB, or they hit relation-not-found and fail closed.
	BillingDB
)

// WithCloudUserID stores the cloud-authenticated user ID in context.
// Delegates to pkg/ctxkeys for cross-module compatibility.
func WithCloudUserID(ctx context.Context, userID string) context.Context {
	return ctxkeys.WithCloudUserID(ctx, userID)
}

// CloudUserIDFromContext retrieves the cloud user ID from context, or "".
// Delegates to pkg/ctxkeys for cross-module compatibility.
func CloudUserIDFromContext(ctx context.Context) string {
	return ctxkeys.CloudUserIDFromContext(ctx)
}

// WithTenantAuthenticated marks a request as already authenticated by the
// tenant middleware. Delegates to pkg/ctxkeys for cross-module compatibility.
func WithTenantAuthenticated(ctx context.Context) context.Context {
	return ctxkeys.WithTenantAuthenticated(ctx)
}

// IsTenantAuthenticated returns true if the request was already authenticated
// by the tenant middleware. Delegates to pkg/ctxkeys for cross-module compatibility.
func IsTenantAuthenticated(ctx context.Context) bool {
	return ctxkeys.IsTenantAuthenticated(ctx)
}
