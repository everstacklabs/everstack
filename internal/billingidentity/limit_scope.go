package billingidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ErrNoOrganization means no live organization claims the tenant id.
var ErrNoOrganization = errors.New("tenant maps to no organization")

// ErrContestedTenant means more than one organization claims the tenant id,
// so its rows cannot be attributed to either one.
var ErrContestedTenant = errors.New("tenant is claimed by more than one organization")

// LimitScope is the unit a plan cap is counted over: one organization and
// every tenant id whose rows belong to it.
type LimitScope struct {
	Organization
	// Aliases are lowercase tenant ids of the organization's live identities.
	// Deleted instances are excluded (their objects are unreachable and must
	// not hold the cap), and so is any id another organization also claims.
	Aliases []string
	// Exempt marks an internal organization: plan caps do not apply. It is
	// read from organizations.plan_limit_exempt, which only migrations and
	// ops tooling write; no customer API can reach it.
	Exempt bool
	// DeletedAliases are the organization's deleted managed instances (not
	// claimed by any other organization). Their objects no longer count, but
	// their hosted sites do until the takedown sweep has removed them.
	DeletedAliases []string
}

// activeOwnersQuery lists the organizations that claim a tenant id for new
// allocations. plan_limit_exempt is read through to_jsonb so a gateway that
// deploys before the services migration adds the column reads FALSE instead
// of failing every lookup. A soft-deleted managed instance claims nothing here, so it
// cannot regain an allocation scope. LIMIT 2 is enough to detect ambiguity.
const activeOwnersQuery = `
	SELECT o.id::text AS organization_id, LOWER(TRIM(o.plan_tier)) AS plan_tier,
	       COALESCE((to_jsonb(o) ->> 'plan_limit_exempt')::boolean, FALSE) AS plan_limit_exempt
	FROM everstack.organizations AS o
	WHERE o.id::text = $1
	   OR EXISTS (SELECT 1 FROM everstack.managed_instances AS mi
	              WHERE mi.organization_id = o.id AND mi.deleted_at IS NULL AND mi.id::text = $1)
	   OR EXISTS (SELECT 1 FROM everstack.tenant_config AS tc
	              WHERE tc.organization_id = o.id AND tc.instance_id::text = $1
	                AND NOT EXISTS (SELECT 1 FROM everstack.managed_instances AS gone
	                                WHERE gone.id = tc.instance_id AND gone.deleted_at IS NOT NULL))
	ORDER BY o.id
	LIMIT 2`

// uncontestedAliasesQuery lists an organization's LIVE aliases (its own id,
// managed instances that are not deleted, and tenant_config instances whose
// managed instance is not deleted) minus any alias a different organization
// also claims. Claims by other organizations count even when deleted, so an
// id once owned elsewhere is never attributed here.
//
// Deleted instances are excluded on purpose: the customer can no longer
// reach their objects, so they must not hold the organization's cap. This
// differs from the billing reporter (ListOrganizationAliases), which keeps
// deleted aliases so cumulative meters never move backwards.
const uncontestedAliasesQuery = `
	WITH claims AS (
		SELECT id AS organization_id, id::text AS alias, TRUE AS live FROM everstack.organizations
		UNION
		SELECT organization_id, id::text, deleted_at IS NULL FROM everstack.managed_instances
		UNION
		SELECT tc.organization_id, tc.instance_id::text,
		       NOT EXISTS (SELECT 1 FROM everstack.managed_instances AS gone
		                   WHERE gone.id = tc.instance_id AND gone.deleted_at IS NOT NULL)
		FROM everstack.tenant_config AS tc
	)
	SELECT DISTINCT c.alias
	FROM claims AS c
	WHERE c.organization_id::text = $1
	  AND c.live
	  AND NOT EXISTS (
		SELECT 1 FROM claims AS other
		WHERE other.alias = c.alias AND other.organization_id <> c.organization_id)
	ORDER BY c.alias`

// deletedAliasesQuery is uncontestedAliasesQuery for the organization's
// deleted identities instead of its live ones.
const deletedAliasesQuery = `
	WITH claims AS (
		SELECT id AS organization_id, id::text AS alias, TRUE AS live FROM everstack.organizations
		UNION
		SELECT organization_id, id::text, deleted_at IS NULL FROM everstack.managed_instances
		UNION
		SELECT tc.organization_id, tc.instance_id::text,
		       NOT EXISTS (SELECT 1 FROM everstack.managed_instances AS gone
		                   WHERE gone.id = tc.instance_id AND gone.deleted_at IS NOT NULL)
		FROM everstack.tenant_config AS tc
	)
	SELECT DISTINCT c.alias
	FROM claims AS c
	WHERE c.organization_id::text = $1
	  AND NOT c.live
	  AND NOT EXISTS (
		SELECT 1 FROM claims AS other
		WHERE other.alias = c.alias AND other.organization_id <> c.organization_id)
	ORDER BY c.alias`

// ResolveLimitScope maps a tenant id (organization, managed instance or
// tenant-config instance) to the organization a plan cap is counted over.
//
// It refuses rather than guesses: a tenant id no live organization claims
// returns ErrNoOrganization, and one that two organizations claim (now or
// through a deleted instance) returns ErrContestedTenant. Callers treat both
// as "no organization scope", never as some other organization's scope.
func ResolveLimitScope(ctx context.Context, db *sqlx.DB, tenantID string) (LimitScope, error) {
	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	if db == nil || tenantID == "" {
		return LimitScope{}, fmt.Errorf("managed organization database and tenant ID are required")
	}

	var owners []struct {
		Organization
		Exempt bool `db:"plan_limit_exempt"`
	}
	if err := db.SelectContext(ctx, &owners, activeOwnersQuery, tenantID); err != nil {
		return LimitScope{}, fmt.Errorf("resolve tenant owner: %w", err)
	}
	switch len(owners) {
	case 0:
		return LimitScope{}, ErrNoOrganization
	case 1:
	default:
		return LimitScope{}, ErrContestedTenant
	}

	var aliases []string
	if err := db.SelectContext(ctx, &aliases, uncontestedAliasesQuery, owners[0].Organization.ID); err != nil {
		return LimitScope{}, fmt.Errorf("list organization aliases: %w", err)
	}
	var deletedAliases []string
	if err := db.SelectContext(ctx, &deletedAliases, deletedAliasesQuery, owners[0].ID); err != nil {
		return LimitScope{}, fmt.Errorf("list deleted organization aliases: %w", err)
	}
	for i, alias := range deletedAliases {
		deletedAliases[i] = strings.ToLower(strings.TrimSpace(alias))
	}
	found := false
	for i, alias := range aliases {
		aliases[i] = strings.ToLower(strings.TrimSpace(alias))
		if aliases[i] == tenantID {
			found = true
		}
	}
	if !found {
		// The tenant is live for this organization but another one also
		// claims it (through a deleted instance), so its rows are not this
		// organization's alone.
		return LimitScope{}, ErrContestedTenant
	}
	return LimitScope{Organization: owners[0].Organization, Aliases: aliases, Exempt: owners[0].Exempt, DeletedAliases: deletedAliases}, nil
}

// organizationExemptQuery reads one organization's exemption by its id, the
// same tolerant way as activeOwnersQuery. No row reads as not exempt.
const organizationExemptQuery = `
	SELECT COALESCE((SELECT (to_jsonb(o) ->> 'plan_limit_exempt')::boolean
	                 FROM everstack.organizations AS o
	                 WHERE o.id::text = $1), FALSE)`

// OrganizationExempt reports whether organizationID is an internal
// organization (the same flag LimitScope.Exempt carries). It only reads, and
// it matches the organization id exactly: an instance or tenant id is never
// exempt here, so a caller must resolve the organization first.
func OrganizationExempt(ctx context.Context, q sqlx.QueryerContext, organizationID string) (bool, error) {
	organizationID = strings.ToLower(strings.TrimSpace(organizationID))
	if q == nil || organizationID == "" {
		return false, nil
	}
	var exempt bool
	if err := sqlx.GetContext(ctx, q, &exempt, organizationExemptQuery, organizationID); err != nil {
		return false, fmt.Errorf("read organization exemption: %w", err)
	}
	return exempt, nil
}

// countOwnedFreeOrganizationsQuery counts the organizations a user owns that
// are on the free (Starter) plan and are not internal, the set the free
// organization cap is counted over. Paid orgs are not limited. The exempt
// flag is read the same tolerant way as activeOwnersQuery.
const countOwnedFreeOrganizationsQuery = `
	SELECT COUNT(DISTINCT o.id)
	FROM everstack.organizations AS o
	JOIN everstack.organization_members AS m ON m.organization_id = o.id
	WHERE m.user_id = $1::uuid AND m.role = 'owner'
	  AND COALESCE(LOWER(TRIM(o.plan_tier)), '') IN ('', 'free', 'starter')
	  AND NOT COALESCE((to_jsonb(o) ->> 'plan_limit_exempt')::boolean, FALSE)`

// CountOwnedFreeOrganizations returns how many free, non-internal
// organizations userID owns. It only reads. q may be a transaction so the
// count can sit under the caller's lock.
func CountOwnedFreeOrganizations(ctx context.Context, q sqlx.QueryerContext, userID string) (int, error) {
	var n int
	if err := sqlx.GetContext(ctx, q, &n, countOwnedFreeOrganizationsQuery, userID); err != nil {
		return 0, fmt.Errorf("count owned free organizations: %w", err)
	}
	return n, nil
}
