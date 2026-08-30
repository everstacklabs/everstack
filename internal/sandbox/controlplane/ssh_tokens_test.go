package controlplane

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestSSHTokenScopeRequiresCompleteSandboxScope(t *testing.T) {
	t.Parallel()

	if _, err := sshTokenScope(sandbox.SandboxScope{}); err == nil {
		t.Fatal("expected empty scope to fail")
	}
	if _, err := sshTokenScope(sandbox.SandboxScope{
		OrganizationID: "org-a",
		TenantID:       "tenant-a",
		InstanceID:     "inst-a",
	}); err == nil {
		t.Fatal("expected missing sandbox_id to fail")
	}
}

func TestSSHTokenScopeMapsCanonicalScope(t *testing.T) {
	t.Parallel()

	scope, err := sshTokenScope(sandbox.SandboxScope{
		OrganizationID: " org-a ",
		TenantID:       " tenant-a ",
		InstanceID:     " inst-a ",
		SandboxID:      " sbx-a ",
	})
	if err != nil {
		t.Fatalf("expected complete scope: %v", err)
	}
	if scope.OrganizationID != "org-a" || scope.TenantID != "tenant-a" || scope.InstanceID != "inst-a" || scope.SandboxID != "sbx-a" {
		t.Fatalf("unexpected token scope: %+v", scope)
	}
}

func TestFormatSSHConnectionString(t *testing.T) {
	t.Parallel()

	if got := formatSSHConnectionString("abc123", "ssh.evs.run", 22); got != "ssh abc123@ssh.evs.run" {
		t.Fatalf("unexpected default-port command: %q", got)
	}
	if got := formatSSHConnectionString("abc123", "localhost", 2223); got != "ssh abc123@localhost -p 2223" {
		t.Fatalf("unexpected non-default-port command: %q", got)
	}
}
