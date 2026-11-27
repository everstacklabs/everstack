// Package billingallowance owns the Cloud plan compute allowances
// (docs/design/cloud-plan-allowances.md, slice 1): how much sandbox CPU,
// sandbox memory and hosted browser time each plan includes per period, which
// period an organization is in, and how much of each allowance is used.
//
// It is shared by the two places that must agree on it: the billing
// service's usage-debit loop, which charges only usage beyond the allowances,
// and the compute admission gate (internal/billingcredit.Resolve), which
// admits an organization with allowance left whatever its wallet holds.
//
// # Meters
//
// Money is charged from the meters the debit loop has always charged:
// sandbox_compute_cost_micros and browser_runtime_cost_micros, the gateway's
// cost in micro-dollars. Each allowance is counted on its own counter:
//
//   - vCPU-hours on sandbox_cpu_cost_micros, the CPU part of the sandbox cost;
//   - GiB-hours on sandbox_memory_cost_micros, the memory part;
//   - browser-hours on browser_runtime_cost_micros itself.
//
// The counters come from the same pricing function as the money meter
// (computeSandboxCost, whose per-resource breakdown every usage row already
// stores), so the CPU and memory pools are counted separately whatever the
// sandbox shape, and each resource's overage is charged at its own rate: a
// sandbox debit is the money delta minus the CPU and memory cost that fell
// inside their allowances. An allowance is held in its counter's unit, list
// price micro-dollars: 40 vCPU-hours is 40 x $0.0504. Hours shown to a
// customer are counter micros divided by the list price of one hour. A tier
// discount the gateway applies (Scale is priced at 0.93 of list) stretches
// the allowance by the same factor, in the customer's favour.
//
// # Periods and segments
//
// Allowances reset each period: a paid plan's Stripe period (monthly windows
// inside a longer one), or the UTC calendar month on Starter. Each counter's
// usage is cut into segments (billing.usage_allowance_period): a segment
// opens when the organization's terms (period start and plan tier) change and
// carries the allowance of those terms, counted from the counter's value when
// it opened. The billing heartbeat opens segments as meter reports arrive, so
// usage is attributed to the period it was reported in even when the debit
// loop misses ticks. A debit that spans several segments settles each part
// against its own segment's allowance.
package billingallowance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/pkg/plans"
)

// Resource is a compute resource with plan allowances.
type Resource string

const (
	Sandbox Resource = "sandbox"
	Browser Resource = "browser"
)

// Billing meters (billing.usage_meter_watermarks metric types).
const (
	// Money meters, charged by the usage-debit loop.
	MetricSandboxCostMicros = "sandbox_compute_cost_micros"
	MetricBrowserCostMicros = "browser_runtime_cost_micros"
	// Allowance counters for sandbox CPU and memory. They are parts of
	// MetricSandboxCostMicros and are never charged on their own.
	MetricSandboxCPUCostMicros    = "sandbox_cpu_cost_micros"
	MetricSandboxMemoryCostMicros = "sandbox_memory_cost_micros"
)

// Counter is a cumulative meter whose growth is counted against one plan
// allowance.
type Counter struct {
	Metric    string   // billing meter metric type
	LimitType string   // plans.json usage_limits type
	Resource  Resource // what admission gates on it
	// Money is the meter this counter discounts.
	Money string
}

var (
	SandboxCPU    = Counter{MetricSandboxCPUCostMicros, plans.UsageLimitSandboxVCPUHours, Sandbox, MetricSandboxCostMicros}
	SandboxMemory = Counter{MetricSandboxMemoryCostMicros, plans.UsageLimitSandboxMemoryGiBHours, Sandbox, MetricSandboxCostMicros}
	BrowserTime   = Counter{MetricBrowserCostMicros, plans.UsageLimitBrowserHours, Browser, MetricBrowserCostMicros}

	// Counters lists every allowance counter, in display order.
	Counters = []Counter{SandboxCPU, SandboxMemory, BrowserTime}
)

// CountersFor returns the counters whose allowances discount a money meter.
func CountersFor(moneyMetric string) []Counter {
	var out []Counter
	for _, c := range Counters {
		if c.Money == moneyMetric {
			out = append(out, c)
		}
	}
	return out
}

// IsCounterOnly reports whether metric is an allowance counter that is not
// itself charged (the sandbox CPU and memory parts).
func IsCounterOnly(metric string) bool {
	return metric == MetricSandboxCPUCostMicros || metric == MetricSandboxMemoryCostMicros
}

// Allowance is one counter's included usage per period for a plan tier.
type Allowance struct {
	Counter
	// Unlimited is true for a plan whose allowance is -1 (Enterprise).
	Unlimited bool
	// IncludedMicros is the allowance at list price, in the counter's unit.
	// Zero means no allowance: all usage is charged.
	IncludedMicros int64
	// HourMicros is the list price of one allowance hour (one vCPU, one
	// GiB, or one browser for an hour). Display only.
	HourMicros int64
}

// included is the allowance as stored on a segment: -1 when unlimited.
func (a Allowance) included() float64 {
	if a.Unlimited {
		return -1
	}
	return float64(a.IncludedMicros)
}

// ForTier reads a tier's allowance for one counter from the plans catalog.
// An unknown tier gets Starter's allowance. A catalog without the allowance
// or the rate card yields no allowance, so usage is charged as before
// allowances existed: a misconfiguration never grants free compute.
func ForTier(cfg *plans.PlansConfig, tier string, c Counter) Allowance {
	out := Allowance{Counter: c}
	if cfg == nil {
		return out
	}
	var rate float64
	switch c.Metric {
	case MetricSandboxCPUCostMicros:
		if cfg.SandboxComputePricing != nil {
			rate = cfg.SandboxComputePricing.CPUPerVCPUHour
		}
	case MetricSandboxMemoryCostMicros:
		if cfg.SandboxComputePricing != nil {
			rate = cfg.SandboxComputePricing.MemoryPerGiBHour
		}
	case MetricBrowserCostMicros:
		if cfg.BrowserRuntimePricing != nil {
			rate = cfg.BrowserRuntimePricing.BrowserHour
		}
	}
	out.HourMicros = usdToMicros(rate)
	if out.HourMicros <= 0 {
		return out
	}
	plan, ok := cfg.GetPlan(strings.ToLower(strings.TrimSpace(tier)))
	if !ok {
		if plan, ok = cfg.GetPlan("free"); !ok {
			return out
		}
	}
	for _, ul := range plan.UsageLimits {
		if ul.Type != c.LimitType || !ul.IsAllowance() {
			continue
		}
		if ul.Value < 0 {
			out.Unlimited = true
		} else {
			out.IncludedMicros = usdToMicros(float64(ul.Value) * rate)
		}
		break
	}
	return out
}

func usdToMicros(usd float64) int64 {
	if usd <= 0 || math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0
	}
	return int64(math.Round(usd * 1_000_000))
}

// Subscription is the part of an organization's billing.subscriptions row
// that places it in an allowance period. A nil *Subscription is "none".
type Subscription struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Period returns the allowance period containing now.
//
//   - Starter, or a paid tier without a usable subscription period: the UTC
//     calendar month.
//   - A paid tier inside its Stripe period: that period when it is a monthly
//     one (up to 32 days, so Stripe's clamp of a 29th to 31st anchor to a
//     short month stays one period), else monthly windows stepped from the
//     anchor (a yearly subscription still resets monthly).
//   - A paid tier past its recorded period end (the renewal webhook has not
//     landed): the window starting at that end, which is exactly where
//     Stripe's next period starts. No other window is opened until the
//     subscription row moves, so a late webhook never splits one Stripe
//     period into two allowance windows.
func Period(tier string, sub *Subscription, now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	if !IsPaidTier(tier) || sub == nil || sub.PeriodStart.IsZero() || !sub.PeriodStart.Before(sub.PeriodEnd) {
		return CalendarMonth(now)
	}
	start, end := sub.PeriodStart.UTC(), sub.PeriodEnd.UTC()
	if !now.Before(end) {
		return end, end.AddDate(0, 1, 0)
	}
	if now.Before(start) || !end.After(start.AddDate(0, 0, 32)) {
		return start, end
	}
	for n := 0; ; n++ {
		ws, we := start.AddDate(0, n, 0), start.AddDate(0, n+1, 0)
		if we.After(end) {
			we = end
		}
		if now.Before(we) || !we.Before(end) {
			return ws, we
		}
	}
}

// IsPaidTier reports whether a plan tier is a paid Cloud plan, whose
// allowance follows the subscription period.
func IsPaidTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "", "free":
		return false
	default:
		return true
	}
}

// CalendarMonth is the UTC calendar month containing now.
func CalendarMonth(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// Terms are what decide which allowance applies: the plan tier and the
// allowance period. A change in either opens a new segment.
type Terms struct {
	Tier        string
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// ErrNoOrganization is returned for an id that is not an organization.
var ErrNoOrganization = errors.New("not an organization")

// ReadTerms reads the organization's plan tier
// (everstack.organizations.plan_tier, which subscription sync keeps at the
// tier whose allowances apply) and its allowance period at now.
func ReadTerms(ctx context.Context, q sqlx.QueryerContext, organizationID string, now time.Time) (Terms, error) {
	var tier sql.NullString
	err := sqlx.GetContext(ctx, q, &tier,
		`SELECT LOWER(TRIM(plan_tier)) FROM everstack.organizations WHERE id = $1::uuid`, organizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Terms{}, ErrNoOrganization
	}
	if err != nil {
		return Terms{}, fmt.Errorf("read organization plan tier: %w", err)
	}
	var row struct {
		Start sql.NullTime `db:"current_period_start"`
		End   sql.NullTime `db:"current_period_end"`
	}
	var sub *Subscription
	err = sqlx.GetContext(ctx, q, &row, `
		SELECT current_period_start, current_period_end
		FROM billing.subscriptions WHERE organization_id = $1::uuid`, organizationID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Terms{}, fmt.Errorf("read subscription period: %w", err)
	case row.Start.Valid && row.End.Valid:
		sub = &Subscription{PeriodStart: row.Start.Time, PeriodEnd: row.End.Time}
	}
	t := Terms{Tier: tier.String}
	if t.Tier == "" {
		t.Tier = "free"
	}
	t.PeriodStart, t.PeriodEnd = Period(t.Tier, sub, now)
	return t, nil
}

// Segment is a stretch of one counter's usage under one set of terms. It
// covers the counter's growth from Baseline up to the next segment's
// Baseline (unbounded for the latest), and Included of it is free.
type Segment struct {
	ID          int64     `db:"id"`
	PeriodStart time.Time `db:"period_start"`
	PeriodEnd   time.Time `db:"period_end"`
	Tier        string    `db:"plan_tier"`
	// Included is the allowance in the counter's unit; -1 is unlimited.
	Included float64 `db:"included_quantity"`
	Baseline float64 `db:"baseline_quantity"`
}

// Matches reports whether the segment was opened under terms t.
func (s Segment) Matches(t Terms) bool {
	return s.PeriodStart.Equal(t.PeriodStart) && s.Tier == t.Tier
}

// LoadSegments returns a counter's segments in the order they were opened,
// which is also baseline order.
func LoadSegments(ctx context.Context, q sqlx.QueryerContext, organizationID, metric string) ([]Segment, error) {
	var segs []Segment
	if err := sqlx.SelectContext(ctx, q, &segs, `
		SELECT id, period_start, period_end, plan_tier, included_quantity, baseline_quantity
		FROM billing.usage_allowance_period
		WHERE organization_id = $1::uuid AND metric_type = $2
		ORDER BY id`, organizationID, metric); err != nil {
		return nil, fmt.Errorf("read allowance segments for %s: %w", metric, err)
	}
	return segs, nil
}

func insertSegment(ctx context.Context, tx sqlx.ExtContext, organizationID, metric string, t Terms, a Allowance, baseline float64) (Segment, error) {
	if baseline < 0 {
		baseline = 0
	}
	seg := Segment{PeriodStart: t.PeriodStart.UTC(), PeriodEnd: t.PeriodEnd.UTC(), Tier: t.Tier, Included: a.included(), Baseline: baseline}
	if err := sqlx.GetContext(ctx, tx, &seg.ID, `
		INSERT INTO billing.usage_allowance_period
			(organization_id, metric_type, period_start, period_end, plan_tier, included_quantity, baseline_quantity)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		organizationID, metric, seg.PeriodStart, seg.PeriodEnd, seg.Tier, seg.Included, seg.Baseline); err != nil {
		return Segment{}, fmt.Errorf("open allowance segment for %s: %w", metric, err)
	}
	return seg, nil
}

func overlap(a1, a2, b1, b2 float64) float64 {
	lo, hi := math.Max(a1, b1), math.Min(a2, b2)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// FreeWithin is how much of the counter's growth (prev, cur] fell inside an
// allowance: each segment contributes the part of the interval inside its own
// (Baseline, Baseline + Included], capped at the next segment's Baseline.
// Growth below the first segment's baseline predates every segment (usage
// before allowances, or before the organization's first report) and is free.
func FreeWithin(segs []Segment, prev, cur float64) float64 {
	if cur <= prev || len(segs) == 0 {
		return 0
	}
	free := 0.0
	if prev < segs[0].Baseline {
		free += overlap(prev, cur, prev, segs[0].Baseline)
	}
	for i, s := range segs {
		hi := math.Inf(1)
		if i+1 < len(segs) {
			hi = segs[i+1].Baseline
		}
		top := hi
		if s.Included >= 0 {
			top = math.Min(hi, s.Baseline+s.Included)
		}
		free += overlap(prev, cur, s.Baseline, top)
	}
	return math.Min(free, cur-prev)
}

// fallbackBaseline places a new segment's baseline when growth (prev, cur]
// arrived without the heartbeat having opened the segment, so there is no
// record of when the terms changed. The counter has no per-time breakdown,
// so the growth is attributed in the customer's favour: first to whatever
// room the previous segment still had, the rest to the new segment.
func fallbackBaseline(segs []Segment, prev, cur float64) float64 {
	if len(segs) == 0 {
		return math.Max(prev, 0)
	}
	last := segs[len(segs)-1]
	start := math.Max(last.Baseline, prev)
	if cur <= start {
		return start
	}
	room := math.Inf(1)
	if last.Included >= 0 {
		room = math.Max(0, last.Baseline+last.Included-start)
	}
	return start + math.Min(room, cur-start)
}

// SettleTx returns how much of a counter's growth (prev, cur] is free under
// the organization's allowances. When the latest segment is not the one for
// the current terms (the heartbeat that should have opened it never ran), it
// opens it first, with the customer-favourable fallback baseline. Call it
// inside a transaction holding the organization's advisory lock.
func SettleTx(ctx context.Context, tx *sqlx.Tx, cfg *plans.PlansConfig, organizationID string, c Counter, t Terms, prev, cur float64) (float64, error) {
	segs, err := LoadSegments(ctx, tx, organizationID, c.Metric)
	if err != nil {
		return 0, err
	}
	if len(segs) == 0 || !segs[len(segs)-1].Matches(t) {
		seg, err := insertSegment(ctx, tx, organizationID, c.Metric, t, ForTier(cfg, t.Tier, c), fallbackBaseline(segs, prev, cur))
		if err != nil {
			return 0, err
		}
		segs = append(segs, seg)
	}
	return FreeWithin(segs, prev, cur), nil
}

// ManagedMeterSourceFilter restricts billing.usage_meter_watermarks rows
// (aliased w) to the managed sources the debit loop charges from the wallet:
// the shared serving plane and dedicated managed instances owned by the same
// organization. Self-hosted sources are never wallet-charged.
const ManagedMeterSourceFilter = `(w.instance_id = 'shared-sandbox-plane'
		       OR w.instance_id LIKE 'shared-sandbox-plane:%'
		       OR EXISTS (
		           SELECT 1 FROM everstack.managed_instances AS mi
		           WHERE mi.id::text = w.instance_id AND mi.organization_id = w.organization_id))`

// meterTotal is an organization's current value of a meter across its
// managed sources, and whether any source has reported it.
func meterTotal(ctx context.Context, q sqlx.QueryerContext, organizationID, metric string) (float64, bool, error) {
	var row struct {
		Total float64 `db:"total"`
		Rows  int     `db:"n"`
	}
	if err := sqlx.GetContext(ctx, q, &row, `
		SELECT COALESCE(SUM(w.total_value), 0)::DOUBLE PRECISION AS total, COUNT(*) AS n
		FROM billing.usage_meter_watermarks AS w
		WHERE w.organization_id = $1::uuid AND w.metric_type = $2
		  AND `+ManagedMeterSourceFilter, organizationID, metric); err != nil {
		return 0, false, fmt.Errorf("read %s meter: %w", metric, err)
	}
	return row.Total, row.Rows > 0, nil
}

// CounterFreshness is how long after a source last reported the sandbox CPU
// and memory counters it is still taken to report them. A rolling gateway
// deploy finishes well inside it; a source silent on counters for longer is
// an old gateway (or a rollback), whose sandbox cost is charged as before
// allowances.
const CounterFreshness = 15 * time.Minute

// MarkCountersSeenTx records that a source's report carried the sandbox
// counters.
func MarkCountersSeenTx(ctx context.Context, tx sqlx.ExtContext, organizationID, source string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.usage_allowance_sources (organization_id, instance_id, counters_seen_at)
		VALUES ($1::uuid, $2, $3)
		ON CONFLICT (organization_id, instance_id)
		DO UPDATE SET counters_seen_at = GREATEST(billing.usage_allowance_sources.counters_seen_at, EXCLUDED.counters_seen_at)`,
		organizationID, source, now.UTC()); err != nil {
		return fmt.Errorf("record allowance counter source: %w", err)
	}
	return nil
}

// CountersFresh reports whether the source (or, with source "", any source)
// sent the sandbox counters for the organization within CounterFreshness.
func CountersFresh(ctx context.Context, q sqlx.QueryerContext, organizationID, source string, now time.Time) (bool, error) {
	var fresh bool
	if err := sqlx.GetContext(ctx, q, &fresh, `
		SELECT EXISTS (SELECT 1 FROM billing.usage_allowance_sources
		               WHERE organization_id = $1::uuid AND ($2 = '' OR instance_id = $2)
		                 AND counters_seen_at > $3)`,
		organizationID, source, now.UTC().Add(-CounterFreshness)); err != nil {
		return false, fmt.Errorf("read allowance counter sources: %w", err)
	}
	return fresh, nil
}

// Heartbeat records, around one meter report, which segment each counter's
// usage belongs to. Begin it before the report's meters are applied and
// Finish it after, in the same transaction, holding the organization's
// advisory lock.
type Heartbeat struct {
	org    string
	before map[string]float64
	had    map[string]bool
}

// BeginHeartbeatTx reads each counter's value before the report is applied.
func BeginHeartbeatTx(ctx context.Context, tx *sqlx.Tx, organizationID string) (*Heartbeat, error) {
	hb := &Heartbeat{org: organizationID, before: map[string]float64{}, had: map[string]bool{}}
	for _, c := range Counters {
		total, had, err := meterTotal(ctx, tx, organizationID, c.Metric)
		if err != nil {
			return nil, err
		}
		hb.before[c.Metric], hb.had[c.Metric] = total, had
	}
	return hb, nil
}

// FinishTx opens a segment for every reported counter whose latest segment
// is not under the current terms. This report's growth happened at some time
// since the previous report, possibly before the terms changed (a gateway
// that could not reach billing across a period end sends the whole backlog
// at once), and the cumulative counter cannot be split by time. So it is
// placed with the same customer-favourable rule as the debit's fallback:
// first into the previous segment's remaining room, the rest under the new
// terms. A counter seen for the first time starts at its value after the
// report instead: its first report is a lifetime baseline, never usage.
func (hb *Heartbeat) FinishTx(ctx context.Context, tx *sqlx.Tx, cfg *plans.PlansConfig, now time.Time, reported map[string]bool) error {
	if hb == nil {
		return nil
	}
	var terms *Terms
	for _, c := range Counters {
		if !reported[c.Metric] {
			continue
		}
		segs, err := LoadSegments(ctx, tx, hb.org, c.Metric)
		if err != nil {
			return err
		}
		if terms == nil {
			t, err := ReadTerms(ctx, tx, hb.org, now)
			if errors.Is(err, ErrNoOrganization) {
				return nil
			}
			if err != nil {
				return err
			}
			terms = &t
		}
		if len(segs) > 0 && segs[len(segs)-1].Matches(*terms) {
			continue
		}
		after, _, err := meterTotal(ctx, tx, hb.org, c.Metric)
		if err != nil {
			return err
		}
		baseline := after
		if hb.had[c.Metric] {
			baseline = fallbackBaseline(segs, hb.before[c.Metric], after)
		}
		if len(segs) > 0 {
			baseline = math.Max(baseline, segs[len(segs)-1].Baseline)
		}
		if _, err := insertSegment(ctx, tx, hb.org, c.Metric, *terms, ForTier(cfg, terms.Tier, c), baseline); err != nil {
			return err
		}
	}
	return nil
}

// Status is one counter's allowance in the current period.
type Status struct {
	Allowance
	PeriodStart time.Time
	PeriodEnd   time.Time
	// UsedMicros is the current segment's usage, from the freshest meter
	// report. It may exceed IncludedMicros: the excess is what credit pays.
	UsedMicros int64
}

// Remaining is the allowance left, never negative.
func (s Status) RemainingMicros() int64 {
	if s.Unlimited {
		return math.MaxInt64
	}
	if r := s.IncludedMicros - s.UsedMicros; r > 0 {
		return r
	}
	return 0
}

// Exhausted reports whether the period's allowance is used up.
func (s Status) Exhausted() bool { return s.RemainingMicros() <= 0 }

// UsedSeconds and IncludedSeconds express the status in allowance-hour
// seconds (vCPU-seconds, GiB-seconds, browser-seconds) for display.
// IncludedSeconds is -1 when unlimited.
func (s Status) UsedSeconds() int64 { return microsToSeconds(s.UsedMicros, s.HourMicros) }

func (s Status) IncludedSeconds() int64 {
	if s.Unlimited {
		return -1
	}
	return microsToSeconds(s.IncludedMicros, s.HourMicros)
}

func microsToSeconds(micros, hourMicros int64) int64 {
	if micros <= 0 || hourMicros <= 0 {
		return 0
	}
	return int64(math.Round(float64(micros) * 3600 / float64(hourMicros)))
}

// ReadStatus reads an organization's allowance status per counter, against
// the billing DB. It does not write.
//
// A sandbox counter is reported only while a gateway reports it: a source
// sent the counters for the org within CounterFreshness, or the org has no
// sandbox usage yet and the shared plane reports counters for anyone.
// Otherwise (a gateway from before per-resource counters, or a rollback to
// one) the debit loop charges sandbox cost in full, so admission must not
// promise the allowance either. An id that is not an organization has none.
func ReadStatus(ctx context.Context, q sqlx.QueryerContext, organizationID string, now time.Time) ([]Status, error) {
	cfg, err := plans.Embedded()
	if err != nil {
		return nil, fmt.Errorf("load plans catalog: %w", err)
	}
	terms, err := ReadTerms(ctx, q, organizationID, now)
	if errors.Is(err, ErrNoOrganization) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, hasSandboxUsage, err := meterTotal(ctx, q, organizationID, MetricSandboxCostMicros)
	if err != nil {
		return nil, err
	}
	var planeReportsCounters bool
	if err := sqlx.GetContext(ctx, q, &planeReportsCounters, `
		SELECT EXISTS (SELECT 1 FROM billing.usage_meter_watermarks
		               WHERE metric_type = $1 AND instance_id LIKE 'shared-sandbox-plane%')`,
		MetricSandboxCPUCostMicros); err != nil {
		return nil, fmt.Errorf("read sandbox counter rollout: %w", err)
	}

	fresh, err := CountersFresh(ctx, q, organizationID, "", now)
	if err != nil {
		return nil, err
	}
	sandboxCounters := fresh || (!hasSandboxUsage && planeReportsCounters)

	out := make([]Status, 0, len(Counters))
	for _, c := range Counters {
		total, _, err := meterTotal(ctx, q, organizationID, c.Metric)
		if err != nil {
			return nil, err
		}
		if IsCounterOnly(c.Metric) && !sandboxCounters {
			continue
		}
		segs, err := LoadSegments(ctx, q, organizationID, c.Metric)
		if err != nil {
			return nil, err
		}
		s := Status{Allowance: ForTier(cfg, terms.Tier, c), PeriodStart: terms.PeriodStart, PeriodEnd: terms.PeriodEnd}
		if n := len(segs); n > 0 && segs[n-1].Matches(terms) {
			latest := segs[n-1]
			s.Unlimited = latest.Included < 0
			s.IncludedMicros = int64(math.Max(latest.Included, 0))
			if used := total - latest.Baseline; used > 0 {
				s.UsedMicros = int64(math.Round(used))
			}
		}
		out = append(out, s)
	}
	return out, nil
}
