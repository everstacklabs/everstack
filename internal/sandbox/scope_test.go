package sandbox

import "testing"

func TestTenantInstanceScopeSandboxTenantID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope TenantInstanceScope
		want  string
	}{
		{name: "instance wins", scope: TenantInstanceScope{TenantID: "tenant-a", InstanceID: "inst-a"}, want: "inst-a"},
		{name: "tenant fallback", scope: TenantInstanceScope{TenantID: "tenant-a"}, want: "tenant-a"},
		{name: "trims fields", scope: TenantInstanceScope{TenantID: " tenant-a ", InstanceID: " inst-a "}, want: "inst-a"},
		{name: "empty", scope: TenantInstanceScope{}, want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.scope.SandboxTenantID(); got != tt.want {
				t.Fatalf("SandboxTenantID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSandboxScopeComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope SandboxScope
		want  bool
	}{
		{name: "complete with instance", scope: SandboxScope{InstanceID: "inst-a", SandboxID: "sbx-a"}, want: true},
		{name: "complete with tenant fallback", scope: SandboxScope{TenantID: "tenant-a", SandboxID: "sbx-a"}, want: true},
		{name: "missing sandbox", scope: SandboxScope{InstanceID: "inst-a"}, want: false},
		{name: "missing owner", scope: SandboxScope{SandboxID: "sbx-a"}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.scope.Complete(); got != tt.want {
				t.Fatalf("Complete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScopeMatchesInstance(t *testing.T) {
	t.Parallel()

	instWithInstance := &Instance{ID: "sbx-a", InstanceID: "inst-a", Config: InstanceConfig{TenantID: "legacy-owner"}}
	legacyInst := &Instance{ID: "sbx-b", Config: InstanceConfig{TenantID: "tenant-b"}}

	if !((TenantInstanceScope{TenantID: "tenant-a", InstanceID: "inst-a"}).MatchesInstance(instWithInstance)) {
		t.Fatal("expected scope to match instance_id-backed instance")
	}
	if (TenantInstanceScope{TenantID: "tenant-a", InstanceID: "wrong"}).MatchesInstance(instWithInstance) {
		t.Fatal("expected wrong instance_id to fail")
	}
	if !(TenantInstanceScope{TenantID: "tenant-b"}).MatchesInstance(legacyInst) {
		t.Fatal("expected tenant fallback to match legacy instance")
	}
	if !(SandboxScope{TenantID: "tenant-b", SandboxID: "sbx-b"}).MatchesInstance(legacyInst) {
		t.Fatal("expected sandbox scope to match legacy instance")
	}
	if (SandboxScope{TenantID: "tenant-b", SandboxID: "sbx-a"}).MatchesInstance(legacyInst) {
		t.Fatal("expected wrong sandbox id to fail")
	}
}
