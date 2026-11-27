package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

const (
	testDeadTenantID = "c02f20ca-f97a-42c7-9b40-5590d2c0973d"
	testLiveTenantID = "67db3305-37d3-445e-a39f-66014449c2aa"
)

type countingBillingEndpoint struct {
	mu   sync.Mutex
	orgs []string
}

func (c *countingBillingEndpoint) posted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.orgs...)
}

func newCountingBillingEndpoint(t *testing.T) (*countingBillingEndpoint, *httptest.Server) {
	t.Helper()
	c := &countingBillingEndpoint{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap sharedSandboxUsageSnapshot
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			t.Errorf("decode request: %v", err)
		}
		c.mu.Lock()
		c.orgs = append(c.orgs, snap.OrganizationID)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"sandbox_allowed":true,"billing_active":true}`))
	}))
	t.Cleanup(server.Close)
	return c, server
}

func expectMeteredTenants(mock sqlmock.Sqlmock, tenants ...string) {
	rows := sqlmock.NewRows([]string{"tenant_id"})
	for _, tenant := range tenants {
		rows.AddRow(tenant)
	}
	mock.ExpectQuery(`SELECT tenant_id[\s\S]*FROM \([\s\S]*sandbox_instances`).WillReturnRows(rows)
}

func expectResolveOrganization(mock sqlmock.Sqlmock, tenantID, organizationID string) {
	mock.ExpectQuery(`SELECT o[.]id::text AS organization_id`).
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).AddRow(organizationID, "free"))
	mock.ExpectQuery(`SELECT [$]1::text AS tenant_id[\s\S]*FROM everstack[.]managed_instances`).
		WithArgs(organizationID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(organizationID))
}

func expectResolveOrganizationError(mock sqlmock.Sqlmock, tenantID string, err error) {
	mock.ExpectQuery(`SELECT o[.]id::text AS organization_id`).WithArgs(tenantID).WillReturnError(err)
}

func expectClosedUsage(mock sqlmock.Sqlmock, seconds int64, costUSD float64) {
	mock.ExpectQuery(`SELECT[\s\S]*COALESCE\(SUM\(duration_seconds\), 0\)[\s\S]*FROM sandbox_usage_records`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"compute_seconds", "cost_usd"}).AddRow(seconds, costUSD))
}

func expectConcurrentSandboxes(mock sqlmock.Sqlmock, n int64) {
	mock.ExpectQuery(`SELECT COUNT\(\*\)[\s\S]*FROM sandbox_instances`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(n))
}

// Observed on dev after the prepaid-credit handoff: two sandbox tenants that
// map to no organization (c02f20ca with $0 of usage, ea43a36b with ~$23.68)
// made every 30s tick end in a warning whose joined, multi-line error read
// like the whole report had failed. An unresolvable tenant is not a report
// failure: it is skipped (its unbilled cost is surfaced), every other tenant
// is still reported, and the dead tenant is not re-resolved on every tick.
func TestSharedSandboxReporterSkipsUnresolvableTenantAndCachesTheMiss(t *testing.T) {
	usageDB, usageMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()
	platformDB, platformMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer platformDB.Close()

	endpoint, server := newCountingBillingEndpoint(t)
	now := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)
	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(usageDB, "sqlmock"),
		billingDB:  sqlx.NewDb(platformDB, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
		now:        func() time.Time { return now },
	}

	// Tick 1: the dead tenant is resolved once, found to map to nothing, and
	// its own unbilled usage is read so the skip can say what it costs.
	expectMeteredTenants(usageMock, testDeadTenantID, testLiveTenantID)
	expectResolveOrganizationError(platformMock, testDeadTenantID, sql.ErrNoRows)
	expectClosedUsage(usageMock, 16, 0)
	expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
	expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
	expectClosedUsage(usageMock, 60, 1.0)
	expectConcurrentSandboxes(usageMock, 0)

	summary := reporter.reportTick(context.Background())
	if err := summary.err(); err != nil {
		t.Fatalf("tick 1: an unresolvable tenant must be skipped, not fail the tick: %v", err)
	}
	if summary.reported != 1 || summary.skipped != 1 || summary.failed != 0 {
		t.Fatalf("tick 1 summary = %+v, want reported=1 skipped=1 failed=0", summary)
	}
	if got := endpoint.posted(); len(got) != 1 || got[0] != testLiveTenantID {
		t.Fatalf("tick 1 posted organizations = %v, want only the live tenant", got)
	}

	// Tick 2, one interval later: the negative result is cached, so the dead
	// tenant costs no platform DB lookup and no log line.
	now = now.Add(30 * time.Second)
	expectMeteredTenants(usageMock, testDeadTenantID, testLiveTenantID)
	expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
	expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
	expectClosedUsage(usageMock, 60, 1.0)
	expectConcurrentSandboxes(usageMock, 0)

	summary = reporter.reportTick(context.Background())
	if err := summary.err(); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if summary.reported != 1 || summary.skipped != 1 || summary.failed != 0 {
		t.Fatalf("tick 2 summary = %+v, want reported=1 skipped=1 failed=0", summary)
	}

	// Tick 3, after the cache window: the tenant is re-checked, so one that
	// gains an organization mapping is picked up without a restart.
	now = now.Add(unresolvedTenantRecheckInterval)
	expectMeteredTenants(usageMock, testDeadTenantID)
	expectResolveOrganization(platformMock, testDeadTenantID, testLiveTenantID)
	expectResolveOrganization(platformMock, testDeadTenantID, testLiveTenantID)
	expectClosedUsage(usageMock, 76, 1.0)
	expectConcurrentSandboxes(usageMock, 0)

	summary = reporter.reportTick(context.Background())
	if err := summary.err(); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if summary.reported != 1 || summary.skipped != 0 {
		t.Fatalf("tick 3 summary = %+v, want the newly mapped tenant reported", summary)
	}

	if err := usageMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := platformMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if got := endpoint.posted(); len(got) != 3 {
		t.Fatalf("posted = %v, want one report per tick", got)
	}
}

// A lookup that fails for any other reason (the platform DB is down, a
// timeout) is a real failure: it is reported as failed, never cached as "maps
// to no organization", and retried on the next tick. The other tenants are
// still reported in the same tick.
func TestSharedSandboxReporterDoesNotCacheResolutionErrors(t *testing.T) {
	usageDB, usageMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()
	platformDB, platformMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer platformDB.Close()

	endpoint, server := newCountingBillingEndpoint(t)
	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(usageDB, "sqlmock"),
		billingDB:  sqlx.NewDb(platformDB, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
	}
	outage := errors.New("connection reset by peer")

	for tick := 1; tick <= 2; tick++ {
		expectMeteredTenants(usageMock, testDeadTenantID, testLiveTenantID)
		expectResolveOrganizationError(platformMock, testDeadTenantID, outage)
		expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
		expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
		expectClosedUsage(usageMock, 60, 1.0)
		expectConcurrentSandboxes(usageMock, 0)

		summary := reporter.reportTick(context.Background())
		if summary.reported != 1 || summary.skipped != 0 || summary.failed != 1 {
			t.Fatalf("tick %d summary = %+v, want reported=1 skipped=0 failed=1", tick, summary)
		}
		err := summary.err()
		if err == nil || !strings.Contains(err.Error(), testDeadTenantID) {
			t.Fatalf("tick %d error = %v, want the failing tenant named", tick, err)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Fatalf("tick %d error spans several lines; a second failure hides on a continuation line: %q", tick, err)
		}
	}
	if err := usageMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := platformMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if got := endpoint.posted(); len(got) != 2 {
		t.Fatalf("posted = %v, want the live tenant reported on both ticks", got)
	}
}

// A failed POST for one organization does not stop the next organization from
// being reported in the same tick.
func TestSharedSandboxReporterIsolatesEndpointFailures(t *testing.T) {
	usageDB, usageMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()

	var mu sync.Mutex
	var posted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap sharedSandboxUsageSnapshot
		_ = json.NewDecoder(r.Body).Decode(&snap)
		mu.Lock()
		posted = append(posted, snap.OrganizationID)
		mu.Unlock()
		if snap.OrganizationID == testDeadTenantID {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"sandbox_allowed":true}`))
	}))
	defer server.Close()

	// billingDB is nil: each tenant is its own organization.
	expectMeteredTenants(usageMock, testDeadTenantID, testLiveTenantID)
	expectClosedUsage(usageMock, 1, 0.1)
	expectConcurrentSandboxes(usageMock, 0)
	expectClosedUsage(usageMock, 1, 0.1)
	expectConcurrentSandboxes(usageMock, 0)

	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(usageDB, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
	}
	summary := reporter.reportTick(context.Background())
	if summary.reported != 1 || summary.failed != 1 {
		t.Fatalf("summary = %+v, want reported=1 failed=1", summary)
	}
	if len(posted) != 2 || posted[1] != testLiveTenantID {
		t.Fatalf("posted = %v, want the second tenant reported after the first failed", posted)
	}
	if err := usageMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Uses only the API that predates the fix, so it pins the behaviour change
// itself: on the old code reportAll returned the unresolvable tenant as an
// error every tick.
func TestSharedSandboxReporterUnresolvableTenantIsNotAReportFailure(t *testing.T) {
	usageDB, usageMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer usageDB.Close()
	platformDB, platformMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer platformDB.Close()
	usageMock.MatchExpectationsInOrder(false)
	platformMock.MatchExpectationsInOrder(false)

	endpoint, server := newCountingBillingEndpoint(t)
	expectMeteredTenants(usageMock, testDeadTenantID, testLiveTenantID)
	for i := 0; i < 2; i++ {
		// Up to twice each: the old code resolved while grouping and again
		// inside ReportTenant.
		expectResolveOrganizationError(platformMock, testDeadTenantID, sql.ErrNoRows)
		expectResolveOrganization(platformMock, testLiveTenantID, testLiveTenantID)
	}
	expectClosedUsage(usageMock, 16, 0)
	expectClosedUsage(usageMock, 60, 1.0)
	expectConcurrentSandboxes(usageMock, 0)

	reporter := &sharedSandboxBillingReporter{
		db:         sqlx.NewDb(usageDB, "sqlmock"),
		billingDB:  sqlx.NewDb(platformDB, "sqlmock"),
		live:       fakeSandboxLiveAccrual{},
		endpoint:   server.URL,
		instanceID: "shared-sandbox-plane:test",
		client:     server.Client(),
	}
	if err := reporter.reportAll(context.Background()); err != nil {
		t.Fatalf("reportAll() = %v, want nil: a tenant that maps to no organization is skipped, not failed", err)
	}
	if got := endpoint.posted(); len(got) != 1 || got[0] != testLiveTenantID {
		t.Fatalf("posted = %v, want the live tenant", got)
	}
}
