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

func TestNormalizeStoredMachineProfile(t *testing.T) {
	t.Parallel()

	sized := func(cpu float64, memoryMB, diskMB int64) SandboxConfig {
		return SandboxConfig{CPULimit: cpu, MemoryMB: memoryMB, DiskMB: diskMB}
	}
	tests := []struct {
		name        string
		config      SandboxConfig
		want        SandboxConfig
		wantChanged bool
	}{
		// Legacy shapes found in persisted agent rows.
		{name: "legacy default 1/512/1G to nano", config: sized(1, 512, 1024), want: machineConfig("nano"), wantChanged: true},
		{name: "half cpu 1G disk to nano", config: sized(0.5, 512, 1024), want: machineConfig("nano"), wantChanged: true},
		{name: "2/2048/2G to medium", config: sized(2, 2048, 2048), want: machineConfig("medium"), wantChanged: true},
		{name: "2/2048/10G to medium", config: sized(2, 2048, 10240), want: machineConfig("medium"), wantChanged: true},
		{name: "never sizes up past cpu", config: sized(1, 4096, 1024), want: machineConfig("small"), wantChanged: true},
		{name: "below every profile floors at nano", config: sized(0.25, 256, 1024), want: machineConfig("nano"), wantChanged: true},
		{name: "above every profile caps at xlarge", config: sized(16, 16384, 1024), want: machineConfig("xlarge"), wantChanged: true},
		{name: "valid profile unchanged", config: machineConfig("small"), want: machineConfig("small")},
		{name: "unset left for defaults", config: sized(0, 0, 0), want: sized(0, 0, 0)},
	}

	m := &SandboxManager{}
	m.SetManagedMachineProfilesRequired(true)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, changed := m.NormalizeStoredMachineProfile(tt.config)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if got.CPULimit != tt.want.CPULimit || got.MemoryMB != tt.want.MemoryMB || got.DiskMB != tt.want.DiskMB {
				t.Fatalf("got %v/%d/%d, want %v/%d/%d", got.CPULimit, got.MemoryMB, got.DiskMB,
					tt.want.CPULimit, tt.want.MemoryMB, tt.want.DiskMB)
			}
			if tt.wantChanged {
				if err := ValidateManagedMachineProfile(got, "enterprise"); err != nil {
					t.Fatalf("normalized size is not a managed profile: %v", err)
				}
			}
		})
	}

	t.Run("self-hosted keeps custom sizes", func(t *testing.T) {
		t.Parallel()
		custom := sized(1, 512, 1024)
		got, changed := (&SandboxManager{}).NormalizeStoredMachineProfile(custom)
		if changed || got.CPULimit != 1 || got.MemoryMB != 512 || got.DiskMB != 1024 {
			t.Fatalf("self-hosted config changed: %v/%d/%d changed=%v", got.CPULimit, got.MemoryMB, got.DiskMB, changed)
		}
	})
}

func TestFitExistingManagedMachineProfile(t *testing.T) {
	t.Parallel()

	sized := func(cpu float64, memoryMB, diskMB int64) SandboxConfig {
		return SandboxConfig{CPULimit: cpu, MemoryMB: memoryMB, DiskMB: diskMB}
	}
	tests := []struct {
		name    string
		config  SandboxConfig
		tier    string
		want    SandboxConfig
		wantErr bool
	}{
		// Legacy shapes found on stopped and archived prod sandboxes.
		{name: "legacy 1/512/1G on free clamps CPU", config: sized(1, 512, 1024), tier: "free", want: sized(0.5, 512, 1024)},
		{name: "legacy 1/1024/1G kept on basic", config: sized(1, 1024, 1024), tier: "basic", want: sized(1, 1024, 1024)},
		{name: "legacy 4/512 on free clamps CPU", config: sized(4, 512, 1024), tier: "free", want: sized(0.5, 512, 1024)},
		{name: "legacy over plan memory clamps", config: sized(1, 1024, 1024), tier: "free", want: sized(0.5, 512, 1024)},
		{name: "profile above plan clamps to plan max", config: machineConfig("small"), tier: "free", want: machineConfig("nano")},
		{name: "profile within plan kept", config: machineConfig("nano"), tier: "pro", want: machineConfig("nano")},
		{name: "legacy 50G disk refused on pro", config: sized(4, 4096, 51200), tier: "pro", wantErr: true},
		{name: "legacy 50G disk refused on enterprise", config: sized(4, 4096, 51200), tier: "enterprise", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := FitExistingManagedMachineProfile(tt.config, tt.tier)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedSandboxSize) {
					t.Fatalf("err = %v, want ErrUnsupportedSandboxSize", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.CPULimit != tt.want.CPULimit || got.MemoryMB != tt.want.MemoryMB || got.DiskMB != tt.want.DiskMB {
				t.Fatalf("fit = (%v, %d, %d), want (%v, %d, %d)", got.CPULimit, got.MemoryMB, got.DiskMB, tt.want.CPULimit, tt.want.MemoryMB, tt.want.DiskMB)
			}
		})
	}
}

// TestTrooperProvisionSizingSequence mirrors GetOrCreateTrooper: clamp to the
// plan's agent limits, normalize, then validate. Clamping works field by
// field, so a legacy 1 GB disk survives it and only normalization makes the
// result a valid profile.
func TestTrooperProvisionSizingSequence(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		tier   string
		stored SandboxConfig
		want   string
	}{
		{tier: "free", stored: SandboxConfig{CPULimit: 1, MemoryMB: 512, DiskMB: 1024}, want: "nano"},
		{tier: "free", stored: SandboxConfig{CPULimit: 2, MemoryMB: 2048, DiskMB: 2048}, want: "nano"},
		{tier: "pro", stored: SandboxConfig{CPULimit: 2, MemoryMB: 2048, DiskMB: 2048}, want: "medium"},
		{tier: "basic", stored: SandboxConfig{CPULimit: 1, MemoryMB: 512, DiskMB: 1024}, want: "nano"},
	} {
		tier, stored, want := tt.tier, tt.stored, tt.want
		t.Run(tier+"/"+want, func(t *testing.T) {
			t.Parallel()
			m := &SandboxManager{}
			m.SetManagedMachineProfilesRequired(true)
			m.SetTenantTierResolver(func(string) string { return tier })

			clamped := m.ClampToTrooperLimits(stored, "tenant")
			if err := m.ValidateSandboxMachineProfile(clamped, "tenant"); !errors.Is(err, ErrUnsupportedSandboxSize) {
				t.Fatalf("precondition: clamped legacy size should be invalid without normalization, err = %v", err)
			}
			normalized, _ := m.NormalizeStoredMachineProfile(clamped)
			if err := m.ValidateSandboxMachineProfile(normalized, "tenant"); err != nil {
				t.Fatalf("normalized size rejected: %v", err)
			}
			if got, _ := MatchManagedMachineProfile(normalized); got.ID != want {
				t.Fatalf("profile = %q, want %q", got.ID, want)
			}
		})
	}
}
