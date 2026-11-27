package contextkeys

import (
	"context"
	"testing"
)

const (
	hostInstance = "22222222-0000-0000-0000-0000000000b2"
	hostOrg      = "33333333-0000-0000-0000-0000000000b3"
)

func hostSelected() context.Context {
	return WithHostSelectedTenant(context.Background(), RequestInstanceScope{InstanceID: hostInstance, OrganizationID: hostOrg})
}

func TestHostScopeAdmitsTenant(t *testing.T) {
	verified := WithRequestInstanceScope(context.Background(), RequestInstanceScope{InstanceID: hostInstance, OrganizationID: hostOrg})
	cases := []struct {
		name   string
		ctx    context.Context
		tenant string
		want   bool
	}{
		{"no host scope admits any tenant (self-hosted, localhost, loopback)", context.Background(), "some-other-tenant", true},
		{"no host scope admits an empty tenant as before", context.Background(), "", true},
		{"key of the host's instance", hostSelected(), hostInstance, true},
		{"instance ids compare case-insensitively", hostSelected(), "22222222-0000-0000-0000-0000000000B2", true},
		{"legacy org-scoped key of the host's organization", hostSelected(), hostOrg, true},
		{"key of another instance", hostSelected(), "44444444-0000-0000-0000-0000000000c4", false},
		{"key with no tenant on a host-scoped request", hostSelected(), "", false},
		{"verified request scope is honoured too", verified, "44444444-0000-0000-0000-0000000000c4", false},
		{"verified request scope admits its own instance", verified, hostInstance, true},
	}
	for _, tc := range cases {
		if got := HostScopeAdmitsTenant(tc.ctx, tc.tenant); got != tc.want {
			t.Errorf("%s: HostScopeAdmitsTenant(%q) = %t, want %t", tc.name, tc.tenant, got, tc.want)
		}
	}
}

// The host-selected tenant is routing evidence, not a verified scope: it must
// never surface through RequestInstanceScopeFromContext, which several
// handlers read as "the instance this caller was authorized for".
func TestHostSelectedTenantIsNotARequestInstanceScope(t *testing.T) {
	ctx := hostSelected()
	if _, ok := RequestInstanceScopeFromContext(ctx); ok {
		t.Fatal("host-selected tenant leaked into RequestInstanceScopeFromContext")
	}
	got, ok := HostSelectedTenantFromContext(ctx)
	if !ok || got.InstanceID != hostInstance || got.OrganizationID != hostOrg {
		t.Fatalf("HostSelectedTenantFromContext = %+v, %t", got, ok)
	}
	if _, ok := HostSelectedTenantFromContext(WithHostSelectedTenant(context.Background(), RequestInstanceScope{})); ok {
		t.Fatal("an empty instance id must not count as a host scope")
	}
}
