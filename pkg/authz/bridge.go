package authz

import "context"

// SessionMembership is the caller's role on the tenant root (an instance),
// derived from the authenticated session rather than persisted tuples.
//
// On an instance the org/workspace membership graph is not present locally (it
// lives in the cloud control plane), but the session already carries the
// caller's role. The engine bridges that role in as a direct instance
// membership, which is what lets instance-local (Option B) per-resource checks
// resolve without a synced membership graph: a resource's own parent tuple links
// it to the instance, and the bridge supplies the caller's role on that instance.
type SessionMembership struct {
	UserID string // the caller
	Tenant string // the instance id (== tenant id) the caller is acting in
	Role   Role   // the caller's org-level role from the session
}

type sessionMembershipKey struct{}

// ContextWithSessionMembership attaches the caller's session-derived membership
// so a BridgeStore can inject it during a Check.
func ContextWithSessionMembership(ctx context.Context, m SessionMembership) context.Context {
	return context.WithValue(ctx, sessionMembershipKey{}, m)
}

func sessionMembershipFromContext(ctx context.Context) (SessionMembership, bool) {
	m, ok := ctx.Value(sessionMembershipKey{}).(SessionMembership)
	return m, ok
}

// instanceRelationForRole maps an org-level role to the equivalent instance
// membership relation. The instance type has no "owner" (billing/delete are
// owner-only org powers that instances do not carry), so an org owner is an
// instance admin. The schema's computed relations cascade admin -> member ->
// viewer, so injecting only the exact mapped relation is sufficient.
func instanceRelationForRole(r Role) string {
	switch r {
	case RoleOwner, RoleAdmin:
		return "admin"
	case RoleMember:
		return "member"
	case RoleViewer:
		return "viewer"
	default:
		return ""
	}
}

// BridgeStore wraps a TupleStore and, during reads, injects the caller's
// session membership (from ctx) as a direct instance-membership tuple for the
// request's tenant root. Writes and Deletes pass through unchanged. With no
// SessionMembership in ctx it is a pure pass-through, so it is always safe to
// install as the engine's store.
type BridgeStore struct{ inner TupleStore }

// NewBridgeStore wraps inner with session-membership injection.
func NewBridgeStore(inner TupleStore) *BridgeStore { return &BridgeStore{inner: inner} }

// Write implements TupleStore.
func (b *BridgeStore) Write(ctx context.Context, tuples ...Tuple) error {
	return b.inner.Write(ctx, tuples...)
}

// Delete implements TupleStore.
func (b *BridgeStore) Delete(ctx context.Context, tuples ...Tuple) error {
	return b.inner.Delete(ctx, tuples...)
}

// DeleteObject implements TupleStore.
func (b *BridgeStore) DeleteObject(ctx context.Context, object Object) error {
	return b.inner.DeleteObject(ctx, object)
}

// ListSubjects implements TupleStore, augmenting the persisted subjects with the
// caller's session membership on the tenant-root instance.
func (b *BridgeStore) ListSubjects(ctx context.Context, object Object, relation string) ([]Subject, error) {
	out, err := b.inner.ListSubjects(ctx, object, relation)
	if err != nil {
		return out, err
	}
	if m, ok := sessionMembershipFromContext(ctx); ok && m.UserID != "" &&
		object.Type == "instance" && object.ID == m.Tenant &&
		relation == instanceRelationForRole(m.Role) {
		out = append(out, User(m.UserID))
	}
	return out, nil
}

var _ TupleStore = (*BridgeStore)(nil)
