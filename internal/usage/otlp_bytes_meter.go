package usage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ProcessedBytesMeter buffers post-decompression OTLP ingest bytes per tenant
// and flushes them into billing_usage_records once per interval.
//
// It records the raw request tenant_id and the byte quantity ONLY — it does not
// price. Pricing (and the tenant_id -> organization_id resolution needed to pick
// the right tier and debit the right wallet) happens on the billing side, which
// owns the organizations / managed_instances mapping. tenant_id here can be an
// org id OR a managed-instance id; the gateway DB cannot tell them apart, so it
// must not guess a rate. See metering point A in
// docs/design/usage-credits-and-data-billing.md.
type ProcessedBytesMeter struct {
	db       *sqlx.DB
	interval time.Duration
	now      func() time.Time

	mu  sync.Mutex
	buf map[string]int64
}

// NewProcessedBytesMeter builds a meter. interval <= 0 defaults to one minute.
func NewProcessedBytesMeter(db *sqlx.DB, interval time.Duration) *ProcessedBytesMeter {
	if interval <= 0 {
		interval = time.Minute
	}
	return &ProcessedBytesMeter{
		db:       db,
		interval: interval,
		now:      time.Now,
		buf:      make(map[string]int64),
	}
}

// AddIngestBytes records n decompressed bytes ingested for tenantID. Safe to
// call on a nil meter (no-op) and concurrently from many goroutines.
func (m *ProcessedBytesMeter) AddIngestBytes(tenantID string, n int) {
	if m == nil || n <= 0 || tenantID == "" {
		return
	}
	m.mu.Lock()
	m.buf[tenantID] += int64(n)
	m.mu.Unlock()
}

func (m *ProcessedBytesMeter) drain() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.buf) == 0 {
		return nil
	}
	out := m.buf
	m.buf = make(map[string]int64)
	return out
}

// requeue adds bytes back after a failed flush so a transient DB error never
// drops billable usage.
func (m *ProcessedBytesMeter) requeue(tenantID string, bytes int64) {
	m.mu.Lock()
	m.buf[tenantID] += bytes
	m.mu.Unlock()
}

// Run flushes on interval until ctx is cancelled, then flushes once more so a
// clean shutdown doesn't drop the final window.
func (m *ProcessedBytesMeter) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flush(context.Background())
			return
		case <-ticker.C:
			m.flush(ctx)
		}
	}
}

func (m *ProcessedBytesMeter) flush(ctx context.Context) {
	snapshot := m.drain()
	if snapshot == nil {
		return
	}
	// One window key per tenant per interval so a retried flush is a no-op via
	// the billing_usage_records idempotency key.
	windowStart := m.now().UTC().Truncate(m.interval)
	for tenantID, bytes := range snapshot {
		rec := m.buildRecord(tenantID, bytes, windowStart)
		if err := InsertBillingUsageRecord(ctx, m.db, rec); err != nil {
			logger.WithError(err).Warnf("otlp meter: failed to record processed bytes for tenant %s, requeueing", tenantID)
			m.requeue(tenantID, bytes)
		}
	}
}

func (m *ProcessedBytesMeter) buildRecord(tenantID string, bytes int64, windowStart time.Time) BillingUsageRecord {
	periodEnd := windowStart.Add(m.interval)
	return BillingUsageRecord{
		IdempotencyKey: fmt.Sprintf("otlp-bytes:%s:%d", tenantID, windowStart.Unix()),
		TenantID:       tenantID,
		ResourceType:   "otlp",
		SourceType:     "otlp.ingest",
		MetricType:     "otlp.processed_bytes",
		Quantity:       float64(bytes),
		Unit:           "bytes",
		// CostUSD is intentionally left 0: pricing is applied on the billing
		// side, which resolves tenant_id -> organization_id and the plan tier.
		PeriodStart: &windowStart,
		PeriodEnd:   &periodEnd,
	}
}
