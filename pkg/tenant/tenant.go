// Package tenant provides shared tenant configuration types used across
// both the open-source gateway (internal/) and the cloud control plane.
// It is intentionally placed under pkg/ so that external Go modules (e.g.
// the cloud repository) can import it without hitting Go's internal/
// package visibility restriction.
package tenant

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// JSONMap is a map[string]any that can be scanned from a Postgres JSONB column.
type JSONMap map[string]any

// Scan implements sql.Scanner for JSONB columns.
func (m *JSONMap) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("JSONMap.Scan: unsupported type %T", src)
	}
	return json.Unmarshal(data, m)
}

// Value implements driver.Valuer for JSONB columns.
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Config holds the resolved configuration for a single tenant.
// It is cached in-memory and injected into each request's context by the
// tenant middleware.
type Config struct {
	InstanceID       string     `db:"instance_id"   json:"instance_id"`
	OrganizationID   string     `db:"organization_id" json:"organization_id"`
	WorkspaceID      string     `db:"workspace_id"  json:"workspace_id"`
	Slug             string     `db:"slug"          json:"slug"`
	OrgSlug          string     `db:"org_slug"      json:"org_slug"`
	Region           string     `db:"region"        json:"region,omitempty"`
	SchemaName       string     `db:"schema_name"   json:"schema_name"`
	Status           string     `db:"status"        json:"status"`
	GatewayURL       string     `db:"gateway_url"   json:"gateway_url"`
	M2MSigningKey    *string    `db:"m2m_signing_key"     json:"-"`
	APIKeyHashSecret *string    `db:"api_key_hash_secret" json:"-"`
	ConfigOverrides  JSONMap    `db:"config_overrides"    json:"config_overrides,omitempty"`
	SchemaVersion    int64      `db:"schema_version"      json:"schema_version"`
	ShardID          *string    `db:"shard_id"            json:"shard_id,omitempty"`
	CreatedAt        time.Time  `db:"created_at"          json:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"          json:"updated_at"`
	PausedAt         *time.Time `db:"paused_at"           json:"paused_at,omitempty"`

	// PlanTier is the org's commercial plan ("free"/"basic"/"pro"/"enterprise"),
	// written at provision time and refreshed by the controlplane reconciler.
	// nil = not yet resolved: the gateway entitlements resolver treats nil as
	// the legacy managed bypass (no gateway-level plan limits), so enforcement
	// rolls out incrementally as rows are backfilled.
	// See docs/design/editions-and-billing.md, section 3.
	PlanTier *string `db:"plan_tier" json:"plan_tier,omitempty"`

	// CustomDomain is an optional tenant-owned domain (e.g. "api.acme.com")
	// that routes to this instance. Verification + cert issuance flow through
	// the controlplane. See docs/tenant-ingress.md.
	CustomDomain                  *string    `db:"custom_domain"                     json:"custom_domain,omitempty"`
	CustomDomainStatus            *string    `db:"custom_domain_status"              json:"custom_domain_status,omitempty"`
	CustomDomainVerificationToken *string    `db:"custom_domain_verification_token"  json:"-"`
	CustomDomainVerifiedAt        *time.Time `db:"custom_domain_verified_at"         json:"custom_domain_verified_at,omitempty"`
}

// configCtxKey is the context key for Config.
type configCtxKey struct{}

// WithConfig stores a Config in the context.
func WithConfig(ctx context.Context, tc *Config) context.Context {
	return context.WithValue(ctx, configCtxKey{}, tc)
}

// ConfigFromContext retrieves the Config from context, or nil.
func ConfigFromContext(ctx context.Context) *Config {
	tc, _ := ctx.Value(configCtxKey{}).(*Config)
	return tc
}
