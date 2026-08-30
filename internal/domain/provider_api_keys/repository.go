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

// ProviderAPIKey represents a single API key for a provider configuration
type ProviderAPIKey struct {
	ID               string
	ProviderConfigID string
	KeyName          string
	KeyEncrypted     string
	Weight           int
	IsActive         bool
	Source           string // "manual" or "config"
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
}
