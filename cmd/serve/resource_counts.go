package serve

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
)

// liveSandboxMgr holds the sandbox manager once it's constructed (later in
// startup than the usage-syncer wiring). collectResourceCounts reads it to
// add in-flight sandboxes' live network bytes, which haven't yet been written
// to the usage ledger. Atomic so the syncer goroutine reads it race-free.
var liveSandboxMgr atomic.Pointer[sandbox.SandboxManager]
var liveBrowserUsageMeter atomic.Pointer[browserpool.PostgresUsageMeter]

// SetLiveSandboxManager registers the sandbox manager for live network
// accrual. Safe to call after the syncer is already running.
func SetLiveSandboxManager(m *sandbox.SandboxManager) {
	if m != nil {
		liveSandboxMgr.Store(m)
	}
}

// SetLiveBrowserUsageMeter registers the durable browser lease meter for
// license telemetry. It reports a lifetime-monotonic source value; central
// billing owns period assignment and Stripe idempotency.
func SetLiveBrowserUsageMeter(m *browserpool.PostgresUsageMeter) {
	if m != nil {
		liveBrowserUsageMeter.Store(m)
	}
}

// collectResourceCounts runs lightweight COUNT queries against the local
// gateway DB to build a per-instance resource snapshot for the usage report.
// Each query has its own short timeout so a slow table never blocks the rest.
func collectResourceCounts(ctx context.Context, db *sqlx.DB) licensemonitor.ResourceCounts {
	if db == nil {
		return licensemonitor.ResourceCounts{}
	}

	count := func(query string, args ...interface{}) int64 {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var n int64
		if err := db.GetContext(qctx, &n, query, args...); err != nil {
			return 0
		}
		return n
	}

	bigint := func(query string, args ...interface{}) int64 {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var n int64
		if err := db.GetContext(qctx, &n, query, args...); err != nil {
			return 0
		}
		return n
	}

	rc := licensemonitor.ResourceCounts{
		Agents:            count(`SELECT COUNT(*) FROM agent_definitions WHERE deleted_at IS NULL`),
		PersistentAgents:  count(`SELECT COUNT(*) FROM agent_definitions WHERE deleted_at IS NULL AND lifecycle_mode = 'persistent'`),
		ConcurrentRunning: count(`SELECT COUNT(*) FROM agent_definitions WHERE deleted_at IS NULL AND lifecycle_status = 'running'`),
		ConcurrentSandboxes: count(`
			SELECT COUNT(*)
			FROM sandbox_instances
			WHERE lifecycle_state IN (
			  'pending', 'creating', 'provisioning', 'running',
			  'stopping', 'reviving', 'terminating'
			)
			   OR (billing_started_at IS NOT NULL AND billing_ended_at IS NULL)`),
		DatasetItems:     count(`SELECT COUNT(*) FROM dataset_items`),
		EvalRunsMonthly:  count(`SELECT COUNT(*) FROM eval_runs WHERE created_at >= date_trunc('month', NOW())`),
		AnnotationQueues: count(`SELECT COUNT(*) FROM annotation_queues WHERE deleted_at IS NULL`),
		ChannelBindings:  count(`SELECT COUNT(*) FROM agent_channel_bindings WHERE deleted_at IS NULL`),
		MessagesMonthly:  count(`SELECT COUNT(*) FROM channel_messages WHERE created_at >= date_trunc('month', NOW())`),
		StorageBytes:     bigint(`SELECT COALESCE(SUM(total_bytes), 0) FROM object_storage_usage`),
		// Sandbox network data transfer is a flow metric: sum the per-VM
		// totals recorded at teardown over the current billing month.
		NetworkRxBytes: bigint(`SELECT COALESCE(SUM(network_rx_bytes), 0) FROM sandbox_usage_records WHERE created_at >= date_trunc('month', NOW())`),
		NetworkTxBytes: bigint(`SELECT COALESCE(SUM(network_tx_bytes), 0) FROM sandbox_usage_records WHERE created_at >= date_trunc('month', NOW())`),
		// Compute is a lifetime-monotonic source meter. Central billing owns
		// billing-period assignment and persists the last-seen watermark.
		SandboxComputeSeconds: bigint(`SELECT COALESCE(SUM(duration_seconds), 0) FROM sandbox_usage_records`),
	}
	var closedComputeCostUSD float64
	costCtx, costCancel := context.WithTimeout(ctx, 2*time.Second)
	_ = db.GetContext(costCtx, &closedComputeCostUSD, `SELECT COALESCE(SUM(cost_total_usd), 0) FROM sandbox_usage_records`)
	costCancel()

	// Add in-flight sandboxes' live network bytes. The ledger SUM above only
	// covers torn-down VMs; running VMs haven't written a record yet, so
	// without this a long-running (persistent) sandbox would report ~0 all
	// period. A VM is either running or torn-down, never both, so no double
	// count. Mirrors LiveAccruedCost's running + ledger pattern for cost.
	if mgr := liveSandboxMgr.Load(); mgr != nil {
		if agg := mgr.AggregateStats(ctx); agg != nil {
			rc.NetworkRxBytes += agg.NetworkRxBytes
			rc.NetworkTxBytes += agg.NetworkTxBytes
		}
		liveCostUSD, liveSeconds := mgr.LiveAccruedCostAll(ctx)
		rc.SandboxComputeSeconds += liveSeconds
		closedComputeCostUSD += liveCostUSD
	}
	// Round only after closed-ledger and open-window costs are combined. That
	// keeps the cumulative meter bit-for-bit stable when a live window becomes
	// a ledger row; rounding each side independently can differ by one micro.
	rc.SandboxComputeCostMicros = int64(math.Round(closedComputeCostUSD * 1_000_000))
	if meter := liveBrowserUsageMeter.Load(); meter != nil {
		meterCtx, meterCancel := context.WithTimeout(ctx, 2*time.Second)
		totals, err := meter.Totals(meterCtx, time.Now())
		meterCancel()
		if err == nil {
			rc.BrowserRuntimeSeconds = totals.RuntimeSeconds
			rc.BrowserRuntimeCostMicros = totals.CostMicros
		}
	}

	return rc
}
