//go:build !dev && !enterprise

package enterprise

import (
	"github.com/jmoiron/sqlx"

	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
)

// CE is the DEFAULT build variant: an untagged `go build` produces a
// Community Edition binary with CE limits and feature gates enforced.
// Unlocked dev behavior requires an explicit -tags dev; the permissive
// variant must never be the accidental default (editions-and-billing.md, D3).

// NewLicenseEnforcer returns a no-op enforcer in CE builds.
func NewLicenseEnforcer(_ *cqrs.System, _ ...*apilic.Policy) LicenseEnforcer {
	return noopLicenseEnforcer{}
}

// NewLicenseMonitor returns a no-op monitor in CE builds.
func NewLicenseMonitor(_ LicenseEnforcer, _ MonitorConfig) LicenseMonitor {
	return noopLicenseMonitor{}
}

// NewPersistentMonitor returns a no-op persistent monitor in CE builds.
func NewPersistentMonitor(_ LicenseMonitor, _ *cache.RedisClient, _ StorageConfig) PersistentMonitor {
	return noopLicenseMonitor{}
}

// NewInstanceManager returns a no-op instance manager in CE builds.
func NewInstanceManager(_ *sqlx.DB, _ InstanceConfig) InstanceManager {
	return noopInstanceManager{}
}
