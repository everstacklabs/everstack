package billingmeter

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

// Real-Postgres tests (EVS_TEST_PG_DSN) for the spend limits v2 read, on the
// production cloud and billing schema.

func v2DB(t *testing.T) *sqlx.DB {
	t.Helper()
	db := spendLimitDB(t)
	// Key rows beside an org row of the same type and period exist only
	// once step 3 has dropped the old four-column constraint.
	_, err := db.Exec(`ALTER TABLE billing.spend_limits DROP CONSTRAINT IF EXISTS unique_spend_limit`)
	require.NoError(t, err)
	return db
}

func (f spendFixture) v2Limit(org string, key any, period, action, basis string, usd float64, enabled bool) string {
	var id string
	require.NoError(f.t, f.db.Get(&id, `INSERT INTO billing.spend_limits (organization_id, api_key_id, basis, limit_type, limit_amount, period, action_on_exceed, enabled)
		VALUES ($1::uuid, $2, $3, 'estimated_cost', $4, $5, $6, $7) RETURNING id::text`, org, key, basis, usd, period, action, enabled))
	return id
}

func (f spendFixture) rollup(org, key string, hour time.Time, platform, byok int64) {
	_, err := f.db.Exec(`INSERT INTO billing.spend_rollups (organization_id, api_key_id, bucket_hour, platform_micros, byok_est_micros, attempts)
		VALUES ($1::uuid, $2, $3, $4, $5, 1)`, org, key, hour, platform, byok)
	require.NoError(f.t, err)
}

// One read returns the organization's v2 rows (organization and key rows,
// whatever key they name), its rollup spend per key for the current UTC day
// and month, and the rollup watermark; nothing of any other organization.
func TestPGReadSpendV2State(t *testing.T) {
	db := v2DB(t)
	f := spendFixture{t, db}
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 15, 20, 0, 0, time.UTC)

	a, b := f.org(), f.org()
	orgDaily := f.v2Limit(a, nil, "daily", "block", "all_inference", 10, true)
	keyMonthly := f.v2Limit(a, "key-1", "monthly", "block", "all_inference", 5, true)
	// Org A naming org B's key: read for A (inert, B's traffic is never
	// A's), never for B.
	foreign := f.v2Limit(a, "key-b1", "daily", "block", "all_inference", 1, true)
	// Ignored: legacy basis, notify, disabled.
	f.v2Limit(a, nil, "monthly", "block", "platform_credit", 1, true)
	// A key row on the legacy basis: neither reader may take it for the
	// organization's limit.
	f.v2Limit(a, "key-4", "daily", "block", "platform_credit", 1, true)
	f.v2Limit(a, "key-2", "daily", "notify", "all_inference", 1, true)
	f.v2Limit(a, "key-3", "daily", "block", "all_inference", 1, false)

	f.rollup(a, "", time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC), 1_000_000, 2_000_000) // today, no key
	f.rollup(a, "key-1", time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC), 0, 500_000)     // today
	f.rollup(a, "key-1", time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC), 0, 4_000_000)     // this month
	f.rollup(a, "key-1", time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), 0, 90_000_000)   // last month
	f.rollup(b, "key-b1", time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), 70_000_000, 0) // other org
	_, err := db.Exec(`UPDATE billing.spend_rollup_state SET watermark = $1 WHERE source = 'inference_attempts'`, now.Add(-time.Minute))
	require.NoError(t, err)

	st, err := ReadSpendV2State(ctx, db, a, now)
	require.NoError(t, err)
	ids := map[string]SpendV2Limit{}
	for _, l := range st.Limits {
		ids[l.ID] = l
	}
	require.Len(t, st.Limits, 3)
	require.Equal(t, SpendV2Limit{ID: orgDaily, Period: "daily", LimitMicros: 10_000_000}, ids[orgDaily])
	require.Equal(t, SpendV2Limit{ID: keyMonthly, APIKeyID: "key-1", Period: "monthly", LimitMicros: 5_000_000}, ids[keyMonthly])
	require.Contains(t, ids, foreign)
	require.Equal(t, SpendV2Sums{Day: 3_000_000, Month: 3_000_000}, st.ByKey[""])
	require.Equal(t, SpendV2Sums{Day: 500_000, Month: 4_500_000}, st.ByKey["key-1"])
	require.Equal(t, SpendV2Sums{Day: 3_500_000, Month: 7_500_000}, st.Org)
	require.NotContains(t, st.ByKey, "key-b1")
	require.True(t, st.Watermark.Equal(now.Add(-time.Minute)))

	d := EvaluateSpendV2(st, now, "key-1", SpendV2Local{}, 0, 0)
	require.Equal(t, SpendOutcomeAdmit, d.Outcome)
	d = EvaluateSpendV2(st, now, "key-1", SpendV2Local{}, 0, 600_000)
	require.Equal(t, SpendOutcomePendingBlock, d.Outcome)
	require.Equal(t, SpendScopeAPIKey, d.Scope)

	// Org B: none of A's rows, including the one naming B's key, so B's
	// $70 on key-b1 is never measured against A's $1 limit.
	st, err = ReadSpendV2State(ctx, db, b, now)
	require.NoError(t, err)
	require.Empty(t, st.Limits)
	require.Empty(t, st.ByKey, "rollups are not even summed for an organization with no v2 row")
	require.Equal(t, SpendOutcomeNoLimit, EvaluateSpendV2(st, now, "key-b1", SpendV2Local{}, 0, 0).Outcome)

	// The legacy reader ignores every v2 and key row.
	legacy, err := ReadSpendState(ctx, db, a, now)
	require.NoError(t, err)
	require.Len(t, legacy.Limits, 1)
	require.Equal(t, int64(1_000_000), legacy.Limits[0].LimitMicros)
}

// The exemption fix: an internal (plan_limit_exempt) organization's own
// all_inference block row is evaluated like any other (would_block), while
// its legacy platform_credit row keeps #891's exemption.
func TestPGSpendV2EvaluatesExemptOrgs(t *testing.T) {
	db := v2DB(t)
	f := spendFixture{t, db}
	now := time.Now().UTC()
	org := f.org()
	_, err := db.Exec(`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = $1::uuid`, org)
	require.NoError(t, err)
	f.v2Limit(org, nil, "monthly", "block", "all_inference", 1, true)
	f.limit(org, "daily", "block", "estimated_cost", 1, true, nil)
	f.rollup(org, "", now.Truncate(time.Hour), 0, 2_000_000)
	f.debit(org, "inference", 2_000_000, now)

	legacy, err := ReadSpendState(context.Background(), db, org, now)
	require.NoError(t, err)
	outcome, _ := EvaluateSpendLimits(legacy)
	require.Equal(t, SpendOutcomeExempt, outcome, "legacy rows keep #891's exemption")

	m := &Meter{billingDB: db, shadow: newShadowRunner(4, 3*time.Second), spendCold: newShadowRunner(4, 3*time.Second)}
	before := v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization)
	m.ReleaseSpend(m.AdmitSpend(context.Background(), SpendAttempt{OrgID: org})) // cold: evaluated in the background
	m.spendCold.wait()
	m.ReleaseSpend(m.AdmitSpend(context.Background(), SpendAttempt{OrgID: org})) // warm: inline
	require.Equal(t, before+2, v2Count(SpendOutcomeWouldBlock, SpendScopeOrganization))
}
