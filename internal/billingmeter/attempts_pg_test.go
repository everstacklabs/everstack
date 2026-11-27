package billingmeter

// Real-Postgres tests for billed inference attempt records
// (billing.inference_attempts): one row per provider attempt, charged ones
// landing in the same transaction as their ledger debit, uncharged ones
// recorded without touching the wallet, every write idempotent on the settle
// unit id across replays (in-process retry, outbox replay, backfill).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func migrationDir(t *testing.T, parts ...string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	require.NoError(t, err)
	require.Len(t, matches, 1, "exactly one migration expected for %v", parts)
	return matches[0]
}

func attemptsMigrationDir(t *testing.T) string {
	return migrationDir(t, "services", "billing", "internal", "database", "migrations", "postgres", "inference_attempts_2*")
}

func attributionMigrationDir(t *testing.T) string {
	return migrationDir(t, "services", "billing", "internal", "database", "migrations", "postgres", "inference_attempts_attribution_*")
}

func outboxAttemptsMigrationDir(t *testing.T) string {
	return migrationDir(t, "internal", "database", "migrations", "sql", "postgres", "settle_outbox_attempt_records_*")
}

func applyAttemptMigrations(t *testing.T, db *sqlx.DB) {
	t.Helper()
	for _, dir := range []string{attemptsMigrationDir(t), attributionMigrationDir(t), outboxAttemptsMigrationDir(t)} {
		up, err := os.ReadFile(filepath.Join(dir, "up.sql"))
		require.NoError(t, err)
		_, err = db.Exec(string(up))
		require.NoError(t, err, "%s up", filepath.Base(dir))
	}
}

type attemptRow struct {
	ID            string  `db:"id"`
	Org           string  `db:"organization_id"`
	RequestID     string  `db:"request_id"`
	Role          *string `db:"role"`
	Provider      string  `db:"provider"`
	Model         string  `db:"model"`
	Provenance    string  `db:"provenance"`
	InputTokens   *int64  `db:"input_tokens"`
	OutputTokens  *int64  `db:"output_tokens"`
	Estimated     bool    `db:"estimated"`
	Charged       bool    `db:"charged"`
	AmountMicros  int64   `db:"amount_micros"`
	LedgerEntryID *string `db:"ledger_entry_id"`
	Backfilled    bool    `db:"backfilled"`
}

func attemptRows(t *testing.T, db *sqlx.DB, org string) []attemptRow {
	t.Helper()
	var rows []attemptRow
	require.NoError(t, db.Select(&rows, `
		SELECT id::text, organization_id::text, request_id, role, provider, model, provenance,
		       input_tokens, output_tokens, estimated, charged, amount_micros, ledger_entry_id::text, backfilled
		FROM billing.inference_attempts WHERE organization_id = $1::uuid ORDER BY occurred_at, id`, org))
	return rows
}

func testAttempt(org, request, role, provenance string, micros int64, in, out int64) SettleUnit {
	return SettleUnit{
		ID: uuid.NewString(), OrgID: org, Modality: MetricChat, Micros: micros,
		At:       time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
		Metadata: map[string]any{MetaCorrelationID: request, MetaProvenance: provenance},
		Attempt: &AttemptRecord{
			RequestID: request, Role: role, Provider: "openai", Model: "gpt-4o-mini",
			Provenance: provenance, InputTokens: in, OutputTokens: out,
		},
	}
}

// Every role lands as its own row; charged roles carry the ledger entry they
// debited, the platform retry and the BYOK attempt are recorded but debit
// nothing.
func TestPGAttemptRecordsEachRole(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()

	served := testAttempt(org, "req-1", RoleServed, ProvenancePlatform, 900, 100, 40)
	loser := testAttempt(org, "req-1", RoleRaceLoser, ProvenancePlatform, 300, 100, 12)
	fallback := testAttempt(org, "req-1", RoleFailedFallback, ProvenancePlatform, 200, 100, 5)
	retry := testAttempt(org, "req-1", RoleRetry, ProvenancePlatform, 150, 100, 3)
	platformRetry := testAttempt(org, "req-1", RolePlatformRetry, ProvenancePlatform, 0, 100, 7)
	byok := testAttempt(org, "req-2", RoleServed, ProvenanceBYOK, 0, 80, 20)
	for _, u := range []SettleUnit{served, loser, fallback, retry, platformRetry, byok} {
		require.NoError(t, DebitUnit(ctx, db, u))
	}

	rows := attemptRows(t, db, org)
	require.Len(t, rows, 6)
	byID := map[string]attemptRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	ledgerIDs := map[string]string{}
	var ledger []struct {
		ID  string `db:"id"`
		Key string `db:"idempotency_key"`
	}
	require.NoError(t, db.Select(&ledger, `SELECT id::text, idempotency_key FROM billing.credit_ledger WHERE organization_id = $1::uuid`, org))
	require.Len(t, ledger, 4, "only the four charged attempts debit the wallet")
	for _, l := range ledger {
		ledgerIDs[l.Key] = l.ID
	}

	for _, u := range []SettleUnit{served, loser, fallback, retry} {
		r := byID[u.ID]
		require.True(t, r.Charged, u.Attempt.Role)
		require.Equal(t, u.Micros, r.AmountMicros, u.Attempt.Role)
		require.NotNil(t, r.LedgerEntryID, u.Attempt.Role)
		require.Equal(t, ledgerIDs[u.IdempotencyKey()], *r.LedgerEntryID, u.Attempt.Role)
		require.Equal(t, u.Attempt.Role, *r.Role)
		require.Equal(t, "req-1", r.RequestID)
	}
	for _, u := range []SettleUnit{platformRetry, byok} {
		r := byID[u.ID]
		require.False(t, r.Charged)
		require.Zero(t, r.AmountMicros)
		require.Nil(t, r.LedgerEntryID)
	}
	require.Equal(t, int64(7), *byID[platformRetry.ID].OutputTokens)
	require.Equal(t, "byok", byID[byok.ID].Provenance)

	var balance int64
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, org))
	require.Equal(t, int64(-1550), balance)
}

// A platform retry can never be charged, whatever amount reaches DebitUnit:
// the charge decision belongs to the role, and the record and the wallet must
// agree.
func TestPGPlatformRetryNeverDebits(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	u := testAttempt(org, "req-pr", RolePlatformRetry, ProvenancePlatform, 500, 10, 10)
	require.NoError(t, DebitUnit(ctx, db, u))
	require.Empty(t, ledgerRows(t, db, org))
	rows := attemptRows(t, db, org)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Charged)
	// A BYOK attempt likewise.
	b := testAttempt(org, "req-b", RoleServed, ProvenanceBYOK, 500, 10, 10)
	require.NoError(t, DebitUnit(ctx, db, b))
	require.Empty(t, ledgerRows(t, db, org))
}

// Replaying a settle unit (in-process retry, worker overlap, outbox replay)
// writes neither a second ledger row nor a second attempt row, and a replay
// after the attempt row was lost re-links the existing ledger entry.
func TestPGAttemptIdempotentAcrossReplay(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	u := testAttempt(org, "req-r", RoleServed, ProvenancePlatform, 1234, 50, 50)
	require.NoError(t, DebitUnit(ctx, db, u))
	require.NoError(t, DebitUnit(ctx, db, u))
	require.Len(t, ledgerRows(t, db, org), 1)
	rows := attemptRows(t, db, org)
	require.Len(t, rows, 1)
	ledgerID := *rows[0].LedgerEntryID

	// Lose the attempt row (as if it had been written by a pre-attempts
	// gateway), then replay: the ledger is untouched and the attempt row
	// comes back linked to the original entry.
	_, err := db.Exec(`DELETE FROM billing.inference_attempts WHERE id = $1::uuid`, u.ID)
	require.NoError(t, err)
	require.NoError(t, DebitUnit(ctx, db, u))
	require.Len(t, ledgerRows(t, db, org), 1)
	rows = attemptRows(t, db, org)
	require.Len(t, rows, 1)
	require.Equal(t, ledgerID, *rows[0].LedgerEntryID)

	// Record-only replays are idempotent too.
	r := testAttempt(org, "req-r", RolePlatformRetry, ProvenancePlatform, 0, 5, 5)
	require.NoError(t, DebitUnit(ctx, db, r))
	require.NoError(t, DebitUnit(ctx, db, r))
	require.Len(t, attemptRows(t, db, org), 2)
}

// Concurrent settles of one unit from two replicas debit once.
func TestPGAttemptConcurrentReplicasDebitOnce(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	u := testAttempt(org, "req-c", RoleRaceLoser, ProvenancePlatform, 777, 9, 9)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { errs <- DebitUnit(ctx, db, u) }()
	}
	for i := 0; i < 8; i++ {
		require.NoError(t, <-errs)
	}
	require.Len(t, ledgerRows(t, db, org), 1)
	require.Len(t, attemptRows(t, db, org), 1)
}

// An aborted stream's estimated attempt is recorded as estimated.
func TestPGAttemptEstimatedFlag(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	u := testAttempt(org, "req-e", RoleServed, ProvenancePlatform, 42, 30, 8)
	u.Attempt.Estimated = true
	u.Metadata[MetaUsageEstimated] = true
	require.NoError(t, DebitUnit(ctx, db, u))
	rows := attemptRows(t, db, org)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Estimated)
	require.True(t, rows[0].Charged)
}

// A parked unit, charged or record-only, replays from the outbox into the
// same attempt row the request path would have written.
func TestPGAttemptOutboxRoundTrip(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	charged := testAttempt(org, "req-o", RoleFailedFallback, ProvenancePlatform, 640, 70, 20)
	charged.Attempt.Estimated = true
	recordOnly := testAttempt(org, "req-o", RolePlatformRetry, ProvenancePlatform, 0, 70, 3)
	require.NoError(t, OutboxInsert(ctx, db, charged, errors.New("down")))
	require.NoError(t, OutboxInsert(ctx, db, recordOnly, errors.New("down")))
	_, err := db.Exec(`UPDATE everstack.billing_settle_outbox SET next_attempt_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	landed, err := outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 2, landed)

	rows := attemptRows(t, db, org)
	require.Len(t, rows, 2)
	byID := map[string]attemptRow{rows[0].ID: rows[0], rows[1].ID: rows[1]}
	c := byID[charged.ID]
	require.Equal(t, RoleFailedFallback, *c.Role)
	require.True(t, c.Charged)
	require.True(t, c.Estimated)
	require.Equal(t, int64(20), *c.OutputTokens)
	require.NotNil(t, c.LedgerEntryID)
	ro := byID[recordOnly.ID]
	require.Equal(t, RolePlatformRetry, *ro.Role)
	require.False(t, ro.Charged)
	require.Len(t, ledgerRows(t, db, org), 1)
}

// Attempts are scoped to their organization: one org's request id never
// pulls another org's attempts, and a unit can only link a ledger entry of
// its own organization.
func TestPGAttemptTenantIsolation(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	orgA, orgB := uuid.NewString(), uuid.NewString()
	a := testAttempt(orgA, "shared-req", RoleServed, ProvenancePlatform, 100, 1, 1)
	b := testAttempt(orgB, "shared-req", RoleServed, ProvenancePlatform, 200, 2, 2)
	require.NoError(t, DebitUnit(ctx, db, a))
	require.NoError(t, DebitUnit(ctx, db, b))
	ra := attemptRows(t, db, orgA)
	rb := attemptRows(t, db, orgB)
	require.Len(t, ra, 1)
	require.Len(t, rb, 1)
	require.Equal(t, a.ID, ra[0].ID)
	require.Equal(t, b.ID, rb[0].ID)

	// A replay of org A's settle unit carrying org B's id: the unit id is
	// global, so it must neither charge org B nor attach org A's ledger
	// entry or attempt to org B.
	forged := a
	forged.OrgID = orgB
	require.NoError(t, DebitUnit(ctx, db, forged))
	require.Len(t, ledgerRows(t, db, orgB), 1, "org B is not charged for org A's settle unit")
	rb = attemptRows(t, db, orgB)
	require.Len(t, rb, 1, "org B gains no attempt row for org A's unit")
	ra = attemptRows(t, db, orgA)
	require.Len(t, ra, 1)
	require.Equal(t, orgA, ra[0].Org)
}

func TestPGAttemptMigrationsDownUp(t *testing.T) {
	db := newScratchDB(t)
	org := uuid.NewString()
	require.NoError(t, OutboxInsert(context.Background(), db, testAttempt(org, "r", RolePlatformRetry, ProvenancePlatform, 0, 1, 1), errors.New("x")))
	for _, dir := range []string{outboxAttemptsMigrationDir(t), attemptsMigrationDir(t)} {
		down, err := os.ReadFile(filepath.Join(dir, "down.sql"))
		require.NoError(t, err)
		_, err = db.Exec(string(down))
		require.NoError(t, err, "%s down", filepath.Base(dir))
	}
	var exists bool
	require.NoError(t, db.Get(&exists, `SELECT to_regclass('billing.inference_attempts') IS NOT NULL`))
	require.False(t, exists)
	depth, err := OutboxDepth(context.Background(), db)
	require.NoError(t, err)
	require.Zero(t, depth, "record-only rows cannot survive the restored constraint")
	applyAttemptMigrations(t, db)
	applyAttemptMigrations(t, db)
}
