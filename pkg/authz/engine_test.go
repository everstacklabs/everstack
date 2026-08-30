package authz

import (
	"context"
	"testing"
)

// buildWorld wires a small but representative graph:
//
//	org:acme            alice=owner, bob=member, carol=viewer
//	workspace:prod      parent=org:acme; dave=admin (workspace-only)
//	instance:inst1      parent=workspace:prod
//	dataset:ds1         parent=instance:inst1; erin=viewer (per-resource share)
func buildWorld(t *testing.T) *Engine {
	t.Helper()
	store := NewMemStore()
	e := NewEngine(store, EverstackSchema().WithResourceTypes("dataset"))
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Write(ctx,
		OrgMembership("acme", "alice", RoleOwner),
		OrgMembership("acme", "bob", RoleMember),
		OrgMembership("acme", "carol", RoleViewer),
		WorkspaceParent("prod", "acme"),
		WorkspaceMembership("prod", "dave", RoleAdmin),
		InstanceParent("inst1", "prod"),
		ResourceParent("dataset", "ds1", "inst1"),
		ResourceGrant("dataset", "ds1", "erin", "viewer"),
	))
	return e
}

func mustCheck(t *testing.T, e *Engine, user, rel string, obj Object) bool {
	t.Helper()
	ok, err := e.Check(context.Background(), user, rel, obj)
	if err != nil {
		t.Fatalf("Check(%s, %s, %s): %v", user, rel, obj, err)
	}
	return ok
}

func TestCheckEmptyObjectDenies(t *testing.T) {
	e := buildWorld(t)
	// A check against an empty/unscoped object must fail closed even for a real
	// user and a real relation, regardless of any "*" public tuples.
	if mustCheck(t, e, "alice", "owner", Object{Type: "organization", ID: ""}) {
		t.Error("empty object id must not authorize")
	}
	if mustCheck(t, e, "alice", "owner", Object{Type: "", ID: "acme"}) {
		t.Error("empty object type must not authorize")
	}
	if mustCheck(t, e, "", "owner", Org("acme")) {
		t.Error("empty user must not authorize")
	}
}

func TestTenantIsolation(t *testing.T) {
	store := NewMemStore()
	e := NewEngine(store, EverstackSchema())
	ctxA := ContextWithTenant(context.Background(), "tenantA")
	ctxB := ContextWithTenant(context.Background(), "tenantB")

	if err := store.Write(ctxA, OrgMembership("acme", "alice", RoleOwner)); err != nil {
		t.Fatal(err)
	}
	// Under tenant A, alice is owner of org:acme.
	if ok, err := e.Check(ctxA, "alice", "owner", Org("acme")); err != nil || !ok {
		t.Fatalf("alice should be owner under tenant A (ok=%v err=%v)", ok, err)
	}
	// The SAME object id under a different tenant must see none of A's tuples,
	// even though object ids are not themselves tenant-qualified.
	if ok, _ := e.Check(ctxB, "alice", "owner", Org("acme")); ok {
		t.Fatal("tenant B must not see tenant A's tuples")
	}
}

func TestOrgRoleHierarchy(t *testing.T) {
	e := buildWorld(t)
	org := Org("acme")

	// Owner subsumes admin/member/viewer and all permissions.
	for _, rel := range []string{"owner", "admin", "member", "viewer", "can_manage_billing", "can_manage_members", "can_delete"} {
		if !mustCheck(t, e, "alice", rel, org) {
			t.Errorf("alice (owner) should have %s on org", rel)
		}
	}
	// Member is member+viewer but not admin/owner.
	if !mustCheck(t, e, "bob", "member", org) || !mustCheck(t, e, "bob", "viewer", org) {
		t.Error("bob (member) should be member and viewer")
	}
	if mustCheck(t, e, "bob", "admin", org) || mustCheck(t, e, "bob", "can_manage_members", org) {
		t.Error("bob (member) must NOT be admin / manage members")
	}
	// Viewer is read-only.
	if !mustCheck(t, e, "carol", "can_view", org) {
		t.Error("carol (viewer) should be able to view org")
	}
	if mustCheck(t, e, "carol", "can_edit", org) {
		t.Error("carol (viewer) must NOT be able to edit org")
	}
}

func TestWorkspaceInheritsFromOrg(t *testing.T) {
	e := buildWorld(t)
	ws := Workspace("prod")

	// Org owner is workspace admin via parent inheritance.
	if !mustCheck(t, e, "alice", "admin", ws) {
		t.Error("org owner alice should be workspace admin via inheritance")
	}
	// Org member is workspace member (can edit), not workspace admin.
	if !mustCheck(t, e, "bob", "can_edit", ws) {
		t.Error("org member bob should be able to edit workspace")
	}
	if mustCheck(t, e, "bob", "can_manage_members", ws) {
		t.Error("org member bob must NOT manage workspace members")
	}
	// Workspace-only admin dave has workspace admin but no org access.
	if !mustCheck(t, e, "dave", "can_manage_members", ws) {
		t.Error("workspace admin dave should manage workspace members")
	}
	if mustCheck(t, e, "dave", "viewer", Org("acme")) {
		t.Error("workspace-only dave must NOT have org access")
	}
}

func TestResourceInheritanceAndDirectGrant(t *testing.T) {
	e := buildWorld(t)
	ds := Resource("dataset", "ds1")

	// Org member bob can edit the dataset via org->workspace->instance->resource.
	if !mustCheck(t, e, "bob", "can_edit", ds) {
		t.Error("org member bob should edit dataset via inheritance")
	}
	// Org viewer carol can view but not edit.
	if !mustCheck(t, e, "carol", "can_view", ds) {
		t.Error("org viewer carol should view dataset")
	}
	if mustCheck(t, e, "carol", "can_edit", ds) {
		t.Error("org viewer carol must NOT edit dataset")
	}
	// erin has only a direct viewer grant: can view, cannot edit, no org access.
	if !mustCheck(t, e, "erin", "can_view", ds) {
		t.Error("erin (direct viewer) should view dataset")
	}
	if mustCheck(t, e, "erin", "can_edit", ds) {
		t.Error("erin (direct viewer) must NOT edit dataset")
	}
	if mustCheck(t, e, "erin", "viewer", Org("acme")) {
		t.Error("erin must NOT have org access from a resource-only grant")
	}
}

func TestNonMemberDenied(t *testing.T) {
	e := buildWorld(t)
	for _, obj := range []Object{Org("acme"), Workspace("prod"), Resource("dataset", "ds1")} {
		if mustCheck(t, e, "mallory", "can_view", obj) {
			t.Errorf("non-member mallory must NOT view %s", obj)
		}
	}
	if mustCheck(t, e, "", "can_view", Org("acme")) {
		t.Error("empty user must be denied")
	}
}

func TestCheckPermission(t *testing.T) {
	e := buildWorld(t)
	ds := Resource("dataset", "ds1")
	ok, err := e.CheckPermission(context.Background(), "bob", PermResourceEdit, ds)
	if err != nil || !ok {
		t.Errorf("bob should have PermResourceEdit on dataset (ok=%v err=%v)", ok, err)
	}
	ok, err = e.CheckPermission(context.Background(), "carol", PermResourceEdit, ds)
	if err != nil || ok {
		t.Errorf("carol must NOT have PermResourceEdit on dataset (ok=%v err=%v)", ok, err)
	}
}

func TestTupleRoundTrip(t *testing.T) {
	in := OrgMembership("acme", "alice", RoleOwner)
	parsed, err := ParseTuple(in.String())
	if err != nil {
		t.Fatalf("ParseTuple(%q): %v", in.String(), err)
	}
	if parsed.String() != in.String() {
		t.Errorf("round trip mismatch: %q != %q", parsed.String(), in.String())
	}
	us := WorkspaceParent("prod", "acme")
	parsedUS, err := ParseTuple(us.String())
	if err != nil || parsedUS.String() != us.String() {
		t.Errorf("userset round trip failed: %v / %q", err, parsedUS.String())
	}
}

func TestModelCoarseMatrix(t *testing.T) {
	if !Can(RoleOwner, PermOrgManageBilling) {
		t.Error("owner should manage billing")
	}
	if Can(RoleAdmin, PermOrgManageBilling) {
		t.Error("admin must NOT manage billing")
	}
	if Can(RoleViewer, PermResourceEdit) {
		t.Error("viewer must NOT edit resources")
	}
	if !Can(RoleAdmin, PermStorageManage) {
		t.Error("admin should manage tenant storage")
	}
	if Can(RoleMember, PermStorageManage) {
		t.Error("member must NOT manage storage connections")
	}
	if !Can(RoleMember, PermStorageWrite) {
		t.Error("member should write storage objects")
	}
	if !Can(RoleViewer, PermStorageRead) || Can(RoleViewer, PermStorageWrite) {
		t.Error("viewer storage permissions should be read-only")
	}
	if !RoleOwner.AtLeast(RoleAdmin) || RoleViewer.AtLeast(RoleAdmin) {
		t.Error("role ranking incorrect")
	}
}

func TestStoragePermissionsResolveAgainstTenantContainers(t *testing.T) {
	e := buildWorld(t)
	tests := []struct {
		name       string
		user       string
		permission Permission
		object     Object
		want       bool
	}{
		{name: "owner manages organization storage", user: "alice", permission: PermStorageManage, object: Org("acme"), want: true},
		{name: "member cannot manage organization storage", user: "bob", permission: PermStorageManage, object: Org("acme"), want: false},
		{name: "member writes instance storage", user: "bob", permission: PermStorageWrite, object: Instance("inst1"), want: true},
		{name: "viewer reads instance storage", user: "carol", permission: PermStorageRead, object: Instance("inst1"), want: true},
		{name: "viewer cannot write instance storage", user: "carol", permission: PermStorageWrite, object: Instance("inst1"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.CheckPermission(context.Background(), tt.user, tt.permission, tt.object)
			if err != nil {
				t.Fatalf("CheckPermission() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CheckPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}
