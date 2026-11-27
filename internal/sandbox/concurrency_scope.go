package sandbox

import (
	"context"
	"strings"

	"github.com/lib/pq"
)

// TenantScope is the set of tenant ids one concurrency cap counts over.
//
// On a managed gateway a plan cap belongs to the organization, and its
// sandboxes run under several tenant ids (the organization id and each of its
// instances). Counting one id let an organization with three ids run three
// times its cap. Off a managed gateway the scope is the tenant alone, which is
// exactly the old behaviour.
type TenantScope struct {
	// Key identifies the scope for the cross-replica advisory lock, so two
	// ids of one organization serialize on the same lock.
	Key string
	// Aliases are the tenant ids whose sandboxes count.
	Aliases []string
	// ShadowOnly marks an organization scope that is reported, not enforced
	// (the activity class of plan caps is in shadow). Enforcement then uses
	// the pre-existing per-tenant gate; see Enforced.
	ShadowOnly bool
}

// Enforced returns the scope a refusal is decided on: this scope, or the
// tenant alone while the organization scope is shadow only.
func (s TenantScope) Enforced(tenantID string) TenantScope {
	if s.ShadowOnly || s.Key == "" || len(s.Aliases) == 0 {
		return SingleTenantScope(tenantID)
	}
	return s
}

// SingleTenantScope is the scope of one tenant id alone.
func SingleTenantScope(tenantID string) TenantScope {
	tenantID = strings.TrimSpace(tenantID)
	return TenantScope{Key: tenantID, Aliases: []string{tenantID}}
}

// Contains reports whether a sandbox owned by tenantID counts in this scope.
// A scope with no tenant (the old empty-tenant call) counts every sandbox.
func (s TenantScope) Contains(tenantID string) bool {
	any := false
	for _, alias := range s.Aliases {
		if alias == "" {
			continue
		}
		any = true
		if strings.EqualFold(alias, strings.TrimSpace(tenantID)) {
			return true
		}
	}
	return !any
}

// LockName is the advisory lock name shared by every slot reservation in this
// scope, across replicas.
func (s TenantScope) LockName() string { return "sandbox-concurrency:" + s.Key }

// AliasArray binds the aliases as a text[] for `tenant_id = ANY($n::text[])`.
func (s TenantScope) AliasArray() interface{} {
	return pq.Array(append([]string(nil), s.Aliases...))
}

// withTenant returns the scope guaranteed to include tenantID, so a resolver
// answer can never exclude the very tenant being checked.
func (s TenantScope) withTenant(tenantID string) TenantScope {
	tenantID = strings.TrimSpace(tenantID)
	if s.Key == "" || len(s.Aliases) == 0 {
		return SingleTenantScope(tenantID)
	}
	if tenantID == "" || s.Contains(tenantID) {
		return s
	}
	s.Aliases = append(append([]string(nil), s.Aliases...), tenantID)
	return s
}

// TenantScopeResolver maps a tenant to the scope its concurrency cap counts
// over. ok=false means "no wider scope known" and falls back to the tenant
// alone.
type TenantScopeResolver func(ctx context.Context, tenantID string) (scope TenantScope, ok bool)

type scopeResolverHolder struct{ fn TenantScopeResolver }

// SetTenantScopeResolver installs the managed-gateway resolver. Nil clears
// it, which returns the manager to counting per tenant id.
func (m *SandboxManager) SetTenantScopeResolver(fn TenantScopeResolver) {
	if fn == nil {
		m.tenantScopeResolver.Store(nil)
		return
	}
	m.tenantScopeResolver.Store(&scopeResolverHolder{fn: fn})
}

// ConcurrencyScope returns the scope CONCURRENT_SANDBOXES is counted over for
// tenantID. Resolve it before taking m.mu: the resolver may query a database.
func (m *SandboxManager) ConcurrencyScope(ctx context.Context, tenantID string) TenantScope {
	if m == nil {
		return SingleTenantScope(tenantID)
	}
	holder := m.tenantScopeResolver.Load()
	if holder == nil || strings.TrimSpace(tenantID) == "" {
		return SingleTenantScope(tenantID)
	}
	scope, ok := holder.fn(ctx, tenantID)
	if !ok {
		return SingleTenantScope(tenantID)
	}
	return scope.withTenant(tenantID)
}
