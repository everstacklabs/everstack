package features

import "time"

// SchemaVersion is the current manifest schema version
const SchemaVersion = "2.0.0"

// Manifest is the global feature manifest received from the edge
type Manifest struct {
	Version       int64                        `json:"version"`
	SchemaVersion string                       `json:"schema_version"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	Features      map[string]FeatureDefinition `json:"features"`
	Signature     string                       `json:"signature"`
	PublicKeyID   string                       `json:"public_key_id"`
}

// FeatureDefinition is a feature in the manifest
type FeatureDefinition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Enabled     bool     `json:"enabled"`
	Categories  []string `json:"categories"`
	MinTier     string   `json:"min_tier,omitempty"`
}

// TenantOverlay is a per-tenant feature override manifest
type TenantOverlay struct {
	TenantID    string                       `json:"tenant_id"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Overrides   map[string]TenantOverrideVal `json:"overrides"`
	Signature   string                       `json:"signature"`
	PublicKeyID string                       `json:"public_key_id"`
}

// TenantOverrideVal is an individual override in a tenant overlay
type TenantOverrideVal struct {
	Enabled   bool       `json:"enabled"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ResolvedFeature is the final resolved state of a feature after
// applying tier evaluation and tenant overrides
type ResolvedFeature struct {
	Name        string
	Description string
	Status      string
	Categories  []string
	Enabled     bool
}

// signedPayload mirrors the license service's canonical payload for verification
type signedPayload struct {
	Version       int64                        `json:"version,omitempty"`
	SchemaVersion string                       `json:"schema_version,omitempty"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	Features      map[string]FeatureDefinition `json:"features,omitempty"`
	TenantID      string                       `json:"tenant_id,omitempty"`
	Overrides     map[string]TenantOverrideVal `json:"overrides,omitempty"`
}
