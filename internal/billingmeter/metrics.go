package billingmeter

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Settle durability signals (docs/design/prepaid-credits-v2.md section 5).
// billing_settle_outbox_depth > 0 for more than a few replay intervals means
// the platform DB is refusing debits; billing_settle_failed_total counts
// units that could be parked in neither the wallet nor the outbox: the only
// place money is lost, and the number an operator reconciles by hand from the
// ERROR log lines that carry every field of the unit.
var (
	outboxDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "billing_settle_outbox_depth",
		Help: "Settle units parked in the gateway outbox awaiting replay into the wallet.",
	})
	outboxReplayed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_settle_outbox_replayed_total",
		Help: "Settle units replayed from the outbox into the wallet.",
	})
	settleFailed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_settle_failed_total",
		Help: "Settle units lost: the wallet debit failed and the outbox could not take them.",
	})
	settleParked = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_settle_parked_total",
		Help: "Settle units written to the outbox after the in-process debit failed.",
	})
	settleUnresolvedOrg = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_settle_unresolved_org_total",
		Help: "Platform-key attempts whose tenant resolved to no billable organization; nothing was debited.",
	})
	settleDebited = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_settle_units_total",
		Help: "Settle units handed to the wallet, by modality.",
	}, []string{"modality"})

	// attemptsRecordedUncharged counts attempt records that debit nothing:
	// BYOK attempts, platform retries, failed attempts with no tokens.
	attemptsRecordedUncharged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_attempts_recorded_uncharged_total",
		Help: "Inference attempt records written without a wallet debit, by role and provenance.",
	}, []string{"role", "provenance"})

	// attemptsDroppedSaturated counts uncharged attempt records dropped
	// because the settle queue was saturated or draining.
	attemptsDroppedSaturated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_attempts_dropped_saturated_total",
		Help: "Uncharged inference attempt records dropped because the settle queue was saturated or draining.",
	})

	// Usage display counters (usage.go): a flush failure is retried from
	// memory, so a rising rate means the gateway DB is refusing writes.
	usageFlushFailed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_usage_counter_flush_failed_total",
		Help: "Usage display counter flushes to the gateway DB that failed and were requeued.",
	})
	usageReportFailed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_usage_report_failed_total",
		Help: "Per-organization usage reports to billing that failed; retried on the next tick.",
	})
	// Last tick of each periodic usage reporter, by outcome (reported,
	// skipped, failed). failed > 0 for several ticks means usage is not
	// reaching billing; skipped counts tenants that map to no organization.
	usageReportTickTenants = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "billing_usage_report_tick_tenants",
		Help: "Tenants or organizations in the last usage report tick, by reporter and outcome.",
	}, []string{"reporter", "outcome"})
)

// usageReportLastSuccess is the ingestion-stall signal: the Unix time of the
// last tick that delivered every report it attempted. It starts at the
// reporter's start time, so a reporter that never succeeds still ages and
// pages instead of staying absent. Alert on the max across replicas: a
// replica that is not the elected inference reporter never advances it.
var usageReportLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "billing_usage_report_last_success_timestamp_seconds",
	Help: "Unix time of the last usage report tick that reached billing without failures (initialized to reporter start), by reporter.",
}, []string{"reporter"})

var (
	usageReportLastSuccessMu sync.Mutex
	usageReportLastSuccessAt = map[string]float64{}
)

// UsageReporterStarted initializes the stall clock for a reporter. It never
// moves an existing value backwards.
func UsageReporterStarted(reporter string, now time.Time) {
	usageReportLastSuccessMu.Lock()
	defer usageReportLastSuccessMu.Unlock()
	if v := float64(now.Unix()); usageReportLastSuccessAt[reporter] < v {
		usageReportLastSuccessAt[reporter] = v
		usageReportLastSuccess.WithLabelValues(reporter).Set(v)
	}
}

// UsageReportSucceeded records a tick that delivered every report it tried.
func UsageReportSucceeded(reporter string, now time.Time) {
	usageReportLastSuccessMu.Lock()
	defer usageReportLastSuccessMu.Unlock()
	v := float64(now.Unix())
	usageReportLastSuccessAt[reporter] = v
	usageReportLastSuccess.WithLabelValues(reporter).Set(v)
}

// UsageReportLastSuccess reads the stall clock for one reporter (0 if unset).
func UsageReportLastSuccess(reporter string) float64 {
	usageReportLastSuccessMu.Lock()
	defer usageReportLastSuccessMu.Unlock()
	return usageReportLastSuccessAt[reporter]
}

// UsageReportFailed counts one failed per-organization usage report.
func UsageReportFailed() { usageReportFailed.Inc() }

// SharedUsageReportTick records the outcome counts of one reporter tick.
func SharedUsageReportTick(reporter string, reported, skipped, failed int) {
	usageReportTickTenants.WithLabelValues(reporter, "reported").Set(float64(reported))
	usageReportTickTenants.WithLabelValues(reporter, "skipped").Set(float64(skipped))
	usageReportTickTenants.WithLabelValues(reporter, "failed").Set(float64(failed))
}

// G1 shadow admission (admission.go). No organization labels (cardinality):
// per-org drill-down is the billing.admission.shadow structured log. Deny
// rate by reason:
//
//	sum by (reason) (rate(billing_admission_would_deny_total[1h]))
//	  / ignoring(reason) group_left sum(rate(billing_admission_evaluated_total{outcome!="exempt"}[1h]))
var (
	admissionEvaluated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_admission_evaluated_total",
		Help: "Shadow admission evaluations, by surface (inference, compute) and outcome (admit, would_deny, exempt, skipped_no_org).",
	}, []string{"surface", "outcome"})
	admissionWouldDeny = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_admission_would_deny_total",
		Help: "Shadow admission would-deny decisions (G1: logged, never enforced), by surface, reason and model (resource kind for compute).",
	}, []string{"surface", "reason", "model"})
	admissionShadowPanics = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_admission_shadow_panics_total",
		Help: "Shadow admission evaluations that panicked and were recovered; must stay 0.",
	})
	admissionShadowDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_admission_shadow_dropped_total",
		Help: "Shadow admission evaluations skipped because the bounded shadow runner was saturated.",
	}, []string{"surface"})
)
