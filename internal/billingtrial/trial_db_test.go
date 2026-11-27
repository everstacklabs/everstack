package billingtrial

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/database/migrations"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// Real-Postgres tests for the model trial take statement and its ops writes
// (docs/design/free-trial-onboarding.md 3.2, 3.3, 4). They run the cloud and
// billing migrations into a scratch database. Set EVS_BILLING_TEST_DSN (or
// EVS_TEST_PG_DSN) to a superuser DSN ending in /postgres; skipped otherwise.

type trialEnv struct {
	t   *testing.T
	ctx context.Context
	db  *sqlx.DB
}

func newTrialEnv(t *testing.T) *trialEnv {
	t.Helper()
	if v := os.Getenv("EVS_BILLING_TEST_DSN"); v != "" && os.Getenv(pgscratch.EnvDSN) == "" {
		t.Setenv(pgscratch.EnvDSN, v)
	}
	db := pgscratch.New(t, "model_trial")
	// Room for the concurrency tests' parallel transactions.
	db.SetMaxOpenConns(64)
	ctx := context.Background()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	for _, svc := range []string{"cloud", "billing"} {
		if err := migrations.EnsureForService(ctx, db, "postgres", svc); err != nil {
			t.Fatalf("%s migrations: %v", svc, err)
		}
	}
	return &trialEnv{t: t, ctx: ctx, db: db}
}

func (e *trialEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.ExecContext(e.ctx, q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// enable turns the trial on with the given interval and daily cap.
func (e *trialEnv) enable(minIntervalMS, daily int) {
	e.exec(`UPDATE billing.model_trial_settings SET enabled = TRUE, min_interval_ms = $1, daily_requests = $2 WHERE id`, minIntervalMS, daily)
}

// org creates an organization with a trial row granting n requests,
// expiring at expires.
func (e *trialEnv) org(n int, expires time.Time) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.exec(`INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, $2, 'Org')`, id, "mt-"+id.String()[:12])
	e.exec(`INSERT INTO billing.model_trials (organization_id, granted_requests, source, expires_at) VALUES ($1, $2, 'signup', $3)`, id, n, expires)
	return id
}

func (e *trialEnv) trial(org uuid.UUID) Trial {
	e.t.Helper()
	t, err := loadTrial(e.ctx, e.db, org, false)
	if err != nil || t == nil {
		e.t.Fatalf("load trial: %v (nil=%v)", err, t == nil)
	}
	return *t
}

func (e *trialEnv) dayUsed(day string) int {
	e.t.Helper()
	var n int
	err := e.db.GetContext(e.ctx, &n, `SELECT COALESCE((SELECT used_requests FROM billing.model_trial_daily WHERE day = $1::date), 0)`, day)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func reasonOf(err error) Reason {
	var r *RefusedError
	if errors.As(err, &r) {
		return r.Reason
	}
	return ""
}

var noon = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestModelTrialMigrationDefaultsDisabled(t *testing.T) {
	e := newTrialEnv(t)
	s, err := LoadSettings(e.ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Enabled: false, Model: "gpt-5.4-mini", RequestsPerOrg: 20, ValidityDays: 30, MaxOutputTokens: 300,
		MaxInputBytes: 4000, MinIntervalMS: 6000, DailyRequests: 430, BackfillEmailsPerDay: 25, UpdatedBy: "migration"}
	s.UpdatedAt = time.Time{}
	if s != want {
		t.Fatalf("settings = %+v, want %+v", s, want)
	}
	// up.sql is rerunnable and down.sql removes everything.
	dir := filepath.Join("services", "billing", "internal", "database", "migrations", "postgres", "model_trial_20261026093045")
	for _, f := range []string{"down.sql", "up.sql", "up.sql"} {
		body, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		tx := e.db.MustBegin()
		if _, err := tx.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := e.db.Get(&rows, `SELECT COUNT(*) FROM billing.model_trial_settings`); err != nil || rows != 1 {
		t.Fatalf("settings rows = %d (%v), want 1", rows, err)
	}
}

func TestTakeRefusedWhileDisabled(t *testing.T) {
	e := newTrialEnv(t)
	org := e.org(20, noon.Add(24*time.Hour))
	if _, err := Take(e.ctx, e.db, org, noon); reasonOf(err) != ReasonPaused {
		t.Fatalf("disabled take: %v, want paused", err)
	}
	// Enabled with a zero ceiling is also paused, and writes nothing.
	e.enable(0, 0)
	if _, err := Take(e.ctx, e.db, org, noon); reasonOf(err) != ReasonPaused {
		t.Fatalf("zero-cap take: %v, want paused", err)
	}
	if tr := e.trial(org); tr.UsedRequests != 0 || tr.LastRequestAt != nil {
		t.Fatalf("refused take wrote the org row: %+v", tr)
	}
	var days int
	_ = e.db.Get(&days, `SELECT COUNT(*) FROM billing.model_trial_daily`)
	if days != 0 {
		t.Fatalf("refused take wrote %d day rows", days)
	}
	// A missing settings row fails closed.
	e.exec(`DELETE FROM billing.model_trial_settings`)
	if _, err := Take(e.ctx, e.db, org, noon); err == nil || reasonOf(err) != "" {
		t.Fatalf("take without settings: %v, want a non-refusal error", err)
	}
}

func TestTakeNotEligibleWithoutRow(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	if _, err := Take(e.ctx, e.db, uuid.New(), noon); reasonOf(err) != ReasonNotEligible {
		t.Fatalf("take: %v, want not_eligible", err)
	}
	if e.dayUsed(DayOf(noon)) != 0 {
		t.Fatal("a refused take counted against the day")
	}
}

// Many parallel takes for one organization take exactly its allowance.
func TestTakeConcurrentPerOrgExact(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	org := e.org(20, noon.Add(24*time.Hour))
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, exhausted := 0, 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Take(e.ctx, e.db, org, noon)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case reasonOf(err) == ReasonExhausted:
				exhausted++
			default:
				t.Errorf("take: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 20 || exhausted != 30 {
		t.Fatalf("ok=%d exhausted=%d, want 20 and 30", ok, exhausted)
	}
	if tr := e.trial(org); tr.UsedRequests != 20 {
		t.Fatalf("used = %d, want 20", tr.UsedRequests)
	}
	if got := e.dayUsed(DayOf(noon)); got != 20 {
		t.Fatalf("day used = %d, want 20", got)
	}
}

// Many organizations in parallel take exactly the day's ceiling, and no
// organization exceeds its allowance; a refused take rolls back the org's
// count.
func TestTakeConcurrentGlobalCeilingExact(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	orgs := make([]uuid.UUID, 25)
	for i := range orgs {
		orgs[i] = e.org(20, noon.Add(24*time.Hour))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, paused := 0, 0
	sem := make(chan struct{}, 48)
	for _, org := range orgs {
		for j := 0; j < 20; j++ {
			wg.Add(1)
			go func(org uuid.UUID) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				_, err := Take(e.ctx, e.db, org, noon)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case reasonOf(err) == ReasonPaused:
					paused++
				default:
					t.Errorf("take: %v", err)
				}
			}(org)
		}
	}
	wg.Wait()
	if ok != 430 || paused != 70 {
		t.Fatalf("ok=%d paused=%d, want 430 and 70", ok, paused)
	}
	var sum, max int
	if err := e.db.QueryRow(`SELECT SUM(used_requests), MAX(used_requests) FROM billing.model_trials`).Scan(&sum, &max); err != nil {
		t.Fatal(err)
	}
	if sum != 430 || max > 20 {
		t.Fatalf("org rows sum=%d max=%d, want 430 and <= 20", sum, max)
	}
	d, err := LoadDaily(e.ctx, e.db, noon)
	if err != nil {
		t.Fatal(err)
	}
	// refused counts only the locked-path refusals (the race as the day
	// filled); the rest were refused by the unlocked read with no write.
	if d.UsedRequests != 430 || d.CapRequests != 430 || d.Refused > 70 || d.CappedAt == nil {
		t.Fatalf("day = %+v, want used 430 cap 430 refused <= 70 capped", d)
	}
	var takes int
	if err := e.db.Get(&takes, `SELECT COUNT(*) FROM billing.model_trial_takes`); err != nil || takes != 430 {
		t.Fatalf("take records = %d (%v), want 430", takes, err)
	}
	// The ceiling refusal names when it resumes.
	_, err = Take(e.ctx, e.db, orgs[0], noon)
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason != ReasonPaused && r.Reason != ReasonExhausted {
		t.Fatalf("take after cap: %v", err)
	}
}

func TestTakeLiveCapChange(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 5)
	org := e.org(20, noon.Add(24*time.Hour))
	for i := 0; i < 3; i++ {
		if _, err := Take(e.ctx, e.db, org, noon); err != nil {
			t.Fatal(err)
		}
	}
	// Lowering the ceiling below today's use applies on the next take.
	e.exec(`UPDATE billing.model_trial_settings SET daily_requests = 3 WHERE id`)
	_, err := Take(e.ctx, e.db, org, noon)
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason != ReasonPaused || !r.ResumesAt.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("take over lowered cap: %v %+v", err, r)
	}
	if tr := e.trial(org); tr.UsedRequests != 3 {
		t.Fatalf("org used = %d after a ceiling refusal, want 3 (rolled back)", tr.UsedRequests)
	}
	// Raising it applies at once too.
	e.exec(`UPDATE billing.model_trial_settings SET daily_requests = 10 WHERE id`)
	if _, err := Take(e.ctx, e.db, org, noon); err != nil {
		t.Fatalf("take after raising cap: %v", err)
	}
}

func TestTakeMinInterval(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(6000, 430)
	org := e.org(20, noon.Add(24*time.Hour))
	if _, err := Take(e.ctx, e.db, org, noon); err != nil {
		t.Fatal(err)
	}
	_, err := Take(e.ctx, e.db, org, noon.Add(2*time.Second))
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason != ReasonRateLimited || r.RetryAfter != 4*time.Second {
		t.Fatalf("take within interval: %v %+v, want rate_limited retry 4s", err, r)
	}
	if e.trial(org).UsedRequests != 1 || e.dayUsed(DayOf(noon)) != 1 {
		t.Fatal("a rate-limited take was counted")
	}
	if _, err := Take(e.ctx, e.db, org, noon.Add(7*time.Second)); err != nil {
		t.Fatalf("take after interval: %v", err)
	}
	// Parallel takes inside one interval: exactly one wins.
	org2 := e.org(20, noon.Add(24*time.Hour))
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Take(e.ctx, e.db, org2, noon)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if reasonOf(err) != ReasonRateLimited {
				t.Errorf("take: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("parallel takes within the interval: %d succeeded, want 1", ok)
	}
}

func TestTakeExpiryDistinctFromExhausted(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	expired := e.org(20, noon)
	if _, err := Take(e.ctx, e.db, expired, noon); reasonOf(err) != ReasonExpired {
		t.Fatalf("take at expiry: %v, want expired", err)
	}
	exhausted := e.org(1, noon.Add(time.Hour))
	if _, err := Take(e.ctx, e.db, exhausted, noon); err != nil {
		t.Fatal(err)
	}
	if _, err := Take(e.ctx, e.db, exhausted, noon); reasonOf(err) != ReasonExhausted {
		t.Fatalf("take past allowance: %v, want exhausted", err)
	}
	// Both used up and expired reads as expired.
	if _, err := Take(e.ctx, e.db, exhausted, noon.Add(2*time.Hour)); reasonOf(err) != ReasonExpired {
		t.Fatalf("take after expiry: %v, want expired", err)
	}
	st, err := GetStatus(e.ctx, e.db, expired, noon)
	if err != nil || st.State != StateExpired || st.Remaining != 20 {
		t.Fatalf("status = %+v (%v), want expired with 20 remaining", st, err)
	}
	st, err = GetStatus(e.ctx, e.db, exhausted, noon)
	if err != nil || st.State != StateExhausted || st.Remaining != 0 {
		t.Fatalf("status = %+v (%v), want exhausted", st, err)
	}
}

// A take counts against the UTC day it happens in; yesterday's full ceiling
// does not block today; a refund returns the request to the day it was taken.
func TestTakeDayBoundaryAndRefund(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 2)
	org := e.org(20, noon.Add(72*time.Hour))
	beforeMidnight := time.Date(2026, 10, 26, 23, 59, 59, 0, time.UTC)
	afterMidnight := time.Date(2026, 10, 27, 0, 0, 1, 0, time.UTC)

	r1, err := Take(e.ctx, e.db, org, beforeMidnight.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Take(e.ctx, e.db, org, beforeMidnight)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Day != "2026-10-26" || r2.Day != "2026-10-26" {
		t.Fatalf("days %s %s, want 2026-10-26", r1.Day, r2.Day)
	}
	if _, err := Take(e.ctx, e.db, org, beforeMidnight); reasonOf(err) != ReasonPaused {
		t.Fatalf("third take on a cap-2 day: %v, want paused", err)
	}
	r3, err := Take(e.ctx, e.db, org, afterMidnight)
	if err != nil {
		t.Fatalf("first take of the next day: %v", err)
	}
	if r3.Day != "2026-10-27" || r3.Remaining() != 17 {
		t.Fatalf("next day take = %+v", r3)
	}

	// Refund the request taken yesterday, after midnight: it goes back to
	// yesterday's row and the org, and today's row is untouched.
	if ok, err := Refund(e.ctx, e.db, org, r2.ID, afterMidnight); err != nil || !ok {
		t.Fatalf("refund: %v %v", ok, err)
	}
	if got := e.dayUsed("2026-10-26"); got != 1 {
		t.Fatalf("refunded day used = %d, want 1", got)
	}
	if got := e.dayUsed("2026-10-27"); got != 1 {
		t.Fatalf("today's used = %d, want 1 (untouched)", got)
	}
	if tr := e.trial(org); tr.UsedRequests != 2 || tr.LastRequestAt == nil || !tr.LastRequestAt.Equal(afterMidnight) {
		t.Fatalf("org after refund = %+v, want used 2 and last request kept", tr)
	}
	// A second refund of the same take is a no-op: one request back, not two.
	if ok, err := Refund(e.ctx, e.db, org, r2.ID, afterMidnight); err != nil || ok {
		t.Fatalf("double refund: %v %v, want a no-op", ok, err)
	}
	if e.trial(org).UsedRequests != 2 || e.dayUsed("2026-10-26") != 1 {
		t.Fatal("double refund returned a second request")
	}
	// Parallel refunds of one take return it once.
	var wg sync.WaitGroup
	var mu sync.Mutex
	refunded := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := Refund(e.ctx, e.db, org, r3.ID, afterMidnight)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("parallel refund: %v", err)
			}
			if ok {
				refunded++
			}
		}()
	}
	wg.Wait()
	if refunded != 1 || e.trial(org).UsedRequests != 1 || e.dayUsed("2026-10-27") != 0 {
		t.Fatalf("parallel refunds: %d succeeded, org used %d, day %d", refunded, e.trial(org).UsedRequests, e.dayUsed("2026-10-27"))
	}
}

// A refund is bound to its take: another org's take or an unknown id is
// refused, and a day row that has gone missing rolls the whole refund back.
func TestRefundBoundToTake(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	a := e.org(20, noon.Add(time.Hour))
	b := e.org(20, noon.Add(time.Hour))
	ta, err := Take(e.ctx, e.db, a, noon)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Take(e.ctx, e.db, b, noon); err != nil {
		t.Fatal(err)
	}
	if _, err := Refund(e.ctx, e.db, b, ta.ID, noon); !errors.Is(err, ErrTakeNotFound) {
		t.Fatalf("org B refunding org A's take: %v, want ErrTakeNotFound", err)
	}
	if _, err := Refund(e.ctx, e.db, a, uuid.New(), noon); !errors.Is(err, ErrTakeNotFound) {
		t.Fatalf("unknown take: %v, want ErrTakeNotFound", err)
	}
	if e.trial(a).UsedRequests != 1 || e.trial(b).UsedRequests != 1 || e.dayUsed(DayOf(noon)) != 2 {
		t.Fatal("a refused refund moved a count")
	}
	// The day row is gone: the refund errors and changes nothing.
	e.exec(`DELETE FROM billing.model_trial_daily`)
	if ok, err := Refund(e.ctx, e.db, a, ta.ID, noon); err == nil || ok {
		t.Fatalf("refund with no day row: %v %v, want an error", ok, err)
	}
	if e.trial(a).UsedRequests != 1 {
		t.Fatal("refund with no day row decremented the org")
	}
	var refundedAt *time.Time
	if err := e.db.Get(&refundedAt, `SELECT refunded_at FROM billing.model_trial_takes WHERE id = $1`, ta.ID); err != nil || refundedAt != nil {
		t.Fatalf("take marked refunded after a failed refund: %v %v", refundedAt, err)
	}
}

// A full day is refused by the unlocked read before the org row is touched:
// no org write, no interval consumed, no write on the day row.
func TestTakeFullDayRefusedEarlyWithoutWrites(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(6000, 1)
	a := e.org(20, noon.Add(time.Hour))
	b := e.org(20, noon.Add(time.Hour))
	if _, err := Take(e.ctx, e.db, a, noon); err != nil {
		t.Fatal(err)
	}
	before, err := LoadDaily(e.ctx, e.db, noon)
	if err != nil {
		t.Fatal(err)
	}
	var xmin string
	_ = e.db.Get(&xmin, `SELECT xmin::text FROM billing.model_trial_daily WHERE day = $1::date`, DayOf(noon))
	for i := 0; i < 20; i++ {
		_, err := Take(e.ctx, e.db, b, noon.Add(time.Duration(i)*time.Second))
		var r *RefusedError
		if !errors.As(err, &r) || r.Reason != ReasonPaused || r.ResumesAt.IsZero() {
			t.Fatalf("take on a full day: %v, want paused with resumes_at", err)
		}
	}
	if tr := e.trial(b); tr.UsedRequests != 0 || tr.LastRequestAt != nil {
		t.Fatalf("full-day refusal wrote the org row: %+v", tr)
	}
	after, err := LoadDaily(e.ctx, e.db, noon)
	if err != nil {
		t.Fatal(err)
	}
	var xmin2 string
	_ = e.db.Get(&xmin2, `SELECT xmin::text FROM billing.model_trial_daily WHERE day = $1::date`, DayOf(noon))
	if after.UsedRequests != before.UsedRequests || after.Refused != before.Refused || xmin != xmin2 {
		t.Fatalf("full-day refusals wrote the day row: before %+v (xmin %s) after %+v (xmin %s)", before, xmin, after, xmin2)
	}
	if before.CappedAt == nil {
		t.Fatal("the take that filled the day did not set capped_at")
	}
}

// Settings limits: requests_per_org at least 1, maxima near the budget, a
// short model allowlist, and the same bounds enforced by the table.
func TestSettingsLimits(t *testing.T) {
	e := newTrialEnv(t)
	n := func(v int) *int { return &v }
	str := func(v string) *string { return &v }
	bad := map[string]SettingsPatch{
		"zero requests_per_org":   {RequestsPerOrg: n(0)},
		"daily over 5000":         {DailyRequests: n(5001)},
		"output over 2000":        {MaxOutputTokens: n(2001)},
		"input over 16000":        {MaxInputBytes: n(16001)},
		"unlisted model":          {Model: str("gpt-5")},
		"empty model":             {Model: str(" ")},
		"negative interval":       {MinIntervalMS: n(-1)},
		"zero validity":           {ValidityDays: n(0)},
		"requests over max grant": {RequestsPerOrg: n(MaxGrantRequests + 1)},
	}
	for name, p := range bad {
		if _, err := UpdateSettings(e.ctx, e.db, p, "ops@everstack.ai", noon); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("%s: %v, want ErrInvalidSettings", name, err)
		}
	}
	good := SettingsPatch{RequestsPerOrg: n(1), DailyRequests: n(5000), MaxOutputTokens: n(2000), MaxInputBytes: n(16000), Model: str("gpt-5.4-mini")}
	if _, err := UpdateSettings(e.ctx, e.db, good, "ops@everstack.ai", noon); err != nil {
		t.Fatalf("settings at the limits: %v", err)
	}
	for _, q := range []string{
		`UPDATE billing.model_trial_settings SET requests_per_org = 0`,
		`UPDATE billing.model_trial_settings SET daily_requests = 5001`,
		`UPDATE billing.model_trial_settings SET max_output_tokens = 2001`,
		`UPDATE billing.model_trial_settings SET max_input_bytes = 16001`,
	} {
		if _, err := e.db.Exec(q); err == nil {
			t.Errorf("table accepted %q", q)
		}
	}
}

// Takes for one org never move another org's row.
func TestTakeTenantIsolation(t *testing.T) {
	e := newTrialEnv(t)
	e.enable(0, 430)
	a := e.org(20, noon.Add(time.Hour))
	b := e.org(20, noon.Add(time.Hour))
	for i := 0; i < 5; i++ {
		if _, err := Take(e.ctx, e.db, a, noon); err != nil {
			t.Fatal(err)
		}
	}
	if tr := e.trial(b); tr.UsedRequests != 0 || tr.LastRequestAt != nil {
		t.Fatalf("org B moved: %+v", tr)
	}
}

func TestGrantFromSignupClaimAndStatus(t *testing.T) {
	e := newTrialEnv(t)
	org := uuid.New()
	user := uuid.New()
	st, err := GetStatus(e.ctx, e.db, org, noon)
	if err != nil || st.State != StateNotEligible || st.Eligible {
		t.Fatalf("no claim: %+v (%v)", st, err)
	}
	e.exec(`INSERT INTO billing.signup_credit_claims (claim_key, user_id, organization_id, source) VALUES ($1, $2, $3, 'grant')`,
		"org:"+org.String(), user, org)
	st, err = GetStatus(e.ctx, e.db, org, noon)
	if err != nil || !st.Eligible || st.State != StatePaused || st.Remaining != 20 || st.ExpiresAt != nil || st.ResumesAt != nil {
		t.Fatalf("claimed, disabled: %+v (%v)", st, err)
	}
	if st.Model != "gpt-5.4-mini" || st.MaxOutputTokens != 300 {
		t.Fatalf("status model fields: %+v", st)
	}
	// A claim on another org does not make this one eligible.
	other := uuid.New()
	if ok, err := GrantFromSignupClaim(e.ctx, e.db, other, noon); err != nil || ok {
		t.Fatalf("grant without claim: %v %v", ok, err)
	}
	ok, err := GrantFromSignupClaim(e.ctx, e.db, org, noon)
	if err != nil || !ok {
		t.Fatalf("grant from claim: %v %v", ok, err)
	}
	tr := e.trial(org)
	if tr.GrantedRequests != 20 || tr.Source != "signup" || tr.UserID == nil || *tr.UserID != user || !tr.ExpiresAt.Equal(noon.AddDate(0, 0, 30)) {
		t.Fatalf("granted row = %+v", tr)
	}
	// Idempotent: a second call leaves the row alone.
	e.enable(0, 430)
	if _, err := Take(e.ctx, e.db, org, noon); err != nil {
		t.Fatal(err)
	}
	if ok, err := GrantFromSignupClaim(e.ctx, e.db, org, noon.Add(time.Hour)); err != nil || !ok {
		t.Fatal(err)
	}
	if tr := e.trial(org); tr.UsedRequests != 1 || !tr.ExpiresAt.Equal(noon.AddDate(0, 0, 30)) {
		t.Fatalf("second grant changed the row: %+v", tr)
	}
	st, err = GetStatus(e.ctx, e.db, org, noon)
	if err != nil || st.State != StateActive || st.Remaining != 19 || st.ExpiresAt == nil {
		t.Fatalf("active status = %+v (%v)", st, err)
	}
	// Today's ceiling reached: paused until the next UTC midnight.
	e.exec(`UPDATE billing.model_trial_settings SET daily_requests = 1 WHERE id`)
	st, err = GetStatus(e.ctx, e.db, org, noon)
	if err != nil || st.State != StatePaused || st.ResumesAt == nil || !st.ResumesAt.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("capped status = %+v (%v)", st, err)
	}
}

func TestOpsGrantRevokeSettingsAudited(t *testing.T) {
	e := newTrialEnv(t)
	org := e.org(2, noon.Add(time.Hour))
	e.enable(0, 430)
	for i := 0; i < 2; i++ {
		if _, err := Take(e.ctx, e.db, org, noon); err != nil {
			t.Fatal(err)
		}
	}
	// Grant on an exhausted row: 5 fresh requests, expiry restarts.
	tr, err := Grant(e.ctx, e.db, GrantInput{OrganizationID: org, Requests: 5, Actor: "ops@everstack.ai"}, noon)
	if err != nil {
		t.Fatal(err)
	}
	if tr.GrantedRequests != 7 || tr.UsedRequests != 2 || !tr.ExpiresAt.Equal(noon.AddDate(0, 0, 30)) {
		t.Fatalf("grant = %+v", tr)
	}
	// Revoke refuses every later take.
	if ok, err := Revoke(e.ctx, e.db, org, "ops@everstack.ai", noon); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if ok, err := Revoke(e.ctx, e.db, org, "ops@everstack.ai", noon); err != nil || ok {
		t.Fatalf("second revoke: %v %v, want no-op", ok, err)
	}
	if _, err := Take(e.ctx, e.db, org, noon); reasonOf(err) != ReasonRevoked {
		t.Fatalf("take after revoke: %v, want revoked", err)
	}
	if st, _ := GetStatus(e.ctx, e.db, org, noon); st.State != StateNotEligible {
		t.Fatalf("revoked status = %+v", st)
	}
	// A grant lifts the revocation.
	if _, err := Grant(e.ctx, e.db, GrantInput{OrganizationID: org, Requests: 1, Actor: "ops@everstack.ai"}, noon); err != nil {
		t.Fatal(err)
	}
	if _, err := Take(e.ctx, e.db, org, noon); err != nil {
		t.Fatalf("take after re-grant: %v", err)
	}
	// Grant on a new org creates an ops row; an unknown org is refused.
	fresh := uuid.New()
	e.exec(`INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, $2, 'Org')`, fresh, "mt-"+fresh.String()[:12])
	tr, err = Grant(e.ctx, e.db, GrantInput{OrganizationID: fresh, Requests: 20, Actor: "ops@everstack.ai"}, noon)
	if err != nil || tr.Source != "ops" || tr.GrantedRequests != 20 {
		t.Fatalf("grant new: %+v %v", tr, err)
	}
	if _, err := Grant(e.ctx, e.db, GrantInput{OrganizationID: uuid.New(), Requests: 1, Actor: "x"}, noon); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("grant unknown org: %v", err)
	}
	if _, err := Grant(e.ctx, e.db, GrantInput{OrganizationID: fresh, Requests: MaxGrantRequests + 1, Actor: "x"}, noon); err == nil {
		t.Fatal("oversized grant accepted")
	}
	if _, err := Revoke(e.ctx, e.db, uuid.New(), "x", noon); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown: %v", err)
	}

	// Settings: a patch changes only what it names and is audited.
	off, daily := false, 100
	s, err := UpdateSettings(e.ctx, e.db, SettingsPatch{Enabled: &off, DailyRequests: &daily}, "ops@everstack.ai", noon)
	if err != nil || s.Enabled || s.DailyRequests != 100 || s.RequestsPerOrg != 20 || s.UpdatedBy != "ops@everstack.ai" {
		t.Fatalf("settings = %+v (%v)", s, err)
	}
	neg := -1
	if _, err := UpdateSettings(e.ctx, e.db, SettingsPatch{DailyRequests: &neg}, "x", noon); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("negative cap: %v", err)
	}
	if _, err := Take(e.ctx, e.db, fresh, noon); reasonOf(err) != ReasonPaused {
		t.Fatalf("take after disabling: %v, want paused", err)
	}

	type ev struct {
		Action string  `db:"action"`
		Org    *string `db:"organization_id"`
		Before *string `db:"before"`
		After  *string `db:"after"`
		Actor  string  `db:"actor"`
	}
	var evs []ev
	if err := e.db.Select(&evs, `SELECT action, organization_id::text, before::text, after::text, actor FROM billing.model_trial_events ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	wantActions := []string{"grant", "revoke", "grant", "grant", "settings_change"}
	if len(evs) != len(wantActions) {
		t.Fatalf("events = %+v", evs)
	}
	for i, a := range wantActions {
		if evs[i].Action != a || evs[i].Actor != "ops@everstack.ai" || evs[i].After == nil {
			t.Fatalf("event %d = %+v, want %s", i, evs[i], a)
		}
	}
	if evs[3].Before != nil {
		t.Fatalf("grant creating a row recorded a before: %v", *evs[3].Before)
	}
	if evs[0].Before == nil || evs[4].Before == nil || evs[4].Org != nil {
		t.Fatalf("before/org columns: %+v %+v", evs[0], evs[4])
	}
}
