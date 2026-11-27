package billingmeter

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/plans"
)

// Meter is the process-wide wallet meter the provider billing middleware
// settles through. It owns the billable-org cache, the background settle
// worker and the outbox replay loop. A nil *Meter is a valid no-op (metering
// not applicable: no billing DB, or not a shared gateway), so every method is
// nil-safe and the middleware can hold one unconditionally.
type Meter struct {
	billingDB *sqlx.DB
	gatewayDB *sqlx.DB
	markup    float64
	orgs      orgCache
	resolve   resolveOrgFunc
	worker    *settleWorker
	usage     *usageCounter
	// shadow runs the G1 dry-run admission reads off the request path;
	// tiers caches the slow-moving tier input of those reads.
	shadow *shadowRunner
	tiers  tierCache
	// spendCache holds each organization's spend limit state for
	// spendCacheTTL; spendNow overrides the clock in tests.
	spendCache spendCache
	spendNow   func() time.Time
	// spendV2, spendPending and spendLocal are the spend limits v2
	// evaluator's per-organization cache, P (in-flight estimates) and the
	// settled spend the rollups do not hold yet (spend_eval.go).
	spendV2      spendV2Cache
	spendPending spendPending
	spendLocal   spendLocal
	// spendCold runs v2 cold-miss reads and evaluations off the request
	// path, on its own slots so they never starve G1 admission (shadow).
	spendCold *shadowRunner
	// trial caches the model trial settings and per-organization
	// eligibility (trial.go).
	trial trialCache

	replayOnce   sync.Once
	replayCancel context.CancelFunc
	replayDone   chan struct{}
}

// Option configures a Meter at construction.
type Option func(*Meter)

// WithMarkup overrides the inference markup (default: plans.json
// credit_pricing.inference_markup, else 1.0).
func WithMarkup(markup float64) Option {
	return func(m *Meter) {
		if markup > 0 {
			m.markup = markup
		}
	}
}

// withResolver injects the org resolver (tests).
func withResolver(fn resolveOrgFunc) Option { return func(m *Meter) { m.resolve = fn } }

// withWorker replaces the default worker with one built by the test (its
// debit and park functions and sizing). The default worker is stopped first
// so no goroutines leak.
func withWorker(w *settleWorker) Option {
	return func(m *Meter) {
		if m.worker != nil {
			m.worker.stop()
		}
		m.worker = w
	}
}

// New builds a meter over the platform DB that owns billing.* (billingDB) and
// the gateway's own primary DB that owns the settle outbox (gatewayDB). Either
// may be nil: a nil billingDB makes the meter inactive (nothing is ever
// debited), a nil gatewayDB disables the outbox so a failed debit is a
// counted, logged loss instead of a parked one.
func New(billingDB, gatewayDB *sqlx.DB, opts ...Option) *Meter {
	m := &Meter{
		billingDB: billingDB,
		gatewayDB: gatewayDB,
		markup:    defaultMarkup(),
		resolve:   ResolveOrg,
		shadow:    newShadowRunner(shadowConcurrency, shadowReadTimeout),
		spendCold: newShadowRunner(spendColdConcurrency, spendV2Timeout+time.Second),
	}
	m.worker = newSettleWorker(billingDB, settleQueueSize, settleWorkers, nil, m.park)
	if gatewayDB != nil {
		m.usage = newUsageCounter(gatewayDB, usageFlushInterval)
	}
	for _, o := range opts {
		o(m)
	}
	registerMeter(m)
	return m
}

// CountUsage records one served attempt (any provenance) against org's
// display counters. Never blocks and never fails the caller.
func (m *Meter) CountUsage(org string, d UsageDelta) {
	if m == nil || m.usage == nil {
		return
	}
	m.usage.add(org, d)
}

func defaultMarkup() float64 {
	if pc, err := plans.Embedded(); err == nil && pc != nil {
		if cp := pc.GetCreditPricing(); cp != nil && cp.InferenceMarkup > 0 {
			return cp.InferenceMarkup
		}
	}
	return 1.0
}

// Active reports whether this meter can debit anything at all.
func (m *Meter) Active() bool {
	return m != nil && m.billingDB != nil
}

// Markup is the multiplier applied to catalog cost.
func (m *Meter) Markup() float64 {
	if m == nil || m.markup <= 0 {
		return 1.0
	}
	return m.markup
}

// ResolveOrg maps the request tenant in ctx to its billable organization.
// ok=false means the tenant maps to no org. Errors are returned, never
// cached, so the caller can distinguish "unbillable" from "could not tell".
func (m *Meter) ResolveOrg(ctx context.Context) (org string, ok bool, err error) {
	if !m.Active() {
		return "", false, nil
	}
	return m.orgs.resolve(ctx, m.billingDB, contextkeys.ExtractTenantID(ctx), m.resolve)
}

// Settle records one settle unit. The debit is handed to the background
// worker; when the worker cannot take it (queue saturated, or draining for
// shutdown) it runs synchronously on a context detached from the request so
// a cancelled or disconnected request still records the spend it incurred.
// Any final failure parks the unit in the outbox. Units with no org or no
// charge are dropped here, and a platform attempt without an org is counted
// as an unresolved-org event because that is real money going unbilled.
func (m *Meter) Settle(ctx context.Context, u SettleUnit) {
	if !m.Active() {
		return
	}
	if u.ID == "" || (u.Micros <= 0 && u.Attempt == nil) {
		return
	}
	charges := u.chargeable()
	if !charges {
		// Nothing to debit; keep only the attempt record.
		u.Micros = 0
	}
	if u.OrgID == "" {
		if !charges {
			// A record-only attempt with no billable org has no money
			// attached; there is nothing to reconcile.
			return
		}
		settleUnresolvedOrg.Inc()
		logger.WithFields("settle_unit", u.ID, "micros", u.Micros, "tenant", contextkeys.ExtractTenantID(ctx)).
			Error("billingmeter: platform attempt with no billable organization; not debited")
		return
	}
	if u.At.IsZero() {
		u.At = time.Now().UTC()
	}
	// The spend limit evaluator counts this unit locally until the rollups
	// hold it. In memory only; never fails or blocks.
	m.recordLocalSpend(u, charges)
	if charges {
		settleDebited.WithLabelValues(u.Modality).Inc()
	} else {
		attemptsRecordedUncharged.WithLabelValues(u.Attempt.Role, u.Attempt.Provenance).Inc()
	}
	if !m.worker.enqueue(u) {
		if !charges {
			// Saturated (or draining): a record-only unit carries no money,
			// so it is dropped and counted rather than written inline on the
			// request goroutine. Charged units keep the never-lose path.
			attemptsDroppedSaturated.Inc()
			logger.WithFields("org", u.OrgID, "settle_unit", u.ID, "role", u.Attempt.Role).
				Warn("billingmeter: settle queue saturated; uncharged attempt record dropped")
			return
		}
		m.worker.settleSync(ctx, u)
	}
}

// park is the worker's terminal fallback: write the outbox, or count and log
// the loss when even that fails.
func (m *Meter) park(u SettleUnit, lastErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), settleAttemptTimeout)
	defer cancel()
	if err := OutboxInsert(ctx, m.gatewayDB, u, lastErr); err != nil {
		settleFailed.Inc()
		fields := []any{"org", u.OrgID, "settle_unit", u.ID, "modality", u.Modality, "micros", u.Micros,
			"at", u.At.Format(time.RFC3339Nano), "debit_error", errString(lastErr), "outbox_error", err.Error()}
		for k, v := range u.Metadata {
			fields = append(fields, "meta."+k, v)
		}
		logger.WithFields(fields...).Error("billingmeter: settle unit LOST: wallet debit failed and outbox unavailable; reconcile by settle_unit")
		return
	}
	settleParked.Inc()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// StartOutboxReplay starts the replay loop for parked units. Idempotent; a
// no-op for an inactive meter or one without a gateway DB.
func (m *Meter) StartOutboxReplay(ctx context.Context) {
	if !m.Active() || m.gatewayDB == nil {
		return
	}
	m.replayOnce.Do(func() {
		rctx, cancel := context.WithCancel(ctx)
		m.replayCancel = cancel
		m.replayDone = make(chan struct{})
		go func() {
			defer close(m.replayDone)
			runOutboxReplay(rctx, m.gatewayDB, m.billingDB, m.worker.debit, outboxReplayInterval)
		}()
	})
}

// Shutdown stops the replay loop and flushes every queued wallet debit on its
// own budget (settleDrainBudget), independent of shutdownCtx: only that
// context's values are kept, never its deadline or cancellation. Call it
// after http.Server.Shutdown has returned (so no handler can enqueue any
// more; a handler that is still running debits synchronously instead). Units
// the budget does not cover are parked in the outbox. Safe to call more than
// once.
func (m *Meter) Shutdown(shutdownCtx context.Context) {
	if m == nil {
		return
	}
	if m.replayCancel != nil {
		m.replayCancel()
		<-m.replayDone
	}
	m.usage.close()
	m.worker.drainDetached(shutdownCtx, settleDrainBudget())
}

// Process-wide meter registry so cmd/serve can flush every meter at shutdown
// without threading handles through the server graph.
var (
	metersMu sync.Mutex
	meters   []*Meter
)

func registerMeter(m *Meter) {
	metersMu.Lock()
	meters = append(meters, m)
	metersMu.Unlock()
}

// ShutdownAll flushes every meter built in this process. See Meter.Shutdown.
func ShutdownAll(shutdownCtx context.Context) {
	metersMu.Lock()
	all := append([]*Meter(nil), meters...)
	metersMu.Unlock()
	for _, m := range all {
		m.Shutdown(shutdownCtx)
	}
}
