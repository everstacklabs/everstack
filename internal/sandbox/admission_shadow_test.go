package sandbox

import (
	"sync"
	"testing"
)

func TestComputeAdmissionShadowHook(t *testing.T) {
	m := &SandboxManager{}
	// Self-hosted: no hook installed, nothing happens.
	m.shadowAdmitCompute("tenant-a", ComputeSurfaceSandboxCreate)

	var mu sync.Mutex
	var got [][2]string
	m.SetComputeAdmissionShadow(func(tenantID, surface string) {
		mu.Lock()
		got = append(got, [2]string{tenantID, surface})
		mu.Unlock()
	})
	m.shadowAdmitCompute("tenant-a", ComputeSurfaceSandboxCreate)
	m.shadowAdmitCompute("tenant-b", ComputeSurfaceSandboxRevive)
	m.shadowAdmitCompute(" ", ComputeSurfaceSandboxCreate) // blank tenant: skipped
	if len(got) != 2 || got[0] != [2]string{"tenant-a", "sandbox_create"} || got[1] != [2]string{"tenant-b", "sandbox_revive"} {
		t.Fatalf("hook calls = %v", got)
	}

	// A panicking hook must never reach the allocation path.
	m.SetComputeAdmissionShadow(func(string, string) { panic("boom") })
	m.shadowAdmitCompute("tenant-a", ComputeSurfaceSandboxCreate)

	m.SetComputeAdmissionShadow(nil)
	m.shadowAdmitCompute("tenant-a", ComputeSurfaceSandboxCreate)
	if len(got) != 2 {
		t.Fatal("cleared hook still called")
	}
}
