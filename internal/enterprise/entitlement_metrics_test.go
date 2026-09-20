package enterprise

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

// The EVS-6 shadow sink must observe without touching enforcement: counters
// move, decisions don't. Global registry state accumulates across tests in
// this package, so assert deltas, not absolutes. client_model is used
// directly to avoid pulling test-only transitive deps.
func counterValue(t *testing.T, c interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, g interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return m.GetGauge().GetValue()
}

func TestObserveEntitlementResolutionCountsSourceAndTier(t *testing.T) {
	before := counterValue(t, entitlementResolutions.WithLabelValues("managed-shadow", "free"))

	observeEntitlementResolution(Entitlements{Tier: "free", Source: "managed-shadow"})
	observeEntitlementResolution(Entitlements{Tier: "free", Source: "managed-shadow"})

	if got := counterValue(t, entitlementResolutions.WithLabelValues("managed-shadow", "free")); got-before != 2 {
		t.Fatalf("managed-shadow/free delta = %v, want 2", got-before)
	}
}

func TestObserveEntitlementResolutionNormalizesEmptyLabels(t *testing.T) {
	before := counterValue(t, entitlementResolutions.WithLabelValues("unknown", "unknown"))

	observeEntitlementResolution(Entitlements{})

	if got := counterValue(t, entitlementResolutions.WithLabelValues("unknown", "unknown")); got-before != 1 {
		t.Fatalf("unknown/unknown delta = %v, want 1", got-before)
	}
}

func TestShadowReportFeedsLimitedTenantsGauge(t *testing.T) {
	ResetShadowReportingForTest()
	before := gaugeValue(t, entitlementShadowLimitedTenants.WithLabelValues("basic", string(UsageTypeAgents)))

	reportShadowLimits("tenant-gauge-1", "basic", map[UsageType]int64{UsageTypeAgents: 5})
	reportShadowLimits("tenant-gauge-1", "basic", map[UsageType]int64{UsageTypeAgents: 5}) // deduped
	reportShadowLimits("tenant-gauge-2", "basic", map[UsageType]int64{UsageTypeAgents: 5})

	if got := gaugeValue(t, entitlementShadowLimitedTenants.WithLabelValues("basic", string(UsageTypeAgents))); got-before != 2 {
		t.Fatalf("shadow limited-tenants delta = %v, want 2 (distinct tenants, repeat deduped)", got-before)
	}
}
