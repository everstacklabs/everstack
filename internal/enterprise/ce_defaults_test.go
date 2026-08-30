package enterprise

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCEDefaultsMatchPlansJSON pins the hand-mirrored CE defaults to the
// canonical free tier in pkg/plans/plans.json. If this test
// fails, plans.json and ce_defaults.go have drifted: fix the mirror (or, once
// Phase 1 of docs/design/editions-and-billing.md lands, delete the mirror).
func TestCEDefaultsMatchPlansJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "plans", "plans.json"))
	if err != nil {
		t.Fatalf("read plans.json: %v", err)
	}

	var doc struct {
		Plans map[string]struct {
			UsageLimits []struct {
				Type  string `json:"type"`
				Value int64  `json:"value"`
			} `json:"usage_limits"`
			Features []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"features"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse plans.json: %v", err)
	}

	free, ok := doc.Plans["free"]
	if !ok {
		t.Fatal("plans.json has no free tier")
	}

	seen := make(map[UsageType]bool, len(free.UsageLimits))
	for _, ul := range free.UsageLimits {
		ut := UsageType(ul.Type)
		seen[ut] = true
		got, ok := CEUsageLimits[ut]
		if !ok {
			t.Errorf("plans.json free tier defines %s but CEUsageLimits does not", ul.Type)
			continue
		}
		// A declared divergence must hold its declared value; everything
		// else must match the free plan exactly. Diverging without declaring
		// it (or declaring one value and shipping another) fails here.
		if want, diverges := ceDivergesFromFree[ut]; diverges {
			if got != want {
				t.Errorf("CEUsageLimits[%s] = %d, ceDivergesFromFree declares %d", ul.Type, got, want)
			}
			if want == ul.Value {
				t.Errorf("ceDivergesFromFree lists %s but it matches the free plan (%d); drop the entry", ul.Type, ul.Value)
			}
			continue
		}
		if got != ul.Value {
			t.Errorf("CEUsageLimits[%s] = %d, plans.json free tier says %d (declare it in ceDivergesFromFree if intended)", ul.Type, got, ul.Value)
		}
	}
	for ut := range CEUsageLimits {
		if !seen[ut] {
			t.Errorf("CEUsageLimits defines %s but plans.json free tier does not", ut)
		}
	}
	for ut := range ceDivergesFromFree {
		if !seen[ut] {
			t.Errorf("ceDivergesFromFree declares %s but plans.json free tier does not define it", ut)
		}
	}

	for _, f := range free.Features {
		enabled, ok := ceFeatures[f.Name]
		if !ok {
			t.Errorf("plans.json free tier defines feature %q but ceFeatures does not", f.Name)
			continue
		}
		if enabled != f.Enabled {
			t.Errorf("ceFeatures[%q] = %v, plans.json free tier says %v", f.Name, enabled, f.Enabled)
		}
	}

	// Legacy CE constants must stay consistent with the map so the two
	// enforcement paths (CheckCELimit callsites passing constants, and the
	// map-based fallbacks) cannot diverge.
	if int64(CEMaxAgents) != CEUsageLimits[UsageTypeAgents] {
		t.Errorf("CEMaxAgents (%d) != CEUsageLimits[AGENTS] (%d)", CEMaxAgents, CEUsageLimits[UsageTypeAgents])
	}
	if int64(CEMaxPersistentAgents) != CEUsageLimits[UsageTypePersistentAgents] {
		t.Errorf("CEMaxPersistentAgents (%d) != CEUsageLimits[PERSISTENT_AGENTS] (%d)", CEMaxPersistentAgents, CEUsageLimits[UsageTypePersistentAgents])
	}
	if int64(CEMaxConcurrentAgents) != CEUsageLimits[UsageTypeConcurrentRunning] {
		t.Errorf("CEMaxConcurrentAgents (%d) != CEUsageLimits[CONCURRENT_RUNNING] (%d)", CEMaxConcurrentAgents, CEUsageLimits[UsageTypeConcurrentRunning])
	}
	if int64(CEMaxChannelBindings) != CEUsageLimits[UsageTypeChannelBindings] {
		t.Errorf("CEMaxChannelBindings (%d) != CEUsageLimits[CHANNEL_BINDINGS] (%d)", CEMaxChannelBindings, CEUsageLimits[UsageTypeChannelBindings])
	}
	if int64(CEMaxChannels) != CEUsageLimits[UsageTypeChannels] {
		t.Errorf("CEMaxChannels (%d) != CEUsageLimits[CHANNELS] (%d)", CEMaxChannels, CEUsageLimits[UsageTypeChannels])
	}
	if int64(CESandboxMemoryMB) != CEUsageLimits[UsageTypeSandboxMemoryMB] {
		t.Errorf("CESandboxMemoryMB (%d) != CEUsageLimits[SANDBOX_MEMORY_MB] (%d)", CESandboxMemoryMB, CEUsageLimits[UsageTypeSandboxMemoryMB])
	}
}
