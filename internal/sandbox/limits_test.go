package sandbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestResolveConcurrentSandboxLimitUsesCustomerPlanQuota(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tier string
		want int
	}{
		{tier: "free", want: 10},
		{tier: "basic", want: 50},
		{tier: "pro", want: 50},
		{tier: "enterprise", want: -1},
		{tier: "unknown", want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			t.Parallel()
			if got := ResolveConcurrentSandboxLimit(tt.tier); got != tt.want {
				t.Fatalf("ResolveConcurrentSandboxLimit(%q) = %d, want %d", tt.tier, got, tt.want)
			}
		})
	}
}

func TestConcurrentSandboxLimitCountsAllocatedComputeOnly(t *testing.T) {
	t.Parallel()

	manager := &SandboxManager{
		instancesBySandbox: make(map[string]*Instance),
	}
	manager.SetTenantTierResolver(func(string) string { return "free" })

	for i := 0; i < FreeConcurrentSandboxLimit; i++ {
		id := fmt.Sprintf("sbx_running_%d", i)
		manager.instancesBySandbox[id] = &Instance{
			ID:     id,
			Status: StatusRunning,
			Config: InstanceConfig{TenantID: "tenant-a"},
		}
	}
	manager.instancesBySandbox["sbx_sleeping"] = &Instance{
		ID:             "sbx_sleeping",
		Status:         StatusStopped,
		LifecycleState: "sleeping",
		Config:         InstanceConfig{TenantID: "tenant-a"},
	}
	manager.instancesBySandbox["sbx_other_tenant"] = &Instance{
		ID:     "sbx_other_tenant",
		Status: StatusRunning,
		Config: InstanceConfig{TenantID: "tenant-b"},
	}

	err := manager.RequireConcurrentSandboxSlot(
		context.Background(),
		"tenant-a",
		"",
	)
	if !errors.Is(err, ErrConcurrentSandboxLimit) {
		t.Fatalf("RequireConcurrentSandboxSlot() error = %v, want ErrConcurrentSandboxLimit", err)
	}

	delete(manager.instancesBySandbox, "sbx_running_0")
	if err := manager.RequireConcurrentSandboxSlot(context.Background(), "tenant-a", ""); err != nil {
		t.Fatalf("sleeping and other-tenant sandboxes must not consume this instance's slot: %v", err)
	}
}
