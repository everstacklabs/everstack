package v1

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

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
// Known first-cut limitations (shadow phase, tracked for the accuracy follow-up):
// only settled attempts recorded via record() are billed, so tool-loop iterations
// and parallel-fallback losers beyond the served response are under-counted, and a
// request cancelled before any attempt completed records nothing.
type inferenceMeter struct {
	db       *sqlx.DB
	org      string
	reqID    string
	modality string
	markup   float64
	micros   int64
}

// newInferenceMeter builds a metering session for a chat request, or returns nil
// when metering does not apply. The org is resolved from the per-request tenant
// (never a server-scoped identity), so a shared multi-tenant gateway bills the
// right organization.
func newInferenceMeter(ctx context.Context, base *Server, reqID string) *inferenceMeter {
	if base == nil || base.ctx == nil || reqID == "" {
		return nil
	}
	db, _ := base.ctx.Value(contextkeys.BillingDB).(*sqlx.DB)
	if db == nil {
		return nil
	}
	tenantID := contextkeys.ExtractTenantID(ctx)
	org, ok, err := billingmeter.ResolveOrg(ctx, db, tenantID)
	if err != nil || !ok || org == "" {
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

// record adds one completed attempt's cost when it was served by a PLATFORM key.
// BYOK and unknown-source attempts are skipped. estimatedUSD is the catalog cost;
// the wallet is charged catalog x inference markup.
func (m *inferenceMeter) record(keySource string, estimatedUSD float64) {
	if m == nil || keySource != platformKeySource || estimatedUSD <= 0 {
		return
	}
	m.micros += int64(estimatedUSD*m.markup*1_000_000 + 0.5)
}

// settle debits the accumulated platform-key cost. It runs on a context detached
// from the request (context.WithoutCancel) so a cancelled or disconnected request
// still records the spend it incurred, and is idempotent on reqID. Metering only:
// a settle failure is logged, never surfaced to the caller.
func (m *inferenceMeter) settle(parent context.Context) {
	if m == nil || m.micros <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	if err := billingmeter.DebitInference(ctx, m.db, m.org, m.reqID, m.modality, m.micros, time.Now().UTC()); err != nil {
		logger.WithFields("org", m.org, "request_id", m.reqID, "error", err.Error()).
			Warn("gateway: inference metering settle failed")
	}
}
