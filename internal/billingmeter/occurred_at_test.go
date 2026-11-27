package billingmeter

import (
	"testing"
	"time"
)

// Inference debits record their usage time in the ledger metadata, so the
// signup credit expiry can attribute a late (replayed) debit to the time its
// usage happened. The caller's map is not modified (the outbox stores it).
func TestWithOccurredAtRecordsUsageTime(t *testing.T) {
	at := time.Date(2026, 10, 30, 11, 59, 0, 123, time.FixedZone("x", 3600))
	in := map[string]any{"model": "gpt"}
	out := withOccurredAt(in, at)
	if got := out[OccurredAtMetadataKey]; got != "2026-10-30T10:59:00.000000123Z" {
		t.Fatalf("occurred_at = %v", got)
	}
	if out["model"] != "gpt" {
		t.Fatal("existing metadata dropped")
	}
	if _, ok := in[OccurredAtMetadataKey]; ok {
		t.Fatal("caller's metadata was modified")
	}
	if got := withOccurredAt(nil, at)[OccurredAtMetadataKey]; got == nil {
		t.Fatal("nil metadata got no usage time")
	}
}
