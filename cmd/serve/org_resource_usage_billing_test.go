package serve

// Real-Postgres tests for the organization resource gauge reporter. Skips
// unless EVS_TEST_PG_DSN points at a Postgres the test may create databases
// on (for example postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/enterprise"
)

func resourceScratchDB(t *testing.T, label string) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres resource gauge reporter test")
	}
	admin, err := sqlx.Connect("pgx", dsn)
	require.NoError(t, err)
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	name := "metering_p0_3_" + label + "_" + hex.EncodeToString(suffix[:])
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
	return db
}

// The gateway tables with the columns the reporter reads, typed as on the
// production gateway DB (information_schema, 2026-10-05): agent, channel and
// binding tables key tenants by UUID, the dataset, eval, annotation and
// storage tables by VARCHAR.
const resourceGatewaySchema = `
CREATE TABLE agent_definitions (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL,
	lifecycle_status VARCHAR(50), deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE dataset_items (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL);
CREATE TABLE eval_runs (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE annotation_queues (id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text, tenant_id VARCHAR(255) NOT NULL);
CREATE TABLE channel_configs (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
CREATE TABLE agent_channel_bindings (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
CREATE TABLE trooper_channel_bindings (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL);
CREATE TABLE object_storage_usage (tenant_id VARCHAR(255) PRIMARY KEY, total_bytes BIGINT NOT NULL DEFAULT 0);
`

const resourcePlatformSchema = `
CREATE SCHEMA everstack;
CREATE TABLE everstack.organizations (id UUID PRIMARY KEY, plan_tier VARCHAR(50) NOT NULL DEFAULT 'free');
CREATE TABLE everstack.managed_instances (id UUID PRIMARY KEY, organization_id UUID NOT NULL, deleted_at TIMESTAMPTZ);
CREATE TABLE everstack.tenant_config (instance_id UUID PRIMARY KEY, organization_id UUID NOT NULL);
`

const (
	resOrgA      = "aaaaaaaa-0000-4000-8000-00000000000a"
	resInstA     = "aaaaaaaa-0000-4000-8000-0000000000a1" // second alias of A (managed instance)
	resOrgB      = "bbbbbbbb-0000-4000-8000-00000000000b"
	resTenantB   = "bbbbbbbb-0000-4000-8000-0000000000b1" // B's only usage alias (tenant_config)
	resOrgC      = "cccccccc-0000-4000-8000-00000000000c" // owns nothing: all zeros
	resOrphan    = "dddddddd-0000-4000-8000-00000000000d" // maps to no organization
	resContested = "eeeeeeee-0000-4000-8000-00000000000e" // claimed by A and B
)

// seedTenantResources gives one tenant a distinct number of rows in every
// counted table, scaled by k, so any cross-attribution shows up as a wrong sum.
func seedTenantResources(t *testing.T, db *sqlx.DB, tenant string, k int, monthStart time.Time) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.Exec(q, args...)
		require.NoError(t, err)
	}
	for i := 0; i < k; i++ {
		exec(`INSERT INTO agent_definitions (tenant_id) VALUES ($1)`, tenant)
		exec(`INSERT INTO dataset_items (tenant_id) VALUES ($1), ($1)`, tenant)
		exec(`INSERT INTO eval_runs (tenant_id, created_at) VALUES ($1, $2)`, tenant, monthStart.Add(time.Hour))
		exec(`INSERT INTO annotation_queues (tenant_id) VALUES ($1)`, tenant)
		exec(`INSERT INTO channel_configs (tenant_id) VALUES ($1)`, tenant)
		exec(`INSERT INTO agent_channel_bindings (tenant_id) VALUES ($1)`, tenant)
	}
	// Rows that must not count: a deleted agent, an eval run from last month.
	exec(`INSERT INTO agent_definitions (tenant_id, deleted_at, lifecycle_status) VALUES ($1, NOW(), 'running')`, tenant)
	exec(`INSERT INTO eval_runs (tenant_id, created_at) VALUES ($1, $2)`, tenant, monthStart.Add(-time.Hour))
	// One running agent per tenant.
	exec(`INSERT INTO agent_definitions (tenant_id, lifecycle_status) VALUES ($1, 'running')`, tenant)
	// A legacy trooper binding, plus one already copied into agent bindings
	// (same id): the copy must be counted once.
	exec(`INSERT INTO trooper_channel_bindings (tenant_id) VALUES ($1)`, tenant)
	exec(`WITH b AS (INSERT INTO agent_channel_bindings (tenant_id) VALUES ($1) RETURNING id, tenant_id)
		INSERT INTO trooper_channel_bindings (id, tenant_id) SELECT id, tenant_id FROM b`, tenant)
	exec(`INSERT INTO object_storage_usage (tenant_id, total_bytes) VALUES ($1, $2)`, tenant, int64(k)*1000)
}

// expectedGauges is what seedTenantResources(k) contributes per tenant.
func expectedGauges(k int64) map[string]int64 {
	return map[string]int64{
		"AGENTS":             k + 1,
		"CONCURRENT_RUNNING": 1,
		"DATASET_ITEMS":      2 * k,
		"EVAL_RUNS_MONTHLY":  k,
		"ANNOTATION_QUEUES":  k,
		"CHANNELS":           k,
		"CHANNEL_BINDINGS":   k + 2,
		"STORAGE_BYTES":      1000 * k,
	}
}

func addGauges(sets ...map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for _, name := range orgResourceGaugeNames {
		out[name] = 0
	}
	for _, s := range sets {
		for k, v := range s {
			out[k] += v
		}
	}
	return out
}

type capturedResourceSnapshots struct {
	mu    sync.Mutex
	snaps []orgResourceSnapshot
}

func (c *capturedResourceSnapshots) byOrg() map[string]orgResourceSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]orgResourceSnapshot{}
	for _, s := range c.snaps {
		out[s.OrganizationID] = s
	}
	return out
}

func (c *capturedResourceSnapshots) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snaps)
}

func newResourceSnapshotServer(t *testing.T) (*capturedResourceSnapshots, *httptest.Server) {
	t.Helper()
	captured := &capturedResourceSnapshots{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snap orgResourceSnapshot
		if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
			t.Errorf("decode snapshot: %v", err)
		}
		captured.mu.Lock()
		captured.snaps = append(captured.snaps, snap)
		captured.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return captured, srv
}

// Tenant isolation: organization A owns two tenant aliases (its own id and a
// managed instance), B owns one, C owns none. Each organization's gauges are
// the sum over exactly its own aliases; a tenant that maps to no organization
// and a tenant claimed by two organizations are counted for nobody; and every
// organization gets all eight gauges, zeros included, under one plane source.
func TestPGOrgResourceReporterAttributesCountsByOrganization(t *testing.T) {
	gw := resourceScratchDB(t, "gw")
	platform := resourceScratchDB(t, "platform")
	_, err := gw.Exec(resourceGatewaySchema)
	require.NoError(t, err)
	_, err = platform.Exec(resourcePlatformSchema)
	require.NoError(t, err)

	_, err = platform.Exec(`INSERT INTO everstack.organizations (id) VALUES ($1), ($2), ($3)`, resOrgA, resOrgB, resOrgC)
	require.NoError(t, err)
	_, err = platform.Exec(`INSERT INTO everstack.managed_instances (id, organization_id) VALUES ($1, $2), ($3, $2)`,
		resInstA, resOrgA, resContested)
	require.NoError(t, err)
	_, err = platform.Exec(`INSERT INTO everstack.tenant_config (instance_id, organization_id) VALUES ($1, $2), ($3, $2)`,
		resTenantB, resOrgB, resContested)
	require.NoError(t, err)

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	seedTenantResources(t, gw, resOrgA, 1, monthStart)
	seedTenantResources(t, gw, resInstA, 3, monthStart)
	seedTenantResources(t, gw, resTenantB, 5, monthStart)
	seedTenantResources(t, gw, resOrphan, 7, monthStart)
	seedTenantResources(t, gw, resContested, 11, monthStart)

	captured, srv := newResourceSnapshotServer(t)
	r := newOrgResourceUsageReporter(gw, platform, srv.URL, srv.Client())
	r.sourceID = "shared-resource-plane:test"
	r.now = func() time.Time { return now }

	led, summary, err := r.reportOnce(context.Background())
	require.NoError(t, err)
	require.True(t, led)
	require.Equal(t, 3, summary.reported, "one report per organization")
	require.Equal(t, 0, summary.failed)
	require.Equal(t, 2, summary.skipped, "the orphan and the contested tenant are skipped")

	got := captured.byOrg()
	require.Len(t, got, 3)
	want := map[string]map[string]int64{
		resOrgA: addGauges(expectedGauges(1), expectedGauges(3)),
		resOrgB: addGauges(expectedGauges(5)),
		resOrgC: addGauges(),
	}
	for org, gauges := range want {
		snap, ok := got[org]
		require.Truef(t, ok, "organization %s was not reported", org)
		require.Equal(t, "shared-resource-plane:test", snap.InstanceID)
		require.Equalf(t, gauges, snap.Gauges, "organization %s gauges", org)
		names := make([]string, 0, len(snap.Gauges))
		for name := range snap.Gauges {
			names = append(names, name)
		}
		sort.Strings(names)
		want := append([]string(nil), orgResourceGaugeNames...)
		sort.Strings(want)
		require.Equal(t, want, names, "every gauge is written, zeros included")
	}
	for _, snap := range got {
		require.NotEqual(t, resOrphan, snap.OrganizationID)
		require.NotEqual(t, resContested, snap.OrganizationID)
	}
}

// Two replicas tick together: only the replica holding the advisory lock
// reports; the other sends nothing.
func TestPGOrgResourceReporterElectsOneReplica(t *testing.T) {
	gw := resourceScratchDB(t, "gw")
	platform := resourceScratchDB(t, "platform")
	_, err := gw.Exec(resourceGatewaySchema)
	require.NoError(t, err)
	_, err = platform.Exec(resourcePlatformSchema)
	require.NoError(t, err)
	_, err = platform.Exec(`INSERT INTO everstack.organizations (id) VALUES ($1)`, resOrgA)
	require.NoError(t, err)

	holder, err := gw.Beginx()
	require.NoError(t, err)
	defer holder.Rollback()
	var held bool
	require.NoError(t, holder.Get(&held, `SELECT pg_try_advisory_xact_lock($1)`, orgResourceUsageReporterLock))
	require.True(t, held)

	captured, srv := newResourceSnapshotServer(t)
	r := newOrgResourceUsageReporter(gw, platform, srv.URL, srv.Client())
	led, _, err := r.reportOnce(context.Background())
	require.NoError(t, err)
	require.False(t, led)
	require.Equal(t, 0, captured.count())

	require.NoError(t, holder.Rollback())
	led, summary, err := r.reportOnce(context.Background())
	require.NoError(t, err)
	require.True(t, led)
	require.Equal(t, 1, summary.reported)
	require.Equal(t, 1, captured.count())
}

func TestManagedResourceUsageURLIsTheSnapshotSibling(t *testing.T) {
	t.Setenv("EVS_RESOURCE_USAGE_URL", "")
	t.Setenv("EVS_BILLING_USAGE_URL", "http://billing:8094/api/billing/usage/sandbox-snapshot")
	require.Equal(t, "http://billing:8094/api/billing/usage/resource-snapshot", managedResourceUsageURL(nil))
	t.Setenv("EVS_BILLING_USAGE_URL", "http://billing:8094/elsewhere")
	require.Equal(t, "", managedResourceUsageURL(nil))
	t.Setenv("EVS_RESOURCE_USAGE_URL", "http://override/x")
	require.Equal(t, "http://override/x", managedResourceUsageURL(nil))
}

// Billing shows these gauges next to the plan caps, so the reporter must
// count exactly what enforcement counts: the same canonical queries over the
// same organization scope (live, uncontested aliases; a deleted managed
// instance's objects no longer count). Prod 2026-10-06 showed the display and
// the caps disagreeing.
func TestPGOrgResourceReporterCountsWhatEnforcementCounts(t *testing.T) {
	gw := resourceScratchDB(t, "gw")
	platform := resourceScratchDB(t, "platform")
	_, err := gw.Exec(resourceGatewaySchema)
	require.NoError(t, err)
	_, err = platform.Exec(resourcePlatformSchema)
	require.NoError(t, err)

	const deletedInstA = "aaaaaaaa-0000-4000-8000-0000000000a2"
	_, err = platform.Exec(`INSERT INTO everstack.organizations (id) VALUES ($1)`, resOrgA)
	require.NoError(t, err)
	_, err = platform.Exec(`INSERT INTO everstack.managed_instances (id, organization_id, deleted_at)
		VALUES ($1, $3, NULL), ($2, $3, NOW())`, resInstA, deletedInstA, resOrgA)
	require.NoError(t, err)

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	monthStart := enterprise.MonthStartUTC(now)
	seedTenantResources(t, gw, resOrgA, 2, monthStart)
	seedTenantResources(t, gw, resInstA, 3, monthStart)
	seedTenantResources(t, gw, deletedInstA, 5, monthStart)

	captured, srv := newResourceSnapshotServer(t)
	r := newOrgResourceUsageReporter(gw, platform, srv.URL, srv.Client())
	r.now = func() time.Time { return now }
	_, _, err = r.reportOnce(context.Background())
	require.NoError(t, err)
	snap, ok := captured.byOrg()[resOrgA]
	require.True(t, ok, "organization A was not reported")

	scope, err := billingidentity.ResolveLimitScope(context.Background(), platform, resOrgA)
	require.NoError(t, err)
	cs := enterprise.CountScope{Aliases: scope.Aliases}
	enforced := func(c enterprise.LimitCheck) int64 {
		t.Helper()
		var n int64
		require.NoError(t, gw.Get(&n, c.Query, append([]interface{}{cs.Arg(c.Column)}, c.Args...)...))
		return n
	}
	want := map[string]int64{
		"AGENTS":             enforced(enterprise.AgentsLimit(resOrgA)),
		"CONCURRENT_RUNNING": enforced(enterprise.ConcurrentRunningLimit(resOrgA, "")),
		"DATASET_ITEMS":      enforced(enterprise.DatasetItemsLimit(resOrgA, 1)),
		"EVAL_RUNS_MONTHLY":  enforced(enterprise.EvalRunsMonthlyLimit(resOrgA, now)),
		"ANNOTATION_QUEUES":  enforced(enterprise.AnnotationQueuesLimit(resOrgA)),
		"CHANNELS":           enforced(enterprise.ChannelsLimit(resOrgA)),
		"CHANNEL_BINDINGS":   enforced(enterprise.ChannelBindingsLimit(resOrgA)),
	}
	for name, n := range want {
		require.Equalf(t, n, snap.Gauges[name], "%s: reported %d, enforcement counts %d", name, snap.Gauges[name], n)
	}
	// And those are A's live aliases only: the deleted instance's 5x rows are out.
	require.Equal(t, addGauges(expectedGauges(2), expectedGauges(3)), snap.Gauges)
}

// The services binary embeds a gateway in shared mode whose primary pool is
// the platform DB, not the serving plane's gateway DB. Its tables there are
// empty, and because its advisory locks live in a different database it also
// "won" every tick, filing zeros over the real resource counts under the same
// source id (prod 2026-10-06). The inference and sandbox/browser reporters
// share the hazard: two processes filing one cumulative source would fight.
// Only the standalone managed gateway runs usage reporters.
func TestEmbeddedSharedGatewayStartsNoUsageReporters(t *testing.T) {
	for _, reporter := range []string{"inference", "resource", "sandbox"} {
		require.Truef(t, startsUsageReporter(reporter, true, false), "standalone managed gateway runs the %s reporter", reporter)
		require.Falsef(t, startsUsageReporter(reporter, true, true), "services-embedded shared gateway must not run the %s reporter", reporter)
		require.Falsef(t, startsUsageReporter(reporter, false, false), "self-hosted gateway runs no %s plane reporter", reporter)
	}
}

// Ownership of each billing plane meter, pinned. Every row the meters read
// lives in the standalone gateway's DB (prod 2026-10-06: 5,109
// sandbox_usage_records and 33 sandbox_instances on postgres-gateway-0, none
// in the platform DB or any inst_* schema; the last watermark advance,
// 2026-10-01 19:35:15, follows the gateway DB's last record at 19:35:12). So
// the standalone managed gateway is the one process that reports them, and
// the services-embedded gateway files nothing under any plane source: not
// on a tick, and not at allocation time, where it now fails closed (an
// allocation it cannot meter is refused, as when the endpoint is missing).
func TestStandaloneGatewayOwnsEveryUsagePlaneSource(t *testing.T) {
	const url = "http://billing/api/billing/usage/sandbox-snapshot"
	owners := map[string]string{
		"inference": "standalone",
		"resource":  "standalone",
		"sandbox":   "standalone", // sandbox compute and browser runtime
	}
	for reporter, owner := range owners {
		require.Equal(t, "standalone", owner)
		require.Truef(t, startsUsageReporter(reporter, true, false), "%s is reported by the standalone gateway", reporter)
		require.Falsef(t, startsUsageReporter(reporter, true, true), "%s is not reported by the embedded gateway", reporter)
	}
	require.Equal(t, url, sharedSandboxBillingURL(url, true, false), "standalone gateway posts sandbox and browser usage")
	require.Equal(t, "", sharedSandboxBillingURL(url, true, true), "embedded gateway never posts under the sandbox plane source")
}
