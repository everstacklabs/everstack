package provider_api_keys

import (
	"context"
	"errors"
	"time"
)

// ErrConfigKeyDuplicatesManual is returned by UpsertConfigKey when a
// user-managed key already holds the same credential for that provider
// configuration. It reports that no config key was written, which is the
// intended outcome rather than a failure: the credential is already served by
// the row its owner controls.
var ErrConfigKeyDuplicatesManual = errors.New("config API key duplicates a user-managed key")

// ErrKeyNotFound reports that no API key matched. The tenant-scoped methods
// return it for keys whose configuration another tenant (or the platform)
// owns, so callers cannot tell "foreign" from "missing".
var ErrKeyNotFound = errors.New("provider API key not found")

// ProviderAPIKey represents a single API key for a provider configuration
type ProviderAPIKey struct {
	ID               string
	ProviderConfigID string
	KeyName          string
	KeyEncrypted     string
	Weight           int
	IsActive         bool
	Source           string // SourceBYOK or SourcePlatform: how the row was written, not who owns it (see BillingSource)
	RateLimitData    map[string]interface{}
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Repository defines the interface for managing provider API keys
type Repository interface {
	// ListByProviderConfig returns all API keys for a given provider configuration
	ListByProviderConfig(ctx context.Context, providerConfigID string) ([]*ProviderAPIKey, error)

	// Create creates a new API key
	Create(ctx context.Context, key *ProviderAPIKey) error

	// Update updates an existing API key
	Update(ctx context.Context, key *ProviderAPIKey) error

	// Delete deletes an API key by ID
	Delete(ctx context.Context, id string) error

	// UpdateRateLimitData updates the rate limit tracking data for a key
	UpdateRateLimitData(ctx context.Context, keyID string, data map[string]interface{}) error

	// GetByID retrieves a single API key by ID
	GetByID(ctx context.Context, id string) (*ProviderAPIKey, error)

	// UpsertConfigKey upserts a config-sourced API key (special handling).
	// It returns ErrConfigKeyDuplicatesManual, and writes nothing, when a
	// user-managed key already holds the same credential.
	UpsertConfigKey(ctx context.Context, key *ProviderAPIKey) error

	// DeactivateConfigKeys marks all config-sourced keys as inactive for a provider
	DeactivateConfigKeys(ctx context.Context, providerConfigID string) error

	// The *ForOrg methods act only on keys whose provider configuration is
	// owned by orgID (provider_configurations.organization_id = orgID).
	// Platform-owned configurations (organization_id NULL) never match. A
	// key outside that scope is reported as ErrKeyNotFound, exactly like a
	// key that does not exist. Request-handling code must use these.

	// ListByProviderConfigForOrg returns the keys of a configuration orgID
	// owns, or an empty list when it owns no such configuration.
	ListByProviderConfigForOrg(ctx context.Context, orgID, providerConfigID string) ([]*ProviderAPIKey, error)

	// GetByIDForOrg retrieves a key whose configuration orgID owns.
	GetByIDForOrg(ctx context.Context, orgID, id string) (*ProviderAPIKey, error)

	// CreateForOrg inserts key only when orgID owns key.ProviderConfigID.
	CreateForOrg(ctx context.Context, orgID string, key *ProviderAPIKey) error

	// UpdateForOrg updates a key whose configuration orgID owns.
	UpdateForOrg(ctx context.Context, orgID string, key *ProviderAPIKey) error

	// DeleteForOrg deletes a key whose configuration orgID owns.
	DeleteForOrg(ctx context.Context, orgID, id string) error
}
