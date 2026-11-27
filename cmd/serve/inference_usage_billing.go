package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	defaultInferenceUsageReportInterval = time.Minute
	inferenceUsageReportRequestTimeout  = 10 * time.Second
	// inferenceUsageLookback bounds which organizations are reported each
	// tick: any with counters in this window. It exceeds the longest
	// monthly period, and an organization idle for longer already has its
	// final period total in billing.
	inferenceUsageLookback = 35 * 24 * time.Hour
	// inferenceUsageReporterLock is the gateway-DB advisory lock that elects
	// one reporter per tick across replicas ("evsusage").
	inferenceUsageReporterLock int64 = 0x6576737573616765
)

// inferenceUsageReporter forwards each organization's period-cumulative
// request and token counts (billingmeter usage counters, written by every
// replica at the provider dispatch seam) to billing's usage snapshot
// endpoint under one stable source id.
//
// Why a single elected reporter, not every replica: billing stores these
// flows replace-on-write per (org, source, period) with no watermark (unlike
// the sandbox cumulative meters), so two replicas that read the counters at
// different instants can land out of order and briefly move the displayed
// total backwards. Each tick runs inside a gateway-DB transaction holding
// pg_try_advisory_xact_lock; a replica that does not get the lock skips the
// tick. Values are absolute, so a lost lock or a retried POST is still
// idempotent; the lock only removes the transient regression.
//
// Nothing here is on the request path: a failure is logged, counted and
// retried on the next tick.
type inferenceUsageReporter struct {
	gatewayDB *sqlx.DB
	billingDB *sqlx.DB
	endpoint  string
	sourceID  string
	interval  time.Duration
	client    *http.Client
	now       func() time.Time

	warnedMu sync.Mutex
	warned   map[string]bool

	// sent is the last snapshot each organization was successfully reported
	// with, so a tick can say at Info which totals moved. Ticks run serially;
	// the mutex only keeps the map safe if that ever changes.
	sentMu sync.Mutex
	sent   map[string]inferenceUsageSnapshot
}

type inferenceUsageSnapshot struct {
	OrganizationID          string `json:"organization_id"`
	InstanceID              string `json:"instance_id"`
	CumulativeTotalTokens   int64  `json:"cumulative_total_tokens"`
	CumulativeTotalRequests int64  `json:"cumulative_total_requests"`
	CumulativeInputTokens   int64  `json:"cumulative_input_tokens"`
	CumulativeOutputTokens  int64  `json:"cumulative_output_tokens"`
}

func newInferenceUsageReporter(gatewayDB, billingDB *sqlx.DB, endpoint string, client *http.Client) *inferenceUsageReporter {
	if client == nil {
		client = &http.Client{Timeout: inferenceUsageReportRequestTimeout}
	}
	return &inferenceUsageReporter{
		gatewayDB: gatewayDB,
		billingDB: billingDB,
		endpoint:  strings.TrimSpace(endpoint),
		sourceID:  inferenceUsageSourceID(),
		interval:  inferenceUsageIntervalFromEnv(),
		client:    client,
		now:       time.Now,
		warned:    map[string]bool{},
		sent:      map[string]inferenceUsageSnapshot{},
	}
}

// inferenceUsageSourceID is the billing instance id the totals are filed
// under. It must be stable across replicas and pods (a new id per pod would
// file a second full copy of the totals) and distinct from the sandbox
// reporter's id: that reporter posts zero token flows under its own id every
// tick, so sharing an id would overwrite these totals with zero.
func inferenceUsageSourceID() string {
	if explicit := strings.TrimSpace(os.Getenv("EVS_INFERENCE_USAGE_SOURCE_ID")); explicit != "" {
		return explicit
	}
	region := strings.TrimSpace(os.Getenv("EVS_CONTROLPLANE_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("EVS_REGION"))
	}
	if region == "" {
		return "shared-inference-plane"
	}
	return "shared-inference-plane:" + region
}

func inferenceUsageIntervalFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("EVS_INFERENCE_USAGE_REPORT_INTERVAL"))
	if raw == "" {
		return defaultInferenceUsageReportInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		logger.WithFields("value", raw).Warn("usage billing: invalid inference usage report interval; using default")
		return defaultInferenceUsageReportInterval
	}
	return d
}

// Run reports on every tick until ctx ends.
func (r *inferenceUsageReporter) Run(ctx context.Context) {
	if r == nil || r.gatewayDB == nil || r.endpoint == "" {
		logger.Warn("usage billing: inference usage reporter is not configured")
		return
	}
	billingmeter.UsageReporterStarted("inference", r.now())
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		// reportOnce logs its own one-line summary per led tick.
		_, _ = r.reportOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reportOnce runs one tick. led is false when another replica holds the
// reporter lock (nothing was sent).
func (r *inferenceUsageReporter) reportOnce(ctx context.Context) (led bool, err error) {
	tick, err := r.tick(ctx)
	return tick.led, err
}

// inferenceUsageTick is what one tick did.
type inferenceUsageTick struct {
	led bool
	// changed lists the organizations whose reported totals differ from the
	// last snapshot this reporter sent for them (all of them on the first
	// tick after a restart).
	changed []string
}

// maxLoggedOrganizations caps the organization ids on one tick log line.
const maxLoggedOrganizations = 10

func (r *inferenceUsageReporter) tick(ctx context.Context) (inferenceUsageTick, error) {
	var out inferenceUsageTick
	tx, err := r.gatewayDB.BeginTxx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin reporter transaction: %w", err)
	}
	// Read-only: rollback releases the transaction-scoped advisory lock.
	defer func() { _ = tx.Rollback() }()

	if err := tx.GetContext(ctx, &out.led, `SELECT pg_try_advisory_xact_lock($1)`, inferenceUsageReporterLock); err != nil {
		return out, fmt.Errorf("acquire reporter lock: %w", err)
	}
	if !out.led {
		return out, nil
	}

	now := r.now().UTC()
	orgs, err := billingmeter.UsageOrgsSince(ctx, tx, now.Add(-inferenceUsageLookback))
	if err != nil {
		return out, fmt.Errorf("list organizations with usage: %w", err)
	}
	summary := sharedUsageReportSummary{}
	for _, org := range orgs {
		reported, changed, err := r.reportOrg(ctx, tx, org, now)
		switch {
		case err != nil:
			billingmeter.UsageReportFailed()
			summary.failed++
			summary.errs = append(summary.errs, fmt.Errorf("organization %s: %w", org, err))
		case reported:
			summary.reported++
			if changed {
				out.changed = append(out.changed, org)
			}
		default:
			summary.skipped++
		}
	}
	billingmeter.SharedUsageReportTick("inference", summary.reported, summary.skipped, summary.failed)
	fields := []interface{}{"reported", summary.reported, "changed", len(out.changed), "skipped", summary.skipped, "failed", summary.failed}
	err = summary.err()
	if err != nil {
		if ctx.Err() == nil {
			logger.WithFields(append(fields, "error", err.Error())...).Warn("usage billing: inference usage report tick")
		}
		return out, err
	}
	// Only the leader advances the stall clock: a follower that skipped the
	// tick says nothing about whether usage reached billing. It advances on
	// every clean tick, whether or not a total moved.
	billingmeter.UsageReportSucceeded("inference", now)
	if len(out.changed) > 0 {
		// Info only when a total moved, so an idle plane stays quiet while
		// a request reaching billing is visible at the default log level.
		ids := out.changed
		if len(ids) > maxLoggedOrganizations {
			ids = ids[:maxLoggedOrganizations]
		}
		logger.WithFields(append(fields, "source_id", r.sourceID, "organization_ids", strings.Join(ids, ","))...).
			Info("usage billing: inference usage report tick")
		return out, nil
	}
	logger.WithFields(fields...).Debug("usage billing: inference usage report tick")
	return out, nil
}

// reportOrg reports one organization. reported is false when the
// organization was skipped (it can never be filed); changed is true when the
// snapshot differs from the last one sent for it.
func (r *inferenceUsageReporter) reportOrg(ctx context.Context, tx *sqlx.Tx, org string, now time.Time) (reported, changed bool, err error) {
	if _, err := uuid.Parse(org); err != nil {
		// Billing keys usage by organization UUID; a non-UUID id can never
		// be filed. Warn once per process, not once per tick.
		r.warnOnce(org, "usage billing: organization id is not a UUID; its inference usage cannot be reported")
		return false, false, nil
	}
	var sub *billingmeter.SubscriptionPeriod
	if r.billingDB != nil {
		loaded, err := billingmeter.LoadSubscriptionPeriod(ctx, r.billingDB, org)
		if err != nil {
			return false, false, fmt.Errorf("resolve billing period: %w", err)
		}
		sub = loaded
	}
	start, end := billingmeter.UsagePeriod(sub, now)
	totals, err := sumOrgUsageInSavepoint(ctx, tx, org, start, end)
	if err != nil {
		return false, false, err
	}
	snap := inferenceUsageSnapshot{
		OrganizationID:          org,
		InstanceID:              r.sourceID,
		CumulativeTotalTokens:   totals.TotalTokens(),
		CumulativeTotalRequests: totals.Requests,
		CumulativeInputTokens:   totals.InputTokens,
		CumulativeOutputTokens:  totals.OutputTokens,
	}
	if err := r.post(ctx, snap); err != nil {
		return false, false, err
	}
	r.sentMu.Lock()
	prev, seen := r.sent[org]
	r.sent[org] = snap
	r.sentMu.Unlock()
	return true, !seen || prev != snap, nil
}

// sumOrgUsageInSavepoint reads one organization's counters inside a savepoint.
// Every organization in a tick shares the transaction that holds the reporter
// lock, and Postgres aborts a transaction on any failed statement: without the
// savepoint, one organization's failed read would fail every organization
// after it with "current transaction is aborted".
func sumOrgUsageInSavepoint(ctx context.Context, tx *sqlx.Tx, org string, start, end time.Time) (billingmeter.UsageTotals, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT inference_usage_org`); err != nil {
		return billingmeter.UsageTotals{}, fmt.Errorf("open savepoint: %w", err)
	}
	totals, err := billingmeter.UsageTotalsBetween(ctx, tx, org, start, end)
	if err != nil {
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT inference_usage_org`); rbErr != nil {
			return billingmeter.UsageTotals{}, fmt.Errorf("sum usage counters: %w (and roll back savepoint: %v)", err, rbErr)
		}
		return billingmeter.UsageTotals{}, fmt.Errorf("sum usage counters: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT inference_usage_org`); err != nil {
		return billingmeter.UsageTotals{}, fmt.Errorf("release savepoint: %w", err)
	}
	return totals, nil
}

func (r *inferenceUsageReporter) post(ctx context.Context, snap inferenceUsageSnapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, inferenceUsageReportRequestTimeout)
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
		return fmt.Errorf("billing usage snapshot returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (r *inferenceUsageReporter) warnOnce(org, msg string) {
	r.warnedMu.Lock()
	seen := r.warned[org]
	r.warned[org] = true
	r.warnedMu.Unlock()
	if !seen {
		logger.WithFields("organization_id", org).Warn(msg)
	}
}

// startInferenceUsageReporter starts the reporter when the billing endpoint
// and the gateway's M2M client are configured, and says why not otherwise.
func startInferenceUsageReporter(ctx context.Context, gatewayDB, billingDB *sqlx.DB, endpoint string) {
	if strings.TrimSpace(endpoint) == "" {
		logger.Warn("usage billing: internal endpoint missing; inference usage will not reach billing")
		return
	}
	client, err := newManagedBillingHTTPClient()
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("usage billing: M2M client unavailable; inference usage will not reach billing")
		return
	}
	r := newInferenceUsageReporter(gatewayDB, billingDB, endpoint, client)
	go r.Run(ctx)
	logger.WithFields("endpoint", endpoint, "source_id", r.sourceID, "interval", r.interval.String()).
		Info("usage billing: inference usage reporter started")
}

// ReportInferenceUsageOnce runs one tick of the production inference usage
// reporter, with the gateway's real M2M client, against endpoint. Production
// runs the same tick on a ticker (startInferenceUsageReporter); this entry
// point lets the cross-service test in services/billing drive the reporter
// against billing's real HTTP handler and database. led is false when another
// replica holds the reporter lock and nothing was sent.
func ReportInferenceUsageOnce(ctx context.Context, gatewayDB, billingDB *sqlx.DB, endpoint string) (led bool, err error) {
	client, err := newManagedBillingHTTPClient()
	if err != nil {
		return false, err
	}
	return newInferenceUsageReporter(gatewayDB, billingDB, endpoint, client).reportOnce(ctx)
}
