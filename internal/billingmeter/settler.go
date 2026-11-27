package billingmeter

// Background wallet-settlement worker (EVS-83 perf work, moved here from the
// gateway handler when the meter moved to provider dispatch).
//
// Why this exists: every platform-key attempt used to pay a full Postgres
// debit transaction (BEGIN + per-org advisory lock + ledger INSERT + balance
// recompute + COMMIT) synchronously, i.e. the HTTP response could not
// complete until the wallet write returned. At concurrency, all of one org's
// debits serialize on a single advisory lock while holding pool connections,
// the signature of a throughput cliff.
//
// Design constraints, in order:
//  1. Billing correctness over latency: a settle is never dropped. When the
//     queue is saturated, or the worker is draining for shutdown, the caller
//     debits synchronously. When a debit fails for good (retries exhausted,
//     sync fallback failed, drain budget spent) the unit is parked in the
//     gateway-DB outbox and replayed with the same idempotency key.
//  2. Idempotency makes backgrounding safe: DebitUnit is idempotent on the
//     settle unit id (ON CONFLICT DO NOTHING), so retries, worker overlap and
//     outbox replay can never double-debit.
//  3. Durability across deploys: the queue is process-local, so shutdown
//     must flush it. cmd/serve calls Shutdown after http.Server.Shutdown
//     returns (no handler can enqueue any more), on a dedicated budget
//     (settleDrainMinBudget floor, EVS_GATEWAY_SETTLE_DRAIN_TIMEOUT to raise)
//     that ignores the shutdown context's deadline. Whatever cannot be
//     flushed in time is parked in the outbox; only when the outbox write
//     fails too is the unit logged at ERROR with every field, and counted in
//     billing_settle_failed_total.

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// settleQueueSize bounds in-flight background debits. At 200 concurrent
	// requests each producing one settle, this holds ~20s of buffer; beyond
	// it the caller falls back to synchronous debit (slower, never lost).
	settleQueueSize = 4096
	// settleWorkers is sized for RTT-bound debit transactions, not CPU: a
	// handful covers thousands of debits/sec while the per-org advisory
	// lock serializes same-org writes regardless of worker count.
	settleWorkers = 4
	// settleAttempts bounds in-process retries per unit before it is parked.
	settleAttempts = 3
	// settleAttemptTimeout bounds one debit transaction.
	settleAttemptTimeout = 10 * time.Second
)

// debitFunc performs one debit attempt. Production value is DebitUnit; tests
// inject fakes.
type debitFunc func(ctx context.Context, db *sqlx.DB, u SettleUnit) error

// parkFunc is the terminal fallback for a unit the worker could not debit.
// Production value writes the outbox; tests inject fakes.
type parkFunc func(u SettleUnit, lastErr error)

// settleWorker debits wallets off the request path.
type settleWorker struct {
	db    *sqlx.DB
	debit debitFunc
	park  parkFunc
	queue chan SettleUnit
	wg    sync.WaitGroup

	// mu gates enqueue against drain. Senders hold the read lock across the
	// (non-blocking) send; drain takes the write lock to flip stopped, so no
	// send can race the stop decision. The queue channel is never closed:
	// a send on a closed channel panics even inside a select, and senders
	// are concurrent request handlers.
	mu      sync.RWMutex
	stopped bool
	// done is closed by drain; workers switch from blocking receives to
	// emptying whatever is left and exiting.
	done chan struct{}
	// runCtx bounds every debit attempt. drain cancels it when the shutdown
	// budget expires, which turns in-flight and remaining units into
	// abandoned ones (parked, never silently lost).
	runCtx    context.Context
	cancelRun context.CancelFunc
	draining  atomic.Bool

	enqueued     atomic.Uint64
	syncFallback atomic.Uint64
	failed       atomic.Uint64
	// drained/abandoned/drainFailed only count units processed after drain
	// began, so the drain report reads as "what happened to the backlog".
	drained     atomic.Uint64
	abandoned   atomic.Uint64
	drainFailed atomic.Uint64
}

// settleStats is a point-in-time snapshot of worker counters.
type settleStats struct {
	Enqueued     uint64
	SyncFallback uint64
	Failed       uint64
	Queued       int
}

// settleDrainReport summarizes one drain: Drained debits committed, Failed
// exhausted their retries (parked), Abandoned never completed because the
// shutdown budget ran out (parked).
type settleDrainReport struct {
	Drained   uint64
	Failed    uint64
	Abandoned uint64
}

func newSettleWorker(db *sqlx.DB, queueSize, workers int, debit debitFunc, park parkFunc) *settleWorker {
	if debit == nil {
		debit = DebitUnit
	}
	if park == nil {
		park = func(SettleUnit, error) {}
	}
	if queueSize <= 0 {
		queueSize = settleQueueSize
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	s := &settleWorker{
		db:        db,
		debit:     debit,
		park:      park,
		queue:     make(chan SettleUnit, queueSize),
		done:      make(chan struct{}),
		runCtx:    runCtx,
		cancelRun: cancelRun,
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go s.loop()
	}
	return s
}

// enqueue hands a debit to the background workers. It returns false when the
// queue is saturated or the worker is draining; the caller must then debit
// synchronously (never drop). Safe to call concurrently with drain.
func (s *settleWorker) enqueue(u SettleUnit) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.stopped {
		s.syncFallback.Add(1)
		return false
	}
	select {
	case s.queue <- u:
		s.enqueued.Add(1)
		return true
	default:
		s.syncFallback.Add(1)
		return false
	}
}

// settleSync is the inline fallback: the debit the worker could not accept
// runs to completion before the caller returns, on a context detached from
// the request and bounded by the attempt timeout. A failure is parked.
func (s *settleWorker) settleSync(parent context.Context, u SettleUnit) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), settleAttemptTimeout)
	defer cancel()
	if err := s.debit(ctx, s.db, u); err != nil {
		s.failed.Add(1)
		logger.WithFields("org", u.OrgID, "settle_unit", u.ID, "micros", u.Micros, "error", err.Error()).
			Warn("billingmeter: settle failed (sync fallback); parking in outbox")
		s.park(u, err)
	}
}

// stop drains with no deadline. Tests use it; production goes through
// Shutdown with the shutdown context.
func (s *settleWorker) stop() {
	s.drain(context.Background())
}

// drain stops accepting new units, then flushes the backlog bounded by ctx.
// Once ctx expires, every unit still queued or in flight is abandoned and
// parked. Idempotent: a second call returns an empty report without waiting.
func (s *settleWorker) drain(ctx context.Context) settleDrainReport {
	if s == nil {
		return settleDrainReport{}
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return settleDrainReport{}
	}
	s.stopped = true
	s.draining.Store(true)
	backlog := len(s.queue)
	s.mu.Unlock()
	close(s.done)

	finished := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
		// Budget spent: cancel in-flight attempts and let the workers
		// abandon the remainder. They exit promptly because every wait
		// (attempt context, retry backoff) observes runCtx.
		s.cancelRun()
		<-finished
	}
	// Whatever the workers left behind (a worker-less instance, or a
	// budget that expired before they emptied the queue) is processed
	// here: debited while runCtx lives, parked otherwise.
	// No sender can add to the queue any more, so this loop terminates.
	for {
		select {
		case u := <-s.queue:
			s.process(u)
		default:
			s.cancelRun()
			return s.report(backlog)
		}
	}
}

// report snapshots the drain counters and logs the outcome once.
func (s *settleWorker) report(backlog int) settleDrainReport {
	report := settleDrainReport{
		Drained:   s.drained.Load(),
		Failed:    s.drainFailed.Load(),
		Abandoned: s.abandoned.Load(),
	}
	fields := []any{"backlog", backlog, "drained", report.Drained,
		"failed", report.Failed, "abandoned", report.Abandoned}
	if report.Abandoned > 0 || report.Failed > 0 {
		logger.WithFields(fields...).Warn("billingmeter: settle drain parked debits in the outbox for replay")
	} else {
		logger.WithFields(fields...).Info("billingmeter: settle drain complete")
	}
	return report
}

// snapshot returns current counters for tests and metrics.
func (s *settleWorker) snapshot() settleStats {
	if s == nil {
		return settleStats{}
	}
	return settleStats{
		Enqueued:     s.enqueued.Load(),
		SyncFallback: s.syncFallback.Load(),
		Failed:       s.failed.Load(),
		Queued:       len(s.queue),
	}
}

func (s *settleWorker) loop() {
	defer s.wg.Done()
	for {
		select {
		case u := <-s.queue:
			s.process(u)
		case <-s.done:
			// No sender can enqueue after stopped flipped, so the queue
			// length is fixed now: empty it and exit.
			for {
				select {
				case u := <-s.queue:
					s.process(u)
				default:
					return
				}
			}
		}
	}
}

// process runs one unit, routing it to the abandoned bucket when the drain
// budget is already gone (no attempt is made against a dead context).
func (s *settleWorker) process(u SettleUnit) {
	if s.runCtx.Err() != nil {
		s.abandon(u)
		return
	}
	s.debitWithRetry(u)
}

func (s *settleWorker) debitWithRetry(u SettleUnit) {
	var err error
	for attempt := 0; attempt < settleAttempts; attempt++ {
		if attempt > 0 {
			// Linear backoff between attempts; blocks only this worker, and
			// gives way to a drain deadline.
			select {
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
			case <-s.runCtx.Done():
			}
		}
		if s.runCtx.Err() != nil {
			s.abandon(u)
			return
		}
		ctx, cancel := context.WithTimeout(s.runCtx, settleAttemptTimeout)
		err = s.debit(ctx, s.db, u)
		cancel()
		if err == nil {
			if s.draining.Load() {
				s.drained.Add(1)
			}
			return
		}
		if s.runCtx.Err() != nil {
			// The attempt was cut short by the drain deadline: the
			// transaction rolled back, so this debit was not recorded.
			s.abandon(u)
			return
		}
	}
	s.failed.Add(1)
	if s.draining.Load() {
		s.drainFailed.Add(1)
	}
	logger.WithFields("org", u.OrgID, "settle_unit", u.ID, "micros", u.Micros, "error", err.Error()).
		Warn("billingmeter: settle failed after retries; parking in outbox")
	s.park(u, err)
}

// abandon parks a debit that will never be attempted again by this process.
func (s *settleWorker) abandon(u SettleUnit) {
	s.abandoned.Add(1)
	s.park(u, context.Canceled)
}

const (
	// settleDrainMinBudget is the floor for the shutdown drain. It is
	// deliberately independent of the shutdown context handed to
	// http.Server.Shutdown: that context is a shared 5s budget that the
	// sandbox proxy, agent checkpointing, OTEL and the HTTP drain spend
	// first, and on the parent-cancelled path it is already dead. Money
	// must not depend on what is left of it.
	settleDrainMinBudget = 10 * time.Second
	// settleDrainBudgetEnv raises the drain budget (a Go duration such as
	// "20s"). Values below the floor, or unparseable, use the floor. Keep
	// terminationGracePeriodSeconds - preStopSleepSeconds in the gateway
	// chart above the shared shutdown budget (5s) plus this value.
	settleDrainBudgetEnv = "EVS_GATEWAY_SETTLE_DRAIN_TIMEOUT"
)

// settleDrainBudget returns the dedicated drain budget: the configured
// duration, never below settleDrainMinBudget.
func settleDrainBudget() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(settleDrainBudgetEnv)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > settleDrainMinBudget {
			return d
		}
		logger.WithFields("env", settleDrainBudgetEnv, "value", raw, "floor", settleDrainMinBudget.String()).
			Warn("billingmeter: settle drain budget unusable or below floor; using floor")
	}
	return settleDrainMinBudget
}

// drainDetached drains on a fresh deadline of budget, ignoring any
// cancellation or deadline on parent (its values are kept). A shutdown
// context that is already done therefore still gets a full drain.
func (s *settleWorker) drainDetached(parent context.Context, budget time.Duration) settleDrainReport {
	if s == nil {
		return settleDrainReport{}
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), budget)
	defer cancel()
	return s.drain(ctx)
}
