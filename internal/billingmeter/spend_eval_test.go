package billingmeter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func v2Count(outcome, scope string) float64 {
	return counterValue(spendLimitEvaluated.WithLabelValues("shadow", outcome, scope, SpendBasisAllInference))
}

var v2Now = time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC)

func v2State(now time.Time, limits []SpendV2Limit, byKey map[string]SpendV2Sums) SpendV2State {
	day, month := spendPeriodStarts(now)
	st := SpendV2State{Limits: limits, ByKey: map[string]SpendV2Sums{}, DayStart: day, MonthStart: month}
	for k, s := range byKey {
		st.ByKey[k] = s
		st.Org.Day += s.Day
		st.Org.Month += s.Month
	}
	return st
}

func TestEvaluateSpendV2(t *testing.T) {
	orgDaily := SpendV2Limit{ID: "org-d", Period: "daily", LimitMicros: 10_000_000}
	orgMonthly := SpendV2Limit{ID: "org-m", Period: "monthly", LimitMicros: 100_000_000}
	keyDaily := SpendV2Limit{ID: "key-d", APIKeyID: "key-A", Period: "daily", LimitMicros: 2_000_000}
	otherKey := SpendV2Limit{ID: "key-b", APIKeyID: "key-B", Period: "daily", LimitMicros: 1}

	for _, tc := range []struct {
		name               string
		limits             []SpendV2Limit
		spend              map[string]SpendV2Sums
		key                string
		local              SpendV2Local
		pOrg, pKey         int64
		outcome, scope, id string
		hot                bool
	}{
		{name: "no limit", outcome: SpendOutcomeNoLimit, scope: SpendScopeNone},
		{name: "under the limit", limits: []SpendV2Limit{orgDaily, orgMonthly},
			spend: map[string]SpendV2Sums{"": {Day: 1_000_000, Month: 5_000_000}}, outcome: SpendOutcomeAdmit, scope: SpendScopeNone},
		{name: "org total sums every key and no key", limits: []SpendV2Limit{orgDaily},
			spend:   map[string]SpendV2Sums{"": {Day: 4_000_000}, "key-A": {Day: 3_000_000}, "key-B": {Day: 3_000_000}},
			outcome: SpendOutcomeWouldBlock, scope: SpendScopeOrganization, id: "org-d", hot: true},
		{name: "key row reached", limits: []SpendV2Limit{orgDaily, keyDaily}, key: "key-A",
			spend:   map[string]SpendV2Sums{"key-A": {Day: 2_000_000}},
			outcome: SpendOutcomeWouldBlock, scope: SpendScopeAPIKey, id: "key-d", hot: true},
		{name: "another key's row never applies", limits: []SpendV2Limit{otherKey}, key: "key-A",
			spend: map[string]SpendV2Sums{"key-B": {Day: 50}, "key-A": {Day: 50}}, outcome: SpendOutcomeNoLimit, scope: SpendScopeNone},
		{name: "a key row needs a key on the request", limits: []SpendV2Limit{keyDaily}, key: "",
			spend: map[string]SpendV2Sums{"key-A": {Day: 9_000_000}}, outcome: SpendOutcomeNoLimit, scope: SpendScopeNone},
		{name: "pending estimates reach it: pending_block", limits: []SpendV2Limit{orgDaily},
			spend: map[string]SpendV2Sums{"": {Day: 6_000_000}}, pOrg: 4_000_000,
			outcome: SpendOutcomePendingBlock, scope: SpendScopeOrganization, id: "org-d", hot: true},
		{name: "just under with pending: admit", limits: []SpendV2Limit{orgDaily},
			spend: map[string]SpendV2Sums{"": {Day: 6_000_000}}, pOrg: 3_999_999, outcome: SpendOutcomeAdmit, scope: SpendScopeNone, hot: true},
		{name: "key P against the key row", limits: []SpendV2Limit{keyDaily}, key: "key-A",
			spend: map[string]SpendV2Sums{"key-A": {Day: 1_000_000}}, pOrg: 0, pKey: 1_000_000,
			outcome: SpendOutcomePendingBlock, scope: SpendScopeAPIKey, id: "key-d", hot: true},
		{name: "would_block wins over an earlier pending_block", limits: []SpendV2Limit{orgDaily, keyDaily}, key: "key-A",
			spend: map[string]SpendV2Sums{"key-A": {Day: 2_500_000}, "": {Day: 5_000_000}}, pOrg: 5_000_000,
			outcome: SpendOutcomeWouldBlock, scope: SpendScopeAPIKey, id: "key-d", hot: true},
		{name: "local delta counts toward S", limits: []SpendV2Limit{orgDaily},
			spend: map[string]SpendV2Sums{"": {Day: 9_000_000}}, local: SpendV2Local{Org: SpendV2Sums{Day: 1_000_000, Month: 1_000_000}},
			outcome: SpendOutcomeWouldBlock, scope: SpendScopeOrganization, id: "org-d", hot: true},
		{name: "a zero limit never blocks", limits: []SpendV2Limit{{ID: "z", Period: "daily"}},
			spend: map[string]SpendV2Sums{"": {Day: 9}}, outcome: SpendOutcomeNoLimit, scope: SpendScopeNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateSpendV2(v2State(v2Now, tc.limits, tc.spend), v2Now, tc.key, tc.local, tc.pOrg, tc.pKey)
			require.Equal(t, tc.outcome, d.Outcome)
			require.Equal(t, tc.scope, d.Scope)
			require.Equal(t, tc.hot, d.Hot)
			if tc.id == "" {
				require.Nil(t, d.Limit)
			} else {
				require.NotNil(t, d.Limit)
				require.Equal(t, tc.id, d.Limit.ID)
			}
		})
	}
}

// A state read yesterday says nothing about today: its day sums are not
// carried over the UTC midnight boundary (and its month sums not over the
// month boundary).
func TestEvaluateSpendV2UTCBoundaries(t *testing.T) {
	daily := SpendV2Limit{ID: "d", Period: "daily", LimitMicros: 10}
	monthly := SpendV2Limit{ID: "m", Period: "monthly", LimitMicros: 10}
	read := time.Date(2026, 10, 31, 23, 59, 58, 0, time.UTC)
	st := v2State(read, []SpendV2Limit{daily, monthly}, map[string]SpendV2Sums{"": {Day: 50, Month: 50}})

	d := EvaluateSpendV2(st, read, "", SpendV2Local{}, 0, 0)
	require.Equal(t, SpendOutcomeWouldBlock, d.Outcome)

	next := time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC)
	d = EvaluateSpendV2(st, next, "", SpendV2Local{}, 0, 0)
	require.Equal(t, SpendOutcomeAdmit, d.Outcome, "a new UTC day and month start at zero")

	sameMonth := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	st = v2State(sameMonth, []SpendV2Limit{daily, monthly}, map[string]SpendV2Sums{"": {Day: 5, Month: 50}})
	d = EvaluateSpendV2(st, sameMonth.Add(13*time.Hour), "", SpendV2Local{}, 0, 0)
	require.Equal(t, SpendOutcomeWouldBlock, d.Outcome)
	require.Equal(t, "m", d.Limit.ID, "the month still counts after the day rolled over")
}

// v2Meter is an active meter whose v2 reads come from read.
func v2Meter(t *testing.T, now *atomic.Pointer[time.Time], read func(ctx context.Context, orgID string) (SpendV2State, error)) *Meter {
	t.Helper()
	m := &Meter{billingDB: sqlx.NewDb(nil, "pgx"), shadow: newShadowRunner(4, 3*time.Second), spendCold: newShadowRunner(4, 3*time.Second)}
	m.spendNow = func() time.Time { return *now.Load() }
	m.spendV2.read = func(ctx context.Context, _ *sqlx.DB, orgID string, _ time.Time) (SpendV2State, error) {
		return read(ctx, orgID)
	}
	return m
}

func clockAt(t time.Time) *atomic.Pointer[time.Time] {
	var p atomic.Pointer[time.Time]
	p.Store(&t)
	return &p
}

func advance(p *atomic.Pointer[time.Time], d time.Duration) {
	t := p.Load().Add(d)
	p.Store(&t)
}

// waitRefreshed waits until org's entry was fetched at or after at.
func waitRefreshed(t *testing.T, m *Meter, org string, at time.Time) {
	t.Helper()
	require.Eventually(t, func() bool {
		m.spendV2.mu.Lock()
		defer m.spendV2.mu.Unlock()
		e := m.spendV2.m[org]
		return e != nil && !e.fetched.Before(at)
	}, 2*time.Second, 5*time.Millisecond)
}

// One query per organization per TTL, however many keys and attempts; the
// first (cold) evaluation runs in the background, never on the request.
func TestSpendV2OneReadPerOrgPerTTL(t *testing.T) {
	now := clockAt(v2Now)
	var reads atomic.Int64
	limits := []SpendV2Limit{{ID: "o", Period: "monthly", LimitMicros: 1_000_000_000}}
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		reads.Add(1)
		return v2State(*now.Load(), limits, nil), nil
	})
	ctx := context.Background()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-1", APIKeyID: "k0"}))
	m.spendCold.wait()
	require.EqualValues(t, 1, reads.Load())
	for i := 0; i < 50; i++ {
		key := []string{"k1", "k2", "k3", ""}[i%4]
		m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-1", APIKeyID: key, EstimateMicros: 10}))
	}
	require.EqualValues(t, 1, reads.Load(), "a warm entry answers every key without another read")

	advance(now, spendV2TTL)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-1"}))
	waitRefreshed(t, m, "org-1", *now.Load())
	require.EqualValues(t, 2, reads.Load())
}

// Stale-while-revalidate: an expired entry is evaluated at once while one
// background refresh runs; the request never waits on the read.
func TestSpendV2StaleWhileRevalidateNeverBlocks(t *testing.T) {
	now := clockAt(v2Now)
	release := make(chan struct{})
	var reads atomic.Int64
	limits := []SpendV2Limit{{ID: "o", Period: "daily", LimitMicros: 1_000_000}}
	m := v2Meter(t, now, func(ctx context.Context, _ string) (SpendV2State, error) {
		if reads.Add(1) > 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return SpendV2State{}, ctx.Err()
			}
		}
		return v2State(*now.Load(), limits, map[string]SpendV2Sums{"": {Day: 2_000_000}}), nil
	})
	defer close(release)
	ctx := context.Background()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-swr"}))
	m.spendCold.wait()

	advance(now, spendV2HotTTL+time.Second) // the reached limit made the entry hot
	before := v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization)
	done := make(chan struct{})
	go func() {
		m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-swr"}))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("an expired entry made the attempt wait on the refresh")
	}
	require.Equal(t, before+1, v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization))
	require.Eventually(t, func() bool { return reads.Load() == 2 }, time.Second, 5*time.Millisecond, "one background refresh started")
}

// Fail stale: a replica that saw "reached" keeps reporting would_block
// through read errors, and each failed read counts error and stale. A cold
// start with the store down evaluates nothing (open) and never blocks.
func TestSpendV2FailStaleAndColdOpen(t *testing.T) {
	now := clockAt(v2Now)
	var fail atomic.Bool
	limits := []SpendV2Limit{{ID: "o", Period: "daily", LimitMicros: 1_000_000}}
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		if fail.Load() {
			return SpendV2State{}, errors.New("platform db down")
		}
		return v2State(*now.Load(), limits, map[string]SpendV2Sums{"": {Day: 1_000_000}}), nil
	})
	ctx := context.Background()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-stale"}))
	m.spendCold.wait()

	fail.Store(true)
	errBefore, staleBefore := v2Count(SpendOutcomeError, SpendScopeNone), v2Count(SpendOutcomeStale, SpendScopeNone)
	blockBefore := v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization)
	advance(now, 30*time.Minute)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-stale"}))
	require.Eventually(t, func() bool { return v2Count(SpendOutcomeError, SpendScopeNone) == errBefore+1 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, staleBefore+1, v2Count(SpendOutcomeStale, SpendScopeNone))
	require.Equal(t, blockBefore+1, v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization), "the last state still says reached")

	// Within the retry interval the next attempt does not re-read.
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-stale"}))
	require.Equal(t, errBefore+1, v2Count(SpendOutcomeError, SpendScopeNone))
	require.Equal(t, blockBefore+2, v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization))

	// Cold start, store down: open.
	openBefore := v2Count(SpendOutcomeOpen, SpendScopeNone)
	start := time.Now()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-cold"}))
	require.Less(t, time.Since(start), 500*time.Millisecond)
	m.spendCold.wait()
	require.Equal(t, openBefore+1, v2Count(SpendOutcomeOpen, SpendScopeNone))
	// Past the maximum age a stale entry is dropped too: open.
	advance(now, spendV2MaxAge)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-stale"}))
	m.spendCold.wait()
	require.Equal(t, openBefore+2, v2Count(SpendOutcomeOpen, SpendScopeNone))
}

// P: concurrent attempts are admitted until their estimates reach the
// limit, the rest report pending_block, and P returns to zero once every
// attempt ended.
func TestSpendV2PendingFanOut(t *testing.T) {
	now := clockAt(v2Now)
	limits := []SpendV2Limit{{ID: "o", Period: "daily", LimitMicros: 50_000_000}}
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		return v2State(*now.Load(), limits, nil), nil
	})
	ctx := context.Background()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-fan"}))
	m.spendCold.wait()

	admitBefore, pendBefore := v2Count(SpendOutcomeAdmit, SpendScopeNone), v2Count(SpendOutcomePendingBlock, SpendScopeOrganization)
	var holds []PendingSpend
	for i := 0; i < 50; i++ {
		holds = append(holds, m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-fan", APIKeyID: "k", EstimateMicros: 5_000_000}))
	}
	// $5 each against $50: 10 fit (P before each is 0, 5, ..., 45), 40 do not.
	require.Equal(t, admitBefore+10, v2Count(SpendOutcomeAdmit, SpendScopeNone))
	require.Equal(t, pendBefore+40, v2Count(SpendOutcomePendingBlock, SpendScopeOrganization))
	orgP, keyP := m.spendPending.get("org-fan", "k")
	require.EqualValues(t, 250_000_000, orgP)
	require.EqualValues(t, 250_000_000, keyP)

	var wg sync.WaitGroup
	for _, h := range holds {
		wg.Add(1)
		go func(h PendingSpend) { defer wg.Done(); m.ReleaseSpend(h) }(h)
	}
	wg.Wait()
	orgP, keyP = m.spendPending.get("org-fan", "k")
	require.Zero(t, orgP)
	require.Zero(t, keyP)
	m.ReleaseSpend(PendingSpend{}) // the zero hold is a no-op
}

// Internal calls count in P (they spend the customer's key) but are never
// evaluated.
func TestSpendV2InternalCountsButIsNotEvaluated(t *testing.T) {
	now := clockAt(v2Now)
	var reads atomic.Int64
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		reads.Add(1)
		return v2State(*now.Load(), nil, nil), nil
	})
	h := m.AdmitSpend(context.Background(), SpendAttempt{OrgID: "org-int", EstimateMicros: 7, Internal: true})
	orgP, _ := m.spendPending.get("org-int", "")
	require.EqualValues(t, 7, orgP)
	require.Zero(t, reads.Load())
	m.ReleaseSpend(h)
	orgP, _ = m.spendPending.get("org-int", "")
	require.Zero(t, orgP)
}

// The local delta mirrors the rollup arithmetic (charged platform amount,
// BYOK list-price estimate, "" for no key), counts only what the rollups do
// not hold yet (buckets at or after the watermark), and is pruned there.
func TestSpendV2LocalDelta(t *testing.T) {
	m := &Meter{billingDB: sqlx.NewDb(nil, "pgx")}
	at := v2Now
	byok := func(key string, micros int64, when time.Time) SettleUnit {
		u := SettleUnit{ID: "u", OrgID: "org-l", At: when, Attempt: &AttemptRecord{Provenance: ProvenanceBYOK, APIKeyID: key, Role: RoleServed}}
		u.Attempt.EstCostMicros = &micros
		return u
	}
	m.recordLocalSpend(byok("k1", 3, at), false)
	m.recordLocalSpend(byok("", 4, at.Add(10*time.Second)), false)
	platform := SettleUnit{ID: "p", OrgID: "org-l", At: at, Micros: 100, Attempt: &AttemptRecord{Provenance: ProvenancePlatform, Role: RoleServed}}
	m.recordLocalSpend(platform, true)
	retry := SettleUnit{ID: "r", OrgID: "org-l", At: at, Micros: 0, Attempt: &AttemptRecord{Provenance: ProvenancePlatform, Role: RolePlatformRetry}}
	m.recordLocalSpend(retry, false)
	m.recordLocalSpend(byok("k1", 5, at), false) // another org-l unit in the same bucket
	other := byok("k1", 1000, at)
	other.OrgID = "org-other"
	m.recordLocalSpend(other, false)

	day, month := spendPeriodStarts(at)
	got := m.spendLocal.sums("org-l", "k1", time.Time{}, day, month)
	require.Equal(t, SpendV2Local{Org: SpendV2Sums{Day: 112, Month: 112}, Key: SpendV2Sums{Day: 8, Month: 8}}, got)

	// The rollups now hold everything recorded before at+5s.
	w := at.Add(5 * time.Second)
	got = m.spendLocal.sums("org-l", "k1", w, day, month)
	require.Equal(t, SpendV2Local{Org: SpendV2Sums{Day: 4, Month: 4}}, got)
	m.spendLocal.prune(w)
	got = m.spendLocal.sums("org-l", "k1", time.Time{}, day, month)
	require.Equal(t, SpendV2Local{Org: SpendV2Sums{Day: 4, Month: 4}}, got, "pruned buckets are gone")
	m.recordLocalSpend(byok("k1", 9, at), false) // late, already behind the watermark
	got = m.spendLocal.sums("org-l", "k1", time.Time{}, day, month)
	require.EqualValues(t, 4, got.Org.Day)
}

// Settle feeds the local delta, so spend settled since the last rollup pass
// counts toward the limit before the rollups catch up.
func TestSpendV2SettleFeedsLocalDelta(t *testing.T) {
	now := clockAt(v2Now)
	limits := []SpendV2Limit{{ID: "k", APIKeyID: "key-1", Period: "daily", LimitMicros: 10}}
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		st := v2State(*now.Load(), limits, nil)
		st.Watermark = v2Now.Add(-time.Minute)
		return st, nil
	})
	m.worker = newSettleWorker(nil, 8, 1, func(context.Context, *sqlx.DB, SettleUnit) error { return nil }, func(SettleUnit, error) {})
	defer m.worker.stop()
	ctx := context.Background()
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-s", APIKeyID: "key-1"}))
	m.spendCold.wait()

	est := int64(12)
	m.Settle(ctx, SettleUnit{ID: "a1", OrgID: "org-s", Modality: MetricChat, At: v2Now,
		Attempt: &AttemptRecord{Provenance: ProvenanceBYOK, Role: RoleServed, APIKeyID: "key-1", EstCostMicros: &est}})
	before := v2Count(SpendOutcomeWouldBlock, SpendScopeAPIKey)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-s", APIKeyID: "key-1"}))
	require.Equal(t, before+1, v2Count(SpendOutcomeWouldBlock, SpendScopeAPIKey))
}

// The local delta and the rollups split at the watermark with sub-second
// precision: a bucket that starts before the watermark (even within the same
// second) is treated as rolled up, one at or after it is local.
func TestSpendV2LocalDeltaSubSecondWatermark(t *testing.T) {
	var l spendLocal
	start := time.Date(2026, 10, 21, 15, 30, 10, 0, time.UTC) // a bucket boundary
	l.add("o", "", start.Add(time.Second), 7)
	day, month := spendPeriodStarts(start)
	require.EqualValues(t, 7, l.sums("o", "", start, day, month).Org.Day, "a bucket starting at the watermark is local")
	require.Zero(t, l.sums("o", "", start.Add(700*time.Millisecond), day, month).Org.Day,
		"a bucket starting before a sub-second watermark is already in the rollups")
	l.prune(start.Add(700 * time.Millisecond))
	require.Zero(t, l.sums("o", "", time.Time{}, day, month).Org.Day, "and pruned")
	l.add("o", "", start.Add(time.Second), 3) // behind the floor: never re-added
	require.Zero(t, l.sums("o", "", time.Time{}, day, month).Org.Day)
}

// A panic in the meter's own evaluation path is contained and the hold it
// already took is still returned, so P goes back to zero.
func TestSpendV2PanicInEvaluationStillReleasesP(t *testing.T) {
	now := clockAt(v2Now)
	// No cold-miss runner: the cold path panics on the nil runner.
	m := &Meter{billingDB: sqlx.NewDb(nil, "pgx")}
	m.spendNow = func() time.Time { return *now.Load() }
	before := counterValue(spendV2Panics)
	h := m.AdmitSpend(context.Background(), SpendAttempt{OrgID: "org-panic", APIKeyID: "k", EstimateMicros: 5})
	require.Equal(t, before+1, counterValue(spendV2Panics))
	m.ReleaseSpend(h)
	orgP, keyP := m.spendPending.get("org-panic", "k")
	require.Zero(t, orgP, "P leaked after a recovered panic")
	require.Zero(t, keyP)
}

// A panic in the read (inside singleflight, which would re-raise it on a
// goroutine nothing can recover) becomes a read error: counted, open or
// stale, and the process keeps running.
func TestSpendV2ReadPanicIsAReadError(t *testing.T) {
	now := clockAt(v2Now)
	var panicky atomic.Bool
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		if panicky.Load() {
			panic("read bug")
		}
		return v2State(*now.Load(), []SpendV2Limit{{ID: "o", Period: "daily", LimitMicros: 10}}, nil), nil
	})
	ctx := context.Background()
	panicky.Store(true)
	openBefore := v2Count(SpendOutcomeOpen, SpendScopeNone)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-rp"})) // cold: background read panics
	m.spendCold.wait()
	require.Equal(t, openBefore+1, v2Count(SpendOutcomeOpen, SpendScopeNone))

	panicky.Store(false)
	advance(now, spendV2Retry)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-rp"}))
	m.spendCold.wait()
	panicky.Store(true)
	advance(now, spendV2TTL)
	staleBefore := v2Count(SpendOutcomeStale, SpendScopeNone)
	m.ReleaseSpend(m.AdmitSpend(ctx, SpendAttempt{OrgID: "org-rp"})) // stale: refresh panics on the singleflight goroutine
	require.Eventually(t, func() bool { return v2Count(SpendOutcomeStale, SpendScopeNone) == staleBefore+1 }, 2*time.Second, 5*time.Millisecond)
}

// v2 cold misses have their own runner: a saturated G1 shadow runner never
// drops them (and they never take G1's slots).
func TestSpendV2ColdMissesDoNotShareTheG1Runner(t *testing.T) {
	now := clockAt(v2Now)
	m := v2Meter(t, now, func(context.Context, string) (SpendV2State, error) {
		return v2State(*now.Load(), []SpendV2Limit{{ID: "o", Period: "daily", LimitMicros: 1_000_000}}, nil), nil
	})
	block := make(chan struct{})
	defer close(block)
	for m.shadow.try(func(context.Context) { <-block }) {
	}
	admitBefore := v2Count(SpendOutcomeAdmit, SpendScopeNone)
	droppedBefore := v2Count(SpendOutcomeDropped, SpendScopeNone)
	m.ReleaseSpend(m.AdmitSpend(context.Background(), SpendAttempt{OrgID: "org-g1"}))
	m.spendCold.wait()
	require.Equal(t, droppedBefore, v2Count(SpendOutcomeDropped, SpendScopeNone))
	require.Equal(t, admitBefore+1, v2Count(SpendOutcomeAdmit, SpendScopeNone))
}
