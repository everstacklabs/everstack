package contextkeys

import (
	"context"
	"strings"
)

// RequestInstanceScope is the instance selected by a server-verified request
// hostname. It is routing evidence, not authentication: callers must still
// validate the user, token, and organization membership before installing it
// as the request tenant.
type RequestInstanceScope struct {
	InstanceID       string `db:"instance_id"`
	OrganizationID   string `db:"organization_id"`
	OrganizationSlug string `db:"organization_slug"`
}

type requestInstanceScopeKey struct{}

// WithRequestInstanceScope records a hostname-resolved instance without
// granting tenant access by itself.
func WithRequestInstanceScope(ctx context.Context, scope RequestInstanceScope) context.Context {
	return context.WithValue(ctx, requestInstanceScopeKey{}, scope)
}

// RequestInstanceScopeFromContext returns the verified hostname scope, when
// both the instance and its owning organization were resolved.
func RequestInstanceScopeFromContext(ctx context.Context) (RequestInstanceScope, bool) {
	scope, ok := ctx.Value(requestInstanceScopeKey{}).(RequestInstanceScope)
	if !ok || scope.InstanceID == "" || scope.OrganizationID == "" {
		return RequestInstanceScope{}, false
	}
	return scope, true
}

type hostSelectedTenantKey struct{}

// WithHostSelectedTenant records the tenant a managed gateway's Host header
// names, as resolved by tenantstatus.HostGuard from its tenant_config
// snapshot (managed host label or verified custom domain). Unlike
// RequestInstanceScope it is stamped on every request to such a host,
// authenticated or not, so it is routing evidence only: it never selects or
// authenticates a tenant and must never be echoed back. Its one consumer is
// HostScopeAdmitsTenant, which lets credential validators refuse a credential
// bound to a different tenant.
func WithHostSelectedTenant(ctx context.Context, scope RequestInstanceScope) context.Context {
	return context.WithValue(ctx, hostSelectedTenantKey{}, scope)
}

// HostSelectedTenantFromContext returns the tenant the request Host names,
// when one was resolved.
func HostSelectedTenantFromContext(ctx context.Context) (RequestInstanceScope, bool) {
	scope, ok := ctx.Value(hostSelectedTenantKey{}).(RequestInstanceScope)
	if !ok || strings.TrimSpace(scope.InstanceID) == "" {
		return RequestInstanceScope{}, false
	}
	return scope, true
}

// HostScopeAdmitsTenant reports whether a credential that resolves to
// tenantID (an API key's instance id, or its org id when it has none) may be
// used on this request's hostname. With no host scope in context
// (self-hosted, localhost, the shared apex host, loopback hops) every tenant
// is admitted, as before. With one, the credential must belong to the
// instance the Host names. A key with no instance id whose org id is the
// host instance's organization is also admitted: such legacy org-scoped keys
// already ran as their organization on any of its hosts, and refusing them
// would break working callers without narrowing what they can reach.
//
// Both the verified RequestInstanceScope and the host-selected tenant are
// checked when present.
func HostScopeAdmitsTenant(ctx context.Context, tenantID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	admits := func(scope RequestInstanceScope) bool {
		if tenantID == "" {
			return false
		}
		if strings.EqualFold(tenantID, strings.TrimSpace(scope.InstanceID)) {
			return true
		}
		org := strings.TrimSpace(scope.OrganizationID)
		return org != "" && strings.EqualFold(tenantID, org)
	}
	if scope, ok := RequestInstanceScopeFromContext(ctx); ok && !admits(scope) {
		return false
	}
	if scope, ok := HostSelectedTenantFromContext(ctx); ok && !admits(scope) {
		return false
	}
	return true
}

type tenantAuthRejectedKey struct{}

// WithTenantAuthRejected records that host-based tenant resolution already
// refused this request on a managed gateway: the Host resolved to no tenant,
// or it resolved to a tenant the presented session's user is not a member
// of. Later cookie/session re-authentication paths must not turn such a
// request into "authenticated as the user's own first tenant" (EVS-309).
func WithTenantAuthRejected(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantAuthRejectedKey{}, true)
}

// IsTenantAuthRejected reports whether WithTenantAuthRejected was applied.
func IsTenantAuthRejected(ctx context.Context) bool {
	v, _ := ctx.Value(tenantAuthRejectedKey{}).(bool)
	return v
}

type sessionUnavailableKey struct{}

// WithSessionUnavailable records that a session cookie on this request could
// not be verified because the session store was unreachable. It always
// accompanies a deny (WithTenantAuthRejected); the flag only lets the layer
// that finally refuses the request answer 503 instead of 401, so an outage
// does not read as a sign-out.
func WithSessionUnavailable(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionUnavailableKey{}, true)
}

// IsSessionUnavailable reports whether WithSessionUnavailable was applied.
func IsSessionUnavailable(ctx context.Context) bool {
	v, _ := ctx.Value(sessionUnavailableKey{}).(bool)
	return v
}
