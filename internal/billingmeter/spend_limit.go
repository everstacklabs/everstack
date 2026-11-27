package billingmeter

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/singleflight"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Spend limits on Cloud (managed gateways).
//
// A spend limit is a budget the customer sets for itself: a USD amount per
// day or month in billing.spend_limits, with an action (block, warn,
// notify). Self-hosted gateways enforce it through the license monitor,
// against an estimated-cost counter the gateway feeds. Managed gateways
// never build that monitor, so on Cloud a limit was never evaluated at all.
//
// Here a Cloud limit is evaluated where Everstack credit is spent on
// inference: a platform-key attempt, before dispatch. "Spend" is the
// platform inference debits on billing.credit_ledger in the current UTC
// calendar day or month, the Cloud equivalent of the self-hosted
// estimated-cost counter. BYOK attempts spend the customer's own key and are
// never checked. Sandboxes and other compute are not gated by spend limits
// (self-hosted parity: spend limits gate inference only).
//
// Only enabled, organization-wide (no instance, no API key), legacy
// platform_credit, estimated_cost limits whose action is block are evaluated
// here. all_inference rows (the v2 combined budget) and per-key rows belong
// to the v2 evaluator (spend_eval.go), which runs in shadow. Internal
// (exempt) organizations and internal platform calls are never refused by
// this legacy path. A limit the platform DB cannot read never refuses (fail
// open): a spend limit is the customer's budget, not credit protection,
// which admission does separately.
//
// EVS_SPEND_LIMIT_ENFORCEMENT selects the mode: shadow (the default) only
// counts and logs what would have been refused; enforce refuses.

// ReasonSpendLimitExceeded is the machine-readable refusal reason.
const ReasonSpendLimitExceeded = "spend_limit_exceeded"

// Outcomes of one evaluation (EvaluateSpendLimits) and the metric labels.
const (
	SpendOutcomeNoLimit = "no_limit"
	SpendOutcomeExempt  = "exempt"
	SpendOutcomeAdmit   = "admit"
	// SpendOutcomeOver is the evaluation result; it is reported as
	// would_block in shadow and blocked in enforce.
	SpendOutcomeOver       = "over"
	SpendOutcomeWouldBlock = "would_block"
	SpendOutcomeBlocked    = "blocked"
	SpendOutcomeError      = "error"
	SpendOutcomeDropped    = "dropped"
)

// Scope and basis labels of billing_spend_limit_evaluated_total.
const (
	SpendScopeNone           = "none"
	SpendScopeOrganization   = "organization"
	SpendScopeAPIKey         = "api_key"
	SpendBasisPlatformCredit = "platform_credit"
	SpendBasisAllInference   = "all_inference"
)

var spendLimitEnforced atomic.Bool

// SetSpendLimitEnforcement switches Cloud spend limits between shadow
// (false, the default) and enforce (true).
func SetSpendLimitEnforcement(enforce bool) { spendLimitEnforced.Store(enforce) }

// SpendLimitEnforced reports whether Cloud spend limits refuse requests.
func SpendLimitEnforced() bool { return spendLimitEnforced.Load() }

// ParseSpendLimitEnforcement reads EVS_SPEND_LIMIT_ENFORCEMENT: "" or
// "shadow" is shadow, "enforce" is enforce. Any other value is an error and
// reads as shadow, so a typo never starts refusing customers.
func ParseSpendLimitEnforcement(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "shadow":
		return false, nil
	case "enforce":
		return true, nil
	default:
		return false, fmt.Errorf("EVS_SPEND_LIMIT_ENFORCEMENT must be shadow or enforce, got %q", s)
	}
}

// SpendLimit is one block-action limit and what was spent against it in
// its current period.
type SpendLimit struct {
	Period      string // daily | monthly
	LimitMicros int64
	SpentMicros int64
}

// SpendState is everything one evaluation needs.
type SpendState struct {
	Exempt bool
	Limits []SpendLimit
}

// EvaluateSpendLimits is the rule: exempt organizations are never over;
// otherwise the first limit whose spend has reached it is the one over. A
// zero or negative limit is ignored rather than read as "block everything".
func EvaluateSpendLimits(s SpendState) (string, *SpendLimit) {
	if s.Exempt {
		return SpendOutcomeExempt, nil
	}
	if len(s.Limits) == 0 {
		return SpendOutcomeNoLimit, nil
	}
	for i := range s.Limits {
		l := s.Limits[i]
		if l.LimitMicros > 0 && l.SpentMicros >= l.LimitMicros {
			return SpendOutcomeOver, &l
		}
	}
	return SpendOutcomeAdmit, nil
}

func spendPeriodStarts(now time.Time) (day, month time.Time) {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC),
		time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ReadSpendState reads one organization's evaluable limits, each with the
// organization's platform inference spend in that limit's current period,
// and whether it is an internal organization exempt from caps, read through
// billingidentity.OrganizationExempt (the single sanctioned reader of that
// flag). It only reads, and every row is scoped to orgID. A gateway deployed
// before that column exists reads FALSE.
func ReadSpendState(ctx context.Context, db *sqlx.DB, orgID string, now time.Time) (SpendState, error) {
	var state SpendState
	exempt, err := billingidentity.OrganizationExempt(ctx, db, orgID)
	if err != nil {
		return SpendState{}, err
	}
	state.Exempt = exempt
	day, month := spendPeriodStarts(now)
	var rows []struct {
		Period      string `db:"period"`
		LimitMicros int64  `db:"limit_micros"`
		SpentMicros int64  `db:"spent_micros"`
	}
	if err := db.SelectContext(ctx, &rows, `
		SELECT sl.period,
		       ROUND(sl.limit_amount * 1000000)::BIGINT AS limit_micros,
		       COALESCE((
		           SELECT -SUM(l.amount_micros)
		             FROM billing.credit_ledger l
		            WHERE l.organization_id = sl.organization_id
		              AND l.entry_type = 'debit'
		              AND l.resource_type = $4
		              AND l.created_at >= CASE sl.period WHEN 'daily' THEN $2::timestamptz ELSE $3::timestamptz END
		       ), 0)::BIGINT AS spent_micros
		  FROM billing.spend_limits sl
		 WHERE sl.organization_id = $1::uuid
		   AND sl.enabled
		   AND sl.instance_id IS NULL
		   AND sl.api_key_id IS NULL
		   AND sl.basis = 'platform_credit'
		   AND sl.action_on_exceed = 'block'
		   AND sl.limit_type = 'estimated_cost'
		   AND sl.period IN ('daily', 'monthly')
		 ORDER BY sl.period`, orgID, day, month, inferenceResource); err != nil {
		return SpendState{}, err
	}
	for _, r := range rows {
		state.Limits = append(state.Limits, SpendLimit{Period: r.Period, LimitMicros: r.LimitMicros, SpentMicros: r.SpentMicros})
	}
	return state, nil
}

// SpendLimitExceededError is the refusal a customer sees when a spend limit
// is enforced. Its message names the limit, the period and when it resets,
// and nothing internal.
type SpendLimitExceededError struct {
	Period      string
	LimitMicros int64
	ResetsAt    time.Time
}

func (e *SpendLimitExceededError) Error() string {
	amount := fmt.Sprintf("$%d.%02d", e.LimitMicros/1_000_000, (e.LimitMicros%1_000_000)/10_000)
	period := "monthly"
	if e.Period == "daily" {
		period = "daily"
	}
	return fmt.Sprintf("Your organization has reached its %s spend limit of %s. "+
		"Requests that use Everstack credit resume when the limit resets at 00:00 UTC on %s. "+
		"To change the limit, contact support@everstack.ai.",
		period, amount, e.ResetsAt.UTC().Format("2 January 2006"))
}

// Reason is the machine-readable refusal reason.
func (e *SpendLimitExceededError) Reason() string { return ReasonSpendLimitExceeded }

// HTTPStatusCode is 402: the refusal is not retried or failed over
// (retrypolicy reads this), and matches the self-hosted REST spend refusal.
func (e *SpendLimitExceededError) HTTPStatusCode() int { return http.StatusPaymentRequired }

// SpendLimitError builds the refusal for a limit that is over at now, as a
// Connect ResourceExhausted error (the self-hosted spend refusal's code; never
// a code the dashboard reads as a lost session).
func SpendLimitError(l SpendLimit, now time.Time) error {
	day, month := spendPeriodStarts(now)
	resets := month.AddDate(0, 1, 0)
	if l.Period == "daily" {
		resets = day.AddDate(0, 0, 1)
	}
	cErr := connect.NewError(connect.CodeResourceExhausted, &SpendLimitExceededError{
		Period: l.Period, LimitMicros: l.LimitMicros, ResetsAt: resets,
	})
	// The reason as a google.rpc.ErrorInfo detail is what survives the gRPC
	// hop to grpc-gateway: the HTTP layer (middleware.SpendLimitStatus)
	// reads it to answer 402 instead of the 429 ResourceExhausted maps to.
	if detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason: ReasonSpendLimitExceeded,
		Domain: "everstack.ai",
		Metadata: map[string]string{
			"period":       l.Period,
			"limit_micros": strconv.FormatInt(l.LimitMicros, 10),
		},
	}); err == nil {
		cErr.AddDetail(detail)
	}
	return cErr
}

// spendCacheTTL bounds how stale an enforce-mode decision may be. Spend is
// debited after each attempt settles, so the limit can be overshot by up to
// this window's worth of requests; in exchange the request path reads the
// platform DB at most once per organization per window.
const spendCacheTTL = 30 * time.Second

// spendReadTimeout bounds one cache-miss read of the platform DB, on its own
// context: the request's context usually has no deadline, and a slow read
// must fail open quickly rather than hold the request.
const spendReadTimeout = 2 * time.Second

// spendCache memoizes ReadSpendState per organization for spendCacheTTL.
// Concurrent misses for one organization share a single read
// (singleflight), which runs detached from any one caller's cancellation
// under spendReadTimeout; a caller that gives up returns at once.
type spendCache struct {
	mu    sync.Mutex
	m     map[string]spendCacheEntry
	group singleflight.Group
	// read and timeout override ReadSpendState and spendReadTimeout in tests.
	read    func(ctx context.Context, db *sqlx.DB, orgID string, now time.Time) (SpendState, error)
	timeout time.Duration
}

type spendCacheEntry struct {
	state SpendState
	at    time.Time
}

func (c *spendCache) get(ctx context.Context, db *sqlx.DB, orgID string, now time.Time) (SpendState, error) {
	c.mu.Lock()
	if e, ok := c.m[orgID]; ok && now.Sub(e.at) < spendCacheTTL && !now.Before(e.at) {
		c.mu.Unlock()
		return e.state, nil
	}
	c.mu.Unlock()
	read, timeout := c.read, c.timeout
	if read == nil {
		read = ReadSpendState
	}
	if timeout <= 0 {
		timeout = spendReadTimeout
	}
	ch := c.group.DoChan(orgID, func() (any, error) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		state, err := read(rctx, db, orgID, now)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.m == nil {
			c.m = map[string]spendCacheEntry{}
		}
		c.m[orgID] = spendCacheEntry{state: state, at: now}
		c.mu.Unlock()
		return state, nil
	})
	select {
	case <-ctx.Done():
		return SpendState{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return SpendState{}, res.Err
		}
		return res.Val.(SpendState), nil
	}
}

func (c *spendCache) forget(orgID string) {
	c.mu.Lock()
	delete(c.m, orgID)
	c.mu.Unlock()
}

func (m *Meter) spendClock() time.Time {
	if m.spendNow != nil {
		return m.spendNow()
	}
	return time.Now()
}

// CheckSpendLimit evaluates the organization's spend limits for one
// platform-key inference attempt. In shadow it never returns an error: the
// evaluation runs off the request path and is counted and logged. In
// enforce it returns SpendLimitError when a limit is reached. Internal
// platform calls, an unresolved organization and an inactive meter are
// never checked; a failed read never refuses.
func (m *Meter) CheckSpendLimit(ctx context.Context, orgID string, internal bool) error {
	orgID = strings.TrimSpace(orgID)
	if !m.Active() || internal || orgID == "" {
		return nil
	}
	if !SpendLimitEnforced() {
		ok := m.shadow.try(func(ctx context.Context) {
			state, err := m.spendCache.get(ctx, m.billingDB, orgID, m.spendClock())
			if err != nil {
				reportSpendLimitError(orgID, "shadow", err)
				return
			}
			outcome, over := EvaluateSpendLimits(state)
			reportSpendLimit(orgID, "shadow", outcome, over)
		})
		if !ok {
			spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeDropped, SpendScopeNone, SpendBasisPlatformCredit).Inc()
		}
		return nil
	}
	now := m.spendClock()
	state, err := m.spendCache.get(ctx, m.billingDB, orgID, now)
	if err != nil {
		reportSpendLimitError(orgID, "enforce", err)
		return nil
	}
	outcome, over := EvaluateSpendLimits(state)
	reportSpendLimit(orgID, "enforce", outcome, over)
	if outcome == SpendOutcomeOver && over != nil {
		return SpendLimitError(*over, now)
	}
	return nil
}

// spendLimitEvaluated counts every Cloud spend limit evaluation by mode
// (shadow, enforce), outcome, scope (organization or api_key for a refusal
// outcome, none otherwise) and basis (platform_credit: the legacy #891 path
// on platform-key attempts; all_inference: the v2 evaluator on every
// attempt). No organization label: per-organization detail goes to the
// rate-limited log. error, stale and open count failed reads, not attempts.
// What enforcing would refuse today:
//
//	sum by (basis, scope) (rate(billing_spend_limit_evaluated_total{mode="shadow",outcome=~"would_block|pending_block"}[1h]))
var spendLimitEvaluated = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "billing_spend_limit_evaluated_total",
	Help: "Cloud spend limit evaluations by mode (shadow, enforce), outcome (no_limit, exempt, admit, would_block, pending_block, blocked, error, stale, open, dropped), scope (organization, api_key, none) and basis (platform_credit, all_inference).",
}, []string{"mode", "outcome", "scope", "basis"})

var spendLogLimiter = &logLimiter{interval: time.Minute}

func reportSpendLimit(orgID, mode, outcome string, over *SpendLimit) {
	if outcome != SpendOutcomeOver {
		spendLimitEvaluated.WithLabelValues(mode, outcome, SpendScopeNone, SpendBasisPlatformCredit).Inc()
		return
	}
	label := SpendOutcomeWouldBlock
	if mode == "enforce" {
		label = SpendOutcomeBlocked
	}
	spendLimitEvaluated.WithLabelValues(mode, label, SpendScopeOrganization, SpendBasisPlatformCredit).Inc()
	suppressed, emit := spendLogLimiter.allow(orgID+"|"+label, time.Now())
	if !emit || over == nil {
		return
	}
	logger.WithFields(
		"organization_id", orgID,
		"mode", mode,
		"outcome", label,
		"period", over.Period,
		"limit_micros", over.LimitMicros,
		"spent_micros", over.SpentMicros,
		"suppressed", suppressed,
	).Warn("billing.spend_limit")
}

func reportSpendLimitError(orgID, mode string, err error) {
	spendLimitEvaluated.WithLabelValues(mode, SpendOutcomeError, SpendScopeNone, SpendBasisPlatformCredit).Inc()
	if _, emit := spendLogLimiter.allow(orgID+"|error", time.Now()); emit {
		logger.WithFields("organization_id", orgID, "mode", mode, "error", err.Error()).
			Warn("billing.spend_limit: could not read the spend limit; request allowed")
	}
}
