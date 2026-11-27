package billingmeter

// Real-Postgres tests for the G1 admission read and the credit_wallet_state
// migration. Same harness as outbox_pg_test.go: skips unless EVS_TEST_PG_DSN
// is set, creates and drops a scratch database.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func walletStateMigration(t *testing.T, which string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "services", "billing", "internal", "database", "migrations", "postgres", "credit_wallet_state_*"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "exactly one credit_wallet_state migration expected")
	raw, err := os.ReadFile(filepath.Join(matches[0], which))
	require.NoError(t, err)
	return string(raw)
}

// seedOrgsTable creates the platform tables ReadTier reads beside the
// wallet: the organization's plan tier and its billing subscriptions (the
// columns the tier read uses, as billing's migrations define them).
func seedOrgsTable(t *testing.T, db *sqlx.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS everstack.organizations (id UUID PRIMARY KEY, plan_tier TEXT NOT NULL DEFAULT 'free')`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS billing.subscriptions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		organization_id UUID NOT NULL,
		plan_tier VARCHAR(50) NOT NULL DEFAULT 'free',
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		deployment_type TEXT NOT NULL DEFAULT 'cloud'
	)`)
	require.NoError(t, err)
}

// A basic or pro subscriber who never bought a top-up is a paying customer,
// not a free-tier org: the cheap-model allowlist must not apply to it. The
// tier is read server-side from billing.subscriptions for the org the
// gateway resolved, never from anything the client sends.
func TestPGReadTierTreatsLivePaidSubscriptionAsPaid(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	seedOrgsTable(t, db)

	for _, tc := range []struct {
		name         string
		tier, status string
		free         bool
	}{
		{"basic active", "basic", "active", false},
		{"pro trialing", "Pro", "trialing", false},
		{"free active", "free", "active", true},
		{"pro canceled", "pro", "canceled", true},
		{"pro past_due", "pro", "past_due", true},
		{"pro incomplete", "pro", "incomplete", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			org := uuid.NewString()
			_, err := db.Exec(`INSERT INTO billing.subscriptions (organization_id, plan_tier, status) VALUES ($1::uuid, $2, $3)`, org, tc.tier, tc.status)
			require.NoError(t, err)
			tier, err := ReadTier(ctx, db, org)
			require.NoError(t, err)
			require.Equal(t, !tc.free, tier.PaidSubscription)
			w, err := readAdmissionWallet(ctx, db, &tierCache{}, org)
			require.NoError(t, err)
			require.Equal(t, tc.free, w.Free())
		})
	}

	// No subscription row at all: free.
	tier, err := ReadTier(ctx, db, uuid.NewString())
	require.NoError(t, err)
	require.False(t, tier.PaidSubscription)
}

func TestPGReadWalletSurvivesMissingWalletStateColumn(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	// Deploy-order window: the gateway reads before billing migrated.
	_, err := db.Exec(`INSERT INTO billing.credit_balances (organization_id, balance_micros) VALUES ($1::uuid, 5000)`, org)
	require.NoError(t, err)
	w, err := ReadWallet(ctx, db, org)
	require.NoError(t, err)
	require.Equal(t, WalletActive, w.State)
	require.Equal(t, int64(5000), w.Available())

	// An org with no balance row at all reads zero and active.
	w, err = ReadWallet(ctx, db, uuid.NewString())
	require.NoError(t, err)
	require.Equal(t, int64(0), w.Available())
	require.Equal(t, WalletActive, w.State)
}

func TestPGWalletStateMigrationUpDownAndShadowRead(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	seedOrgsTable(t, db)
	up, down := walletStateMigration(t, "up.sql"), walletStateMigration(t, "down.sql")

	existing := uuid.NewString()
	_, err := db.Exec(`INSERT INTO billing.credit_balances (organization_id, balance_micros) VALUES ($1::uuid, 100)`, existing)
	require.NoError(t, err)

	_, err = db.Exec(up)
	require.NoError(t, err, "credit_wallet_state up")
	_, err = db.Exec(up)
	require.NoError(t, err, "credit_wallet_state up is re-runnable")

	var state string
	require.NoError(t, db.Get(&state, `SELECT wallet_state FROM billing.credit_balances WHERE organization_id = $1::uuid`, existing))
	require.Equal(t, WalletActive, state, "existing rows default to active")
	_, err = db.Exec(`UPDATE billing.credit_balances SET wallet_state = 'deleted' WHERE organization_id = $1::uuid`, existing)
	require.Error(t, err, "S4 CHECK admits only active|frozen")

	// The settle path's upsert keeps working with the new columns.
	require.NoError(t, DebitUnit(ctx, db, SettleUnit{ID: uuid.NewString(), OrgID: existing, Modality: MetricChat, Micros: 40}))

	// Frozen wallet with funds and an active hold.
	_, err = db.Exec(`UPDATE billing.credit_balances SET wallet_state = 'frozen', frozen_at = NOW() WHERE organization_id = $1::uuid`, existing)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO billing.credit_holds (organization_id, amount_micros, state) VALUES ($1::uuid, 10, 'held'), ($1::uuid, 999, 'released')`, existing)
	require.NoError(t, err)
	w, err := readAdmissionWallet(ctx, db, nil, existing)
	require.NoError(t, err)
	require.Equal(t, WalletFrozen, w.State)
	require.Equal(t, int64(-40), w.BalanceMicros, "balance recomputed from the ledger by DebitUnit")
	require.Equal(t, int64(10), w.HeldMicros, "only held holds count")
	require.True(t, w.Free(), "no purchase and no org row: free")
	d := EvaluateAdmission(AdmissionInput{Wallet: w, EstimateMicros: 1, FreeAllowed: true})
	require.Equal(t, ReasonWalletFrozen, d.Reason)

	// Tier: a purchase makes the org paid; an enterprise plan_tier is not free.
	paid := uuid.NewString()
	_, err = db.Exec(`INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, idempotency_key) VALUES ($1::uuid, 'purchase', 10000000, 'purchase:' || $1)`, paid)
	require.NoError(t, err)
	tier, err := ReadTier(ctx, db, paid)
	require.NoError(t, err)
	require.True(t, tier.HasPurchase)
	ent := uuid.NewString()
	_, err = db.Exec(`INSERT INTO everstack.organizations (id, plan_tier) VALUES ($1::uuid, 'enterprise')`, ent)
	require.NoError(t, err)
	w, err = readAdmissionWallet(ctx, db, &tierCache{}, ent)
	require.NoError(t, err)
	require.False(t, w.Free())
	require.Equal(t, "enterprise", w.PlanTier)

	_, err = db.Exec(down)
	require.NoError(t, err, "credit_wallet_state down")
	_, err = db.Exec(down)
	require.NoError(t, err, "credit_wallet_state down is re-runnable")
	var cols int
	require.NoError(t, db.Get(&cols, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'billing' AND table_name = 'credit_balances' AND column_name IN ('wallet_state', 'frozen_at')`))
	require.Equal(t, 0, cols)
	// And the shadow read falls back to active once the column is gone.
	w2, err := ReadWallet(ctx, db, existing)
	require.NoError(t, err)
	require.Equal(t, WalletActive, w2.State)
}

// End to end over a real wallet: the meter's shadow path reads the wallet
// uncached and reports the would-deny reason the S5 hold would return.
func TestPGShadowAdmitReportsWouldDenyFromTheWallet(t *testing.T) {
	db := newScratchDB(t)
	seedOrgsTable(t, db)
	_, err := db.Exec(walletStateMigration(t, "up.sql"))
	require.NoError(t, err)
	m := &Meter{billingDB: db, shadow: newShadowRunner(4, shadowReadTimeout)}

	broke, frozen, funded := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(`
		INSERT INTO billing.credit_balances (organization_id, balance_micros, wallet_state) VALUES
			($1::uuid, 0, 'active'), ($2::uuid, 9000000, 'frozen'), ($3::uuid, 9000000, 'active')`, broke, frozen, funded)
	require.NoError(t, err)

	for _, tc := range []struct {
		org, model, reason string
		free               bool
	}{
		{broke, "pg-zero-balance", ReasonInsufficientCredit, true},
		{frozen, "pg-frozen", ReasonWalletFrozen, true},
		{funded, "pg-free-allowlist-miss", ReasonModelNotInFreeAllowlist, false},
	} {
		before := wouldDeny(SurfaceInference, tc.reason, tc.model)
		m.ShadowAdmit(AdmissionRequest{OrgID: tc.org, Model: tc.model, EstimateMicros: 500, FreeAllowed: tc.free})
		m.shadow.wait()
		require.Equal(t, 1.0, wouldDeny(SurfaceInference, tc.reason, tc.model)-before, tc.reason)
	}

	admits := evaluated(SurfaceInference, "admit")
	m.ShadowAdmit(AdmissionRequest{OrgID: funded, Model: "pg-ok", EstimateMicros: 500, FreeAllowed: true})
	m.shadow.wait()
	require.Equal(t, 1.0, evaluated(SurfaceInference, "admit")-admits)

	// Compute: zero estimate, wallet state and balance only. The tenant is
	// resolved to its organization in the background, including a managed
	// instance id.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS everstack.managed_instances (id UUID PRIMARY KEY, organization_id UUID NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO everstack.organizations (id) VALUES ($1::uuid), ($2::uuid)`, broke, funded)
	require.NoError(t, err)
	instance := uuid.NewString()
	_, err = db.Exec(`INSERT INTO everstack.managed_instances (id, organization_id) VALUES ($1::uuid, $2::uuid)`, instance, broke)
	require.NoError(t, err)

	before := wouldDeny(SurfaceSandboxCreate, ReasonInsufficientCredit, "compute")
	ShadowAdmitComputeTenant(db, broke, SurfaceSandboxCreate)
	computeShadow.wait()
	require.Equal(t, 1.0, wouldDeny(SurfaceSandboxCreate, ReasonInsufficientCredit, "compute")-before)

	before = wouldDeny(SurfaceBrowserLease, ReasonInsufficientCredit, "compute")
	ShadowAdmitComputeTenant(db, instance, SurfaceBrowserLease)
	computeShadow.wait()
	require.Equal(t, 1.0, wouldDeny(SurfaceBrowserLease, ReasonInsufficientCredit, "compute")-before, "instance id resolves to its org")

	admitted := evaluated(SurfaceSandboxRevive, "admit")
	ShadowAdmitComputeTenant(db, funded, SurfaceSandboxRevive)
	computeShadow.wait()
	require.Equal(t, 1.0, evaluated(SurfaceSandboxRevive, "admit")-admitted)

	skipped := evaluated(SurfaceSandboxCreate, "skipped_no_org")
	ShadowAdmitComputeTenant(db, uuid.NewString(), SurfaceSandboxCreate)
	computeShadow.wait()
	require.Equal(t, 1.0, evaluated(SurfaceSandboxCreate, "skipped_no_org")-skipped)
	var holds int
	require.NoError(t, db.Get(&holds, `SELECT COUNT(*) FROM billing.credit_holds`))
	require.Equal(t, 0, holds, "dry-run writes nothing")
}
