package sandbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	usagecmd "github.com/everstacklabs/everstack/internal/commands/handlers/usage"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/usage"
)

type sandboxUsageCostBreakdown struct {
	CPUUSD      float64 `json:"cpu_usd"`
	MemoryUSD   float64 `json:"memory_usd"`
	DiskUSD     float64 `json:"disk_usd"`
	PlatformUSD float64 `json:"platform_usd"`
	TotalUSD    float64 `json:"total_usd"`
}

// computeSandboxCost is the single source of truth for how a sandbox's
// compute time turns into dollars. The same math is used by:
//
//   - recordUsageSnapshot, which persists immutable rows to
//     sandbox_usage_records at each lifecycle event (and feeds Stripe).
//   - LiveAccruedCost, which estimates ongoing accrual for sandboxes
//     that are currently running and therefore have not yet written a
//     ledger row for the current window.
//
// Keeping both callers funneled through this function is what makes
// the "Cost so far" tile match the eventual bill: when a running
// sandbox terminates, its live-accrued estimate is replaced by the
// ledger row, but both are computed identically so the total never
// jumps.
//
// durationSeconds is rounded up (math.Ceil) to stay consistent with
// the legacy persisted records — a 30-second sandbox that's been up
// for 0.4s would otherwise round to zero and disappear from billing.
func computeSandboxCost(
	cfg InstanceConfig,
	periodStart, periodEnd time.Time,
	pricing SandboxPricingConfig,
	tierMultiplier float64,
) (sandboxUsageCostBreakdown, int64) {
	if periodStart.IsZero() || periodEnd.IsZero() || !periodEnd.After(periodStart) {
		return sandboxUsageCostBreakdown{}, 0
	}
	durationSeconds := int64(math.Ceil(periodEnd.Sub(periodStart).Seconds()))
	if durationSeconds <= 0 {
		return sandboxUsageCostBreakdown{}, 0
	}

	cpuRate := pricing.CPUPerHourUSD
	memRate := pricing.MemoryGBPerHourUSD
	diskRate := pricing.DiskGBPerHourUSD
	platformRate := pricing.PlatformFeePerHourUSD
	if !pricing.Enabled {
		cpuRate, memRate, diskRate, platformRate = 0, 0, 0, 0
	}

	hours := float64(durationSeconds) / 3600.0
	cost := sandboxUsageCostBreakdown{
		CPUUSD:      cfg.CPULimit * hours * cpuRate,
		MemoryUSD:   (float64(cfg.MemoryMB) / 1024.0) * hours * memRate,
		DiskUSD:     tieredDiskCostUSD(cfg.DiskMB, hours, diskRate, pricing.IncludedDiskGiB, pricing.DiskTier2ThresholdGiB, pricing.DiskTier2Multiplier),
		PlatformUSD: hours * platformRate,
	}
	if tierMultiplier > 0 && tierMultiplier != 1.0 {
		cost.CPUUSD *= tierMultiplier
		cost.MemoryUSD *= tierMultiplier
		cost.DiskUSD *= tierMultiplier
		cost.PlatformUSD *= tierMultiplier
	}
	cost.TotalUSD = cost.CPUUSD + cost.MemoryUSD + cost.DiskUSD + cost.PlatformUSD
	return cost, durationSeconds
}

// tieredDiskCostUSD turns a sandbox's provisioned root disk into a dollar
// figure using the included allowance + marginal tiering from
// SandboxPricingConfig:
//
//   - The first includedGiB are included in the fixed machine rate.
//   - GiB from includedGiB up to tier2ThresholdGiB bill at diskRate.
//   - GiB beyond tier2ThresholdGiB bill at diskRate * tier2Mult.
//
// Tiering is marginal: only the portion of disk that falls inside each band
// is charged at that band's rate. Defaults are defensive so legacy/partial
// pricing configs degrade to "bill everything at the base rate" rather than
// accidentally giving storage away: a zero allowance bills from the first
// GiB, and a missing/unusable tier-2 threshold or multiplier disables the
// premium band (all overage at base rate).
func tieredDiskCostUSD(diskMB int64, hours, diskRate, includedGiB, tier2ThresholdGiB, tier2Mult float64) float64 {
	if diskMB <= 0 || diskRate <= 0 {
		return 0
	}
	diskGiB := float64(diskMB) / 1024.0
	if includedGiB < 0 {
		includedGiB = 0
	}
	billableGiB := diskGiB - includedGiB
	if billableGiB <= 0 {
		return 0
	}

	// No usable premium band: charge all billable disk at the base rate.
	if tier2ThresholdGiB <= includedGiB || tier2Mult <= 0 {
		return billableGiB * hours * diskRate
	}

	tier1GiB := math.Min(diskGiB, tier2ThresholdGiB) - includedGiB
	if tier1GiB < 0 {
		tier1GiB = 0
	}
	tier2GiB := diskGiB - tier2ThresholdGiB
	if tier2GiB < 0 {
		tier2GiB = 0
	}
	return (tier1GiB + tier2GiB*tier2Mult) * hours * diskRate
}

// EffectiveDiskRateUSD returns the per-GiB-hour storage rate for a tenant,
// including any tier multiplier, resolved from the same runtime-config pricing
// source the sandbox meter uses. Returns 0 when pricing is disabled or the
// manager/db is unavailable. Used by the volume metering sweep so volume
// storage is billed at the same rate as sandbox disk (volumes have no bundled
// allowance — the included GiB is root-disk only).
func (m *SandboxManager) EffectiveDiskRateUSD(ctx context.Context, tenantID string) float64 {
	if m == nil {
		return 0
	}
	pricing := m.resolvePricingFromRuntimeConfig(ctx, m.globalConfig.Pricing)
	if !pricing.Enabled {
		return 0
	}
	rate := pricing.DiskGBPerHourUSD
	if tenantID != "" {
		tier := m.resolveTenantTier(tenantID)
		if mul, ok := pricing.TierMultipliers[tier]; ok && mul > 0 {
			rate *= mul
		}
	}
	return rate
}

// LiveAccruedCost returns the cost that is currently accruing for this
// tenant's running sandboxes — work that's already been done but
// hasn't yet been written to sandbox_usage_records because no
// lifecycle event has fired. Pair this with SUM(cost_total_usd) over
// the ledger to get a "Cost so far" number that doesn't undercount
// in-flight sandboxes (and therefore doesn't appear to jump when one
// stops and its row finally lands).
//
// Returns 0 when tenantID is empty (defensive — no fallback to "the
// first tenant in DB"; see tenant_isolation_bugs memory).
func (m *SandboxManager) LiveAccruedCost(ctx context.Context, tenantID string) (costUSD float64, computeSeconds int64) {
	return m.liveAccruedCostAt(ctx, tenantID, time.Now())
}

// LiveAccruedCostAll returns all currently open compute windows on this
// gateway. It is used only for the instance-level monotonic billing meter;
// tenant-facing APIs must continue to use LiveAccruedCost with an explicit
// tenant ID.
func (m *SandboxManager) LiveAccruedCostAll(ctx context.Context) (costUSD float64, computeSeconds int64) {
	if m == nil {
		return 0, 0
	}
	now := time.Now()
	pricing := m.resolvePricingFromRuntimeConfig(ctx, m.globalConfig.Pricing)
	multiplierFor := func(tenantID string) float64 {
		multiplier := 1.0
		if mul, ok := pricing.TierMultipliers[m.resolveTenantTier(tenantID)]; ok && mul > 0 {
			multiplier = mul
		}
		return multiplier
	}

	if m.db != nil {
		type openWindow struct {
			TenantID         string       `db:"tenant_id"`
			Config           []byte       `db:"config"`
			BillingStartedAt time.Time    `db:"billing_started_at"`
			BillingEndedAt   sql.NullTime `db:"billing_ended_at"`
		}
		var windows []openWindow
		if err := m.db.SelectContext(ctx, &windows, `
			SELECT tenant_id, config, billing_started_at, billing_ended_at
			FROM sandbox_instances
			WHERE billing_started_at IS NOT NULL
			  AND destroyed_at IS NULL`); err == nil {
			multipliers := make(map[string]float64)
			for _, window := range windows {
				cfg, err := parseInstanceConfig(window.Config)
				if err != nil {
					continue
				}
				mul, ok := multipliers[window.TenantID]
				if !ok {
					mul = multiplierFor(window.TenantID)
					multipliers[window.TenantID] = mul
				}
				periodEnd := now
				if window.BillingEndedAt.Valid {
					periodEnd = window.BillingEndedAt.Time
				}
				cost, dur := computeSandboxCost(cfg, window.BillingStartedAt, periodEnd, pricing, mul)
				costUSD += cost.TotalUSD
				computeSeconds += dur
			}
			return costUSD, computeSeconds
		} else {
			logger.WithFields("error", err.Error()).
				Warn("sandbox_manager: all open billing-window query failed; using routing cache")
		}
	}

	for _, inst := range m.ListInstances() {
		if !hasAllocatedCompute(inst) {
			continue
		}
		periodEnd := now
		if !inst.BillingEndedAt.IsZero() {
			periodEnd = inst.BillingEndedAt
		}
		cost, dur := computeSandboxCost(inst.Config, inst.BillingStartedAt, periodEnd, pricing, multiplierFor(inst.Config.TenantID))
		costUSD += cost.TotalUSD
		computeSeconds += dur
	}
	return costUSD, computeSeconds
}

// liveAccruedCostAt is the deterministic implementation behind
// LiveAccruedCost. Only an explicitly open billing window is metered; the
// sandbox's immutable CreatedAt is never a billing fallback.
func (m *SandboxManager) liveAccruedCostAt(ctx context.Context, tenantID string, now time.Time) (costUSD float64, computeSeconds int64) {
	if tenantID == "" || m == nil {
		return 0, 0
	}

	pricing := m.resolvePricingFromRuntimeConfig(ctx, m.globalConfig.Pricing)
	tier := m.resolveTenantTier(tenantID)
	tierMultiplier := 1.0
	if mul, ok := pricing.TierMultipliers[tier]; ok && mul > 0 {
		tierMultiplier = mul
	}

	// Postgres owns lifecycle truth. Reading open windows directly keeps live
	// accrual correct during stopping/terminating and after a gateway restart,
	// when the process-local routing cache may be incomplete.
	if m.db != nil {
		type openWindow struct {
			Config           []byte       `db:"config"`
			BillingStartedAt time.Time    `db:"billing_started_at"`
			BillingEndedAt   sql.NullTime `db:"billing_ended_at"`
		}
		var windows []openWindow
		if err := m.db.SelectContext(ctx, &windows, `
			SELECT config, billing_started_at, billing_ended_at
			FROM sandbox_instances
			WHERE tenant_id = $1
			  AND billing_started_at IS NOT NULL
			  AND destroyed_at IS NULL`, tenantID); err == nil {
			for _, window := range windows {
				cfg, err := parseInstanceConfig(window.Config)
				if err != nil {
					continue
				}
				periodEnd := now
				if window.BillingEndedAt.Valid {
					periodEnd = window.BillingEndedAt.Time
				}
				cost, dur := computeSandboxCost(cfg, window.BillingStartedAt, periodEnd, pricing, tierMultiplier)
				costUSD += cost.TotalUSD
				computeSeconds += dur
			}
			return costUSD, computeSeconds
		} else {
			logger.WithFields("tenant_id", tenantID, "error", err.Error()).
				Warn("sandbox_manager: open billing-window query failed; using routing cache")
		}
	}

	for _, inst := range m.ListInstances() {
		if !hasAllocatedCompute(inst) {
			continue
		}
		if inst.Config.TenantID != tenantID {
			continue
		}
		periodEnd := now
		if !inst.BillingEndedAt.IsZero() {
			periodEnd = inst.BillingEndedAt
		}
		cost, dur := computeSandboxCost(inst.Config, inst.BillingStartedAt, periodEnd, pricing, tierMultiplier)
		costUSD += cost.TotalUSD
		computeSeconds += dur
	}
	return costUSD, computeSeconds
}

// hasAllocatedCompute is the process-local fallback for deciding whether an
// open billing window still represents allocated compute. Durable lifecycle
// rows normally make billing_started_at authoritative; this guard prevents a
// stale field on a stopped cache entry from accruing after a partial restart.
func hasAllocatedCompute(inst *Instance) bool {
	if inst == nil || inst.BillingStartedAt.IsZero() {
		return false
	}
	switch inst.LifecycleState {
	case LifecycleRunning, LifecycleStopping, LifecycleTerminating:
		return true
	case "":
		return inst.Status == StatusRunning
	default:
		return false
	}
}

// AccruedCostForWindow prices one currently-open allocation window using the
// same resolved rates and tier multiplier as the immutable ledger writer. API
// responses use this rather than duplicating pricing math in a frontend.
func (m *SandboxManager) AccruedCostForWindow(
	ctx context.Context,
	tenantID string,
	cfg InstanceConfig,
	periodStart, periodEnd time.Time,
) (costUSD float64, computeSeconds int64) {
	if m == nil || tenantID == "" || periodStart.IsZero() {
		return 0, 0
	}
	pricing := m.resolvePricingFromRuntimeConfig(ctx, m.globalConfig.Pricing)
	tierMultiplier := 1.0
	if mul, ok := pricing.TierMultipliers[m.resolveTenantTier(tenantID)]; ok && mul > 0 {
		tierMultiplier = mul
	}
	cost, seconds := computeSandboxCost(cfg, periodStart, periodEnd, pricing, tierMultiplier)
	return cost.TotalUSD, seconds
}

// captureNetworkBytes samples the backend's cumulative network counters for
// an instance and stashes them on the Instance. Must be called BEFORE the VM
// is torn down — once it's destroyed the counters are gone. Best-effort: a
// failed sample leaves the previously-captured value in place.
func (m *SandboxManager) captureNetworkBytes(ctx context.Context, inst *Instance) {
	if m == nil || inst == nil || m.backend == nil {
		return
	}
	stats, err := m.backend.Stats(ctx, inst.ID)
	if err != nil || stats == nil {
		return
	}
	if stats.NetworkRxBytes > 0 {
		inst.LastNetworkRxBytes = stats.NetworkRxBytes
	}
	if stats.NetworkTxBytes > 0 {
		inst.LastNetworkTxBytes = stats.NetworkTxBytes
	}
}

func (m *SandboxManager) recordUsageForInstance(ctx context.Context, inst *Instance, lifecycleEvent EventType, reason string, periodEnd time.Time) bool {
	if inst == nil {
		return true
	}
	return m.recordUsageSnapshot(
		ctx,
		inst.ID,
		inst.Config.SessionID,
		inst.Config.TenantID,
		inst.Backend,
		inst.Config,
		inst.BillingStartedAt,
		periodEnd,
		lifecycleEvent,
		reason,
		inst.LastNetworkRxBytes,
		inst.LastNetworkTxBytes,
	)
}

// CloseMissingCompute closes the allocated-compute window when the lifecycle
// health sweeper has authoritatively confirmed that a VM disappeared outside a
// normal stop/terminate transition.
func (m *SandboxManager) CloseMissingCompute(ctx context.Context, sandboxID, reason string, endedAt time.Time) error {
	if m == nil || m.db == nil {
		return nil
	}
	inst, err := m.LookupInstanceByIDFromDBScoped(ctx, sandboxID, "")
	if err != nil {
		return err
	}
	if inst.BillingStartedAt.IsZero() {
		return nil
	}
	if !m.recordUsageForInstance(ctx, inst, EventSandboxError, reason, endedAt) {
		return fmt.Errorf("persist missing sandbox usage")
	}
	m.mu.Lock()
	if cached := m.instancesBySandbox[sandboxID]; cached != nil {
		cached.BillingStartedAt = time.Time{}
		cached.BillingEndedAt = time.Time{}
	}
	m.mu.Unlock()
	return nil
}

func (m *SandboxManager) recordUsageSnapshot(
	ctx context.Context,
	sandboxID, sessionID, tenantID, backend string,
	cfg InstanceConfig,
	periodStart, periodEnd time.Time,
	lifecycleEvent EventType,
	reason string,
	networkRxBytes, networkTxBytes int64,
) bool {
	if m.db == nil {
		return true
	}

	// Pin the first backend-confirmed end before doing the ledger transaction.
	// If persistence or the process fails afterward, a retry reuses this exact
	// timestamp rather than billing the retry/outage delay.
	pinnedEnd, err := m.pinBillingWindowEnd(ctx, sandboxID, periodStart, periodEnd)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to pin sandbox billing end")
		return false
	}
	periodEnd = pinnedEnd

	pricing := m.resolvePricingFromRuntimeConfig(ctx, m.globalConfig.Pricing)
	if pricing.Currency == "" {
		pricing.Currency = "USD"
	}

	// Resolve tier discount. pricing.TierMultipliers (e.g.
	// {pro: 0.93, enterprise: 0.88}) was previously written to the
	// audit record but never multiplied in — paid customers got
	// silently billed list price. Default resolver returns "free" →
	// multiplier 1.0 → no change for self-hosted / unresolved tenants.
	tier := m.resolveTenantTier(tenantID)
	tierMultiplier := 1.0
	if mul, ok := pricing.TierMultipliers[tier]; ok && mul > 0 {
		tierMultiplier = mul
	}

	// Shared cost math with LiveAccruedCost — when this row replaces a
	// running sandbox's live estimate, the totals must match exactly so
	// the displayed cost doesn't jump on lifecycle transitions.
	cost, durationSeconds := computeSandboxCost(cfg, periodStart, periodEnd, pricing, tierMultiplier)
	if durationSeconds <= 0 {
		return true
	}

	// Rates after the pricing.Enabled gate — recorded so future audits
	// can reproduce the bill from the snapshot alone.
	cpuRate := pricing.CPUPerHourUSD
	memRate := pricing.MemoryGBPerHourUSD
	diskRate := pricing.DiskGBPerHourUSD
	platformRate := pricing.PlatformFeePerHourUSD
	if !pricing.Enabled {
		cpuRate, memRate, diskRate, platformRate = 0, 0, 0, 0
	}

	pricingSnapshot := map[string]interface{}{
		"enabled":                   pricing.Enabled,
		"currency":                  pricing.Currency,
		"cpu_per_hour_usd":          cpuRate,
		"memory_gb_per_hour_usd":    memRate,
		"disk_gb_per_hour_usd":      diskRate,
		"platform_fee_per_hour_usd": platformRate,
		"included_disk_gib":         pricing.IncludedDiskGiB,
		"disk_tier2_threshold_gib":  pricing.DiskTier2ThresholdGiB,
		"disk_tier2_multiplier":     pricing.DiskTier2Multiplier,
		"tier_multipliers":          pricing.TierMultipliers,
		// New fields — the resolved tier and applied multiplier are
		// recorded explicitly so a future audit can answer "why was
		// this tenant charged $X" without re-running the resolver.
		"tier":            tier,
		"tier_multiplier": tierMultiplier,
		"cost_breakdown":  cost,
	}
	pricingJSON, err := json.Marshal(pricingSnapshot)
	if err != nil {
		pricingJSON = []byte("{}")
	}

	usageMetadata := map[string]interface{}{
		"session_id":       sessionID,
		"backend":          backend,
		"lifecycle_event":  lifecycleEvent,
		"reason":           reason,
		"cpu_limit":        cfg.CPULimit,
		"memory_mb":        cfg.MemoryMB,
		"disk_mb":          cfg.DiskMB,
		"network_rx_bytes": networkRxBytes,
		"network_tx_bytes": networkTxBytes,
		"pricing":          pricingSnapshot,
	}
	// The start boundary identifies one allocation window across retries. The
	// lifecycle event/end time are deliberately excluded: a lease retry after
	// the VM is already gone must not create another charge for the same run.
	usageKey := fmt.Sprintf(
		"sandbox:%s:compute:%d",
		sandboxID,
		periodStart.UTC().UnixNano(),
	)
	usageRec := usage.BillingUsageRecord{
		IdempotencyKey: usageKey,
		TenantID:       tenantID,
		ResourceType:   "sandbox",
		ResourceID:     sandboxID,
		SourceType:     "sandbox.usage",
		SourceRef:      usageKey,
		MetricType:     "sandbox.compute_seconds",
		Quantity:       float64(durationSeconds),
		Unit:           "seconds",
		CostUSD:        cost.TotalUSD,
		Currency:       pricing.Currency,
		Metadata:       usageMetadata,
		PeriodStart:    &periodStart,
		PeriodEnd:      &periodEnd,
	}

	// Serialize closes for this sandbox and commit the domain ledger + billing
	// outbox atomically. This makes stop/terminate retries exactly-once for a
	// billing window even when a reconciler lease expires mid-transition.
	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to begin sandbox usage transaction")
		return false
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sandbox-billing:"+sandboxID); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to lock sandbox billing window")
		return false
	}
	var alreadyClosed bool
	if err := tx.GetContext(ctx, &alreadyClosed, `
		SELECT EXISTS (
			SELECT 1 FROM sandbox_usage_records
			WHERE sandbox_id = $1 AND period_start = $2
		)`, sandboxID, periodStart.UTC()); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to check sandbox billing window")
		return false
	}
	if alreadyClosed {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sandbox_instances
			SET billing_started_at = NULL, billing_ended_at = NULL
			WHERE id = $1 AND billing_started_at = $2`, sandboxID, periodStart.UTC()); err != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
				Warn("sandbox_manager: failed to close duplicate billing window")
			return false
		}
		if err := tx.Commit(); err != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
				Warn("sandbox_manager: failed to finish duplicate billing-window check")
			return false
		}
		return true
	}

	const q = `
		INSERT INTO sandbox_usage_records (
			sandbox_id, session_id, tenant_id, backend, lifecycle_event, reason,
			period_start, period_end, duration_seconds,
			cpu_limit, memory_mb, disk_mb,
			pricing, cost_total_usd,
			network_rx_bytes, network_tx_bytes
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11, $12,
			$13, $14,
			$15, $16
		)`
	if _, err := tx.ExecContext(ctx, q,
		sandboxID, sessionID, tenantID, backend, string(lifecycleEvent), reason,
		periodStart.UTC(), periodEnd.UTC(), durationSeconds,
		cfg.CPULimit, cfg.MemoryMB, cfg.DiskMB,
		pricingJSON, cost.TotalUSD,
		networkRxBytes, networkTxBytes,
	); err != nil {
		logger.WithFields(
			"sandbox_id", sandboxID,
			"event_type", lifecycleEvent,
			"error", err.Error(),
		).Warn("sandbox_manager: failed to persist sandbox usage record")
		return false
	}

	if err := usage.InsertBillingUsageRecordTx(ctx, tx, usageRec); err != nil {
		logger.WithFields(
			"sandbox_id", sandboxID,
			"error", err.Error(),
		).Warn("sandbox_manager: failed to persist billing usage record")
		return false
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET billing_started_at = NULL, billing_ended_at = NULL
		WHERE id = $1 AND billing_started_at = $2`, sandboxID, periodStart.UTC()); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to close sandbox billing window")
		return false
	}
	if err := tx.Commit(); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to commit sandbox usage transaction")
		return false
	}

	m.emitBillingUsageCommand(ctx, usageRec)

	m.recordEvent(
		sandboxID,
		sessionID,
		tenantID,
		EventUsageMetered,
		"Sandbox usage metered",
		map[string]interface{}{
			"lifecycle_event":  lifecycleEvent,
			"reason":           reason,
			"period_start":     periodStart.UTC().Format(time.RFC3339),
			"period_end":       periodEnd.UTC().Format(time.RFC3339),
			"duration_seconds": durationSeconds,
			"cost_total_usd":   cost.TotalUSD,
			"currency":         pricing.Currency,
		},
		nil,
		"",
	)
	return true
}

// pinBillingWindowEnd durably remembers the first time the backend confirmed
// compute was gone. The UPDATE is deliberately committed before the ledger
// transaction: if the latter retries, elapsed retry time is not billable.
// When another closer already committed the ledger, return its period_end so
// the duplicate path remains idempotent.
func (m *SandboxManager) pinBillingWindowEnd(
	ctx context.Context,
	sandboxID string,
	periodStart, observedEnd time.Time,
) (time.Time, error) {
	var pinned time.Time
	err := m.db.GetContext(ctx, &pinned, `
		UPDATE sandbox_instances
		SET billing_ended_at = COALESCE(billing_ended_at, $3)
		WHERE id = $1 AND billing_started_at = $2
		RETURNING billing_ended_at`, sandboxID, periodStart.UTC(), observedEnd.UTC())
	if err == nil {
		// Some teardown paths intentionally hold the routing mutex while they
		// remove a stale instance. The database marker is authoritative, so a
		// best-effort cache update must never deadlock those callers.
		if m.mu.TryLock() {
			if cached := m.instancesBySandbox[sandboxID]; cached != nil {
				cached.BillingEndedAt = pinned
			}
			m.mu.Unlock()
		}
		return pinned, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}

	if err := m.db.GetContext(ctx, &pinned, `
		SELECT period_end
		FROM sandbox_usage_records
		WHERE sandbox_id = $1 AND period_start = $2
		LIMIT 1`, sandboxID, periodStart.UTC()); err != nil {
		return time.Time{}, err
	}
	return pinned, nil
}

func (m *SandboxManager) emitBillingUsageCommand(ctx context.Context, rec usage.BillingUsageRecord) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil || sys == nil || sys.CommandBus == nil {
		return
	}

	cmd := usagecmd.NewRecordBillingUsageCommand(rec, "", "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		logger.WithFields(
			"idempotency_key", rec.IdempotencyKey,
			"source_type", rec.SourceType,
			"source_ref", rec.SourceRef,
			"error", err.Error(),
		).Warn("sandbox_manager: failed to dispatch billing usage command")
	}
}

func parseInstanceConfig(raw []byte) (InstanceConfig, error) {
	var cfg InstanceConfig
	if len(raw) == 0 {
		return cfg, fmt.Errorf("empty config")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (m *SandboxManager) resolvePricingFromRuntimeConfig(ctx context.Context, fallback SandboxPricingConfig) SandboxPricingConfig {
	// Managed Everstack compute is governed by the centrally shipped pricing
	// contract. An instance runtime-config row must never turn paid compute off
	// or change the amount Stripe receives. Self-hosted operators retain the
	// runtime override below because they own the infrastructure and ledger.
	if m != nil && m.managedMachineProfiles.Load() {
		fallback.Enabled = true
		return fallback
	}
	if m.db == nil {
		return fallback
	}

	var raw json.RawMessage
	// Sandbox pricing is operator-set (deployment-wide), not per-tenant —
	// always read the empty-tenant row. Per-tenant pricing override is
	// not a feature and shouldn't be added here.
	if err := m.db.GetContext(ctx, &raw, `SELECT config FROM runtime_config WHERE section = 'features' AND tenant_id = '' LIMIT 1`); err != nil {
		return fallback
	}

	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return fallback
	}

	sandbox, ok := root["sandbox"].(map[string]interface{})
	if !ok {
		return fallback
	}
	pricingRaw, ok := sandbox["pricing"].(map[string]interface{})
	if !ok {
		return fallback
	}

	return mergeSandboxPricing(fallback, pricingRaw)
}

// mergeSandboxPricing overlays an operator-set pricing block (the parsed
// runtime_config sandbox.pricing object) onto a fallback config. Only keys
// present in pricingRaw take effect; everything else — crucially the
// IncludedDiskGiB allowance and tier-2 fields, which existing rows predate —
// is inherited from fallback. Pulled out as a pure function so the overlay
// contract is unit-testable without a database. snake_case and camelCase
// keys are both accepted (admin UI writes camelCase; configs use snake_case).
func mergeSandboxPricing(fallback SandboxPricingConfig, pricingRaw map[string]interface{}) SandboxPricingConfig {
	merged := fallback
	if v, ok := pricingRaw["enabled"].(bool); ok {
		merged.Enabled = v
	}
	if v, ok := pricingRaw["currency"].(string); ok && v != "" {
		merged.Currency = v
	}
	if v, ok := toFloat(pricingRaw["cpu_per_hour_usd"], pricingRaw["cpuPerHourUsd"]); ok {
		merged.CPUPerHourUSD = v
	}
	if v, ok := toFloat(pricingRaw["memory_gb_per_hour_usd"], pricingRaw["memoryGbPerHourUsd"]); ok {
		merged.MemoryGBPerHourUSD = v
	}
	if v, ok := toFloat(pricingRaw["disk_gb_per_hour_usd"], pricingRaw["diskGbPerHourUsd"]); ok {
		merged.DiskGBPerHourUSD = v
	}
	if v, ok := toFloat(pricingRaw["platform_fee_per_hour_usd"], pricingRaw["platformFeePerHourUsd"]); ok {
		merged.PlatformFeePerHourUSD = v
	}
	if v, ok := toFloat(pricingRaw["included_disk_gib"], pricingRaw["includedDiskGib"]); ok {
		merged.IncludedDiskGiB = v
	}
	if v, ok := toFloat(pricingRaw["disk_tier2_threshold_gib"], pricingRaw["diskTier2ThresholdGib"]); ok {
		merged.DiskTier2ThresholdGiB = v
	}
	if v, ok := toFloat(pricingRaw["disk_tier2_multiplier"], pricingRaw["diskTier2Multiplier"]); ok {
		merged.DiskTier2Multiplier = v
	}
	if tierMap, ok := pricingRaw["tier_multipliers"].(map[string]interface{}); ok {
		merged.TierMultipliers = mergeTierMultipliers(merged.TierMultipliers, tierMap)
	} else if tierMap, ok := pricingRaw["tierMultipliers"].(map[string]interface{}); ok {
		merged.TierMultipliers = mergeTierMultipliers(merged.TierMultipliers, tierMap)
	}

	return merged
}

func toFloat(values ...interface{}) (float64, bool) {
	for _, value := range values {
		switch v := value.(type) {
		case float64:
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				return v, true
			}
		case float32:
			f := float64(v)
			if !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f, true
			}
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			if f, err := v.Float64(); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func mergeTierMultipliers(base map[string]float64, updates map[string]interface{}) map[string]float64 {
	merged := make(map[string]float64, len(base)+len(updates))
	for k, v := range base {
		merged[k] = v
	}
	for tier, raw := range updates {
		if v, ok := toFloat(raw); ok {
			merged[tier] = v
		}
	}
	return merged
}
