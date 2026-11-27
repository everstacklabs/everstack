package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// With a managed scope resolver, one organization's sandboxes under its own
// id and under each instance fill one cap together; another organization's
// sandboxes never count, and the scope is resolved per checked tenant.
func TestConcurrentSandboxCapCountsTheWholeOrganization(t *testing.T) {
	t.Parallel()

	manager := &SandboxManager{instancesBySandbox: make(map[string]*Instance)}
	manager.SetTenantTierResolver(func(string) string { return "free" })
	orgScope := TenantScope{Key: "org:a", Aliases: []string{"org-a", "inst-a1", "inst-a2"}}
	manager.SetTenantScopeResolver(func(_ context.Context, tenantID string) (TenantScope, bool) {
		switch tenantID {
		case "org-a", "inst-a1", "inst-a2":
			return orgScope, true
		}
		return TenantScope{}, false
	})

	// A runs the free cap split over three ids.
	owners := []string{"org-a", "inst-a1", "inst-a2"}
	for i := 0; i < FreeConcurrentSandboxLimit; i++ {
		id := fmt.Sprintf("sbx_a_%d", i)
		manager.instancesBySandbox[id] = &Instance{ID: id, Status: StatusRunning,
			Config: InstanceConfig{TenantID: owners[i%len(owners)]}}
	}
	// B runs well past A's cap.
	for i := 0; i < FreeConcurrentSandboxLimit+5; i++ {
		id := fmt.Sprintf("sbx_b_%d", i)
		manager.instancesBySandbox[id] = &Instance{ID: id, Status: StatusRunning,
			Config: InstanceConfig{TenantID: "org-b"}}
	}

	for _, tenant := range owners {
		err := manager.RequireConcurrentSandboxSlot(context.Background(), tenant, "")
		if !errors.Is(err, ErrConcurrentSandboxLimit) {
			t.Fatalf("A via %s at cap: err = %v, want ErrConcurrentSandboxLimit", tenant, err)
		}
	}
	used, err := manager.ConcurrentSandboxCount(context.Background(), "inst-a1", "")
	if err != nil || used != FreeConcurrentSandboxLimit {
		t.Fatalf("A count via instance = %d, %v; want %d (B's sandboxes must not count)", used, err, FreeConcurrentSandboxLimit)
	}

	// B resolves no wider scope and counts itself only.
	if used, _ := manager.ConcurrentSandboxCount(context.Background(), "org-b", ""); used != FreeConcurrentSandboxLimit+5 {
		t.Fatalf("B count = %d, want %d", used, FreeConcurrentSandboxLimit+5)
	}

	delete(manager.instancesBySandbox, "sbx_a_0")
	if err := manager.RequireConcurrentSandboxSlot(context.Background(), "inst-a2", ""); err != nil {
		t.Fatalf("A below cap must start one more: %v", err)
	}
}

func TestConcurrencyScopeAlwaysIncludesTheCheckedTenant(t *testing.T) {
	t.Parallel()

	manager := &SandboxManager{}
	if got := manager.ConcurrencyScope(context.Background(), "t1"); got.Key != "t1" || !got.Contains("t1") || got.Contains("t2") {
		t.Fatalf("no resolver: scope = %+v, want t1 alone", got)
	}
	manager.SetTenantScopeResolver(func(context.Context, string) (TenantScope, bool) {
		return TenantScope{Key: "org:x", Aliases: []string{"other"}}, true
	})
	if got := manager.ConcurrencyScope(context.Background(), "t1"); !got.Contains("t1") || got.Key != "org:x" {
		t.Fatalf("resolver omitting the tenant: scope = %+v, want org:x including t1", got)
	}
	if !(TenantScope{}).Contains("anything") {
		t.Fatal("an empty scope counts every sandbox, as the empty-tenant call always did")
	}
}

// Org-wide counting follows the activity enforcement switch. In shadow it
// only reports: the pre-existing per-tenant hard gate is what refuses, so an
// instance under its own cap still starts while its organization is over.
func TestShadowOrgScopeEnforcesPerTenantOnly(t *testing.T) {
	t.Parallel()

	manager := &SandboxManager{instancesBySandbox: make(map[string]*Instance)}
	manager.SetTenantTierResolver(func(string) string { return "free" })
	manager.SetTenantScopeResolver(func(context.Context, string) (TenantScope, bool) {
		return TenantScope{Key: "org:a", Aliases: []string{"org-a", "inst-a1"}, ShadowOnly: true}, true
	})
	// The organization is at the cap, all of it under org-a.
	for i := 0; i < FreeConcurrentSandboxLimit; i++ {
		id := fmt.Sprintf("sbx_a_%d", i)
		manager.instancesBySandbox[id] = &Instance{ID: id, Status: StatusRunning, Config: InstanceConfig{TenantID: "org-a"}}
	}

	if err := manager.RequireConcurrentSandboxSlot(context.Background(), "inst-a1", ""); err != nil {
		t.Fatalf("shadow: inst-a1 is under its own cap and must start: %v", err)
	}
	if err := manager.RequireConcurrentSandboxSlot(context.Background(), "org-a", ""); !errors.Is(err, ErrConcurrentSandboxLimit) {
		t.Fatalf("shadow: org-a at its own per-tenant cap: err = %v, want the pre-existing refusal", err)
	}
	if got := manager.ConcurrencyScope(context.Background(), "inst-a1").Enforced("inst-a1"); got.Key != "inst-a1" {
		t.Fatalf("shadow enforcement scope = %+v, want the tenant alone", got)
	}
}
