package billingmeter

// Tests for the background wallet-settlement worker and the meter org
// cache (EVS-83 perf work). The core guarantees under test:
//  1. settle() never blocks on the debit (returns while a slow debit runs).
//  2. Saturation never drops a settle (enqueue reports false; caller syncs).
//  3. Retries are bounded and idempotent-safe; persistent failure is counted.
//  4. Org resolution hits Postgres at most once per tenant per TTL.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

type fakeDebit struct {
	calls   atomic.Int64
	failFor atomic.Int64
	got     chan SettleUnit
	block   chan struct{}
}

func (f *fakeDebit) fn(ctx context.Context, db *sqlx.DB, u SettleUnit) error {
	n := f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.got != nil {
		select {
		case f.got <- u:
		default:
		}
	}
	if n <= f.failFor.Load() {
		return errors.New("fake debit failure")
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSettleWorkerRunsJobInBackground(t *testing.T) {
	fake := &fakeDebit{got: make(chan SettleUnit, 1)}
	w := newSettleWorker(nil, 16, 2, fake.fn, nil)
	defer w.stop()

	if !w.enqueue(SettleUnit{OrgID: "org1", ID: "req1", Modality: "chat", Micros: 1500}) {
		t.Fatal("enqueue should succeed on empty worker")
	}
	waitFor(t, "job execution", func() bool { return fake.calls.Load() == 1 })
	select {
	case got := <-fake.got:
		if got.OrgID != "org1" || got.ID != "req1" || got.Micros != 1500 {
			t.Fatalf("job fields mangled: %+v", got)
		}
	default:
		t.Fatal("job payload not delivered")
	}
	if s := w.snapshot(); s.Enqueued != 1 || s.Failed != 0 {
		t.Fatalf("bad snapshot: %+v", s)
	}
}

func TestSettleWorkerSaturationReportsFalse(t *testing.T) {
	fake := &fakeDebit{}
	// No workers draining + capacity 1: second enqueue must report false so
	// the caller debits synchronously instead of dropping the settle.
	w := newSettleWorker(nil, 1, 0, fake.fn, nil)
	defer w.stop()

	if !w.enqueue(SettleUnit{OrgID: "o", ID: "r1"}) {
		t.Fatal("first enqueue should succeed")
	}
	if w.enqueue(SettleUnit{OrgID: "o", ID: "r2"}) {
		t.Fatal("saturated enqueue must return false (caller falls back to sync)")
	}
	if s := w.snapshot(); s.SyncFallback != 1 {
		t.Fatalf("expected 1 sync fallback, got %+v", s)
	}
}

func TestSettleWorkerRetriesThenSucceeds(t *testing.T) {
	fake := &fakeDebit{}
	fake.failFor.Store(2)
	w := newSettleWorker(nil, 16, 1, fake.fn, nil)
	defer w.stop()

	if !w.enqueue(SettleUnit{OrgID: "o", ID: "r"}) {
		t.Fatal("enqueue should succeed")
	}
	waitFor(t, "3 attempts", func() bool { return fake.calls.Load() == 3 })
	if s := w.snapshot(); s.Failed != 0 {
		t.Fatalf("eventual success must not count as failed: %+v", s)
	}
}

func TestSettleWorkerPersistentFailureCounted(t *testing.T) {
	fake := &fakeDebit{}
	fake.failFor.Store(1 << 30) // always fails
	w := newSettleWorker(nil, 16, 1, fake.fn, nil)
	defer w.stop()

	if !w.enqueue(SettleUnit{OrgID: "o", ID: "r"}) {
		t.Fatal("enqueue should succeed")
	}
	waitFor(t, "bounded attempts", func() bool { return fake.calls.Load() == settleAttempts })
	waitFor(t, "failure counted", func() bool { return w.snapshot().Failed == 1 })
}

func TestSettleWorkerNilSafe(t *testing.T) {
	var w *settleWorker
	if w.enqueue(SettleUnit{}) {
		t.Fatal("nil worker enqueue must return false (caller syncs)")
	}
	if s := w.snapshot(); s != (settleStats{}) {
		t.Fatalf("nil worker snapshot must be zero, got %+v", s)
	}
	w.stop() // must not panic
}

func TestSettleReturnsWithoutWaitingForDebit(t *testing.T) {
	// A blocked debit must not delay settle()'s return: the response path
	// proceeds while the wallet write runs behind it.
	release := make(chan struct{})
	slow := &fakeDebit{block: release}
	w := newSettleWorker(nil, 16, 1, slow.fn, nil)
	defer w.stop()
	// Registered after w.stop so LIFO order releases the stuck debit
	// first: teardown completes in milliseconds, not after attempt timeouts.
	defer close(release)

	// Drive the worker directly (same contract settle uses): enqueue must
	// return immediately even though no debit can complete yet.
	start := time.Now()
	if !w.enqueue(SettleUnit{OrgID: "o", ID: "slow", Micros: 100}) {
		t.Fatal("enqueue should succeed")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("enqueue blocked for %v with a stuck debit behind it", elapsed)
	}
}

func TestMeterOrgCache(t *testing.T) {
	var c orgCache

	if _, _, hit := c.lookup("t1"); hit {
		t.Fatal("empty cache must miss")
	}
	c.store("t1", "org1", true)
	org, ok, hit := c.lookup("t1")
	if !hit || !ok || org != "org1" {
		t.Fatalf("positive entry not returned: %q %v %v", org, ok, hit)
	}
	c.store("t2", "", false)
	if _, ok, hit := c.lookup("t2"); !hit || ok {
		t.Fatal("negative entry must hit as not-ok")
	}

	// Backdate to force expiry (same-package access to internals).
	c.mu.Lock()
	e := c.byTenant["t1"]
	e.expires = time.Now().Add(-time.Second)
	c.byTenant["t1"] = e
	c.mu.Unlock()
	if _, _, hit := c.lookup("t1"); hit {
		t.Fatal("expired entry must miss")
	}

	var nilCache *orgCache
	if _, _, hit := nilCache.lookup("t"); hit {
		t.Fatal("nil cache must miss")
	}
	nilCache.store("t", "o", true) // must not panic
}

// --- Durability: drain at shutdown, and the stopped/full sync fallbacks ---

func TestSettleWorkerDrainFlushesQueue(t *testing.T) {
	// Every queued debit must be committed before drain returns when the
	// budget allows: a deploy loses nothing that a handler already handed
	// to the worker.
	fake := &fakeDebit{}
	w := newSettleWorker(nil, 128, 2, fake.fn, nil)
	const n = 64
	for i := 0; i < n; i++ {
		if !w.enqueue(SettleUnit{OrgID: "o", ID: "r", Micros: 1}) {
			t.Fatalf("enqueue %d should succeed", i)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := w.drain(ctx)
	if fake.calls.Load() != n {
		t.Fatalf("drain ran %d debits, want %d", fake.calls.Load(), n)
	}
	if report.Abandoned != 0 || report.Failed != 0 {
		t.Fatalf("drain must not abandon with budget to spare: %+v", report)
	}
	if w.snapshot().Queued != 0 {
		t.Fatalf("queue not empty after drain: %+v", w.snapshot())
	}
	// Idempotent: a second drain returns immediately with nothing to say.
	if again := w.drain(ctx); again != (settleDrainReport{}) {
		t.Fatalf("second drain must be a no-op, got %+v", again)
	}
}

func TestSettleWorkerDrainWithoutWorkersFlushesInline(t *testing.T) {
	// A worker-less instance (or one whose workers already exited) still
	// flushes its backlog inside drain rather than leaving it in the channel.
	fake := &fakeDebit{}
	w := newSettleWorker(nil, 8, 0, fake.fn, nil)
	for i := 0; i < 5; i++ {
		w.enqueue(SettleUnit{OrgID: "o", ID: "r"})
	}
	report := w.drain(context.Background())
	if fake.calls.Load() != 5 || report.Drained != 5 || report.Abandoned != 0 {
		t.Fatalf("inline drain: calls=%d report=%+v", fake.calls.Load(), report)
	}
}

func TestSettleWorkerDrainAbandonsWhenBudgetExpires(t *testing.T) {
	// A debit stuck past the shutdown budget is cancelled (its transaction
	// rolls back) and everything behind it is abandoned: all of them counted
	// and logged, none silently lost, and drain returns promptly.
	stuck := &fakeDebit{block: make(chan struct{})} // never released
	w := newSettleWorker(nil, 16, 1, stuck.fn, nil)
	const queued = 5
	for i := 0; i < queued; i++ {
		if !w.enqueue(SettleUnit{OrgID: "o", ID: "r", Micros: 1}) {
			t.Fatal("enqueue should succeed")
		}
	}
	waitFor(t, "first job in flight", func() bool { return stuck.calls.Load() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	report := w.drain(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("drain did not honor its budget: took %v", elapsed)
	}
	if report.Abandoned != queued {
		t.Fatalf("abandoned = %d, want %d (1 in flight + %d queued): %+v", report.Abandoned, queued, queued-1, report)
	}
	if report.Drained != 0 {
		t.Fatalf("nothing could have been drained: %+v", report)
	}
	if stuck.calls.Load() != 1 {
		t.Fatalf("no attempt may run against a dead context, got %d attempts", stuck.calls.Load())
	}
}

func TestSettleWorkerStoppedFallsBackToSync(t *testing.T) {
	fake := &fakeDebit{}
	w := newSettleWorker(nil, 16, 1, fake.fn, nil)
	w.stop()
	if w.enqueue(SettleUnit{OrgID: "o", ID: "after-stop"}) {
		t.Fatal("stopped worker must refuse the job so the caller debits synchronously")
	}
	if s := w.snapshot(); s.SyncFallback != 1 || s.Enqueued != 0 {
		t.Fatalf("expected the refusal to count as a sync fallback: %+v", s)
	}
	if fake.calls.Load() != 0 {
		t.Fatalf("refused job must not run in the worker, got %d calls", fake.calls.Load())
	}
}

func TestSettleWorkerConcurrentEnqueueDuringStop(t *testing.T) {
	// Handlers keep finishing while shutdown drains. Every enqueue must
	// either be accepted (and then debited by the drain) or refused (so the
	// handler debits synchronously); none may panic or vanish. Run with -race.
	fake := &fakeDebit{}
	w := newSettleWorker(nil, 64, 2, fake.fn, nil)

	const goroutines, perGoroutine = 16, 200
	var accepted, refused atomic.Int64
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			for i := 0; i < perGoroutine; i++ {
				if w.enqueue(SettleUnit{OrgID: "o", ID: "r", Micros: 1}) {
					accepted.Add(1)
				} else {
					refused.Add(1)
				}
			}
		}()
	}
	close(startGate)
	// Stop while the senders are mid-flight.
	time.Sleep(2 * time.Millisecond)
	report := w.drain(context.Background())
	wg.Wait()

	if got := accepted.Load() + refused.Load(); got != goroutines*perGoroutine {
		t.Fatalf("accounted %d enqueues, want %d", got, goroutines*perGoroutine)
	}
	// Whatever the worker accepted, it ran: before or during the drain.
	waitFor(t, "accepted jobs to run", func() bool { return fake.calls.Load() == accepted.Load() })
	if report.Abandoned != 0 {
		t.Fatalf("unbounded drain must not abandon: %+v", report)
	}
	// After stop every enqueue is refused, deterministically.
	if w.enqueue(SettleUnit{OrgID: "o", ID: "late"}) {
		t.Fatal("enqueue after drain must be refused")
	}
}

// --- Dedicated drain budget: a spent shutdown context must not lose money ---

func TestSettleWorkerDrainDetachedIgnoresCancelledShutdownCtx(t *testing.T) {
	// The shared shutdown context is often already spent (or cancelled on
	// the parent-cancelled path) when the drain starts. The drain must run
	// on its own budget and still settle every queued debit.
	fake := &fakeDebit{}
	w := newSettleWorker(nil, 64, 2, fake.fn, nil)
	const n = 20
	for i := 0; i < n; i++ {
		if !w.enqueue(SettleUnit{OrgID: "o", ID: "r", Micros: 1}) {
			t.Fatalf("enqueue %d should succeed", i)
		}
	}
	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel() // dead on arrival, exactly like listenAndServe's <-ctx.Done() path
	report := w.drainDetached(shutdownCtx, 5*time.Second)
	// Drained counts completions after the stop flag flipped; a worker may
	// have finished a job or two before that, so assert on the invariants
	// that matter: every accepted debit ran, none was abandoned.
	if fake.calls.Load() != n || report.Abandoned != 0 || w.snapshot().Queued != 0 {
		t.Fatalf("drain on a cancelled shutdown ctx: calls=%d queued=%d report=%+v, want all %d settled", fake.calls.Load(), w.snapshot().Queued, report, n)
	}
}

func TestSettleWorkerDrainDetachedStillBounded(t *testing.T) {
	// The dedicated budget is a real deadline: a stuck debit is abandoned
	// once it expires, regardless of the (live) parent context.
	stuck := &fakeDebit{block: make(chan struct{})}
	w := newSettleWorker(nil, 8, 1, stuck.fn, nil)
	w.enqueue(SettleUnit{OrgID: "o", ID: "r"})
	waitFor(t, "job in flight", func() bool { return stuck.calls.Load() == 1 })
	start := time.Now()
	report := w.drainDetached(context.Background(), 50*time.Millisecond)
	if time.Since(start) > 2*time.Second || report.Abandoned != 1 {
		t.Fatalf("detached drain must honor its own budget: took %v, report %+v", time.Since(start), report)
	}
}

func TestSettleDrainBudgetFloor(t *testing.T) {
	cases := map[string]time.Duration{
		"":        settleDrainMinBudget,
		"2s":      settleDrainMinBudget, // below floor: floor wins
		"garbage": settleDrainMinBudget,
		"30s":     30 * time.Second,
	}
	for raw, want := range cases {
		t.Setenv(settleDrainBudgetEnv, raw)
		if got := settleDrainBudget(); got != want {
			t.Fatalf("%s=%q: budget %v, want %v", settleDrainBudgetEnv, raw, got, want)
		}
	}
}
