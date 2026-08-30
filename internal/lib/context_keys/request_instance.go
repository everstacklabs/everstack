package contextkeys

import "context"

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
