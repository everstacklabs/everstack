package v1

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/plans"
)

// platformKeySource is the provider_api_keys.Source value for a platform-owned
// key. Only platform-key inference is metered; a BYOK (or unknown-source) attempt
// is never billed against the wallet.
const platformKeySource = "config"

// inferenceMeter records the credit cost of one platform-key inference request
// against its org's fungible wallet. Metering phase only: it accumulates cost as
// attempts complete and settles once, on a detached context, when the request
// ends. It never holds or blocks. A nil *inferenceMeter is a valid no-op (metering
// not applicable: no billing DB, or the tenant maps to no billable org), so every
// method is nil-safe.
//
// Shadow reasons: platform-key cost the wallet does NOT debit today, tracked
// log-only so G1 admission and the billing-accuracy follow-up decide on true
// numbers. G0 metering-truth slice: recording shadow never changes balances.
const (
	// shadowFallbackServed: a fallback winner served the response but bypassed
	// wallet metering (unary + parallel return paths record no wallet cost).
	shadowFallbackServed = "fallback_served_unbilled"
	// shadowParallelLoser: a parallel-race attempt that succeeded upstream but
	// lost the race; its tokens were spent and are unbilled.
	shadowParallelLoser = "parallel_loser"
	// shadowRetryFailed: a failed retry/fallback attempt. Upstream may still
	// have spent input tokens, but no usage is reported, so this is a count
	// with zero attributed cost.
	shadowRetryFailed = "retry_attempt_failed"
	// shadowToolLoop: intermediate tool-loop iterations whose responses are
	// overwritten by the re-invocation; only the final response is billed.
	shadowToolLoop = "tool_loop_iteration"
	// shadowCanceled: the request ended (client cancel) before any attempt
	// settled, so nothing was recorded anywhere.
	shadowCanceled = "canceled_before_settle"
	// shadowUnmeteredProcedure: a served billable procedure with no wallet
	// meter on its path (Responses, embeddings fallbacks). Reported via
	// reportShadowCost, which needs no meter instance.
	shadowUnmeteredProcedure = "unmetered_procedure"
)

// Known first-cut limitations (shadow phase, tracked for the accuracy follow-up):
// only settled attempts recorded via record() are billed. G0 tracks the rest
// as shadow (see reasons above): tool-loop iterations, fallback winners and
// parallel losers, failed retry counts, and cancels-before-settle are observed
// in logs + span attributes without touching the wallet.
type inferenceMeter struct {
	mu       sync.Mutex
	db       *sqlx.DB
	org      string
	reqID    string
	modality string
	markup   float64
	micros   int64
	shadow   map[string]*shadowBucket
}

type shadowBucket struct {
	count  int64
	micros int64
}

// newInferenceMeter builds a metering session for a chat request, or returns nil
// when metering does not apply. The org is resolved from the per-request tenant
// (never a server-scoped identity), so a shared multi-tenant gateway bills the
// right organization.
func newInferenceMeter(ctx context.Context, base *Server, reqID string) *inferenceMeter {
	db, org, ok := resolveMeterOrg(ctx, base)
	if !ok || reqID == "" {
		return nil
	}
	markup := 1.0
	if pc, e := plans.Embedded(); e == nil && pc != nil {
		if cp := pc.GetCreditPricing(); cp != nil && cp.InferenceMarkup > 0 {
			markup = cp.InferenceMarkup
		}
	}
	return &inferenceMeter{db: db, org: org, reqID: reqID, modality: billingmeter.MetricChat, markup: markup}
}

// resolveMeterOrg resolves the billing DB and billable org for a request.
// ok=false means metering (and shadow reporting) does not apply.
func resolveMeterOrg(ctx context.Context, base *Server) (db *sqlx.DB, org string, ok bool) {
	if base == nil || base.ctx == nil {
		return nil, "", false
	}
	db, _ = base.ctx.Value(contextkeys.BillingDB).(*sqlx.DB)
	if db == nil {
		return nil, "", false
	}
	tenantID := contextkeys.ExtractTenantID(ctx)
	org, ok, err := billingmeter.ResolveOrg(ctx, db, tenantID)
	if err != nil || !ok || org == "" {
		return nil, "", false
	}
	return db, org, true
}

// record adds one completed attempt's cost when it was served by a PLATFORM key.
// BYOK and unknown-source attempts are skipped. estimatedUSD is the catalog cost;
// the wallet is charged catalog x inference markup.
func (m *inferenceMeter) record(keySource string, estimatedUSD float64) {
	if m == nil || keySource != platformKeySource || estimatedUSD <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.micros += int64(estimatedUSD*m.markup*1_000_000 + 0.5)
}

// recordShadow tracks platform-key cost the wallet does NOT debit, for G0
// observation only. It mirrors record's billing scope (platform keys; BYOK and
// unknown sources are skipped) so shadow totals read as would-bill totals.
// estimatedUSD may be 0 for count-only observations (failed attempts whose
// upstream spend is unreported). Never touches the wallet.
func (m *inferenceMeter) recordShadow(reason, keySource string, estimatedUSD float64) {
	if m == nil || reason == "" || keySource != platformKeySource || estimatedUSD < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shadow == nil {
		m.shadow = make(map[string]*shadowBucket)
	}
	b := m.shadow[reason]
	if b == nil {
		b = &shadowBucket{}
		m.shadow[reason] = b
	}
	b.count++
	if estimatedUSD > 0 {
		b.micros += int64(estimatedUSD*m.markup*1_000_000 + 0.5)
	}
}

// reportShadowCost logs would-be-billed cost for paths with no wallet meter
// instance (Responses, embeddings fallbacks). Best-effort org resolution;
// log-only, never debits. G0 metering-truth slice.
func reportShadowCost(ctx context.Context, base *Server, reqID, procedure, reason, keySource string, estimatedUSD float64) {
	if reason == "" || keySource != platformKeySource || estimatedUSD < 0 {
		return
	}
	_, org, ok := resolveMeterOrg(ctx, base)
	if !ok {
		return
	}
	micros := int64(0)
	if estimatedUSD > 0 {
		markup := 1.0
		if pc, e := plans.Embedded(); e == nil && pc != nil {
			if cp := pc.GetCreditPricing(); cp != nil && cp.InferenceMarkup > 0 {
				markup = cp.InferenceMarkup
			}
		}
		micros = int64(estimatedUSD*markup*1_000_000 + 0.5)
	}
	logger.WithFields(
		"request_id", reqID,
		"org", org,
		"procedure", procedure,
		"shadow_reason", reason,
		"shadow_micros", micros,
	).Info("gateway: inference shadow cost (unmetered path)")
}

// settle debits the accumulated platform-key cost. It runs on a context detached
// from the request (context.WithoutCancel) so a cancelled or disconnected request
// still records the spend it incurred, and is idempotent on reqID. Metering only:
// a settle failure is logged, never surfaced to the caller.
//
// G0: after the (unchanged) debit, settle reports shadow observations —
// unbilled cost buckets plus cancels-before-settle — via span attributes and
// one structured log line. Shadow reporting never touches the wallet.
func (m *inferenceMeter) settle(parent context.Context, span trace.Span) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.micros > 0 {
		micros := m.micros
		m.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		if err := billingmeter.DebitInference(ctx, m.db, m.org, m.reqID, m.modality, micros, time.Now().UTC()); err != nil {
			logger.WithFields("org", m.org, "request_id", m.reqID, "error", err.Error()).
				Warn("gateway: inference metering settle failed")
		}
		cancel()
		m.mu.Lock()
	}
	canceled := parent.Err() == context.Canceled
	if canceled && m.micros == 0 && len(m.shadow) == 0 {
		if m.shadow == nil {
			m.shadow = make(map[string]*shadowBucket)
		}
		m.shadow[shadowCanceled] = &shadowBucket{count: 1}
	}
	if len(m.shadow) == 0 {
		m.mu.Unlock()
		return
	}
	type shadowLine struct {
		reason string
		count  int64
		micros int64
	}
	lines := make([]shadowLine, 0, len(m.shadow))
	var shadowTotal int64
	for reason, b := range m.shadow {
		lines = append(lines, shadowLine{reason: reason, count: b.count, micros: b.micros})
		shadowTotal += b.micros
	}
	billed := m.micros
	org, reqID := m.org, m.reqID
	m.mu.Unlock()

	if span != nil {
		span.SetAttributes(
			attribute.Int64("metering.shadow_usd_micros", shadowTotal),
			attribute.Int64("metering.billed_usd_micros", billed),
			attribute.Bool("metering.request_canceled", canceled),
		)
		for _, l := range lines {
			span.SetAttributes(
				attribute.Int64("metering.shadow."+l.reason+".count", l.count),
				attribute.Int64("metering.shadow."+l.reason+".micros", l.micros),
			)
		}
	}
	fields := []any{"org", org, "request_id", reqID,
		"billed_micros", billed, "shadow_micros_total", shadowTotal, "canceled", canceled}
	for _, l := range lines {
		fields = append(fields, "shadow."+l.reason+".count", l.count, "shadow."+l.reason+".micros", l.micros)
	}
	logger.WithFields(fields...).Info("gateway: inference shadow metering")
}
