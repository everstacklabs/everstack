package license_monitor

import "testing"

// TestLoadPlansConfigFallsBackToEmbedded pins the air-gapped plan resolution:
// with no license service URL, the monitor must load the canonical plans
// embedded in the binary, not the 3-type hard-coded rate subset. Without this
// an offline-licensed (EVS_LICENSE_FILE) instance resolves every
// resource-count limit (AGENTS, DATASET_ITEMS, ...) to unlimited because the
// paid-tier fallback only carries RPM/TOKENS/REQUESTS.
func TestLoadPlansConfigFallsBackToEmbedded(t *testing.T) {
	cfg := loadPlansConfig(Config{}) // no LicenseServiceURL: air-gapped
	if cfg == nil {
		t.Fatal("loadPlansConfig returned nil; embedded canonical plans must be the offline fallback")
	}

	limits := cfg.GetPlanLimits("pro")
	if limits == nil {
		t.Fatal("embedded plans have no pro tier")
	}
	agents, ok := limits.GetUsageLimit("AGENTS")
	if !ok {
		t.Fatal("embedded pro tier has no AGENTS limit; resource counts would fail open")
	}
	if agents != 30 {
		t.Fatalf("embedded pro AGENTS = %d, want 30 (plans.json)", agents)
	}
	if items, ok := limits.GetUsageLimit("DATASET_ITEMS"); !ok || items != 500000 {
		t.Fatalf("embedded pro DATASET_ITEMS = %d (present=%v), want 500000", items, ok)
	}

	// The full matrix must be present, not a rate-limit subset.
	if len(limits.UsageLimits) < 10 {
		t.Fatalf("embedded pro tier carries only %d usage types; expected the full matrix", len(limits.UsageLimits))
	}
}
