package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	defaultOrgResourceUsageInterval   = 5 * time.Minute
	orgResourceUsageRequestTimeout    = 10 * time.Second
	orgResourceUnattributedLogWindow  = time.Hour
	orgResourceUsageReporterLock      = int64(0x65767367617567) // "evsgaug"
	orgResourceUsageSourcePrefix      = "shared-resource-plane"
	orgResourceUsageSavepoint         = "org_resource_usage_org"
	orgResourceUsageEndpointSuffix    = "/api/billing/usage/resource-snapshot"
	orgResourceUsageSandboxURLSuffix  = "/api/billing/usage/sandbox-snapshot"
	orgResourceUsageSnapshotURLSuffix = "/api/billing/usage/snapshot"
)

// orgResourceGaugeNames are the plan-limit gauges this reporter files. Every
// one is written for every organization on every tick, zeros included:
// billing reads "this organization has a row for the gauge" as "the gauge is
// metered", so a missing row must mean "not measured", never "zero".
var orgResourceGaugeNames = []string{
	"AGENTS",
	"CONCURRENT_RUNNING",
	"DATASET_ITEMS",
	"EVAL_RUNS_MONTHLY",
	"ANNOTATION_QUEUES",
	"CHANNELS",
	"CHANNEL_BINDINGS",
	"STORAGE_BYTES",
}

// orgResourceGaugeChecks are the canonical plan-cap counts behind each
// gauge, taken from internal/enterprise so Billing shows exactly what the cap
// is enforced against. The tenant argument is ignored here: the reporter binds
// the organization's own alias set (billingidentity.ResolveLimitScope, the
// same scope enforcement uses) as $1.
func orgResourceGaugeChecks(now time.Time) map[string]enterprise.LimitCheck {
	return map[string]enterprise.LimitCheck{
		"AGENTS": enterprise.AgentsLimit(""),
		// No agent is being started, so none is excluded.
		"CONCURRENT_RUNNING": enterprise.ConcurrentRunningLimit("", ""),
		"DATASET_ITEMS":      enterprise.DatasetItemsLimit("", 1),
		"EVAL_RUNS_MONTHLY":  enterprise.EvalRunsMonthlyLimit("", now),
		"ANNOTATION_QUEUES":  enterprise.AnnotationQueuesLimit(""),
		"CHANNELS":           enterprise.ChannelsLimit(""),
		"CHANNEL_BINDINGS":   enterprise.ChannelBindingsLimit(""),
	}
}

// orgStorageBytesQuery has no plan-cap twin in enterprise yet; it binds the
// same alias set as text.
const orgStorageBytesQuery = `SELECT COALESCE(SUM(total_bytes), 0)::BIGINT FROM object_storage_usage
	WHERE tenant_id = ANY($1::text[])`

// orgResourceTenantsQuery lists every tenant that owns a counted row, so a
// tenant that maps to no organization is noticed rather than silently dropped.
const orgResourceTenantsQuery = `
	SELECT tenant_id::text FROM agent_definitions
	UNION SELECT tenant_id FROM dataset_items
	UNION SELECT tenant_id FROM eval_runs
	UNION SELECT tenant_id FROM annotation_queues
	UNION SELECT tenant_id::text FROM channel_configs
	UNION SELECT tenant_id::text FROM agent_channel_bindings
	UNION SELECT tenant_id::text FROM trooper_channel_bindings
	UNION SELECT tenant_id FROM object_storage_usage`

// orgResourceSnapshot is the body of POST /api/billing/usage/resource-snapshot.
type orgResourceSnapshot struct {
	OrganizationID string           `json:"organization_id"`
	InstanceID     string           `json:"instance_id"`
	Gauges         map[string]int64 `json:"gauges"`
}

// orgResourceUsageReporter files each organization's plan-limit resource
// counts (agents, datasets, channels, storage, ...) from the managed shared
// gateway to billing's usage snapshots, for display and for enforcement
// readiness. It charges nothing: billing stores these as usage_snapshots
// gauges, which no debit path reads.
//
// Attribution is by organization only. The shared gateway DB keys every row
// by a tenant id, and one organization owns several (its own id, each managed
// instance, legacy tenant_config instances). The reporter enumerates
// organizations from the platform DB, resolves each one's aliases, and counts
// rows whose tenant id is one of them. A tenant id claimed by two
// organizations is counted for neither; a tenant owning rows that maps to no
// organization is counted for nobody. Both are logged and reported as skipped.
//
// One replica reports per tick (gateway-DB advisory lock), as for inference
// usage: values are absolute and replace-on-write, so the lock only prevents
// two replicas landing out of order.
type orgResourceUsageReporter struct {
	gatewayDB *sqlx.DB
	billingDB *sqlx.DB
	endpoint  string
	sourceID  string
	interval  time.Duration
	client    *http.Client
	now       func() time.Time

	loggedMu sync.Mutex
	logged   map[string]time.Time
}

func newOrgResourceUsageReporter(gatewayDB, billingDB *sqlx.DB, endpoint string, client *http.Client) *orgResourceUsageReporter {
	if client == nil {
		client = &http.Client{Timeout: orgResourceUsageRequestTimeout}
	}
	return &orgResourceUsageReporter{
		gatewayDB: gatewayDB,
		billingDB: billingDB,
		endpoint:  strings.TrimSpace(endpoint),
		sourceID:  orgResourceUsageSourceID(),
		interval:  orgResourceUsageIntervalFromEnv(),
		client:    client,
		now:       time.Now,
		logged:    map[string]time.Time{},
	}
}

// orgResourceUsageSourceID is the billing instance id the gauges are filed
// under: stable across replicas and pods, distinct per regional plane, and
// distinct from the sandbox and inference sources (it carries no
// CONCURRENT_SANDBOXES and no flows, so it can never overwrite theirs).
func orgResourceUsageSourceID() string {
	if explicit := strings.TrimSpace(os.Getenv("EVS_RESOURCE_USAGE_SOURCE_ID")); explicit != "" {
		return explicit
	}
	region := strings.TrimSpace(os.Getenv("EVS_CONTROLPLANE_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("EVS_REGION"))
	}
	if region == "" {
		return orgResourceUsageSourcePrefix
	}
	return orgResourceUsageSourcePrefix + ":" + region
}

func orgResourceUsageIntervalFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("EVS_RESOURCE_USAGE_REPORT_INTERVAL"))
	if raw == "" {
		return defaultOrgResourceUsageInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		logger.WithFields("value", raw).Warn("usage billing: invalid resource usage report interval; using default")
		return defaultOrgResourceUsageInterval
	}
	return d
}

// managedResourceUsageURL is billing's resource gauge endpoint, the sibling
// of the configured sandbox heartbeat URL. EVS_RESOURCE_USAGE_URL overrides;
// any other shape yields "" (the reporter then says it is not configured).
func managedResourceUsageURL(defaults *EmbeddedDefaults) string {
	if explicit := strings.TrimSpace(os.Getenv("EVS_RESOURCE_USAGE_URL")); explicit != "" {
		return explicit
	}
	base := managedBillingUsageURL(defaults)
	for _, suffix := range []string{orgResourceUsageSandboxURLSuffix, orgResourceUsageSnapshotURLSuffix} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix) + orgResourceUsageEndpointSuffix
		}
	}
	if strings.HasSuffix(base, orgResourceUsageEndpointSuffix) {
		return base
	}
	return ""
}

// Run reports on every tick until ctx ends.
func (r *orgResourceUsageReporter) Run(ctx context.Context) {
	if r == nil || r.gatewayDB == nil || r.billingDB == nil || r.endpoint == "" {
		logger.Warn("usage billing: resource usage reporter is not configured")
		return
	}
	billingmeter.UsageReporterStarted("resource", r.now())
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		_, _, _ = r.reportOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// orgAliasSet is one organization and the tenant ids its rows may carry.
type orgAliasSet struct {
	organizationID string
	aliases        []string
}

// loadOrganizationAliases returns every organization with the tenant ids its
// plan caps are counted over: billingidentity.ResolveLimitScope, exactly what
// enforcement uses. That excludes deleted managed instances (their objects no
// longer count) and any id another organization also claims (counted for
// neither). owner maps each counted alias to its organization; an
// organization whose own id is unresolvable is skipped and counted in skipped.
func (r *orgResourceUsageReporter) loadOrganizationAliases(ctx context.Context) (orgs []orgAliasSet, owner map[string]string, skipped int, err error) {
	ids, err := billingidentity.ListOrganizationIDs(ctx, r.billingDB)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("list organizations: %w", err)
	}
	owner = make(map[string]string)
	orgs = make([]orgAliasSet, 0, len(ids))
	for _, id := range ids {
		scope, err := billingidentity.ResolveLimitScope(ctx, r.billingDB, id)
		switch {
		case err == nil:
		case errors.Is(err, billingidentity.ErrNoOrganization), errors.Is(err, billingidentity.ErrContestedTenant):
			skipped++
			r.logWindowed("org:"+id, func() {
				logger.WithFields("organization_id", id, "error", err.Error()).
					Error("usage billing: organization has no unambiguous counting scope; its resources are reported for none")
			})
			continue
		default:
			return nil, nil, 0, fmt.Errorf("resolve counting scope of organization %s: %w", id, err)
		}
		for _, alias := range scope.Aliases {
			owner[alias] = id
		}
		orgs = append(orgs, orgAliasSet{organizationID: id, aliases: scope.Aliases})
	}
	return orgs, owner, skipped, nil
}

// reportOnce runs one tick. led is false when another replica holds the lock.
func (r *orgResourceUsageReporter) reportOnce(ctx context.Context) (led bool, summary sharedUsageReportSummary, err error) {
	tx, err := r.gatewayDB.BeginTxx(ctx, nil)
	if err != nil {
		return false, summary, fmt.Errorf("begin reporter transaction: %w", err)
	}
	// Read-only: rollback releases the transaction-scoped advisory lock.
	defer func() { _ = tx.Rollback() }()
	if err := tx.GetContext(ctx, &led, `SELECT pg_try_advisory_xact_lock($1)`, orgResourceUsageReporterLock); err != nil {
		return false, summary, fmt.Errorf("acquire reporter lock: %w", err)
	}
	if !led {
		return false, summary, nil
	}

	orgs, owner, skippedOrgs, err := r.loadOrganizationAliases(ctx)
	if err != nil {
		return true, summary, err
	}
	summary.skipped += skippedOrgs
	summary.skipped += r.noteUnattributedTenants(ctx, tx, owner, &summary)

	now := r.now().UTC()
	for _, org := range orgs {
		if err := r.reportOrg(ctx, tx, org, now); err != nil {
			billingmeter.UsageReportFailed()
			summary.failed++
			summary.errs = append(summary.errs, fmt.Errorf("organization %s: %w", org.organizationID, err))
			continue
		}
		summary.reported++
	}
	billingmeter.SharedUsageReportTick("resource", summary.reported, summary.skipped, summary.failed)
	fields := []interface{}{"reported", summary.reported, "skipped", summary.skipped, "failed", summary.failed}
	if err := summary.err(); err != nil {
		if ctx.Err() == nil {
			logger.WithFields(append(fields, "error", err.Error())...).Warn("usage billing: resource usage report tick")
		}
		return true, summary, err
	}
	// Only the leader advances the stall clock: a follower that skipped the
	// tick says nothing about whether the gauges reached billing.
	billingmeter.UsageReportSucceeded("resource", now)
	logger.WithFields(fields...).Debug("usage billing: resource usage report tick")
	return true, summary, nil
}

// noteUnattributedTenants finds tenants owning counted rows that belong to no
// organization (contested ones are already counted by the caller) and logs
// each once per window. It returns how many there are.
func (r *orgResourceUsageReporter) noteUnattributedTenants(ctx context.Context, tx *sqlx.Tx, owner map[string]string, summary *sharedUsageReportSummary) int {
	var tenants []string
	if err := r.inSavepoint(ctx, tx, func() error {
		return tx.SelectContext(ctx, &tenants, orgResourceTenantsQuery)
	}); err != nil {
		summary.errs = append(summary.errs, fmt.Errorf("list tenants with resources: %w", err))
		return 0
	}
	n := 0
	for _, tenant := range tenants {
		key := strings.ToLower(strings.TrimSpace(tenant))
		if _, ok := owner[key]; ok || key == "" {
			continue
		}
		n++
		r.logWindowed("orphan:"+key, func() {
			logger.WithFields("tenant_id", key).
				Warn("usage billing: tenant owns resources but is no live alias of exactly one organization (unknown, claimed twice, or a deleted instance); its resources are reported for none")
		})
	}
	return n
}

func (r *orgResourceUsageReporter) reportOrg(ctx context.Context, tx *sqlx.Tx, org orgAliasSet, now time.Time) error {
	if _, err := uuid.Parse(org.organizationID); err != nil {
		return fmt.Errorf("organization id is not a UUID")
	}
	scope := enterprise.CountScope{Aliases: org.aliases}
	gauges := make(map[string]int64, len(orgResourceGaugeNames))
	if err := r.inSavepoint(ctx, tx, func() error {
		for name, check := range orgResourceGaugeChecks(now) {
			var n int64
			args := append([]interface{}{scope.Arg(check.Column)}, check.Args...)
			if err := tx.GetContext(ctx, &n, check.Query, args...); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			gauges[name] = n
		}
		var storage int64
		if err := tx.GetContext(ctx, &storage, orgStorageBytesQuery, scope.Arg(enterprise.TenantText)); err != nil {
			return fmt.Errorf("STORAGE_BYTES: %w", err)
		}
		gauges["STORAGE_BYTES"] = storage
		return nil
	}); err != nil {
		return fmt.Errorf("count resources: %w", err)
	}
	return r.post(ctx, orgResourceSnapshot{
		OrganizationID: org.organizationID,
		InstanceID:     r.sourceID,
		Gauges:         gauges,
	})
}

// startsUsageReporter says whether this gateway process runs the periodic
// usage reporter for a billing plane ("inference", "resource", "sandbox";
// the sandbox reporter also carries browser runtime). Only a standalone
// managed gateway does: its primary pool is the serving plane's gateway DB.
// The services binary embeds a shared-mode gateway whose primary pool is the
// platform DB. Reporting from there counts the wrong tables, and because its
// advisory locks live in another database it does not exclude the real
// reporter: both would file the same plane source id and overwrite each
// other's totals (prod 2026-10-06: zeros over every resource count).
func startsUsageReporter(reporter string, managedGateway, sharedMode bool) bool {
	_ = reporter // one rule for every plane; named for the call sites and tests
	return managedGateway && !sharedMode
}

// sharedSandboxBillingURL is the endpoint this process posts sandbox compute
// and browser runtime to, for both the periodic tick and allocation-time
// admission. It is empty in the services-embedded gateway, which sees none of
// that usage: an empty endpoint makes ReportTenant refuse, so an allocation
// there fails closed instead of filing a second writer for the plane source.
func sharedSandboxBillingURL(url string, managedGateway, sharedMode bool) string {
	if !startsUsageReporter("sandbox", managedGateway, sharedMode) {
		return ""
	}
	return url
}

// inSavepoint runs fn inside a savepoint: every organization shares the
// transaction holding the lock, and one failed statement would otherwise
// abort the reads of every organization after it.
func (r *orgResourceUsageReporter) inSavepoint(ctx context.Context, tx *sqlx.Tx, fn func() error) error {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT `+orgResourceUsageSavepoint); err != nil {
		return fmt.Errorf("open savepoint: %w", err)
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT `+orgResourceUsageSavepoint); rbErr != nil {
			return fmt.Errorf("%w (and roll back savepoint: %v)", err, rbErr)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+orgResourceUsageSavepoint); err != nil {
		return fmt.Errorf("release savepoint: %w", err)
	}
	return nil
}

func (r *orgResourceUsageReporter) post(ctx context.Context, snap orgResourceSnapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, orgResourceUsageRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("billing resource snapshot returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// logWindowed runs log at most once per orgResourceUnattributedLogWindow per key.
func (r *orgResourceUsageReporter) logWindowed(key string, log func()) {
	now := r.now()
	r.loggedMu.Lock()
	if r.logged == nil {
		r.logged = map[string]time.Time{}
	}
	last, seen := r.logged[key]
	if seen && now.Sub(last) < orgResourceUnattributedLogWindow {
		r.loggedMu.Unlock()
		return
	}
	r.logged[key] = now
	r.loggedMu.Unlock()
	log()
}

// startOrgResourceUsageReporter starts the reporter when the billing endpoint,
// the platform DB and the gateway's M2M client are configured, and says why
// not otherwise.
func startOrgResourceUsageReporter(ctx context.Context, gatewayDB, billingDB *sqlx.DB, endpoint string) {
	if strings.TrimSpace(endpoint) == "" {
		logger.Warn("usage billing: internal endpoint missing; resource counts will not reach billing")
		return
	}
	if billingDB == nil {
		logger.Warn("usage billing: platform database missing; resource counts cannot be attributed to organizations")
		return
	}
	client, err := newManagedBillingHTTPClient()
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("usage billing: M2M client unavailable; resource counts will not reach billing")
		return
	}
	r := newOrgResourceUsageReporter(gatewayDB, billingDB, endpoint, client)
	go r.Run(ctx)
	logger.WithFields("endpoint", endpoint, "source_id", r.sourceID, "interval", r.interval.String()).
		Info("usage billing: resource usage reporter started")
}
