package sandbox

import "testing"

func TestInstanceBelongsToTenant(t *testing.T) {
	own := &Instance{Config: InstanceConfig{TenantID: "tenant-a"}}
	unrecorded := &Instance{}
	cases := []struct {
		name   string
		inst   *Instance
		tenant string
		want   bool
	}{
		{"own sandbox", own, "tenant-a", true},
		{"another tenant's sandbox", own, "tenant-b", false},
		{"no sandbox", nil, "tenant-a", false},
		{"job with no tenant", own, "", false},
		{"sandbox with no recorded tenant", unrecorded, "tenant-a", true},
	}
	for _, tc := range cases {
		if got := instanceBelongsToTenant(tc.inst, tc.tenant); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
