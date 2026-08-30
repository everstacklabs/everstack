// Package billingidentity resolves the stable billing identity for managed
// gateway tenants. Usage can be stored under either an organization ID or any
// current or deleted instance ID, so billing must keep historical aliases for
// the lifetime of the organization.
package billingidentity

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Organization is the canonical owner of managed usage.
type Organization struct {
	ID   string `db:"organization_id"`
	Tier string `db:"plan_tier"`
}

const resolveOrganizationQuery = `
	SELECT o.id::text AS organization_id, LOWER(TRIM(o.plan_tier)) AS plan_tier
	FROM everstack.organizations AS o
	LEFT JOIN everstack.managed_instances AS mi ON mi.organization_id = o.id
	LEFT JOIN everstack.tenant_config AS tc ON tc.organization_id = o.id
	WHERE o.id::text = $1 OR mi.id::text = $1 OR tc.instance_id::text = $1
	LIMIT 1`

const resolveActiveOrganizationQuery = `
	SELECT o.id::text AS organization_id, LOWER(TRIM(o.plan_tier)) AS plan_tier
	FROM everstack.organizations AS o
	LEFT JOIN everstack.managed_instances AS mi ON mi.organization_id = o.id AND mi.deleted_at IS NULL
	LEFT JOIN everstack.tenant_config AS tc ON tc.organization_id = o.id
	WHERE o.id::text = $1 OR mi.id::text = $1 OR tc.instance_id::text = $1
	LIMIT 1`

const listOrganizationAliasesQuery = `
	SELECT $1::text AS tenant_id
	UNION
	SELECT id::text AS tenant_id
	FROM everstack.managed_instances
	WHERE organization_id::text = $1
	UNION
	SELECT instance_id::text AS tenant_id
	FROM everstack.tenant_config
	WHERE organization_id::text = $1`

// ResolveOrganization maps an organization, managed-instance, or legacy
// tenant-config ID to the canonical organization. Soft-deleted instances stay
// resolvable because their historical usage remains billable.
func ResolveOrganization(ctx context.Context, db *sqlx.DB, tenantID string) (Organization, error) {
	return resolveOrganization(ctx, db, tenantID, resolveOrganizationQuery)
}

// ResolveActiveOrganization resolves identities used for new allocations and
// entitlements. Unlike historical metering, a soft-deleted instance must not
// regain access merely because its organization still has an active plan.
func ResolveActiveOrganization(ctx context.Context, db *sqlx.DB, tenantID string) (Organization, error) {
	return resolveOrganization(ctx, db, tenantID, resolveActiveOrganizationQuery)
}

func resolveOrganization(ctx context.Context, db *sqlx.DB, tenantID, query string) (Organization, error) {
	if db == nil || strings.TrimSpace(tenantID) == "" {
		return Organization{}, fmt.Errorf("managed organization database and tenant ID are required")
	}
	var organization Organization
	if err := db.GetContext(ctx, &organization, query, tenantID); err != nil {
		return Organization{}, err
	}
	return organization, nil
}

// ListOrganizationAliases returns every durable tenant identity whose usage
// belongs to an organization. Deleted managed instances are intentionally
// included so a cumulative lifetime meter can never move backwards.
func ListOrganizationAliases(ctx context.Context, db *sqlx.DB, organizationID string) ([]string, error) {
	if db == nil || strings.TrimSpace(organizationID) == "" {
		return nil, fmt.Errorf("managed organization database and organization ID are required")
	}
	var aliases []string
	if err := db.SelectContext(ctx, &aliases, listOrganizationAliasesQuery, organizationID); err != nil {
		return nil, err
	}
	return aliases, nil
}
