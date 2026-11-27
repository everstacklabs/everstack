package billingmeter

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Usage display counters. Requests and tokens served through the shared
// gateway had never reached billing for any organization: the license
// monitor's usage syncer is process-wide and does not run on the shared
// gateway, and the license service forwards usage only for activated
// instances, which managed instances never are.
//
// Every provider attempt on a managed gateway, platform AND BYOK, adds to an
// (organization, UTC hour) row in the gateway DB. Every replica writes the
// same rows with atomic upsert increments, so replicas never double count.
// A single elected reporter (cmd/serve) sums the hours inside each
// organization's current billing period and sends that period-cumulative
// total to billing, which stores it replace-on-write per (org, source,
// period) in billing.usage_totals.
//
// This path is a display, not a charge: nothing here can fail or block a
// request, and wallet debits never depend on it. "Requests" counts provider
// attempts: a fallback or a tool-loop iteration is a request of its own.

const (
	usageFlushInterval = 2 * time.Second
	usageFlushTimeout  = 5 * time.Second
	usageBucket        = time.Hour
	// usageRetryHorizon bounds how long a failed display count is retried.
	usageRetryHorizon = 48 * time.Hour
)

// UsageDelta is one organization's pending increment.
type UsageDelta struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
}

func (d UsageDelta) zero() bool {
	return d.Requests == 0 && d.InputTokens == 0 && d.OutputTokens == 0
}

// UsageTotals is a sum of counters over a window.
type UsageTotals struct {
	Requests     int64 `db:"requests"`
	InputTokens  int64 `db:"input_tokens"`
	OutputTokens int64 `db:"output_tokens"`
}

// TotalTokens is input plus output.
func (t UsageTotals) TotalTokens() int64 { return t.InputTokens + t.OutputTokens }

type usageKey struct {
	org    string
	bucket time.Time
}

// usageCounter accumulates per-(org, hour) deltas in memory and flushes them
// to the gateway DB on a short interval, so the request path pays a mutex
// and a map write, never a database round trip. The in-memory window (at
// most one flush interval) is lost if the process dies: acceptable for a
// display counter, and the reason nothing billable lives here.
type usageCounter struct {
	db      *sqlx.DB
	now     func() time.Time
	mu      sync.Mutex
	pending map[usageKey]*UsageDelta

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func newUsageCounter(db *sqlx.DB, interval time.Duration) *usageCounter {
	return newUsageCounterWithClock(db, interval, time.Now)
}

func newUsageCounterWithClock(db *sqlx.DB, interval time.Duration, now func() time.Time) *usageCounter {
	c := &usageCounter{
		db:      db,
		now:     now,
		pending: make(map[usageKey]*UsageDelta),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	if interval <= 0 {
		interval = usageFlushInterval
	}
	go c.loop(interval)
	return c
}

// add records one attempt for org in the current hour. Never blocks on the
// database.
func (c *usageCounter) add(org string, d UsageDelta) {
	org = strings.TrimSpace(org)
	if c == nil || org == "" || d.zero() {
		return
	}
	c.addAt(usageKey{org: org, bucket: c.now().UTC().Truncate(usageBucket)}, d)
}

func (c *usageCounter) addAt(k usageKey, d UsageDelta) {
	c.mu.Lock()
	p := c.pending[k]
	if p == nil {
		p = &UsageDelta{}
		c.pending[k] = p
	}
	p.Requests += d.Requests
	p.InputTokens += d.InputTokens
	p.OutputTokens += d.OutputTokens
	c.mu.Unlock()
}

// take swaps out the pending map so the flush runs outside the lock.
func (c *usageCounter) take() map[usageKey]*UsageDelta {
	c.mu.Lock()
	out := c.pending
	c.pending = make(map[usageKey]*UsageDelta)
	c.mu.Unlock()
	return out
}

// flush writes every pending delta with an atomic increment. A failed row is
// merged back into pending so a transient error drops nothing; only a
// process exit loses it.
func (c *usageCounter) flush(ctx context.Context) {
	if c == nil {
		return
	}
	oldest := c.now().UTC().Add(-usageRetryHorizon)
	var failed, dropped int
	var lastErr error
	for k, d := range c.take() {
		if err := UsageIncrement(ctx, c.db, k.org, k.bucket, *d); err != nil {
			usageFlushFailed.Inc()
			lastErr = err
			if k.bucket.Before(oldest) {
				// A display count that has failed for this long is
				// dropped rather than retried forever against the DB the
				// request path shares.
				dropped++
				continue
			}
			failed++
			c.addAt(k, *d)
		}
	}
	if lastErr != nil {
		// One line per failed flush, not one per key: under a persistent
		// failure this runs every flush interval.
		logger.WithFields("requeued", failed, "dropped", dropped, "error", lastErr.Error()).
			Warn("billingmeter: usage counter flush failed; will retry")
	}
}

func (c *usageCounter) loop(interval time.Duration) {
	defer close(c.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), usageFlushTimeout)
			c.flush(ctx)
			cancel()
		case <-c.stop:
			ctx, cancel := context.WithTimeout(context.Background(), usageFlushTimeout)
			c.flush(ctx)
			cancel()
			return
		}
	}
}

// close flushes once more and stops the loop. Idempotent.
func (c *usageCounter) close() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stop)
		<-c.done
	})
}

// UsageIncrement atomically adds d to org's counters for the hour starting
// at bucket. Safe for any number of replicas: the increment happens in the
// database.
func UsageIncrement(ctx context.Context, db sqlx.ExecerContext, org string, bucket time.Time, d UsageDelta) error {
	org = strings.TrimSpace(org)
	if db == nil || org == "" || d.zero() {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO everstack.inference_usage_counters (organization_id, bucket_start, requests, input_tokens, output_tokens, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (organization_id, bucket_start) DO UPDATE SET
			requests = everstack.inference_usage_counters.requests + EXCLUDED.requests,
			input_tokens = everstack.inference_usage_counters.input_tokens + EXCLUDED.input_tokens,
			output_tokens = everstack.inference_usage_counters.output_tokens + EXCLUDED.output_tokens,
			updated_at = NOW()`,
		org, bucket.UTC().Truncate(usageBucket), d.Requests, d.InputTokens, d.OutputTokens)
	return err
}

// UsageOrgsSince lists the organizations with counters in any hour at or
// after since.
func UsageOrgsSince(ctx context.Context, db sqlx.QueryerContext, since time.Time) ([]string, error) {
	var orgs []string
	if err := sqlx.SelectContext(ctx, db, &orgs, `
		SELECT DISTINCT organization_id
		FROM everstack.inference_usage_counters
		WHERE bucket_start >= $1
		ORDER BY organization_id`, since.UTC()); err != nil {
		return nil, err
	}
	return orgs, nil
}

// UsageTotalsBetween sums org's counters over the hours starting in
// [start, end). A subscription period that does not start on the hour
// attributes the straddling hour by its start: a display tolerance of at
// most one hour at the period boundary.
func UsageTotalsBetween(ctx context.Context, db sqlx.QueryerContext, org string, start, end time.Time) (UsageTotals, error) {
	var t UsageTotals
	err := sqlx.GetContext(ctx, db, &t, `
		SELECT COALESCE(SUM(requests), 0)::BIGINT AS requests,
		       COALESCE(SUM(input_tokens), 0)::BIGINT AS input_tokens,
		       COALESCE(SUM(output_tokens), 0)::BIGINT AS output_tokens
		FROM everstack.inference_usage_counters
		WHERE organization_id = $1 AND bucket_start >= $2 AND bucket_start < $3`,
		org, start.UTC(), end.UTC())
	return t, err
}
