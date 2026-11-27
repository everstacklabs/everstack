package billingmeter

// Real-Postgres tests (EVS_TEST_PG_DSN) for the gateway's model trial gate
// (docs/design/free-trial-onboarding.md 3.3, 3.4, 5), against the production
// cloud and billing schema.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/billingtrial"
)

func trialFixture(t *testing.T) (*sqlx.DB, *Meter, spendFixture) {
	t.Helper()
	db := spendLimitDB(t)
	db.SetMaxOpenConns(64)
	m := New(db, nil)
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return db, m, spendFixture{t: t, db: db}
}

func enableTrial(t *testing.T, db *sqlx.DB, minIntervalMS, daily int) {
	t.Helper()
	_, err := db.Exec(`UPDATE billing.model_trial_settings SET enabled = TRUE, min_interval_ms = $1, daily_requests = $2 WHERE id`, minIntervalMS, daily)
	require.NoError(t, err)
}

func grantTrial(t *testing.T, db *sqlx.DB, org string, n int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO billing.model_trials (organization_id, granted_requests, source, expires_at)
		VALUES ($1::uuid, $2, 'signup', NOW() + INTERVAL '30 days')`, org, n)
	require.NoError(t, err)
}

func trialUsed(t *testing.T, db *sqlx.DB, org string) int {
	t.Helper()
	var used int
	require.NoError(t, db.Get(&used, `SELECT used_requests FROM billing.model_trials WHERE organization_id = $1::uuid`, org))
	return used
}

// 50 parallel requests from one organization with a 20-request allowance
// take exactly 20: never more, whatever the interleaving.
func TestTrialConcurrentTakesStopAtTheAllowance(t *testing.T) {
	db, m, f := trialFixture(t)
	enableTrial(t, db, 0, 430)
	org := f.org()
	grantTrial(t, db, org, 20)

	var wg sync.WaitGroup
	var mu sync.Mutex
	served, exhausted, other := 0, 0, 0
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := m.TakeTrial(context.Background(), org)
			mu.Lock()
			defer mu.Unlock()
			var re *billingtrial.RefusedError
			switch {
			case err == nil:
				served++
			case errors.As(err, &re) && re.Reason == billingtrial.ReasonExhausted:
				exhausted++
			default:
				other++
				t.Logf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	require.Equal(t, 20, served, "taken")
	require.Equal(t, 30, exhausted, "refused exhausted")
	require.Zero(t, other)
	require.Equal(t, 20, trialUsed(t, db, org))
	day, err := billingtrial.LoadDaily(context.Background(), db, time.Now())
	require.NoError(t, err)
	require.Equal(t, 20, day.UsedRequests)
}

// The daily ceiling holds across organizations under concurrency too.
func TestTrialConcurrentTakesStopAtTheDailyCeiling(t *testing.T) {
	db, m, f := trialFixture(t)
	enableTrial(t, db, 0, 7)
	orgs := make([]string, 10)
	for i := range orgs {
		orgs[i] = f.org()
		grantTrial(t, db, orgs[i], 20)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	served := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(org string) {
			defer wg.Done()
			if _, err := m.TakeTrial(context.Background(), org); err == nil {
				mu.Lock()
				served++
				mu.Unlock()
			}
		}(orgs[i%len(orgs)])
	}
	wg.Wait()
	require.Equal(t, 7, served)
}

// Tenant isolation: one organization's takes and refunds never move another
// organization's allowance, and an organization without a trial row is
// never served on someone else's.
func TestTrialTakesAreScopedToTheOrganization(t *testing.T) {
	db, m, f := trialFixture(t)
	enableTrial(t, db, 0, 430)
	a, b, c := f.org(), f.org(), f.org()
	grantTrial(t, db, a, 3)
	grantTrial(t, db, b, 3)

	var last billingtrial.TakeResult
	for i := 0; i < 3; i++ {
		res, err := m.TakeTrial(context.Background(), a)
		require.NoError(t, err)
		last = res
	}
	_, err := m.TakeTrial(context.Background(), a)
	var re *billingtrial.RefusedError
	require.True(t, errors.As(err, &re) && re.Reason == billingtrial.ReasonExhausted, "a exhausted: %v", err)
	require.Equal(t, 0, trialUsed(t, db, b), "org b moved by org a's takes")

	// A refund names its organization: b cannot return a's take.
	require.Error(t, m.RefundTrial(context.Background(), b, last.ID))
	require.Equal(t, 3, trialUsed(t, db, a))
	require.NoError(t, m.RefundTrial(context.Background(), a, last.ID))
	require.Equal(t, 2, trialUsed(t, db, a))
	require.Equal(t, 0, trialUsed(t, db, b))

	// c has no row: not eligible, nothing written.
	_, err = m.TakeTrial(context.Background(), c)
	require.True(t, errors.As(err, &re) && re.Reason == billingtrial.ReasonNotEligible, "c: %v", err)
	var rows int
	require.NoError(t, db.Get(&rows, `SELECT COUNT(*) FROM billing.model_trials WHERE organization_id = $1::uuid`, c))
	require.Zero(t, rows)

	// Not a uuid (a tenant id that never mapped to an org): refused.
	_, err = m.TakeTrial(context.Background(), "tenant-x")
	require.True(t, errors.As(err, &re) && re.Reason == billingtrial.ReasonNotEligible)
}

// Eligibility at bundle build is a pure read: disabled means nobody, enabled
// means the claim holder, and nothing is written either way. The row is
// created only by GrantTrial (the first free request), and only for the
// claim's organization.
func TestTrialEligibilityIsReadOnlyAndGrantIsLazy(t *testing.T) {
	db, m, f := trialFixture(t)
	claimed, other := f.org(), f.org()
	user := uuid.NewString()
	_, err := db.Exec(`INSERT INTO billing.signup_credit_claims (claim_key, user_id, organization_id, source) VALUES ($1, $2::uuid, $3::uuid, 'grant')`,
		"org:"+claimed, user, claimed)
	require.NoError(t, err)

	countRows := func(org string) int {
		var n int
		require.NoError(t, db.Get(&n, `SELECT COUNT(*) FROM billing.model_trials WHERE organization_id = $1::uuid`, org))
		return n
	}

	ok, err := m.TrialEligible(context.Background(), claimed)
	require.NoError(t, err)
	require.False(t, ok, "nobody is eligible while the trial is disabled")

	enableTrial(t, db, 6000, 430)
	m.trial = trialCache{} // drop the cached settings
	ok, err = m.TrialEligible(context.Background(), claimed)
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, countRows(claimed), "bundle-build eligibility started the trial")

	ok, err = m.TrialEligible(context.Background(), other)
	require.NoError(t, err)
	require.False(t, ok, "an org without the claim is not eligible")

	// The row is created only on the first free request, for the claim's org.
	granted, err := m.GrantTrial(context.Background(), claimed)
	require.NoError(t, err)
	require.True(t, granted)
	require.Equal(t, 1, countRows(claimed))
	granted, err = m.GrantTrial(context.Background(), other)
	require.NoError(t, err)
	require.False(t, granted)
	require.Zero(t, countRows(other))
}

// A trial unit never writes credit_ledger, whatever amount it carries: it is
// recorded as an uncharged attempt with provenance trial.
func TestTrialUnitNeverWritesTheLedger(t *testing.T) {
	db, _, f := trialFixture(t)
	org := f.org()
	f.credit(org, 5_000_000, time.Now())
	var before int
	require.NoError(t, db.Get(&before, `SELECT COUNT(*) FROM billing.credit_ledger WHERE organization_id = $1::uuid`, org))

	id := uuid.NewString()
	u := SettleUnit{ID: id, OrgID: org, Modality: MetricChat, Micros: 4350, At: time.Now(),
		Attempt: &AttemptRecord{RequestID: "req-1", Provider: "openai", Model: "gpt-5.4-mini", Provenance: ProvenanceTrial,
			Role: RoleServed, InputTokens: 4000, OutputTokens: 300}}
	require.False(t, u.chargeable())
	require.NoError(t, DebitUnit(context.Background(), db, u))

	var after int
	require.NoError(t, db.Get(&after, `SELECT COUNT(*) FROM billing.credit_ledger WHERE organization_id = $1::uuid`, org))
	require.Equal(t, before, after, "a trial unit wrote the ledger")
	var provenance string
	var charged bool
	var amount int64
	require.NoError(t, db.QueryRow(`SELECT provenance, charged, amount_micros FROM billing.inference_attempts WHERE id = $1::uuid`, id).
		Scan(&provenance, &charged, &amount))
	require.Equal(t, ProvenanceTrial, provenance)
	require.False(t, charged)
	require.Zero(t, amount)

	// The schema refuses a charged trial attempt outright.
	_, err := db.Exec(`INSERT INTO billing.inference_attempts (id, organization_id, request_id, role, provenance, charged, amount_micros, occurred_at)
		VALUES (gen_random_uuid(), $1::uuid, 'r', 'served', 'trial', TRUE, 10, NOW())`, org)
	require.Error(t, err)
}
