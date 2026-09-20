package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/jmoiron/sqlx"
)

const testSandboxBillingTenantID = "11111111-1111-1111-1111-111111111111"

func expectManagedOrganization(mock sqlmock.Sqlmock, tenantID, plan string) {
	mock.ExpectQuery(`SELECT o[.]id::text AS organization_id`).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).AddRow(tenantID, plan))
}

type fakeSandboxLiveAccrual struct {
	costUSD float64
	seconds int64
}

func (f fakeSandboxLiveAccrual) LiveAccruedCost(context.Context, string) (float64, int64) {
	return f.costUSD, f.seconds
}

type fakeTenantSandboxUsageReporter struct {
	err      error
	access   sharedSandboxBillingAccess
	reported []string
}

type fakeTenantSandboxStopper struct {
	stopped    []string
	suspended  []string
	resumed    []string
	count      int
	suspendedN int
	resumedN   int
	err        error
	suspendErr error
}

type fakeBrowserUsageTotals struct {
	totals browserpool.UsageTotals
}

type fakeBrowserUsageRecorder struct {
	started int
}

func (f fakeBrowserUsageTotals) TotalsForTenant(context.Context, string, time.Time) (browserpool.UsageTotals, error) {
	return f.totals, nil
}

func (f *fakeBrowserUsageRecorder) Start(context.Context, *browserpool.Lease, time.Time) error {
	f.started++
	return nil
}

func (f *fakeBrowserUsageRecorder) Heartbeat(context.Context, string, time.Time) error {
	return nil
}

func (f *fakeBrowserUsageRecorder) Finish(context.Context, string, time.Time, string) error {
	return nil
}

func (f *fakeTenantSandboxUsageReporter) ReportTenant(_ context.Context, tenantID string) (sharedSandboxBillingAccess, error) {
	f.reported = append(f.reported, tenantID)
	return f.access, f.err
}

func (f *fakeTenantSandboxStopper) StopTenantSandboxes(_ context.Context, tenantID string) (int, error) {
	f.stopped = append(f.stopped, tenantID)
	return f.count, f.err
}

func (f *fakeTenantSandboxStopper) SuspendTenantAgents(_ context.Context, tenantID string) (int, error) {
	f.suspended = append(f.suspended, tenantID)
	return f.suspendedN, f.suspendErr
}

func (f *fakeTenantSandboxStopper) ResumeTenantAgents(_ context.Context, tenantID string) (int, error) {
	f.resumed = append(f.resumed, tenantID)
	return f.resumedN, nil
}

func TestSharedSandboxBillingReporterIncludesClosedAndLiveUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(int64(40), 1.25))
	mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))

	var received sharedSandboxUsageSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"sandbox_allowed": true,
			"starter_credit_micros": 5000000,
			"starter_credit_used_micros": 1750000,
			"starter_credit_remaining_micros": 3250000
		}`))
	}))
	defer server.Close()

	reporter := &sharedSandboxBillingReporter{
		db:   sqlx.NewDb(db, "sqlmock"),
		live: fakeSandboxLiveAccrual{costUSD: 0.5, seconds: 5},
		browser: fakeBrowserUsageTotals{totals: browserpool.UsageTotals{
			RuntimeSeconds: 3_600,
			CostMicros:     10_000,
		}},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
	}
	access, err := reporter.ReportTenant(context.Background(), testSandboxBillingTenantID)
	if err != nil {
		t.Fatalf("report tenant: %v", err)
	}
	if !access.SandboxAllowed || access.StarterCreditRemainingMicros != 3_250_000 {
		t.Fatalf("access = %#v", access)
	}

	if received.OrganizationID != testSandboxBillingTenantID {
		t.Fatalf("organization ID = %q", received.OrganizationID)
	}
	if received.InstanceID != "shared-sandbox-plane:test" {
		t.Fatalf("instance ID = %q", received.InstanceID)
	}
	if received.CumulativeSandboxComputeSeconds != 45 {
		t.Fatalf("compute seconds = %d, want 45", received.CumulativeSandboxComputeSeconds)
	}
	if received.ConcurrentSandboxesCount != 3 {
		t.Fatalf("concurrent sandboxes = %d, want 3", received.ConcurrentSandboxesCount)
	}
	if received.CumulativeSandboxComputeCostMicros != 1_750_000 {
		t.Fatalf("cost micros = %d, want 1750000", received.CumulativeSandboxComputeCostMicros)
	}
	if received.CumulativeBrowserRuntimeSeconds != 3_600 {
		t.Fatalf("browser seconds = %d, want 3600", received.CumulativeBrowserRuntimeSeconds)
	}
	if received.CumulativeBrowserRuntimeCostMicros != 10_000 {
		t.Fatalf("browser cost micros = %d, want 10000", received.CumulativeBrowserRuntimeCostMicros)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestSharedSandboxBillingReporterMapsManagedInstanceUsageToOrganization(t *testing.T) {
	usageDB, usageMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create usage sqlmock: %v", err)
	}
	defer usageDB.Close()
	platformDB, platformMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create platform sqlmock: %v", err)
	}
	defer platformDB.Close()

	const instanceID = "22222222-2222-2222-2222-222222222222"
	platformMock.ExpectQuery(`SELECT o[.]id::text AS organization_id[\s\S]*tenant_config`).
		WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).
			AddRow(testSandboxBillingTenantID, "basic"))
	platformMock.ExpectQuery(`SELECT [$]1::text AS tenant_id[\s\S]*FROM everstack[.]managed_instances`).
		WithArgs(testSandboxBillingTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(instanceID))

	usageMock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(int64(90), 2.5))
	usageMock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))

	var received sharedSandboxUsageSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"sandbox_allowed":true,"billing_active":true}`))
	}))
	defer server.Close()

	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(usageDB, "sqlmock"),
		billingDB:  sqlx.NewDb(platformDB, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "managed-gateway:test",
		client:     server.Client(),
	}
	access, err := reporter.ReportTenant(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("report managed instance tenant: %v", err)
	}
	if !access.SandboxAllowed || !access.BillingActive {
		t.Fatalf("access = %#v", access)
	}
	if received.OrganizationID != testSandboxBillingTenantID {
		t.Fatalf("organization ID = %q, want %q", received.OrganizationID, testSandboxBillingTenantID)
	}
	if received.InstanceID != "managed-gateway:test" {
		t.Fatalf("meter source = %q", received.InstanceID)
	}
	if received.CumulativeSandboxComputeSeconds != 90 || received.CumulativeSandboxComputeCostMicros != 2_500_000 {
		t.Fatalf("sandbox meter = (%d seconds, %d micros)", received.CumulativeSandboxComputeSeconds, received.CumulativeSandboxComputeCostMicros)
	}
	if err := usageMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := platformMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedSandboxBillingReporterStopsTenantWhenCreditIsExhausted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT tenant_id[\s\S]*FROM \([\s\S]*sandbox_instances`).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(testSandboxBillingTenantID))
	mock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(int64(60), 5.0))
	mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"sandbox_allowed": false,
			"starter_credit_micros": 5000000,
			"starter_credit_used_micros": 5000000,
			"starter_credit_remaining_micros": 0
		}`))
	}))
	defer server.Close()

	stopper := &fakeTenantSandboxStopper{}
	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(db, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
		stopper:    stopper,
	}
	if err := reporter.reportAll(context.Background()); err != nil {
		t.Fatalf("report all: %v", err)
	}
	if len(stopper.stopped) != 1 || stopper.stopped[0] != testSandboxBillingTenantID {
		t.Fatalf("stopped tenants = %#v", stopper.stopped)
	}
	// Stopping the VM without parking the agent is the bug that let six
	// sandboxes on eu-gra-1-prod survive ~8,640 stop calls a day: the
	// persistent-agent reconciler reprovisioned each one ~90s later.
	if len(stopper.suspended) != 1 || stopper.suspended[0] != testSandboxBillingTenantID {
		t.Fatalf("suspended tenants = %#v, want the refused tenant parked so the reconciler skips it", stopper.suspended)
	}
	if len(stopper.resumed) != 0 {
		t.Fatalf("resumed tenants = %#v, want none while access is refused", stopper.resumed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSharedSandboxBillingAllowsStarterCredit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	expectManagedOrganization(mock, testSandboxBillingTenantID, "free")

	reporter := &fakeTenantSandboxUsageReporter{
		access: sharedSandboxBillingAccess{
			SandboxAllowed:               true,
			StarterCreditMicros:          5_000_000,
			StarterCreditRemainingMicros: 5_000_000,
		},
	}
	enabled, err := resolveSharedSandboxBilling(
		context.Background(), sqlx.NewDb(db, "sqlmock"), reporter, testSandboxBillingTenantID,
	)
	if err != nil {
		t.Fatalf("resolve billing: %v", err)
	}
	if !enabled {
		t.Fatal("Free tenant with starter credit should be enabled")
	}
	if len(reporter.reported) != 1 || reporter.reported[0] != testSandboxBillingTenantID {
		t.Fatalf("reported tenants = %#v", reporter.reported)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestResolveSharedSandboxBillingFailsClosedWhenMeterUnavailable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	expectManagedOrganization(mock, testSandboxBillingTenantID, "free")
	reporter := &fakeTenantSandboxUsageReporter{err: errors.New("billing unavailable")}
	enabled, err := resolveSharedSandboxBilling(
		context.Background(), sqlx.NewDb(db, "sqlmock"), reporter, testSandboxBillingTenantID,
	)
	if err == nil {
		t.Fatal("expected meter preflight error")
	}
	if enabled {
		t.Fatal("sandbox billing must fail closed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestResolveSharedSandboxBillingBlocksExhaustedStarterCredit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	expectManagedOrganization(mock, testSandboxBillingTenantID, "free")

	reporter := &fakeTenantSandboxUsageReporter{
		access: sharedSandboxBillingAccess{
			StarterCreditMicros:     5_000_000,
			StarterCreditUsedMicros: 5_000_000,
		},
	}
	enabled, err := resolveSharedSandboxBilling(
		context.Background(), sqlx.NewDb(db, "sqlmock"), reporter, testSandboxBillingTenantID,
	)
	if err != nil {
		t.Fatalf("resolve billing: %v", err)
	}
	if enabled {
		t.Fatal("exhausted starter credit must block new sandbox compute")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSharedSandboxBillingMetersEnterpriseContractWithoutStripe(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	expectManagedOrganization(mock, testSandboxBillingTenantID, "enterprise")
	reporter := &fakeTenantSandboxUsageReporter{
		access: sharedSandboxBillingAccess{SandboxAllowed: true},
	}

	enabled, err := resolveSharedSandboxBilling(
		context.Background(), sqlx.NewDb(db, "sqlmock"), reporter, testSandboxBillingTenantID,
	)
	if err != nil {
		t.Fatalf("resolve enterprise billing: %v", err)
	}
	if !enabled {
		t.Fatal("Enterprise contract should be enabled by central billing without a public Stripe meter")
	}
	if len(reporter.reported) != 1 || reporter.reported[0] != testSandboxBillingTenantID {
		t.Fatalf("reported tenants = %#v", reporter.reported)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestResolveSharedBrowserBillingDoesNotSpendSandboxStarterCredit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	expectManagedOrganization(mock, testSandboxBillingTenantID, "free")

	reporter := &fakeTenantSandboxUsageReporter{
		access: sharedSandboxBillingAccess{
			SandboxAllowed:               true,
			StarterCreditMicros:          5_000_000,
			StarterCreditRemainingMicros: 5_000_000,
		},
	}
	meter := &fakeBrowserUsageRecorder{}
	recorder := &sharedBrowserUsageRecorder{
		db:       sqlx.NewDb(db, "sqlmock"),
		reporter: reporter,
		meter:    meter,
	}
	err = recorder.Start(context.Background(), &browserpool.Lease{
		TenantID: testSandboxBillingTenantID,
	}, time.Now())
	if err == nil {
		t.Fatal("sandbox starter credit must not enable hosted browser billing")
	}
	if meter.started != 0 {
		t.Fatalf("browser usage recorder started %d times, want 0", meter.started)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestSandboxStopReasonNamesTheRefusingBranch pins the operator-facing reason.
// billingcredit.Resolve refuses compute from three different branches, and the
// old line reported all of them as starter-credit exhaustion. On eu-gra-1-prod
// that meant an organization with a canceled Stripe subscription logged
// "starter credit exhaustion ... micros=0 used_micros=0" 8,640 times a day:
// a credit that was never granted, described as spent.
func TestSandboxStopReasonNamesTheRefusingBranch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		access sharedSandboxBillingAccess
		want   string
	}{
		{
			name:   "lapsed subscription",
			access: sharedSandboxBillingAccess{BillingConfigured: true, BillingActive: false},
			want:   "subscription inactive",
		},
		{
			name:   "credit spent",
			access: sharedSandboxBillingAccess{StarterCreditMicros: 5000000, StarterCreditUsedMicros: 5000000},
			want:   "starter credit exhausted",
		},
		{
			name:   "never granted",
			access: sharedSandboxBillingAccess{},
			want:   "no active subscription or starter credit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxStopReason(tc.access); got != tc.want {
				t.Errorf("sandboxStopReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGroupTenantsByOrganizationCollapsesAliases covers the reporting side of
// the fan-out: one organization owning several metered tenant IDs must produce
// one usage snapshot, not one per alias, while still exposing every alias so
// enforcement can sweep them all.
func TestGroupTenantsByOrganizationCollapsesAliases(t *testing.T) {
	// billingDB is nil, so resolveBillingIdentity returns each tenant as its
	// own organization — the documented degraded path. Grouping must then
	// behave exactly as the old per-tenant loop did.
	reporter := &sharedSandboxBillingReporter{}
	groups := reporter.groupTenantsByOrganization(context.Background(), []string{"a", "b", "a"})
	if len(groups) != 2 {
		t.Fatalf("groups = %#v, want one per distinct tenant when resolution is unavailable", groups)
	}
	if groups[0].organizationID != "a" || len(groups[0].aliases) != 2 {
		t.Errorf("group[0] = %#v, want organization a with both of its entries", groups[0])
	}
	if groups[1].organizationID != "b" || len(groups[1].aliases) != 1 {
		t.Errorf("group[1] = %#v, want organization b with one entry", groups[1])
	}
	// Input order is preserved so log output and stop order stay deterministic.
	if groups[0].aliases[0] != "a" || groups[1].aliases[0] != "b" {
		t.Errorf("groups did not preserve input order: %#v", groups)
	}
}

// TestSharedSandboxBillingReporterResumesAgentsWhenAccessReturns covers the
// other half of the suspend handshake. Nothing but this loop clears the
// suspended status: the persistent-agent reconciler skips it by design, the
// message bus only auto-wakes "sleeping", and the WakeAgent RPC rejects
// anything that is not "sleeping". Without a resume here a tenant that starts
// paying again stays parked forever.
func TestSharedSandboxBillingReporterResumesAgentsWhenAccessReturns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT tenant_id[\s\S]*FROM \([\s\S]*sandbox_instances`).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(testSandboxBillingTenantID))
	mock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(int64(60), 5.0))
	mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"sandbox_allowed": true,
			"starter_credit_micros": 5000000,
			"starter_credit_used_micros": 1000000,
			"starter_credit_remaining_micros": 4000000
		}`))
	}))
	defer server.Close()

	stopper := &fakeTenantSandboxStopper{resumedN: 2}
	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(db, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
		stopper:    stopper,
	}
	if err := reporter.reportAll(context.Background()); err != nil {
		t.Fatalf("report all: %v", err)
	}
	if len(stopper.resumed) != 1 || stopper.resumed[0] != testSandboxBillingTenantID {
		t.Fatalf("resumed tenants = %#v, want the allowed tenant released", stopper.resumed)
	}
	if len(stopper.stopped) != 0 || len(stopper.suspended) != 0 {
		t.Fatalf("allowed tenant was enforced against: stopped=%#v suspended=%#v", stopper.stopped, stopper.suspended)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
