package authz

import (
	"context"
	"testing"
)

func TestBridgeResolvesInstanceResources(t *testing.T) {
	const tenant = "inst-T"
	store := NewMemStore()
	eng := NewEngine(NewBridgeStore(store), EverstackSchema().WithResourceTypes("dataset"))

	// A dataset created on the instance: ONLY its parent link is persisted. There
	// is no membership graph locally — the bridge supplies the caller's role.
	wctx := ContextWithTenant(context.Background(), tenant)
	if err := store.Write(wctx,
		ResourceParent("dataset", "ds1", tenant),
		// An explicit per-resource share: vic is granted editor on ds1 directly.
		ResourceGrant("dataset", "ds1", "vic", "editor"),
		// cleo is the creator: a manager grant lets her delete her own resource.
		ResourceGrant("dataset", "ds1", "cleo", "manager"),
	); err != nil {
		t.Fatal(err)
	}

	callerCtx := func(user string, role Role, tnt string) context.Context {
		ctx := ContextWithTenant(context.Background(), tnt)
		return ContextWithSessionMembership(ctx, SessionMembership{UserID: user, Tenant: tnt, Role: role})
	}
	check := func(ctx context.Context, user, rel string, want bool) {
		t.Helper()
		got, err := eng.Check(ctx, user, rel, Resource("dataset", "ds1"))
		if err != nil {
			t.Fatalf("Check(%s,%s): %v", user, rel, err)
		}
		if got != want {
			t.Errorf("Check(%s,%s)=%v want %v", user, rel, got, want)
		}
	}

	// Member: inherits view + edit on the instance's resources via the bridge.
	check(callerCtx("mia", RoleMember, tenant), "mia", "can_view", true)
	check(callerCtx("mia", RoleMember, tenant), "mia", "can_edit", true)

	// Viewer: read-only.
	check(callerCtx("val", RoleViewer, tenant), "val", "can_view", true)
	check(callerCtx("val", RoleViewer, tenant), "val", "can_edit", false)

	// No session membership in ctx -> nothing inherited -> deny.
	plain := ContextWithTenant(context.Background(), tenant)
	check(plain, "nobody", "can_view", false)

	// Explicit per-resource grant beats the coarse role: vic is a session viewer
	// but was granted editor on ds1 specifically.
	check(callerCtx("vic", RoleViewer, tenant), "vic", "can_edit", true)

	// Delete requires manager (admin-level), matching the coarse matrix:
	check(callerCtx("mia", RoleMember, tenant), "mia", "can_delete", false) // member cannot delete
	check(callerCtx("ada", RoleOwner, tenant), "ada", "can_delete", true)   // owner -> instance admin -> manager
	check(callerCtx("vic", RoleViewer, tenant), "vic", "can_delete", false) // editor grant != delete
	check(callerCtx("cleo", RoleViewer, tenant), "cleo", "can_delete", true) // creator's manager grant

	// Tenant isolation: a member of a DIFFERENT tenant cannot even see ds1 (its
	// parent tuple lives under "inst-T", and the store is tenant-scoped).
	check(callerCtx("mia", RoleMember, "other-tenant"), "mia", "can_view", false)
}
