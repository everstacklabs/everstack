package sandbox

import (
	"errors"
	"testing"
)

func TestRequireSandboxBilling(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	if err := m.RequireSandboxBilling("tenant-free"); err != nil {
		t.Fatalf("unconfigured self-hosted manager should remain usable: %v", err)
	}

	m.SetSandboxBillingResolver(func(string) bool { return false })
	if err := m.RequireSandboxBilling("tenant-free"); !errors.Is(err, ErrSandboxBillingRequired) {
		t.Fatalf("RequireSandboxBilling() error = %v, want ErrSandboxBillingRequired", err)
	}

	m.SetSandboxBillingResolver(func(tenantID string) bool { return tenantID == "tenant-paid" })
	if err := m.RequireSandboxBilling("tenant-paid"); err != nil {
		t.Fatalf("paid tenant rejected: %v", err)
	}
}
