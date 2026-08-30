package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ManagedTenantSource pages through the authoritative cloud tenant inventory.
// Implementations must return IDs in ascending order after the supplied cursor.
type ManagedTenantSource interface {
	ListManagedTenantIDs(ctx context.Context, after string, limit int) ([]string, error)
}

// ManagedDefaultsBackfillReport records resumable progress without exposing
// storage placement or credential data.
type ManagedDefaultsBackfillReport struct {
	TenantsScanned  int
	DefaultsEnsured int
}

// BackfillManagedDefaults creates or repairs the stable system-managed default
// for every tenant returned by source. It stops at the first failure so a
// subsequent idempotent run can safely resume the complete inventory.
func BackfillManagedDefaults(
	ctx context.Context,
	source ManagedTenantSource,
	defaults ManagedDefaultEnsurer,
	batchSize int,
) (ManagedDefaultsBackfillReport, error) {
	var report ManagedDefaultsBackfillReport
	if source == nil {
		return report, errors.New("managed storage tenant source is required")
	}
	if defaults == nil {
		return report, errors.New("managed storage default ensurer is required")
	}
	if batchSize <= 0 {
		return report, errors.New("managed storage backfill batch size must be positive")
	}

	after := ""
	for {
		tenantIDs, err := source.ListManagedTenantIDs(ctx, after, batchSize)
		if err != nil {
			return report, fmt.Errorf("list managed storage tenants: %w", err)
		}
		if len(tenantIDs) == 0 {
			return report, nil
		}
		for _, tenantID := range tenantIDs {
			tenantID = strings.TrimSpace(tenantID)
			report.TenantsScanned++
			if tenantID == "" {
				return report, errors.New("managed storage tenant inventory returned an empty ID")
			}
			if _, err := defaults.EnsureDefault(ctx, tenantID); err != nil {
				return report, fmt.Errorf("ensure managed storage default for tenant %q: %w", tenantID, err)
			}
			report.DefaultsEnsured++
		}
		next := strings.TrimSpace(tenantIDs[len(tenantIDs)-1])
		if next == "" || next == after {
			return report, errors.New("managed storage tenant inventory did not advance")
		}
		after = next
	}
}
