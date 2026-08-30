package config

import (
	"os"
	"testing"

	"github.com/everstacklabs/everstack/pkg/plans"
)

func TestBrowserRuntimeCommercialContract(t *testing.T) {
	data, err := os.ReadFile("../../" + plans.DefaultPath)
	if err != nil {
		t.Fatalf("read plans config: %v", err)
	}
	cfg, err := LoadPlansConfigFromBytes(data)
	if err != nil {
		t.Fatalf("load plans config: %v", err)
	}

	pricing := cfg.BrowserRuntimePricing
	if pricing == nil {
		t.Fatal("browser runtime pricing is missing")
	}
	if pricing.BrowserHour != 0.01 {
		t.Fatalf("browser hour = %v, want 0.01", pricing.BrowserHour)
	}
	if pricing.MinimumSessionSeconds != 60 {
		t.Fatalf("minimum session = %d, want 60", pricing.MinimumSessionSeconds)
	}
	if pricing.BillingIncrementSeconds != 1 {
		t.Fatalf("billing increment = %d, want 1", pricing.BillingIncrementSeconds)
	}
	if pricing.IdlePoolBilling {
		t.Fatal("warm-pool idle time must not be customer billed")
	}

	expected := map[string]struct {
		concurrency int64
		maxSeconds  int64
	}{
		"free":       {concurrency: 2, maxSeconds: 900},
		"basic":      {concurrency: 10, maxSeconds: 3600},
		"pro":        {concurrency: 25, maxSeconds: 14400},
		"enterprise": {concurrency: -1, maxSeconds: -1},
	}
	for tier, want := range expected {
		plan, ok := cfg.Plans[tier]
		if !ok {
			t.Fatalf("plan %q is missing", tier)
		}

		limits := make(map[string]int64, len(plan.UsageLimits))
		for _, limit := range plan.UsageLimits {
			limits[limit.Type] = limit.Value
		}
		if got := limits["CONCURRENT_BROWSERS"]; got != want.concurrency {
			t.Errorf("%s browser concurrency = %d, want %d", tier, got, want.concurrency)
		}
		if got := limits["BROWSER_SESSION_MAX_SECONDS"]; got != want.maxSeconds {
			t.Errorf("%s browser max session = %d, want %d", tier, got, want.maxSeconds)
		}
	}
}
