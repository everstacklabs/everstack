package serve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestResolveSandboxBackendType(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *validator.SandboxFeaturesConfig
		envBackend string
		sharedMode bool
		want       string
	}{
		{
			name: "explicit config backend wins",
			cfg: &validator.SandboxFeaturesConfig{
				Backend: "Firecracker",
			},
			envBackend: "kubernetes",
			sharedMode: true,
			want:       "firecracker",
		},
		{
			name: "shared mode coerces docker config to kubernetes",
			cfg: &validator.SandboxFeaturesConfig{
				Backend: "docker",
			},
			envBackend: "",
			sharedMode: true,
			want:       "kubernetes",
		},
		{
			name:       "env backend used when config missing",
			cfg:        nil,
			envBackend: "KUBERNETES",
			sharedMode: false,
			want:       "kubernetes",
		},
		{
			name:       "shared mode coerces docker env to kubernetes",
			cfg:        nil,
			envBackend: "docker",
			sharedMode: true,
			want:       "kubernetes",
		},
		{
			name:       "shared mode defaults to kubernetes",
			cfg:        nil,
			envBackend: "",
			sharedMode: true,
			want:       "kubernetes",
		},
		{
			name:       "self-hosted defaults to docker",
			cfg:        nil,
			envBackend: "",
			sharedMode: false,
			want:       "docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("EVS_SANDBOX_BACKEND", tt.envBackend)
			got := resolveSandboxBackendType(tt.cfg, tt.sharedMode)
			if got != tt.want {
				t.Fatalf("resolveSandboxBackendType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplySandboxPricingOverridesKeepsDefaultsWhenPricingOmitted(t *testing.T) {
	pricing := sandbox.DefaultGlobalSandboxConfig().Pricing
	original := pricing

	applySandboxPricingOverrides(&pricing, validator.SandboxPricingConfig{})

	if pricing.Enabled != original.Enabled {
		t.Fatalf("enabled = %v, want %v", pricing.Enabled, original.Enabled)
	}
	if pricing.MemoryGBPerHourUSD != original.MemoryGBPerHourUSD {
		t.Fatalf("memory rate = %v, want %v", pricing.MemoryGBPerHourUSD, original.MemoryGBPerHourUSD)
	}
	if pricing.DiskGBPerHourUSD != original.DiskGBPerHourUSD {
		t.Fatalf("disk rate = %v, want %v", pricing.DiskGBPerHourUSD, original.DiskGBPerHourUSD)
	}
}

func TestApplySandboxPricingFromPlans(t *testing.T) {
	pricing := sandbox.SandboxPricingConfig{}

	if err := applySandboxPricingFromPlans(&pricing, sandboxPlansConfigPath()); err != nil {
		t.Fatalf("applySandboxPricingFromPlans() error = %v", err)
	}

	if !pricing.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if pricing.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", pricing.Currency)
	}
	if pricing.CPUPerHourUSD != 0.0504 {
		t.Fatalf("cpu rate = %v, want 0.0504", pricing.CPUPerHourUSD)
	}
	if pricing.MemoryGBPerHourUSD != 0.0162 {
		t.Fatalf("memory rate = %v, want 0.0162", pricing.MemoryGBPerHourUSD)
	}
	if pricing.DiskGBPerHourUSD != 0.000166644 {
		t.Fatalf("disk rate = %v, want 0.000166644", pricing.DiskGBPerHourUSD)
	}
	if pricing.PlatformFeePerHourUSD != 0 {
		t.Fatalf("platform rate = %v, want 0", pricing.PlatformFeePerHourUSD)
	}
	if pricing.IncludedDiskGiB != 20 {
		t.Fatalf("included disk = %v, want 20", pricing.IncludedDiskGiB)
	}
	if pricing.DiskTier2ThresholdGiB != 50 {
		t.Fatalf("tier2 threshold = %v, want 50", pricing.DiskTier2ThresholdGiB)
	}
	if pricing.DiskTier2Multiplier != 1.25 {
		t.Fatalf("tier2 multiplier = %v, want 1.25", pricing.DiskTier2Multiplier)
	}
}

func TestLoadBrowserUsagePricingFromPlans(t *testing.T) {
	pricing, err := loadBrowserUsagePricing(sandboxPlansConfigPath())
	if err != nil {
		t.Fatalf("loadBrowserUsagePricing() error = %v", err)
	}
	if pricing.CostMicrosPerHour != 10_000 {
		t.Fatalf("browser hourly micros = %d, want 10000", pricing.CostMicrosPerHour)
	}
	if pricing.BillingIncrementSeconds != 1 || pricing.MinimumSessionSeconds != 60 {
		t.Fatalf("browser billing window = %#v", pricing)
	}
}

func TestLoadBrowserTenantLimitsFromPlans(t *testing.T) {
	limits, err := loadBrowserTenantLimits(sandboxPlansConfigPath())
	if err != nil {
		t.Fatalf("loadBrowserTenantLimits() error = %v", err)
	}
	if got := limits["free"]; got.MaxConcurrent != 2 || got.MaxSession.Seconds() != 900 {
		t.Fatalf("free browser limits = %#v", got)
	}
	if got := limits["basic"]; got.MaxConcurrent != 10 || got.MaxSession.Seconds() != 3600 {
		t.Fatalf("basic browser limits = %#v", got)
	}
	if got := limits["pro"]; got.MaxConcurrent != 25 || got.MaxSession.Seconds() != 14_400 {
		t.Fatalf("pro browser limits = %#v", got)
	}
	if got := limits["enterprise"]; got.MaxConcurrent != -1 || got.MaxSession != -1 {
		t.Fatalf("enterprise browser limits = %#v", got)
	}
}

func TestApplySandboxPricingFromPlansRejectsZeroComputeRates(t *testing.T) {
	plansPath := filepath.Join(t.TempDir(), "plans.json")
	if err := os.WriteFile(plansPath, []byte(`{
		"plans": {},
		"sandbox_compute_pricing": {
			"currency": "USD",
			"cpu_per_vcpu_hour": 0,
			"memory_per_gib_hour": 0.0162
		}
	}`), 0o600); err != nil {
		t.Fatalf("write plans fixture: %v", err)
	}

	pricing := sandbox.DefaultGlobalSandboxConfig().Pricing
	if err := applySandboxPricingFromPlans(&pricing, plansPath); err == nil {
		t.Fatal("applySandboxPricingFromPlans() error = nil, want zero-rate rejection")
	}
}

func TestApplySandboxPricingOverridesAppliesExplicitPricing(t *testing.T) {
	pricing := sandbox.DefaultGlobalSandboxConfig().Pricing

	applySandboxPricingOverrides(&pricing, validator.SandboxPricingConfig{
		Enabled:               true,
		Currency:              "EUR",
		MemoryGBPerHourUSD:    0.05,
		DiskGBPerHourUSD:      0.002,
		PlatformFeePerHourUSD: 0.01,
		TierMultipliers:       map[string]float64{"pro": 0.9},
	})

	if !pricing.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if pricing.Currency != "EUR" {
		t.Fatalf("currency = %q, want EUR", pricing.Currency)
	}
	if pricing.MemoryGBPerHourUSD != 0.05 {
		t.Fatalf("memory rate = %v, want 0.05", pricing.MemoryGBPerHourUSD)
	}
	if pricing.DiskGBPerHourUSD != 0.002 {
		t.Fatalf("disk rate = %v, want 0.002", pricing.DiskGBPerHourUSD)
	}
	if pricing.PlatformFeePerHourUSD != 0.01 {
		t.Fatalf("platform rate = %v, want 0.01", pricing.PlatformFeePerHourUSD)
	}
	if pricing.TierMultipliers["pro"] != 0.9 {
		t.Fatalf("pro multiplier = %v, want 0.9", pricing.TierMultipliers["pro"])
	}
}

func TestApplySandboxPricingOverridesAllowsExplicitDisable(t *testing.T) {
	pricing := sandbox.DefaultGlobalSandboxConfig().Pricing

	applySandboxPricingOverrides(&pricing, validator.SandboxPricingConfig{
		Enabled:  false,
		Currency: "USD",
	})

	if pricing.Enabled {
		t.Fatal("enabled = true, want false")
	}
	if pricing.MemoryGBPerHourUSD == 0 {
		t.Fatal("memory rate was zeroed when only disabling pricing")
	}
}
