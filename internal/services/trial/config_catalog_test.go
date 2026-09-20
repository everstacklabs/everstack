package trial

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/pkg/plans"
)

func TestConfigFromCatalogMatchesTheFreeTier(t *testing.T) {
	pc, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	cfg, err := ConfigFromCatalog(pc, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("ConfigFromCatalog() error = %v", err)
	}

	ent, err := pc.ResolveEntitlements(plans.SafeTier)
	if err != nil {
		t.Fatalf("ResolveEntitlements(free) error = %v", err)
	}

	for _, c := range []struct {
		limit string
		got   int64
	}{
		{"REQUESTS", cfg.DailyLimit},
		{"RPM", cfg.RPMLimit},
		{"TOKENS", cfg.TokenLimit},
	} {
		want, ok := ent.Limit(c.limit)
		if !ok {
			t.Fatalf("free tier does not declare %s", c.limit)
		}
		if c.got != want {
			t.Errorf("%s = %d, catalog says %d", c.limit, c.got, want)
		}
	}
}

// The constants agree with the catalog today. This asserts the NEW path
// reproduces them, so switching a caller from DefaultConfig to
// ConfigFromCatalog is provably behaviour-preserving right now -- which is what
// makes it safe to adopt before the catalog and the constants ever diverge.
func TestConfigFromCatalogReproducesTodaysConstants(t *testing.T) {
	pc, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	cfg, err := ConfigFromCatalog(pc, DefaultTrialDuration)
	if err != nil {
		t.Fatalf("ConfigFromCatalog() error = %v", err)
	}
	def := DefaultConfig()

	if cfg.DailyLimit != def.DailyLimit || cfg.RPMLimit != def.RPMLimit || cfg.TokenLimit != def.TokenLimit {
		t.Fatalf("catalog config %+v differs from constants %+v; if the catalog changed deliberately this test should be updated, not the catalog", cfg, def)
	}
}

// An unresolvable catalog must not widen limits. Failing open here would hand a
// trial user unlimited requests, which is the exact failure the resolver exists
// to prevent.
func TestConfigFromCatalogFailsClosed(t *testing.T) {
	cfg, err := ConfigFromCatalog(nil, DefaultTrialDuration)

	if err == nil {
		t.Fatal("expected an error for a nil catalog")
	}
	if cfg.DailyLimit <= 0 || cfg.RPMLimit <= 0 || cfg.TokenLimit <= 0 {
		t.Fatalf("nil catalog produced unbounded config %+v", cfg)
	}
	def := DefaultConfig()
	if cfg.DailyLimit != def.DailyLimit {
		t.Fatalf("fallback DailyLimit = %d, want the historical constant %d", cfg.DailyLimit, def.DailyLimit)
	}
}

// Documents the gap rather than hiding it: the catalog declares far more than
// the trial manager can enforce. If someone later adds gates, this test tells
// them how many are still unenforced.
func TestTrialEnforcesOnlyThreeOfTheCatalogsLimits(t *testing.T) {
	pc, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	ent, err := pc.ResolveEntitlements(plans.SafeTier)
	if err != nil {
		t.Fatalf("ResolveEntitlements(free) error = %v", err)
	}

	enforced := map[string]bool{"REQUESTS": true, "RPM": true, "TOKENS": true}

	var unenforced []string
	for name := range ent.UsageLimits {
		if !enforced[name] {
			unenforced = append(unenforced, name)
		}
	}

	t.Logf("catalog declares %d limits; trial enforces %d; %d have no gate on the execution path",
		len(ent.UsageLimits), len(enforced), len(unenforced))

	if len(unenforced) == 0 {
		t.Fatal("every catalog limit is now enforced -- update this test and close EVS-126")
	}
}
