package billingmeter

// Real-Postgres tests (EVS_TEST_PG_DSN) for spend attribution on
// billing.inference_attempts (docs/design/spend-limits-v2.md, slice S1):
// api_key_id, est_cost_micros and unpriced land with the attempt, in the
// same single insert, for charged and record-only attempts alike.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type attributionRow struct {
	ID       string         `db:"id"`
	APIKeyID sql.NullString `db:"api_key_id"`
	EstCost  sql.NullInt64  `db:"est_cost_micros"`
	Unpriced bool           `db:"unpriced"`
	Charged  bool           `db:"charged"`
	Amount   int64          `db:"amount_micros"`
}

func attributionRows(t *testing.T, db interface {
	Select(dest any, query string, args ...any) error
}, org string) map[string]attributionRow {
	t.Helper()
	var rows []attributionRow
	require.NoError(t, db.Select(&rows, `
		SELECT id::text, api_key_id, est_cost_micros, unpriced, charged, amount_micros
		FROM billing.inference_attempts WHERE organization_id = $1::uuid`, org))
	out := map[string]attributionRow{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func i64(n int64) *int64 { return &n }

func TestPGAttemptAttribution(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()

	byokA := testAttempt(org, "req-1", RoleServed, ProvenanceBYOK, 0, 1000, 500)
	byokA.Attempt.APIKeyID, byokA.Attempt.EstCostMicros = "key-A", i64(25000)
	byokB := testAttempt(org, "req-2", RoleRaceLoser, ProvenanceBYOK, 0, 10, 10)
	byokB.Attempt.APIKeyID, byokB.Attempt.EstCostMicros = "key-B", i64(400)
	unpriced := testAttempt(org, "req-3", RoleServed, ProvenanceBYOK, 0, 10, 10)
	unpriced.Attempt.APIKeyID, unpriced.Attempt.EstCostMicros, unpriced.Attempt.Unpriced = "key-A", i64(0), true
	noKey := testAttempt(org, "req-4", RoleServed, ProvenanceBYOK, 0, 10, 10)
	noKey.Attempt.EstCostMicros = i64(7)
	platform := testAttempt(org, "req-5", RoleServed, ProvenancePlatform, 900, 100, 40)
	platform.Attempt.APIKeyID = "key-A"
	for _, u := range []SettleUnit{byokA, byokB, unpriced, noKey, platform} {
		require.NoError(t, DebitUnit(ctx, db, u))
		require.NoError(t, DebitUnit(ctx, db, u), "replay is a no-op")
	}

	rows := attributionRows(t, db, org)
	require.Len(t, rows, 5, "exactly one row per attempt, replays included")

	require.Equal(t, "key-A", rows[byokA.ID].APIKeyID.String)
	require.Equal(t, int64(25000), rows[byokA.ID].EstCost.Int64)
	require.True(t, rows[byokA.ID].EstCost.Valid)
	require.False(t, rows[byokA.ID].Unpriced)
	require.False(t, rows[byokA.ID].Charged)

	require.Equal(t, "key-B", rows[byokB.ID].APIKeyID.String)
	require.Equal(t, int64(400), rows[byokB.ID].EstCost.Int64)

	require.True(t, rows[unpriced.ID].Unpriced)
	require.True(t, rows[unpriced.ID].EstCost.Valid)
	require.Zero(t, rows[unpriced.ID].EstCost.Int64)

	require.False(t, rows[noKey.ID].APIKeyID.Valid, "no API key is NULL, never ''")

	// Platform keeps its charged amount and carries no estimate.
	require.True(t, rows[platform.ID].Charged)
	require.Equal(t, int64(900), rows[platform.ID].Amount)
	require.False(t, rows[platform.ID].EstCost.Valid)
	require.Equal(t, "key-A", rows[platform.ID].APIKeyID.String)

	// Per-key BYOK spend, as S2 will roll it up, scoped to the org.
	var perKey []struct {
		Key string `db:"api_key_id"`
		Est int64  `db:"est"`
	}
	require.NoError(t, db.Select(&perKey, `
		SELECT api_key_id, SUM(est_cost_micros)::BIGINT AS est FROM billing.inference_attempts
		WHERE organization_id = $1::uuid AND api_key_id IS NOT NULL AND est_cost_micros IS NOT NULL
		GROUP BY api_key_id ORDER BY api_key_id`, org))
	require.Equal(t, []struct {
		Key string `db:"api_key_id"`
		Est int64  `db:"est"`
	}{{"key-A", 25000}, {"key-B", 400}}, perKey)

	// Another org's rows are invisible to this org's query.
	other := uuid.NewString()
	foreign := testAttempt(other, "req-x", RoleServed, ProvenanceBYOK, 0, 10, 10)
	foreign.Attempt.APIKeyID, foreign.Attempt.EstCostMicros = "key-A", i64(999)
	require.NoError(t, DebitUnit(ctx, db, foreign))
	require.Len(t, attributionRows(t, db, org), 5)
	require.Len(t, attributionRows(t, db, other), 1)
}

// A negative estimate can never be stored, whatever the writer passes.
func TestPGNegativeEstimateRefused(t *testing.T) {
	db := newScratchDB(t)
	org := uuid.NewString()
	_, err := db.Exec(`
		INSERT INTO billing.inference_attempts (id, organization_id, provenance, est_cost_micros, occurred_at)
		VALUES ($1::uuid, $2::uuid, 'byok', -1, NOW())`, uuid.NewString(), org)
	require.Error(t, err)
	// The writer clamps rather than failing the record.
	u := testAttempt(org, "req", RoleServed, ProvenanceBYOK, 0, 1, 1)
	u.Attempt.EstCostMicros = i64(-5)
	require.NoError(t, DebitUnit(context.Background(), db, u))
	require.Zero(t, attributionRows(t, db, org)[u.ID].EstCost.Int64)
}

// The migration rolls back and forward cleanly.
func TestPGAttributionMigrationDownUp(t *testing.T) {
	db := newScratchDB(t)
	dir := attributionMigrationDir(t)
	for _, f := range []string{"down.sql", "up.sql", "up.sql"} {
		sqlText, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err)
		_, err = db.Exec(string(sqlText))
		require.NoError(t, err, f)
	}
	u := testAttempt(uuid.NewString(), "req", RoleServed, ProvenanceBYOK, 0, 1, 1)
	u.Attempt.APIKeyID, u.Attempt.EstCostMicros = "key-A", i64(3)
	require.NoError(t, DebitUnit(context.Background(), db, u))
}

// The record rides the settle outbox as JSON: the new fields survive a
// replay, and a record written before them decodes with no estimate.
func TestAttemptAttributionSurvivesOutboxEncoding(t *testing.T) {
	a := &AttemptRecord{Role: RoleServed, Provenance: ProvenanceBYOK, APIKeyID: "key-A", EstCostMicros: i64(0), Unpriced: true}
	got := decodeAttempt(encodeAttempt(a))
	require.NotNil(t, got)
	require.Equal(t, "key-A", got.APIKeyID)
	require.NotNil(t, got.EstCostMicros, "an explicit $0 estimate is kept, not dropped")
	require.Zero(t, *got.EstCostMicros)
	require.True(t, got.Unpriced)

	legacy, err := json.Marshal(map[string]any{"role": RoleServed, "provenance": ProvenanceBYOK})
	require.NoError(t, err)
	old := decodeAttempt(legacy)
	require.Nil(t, old.EstCostMicros)
	require.Empty(t, old.APIKeyID)
}
