package sandbox

import (
	"context"
	"errors"
	"testing"
)

func TestValidateManagedMachineProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  SandboxConfig
		tier    string
		wantErr bool
	}{
		{name: "Free Nano", config: machineConfig("nano"), tier: "free"},
		{name: "Free Small denied", config: machineConfig("small"), tier: "free", wantErr: true},
		{name: "Basic Small", config: machineConfig("small"), tier: "basic"},
		{name: "Pro Large", config: machineConfig("large"), tier: "pro"},
		{name: "Pro XL denied", config: machineConfig("xlarge"), tier: "pro", wantErr: true},
		{name: "Enterprise XL", config: machineConfig("xlarge"), tier: "enterprise"},
		{
			name: "custom tuple denied",
			config: SandboxConfig{
				CPULimit: 0.75,
				MemoryMB: 768,
				DiskMB:   20 * 1024,
			},
			tier:    "pro",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateManagedMachineProfile(tt.config, tt.tier)
			if tt.wantErr && !errors.Is(err, ErrUnsupportedSandboxSize) {
				t.Fatalf("ValidateManagedMachineProfile() error = %v, want ErrUnsupportedSandboxSize", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateManagedMachineProfile() error = %v", err)
			}
		})
	}
}

func TestManagerManagedMachinePolicyUsesTenantTier(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)
	m.SetTenantTierResolver(func(string) string { return "free" })

	if err := m.ValidateSandboxMachineProfile(machineConfig("nano"), "tenant-free"); err != nil {
		t.Fatalf("Free Nano validation error = %v", err)
	}
	if err := m.ValidateSandboxMachineProfile(machineConfig("small"), "tenant-free"); !errors.Is(err, ErrUnsupportedSandboxSize) {
		t.Fatalf("Free Small validation error = %v, want ErrUnsupportedSandboxSize", err)
	}
}

func TestManagerManagedMachinePolicyCoversPersistedInstanceConfig(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)
	m.SetTenantTierResolver(func(string) string { return "free" })

	nano := machineConfig("nano")
	if err := m.validateInstanceMachineProfile(InstanceConfig{
		TenantID: "tenant-free",
		CPULimit: nano.CPULimit,
		MemoryMB: nano.MemoryMB,
		DiskMB:   nano.DiskMB,
	}); err != nil {
		t.Fatalf("persisted Free Nano validation error = %v", err)
	}
	if err := m.validateInstanceMachineProfile(InstanceConfig{
		TenantID: "tenant-free",
		CPULimit: 1,
		MemoryMB: 1024,
		DiskMB:   20 * 1024,
	}); !errors.Is(err, ErrUnsupportedSandboxSize) {
		t.Fatalf("persisted Free Small validation error = %v, want ErrUnsupportedSandboxSize", err)
	}
}

func TestManagedPricingCannotBeDisabledByRuntimeConfig(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)
	pricing := m.resolvePricingFromRuntimeConfig(context.Background(), SandboxPricingConfig{
		Enabled:            false,
		CPUPerHourUSD:      0.0504,
		MemoryGBPerHourUSD: 0.0162,
	})
	if !pricing.Enabled {
		t.Fatal("managed sandbox pricing must remain enabled")
	}
}

func TestClampToTrooperLimitsPreservesSmallerFixedSize(t *testing.T) {
	t.Parallel()

	m := &SandboxManager{}
	m.SetTenantTierResolver(func(string) string { return "pro" })

	nano := machineConfig("nano")
	if got := m.ClampToTrooperLimits(nano, "tenant-pro"); got.CPULimit != nano.CPULimit || got.MemoryMB != nano.MemoryMB || got.DiskMB != nano.DiskMB {
		t.Fatalf("Nano was changed: got (%v, %d, %d), want (%v, %d, %d)", got.CPULimit, got.MemoryMB, got.DiskMB, nano.CPULimit, nano.MemoryMB, nano.DiskMB)
	}

	xl := machineConfig("xlarge")
	got := m.ClampToTrooperLimits(xl, "tenant-pro")
	want := machineConfig("large")
	if got.CPULimit != want.CPULimit || got.MemoryMB != want.MemoryMB || got.DiskMB != want.DiskMB {
		t.Fatalf("Pro XL clamp = (%v, %d, %d), want Large (%v, %d, %d)", got.CPULimit, got.MemoryMB, got.DiskMB, want.CPULimit, want.MemoryMB, want.DiskMB)
	}
}

func machineConfig(id string) SandboxConfig {
	for _, profile := range ManagedMachineProfiles {
		if profile.ID == id {
			return SandboxConfig{
				CPULimit: profile.CPU,
				MemoryMB: profile.MemoryMB,
				DiskMB:   profile.DiskMB,
			}
		}
	}
	return SandboxConfig{}
}
