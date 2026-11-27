package github

import (
	"context"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// ErrTenantNotAuthorized is returned when a request has no verified tenant
// principal, or names a tenant that principal is not authorized for.
var ErrTenantNotAuthorized = errors.New("github: tenant is not authorized for this caller")

// AuthorizedTenantID returns the tenant id GitHub integration rows are keyed
// by for this request, derived from the authenticated principal only.
//
// The caller may still send a tenant_id (the admin UI sends its organization
// id in the body and the x-tenant-id header); it is honored only when it is
// one of the ids the principal already holds:
//
//   - the authenticated tenant id in context (API key org, self-hosted
//     session org, shared-gateway cookie org, or managed instance id), and
//   - on a managed instance, the owning organization of that instance, from
//     the request instance scope (gateway cookie auth) or the tenant config
//     (controlplane tenant middleware). Installations there have always been
//     keyed by organization id, because that is what the admin UI sends, so
//     the organization is the canonical key.
//
// Any other value is ignored, never trusted: the canonical id (the managed
// organization id when present, else the context tenant) is used instead.
// The admin UI sends its first organization, which for a member of several
// organizations can differ from the organization the request host selected;
// refusing that would break those users while adding no protection, since
// every store query is keyed on the returned id. Only a request with no
// verified principal is ErrTenantNotAuthorized.
func AuthorizedTenantID(ctx context.Context, requested string) (string, error) {
	if !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return "", ErrTenantNotAuthorized
	}
	principal := strings.TrimSpace(contextkeys.GetTenantID(ctx))
	if principal == "" {
		return "", ErrTenantNotAuthorized
	}

	// On a managed instance the principal's tenant is the instance id, but
	// GitHub rows have always been keyed by the owning organization (what the
	// admin UI sends). The gateway's cookie auth records that organization in
	// the request instance scope after verifying membership; the controlplane
	// tenant middleware records it in the tenant config.
	managedOrg := ""
	if scope, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok && strings.TrimSpace(scope.InstanceID) == principal {
		managedOrg = strings.TrimSpace(scope.OrganizationID)
	} else if tc := tenant.ConfigFromContext(ctx); tc != nil && strings.TrimSpace(tc.InstanceID) == principal {
		managedOrg = strings.TrimSpace(tc.OrganizationID)
	}

	requested = strings.TrimSpace(requested)
	if requested != "" && requested == principal {
		return principal, nil
	}
	if managedOrg != "" {
		return managedOrg, nil
	}
	return principal, nil
}

// TenantKeyResolver maps the tenant id a background caller holds (a
// sandbox's tenant, which is the instance id on a managed instance) to the
// key GitHub rows are stored under (the owning organization id).
type TenantKeyResolver func(ctx context.Context, tenantID string) string

// NewInstanceOrgResolver resolves a managed instance id to its organization
// through everstack.tenant_config on the platform database (the same table
// the gateway's host resolution reads). Any other id, or a failed lookup,
// maps to itself: never to another tenant.
func NewInstanceOrgResolver(db *sqlx.DB) TenantKeyResolver {
	return func(ctx context.Context, tenantID string) string {
		tenantID = strings.TrimSpace(tenantID)
		if db == nil || tenantID == "" {
			return tenantID
		}
		var orgID string
		if err := db.GetContext(ctx, &orgID,
			`SELECT organization_id::text FROM everstack.tenant_config WHERE instance_id::text = $1`,
			tenantID); err != nil || strings.TrimSpace(orgID) == "" {
			return tenantID
		}
		return strings.TrimSpace(orgID)
	}
}
