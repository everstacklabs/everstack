package sandbox

import "testing"

func TestResolveTenantTier(t *testing.T) {
	m := &SandboxManager{}

	// No resolver installed → safe default.
	if got := m.resolveTenantTier("tenant-abc"); got != "free" {
		t.Fatalf("no resolver: got %q, want %q", got, "free")
	}

	// Empty tenant ID with a resolver installed → still "free" without
	// invoking the resolver (which would otherwise hit the DB pointlessly).
	called := 0
	m.SetTenantTierResolver(func(tid string) string {
		called++
		return "pro"
	})
	if got := m.resolveTenantTier(""); got != "free" {
		t.Fatalf("empty tenant: got %q, want %q", got, "free")
	}
	if called != 0 {
		t.Fatalf("resolver invoked for empty tenant: called=%d", called)
	}

	// Real lookup hits the resolver.
	if got := m.resolveTenantTier("tenant-abc"); got != "pro" {
		t.Fatalf("resolver: got %q, want %q", got, "pro")
	}
	if called != 1 {
		t.Fatalf("resolver called wrong number of times: got %d, want 1", called)
	}

	// Resolver returning "" → fall back to "free" so a missing org row
	// can never produce an unknown-tier multiplier lookup miss that
	// would otherwise silently bill at 1.0 instead of an explicit
	// "this is the free-tier price" decision.
	m.SetTenantTierResolver(func(string) string { return "" })
	if got := m.resolveTenantTier("tenant-xyz"); got != "free" {
		t.Fatalf("empty tier: got %q, want %q", got, "free")
	}

	// Clearing the resolver returns to the no-resolver default.
	m.SetTenantTierResolver(nil)
	if got := m.resolveTenantTier("tenant-abc"); got != "free" {
		t.Fatalf("after clear: got %q, want %q", got, "free")
	}
}
