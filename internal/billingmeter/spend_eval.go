package billingmeter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/singleflight"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Spend limits v2 evaluator (docs/design/spend-limits-v2.md 4.3, slice S3).
//
// A v2 limit is an enabled billing.spend_limits row with basis all_inference,
// limit_type estimated_cost, action block, period daily or monthly and no
// instance: an organization row (api_key_id NULL) or a key row (api_key_id
// set). It counts the combined budget: Everstack credit charged plus BYOK at
// catalog list price, from billing.spend_rollups. Every attempt (platform and
// BYOK) of an organization is evaluated against the organization's rows and,
// when the request authenticated with an API key, that key's rows.
//
// Per attempt, at dispatch:
//
//	S = rollup spend (cached) + this replica's settled spend the rollups do
//	    not hold yet (the local delta)
//	P = list-price estimates of this replica's dispatched, unsettled attempts
//	admit when S + P < L; the attempt's own estimate is then added to P and
//	released when it ends (its actual cost joins the local delta at settle).
//
// Outcomes: no_limit, admit, would_block (S alone reached L) and
// pending_block (S < L but S + P reached it: refused only because of what is
// in flight, the cost of counting estimates).
//
// S3 is SHADOW ONLY, whatever EVS_SPEND_LIMIT_ENFORCEMENT says: an outcome is
// counted and logged, never acted on. Nothing here returns an error to the
// caller, blocks, delays or alters a request.
//
// Exemption: the internal-organization exemption (billingidentity) lifts
// Everstack-imposed caps; a budget the organization set on itself is not
// one, so this evaluator never consults it (legacy platform_credit rows
// keep #891's exemption in spend_limit.go).
//
// Cache: one query per organization per TTL loads its v2 rows, its rollup
// sums per API key for the current UTC day and month, and the rollup
// watermark. TTL is 10 s, 2 s once any applicable limit is at 80% or more.
// An expired entry is served while one background refresh runs
// (stale-while-revalidate). A failed refresh keeps the last state for up to
// an hour (fail stale); only a cold start with the store down has no state,
// and it admits (open). In shadow a cold miss never waits on the request
// path: the first evaluation runs in the background.
//
// Tenant isolation: the organization is the one the meter resolved from the
// authenticated request, and the API key id the one the key interceptor
// verified; neither comes from request input. Every row is read by
// organization_id, and a key row applies only when its organization is the
// request's and its api_key_id is the request's key.

// v2 outcomes and metric labels (with SpendOutcomeNoLimit, SpendOutcomeAdmit,
// SpendOutcomeWouldBlock, SpendOutcomeError and SpendOutcomeDropped).
const (
	SpendOutcomePendingBlock = "pending_block"
	// SpendOutcomeStale and SpendOutcomeOpen are counted per failed read,
	// beside SpendOutcomeError: stale when a last state is still served,
	// open when there is none and the organization goes unevaluated.
	SpendOutcomeStale = "stale"
	SpendOutcomeOpen  = "open"
)

const (
	spendV2TTL     = 10 * time.Second
	spendV2HotTTL  = 2 * time.Second
	spendV2MaxAge  = time.Hour
	spendV2Retry   = 2 * time.Second
	spendV2Timeout = 2 * time.Second
	// spendColdConcurrency bounds concurrent v2 cold-miss evaluations.
	spendColdConcurrency = 8
	// spendLocalBucket is the local delta's granularity. A bucket that
	// starts before the rollup watermark is dropped whole (see spendLocal
	// for the error bounds).
	spendLocalBucket = 5 * time.Second
	// spendLocalMaxBuckets bounds the local delta if the rollup job stalls
	// for a long time; past it new spend is not held locally (an
	// under-count, counted in billing_spend_local_dropped_total).
	spendLocalMaxBuckets = 200_000
)

// SpendAttempt is one dispatched attempt as the v2 evaluator sees it.
type SpendAttempt struct {
	OrgID string
	// APIKeyID is the verified gateway API key id; "" when none.
	APIKeyID string
	// EstimateMicros is the attempt's estimated cost: list price on BYOK,
	// the marked-up charge on a platform key (what each settles as).
	EstimateMicros int64
	// Internal platform calls are counted in P but never evaluated.
	Internal bool
}

// PendingSpend is what AdmitSpend added to P; ReleaseSpend removes it. The
// zero value releases nothing.
type PendingSpend struct {
	org, key string
	micros   int64
}

// SpendV2Limit is one v2 limit row.
type SpendV2Limit struct {
	ID          string `json:"id"`
	APIKeyID    string `json:"api_key_id"` // "" on an organization row
	Period      string `json:"period"`
	LimitMicros int64  `json:"limit_micros"`
}

// Scope is the limit's scope label.
func (l SpendV2Limit) Scope() string {
	if l.APIKeyID != "" {
		return SpendScopeAPIKey
	}
	return SpendScopeOrganization
}

// SpendV2Sums is spend in the current UTC day and month.
type SpendV2Sums struct {
	Day   int64 `json:"day_micros"`
	Month int64 `json:"month_micros"`
}

// SpendV2State is one organization's cached v2 state.
type SpendV2State struct {
	Limits []SpendV2Limit
	// ByKey is rollup spend per api_key_id ("" is traffic with no key); Org
	// is its sum. Both are for the windows starting DayStart and MonthStart.
	ByKey      map[string]SpendV2Sums
	Org        SpendV2Sums
	DayStart   time.Time
	MonthStart time.Time
	// Watermark: the rollups hold every attempt recorded before it. Zero
	// before the first rollup pass (the rollups are then empty).
	Watermark time.Time
}

// SpendV2Decision is one evaluation.
type SpendV2Decision struct {
	Outcome string
	Scope   string
	// Limit is the row that would block (would_block or pending_block).
	Limit         *SpendV2Limit
	SettledMicros int64
	PendingMicros int64
	// Hot: some applicable limit is at 80% or more of S + P.
	Hot bool
}

// SpendV2Local is this replica's settled spend not yet in the rollups.
type SpendV2Local struct {
	Org SpendV2Sums
	Key SpendV2Sums
}

// EvaluateSpendV2 is the rule. Organization rows are checked against the
// organization's totals (every key and no key) and P over all its attempts;
// a key row against that key's spend and P, and only when apiKeyID is the
// row's key. A limit already reached by settled spend is would_block, ahead
// of any limit reached only with pending estimates (pending_block).
func EvaluateSpendV2(st SpendV2State, now time.Time, apiKeyID string, local SpendV2Local, pendingOrg, pendingKey int64) SpendV2Decision {
	day, month := spendPeriodStarts(now)
	sameDay, sameMonth := st.DayStart.Equal(day), st.MonthStart.Equal(month)
	settled := func(s SpendV2Sums, l SpendV2Sums, period string) int64 {
		if period == "daily" {
			if sameDay {
				return s.Day + l.Day
			}
			return l.Day
		}
		if sameMonth {
			return s.Month + l.Month
		}
		return l.Month
	}
	d := SpendV2Decision{Outcome: SpendOutcomeNoLimit, Scope: SpendScopeNone}
	var pending *SpendV2Limit
	var pendingS, pendingP int64
	applicable := false
	for i := range st.Limits {
		l := st.Limits[i]
		if l.LimitMicros <= 0 || (l.Period != "daily" && l.Period != "monthly") {
			continue
		}
		var s, p int64
		switch {
		case l.APIKeyID == "":
			s, p = settled(st.Org, local.Org, l.Period), pendingOrg
		case apiKeyID != "" && l.APIKeyID == apiKeyID:
			s, p = settled(st.ByKey[apiKeyID], local.Key, l.Period), pendingKey
		default:
			continue
		}
		applicable = true
		if (s+p)*5 >= l.LimitMicros*4 {
			d.Hot = true
		}
		if s >= l.LimitMicros {
			d.Outcome, d.Scope, d.Limit, d.SettledMicros, d.PendingMicros = SpendOutcomeWouldBlock, l.Scope(), &l, s, p
			return d
		}
		if pending == nil && s+p >= l.LimitMicros {
			pending, pendingS, pendingP = &l, s, p
		}
	}
	switch {
	case pending != nil:
		d.Outcome, d.Scope, d.Limit, d.SettledMicros, d.PendingMicros = SpendOutcomePendingBlock, pending.Scope(), pending, pendingS, pendingP
	case applicable:
		d.Outcome = SpendOutcomeAdmit
	}
	return d
}

// spendV2Query is the one read per organization per TTL. Every subquery is
// scoped to $1, the resolved organization; $1 is cast to uuid in every
// position (a reused parameter must be typed the same way everywhere). The
// rollups are summed only when the organization has a v2 row.
const spendV2Query = `
WITH lim AS (
    SELECT id::text AS id, COALESCE(api_key_id, '') AS api_key_id, period,
           ROUND(limit_amount * 1000000)::BIGINT AS limit_micros
      FROM billing.spend_limits
     WHERE organization_id = $1::uuid
       AND enabled
       AND basis = 'all_inference'
       AND instance_id IS NULL
       AND action_on_exceed = 'block'
       AND limit_type = 'estimated_cost'
       AND period IN ('daily', 'monthly')
       AND limit_amount > 0
), spend AS (
    SELECT api_key_id,
           COALESCE(SUM(platform_micros + byok_est_micros) FILTER (WHERE bucket_hour >= $2::timestamptz), 0)::BIGINT AS day_micros,
           COALESCE(SUM(platform_micros + byok_est_micros), 0)::BIGINT AS month_micros
      FROM billing.spend_rollups
     WHERE organization_id = $1::uuid
       AND bucket_hour >= $3::timestamptz
       AND bucket_hour < $4::timestamptz
       AND EXISTS (SELECT 1 FROM lim)
     GROUP BY api_key_id
)
SELECT (SELECT COALESCE(json_agg(lim), '[]'::json) FROM lim)::text AS limits,
       (SELECT COALESCE(json_agg(spend), '[]'::json) FROM spend)::text AS spend,
       (SELECT CASE WHEN isfinite(watermark) THEN watermark END
          FROM billing.spend_rollup_state WHERE source = 'inference_attempts') AS watermark`

// ReadSpendV2State reads one organization's v2 limits and its rollup spend
// for the current UTC day and month in one query. It only reads.
func ReadSpendV2State(ctx context.Context, db *sqlx.DB, orgID string, now time.Time) (SpendV2State, error) {
	day, month := spendPeriodStarts(now)
	var row struct {
		Limits    string     `db:"limits"`
		Spend     string     `db:"spend"`
		Watermark *time.Time `db:"watermark"`
	}
	if err := db.GetContext(ctx, &row, spendV2Query, orgID, day, month, month.AddDate(0, 1, 0)); err != nil {
		return SpendV2State{}, err
	}
	st := SpendV2State{DayStart: day, MonthStart: month, ByKey: map[string]SpendV2Sums{}}
	if err := json.Unmarshal([]byte(row.Limits), &st.Limits); err != nil {
		return SpendV2State{}, fmt.Errorf("decode spend limits: %w", err)
	}
	var spend []struct {
		APIKeyID string `json:"api_key_id"`
		SpendV2Sums
	}
	if err := json.Unmarshal([]byte(row.Spend), &spend); err != nil {
		return SpendV2State{}, fmt.Errorf("decode spend rollups: %w", err)
	}
	for _, s := range spend {
		st.ByKey[s.APIKeyID] = s.SpendV2Sums
		st.Org.Day += s.Day
		st.Org.Month += s.Month
	}
	if row.Watermark != nil {
		st.Watermark = row.Watermark.UTC()
	}
	return st, nil
}

// spendV2Cache holds each organization's SpendV2State.
type spendV2Cache struct {
	mu    sync.Mutex
	m     map[string]*spendV2Entry
	group singleflight.Group
	// read and timeout override ReadSpendV2State and spendV2Timeout in tests.
	read    func(ctx context.Context, db *sqlx.DB, orgID string, now time.Time) (SpendV2State, error)
	timeout time.Duration
}

type spendV2Entry struct {
	state   SpendV2State
	fetched time.Time
	hot     bool
	// failedAt is the last failed refresh since the last success.
	failedAt time.Time
}

// lookup returns the entry usable at now: fresh within its TTL, or an
// expired one up to spendV2MaxAge (served while it refreshes, or kept
// through failures). ok is false on a cold miss or an entry past
// spendV2MaxAge.
func (c *spendV2Cache) lookup(org string, now time.Time) (st SpendV2State, ok, fresh, retry bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[org]
	if e == nil {
		return SpendV2State{}, false, false, true
	}
	age := now.Sub(e.fetched)
	ttl := spendV2TTL
	if e.hot {
		ttl = spendV2HotTTL
	}
	retry = e.failedAt.IsZero() || now.Sub(e.failedAt) >= spendV2Retry || now.Before(e.failedAt)
	if age < 0 || age >= spendV2MaxAge {
		return SpendV2State{}, false, false, retry
	}
	return e.state, true, age < ttl, retry
}

// markHot shortens org's TTL once an evaluation saw a limit at 80%.
func (c *spendV2Cache) markHot(org string) {
	c.mu.Lock()
	if e := c.m[org]; e != nil {
		e.hot = true
	}
	c.mu.Unlock()
}

// load reads org's state once (singleflight across callers), detached from
// any caller's cancellation and bounded by the read timeout, and stores it.
// A failure keeps the last entry (fail stale). The returned channel yields
// the result; callers that do not wait simply drop it.
func (c *spendV2Cache) load(parent context.Context, db *sqlx.DB, org string, now time.Time, onLoaded func(SpendV2State)) <-chan singleflight.Result {
	read, timeout := c.read, c.timeout
	if read == nil {
		read = ReadSpendV2State
	}
	if timeout <= 0 {
		timeout = spendV2Timeout
	}
	return c.group.DoChan(org, func() (_ any, err error) {
		// singleflight re-raises a panic from this function on a fresh
		// goroutine, where nothing can recover it and the gateway would
		// crash. Recover here and report it as a failed read instead.
		defer func() {
			if r := recover(); r != nil {
				spendV2Panics.Inc()
				logger.WithFields("organization_id", org, "panic", r).
					Error("billing.spend_limit_v2: read panicked; treated as a failed read")
				err = fmt.Errorf("spend limit read panicked: %v", r)
				c.failed(org, now, err)
			}
		}()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
		defer cancel()
		st, err := read(ctx, db, org, now)
		if err != nil {
			c.failed(org, now, err)
			return nil, err
		}
		hot := spendV2StateHot(st) // outside the lock: nothing that can panic runs under it
		c.mu.Lock()
		if c.m == nil {
			c.m = map[string]*spendV2Entry{}
		}
		c.m[org] = &spendV2Entry{state: st, fetched: now, hot: hot}
		c.mu.Unlock()
		if onLoaded != nil {
			onLoaded(st)
		}
		return st, nil
	})
}

// failed records a failed read of org at now: the last entry is kept (fail
// stale) and marked so it is retried at most every spendV2Retry; a cold
// organization gets a placeholder that serves nothing (open).
func (c *spendV2Cache) failed(org string, now time.Time, err error) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*spendV2Entry{}
	}
	e := c.m[org]
	served := e != nil && now.Sub(e.fetched) >= 0 && now.Sub(e.fetched) < spendV2MaxAge
	if e != nil {
		e.failedAt = now
	} else {
		c.m[org] = &spendV2Entry{fetched: now.Add(-spendV2MaxAge), failedAt: now}
	}
	c.mu.Unlock()
	reportSpendV2ReadError(org, served, err)
}

// spendV2StateHot reports whether a freshly read state already has a limit
// at 80% or more on rollup spend alone (no request in hand, so every key
// row is checked against its own key).
func spendV2StateHot(st SpendV2State) bool {
	for _, l := range st.Limits {
		s := st.Org
		if l.APIKeyID != "" {
			s = st.ByKey[l.APIKeyID]
		}
		v := s.Month
		if l.Period == "daily" {
			v = s.Day
		}
		if l.LimitMicros > 0 && v*5 >= l.LimitMicros*4 {
			return true
		}
	}
	return false
}

// spendPending is P: per organization and API key, the estimates of this
// replica's dispatched, unsettled attempts. Process-wide, like the platform
// in-flight counter.
type spendPending struct {
	mu    sync.Mutex
	byOrg map[string]*pendingOrg
	total int64
}

type pendingOrg struct {
	total int64
	byKey map[string]int64
}

// add returns the organization's and the key's P before adding micros.
func (p *spendPending) add(org, key string, micros int64) (orgBefore, keyBefore int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byOrg == nil {
		p.byOrg = map[string]*pendingOrg{}
	}
	o := p.byOrg[org]
	if o == nil {
		o = &pendingOrg{byKey: map[string]int64{}}
		p.byOrg[org] = o
	}
	orgBefore, keyBefore = o.total, o.byKey[key]
	if micros > 0 {
		o.total += micros
		o.byKey[key] += micros
		p.total += micros
		spendPendingMicros.Set(float64(p.total))
	}
	return orgBefore, keyBefore
}

func (p *spendPending) release(org, key string, micros int64) {
	if micros <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.byOrg[org]
	if o == nil {
		return
	}
	o.total -= micros
	if v := o.byKey[key] - micros; v > 0 {
		o.byKey[key] = v
	} else {
		delete(o.byKey, key)
	}
	if o.total <= 0 {
		delete(p.byOrg, org)
	}
	p.total -= micros
	if p.total < 0 {
		p.total = 0
	}
	spendPendingMicros.Set(float64(p.total))
}

func (p *spendPending) get(org, key string) (orgP, keyP int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if o := p.byOrg[org]; o != nil {
		return o.total, o.byKey[key]
	}
	return 0, 0
}

// spendLocal is this replica's settled spend that the rollups may not hold
// yet: per organization and API key, in spendLocalBucket buckets by the
// attempt's time. The rollup job recomputes from billing.inference_attempts
// once a minute and the cache reads it every TTL, so without this a burst
// would be invisible for up to a minute and a half.
//
// The split with the rollups is at the watermark W, the created_at bound of
// the last rollup pass: the rollups hold attempts recorded at or before W
// (the job rebuilds hours only from those), and the local delta holds
// buckets that start at or after W, compared at full precision
// (spendCutoff). Bucket times are the gateway's settle time (At), and the
// settle worker records an attempt after it settles (created_at >= At on
// one clock), so:
//   - under-count: an attempt settled before W but recorded after it is in
//     neither for one rollup pass, and a bucket straddling W is dropped
//     whole; at most the settle queue delay plus one bucket.
//   - over-count: only when the gateway clock runs ahead of the database
//     clock (At > created_at); bounded by that skew's worth of this
//     replica's spend, and gone at the next pass.
type spendLocal struct {
	mu    sync.Mutex
	byOrg map[string]map[string]map[int64]int64 // org -> key -> bucket start (unix s) -> micros
	n     int
	// floor is the last cutoff pruned at; buckets before it are never
	// added (their spend is already rolled up or out of every window).
	floor time.Time
}

func spendBucket(t time.Time) int64 {
	return t.UTC().Truncate(spendLocalBucket).Unix()
}

// spendCutoff is the first bucket start (unix seconds) at or after t. A
// bucket b is local exactly when b >= spendCutoff(t), i.e. when it starts
// at or after t at full precision: rounding t up, never down, so a bucket
// that starts earlier in the same second as the watermark is not counted
// both here and in the rollups.
func spendCutoff(t time.Time) int64 {
	s := t.Unix()
	if t.Nanosecond() > 0 {
		s++
	}
	return s
}

func (l *spendLocal) add(org, key string, at time.Time, micros int64) {
	if org == "" || micros <= 0 {
		return
	}
	b := spendBucket(at)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.floor.IsZero() && b < spendCutoff(l.floor) {
		return
	}
	if l.byOrg == nil {
		l.byOrg = map[string]map[string]map[int64]int64{}
	}
	keys := l.byOrg[org]
	if keys == nil {
		keys = map[string]map[int64]int64{}
		l.byOrg[org] = keys
	}
	buckets := keys[key]
	if buckets == nil {
		buckets = map[int64]int64{}
		keys[key] = buckets
	}
	if _, ok := buckets[b]; !ok {
		if l.n >= spendLocalMaxBuckets {
			spendLocalDropped.Inc()
			return
		}
		l.n++
	}
	buckets[b] += micros
}

// sums is org's (and key's) local spend from buckets starting at or after
// the watermark, in the day and month windows.
func (l *spendLocal) sums(org, key string, watermark, day, month time.Time) SpendV2Local {
	var out SpendV2Local
	w := spendCutoff(watermark)
	if watermark.IsZero() {
		w = month.Unix()
	}
	ds, ms := day.Unix(), month.Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, buckets := range l.byOrg[org] {
		for b, v := range buckets {
			if b < w || b < ms {
				continue
			}
			out.Org.Month += v
			if b >= ds {
				out.Org.Day += v
			}
			if key != "" && k == key {
				out.Key.Month += v
				if b >= ds {
					out.Key.Day += v
				}
			}
		}
	}
	return out
}

// prune drops every bucket that starts before cutoff (the rollup watermark,
// or the month start when that is later: no window counts older spend).
func (l *spendLocal) prune(cutoff time.Time) {
	c := spendCutoff(cutoff)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.floor.IsZero() && !cutoff.After(l.floor) {
		return
	}
	l.floor = cutoff
	for org, keys := range l.byOrg {
		for k, buckets := range keys {
			for b := range buckets {
				if b < c {
					delete(buckets, b)
					l.n--
				}
			}
			if len(buckets) == 0 {
				delete(keys, k)
			}
		}
		if len(keys) == 0 {
			delete(l.byOrg, org)
		}
	}
}

// recordLocalSpend adds a settled unit to the local delta with exactly the
// arithmetic the rollup job uses: a charged platform attempt's amount
// (marked up), a BYOK attempt's list-price estimate; "" is the no-key
// bucket.
func (m *Meter) recordLocalSpend(u SettleUnit, charges bool) {
	if u.Attempt == nil || u.OrgID == "" {
		return
	}
	var micros int64
	switch {
	case charges:
		micros = u.Micros
	case u.Attempt.Provenance == ProvenanceBYOK && u.Attempt.EstCostMicros != nil:
		micros = *u.Attempt.EstCostMicros
	}
	m.spendLocal.add(u.OrgID, strings.TrimSpace(u.Attempt.APIKeyID), u.At, micros)
}

// AdmitSpend is the v2 check for one dispatched attempt: it adds the
// attempt's estimate to P and evaluates the organization's v2 limits in
// shadow. It never fails, blocks or alters the attempt; the caller must
// pass the result to ReleaseSpend when the attempt ends.
func (m *Meter) AdmitSpend(ctx context.Context, a SpendAttempt) (ps PendingSpend) {
	a.OrgID = strings.TrimSpace(a.OrgID)
	if !m.Active() || a.OrgID == "" {
		return PendingSpend{}
	}
	if a.EstimateMicros < 0 {
		a.EstimateMicros = 0
	}
	orgP, keyP := m.spendPending.add(a.OrgID, a.APIKeyID, a.EstimateMicros)
	// The hold is the named result, set before anything that can panic, so
	// a recovered panic still hands it back for ReleaseSpend (P never leaks).
	ps = PendingSpend{org: a.OrgID, key: a.APIKeyID, micros: a.EstimateMicros}
	if a.Internal {
		// Counted (internal calls spend the customer's key), never refused.
		return ps
	}
	defer func() {
		if r := recover(); r != nil {
			spendV2Panics.Inc()
			logger.WithFields("organization_id", a.OrgID, "panic", r).
				Error("billing.spend_limit_v2: evaluation panicked; request unaffected")
		}
	}()
	m.evaluateSpendV2(ctx, a, orgP, keyP)
	return ps
}

// ReleaseSpend takes an ended attempt's estimate out of P. Safe on the zero
// value.
func (m *Meter) ReleaseSpend(p PendingSpend) {
	if m == nil || p.org == "" {
		return
	}
	m.spendPending.release(p.org, p.key, p.micros)
}

func (m *Meter) evaluateSpendV2(ctx context.Context, a SpendAttempt, orgP, keyP int64) {
	now := m.spendClock()
	st, ok, fresh, retry := m.spendV2.lookup(a.OrgID, now)
	if ok {
		if !fresh && retry {
			// Stale-while-revalidate: refresh in the background, decide
			// on the entry in hand.
			m.spendV2.load(ctx, m.billingDB, a.OrgID, now, m.pruneSpendLocal)
		}
		m.decideSpendV2(a, st, now, orgP, keyP)
		return
	}
	if !retry {
		// The last read for this cold organization failed moments ago:
		// unevaluated (counted as open on that read), never retried per
		// attempt.
		return
	}
	// Cold miss. Shadow never waits on the request path: the read and the
	// first evaluation run in the background with the P seen at dispatch.
	org := a.OrgID
	if !m.spendCold.try(func(ctx context.Context) {
		res := <-m.spendV2.load(ctx, m.billingDB, org, now, m.pruneSpendLocal)
		if res.Err != nil {
			return
		}
		m.decideSpendV2(a, res.Val.(SpendV2State), now, orgP, keyP)
	}) {
		spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeDropped, SpendScopeNone, SpendBasisAllInference).Inc()
	}
}

func (m *Meter) pruneSpendLocal(st SpendV2State) {
	cutoff := st.MonthStart
	if st.Watermark.After(cutoff) {
		cutoff = st.Watermark
	}
	m.spendLocal.prune(cutoff)
}

func (m *Meter) decideSpendV2(a SpendAttempt, st SpendV2State, now time.Time, orgP, keyP int64) {
	if len(st.Limits) == 0 {
		spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeNoLimit, SpendScopeNone, SpendBasisAllInference).Inc()
		return
	}
	day, month := spendPeriodStarts(now)
	local := m.spendLocal.sums(a.OrgID, a.APIKeyID, st.Watermark, day, month)
	d := EvaluateSpendV2(st, now, a.APIKeyID, local, orgP, keyP)
	if d.Hot {
		m.spendV2.markHot(a.OrgID)
	}
	reportSpendV2(a, d)
}

// reportSpendV2 counts one evaluation and logs a would_block or
// pending_block at most once a minute per organization, outcome and limit.
// The log carries ids and amounts, never a key secret.
func reportSpendV2(a SpendAttempt, d SpendV2Decision) {
	spendLimitEvaluated.WithLabelValues("shadow", d.Outcome, d.Scope, SpendBasisAllInference).Inc()
	if d.Limit == nil || (d.Outcome != SpendOutcomeWouldBlock && d.Outcome != SpendOutcomePendingBlock) {
		return
	}
	suppressed, emit := spendLogLimiter.allow(a.OrgID+"|v2|"+d.Outcome+"|"+d.Limit.ID, time.Now())
	if !emit {
		return
	}
	fields := []any{
		"organization_id", a.OrgID,
		"mode", "shadow",
		"basis", SpendBasisAllInference,
		"outcome", d.Outcome,
		"scope", d.Scope,
		"spend_limit_id", d.Limit.ID,
		"period", d.Limit.Period,
		"limit_micros", d.Limit.LimitMicros,
		"settled_micros", d.SettledMicros,
		"pending_micros", d.PendingMicros,
		"estimate_micros", a.EstimateMicros,
		"suppressed", suppressed,
	}
	if d.Scope == SpendScopeAPIKey {
		fields = append(fields, "api_key_id", a.APIKeyID)
	}
	logger.WithFields(fields...).Warn("billing.spend_limit_v2")
}

func reportSpendV2ReadError(org string, served bool, err error) {
	spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeError, SpendScopeNone, SpendBasisAllInference).Inc()
	follow := SpendOutcomeOpen
	if served {
		follow = SpendOutcomeStale
	}
	spendLimitEvaluated.WithLabelValues("shadow", follow, SpendScopeNone, SpendBasisAllInference).Inc()
	if _, emit := spendLogLimiter.allow(org+"|v2|error", time.Now()); emit {
		logger.WithFields("organization_id", org, "mode", "shadow", "basis", SpendBasisAllInference,
			"then", follow, "error", err.Error()).
			Warn("billing.spend_limit_v2: could not read spend limits; request allowed")
	}
}

var (
	// spendPendingMicros is P summed over every organization on this
	// replica (no organization label).
	spendPendingMicros = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "billing_spend_pending_micros",
		Help: "Estimated cost of dispatched, unsettled inference attempts on this replica (spend limit P), micro-USD.",
	})
	spendLocalDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_spend_local_dropped_total",
		Help: "Settled spend not held in the spend limit local delta because it was full (the rollup job is far behind). Must stay 0.",
	})
	spendV2Panics = promauto.NewCounter(prometheus.CounterOpts{
		Name: "billing_spend_limit_v2_panics_total",
		Help: "Recovered panics in the spend limit v2 evaluator. Must stay 0; requests are unaffected.",
	})
)
