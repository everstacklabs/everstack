package enterprise

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/everstacklabs/everstack/internal/billingidentity"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// On a managed gateway a plan cap belongs to the ORGANIZATION, but rows are
// stored under whichever tenant id created them: the organization id itself,
// any of its managed instances (live or deleted), or a legacy tenant_config
// instance. Counting only the request's tenant id let an organization with
// three aliases hold three times its cap. This file resolves the set of tenant
// ids a cap is counted over.
//
// Self-hosted and CE gateways have no organization table to consult and keep
// the old behaviour exactly: the scope is the request's tenant id alone.

// OrgScope is one organization and every tenant id whose rows it owns.
type OrgScope struct {
	OrganizationID string
	Tier           string
	Aliases        []string
	// Exempt is an internal organization: no plan cap applies. Read from a
	// column only migrations and ops tooling write.
	Exempt bool
	// DeletedAliases are the organization's deleted instances: excluded from
	// counts, except for hosted sites still awaiting takedown.
	DeletedAliases []string
}

// OrgScopeResolver maps a tenant id to its organization scope. Errors mean
// "no organization scope" (unknown, contested or unavailable) and must never
// be answered with another organization's scope.
type OrgScopeResolver interface {
	ResolveOrgScope(ctx context.Context, tenantID string) (OrgScope, error)
}

// TenantColumn says how a count query's tenant column is typed, which decides
// how the alias list is bound to $1.
type TenantColumn int

const (
	// TenantText binds $1 as text[] (varchar tenant_id columns).
	TenantText TenantColumn = iota
	// TenantUUID binds $1 as uuid[] (uuid tenant_id columns). Aliases that
	// are not UUIDs cannot match such a column and are dropped.
	TenantUUID
)

// CountScope is what one cap check counts over.
type CountScope struct {
	// Key identifies the scope for serialization: two creators with the same
	// key take the same advisory lock. It is the organization on a managed
	// gateway, so two aliases of one organization serialize together.
	Key string
	// Aliases are the tenant ids whose rows count. Never empty for a
	// non-empty tenant id.
	Aliases []string
	// DeletedAliases are the organization's deleted instances. Only hosted
	// sites count them (until the takedown sweep removes their sites).
	DeletedAliases []string
}

// Arg returns the alias list bound for a query whose tenant column is typed
// as col.
func (s CountScope) Arg(col TenantColumn) interface{} {
	if col == TenantUUID {
		ids := make([]string, 0, len(s.Aliases))
		for _, alias := range s.Aliases {
			if _, err := uuid.Parse(alias); err == nil {
				ids = append(ids, alias)
			}
		}
		return pq.Array(ids)
	}
	return pq.Array(append([]string(nil), s.Aliases...))
}

// ResolveCountScope returns the scope a cap check for tenantID counts over.
// On a managed gateway with an organization resolver installed it is the
// tenant's organization; everywhere else (and whenever the organization
// cannot be resolved unambiguously) it is the tenant id alone.
func ResolveCountScope(ctx context.Context, tenantID string) CountScope {
	tenantID = strings.TrimSpace(tenantID)
	single := CountScope{Key: tenantID, Aliases: []string{tenantID}}
	if tenantID == "" || !ManagedGateway() {
		return single
	}
	resolver, ok := currentPlanResolver().(OrgScopeResolver)
	if !ok || resolver == nil {
		return single
	}
	scope, err := resolver.ResolveOrgScope(ctx, tenantID)
	if err != nil || scope.OrganizationID == "" || len(scope.Aliases) == 0 {
		// Unreachable for an enforced cap in practice: the tier comes from the
		// same cached lookup, so a tenant with no scope also has no tier and
		// resolves unlimited before any count runs.
		return single
	}
	return CountScope{Key: "org:" + scope.OrganizationID, Aliases: scope.Aliases}
}

// ErrLimitCheckUnavailable means a cap could not be checked right now (the
// organization's tenant ids could not be resolved). The create is refused
// rather than counted against a partial scope; callers map it to a
// retryable Unavailable error.
var ErrLimitCheckUnavailable = errors.New("plan limits cannot be checked right now; please try again in a moment")

// resolveCountScopeStrict is ResolveCountScope for enforcement: an
// organization lookup failure is an error, never a quiet fall back to the
// tenant's own rows (which would under-count and let the create through).
// "No organization" and "contested" are definite answers and count the
// tenant alone, as before.
func resolveCountScopeStrict(ctx context.Context, tenantID string) (CountScope, error) {
	tenantID = strings.TrimSpace(tenantID)
	single := CountScope{Key: tenantID, Aliases: []string{tenantID}}
	if tenantID == "" || !ManagedGateway() {
		return single, nil
	}
	resolver, ok := currentPlanResolver().(OrgScopeResolver)
	if !ok || resolver == nil {
		return single, nil
	}
	scope, err := resolver.ResolveOrgScope(ctx, tenantID)
	switch {
	case err == nil && scope.OrganizationID != "" && len(scope.Aliases) > 0:
		return CountScope{Key: "org:" + scope.OrganizationID, Aliases: scope.Aliases}, nil
	case err == nil, errors.Is(err, billingidentity.ErrNoOrganization), errors.Is(err, billingidentity.ErrContestedTenant):
		return single, nil
	default:
		return CountScope{}, fmt.Errorf("%w: %v", ErrLimitCheckUnavailable, err)
	}
}

// --- Trusted server-side tenant ------------------------------------------

type serverResolvedTenantKey struct{}

// WithServerResolvedTenant binds a tenant id the server itself derived from
// stored state (a channel config, a scheduled job row, a verified request that
// has since detached into a goroutine) and marks it as authoritative for
// entitlement resolution.
//
// Without the mark, a managed gateway resolves such a context through the
// self-hosted path and applies Community Edition limits to a cloud customer.
// Only server code can set it: no header, body field or metadata reaches it.
// Never call it with a tenant id taken from a request field.
func WithServerResolvedTenant(ctx context.Context, tenantID string) context.Context {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ctx
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	return context.WithValue(ctx, serverResolvedTenantKey{}, tenantID)
}

// hasEntitlementPrincipal reports whether the tenant on ctx may resolve its
// managed plan: a verified API key or session, or a tenant the server bound
// with WithServerResolvedTenant. The mark only counts while the context's
// tenant is still the one it was set for.
func hasEntitlementPrincipal(ctx context.Context) bool {
	if contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return true
	}
	marked, _ := ctx.Value(serverResolvedTenantKey{}).(string)
	return marked != "" && marked == contextkeys.GetTenantID(ctx)
}

// --- Database-backed resolver ---------------------------------------------

// resolveOrgScopeFromDB is the uncached lookup behind dbPlanTierResolver.
func resolveOrgScopeFromDB(ctx context.Context, r *dbPlanTierResolver, tenantID string) (OrgScope, error) {
	scope, err := billingidentity.ResolveLimitScope(ctx, r.db, tenantID)
	switch {
	case err == nil:
		return OrgScope{OrganizationID: scope.ID, Tier: scope.Tier, Aliases: scope.Aliases, Exempt: scope.Exempt, DeletedAliases: scope.DeletedAliases}, nil
	case errors.Is(err, billingidentity.ErrContestedTenant):
		// Two organizations claim this tenant id. Counting it for either would
		// put one customer's objects in the other's cap, so it gets neither.
		logger.WithFields("tenant", tenantID).
			Error("entitlements: tenant is claimed by more than one organization; no plan scope applies")
	case errors.Is(err, billingidentity.ErrNoOrganization):
		logger.WithFields("tenant", tenantID).
			Warn("entitlements: tenant maps to no organization; no plan scope applies")
	default:
		logger.WithFields("tenant", tenantID).WithError(err).
			Warn("entitlements: organization scope lookup failed")
	}
	return OrgScope{}, err
}

// EnforcedManagedPlan resolves tenantID's organization plan for subsystems
// that keep their own quota mechanics (hosting). A tenant with no live
// organization (or a failed lookup) gets the Starter plan on its own rows.
// ok is false off a managed gateway or while the tier is in shadow: the
// caller then applies no plan limit, exactly as ResolveEntitlements does.
func EnforcedManagedPlan(ctx context.Context, tenantID string) (ent Entitlements, scope CountScope, ok bool, err error) {
	if IsDev() || !ManagedGateway() || strings.TrimSpace(tenantID) == "" {
		return Entitlements{}, CountScope{}, false, nil
	}
	resolver, isScoped := currentPlanResolver().(OrgScopeResolver)
	if !isScoped || resolver == nil {
		return Entitlements{}, CountScope{}, false, nil
	}
	source := "managed-plan"
	org, err := resolver.ResolveOrgScope(ctx, tenantID)
	if err != nil && !errors.Is(err, billingidentity.ErrNoOrganization) && !errors.Is(err, billingidentity.ErrContestedTenant) {
		// The lookup failed with no last known good answer. Under an
		// enforced Starter tier, refuse (retryable) rather than check the
		// quota against the tenant's own sites only.
		if TierEnforced("free") {
			return Entitlements{}, CountScope{}, false, fmt.Errorf("%w: %v", ErrLimitCheckUnavailable, err)
		}
		return Entitlements{}, CountScope{}, false, nil
	}
	if err == nil && org.Exempt {
		return Entitlements{}, CountScope{}, false, nil // internal organization: no quota
	}
	if err != nil || org.Tier == "" {
		// No live organization, or the lookup failed: Starter quota on the
		// tenant's own rows, never unlimited.
		org = OrgScope{Tier: "free"}
		source = "managed-unresolved"
		scope = CountScope{Key: strings.TrimSpace(tenantID), Aliases: []string{strings.TrimSpace(tenantID)}}
	} else {
		scope = CountScope{Key: "org:" + org.OrganizationID, Aliases: org.Aliases, DeletedAliases: org.DeletedAliases}
	}
	limits, found := planLimitsForTier(org.Tier)
	if !found {
		return Entitlements{}, CountScope{}, false, nil
	}
	if !TierEnforced(org.Tier) {
		reportShadowLimits(tenantID, org.Tier, limits)
		return Entitlements{}, CountScope{}, false, nil
	}
	limits = enforcedClasses(tenantID, org.Tier, limits)
	return Entitlements{Tier: org.Tier, Source: source, Limits: limits}, scope, true, nil
}

// managedTenantExempt reports whether the request's tenant belongs to an
// internal organization exempt from plan caps. The answer comes from the
// cached organization lookup only; nothing on the request can set it.
func managedTenantExempt(ctx context.Context) bool {
	resolver, ok := currentPlanResolver().(OrgScopeResolver)
	if !ok || resolver == nil {
		return false
	}
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return false
	}
	scope, err := resolver.ResolveOrgScope(ctx, tenantID)
	return err == nil && scope.Exempt
}
