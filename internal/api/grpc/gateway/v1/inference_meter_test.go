package v1

import (
	"context"
	"sync"
	"testing"
)

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

func TestInferenceMeterRecordShadow(t *testing.T) {
	m := &inferenceMeter{markup: 1.4}

	// Mirrors billing scope: only platform keys observed.
	m.recordShadow(shadowFallbackServed, "manual", 1.0)
	m.recordShadow(shadowFallbackServed, "", 1.0)
	m.recordShadow("", "config", 1.0)
	m.recordShadow(shadowFallbackServed, "config", -1)
	if len(m.shadow) != 0 {
		t.Fatalf("non-platform/empty/negative shadow must not accumulate, got %#v", m.shadow)
	}

	// Platform key accumulates per reason ($1 x 1.4 = 1400000 micros).
	m.recordShadow(shadowFallbackServed, "config", 1.0)
	m.recordShadow(shadowParallelLoser, "config", 0.5) // +700000
	m.recordShadow(shadowRetryFailed, "config", 0)     // count-only
	if m.shadow[shadowFallbackServed].micros != 1_400_000 {
		t.Fatalf("fallback shadow = %d, want 1400000", m.shadow[shadowFallbackServed].micros)
	}
	if m.shadow[shadowParallelLoser].micros != 700_000 {
		t.Fatalf("loser shadow = %d, want 700000", m.shadow[shadowParallelLoser].micros)
	}
	if m.shadow[shadowRetryFailed].count != 1 || m.shadow[shadowRetryFailed].micros != 0 {
		t.Fatalf("retry shadow = %#v, want count 1 micros 0", m.shadow[shadowRetryFailed])
	}

	// Shadow never touches billed micros.
	if m.micros != 0 {
		t.Fatalf("shadow must not affect billed micros, got %d", m.micros)
	}

	// A nil meter is a safe no-op.
	var nilM *inferenceMeter
	nilM.recordShadow(shadowFallbackServed, "config", 5)
}

func TestInferenceMeterSettleShadowOnly(t *testing.T) {
	// No DB: micros==0 skips the debit, shadow path is log-only.
	m := &inferenceMeter{org: "org_1", reqID: "req_1", markup: 1.0}
	m.recordShadow(shadowToolLoop, "config", 0.25)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client went away before anything settled
	m.settle(ctx, nil)

	if m.micros != 0 {
		t.Fatalf("settle must not bill shadow, got %d", m.micros)
	}
}

func TestInferenceMeterShadowConcurrent(t *testing.T) {
	m := &inferenceMeter{markup: 1.0}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.recordShadow(shadowParallelLoser, "config", 0.1)
			m.record("config", 0.1)
		}()
	}
	wg.Wait()
	// 8 x $0.10 x 1.0 = 800000 micros each side, no race (run with -race).
	if m.micros != 800_000 {
		t.Fatalf("billed = %d, want 800000", m.micros)
	}
	if m.shadow[shadowParallelLoser].count != 8 || m.shadow[shadowParallelLoser].micros != 800_000 {
		t.Fatalf("shadow = %#v, want count 8 micros 800000", m.shadow[shadowParallelLoser])
	}
}
