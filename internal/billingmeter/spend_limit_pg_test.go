package billingmeter

// Real-Postgres tests (EVS_TEST_PG_DSN) for the Cloud spend limit check,
// against the production cloud and billing schema.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/database/migrations"
)

func spendLimitDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db := newScratchDB(t)
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot(t)))
	t.Cleanup(func() { _ = os.Chdir(wd) })
	ctx := context.Background()
	for _, service := range []string{"cloud", "billing"} {
		require.NoError(t, migrations.EnsureForService(ctx, db, "postgres", service), service)
	}
	return db
}

type spendFixture struct {
	t  *testing.T
	db *sqlx.DB
}

func (f spendFixture) org() string {
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO everstack.organizations (id, slug, name) VALUES ($1::uuid, $2, 'Org')`, id, "o-"+id[:8])
	require.NoError(f.t, err)
	return id
}

func (f spendFixture) limit(org, period, action, limitType string, usd float64, enabled bool, instance any) {
	_, err := f.db.Exec(`INSERT INTO billing.spend_limits (organization_id, instance_id, limit_type, limit_amount, period, action_on_exceed, enabled)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`, org, instance, limitType, usd, period, action, enabled)
	require.NoError(f.t, err)
}

func (f spendFixture) debit(org, resource string, micros int64, at time.Time) {
	start := time.Date(at.UTC().Year(), at.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	_, err := f.db.Exec(`INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, created_at, period_start, period_end)
		VALUES ($1::uuid, 'debit', $2, $3, $4, $5, $6, $7)`, org, -micros, resource, uuid.NewString(), at, start, start.AddDate(0, 1, 0))
	require.NoError(f.t, err)
}

func (f spendFixture) credit(org string, micros int64, at time.Time) {
	_, err := f.db.Exec(`INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, created_at)
		VALUES ($1::uuid, 'purchase', $2, 'credit', $3, $4)`, org, micros, uuid.NewString(), at)
	require.NoError(f.t, err)
}

// The read counts only this organization's platform inference debits in
// the current UTC period, and only enabled org-wide estimated_cost limits
// whose action is block.
func TestPGReadSpendState(t *testing.T) {
	db := spendLimitDB(t)
	f := spendFixture{t, db}
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)

	org := f.org()
	f.limit(org, "monthly", "block", "estimated_cost", 50, true, nil)
	f.limit(org, "daily", "block", "estimated_cost", 5, true, nil)
	// Ignored: per-instance override, invoice-fed type.
	f.limit(org, "daily", "block", "estimated_cost", 1, true, "inst-1")
	f.limit(org, "monthly", "block", "actual_billing", 1, true, nil)
	// Ignored: warn only, and disabled.
	warnOnly := f.org()
	f.limit(warnOnly, "monthly", "warn", "estimated_cost", 1, true, nil)
	f.limit(warnOnly, "daily", "block", "estimated_cost", 1, false, nil)
	f.debit(warnOnly, "inference", 9_000_000, now.Add(-time.Hour))

	f.debit(org, "inference", 3_000_000, now.Add(-time.Hour))                            // today
	f.debit(org, "inference", 10_000_000, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))  // this month
	f.debit(org, "inference", 99_000_000, time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)) // last month
	f.debit(org, "sandbox", 7_000_000, now.Add(-time.Hour))                              // not inference
	f.credit(org, 100_000_000, now.Add(-time.Hour))                                      // not spend

	other := f.org()
	f.debit(other, "inference", 500_000_000, now.Add(-time.Hour))

	state, err := ReadSpendState(ctx, db, org, now)
	require.NoError(t, err)
	require.False(t, state.Exempt)
	got := map[string]SpendLimit{}
	for _, l := range state.Limits {
		got[l.Period] = l
	}
	require.Len(t, state.Limits, 2)
	require.Equal(t, int64(50_000_000), got["monthly"].LimitMicros)
	require.Equal(t, int64(13_000_000), got["monthly"].SpentMicros)
	require.Equal(t, int64(5_000_000), got["daily"].LimitMicros)
	require.Equal(t, int64(3_000_000), got["daily"].SpentMicros)

	state, err = ReadSpendState(ctx, db, warnOnly, now)
	require.NoError(t, err)
	require.Empty(t, state.Limits, "warn and disabled limits never block")

	// Another organization's limits and spend never leak in.
	state, err = ReadSpendState(ctx, db, other, now)
	require.NoError(t, err)
	require.Empty(t, state.Limits)

	_, err = db.Exec(`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = $1::uuid`, org)
	require.NoError(t, err)
	state, err = ReadSpendState(ctx, db, org, now)
	require.NoError(t, err)
	require.True(t, state.Exempt)
}

// Shadow (the default) never refuses and counts the would-block; enforce
// refuses with the customer-facing error; exempt and internal calls are
// never refused; a failed read never refuses.
func TestPGCheckSpendLimitModes(t *testing.T) {
	db := spendLimitDB(t)
	f := spendFixture{t, db}
	ctx := context.Background()
	now := time.Now().UTC()

	over := f.org()
	f.limit(over, "monthly", "block", "estimated_cost", 10, true, nil)
	f.debit(over, "inference", 12_000_000, now.Add(-time.Minute))
	under := f.org()
	f.limit(under, "monthly", "block", "estimated_cost", 10, true, nil)
	f.debit(under, "inference", 1_000_000, now.Add(-time.Minute))

	m := New(db, nil)
	m.spendNow = func() time.Time { return now }
	t.Cleanup(func() { SetSpendLimitEnforcement(false) })

	SetSpendLimitEnforcement(false)
	before := counterValue(spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeWouldBlock, SpendScopeOrganization, SpendBasisPlatformCredit))
	require.NoError(t, m.CheckSpendLimit(ctx, over, false), "shadow never refuses")
	m.shadow.wait()
	require.Equal(t, before+1, counterValue(spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeWouldBlock, SpendScopeOrganization, SpendBasisPlatformCredit)))

	SetSpendLimitEnforcement(true)
	err := m.CheckSpendLimit(ctx, over, false)
	var typed *SpendLimitExceededError
	require.True(t, errors.As(err, &typed), "enforce refuses an org over its limit: %v", err)
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.NoError(t, m.CheckSpendLimit(ctx, under, false))
	require.NoError(t, m.CheckSpendLimit(ctx, over, true), "internal platform calls are never refused")
	require.NoError(t, m.CheckSpendLimit(ctx, "", false), "no organization, nothing to check")

	_, err = db.Exec(`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = $1::uuid`, over)
	require.NoError(t, err)
	m.spendCache.forget(over)
	require.NoError(t, m.CheckSpendLimit(ctx, over, false), "an exempt organization is never refused")

	// The platform DB being unable to answer fails open.
	broken := f.org()
	_, err = db.Exec(`ALTER TABLE billing.spend_limits RENAME TO spend_limits_gone`)
	require.NoError(t, err)
	require.NoError(t, m.CheckSpendLimit(ctx, broken, false))
}
