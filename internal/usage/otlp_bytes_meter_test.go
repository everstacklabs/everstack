package usage

import (
	"testing"
	"time"
)

func TestProcessedBytesMeterAccumulatesAndDrains(t *testing.T) {
	m := NewProcessedBytesMeter(nil, time.Minute)
	m.AddIngestBytes("t1", 100)
	m.AddIngestBytes("t1", 50)
	m.AddIngestBytes("t2", 200)
	m.AddIngestBytes("", 999) // no tenant -> ignored
	m.AddIngestBytes("t3", 0) // zero bytes -> ignored

	var nilMeter *ProcessedBytesMeter
	nilMeter.AddIngestBytes("t1", 5) // nil receiver must not panic

	snap := m.drain()
	if snap["t1"] != 150 || snap["t2"] != 200 {
		t.Fatalf("drain = %v; want t1=150 t2=200", snap)
	}
	if _, ok := snap[""]; ok {
		t.Errorf("empty tenant should never be buffered")
	}
	if _, ok := snap["t3"]; ok {
		t.Errorf("zero-byte add should never be buffered")
	}
	if len(m.drain()) != 0 {
		t.Errorf("second drain should be empty after the first drained the buffer")
	}

	// Requeue puts bytes back for the next flush.
	m.requeue("t1", 42)
	if m.drain()["t1"] != 42 {
		t.Errorf("requeue did not restore bytes")
	}
}

func TestProcessedBytesMeterRecordShape(t *testing.T) {
	m := NewProcessedBytesMeter(nil, time.Minute)
	windowStart := time.Unix(1_700_000_000, 0).UTC()

	rec := m.buildRecord("tenant-x", 4096, windowStart)
	if rec.MetricType != "otlp.processed_bytes" || rec.Unit != "bytes" {
		t.Errorf("unexpected metric/unit: %q / %q", rec.MetricType, rec.Unit)
	}
	if rec.Quantity != 4096 {
		t.Errorf("quantity = %v; want 4096 (raw bytes)", rec.Quantity)
	}
	// The gateway meter never prices; billing applies the rate after resolving
	// tenant_id -> organization_id.
	if rec.CostUSD != 0 {
		t.Errorf("CostUSD = %v; meter must not price (want 0)", rec.CostUSD)
	}
	if rec.TenantID != "tenant-x" {
		t.Errorf("TenantID = %q; want tenant-x (raw request tenant)", rec.TenantID)
	}
	if rec.IdempotencyKey != "otlp-bytes:tenant-x:1700000000" {
		t.Errorf("idempotency key = %q; want otlp-bytes:tenant-x:1700000000", rec.IdempotencyKey)
	}
	if rec.PeriodStart == nil || rec.PeriodEnd == nil || !rec.PeriodEnd.After(*rec.PeriodStart) {
		t.Errorf("period bounds not set/ordered: %+v .. %+v", rec.PeriodStart, rec.PeriodEnd)
	}
}
