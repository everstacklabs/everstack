package authzresource

import (
	"context"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/authz"
)

func TestOnResourceCreatedWritesTenantScopedParent(t *testing.T) {
	store := authz.NewMemStore()
	SetStore(store)
	t.Cleanup(func() { SetStore(nil) })

	ctx := contextkeys.WithTenantID(context.Background(), "tenantX")
	OnResourceCreated(ctx, "dataset", "ds9", "creator-1")

	read := authz.ContextWithTenant(context.Background(), "tenantX")
	subs, err := store.ListSubjects(read, authz.Resource("dataset", "ds9"), "parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Object != authz.Instance("tenantX") {
		t.Fatalf("expected parent instance:tenantX, got %+v", subs)
	}

	// The creator gets a manager grant so they can delete their own resource.
	mgrs, _ := store.ListSubjects(read, authz.Resource("dataset", "ds9"), "manager")
	if len(mgrs) != 1 || mgrs[0] != authz.User("creator-1") {
		t.Fatalf("expected creator manager grant, got %+v", mgrs)
	}

	// Tenant scoping: another tenant must not see the parent tuple.
	other := authz.ContextWithTenant(context.Background(), "tenantY")
	if got, _ := store.ListSubjects(other, authz.Resource("dataset", "ds9"), "parent"); len(got) != 0 {
		t.Fatalf("another tenant must not see the parent tuple, got %+v", got)
	}

	// Delete removes ALL of the resource's tuples (parent + grants).
	OnResourceDeleted(ctx, "dataset", "ds9")
	if got, _ := store.ListSubjects(read, authz.Resource("dataset", "ds9"), "parent"); len(got) != 0 {
		t.Fatalf("parent tuple should be gone after delete, got %+v", got)
	}
	if got, _ := store.ListSubjects(read, authz.Resource("dataset", "ds9"), "manager"); len(got) != 0 {
		t.Fatalf("manager grant should be gone after delete, got %+v", got)
	}
}

func TestRecordersAreNoopWithoutStore(t *testing.T) {
	SetStore(nil)
	// Must not panic when no store is wired (authz disabled).
	OnResourceCreated(contextkeys.WithTenantID(context.Background(), "t"), "dataset", "x", "u")
	OnResourceDeleted(contextkeys.WithTenantID(context.Background(), "t"), "dataset", "x")
}
