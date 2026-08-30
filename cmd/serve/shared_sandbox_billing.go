package serve

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/m2m"
	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	servicescfg "github.com/everstacklabs/everstack/internal/services/config"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const (
	defaultSharedSandboxBillingInterval = 30 * time.Second
	sharedSandboxBillingRequestTimeout  = 10 * time.Second
)

// sandboxLiveAccrual is the portion of SandboxManager needed by the shared
// billing reporter. Keeping the seam narrow makes the billable total testable
// without provisioning a sandbox backend.
type sandboxLiveAccrual interface {
	LiveAccruedCost(ctx context.Context, tenantID string) (costUSD float64, computeSeconds int64)
}

type tenantSandboxUsageReporter interface {
	ReportTenant(ctx context.Context, tenantID string) (sharedSandboxBillingAccess, error)
}

type tenantSandboxStopper interface {
	StopTenantSandboxes(ctx context.Context, tenantID string) error
}

type tenantBrowserUsageTotals interface {
	TotalsForTenant(context.Context, string, time.Time) (browserpool.UsageTotals, error)
}

// sharedSandboxBillingReporter forwards one lifetime-monotonic source meter
// per tenant from the managed shared gateway to central billing. Central
// billing owns billing-period assignment, durable watermarks, and the Stripe
// outbox. A stable source ID lets every gateway replica safely report the same
// value; duplicate and out-of-order reports are no-ops.
type sharedSandboxBillingReporter struct {
	db         *sqlx.DB
	billingDB  *sqlx.DB
	live       sandboxLiveAccrual
	browser    tenantBrowserUsageTotals
	endpoint   string
	instanceID string
	interval   time.Duration
	client     *http.Client
	stopper    tenantSandboxStopper
}

type sharedSandboxUsageSnapshot struct {
	OrganizationID                     string `json:"organization_id"`
	InstanceID                         string `json:"instance_id"`
	ConcurrentSandboxesCount           int64  `json:"concurrent_sandboxes_count"`
	CumulativeSandboxComputeSeconds    int64  `json:"cumulative_sandbox_compute_seconds"`
	CumulativeSandboxComputeCostMicros int64  `json:"cumulative_sandbox_compute_cost_micros"`
	CumulativeBrowserRuntimeSeconds    int64  `json:"cumulative_browser_runtime_seconds"`
	CumulativeBrowserRuntimeCostMicros int64  `json:"cumulative_browser_runtime_cost_micros"`
}

type sharedSandboxBillingAccess struct {
	SandboxAllowed               bool
	BillingConfigured            bool
	BillingActive                bool
	StarterCreditMicros          int64
	StarterCreditUsedMicros      int64
	StarterCreditRemainingMicros int64
}

type sharedSandboxBillingResponse struct {
	Success                      bool  `json:"success"`
	SandboxAllowed               *bool `json:"sandbox_allowed"`
	BillingConfigured            bool  `json:"billing_configured"`
	BillingActive                bool  `json:"billing_active"`
	StarterCreditMicros          int64 `json:"starter_credit_micros"`
	StarterCreditUsedMicros      int64 `json:"starter_credit_used_micros"`
	StarterCreditRemainingMicros int64 `json:"starter_credit_remaining_micros"`
}

func newSharedSandboxBillingReporter(db, billingDB *sqlx.DB, live sandboxLiveAccrual, endpoint string) *sharedSandboxBillingReporter {
	reporter := &sharedSandboxBillingReporter{
		db:         db,
		billingDB:  billingDB,
		live:       live,
		endpoint:   strings.TrimSpace(endpoint),
		instanceID: sharedSandboxBillingSourceID(),
		interval:   sharedSandboxBillingIntervalFromEnv(),
		client:     &http.Client{Timeout: sharedSandboxBillingRequestTimeout},
	}
	if stopper, ok := live.(tenantSandboxStopper); ok {
		reporter.stopper = stopper
	}
	return reporter
}

func (r *sharedSandboxBillingReporter) SetBrowserUsage(meter tenantBrowserUsageTotals) {
	if r != nil {
		r.browser = meter
	}
}

func newManagedBillingHTTPClient() (*http.Client, error) {
	svcCfg, err := servicescfg.Load("")
	if err != nil {
		return nil, fmt.Errorf("load M2M configuration: %w", err)
	}
	if svcCfg == nil || !svcCfg.Security.M2M.Enabled {
		return nil, fmt.Errorf("M2M authentication is not enabled")
	}
	raw := svcCfg.Security.M2M.ToM2MConfig()
	if raw == nil {
		return nil, fmt.Errorf("M2M authentication configuration is empty")
	}
	authConfig, err := m2m.ConfigFromServices(&m2m.ServicesM2MConfig{
		Enabled:         true,
		Provider:        raw.Provider,
		SimpleConfig:    convertSimpleConfig(raw.SimpleConfig),
		OIDCConfig:      convertOIDCConfig(raw.OIDCConfig),
		Clients:         convertClients(raw.Clients),
		OIDCClients:     convertClients(raw.OIDCClients),
		PublicEndpoints: raw.PublicEndpoints,
		EndpointScopes:  raw.EndpointScopes,
		ScopePolicy:     convertScopePolicy(raw.ScopePolicy),
	})
	if err != nil {
		return nil, fmt.Errorf("convert M2M configuration: %w", err)
	}
	provider, err := m2m.NewTokenProvider(authConfig, "gateway")
	if err != nil {
		return nil, fmt.Errorf("create gateway M2M provider: %w", err)
	}
	return m2m.NewHTTPClient(provider, sharedSandboxBillingRequestTimeout), nil
}

func sharedSandboxBillingSourceID() string {
	if explicit := strings.TrimSpace(os.Getenv("EVS_SHARED_SANDBOX_BILLING_SOURCE_ID")); explicit != "" {
		return explicit
	}

	// The source ID must be stable across replicas but distinct across regional
	// planes. Never use a pod name: each new pod would create a new watermark
	// and could cause the same shared-DB total to be billed twice.
	region := strings.TrimSpace(os.Getenv("EVS_CONTROLPLANE_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("EVS_REGION"))
	}
	if region == "" {
		return "shared-sandbox-plane"
	}
	return "shared-sandbox-plane:" + region
}

func sharedSandboxBillingIntervalFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("EVS_SHARED_SANDBOX_BILLING_SYNC_INTERVAL"))
	if raw == "" {
		return defaultSharedSandboxBillingInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		logger.WithFields("value", raw).Warn("sandbox billing: invalid sync interval; using default")
		return defaultSharedSandboxBillingInterval
	}
	return interval
}

// Run reports immediately and then periodically. Errors are retried on the
// next tick; central billing's watermark makes every retry idempotent.
func (r *sharedSandboxBillingReporter) Run(ctx context.Context) {
	if r == nil || r.db == nil || r.live == nil || r.endpoint == "" {
		logger.Warn("sandbox billing: shared usage reporter is not configured")
		return
	}

	report := func() {
		if err := r.reportAll(ctx); err != nil {
			logger.WithFields("error", err.Error()).Warn("sandbox billing: shared usage report failed")
		}
	}
	report()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report()
		}
	}
}

func (r *sharedSandboxBillingReporter) reportAll(ctx context.Context) error {
	tenants, err := r.listMeteredTenants(ctx)
	if err != nil {
		return err
	}

	var reportErrors []error
	for _, tenantID := range tenants {
		access, err := r.ReportTenant(ctx, tenantID)
		if err != nil {
			reportErrors = append(reportErrors, fmt.Errorf("tenant %s: %w", tenantID, err))
			continue
		}
		if !access.SandboxAllowed && r.stopper != nil {
			if err := r.stopper.StopTenantSandboxes(ctx, tenantID); err != nil {
				reportErrors = append(reportErrors, fmt.Errorf("stop tenant %s after starter credit exhaustion: %w", tenantID, err))
			} else {
				logger.WithFields(
					"tenant_id", tenantID,
					"starter_credit_micros", access.StarterCreditMicros,
					"starter_credit_used_micros", access.StarterCreditUsedMicros,
				).Info("sandbox billing: stopped tenant compute after starter credit exhaustion")
			}
		}
	}
	return errors.Join(reportErrors...)
}

func (r *sharedSandboxBillingReporter) listMeteredTenants(ctx context.Context) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("shared sandbox billing database is not configured")
	}
	var tenantIDs []string
	if err := r.db.SelectContext(ctx, &tenantIDs, `
		SELECT tenant_id
		FROM (
			SELECT tenant_id
			FROM sandbox_instances
			WHERE tenant_id <> ''
			UNION
			SELECT tenant_id
			FROM sandbox_usage_records
			WHERE tenant_id <> ''
			UNION
			SELECT tenant_id
			FROM browser_usage_windows
			WHERE tenant_id <> ''
		) AS metered_tenants
		ORDER BY tenant_id`); err != nil {
		return nil, fmt.Errorf("list managed sandbox tenants: %w", err)
	}
	return tenantIDs, nil
}

// ReportTenant reports closed ledger rows plus the current open allocation
// windows. The two sides use the same pricing function, so closing a live
// window replaces live accrual with an equal immutable row without changing
// the cumulative total.
func (r *sharedSandboxBillingReporter) ReportTenant(ctx context.Context, tenantID string) (sharedSandboxBillingAccess, error) {
	if r == nil || r.db == nil || r.live == nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("shared sandbox billing reporter is not configured")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return sharedSandboxBillingAccess{}, fmt.Errorf("tenant ID is required")
	}
	if r.endpoint == "" {
		return sharedSandboxBillingAccess{}, fmt.Errorf("billing usage endpoint is not configured")
	}
	organizationID, usageTenantIDs, err := r.resolveBillingIdentity(ctx, tenantID)
	if err != nil {
		return sharedSandboxBillingAccess{}, err
	}

	var closed struct {
		ComputeSeconds int64   `db:"compute_seconds"`
		CostUSD        float64 `db:"cost_usd"`
	}
	if err := r.db.GetContext(ctx, &closed, `
		SELECT
			COALESCE(SUM(duration_seconds), 0)::BIGINT AS compute_seconds,
			COALESCE(SUM(cost_total_usd), 0)::DOUBLE PRECISION AS cost_usd
		FROM sandbox_usage_records
		WHERE tenant_id = ANY($1)`, pq.Array(usageTenantIDs)); err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("read closed sandbox usage: %w", err)
	}

	var concurrentSandboxes int64
	if err := r.db.GetContext(ctx, &concurrentSandboxes, `
		SELECT COUNT(*)
		FROM sandbox_instances
		WHERE tenant_id = ANY($1)
		  AND (
		    lifecycle_state IN (
		      'pending', 'creating', 'provisioning', 'running',
		      'stopping', 'reviving', 'terminating'
		    )
		    OR (billing_started_at IS NOT NULL AND billing_ended_at IS NULL)
		  )`, pq.Array(usageTenantIDs)); err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("read concurrent sandbox count: %w", err)
	}

	var liveCostUSD float64
	var liveSeconds int64
	for _, usageTenantID := range usageTenantIDs {
		costUSD, seconds := r.live.LiveAccruedCost(ctx, usageTenantID)
		liveCostUSD += costUSD
		liveSeconds += seconds
	}
	totalSeconds := closed.ComputeSeconds + liveSeconds
	totalCostMicros := int64(math.Round((closed.CostUSD + liveCostUSD) * 1_000_000))
	if totalSeconds < 0 || totalCostMicros < 0 {
		return sharedSandboxBillingAccess{}, fmt.Errorf("sandbox cumulative usage cannot be negative")
	}

	var browserTotals browserpool.UsageTotals
	if r.browser != nil {
		for _, usageTenantID := range usageTenantIDs {
			totals, totalsErr := r.browser.TotalsForTenant(ctx, usageTenantID, time.Now())
			if totalsErr != nil {
				return sharedSandboxBillingAccess{}, fmt.Errorf("read browser runtime usage: %w", totalsErr)
			}
			browserTotals.RuntimeSeconds += totals.RuntimeSeconds
			browserTotals.CostMicros += totals.CostMicros
		}
	}

	payload, err := json.Marshal(sharedSandboxUsageSnapshot{
		OrganizationID:                     organizationID,
		InstanceID:                         r.instanceID,
		ConcurrentSandboxesCount:           concurrentSandboxes,
		CumulativeSandboxComputeSeconds:    totalSeconds,
		CumulativeSandboxComputeCostMicros: totalCostMicros,
		CumulativeBrowserRuntimeSeconds:    browserTotals.RuntimeSeconds,
		CumulativeBrowserRuntimeCostMicros: browserTotals.CostMicros,
	})
	if err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("encode sandbox usage snapshot: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("create sandbox usage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("send sandbox usage snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return sharedSandboxBillingAccess{}, fmt.Errorf("billing usage endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var result sharedSandboxBillingResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("decode sandbox billing access: %w", err)
	}
	if result.SandboxAllowed == nil {
		return sharedSandboxBillingAccess{}, fmt.Errorf("billing usage response omitted sandbox access")
	}
	return sharedSandboxBillingAccess{
		SandboxAllowed:               *result.SandboxAllowed,
		BillingConfigured:            result.BillingConfigured,
		BillingActive:                result.BillingActive,
		StarterCreditMicros:          result.StarterCreditMicros,
		StarterCreditUsedMicros:      result.StarterCreditUsedMicros,
		StarterCreditRemainingMicros: result.StarterCreditRemainingMicros,
	}, nil
}

// resolveBillingIdentity maps the raw tenant written by the serving gateway to
// its billable organization and returns every tenant alias owned by that
// organization. Older rows may use the organization id while newer managed
// requests use the instance id; aggregating both prevents either side from
// disappearing when the central cumulative meter advances.
func (r *sharedSandboxBillingReporter) resolveBillingIdentity(ctx context.Context, tenantID string) (string, []string, error) {
	if r == nil || r.billingDB == nil {
		return tenantID, []string{tenantID}, nil
	}

	organization, err := billingidentity.ResolveOrganization(ctx, r.billingDB, tenantID)
	if err != nil {
		return "", nil, fmt.Errorf("resolve managed tenant organization: %w", err)
	}

	aliases, err := billingidentity.ListOrganizationAliases(ctx, r.billingDB, organization.ID)
	if err != nil {
		return "", nil, fmt.Errorf("list managed tenant aliases: %w", err)
	}
	return organization.ID, aliases, nil
}

// resolveSharedSandboxBilling verifies that the tenant may allocate compute.
// The synchronous meter report establishes the starter-credit baseline before
// first allocation and returns the central billing decision.
func resolveSharedSandboxBilling(
	ctx context.Context,
	db *sqlx.DB,
	reporter tenantSandboxUsageReporter,
	tenantID string,
) (bool, error) {
	_, access, err := resolveSharedRuntimeBillingAccess(ctx, db, reporter, tenantID)
	if err != nil {
		return false, err
	}
	return access.SandboxAllowed, nil
}

// resolveSharedRuntimeBillingAccess returns the organization tier alongside
// central billing's current decision. Hosted browser admission uses the same
// usage heartbeat, but deliberately does not consume sandbox starter credit.
func resolveSharedRuntimeBillingAccess(
	ctx context.Context,
	db *sqlx.DB,
	reporter tenantSandboxUsageReporter,
	tenantID string,
) (string, sharedSandboxBillingAccess, error) {
	tenantID = strings.TrimSpace(tenantID)
	if db == nil || tenantID == "" {
		return "", sharedSandboxBillingAccess{}, nil
	}

	organization, err := billingidentity.ResolveActiveOrganization(ctx, db, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", sharedSandboxBillingAccess{}, nil
		}
		return "", sharedSandboxBillingAccess{}, fmt.Errorf("resolve organization plan: %w", err)
	}
	organizationTier := organization.Tier
	if reporter == nil {
		return organizationTier, sharedSandboxBillingAccess{}, fmt.Errorf("sandbox usage reporter is not configured")
	}
	access, err := reporter.ReportTenant(ctx, tenantID)
	if err != nil {
		return organizationTier, sharedSandboxBillingAccess{}, fmt.Errorf("establish managed usage watermark: %w", err)
	}
	return organizationTier, access, nil
}
