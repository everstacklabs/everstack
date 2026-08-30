package v1

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/query"
)

func baseTrace() *query.TraceReadModel {
	start := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	return &query.TraceReadModel{
		TraceID:       "t-1",
		StartTime:     start,
		EndTime:       start.Add(20 * time.Second),
		TotalDuration: int64(20 * time.Second),
		SpanCount:     10,
		TotalTokens:   1200,
		TotalCost:     0.02,
		ServedModel:   "claude-opus-5",
		SessionID:     "sess-1",
	}
}

func TestFingerprintStableWhenNothingChanged(t *testing.T) {
	if traceFingerprint(baseTrace()) != traceFingerprint(baseTrace()) {
		t.Fatal("fingerprint is not deterministic; the live tail would re-send every tick")
	}
}

// Each of these is a way an in-flight trace grows. Missing any one of them
// means the row freezes in the client until a full page reload.
func TestFingerprintChangesAsTraceGrows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutch func(*query.TraceReadModel)
	}{
		{"another span arrives", func(tr *query.TraceReadModel) { tr.SpanCount++ }},
		{"trace extends in time", func(tr *query.TraceReadModel) { tr.EndTime = tr.EndTime.Add(time.Second) }},
		{"duration recomputed", func(tr *query.TraceReadModel) { tr.TotalDuration += int64(time.Second) }},
		{"tokens accumulate", func(tr *query.TraceReadModel) { tr.TotalTokens += 50 }},
		{"cost accumulates", func(tr *query.TraceReadModel) { tr.TotalCost += 0.001 }},
		{"an error appears", func(tr *query.TraceReadModel) { tr.ErrorCount++ }},
		{"root span finally lands", func(tr *query.TraceReadModel) { tr.RootStatus = "STATUS_CODE_OK" }},
		{"model resolves", func(tr *query.TraceReadModel) { tr.ServedModel = "claude-sonnet-5" }},
		{"session attaches", func(tr *query.TraceReadModel) { tr.SessionID = "sess-2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := baseTrace()
			after := baseTrace()
			tc.mutch(after)
			if traceFingerprint(before) == traceFingerprint(after) {
				t.Fatalf("fingerprint unchanged after %q; the client would never see the update", tc.name)
			}
		})
	}
}

// The zero-duration case the list used to show for every in-flight trace: no
// root span means maxIf(Duration, ParentSpanId=”) is 0. Once the SQL falls
// back to observed wall-clock the value moves, and the fingerprint must move
// with it so the corrected duration reaches the client.
func TestFingerprintReflectsDurationFallback(t *testing.T) {
	inFlight := baseTrace()
	inFlight.TotalDuration = 0

	corrected := baseTrace()
	corrected.TotalDuration = int64(196 * time.Second)

	if traceFingerprint(inFlight) == traceFingerprint(corrected) {
		t.Fatal("zero-duration and corrected-duration traces fingerprint identically")
	}
}
