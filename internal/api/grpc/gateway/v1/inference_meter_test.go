package v1

import "testing"

// The metering decision (which attempts are billed, and how much) is the
// money-critical logic; keep it pure and tested. DB-dependent paths
// (newInferenceMeter/settle) are covered by billingmeter's live validation.
func TestInferenceMeterRecord(t *testing.T) {
	m := &inferenceMeter{markup: 1.4}

	// BYOK ("manual"), unknown (""), and the config-level hint ("ui") are never
	// billed -- only a served platform key ("config") is.
	m.record("manual", 1.0)
	m.record("", 1.0)
	m.record("ui", 1.0)
	if m.micros != 0 {
		t.Fatalf("non-platform sources must not accumulate, got %d", m.micros)
	}

	// Platform key: catalog cost x inference markup ($1 x 1.4 = $1.40).
	m.record("config", 1.0)
	if m.micros != 1_400_000 {
		t.Fatalf("platform record = %d, want 1400000", m.micros)
	}

	// Accumulates across attempts; zero/negative cost is a no-op.
	m.record("config", 0.5) // +700000
	m.record("config", 0)
	m.record("config", -1)
	if m.micros != 2_100_000 {
		t.Fatalf("accumulated = %d, want 2100000", m.micros)
	}

	// A nil meter is a safe no-op (metering not applicable).
	var nilM *inferenceMeter
	nilM.record("config", 5)
}
