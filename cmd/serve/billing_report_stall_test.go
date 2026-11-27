package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/billingmeter"
)

// The ingestion-stall alert reads billing_usage_report_last_success_timestamp_seconds.
// It must advance only on a tick that delivered every report, and for the
// inference reporter only on the replica that held the lock: a follower
// advancing it would hide a leader wedged mid-tick.
func TestPGInferenceUsageStallClockAdvancesOnlyOnLedSuccess(t *testing.T) {
	db := usageScratchDB(t)
	ctx := context.Background()
	org := "44444444-4444-4444-4444-444444444444"
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
	failedAt := time.Now().Truncate(time.Second)
	r.now = func() time.Time { return failedAt }
	before := billingmeter.UsageReportLastSuccess("inference")
	_, err := r.reportOnce(ctx)
	require.Error(t, err)
	require.Equal(t, before, billingmeter.UsageReportLastSuccess("inference"), "a failed tick must not advance the stall clock")

	// A follower: another session holds the reporter lock.
	holder, err := db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, inferenceUsageReporterLock)
	require.NoError(t, err)
	fail.Store(false)
	led, err := r.reportOnce(ctx)
	require.NoError(t, err)
	require.False(t, led)
	require.Equal(t, before, billingmeter.UsageReportLastSuccess("inference"), "a follower must not advance the stall clock")
	require.NoError(t, holder.Rollback())

	okAt := failedAt.Add(time.Minute)
	r.now = func() time.Time { return okAt }
	led, err = r.reportOnce(ctx)
	require.NoError(t, err)
	require.True(t, led)
	require.Equal(t, float64(okAt.Unix()), billingmeter.UsageReportLastSuccess("inference"))
}

func TestSharedSandboxStallClockAdvancesOnlyOnCleanTick(t *testing.T) {
	newReporter := func(t *testing.T, status int, at time.Time) *sharedSandboxBillingReporter {
		t.Helper()
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { db.Close() })
		mock.ExpectQuery(`SELECT tenant_id[\s\S]*FROM \([\s\S]*sandbox_instances`).
			WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(testSandboxBillingTenantID))
		mock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(int64(60), 1.0))
		mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if status != http.StatusOK {
				http.Error(w, "billing down", status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success": true, "sandbox_allowed": true}`))
		}))
		t.Cleanup(srv.Close)
		return &sharedSandboxBillingReporter{
			db:         sqlx.NewDb(db, "sqlmock"),
			live:       fakeSandboxLiveAccrual{},
			endpoint:   srv.URL,
			instanceID: "shared-sandbox-plane:test",
			client:     srv.Client(),
			now:        func() time.Time { return at },
		}
	}

	before := billingmeter.UsageReportLastSuccess("sandbox")
	failing := newReporter(t, http.StatusInternalServerError, time.Unix(1_900_000_100, 0))
	require.Error(t, failing.reportAll(context.Background()))
	require.Equal(t, before, billingmeter.UsageReportLastSuccess("sandbox"), "a failed tick must not advance the stall clock")

	okAt := time.Unix(1_900_000_200, 0)
	require.NoError(t, newReporter(t, http.StatusOK, okAt).reportAll(context.Background()))
	require.Equal(t, float64(okAt.Unix()), billingmeter.UsageReportLastSuccess("sandbox"))
}

// The org resource reporter feeds the gauges Billing shows; if it stops, the
// page quietly falls back to "Not metered yet". Its stall clock must advance
// only on a clean tick of the replica holding the lock, like inference.
func TestPGOrgResourceStallClockAdvancesOnlyOnLedSuccess(t *testing.T) {
	gw := resourceScratchDB(t, "gw")
	platform := resourceScratchDB(t, "platform")
	_, err := gw.Exec(resourceGatewaySchema)
	require.NoError(t, err)
	_, err = platform.Exec(resourcePlatformSchema)
	require.NoError(t, err)
	_, err = platform.Exec(`INSERT INTO everstack.organizations (id) VALUES ($1)`, resOrgA)
	require.NoError(t, err)
	ctx := context.Background()

	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "billing down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newOrgResourceUsageReporter(gw, platform, srv.URL, srv.Client())
	failedAt := time.Now().Truncate(time.Second)
	r.now = func() time.Time { return failedAt }
	before := billingmeter.UsageReportLastSuccess("resource")
	_, _, err = r.reportOnce(ctx)
	require.Error(t, err)
	require.Equal(t, before, billingmeter.UsageReportLastSuccess("resource"), "a failed tick must not advance the stall clock")

	holder, err := gw.BeginTxx(ctx, nil)
	require.NoError(t, err)
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, orgResourceUsageReporterLock)
	require.NoError(t, err)
	fail.Store(false)
	led, _, err := r.reportOnce(ctx)
	require.NoError(t, err)
	require.False(t, led)
	require.Equal(t, before, billingmeter.UsageReportLastSuccess("resource"), "a follower must not advance the stall clock")
	require.NoError(t, holder.Rollback())

	okAt := failedAt.Add(5 * time.Minute)
	r.now = func() time.Time { return okAt }
	led, _, err = r.reportOnce(ctx)
	require.NoError(t, err)
	require.True(t, led)
	require.Equal(t, float64(okAt.Unix()), billingmeter.UsageReportLastSuccess("resource"))
}
