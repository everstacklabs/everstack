package billingmeter

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Admission (docs/design/prepaid-credits-v2.md sections 5 and 6.1). The model
// is an atomic reservation per settle unit: PlaceHoldIfAvailable admits when
// available >= estimate + credit_limit_micros under the org lock. G1 (S4)
// runs the same decision in dry-run: it reads the wallet uncached, computes
// the decision, logs and counts would-deny, and writes nothing. Nothing in
// this file denies anything: S5 flips the dry-run to the hold.

// InferenceAdmissionEnforced reports whether this binary refuses platform-key
// inference when the wallet cannot cover it. It is false while admission runs
// only in shadow (ShadowAdmit logs would-deny and never refuses, and
// PlaceHoldIfAvailable has no caller on the request path). The gateway keeps
// the platform key pool closed while this is false, so the PR that ships the
// enforcing hold flips this one constant.
const InferenceAdmissionEnforced = false

// Wallet states on billing.credit_balances.wallet_state. S4 adds active and
// frozen; S8 extends the CHECK with deleted. Any state other than active is
// treated as frozen, so a state this binary does not know fails closed.
const (
	WalletActive = "active"
	WalletFrozen = "frozen"
)

// Typed admission reasons. These are the machine-readable reasons S5 returns
// with the denial and the label values of billing_admission_would_deny_total.
const (
	ReasonInsufficientCredit      = "insufficient_credit"
	ReasonWalletFrozen            = "wallet_frozen"
	ReasonModelNotInFreeAllowlist = "model_not_in_free_allowlist"
	ReasonInflightLimit           = "inflight_limit"
	ReasonUnpricedModel           = "unpriced_model"
	// ReasonArbiterUnavailable is "the platform DB could not serve the
	// reservation". In enforce mode it is Unavailable, never 402, so the
	// FE never shows "add credit" for an outage.
	ReasonArbiterUnavailable = "arbiter_unavailable"
)

// Would-be status codes S5 returns for each decision. FailedPrecondition is
// the credit and policy denial (REST 402 for insufficient_credit); never codes
// 7 or 16, which the cloud auth interceptor treats as a lost session.
const (
	CodeOK                 = "ok"
	CodeFailedPrecondition = "failed_precondition"
	CodeResourceExhausted  = "resource_exhausted"
	CodeUnavailable        = "unavailable"
)

// Admission surfaces, the "surface" metric label.
const (
	SurfaceInference = "inference"
)

// Wallet is one uncached read of an organization's spendable state.
type Wallet struct {
	OrgID         string
	BalanceMicros int64
	HeldMicros    int64
	// State is billing.credit_balances.wallet_state ("active" when the org
	// has no balance row yet).
	State string
	// HasPurchase, PaidSubscription and PlanTier derive the tier for the
	// free allowlist: an org is free unless it has bought credit, holds a
	// live (active or trialing) paid subscription, or is on an enterprise
	// contract. Cloud keeps subscription plans alongside prepaid credit, so
	// a basic or pro subscriber is paid even if it never bought a top-up.
	// All three are read server-side for the org the gateway resolved.
	// S9 replaces this with the derived-tier resolver.
	HasPurchase      bool
	PaidSubscription bool
	PlanTier         string
	// CreditLimitMicros is the enterprise postpaid seam: admit while
	// available - estimate >= CreditLimitMicros. 0 for everyone (prepaid);
	// a negative value lets the wallet go that far below zero. The
	// organization column lands in S5, so S4 always reads 0.
	CreditLimitMicros int64
}

// Available is balance minus active holds, exactly what PlaceHoldIfAvailable
// compares against (services/billing credit_ledger_repo.go availableTx).
func (w Wallet) Available() int64 { return w.BalanceMicros - w.HeldMicros }

// Free reports whether the free-tier rules (the cheap-model allowlist) apply.
func (w Wallet) Free() bool {
	return !w.HasPurchase && !w.PaidSubscription && !strings.EqualFold(strings.TrimSpace(w.PlanTier), "enterprise")
}

// AdmissionInput is everything one admission decision needs. It is plain
// data so the decision is a pure function and the matrix is testable without
// a database.
type AdmissionInput struct {
	// Wallet is the uncached wallet read; nil with WalletErr set when the
	// platform DB could not serve it.
	Wallet    *Wallet
	WalletErr error
	// EstimateMicros is the reservation the attempt would place (marked up).
	// 0 for compute admission (wallet state and balance only).
	EstimateMicros int64
	// Compute marks a compute admission (sandbox, browser): no model, so the
	// free allowlist does not apply.
	Compute bool
	// FreeAllowed is whether the model is on plans.json
	// free_inference_allowlist for this provider.
	FreeAllowed bool
	// Unpriced marks a model the catalog cannot price.
	Unpriced bool
	// Internal marks a platform-machinery call (6.1b): debited, never denied.
	Internal bool
	// Inflight is this org's in-flight platform attempts on this replica,
	// including this one; InflightCap is the guardrail (0 = no cap).
	Inflight    int
	InflightCap int
}

// Decision is the admission outcome. Admit=false is a would-deny in G1.
type Decision struct {
	Admit  bool
	Reason string
	Code   string
	// Exempt marks an internal call that is admitted without evaluation.
	Exempt bool
}

func deny(reason, code string) Decision { return Decision{Reason: reason, Code: code} }

// EvaluateAdmission is the admission rule, first match wins: internal calls
// are exempt; then the checks that need no database (unpriced model,
// in-flight guardrail); then the platform DB must have answered (else
// Unavailable); then wallet state, the free allowlist, and the balance
// against estimate + credit limit.
func EvaluateAdmission(in AdmissionInput) Decision {
	if in.Internal {
		return Decision{Admit: true, Code: CodeOK, Exempt: true}
	}
	if in.Unpriced {
		return deny(ReasonUnpricedModel, CodeFailedPrecondition)
	}
	if in.InflightCap > 0 && in.Inflight > in.InflightCap {
		return deny(ReasonInflightLimit, CodeResourceExhausted)
	}
	if in.WalletErr != nil || in.Wallet == nil {
		return deny(ReasonArbiterUnavailable, CodeUnavailable)
	}
	w := in.Wallet
	if state := strings.TrimSpace(w.State); state != "" && state != WalletActive {
		return deny(ReasonWalletFrozen, CodeFailedPrecondition)
	}
	if !in.Compute && w.Free() && !in.FreeAllowed {
		return deny(ReasonModelNotInFreeAllowlist, CodeFailedPrecondition)
	}
	estimate := in.EstimateMicros
	if estimate < 0 {
		estimate = 0
	}
	available := w.Available()
	// available >= estimate + limit, and strictly above the limit so a zero
	// estimate (compute) at exactly the floor is not admitted: that is the
	// active -> frozen point (6.5).
	if available-estimate < w.CreditLimitMicros || available <= w.CreditLimitMicros {
		return deny(ReasonInsufficientCredit, CodeFailedPrecondition)
	}
	return Decision{Admit: true, Code: CodeOK}
}

// ReadWallet is the uncached money read: balance, active holds and wallet
// state in one statement, mirroring availableTx. wallet_state is read through
// to_jsonb so the statement keeps working on a platform DB where the S4
// migration has not run yet (deploy order between gateway and billing): the
// state then reads "active", which is what the column default would say.
// It takes no lock and writes nothing.
func ReadWallet(ctx context.Context, db *sqlx.DB, orgID string) (Wallet, error) {
	w := Wallet{OrgID: orgID, State: WalletActive}
	var row struct {
		Balance int64  `db:"balance"`
		Held    int64  `db:"held"`
		State   string `db:"wallet_state"`
	}
	err := db.GetContext(ctx, &row, `
		SELECT
			COALESCE((SELECT b.balance_micros FROM billing.credit_balances b WHERE b.organization_id = $1::uuid), 0)::BIGINT AS balance,
			COALESCE((SELECT SUM(h.amount_micros) FROM billing.credit_holds h WHERE h.organization_id = $1::uuid AND h.state = 'held'), 0)::BIGINT AS held,
			COALESCE((SELECT to_jsonb(b) ->> 'wallet_state' FROM billing.credit_balances b WHERE b.organization_id = $1::uuid), 'active') AS wallet_state`,
		orgID)
	if err != nil {
		return w, err
	}
	w.BalanceMicros, w.HeldMicros, w.State = row.Balance, row.Held, row.State
	return w, nil
}

// walletTier is the slow-moving tier input for the free allowlist.
type walletTier struct {
	HasPurchase      bool   `db:"has_purchase"`
	PaidSubscription bool   `db:"paid_subscription"`
	PlanTier         string `db:"plan_tier"`
}

// ReadTier reads whether the org has ever purchased credit, whether it holds
// a live paid subscription (active or trialing on a tier other than free),
// and its plan tier. Unlike the money read this may be cached: it changes on
// a purchase or a plan change (rare) and only selects which rule applies,
// never the balance compared.
func ReadTier(ctx context.Context, db *sqlx.DB, orgID string) (walletTier, error) {
	var t walletTier
	err := db.GetContext(ctx, &t, `
		SELECT
			EXISTS (SELECT 1 FROM billing.credit_ledger l WHERE l.organization_id = $1::uuid AND l.entry_type = 'purchase' AND l.amount_micros > 0) AS has_purchase,
			EXISTS (SELECT 1 FROM billing.subscriptions s WHERE s.organization_id = $1::uuid
				AND LOWER(TRIM(s.status)) IN ('active', 'trialing')
				AND LOWER(TRIM(s.plan_tier)) NOT IN ('', 'free')) AS paid_subscription,
			COALESCE((SELECT o.plan_tier FROM everstack.organizations o WHERE o.id = $1::uuid), '') AS plan_tier`,
		orgID)
	return t, err
}

// tierCache memoizes ReadTier per org. Keyed by org id only.
type tierCache struct {
	mu    sync.Mutex
	byOrg map[string]tierEntry
}

type tierEntry struct {
	tier    walletTier
	expires time.Time
}

const tierTTL = 5 * time.Minute

func (c *tierCache) get(ctx context.Context, db *sqlx.DB, orgID string) (walletTier, error) {
	now := time.Now()
	c.mu.Lock()
	e, ok := c.byOrg[orgID]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.tier, nil
	}
	t, err := ReadTier(ctx, db, orgID)
	if err != nil {
		return t, err
	}
	c.mu.Lock()
	if c.byOrg == nil {
		c.byOrg = make(map[string]tierEntry)
	}
	c.byOrg[orgID] = tierEntry{tier: t, expires: now.Add(tierTTL)}
	c.mu.Unlock()
	return t, nil
}

// readAdmissionWallet is the full wallet input: the uncached money read plus
// the (optionally cached) tier.
func readAdmissionWallet(ctx context.Context, db *sqlx.DB, tiers *tierCache, orgID string) (*Wallet, error) {
	w, err := ReadWallet(ctx, db, orgID)
	if err != nil {
		return nil, err
	}
	var t walletTier
	if tiers != nil {
		t, err = tiers.get(ctx, db, orgID)
	} else {
		t, err = ReadTier(ctx, db, orgID)
	}
	if err != nil {
		return nil, err
	}
	w.HasPurchase, w.PaidSubscription, w.PlanTier = t.HasPurchase, t.PaidSubscription, t.PlanTier
	return &w, nil
}

// AdmissionRequest is one shadow admission the provider middleware hands the
// meter at dispatch. Plain data, captured by value.
type AdmissionRequest struct {
	Surface    string
	OrgID      string
	ResolveErr error
	Provider   string
	// Model is the requested model (logged); ModelLabel is the bounded
	// metric label (the catalog name, or the resource kind for compute).
	// Empty ModelLabel falls back to Model.
	Model      string
	ModelLabel string
	Modality   string
	KeyID      string
	SettleUnit string
	// CorrelationID ties the shadow decision to the request's trace.
	CorrelationID  string
	EstimateMicros int64
	FreeAllowed    bool
	Internal       bool
	Inflight       int
	InflightCap    int
}

// shadowRunner bounds the asynchronous shadow reads. In G1 the decision is
// never acted on, so a synchronous platform-DB round trip on every provider
// attempt would buy nothing and cost latency; the read runs off the request
// goroutine, uncached, at dispatch time. When the runner is saturated the
// evaluation is dropped and counted, never queued and never blocking.
type shadowRunner struct {
	sem     chan struct{}
	timeout time.Duration
	wg      sync.WaitGroup
}

const (
	shadowConcurrency = 16
	shadowReadTimeout = 2 * time.Second
)

func newShadowRunner(n int, timeout time.Duration) *shadowRunner {
	return &shadowRunner{sem: make(chan struct{}, n), timeout: timeout}
}

// try runs fn asynchronously when a slot is free and reports whether it did.
func (r *shadowRunner) try(fn func(ctx context.Context)) bool {
	select {
	case r.sem <- struct{}{}:
	default:
		return false
	}
	r.wg.Add(1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				admissionShadowPanics.Inc()
				logger.WithFields("panic", p).Error("billing admission shadow: evaluation panicked; request unaffected")
			}
			<-r.sem
			r.wg.Done()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()
		fn(ctx)
	}()
	return true
}

// wait blocks until every running evaluation finished (tests).
func (r *shadowRunner) wait() { r.wg.Wait() }

// ShadowAdmit evaluates one platform-key attempt in dry-run and reports the
// decision through metrics and the structured log. It never blocks the
// caller and never denies. The decisions that need no database (internal,
// in-flight, org-resolution failure) are made inline; the rest run on the
// bounded shadow runner.
func (m *Meter) ShadowAdmit(req AdmissionRequest) {
	if !m.Active() {
		return
	}
	req.Surface = SurfaceInference
	in := AdmissionInput{
		EstimateMicros: req.EstimateMicros,
		FreeAllowed:    req.FreeAllowed,
		Internal:       req.Internal,
		Inflight:       req.Inflight,
		InflightCap:    req.InflightCap,
	}
	switch {
	case req.Internal:
		reportShadowAdmission(req, nil, EvaluateAdmission(in))
		return
	case req.ResolveErr != nil:
		in.WalletErr = req.ResolveErr
		reportShadowAdmission(req, nil, EvaluateAdmission(in))
		return
	case req.OrgID == "":
		admissionEvaluated.WithLabelValues(SurfaceInference, "skipped_no_org").Inc()
		return
	}
	if in.InflightCap > 0 && in.Inflight > in.InflightCap {
		reportShadowAdmission(req, nil, EvaluateAdmission(in))
		return
	}
	ok := m.shadow.try(func(ctx context.Context) {
		w, err := readAdmissionWallet(ctx, m.billingDB, &m.tiers, req.OrgID)
		in.Wallet, in.WalletErr = w, err
		reportShadowAdmission(req, w, EvaluateAdmission(in))
	})
	if !ok {
		admissionShadowDropped.WithLabelValues(SurfaceInference).Inc()
	}
}

// Compute admission surfaces (metric label values). They match the sandbox
// package's ComputeSurface* constants and the browser lease site.
const (
	SurfaceSandboxCreate = "sandbox_create"
	SurfaceSandboxRevive = "sandbox_revive"
	SurfaceBrowserLease  = "browser_lease"
)

var (
	computeShadow = newShadowRunner(shadowConcurrency, shadowReadTimeout)
	computeTiers  tierCache
	computeOrgs   orgCache
)

// ShadowAdmitComputeTenant evaluates one real compute allocation (sandbox
// create or revive, hosted browser lease) in dry-run with a zero estimate:
// wallet state and balance only, as S5's Admit will. Everything that
// touches the database (tenant to organization resolution and the uncached
// wallet read) runs on the bounded shadow runner, so the allocation path pays
// no extra synchronous query. db must be the platform DB of a managed
// gateway; callers on self-hosted never install it.
func ShadowAdmitComputeTenant(db *sqlx.DB, tenantID, surface string) {
	tenantID = strings.TrimSpace(tenantID)
	if db == nil || tenantID == "" {
		return
	}
	ok := computeShadow.try(func(ctx context.Context) {
		org, found, err := computeOrgs.resolve(ctx, db, tenantID, ResolveOrg)
		req := AdmissionRequest{Surface: surface, OrgID: org, ModelLabel: "compute", ResolveErr: err}
		if err == nil && !found {
			admissionEvaluated.WithLabelValues(surface, "skipped_no_org").Inc()
			return
		}
		in := AdmissionInput{Compute: true, WalletErr: err}
		var w *Wallet
		if err == nil {
			w, in.WalletErr = readAdmissionWallet(ctx, db, &computeTiers, org)
			in.Wallet = w
		}
		reportShadowAdmission(req, w, EvaluateAdmission(in))
	})
	if !ok {
		admissionShadowDropped.WithLabelValues(surface).Inc()
	}
}

// reportShadowAdmission is the single G1 sink: every evaluation is counted;
// a would-deny is also counted by reason and logged (rate limited per org and
// reason, with the suppressed count).
func reportShadowAdmission(req AdmissionRequest, w *Wallet, d Decision) {
	surface := req.Surface
	if surface == "" {
		surface = SurfaceInference
	}
	switch {
	case d.Exempt:
		admissionEvaluated.WithLabelValues(surface, "exempt").Inc()
		return
	case d.Admit:
		admissionEvaluated.WithLabelValues(surface, "admit").Inc()
		return
	}
	admissionEvaluated.WithLabelValues(surface, "would_deny").Inc()
	label := req.ModelLabel
	if label == "" {
		label = req.Model
	}
	if label == "" {
		label = "unknown"
	}
	admissionWouldDeny.WithLabelValues(surface, d.Reason, label).Inc()

	suppressed, emit := shadowLogLimiter.allow(req.OrgID+"|"+surface+"|"+d.Reason, time.Now())
	if !emit {
		return
	}
	fields := map[string]interface{}{
		"org":             req.OrgID,
		"surface":         surface,
		"reason":          d.Reason,
		"would_be_code":   d.Code,
		"model":           req.Model,
		"provider":        req.Provider,
		"modality":        req.Modality,
		"key_id":          req.KeyID,
		"settle_unit":     req.SettleUnit,
		"correlation_id":  req.CorrelationID,
		"estimate_micros": req.EstimateMicros,
		"inflight":        req.Inflight,
		"inflight_cap":    req.InflightCap,
		"suppressed":      suppressed,
	}
	if w != nil {
		fields["available_micros"] = w.Available()
		fields["balance_micros"] = w.BalanceMicros
		fields["held_micros"] = w.HeldMicros
		fields["wallet_state"] = w.State
		fields["free"] = w.Free()
		fields["paid_subscription"] = w.PaidSubscription
		fields["plan_tier"] = w.PlanTier
		fields["credit_limit_micros"] = w.CreditLimitMicros
	}
	if req.ResolveErr != nil {
		fields["error"] = req.ResolveErr.Error()
	}
	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent("billing.admission.shadow").
		WithFields(fields).
		Info("billing admission shadow: would deny (not enforced)")
}

// logLimiter emits at most one log line per key per interval and reports how
// many were suppressed since the last one. Counts are exact in the metrics;
// the log is for per-org drill-down, so one line per org and reason per
// interval is enough and an org at $0 cannot flood the pipeline.
type logLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	byKey    map[string]*logLimit
}

type logLimit struct {
	last       time.Time
	suppressed int
}

func (l *logLimiter) allow(key string, now time.Time) (suppressed int, emit bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byKey == nil {
		l.byKey = make(map[string]*logLimit)
	}
	e, ok := l.byKey[key]
	if !ok {
		if len(l.byKey) > 10_000 {
			// Bound memory: forget everything; worst case a burst of
			// first lines.
			l.byKey = make(map[string]*logLimit)
		}
		l.byKey[key] = &logLimit{last: now}
		return 0, true
	}
	if now.Sub(e.last) < l.interval {
		e.suppressed++
		return 0, false
	}
	suppressed = e.suppressed
	e.last, e.suppressed = now, 0
	return suppressed, true
}

var shadowLogLimiter = &logLimiter{interval: 30 * time.Second}
