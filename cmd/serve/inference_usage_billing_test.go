package serve

// Real-Postgres tests for the inference usage reporter. Skips unless
// EVS_TEST_PG_DSN points at a Postgres the test may create databases on (for
// example postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/billingmeter"
)

func usageScratchDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres usage reporter test")
	}
	admin, err := sqlx.Connect("pgx", dsn)
	require.NoError(t, err)
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	name := "usage_reporter_" + hex.EncodeToString(suffix[:])
	_, err = admin.Exec(`CREATE DATABASE ` + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		admin.Close()
	})
	scratch := dsn
	if i := strings.LastIndex(dsn, "/"); i > 0 {
		rest, q := dsn[i+1:], ""
		if j := strings.Index(rest, "?"); j >= 0 {
			q = rest[j:]
		}
		scratch = dsn[:i+1] + name + q
	}
	db, err := sqlx.Connect("pgx", scratch)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	matches, err := filepath.Glob(filepath.Join("..", "..", "internal", "database", "migrations", "sql", "postgres", "inference_usage_counters_*", "up.sql"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	up, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	_, err = db.Exec(`CREATE SCHEMA IF NOT EXISTS everstack; CREATE SCHEMA IF NOT EXISTS billing;
		CREATE TABLE billing.subscriptions (
			organization_id UUID PRIMARY KEY,
			billing_period VARCHAR(50) NOT NULL,
			status VARCHAR(50) NOT NULL,
			current_period_start TIMESTAMPTZ NOT NULL,
			current_period_end TIMESTAMPTZ NOT NULL,
			cancel_at_period_end BOOLEAN NOT NULL DEFAULT false)`)
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	return db
}

type capturedSnapshots struct {
	mu    sync.Mutex
	snaps []inferenceUsageSnapshot
}

func (c *capturedSnapshots) all() []inferenceUsageSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]inferenceUsageSnapshot(nil), c.snaps...)
}

// Two replicas tick at the same moment: exactly one reports (one POST per
// organization), the other skips. The totals are the period sums: the
// calendar month for an organization without a subscription, the live
// subscription window for one with.
func TestPGInferenceUsageReporterElectsOneReplica(t *testing.T) {
	db := usageScratchDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	free := "11111111-1111-1111-1111-111111111111"
	paid := "22222222-2222-2222-2222-222222222222"

	// free: 2 requests this month, 1 last month (must not count).
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, free, now.Add(-time.Hour), billingmeter.UsageDelta{Requests: 2, InputTokens: 100, OutputTokens: 40}))
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, free, time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC), billingmeter.UsageDelta{Requests: 1, InputTokens: 999}))
	// paid: live period Sep 10 to Oct 10; a Sep 5 hour belongs to the
	// previous period.
	_, err := db.Exec(`INSERT INTO billing.subscriptions VALUES ($1, 'monthly', 'active', '2026-09-10T00:00:00Z', '2026-10-10T00:00:00Z', false)`, paid)
	require.NoError(t, err)
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, paid, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), billingmeter.UsageDelta{Requests: 7, InputTokens: 7}))
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, paid, time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC), billingmeter.UsageDelta{Requests: 3, InputTokens: 30, OutputTokens: 3}))
	// Not a UUID: skipped, never posted.
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, "tenant-alias", now, billingmeter.UsageDelta{Requests: 1}))

	got := &capturedSnapshots{}
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s inferenceUsageSnapshot
		require.NoError(t, json.NewDecoder(r.Body).Decode(&s))
		got.mu.Lock()
		got.snaps = append(got.snaps, s)
		got.mu.Unlock()
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	replica := func() *inferenceUsageReporter {
		r := newInferenceUsageReporter(db, db, srv.URL, srv.Client())
		r.now = func() time.Time { return now }
		return r
	}
	a, b := replica(), replica()
	require.Equal(t, a.sourceID, b.sourceID, "replicas must share one stable source id")
	require.NotEqual(t, sharedSandboxBillingSourceID(), a.sourceID, "must not share the sandbox reporter's source id")

	var aLed bool
	var aErr error
	done := make(chan struct{})
	go func() {
		aLed, aErr = a.reportOnce(ctx)
		close(done)
	}()
	<-entered // a holds the lock and is mid-POST
	bLed, bErr := b.reportOnce(ctx)
	require.NoError(t, bErr)
	require.False(t, bLed, "second replica must not report while the first holds the lock")
	close(release)
	<-done
	require.NoError(t, aErr)
	require.True(t, aLed)

	snaps := got.all()
	require.Len(t, snaps, 2, "one POST per reportable organization")
	byOrg := map[string]inferenceUsageSnapshot{}
	for _, s := range snaps {
		byOrg[s.OrganizationID] = s
		require.Equal(t, a.sourceID, s.InstanceID)
	}
	require.Equal(t, inferenceUsageSnapshot{OrganizationID: free, InstanceID: a.sourceID,
		CumulativeTotalTokens: 140, CumulativeTotalRequests: 2, CumulativeInputTokens: 100, CumulativeOutputTokens: 40}, byOrg[free])
	require.Equal(t, inferenceUsageSnapshot{OrganizationID: paid, InstanceID: a.sourceID,
		CumulativeTotalTokens: 33, CumulativeTotalRequests: 3, CumulativeInputTokens: 30, CumulativeOutputTokens: 3}, byOrg[paid])

	// Lock released at the end of a's tick: b can lead the next one, and
	// re-sending the same absolute totals is idempotent for billing.
	bLed, bErr = b.reportOnce(ctx)
	require.NoError(t, bErr)
	require.True(t, bLed)
	require.Len(t, got.all(), 4)
}

// A billing outage fails the tick (logged, retried next tick) and leaves the
// counters untouched.
func TestPGInferenceUsageReporterFailureIsRetryable(t *testing.T) {
	db := usageScratchDB(t)
	ctx := context.Background()
	org := "33333333-3333-3333-3333-333333333333"
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, org, time.Now(), billingmeter.UsageDelta{Requests: 1, InputTokens: 5}))
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "billing down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := newInferenceUsageReporter(db, nil, srv.URL, srv.Client())
	led, err := r.reportOnce(ctx)
	require.True(t, led)
	require.ErrorContains(t, err, "503")
	fail.Store(false)
	_, err = r.reportOnce(ctx)
	require.NoError(t, err)
}

// A tick reports which organizations' totals moved, so the Info tick line
// fires when a request reaches billing and stays quiet on an idle plane. A
// failed post is not remembered as sent: the next tick reports it as moved.
func TestPGInferenceUsageReporterTickReportsChangedOrganizations(t *testing.T) {
	db := usageScratchDB(t)
	ctx := context.Background()
	a := "44444444-4444-4444-4444-444444444444"
	b := "55555555-5555-5555-5555-555555555555"
	now := time.Now()
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, a, now, billingmeter.UsageDelta{Requests: 1, InputTokens: 5}))
	require.NoError(t, billingmeter.UsageIncrement(ctx, db, b, now, billingmeter.UsageDelta{Requests: 2, OutputTokens: 9}))
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "billing down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := newInferenceUsageReporter(db, nil, srv.URL, srv.Client())

	tick, err := r.tick(ctx)
	require.NoError(t, err)
	require.True(t, tick.led)
	require.ElementsMatch(t, []string{a, b}, tick.changed, "first tick after start reports every organization as moved")

	tick, err = r.tick(ctx)
	require.NoError(t, err)
	require.Empty(t, tick.changed, "no new traffic: nothing moved")

	require.NoError(t, billingmeter.UsageIncrement(ctx, db, b, now, billingmeter.UsageDelta{Requests: 1, InputTokens: 1}))
	fail.Store(true)
	tick, err = r.tick(ctx)
	require.Error(t, err)
	require.Empty(t, tick.changed, "a failed post did not reach billing")
	fail.Store(false)
	tick, err = r.tick(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{b}, tick.changed)
}
