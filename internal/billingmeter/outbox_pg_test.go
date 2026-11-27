package billingmeter

// Real-Postgres tests for the settle path: the ledger debit's idempotency and
// the outbox park-and-replay round trip, plus up/down validation of the
// billing_settle_outbox migration. Skips unless EVS_TEST_PG_DSN points at a
// Postgres the test may create databases on (for example
// postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable). It
// creates a scratch database, applies the wallet migration (billing.*) and the
// outbox migration (everstack.*) into it, and drops the database afterwards.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func outboxMigrationDir(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "database", "migrations", "sql", "postgres", "billing_settle_outbox_*"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "exactly one billing_settle_outbox migration expected")
	return matches[0]
}

func newScratchDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("EVS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EVS_TEST_PG_DSN not set; skipping real-Postgres settle test")
	}
	admin, err := sqlx.Connect("pgx", dsn)
	require.NoError(t, err)

	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	dbName := "credits_s1_" + hex.EncodeToString(suffix[:])
	_, err = admin.Exec(`CREATE DATABASE ` + dbName)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + dbName + ` WITH (FORCE)`)
		admin.Close()
		t.Logf("dropped scratch database %s", dbName)
	})
	t.Logf("created scratch database %s", dbName)

	scratchDSN := dsn
	if i := strings.LastIndex(dsn, "/"); i > 0 {
		rest := dsn[i+1:]
		q := ""
		if j := strings.Index(rest, "?"); j >= 0 {
			q = rest[j:]
		}
		scratchDSN = dsn[:i+1] + dbName + q
	}
	db, err := sqlx.Connect("pgx", scratchDSN)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE SCHEMA IF NOT EXISTS billing; CREATE SCHEMA IF NOT EXISTS everstack;`)
	require.NoError(t, err)
	wallet, err := os.ReadFile(filepath.Join(repoRoot(t), "services", "billing", "internal", "database", "migrations", "postgres", "credit_wallet_20260730191842", "up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(wallet))
	require.NoError(t, err, "credit_wallet up")
	up, err := os.ReadFile(filepath.Join(outboxMigrationDir(t), "up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err, "billing_settle_outbox up")
	applyAttemptMigrations(t, db)
	return db
}

func metaField(t *testing.T, raw []byte, key string) any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m[key]
}

func ledgerRows(t *testing.T, db *sqlx.DB, org string) []struct {
	Key    string `db:"idempotency_key"`
	Micros int64  `db:"amount_micros"`
	Meta   []byte `db:"metadata"`
} {
	t.Helper()
	var rows []struct {
		Key    string `db:"idempotency_key"`
		Micros int64  `db:"amount_micros"`
		Meta   []byte `db:"metadata"`
	}
	require.NoError(t, db.Select(&rows, `SELECT idempotency_key, amount_micros, metadata FROM billing.credit_ledger WHERE organization_id = $1::uuid ORDER BY created_at`, org))
	return rows
}

func TestPGDebitUnitIdempotentPerSettleUnit(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	// Two settle units under one correlation id: two rows. Replaying the
	// first unit: still two rows.
	u1 := SettleUnit{ID: uuid.NewString(), OrgID: org, Modality: MetricChat, Micros: 700, At: now,
		Metadata: map[string]any{MetaCorrelationID: "corr-1", MetaProvenance: ProvenancePlatform, MetaInternal: false}}
	u2 := SettleUnit{ID: uuid.NewString(), OrgID: org, Modality: MetricChat, Micros: 300, At: now,
		Metadata: map[string]any{MetaCorrelationID: "corr-1", MetaProvenance: ProvenancePlatform, MetaInternal: true}}
	require.NoError(t, DebitUnit(ctx, db, u1))
	require.NoError(t, DebitUnit(ctx, db, u2))
	require.NoError(t, DebitUnit(ctx, db, u1))

	rows := ledgerRows(t, db, org)
	require.Len(t, rows, 2)
	require.Equal(t, u1.IdempotencyKey(), rows[0].Key)
	require.Equal(t, int64(-700), rows[0].Micros)
	require.Equal(t, "platform", metaField(t, rows[0].Meta, MetaProvenance))
	require.Equal(t, true, metaField(t, rows[1].Meta, MetaInternal))

	var balance int64
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, org))
	require.Equal(t, int64(-1000), balance)
}

func TestPGOutboxParkAndReplayLandsExactlyOnce(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	u := SettleUnit{ID: uuid.NewString(), OrgID: org, Modality: MetricEmbeddings, Micros: 4200, At: time.Now().UTC(),
		Metadata: map[string]any{MetaCorrelationID: "corr-9", MetaProvenance: ProvenancePlatform}}

	// Park it twice (a unit retried in-process and then parked again on the
	// drain path): one outbox row.
	require.NoError(t, OutboxInsert(ctx, db, u, errors.New("platform db down")))
	require.NoError(t, OutboxInsert(ctx, db, u, errors.New("platform db down")))
	depth, err := OutboxDepth(ctx, db)
	require.NoError(t, err)
	require.Equal(t, int64(1), depth)

	// Not due yet (backoff): a replay pass lands nothing.
	landed, err := outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 0, landed)

	// Make it due, but the platform DB still fails: attempts and backoff grow,
	// the row stays.
	_, err = db.Exec(`UPDATE everstack.billing_settle_outbox SET next_attempt_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	failing := func(context.Context, *sqlx.DB, SettleUnit) error { return errors.New("still down") }
	landed, err = outboxReplayOnce(ctx, db, db, failing)
	require.NoError(t, err)
	require.Equal(t, 0, landed)
	var attempts int
	var due time.Time
	require.NoError(t, db.QueryRow(`SELECT attempts, next_attempt_at FROM everstack.billing_settle_outbox`).Scan(&attempts, &due))
	require.Equal(t, 1, attempts)
	require.True(t, due.After(time.Now()), "failed replay must back off")

	// Platform DB recovers: the replay lands the same key exactly once and
	// the row is gone; a further pass is a no-op and the ledger is unchanged.
	_, err = db.Exec(`UPDATE everstack.billing_settle_outbox SET next_attempt_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	landed, err = outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 1, landed)
	depth, err = OutboxDepth(ctx, db)
	require.NoError(t, err)
	require.Equal(t, int64(0), depth)
	rows := ledgerRows(t, db, org)
	require.Len(t, rows, 1)
	require.Equal(t, u.IdempotencyKey(), rows[0].Key)
	require.Equal(t, int64(-4200), rows[0].Micros)
	require.Equal(t, "corr-9", metaField(t, rows[0].Meta, MetaCorrelationID))

	landed, err = outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 0, landed)
	require.Len(t, ledgerRows(t, db, org), 1)

	// Replaying a key the ledger already holds (the debit landed but the
	// outbox delete was lost) is a no-op on the ledger and clears the row.
	require.NoError(t, OutboxInsert(ctx, db, u, errors.New("delete lost")))
	_, err = db.Exec(`UPDATE everstack.billing_settle_outbox SET next_attempt_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	landed, err = outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 1, landed)
	require.Len(t, ledgerRows(t, db, org), 1)
}

// The meter end to end against Postgres: a debit function that fails parks
// the unit, and the replay loop lands it.
func TestPGMeterParksFailedSettleAndReplays(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	org := uuid.NewString()
	failing := func(context.Context, *sqlx.DB, SettleUnit) error { return errors.New("ledger unavailable") }
	m := New(db, db)
	m.worker.stop()
	m.worker = newSettleWorker(db, 8, 1, failing, m.park)
	u := SettleUnit{ID: uuid.NewString(), OrgID: org, Modality: MetricChat, Micros: 99, At: time.Now().UTC()}
	m.Settle(ctx, u)
	m.worker.stop()
	depth, err := OutboxDepth(ctx, db)
	require.NoError(t, err)
	require.Equal(t, int64(1), depth, "failed settle must be parked")

	_, err = db.Exec(`UPDATE everstack.billing_settle_outbox SET next_attempt_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	landed, err := outboxReplayOnce(ctx, db, db, DebitUnit)
	require.NoError(t, err)
	require.Equal(t, 1, landed)
	require.Len(t, ledgerRows(t, db, org), 1)
}

func TestPGOutboxMigrationDown(t *testing.T) {
	db := newScratchDB(t)
	down, err := os.ReadFile(filepath.Join(outboxMigrationDir(t), "down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err, "billing_settle_outbox down")
	var exists bool
	require.NoError(t, db.Get(&exists, `SELECT to_regclass('everstack.billing_settle_outbox') IS NOT NULL`))
	require.False(t, exists, "down must drop the outbox table")
	// Up again is idempotent (IF NOT EXISTS).
	up, err := os.ReadFile(filepath.Join(outboxMigrationDir(t), "up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
}

// The byok-correction migration reverses every pre-seam inference debit
// (no metadata.provenance) as a positive adjustment keyed on the original
// ledger id, leaves seam-written debits alone, rebuilds balances, is
// idempotent, and its down removes exactly what it added.
func TestPGByokCorrectionMigration(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "services", "billing", "internal", "database", "migrations", "postgres", "byok_correction_*"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	up, err := os.ReadFile(filepath.Join(matches[0], "up.sql"))
	require.NoError(t, err)
	down, err := os.ReadFile(filepath.Join(matches[0], "down.sql"))
	require.NoError(t, err)

	acme := uuid.NewString()
	other := uuid.NewString()
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	seed := func(org, key string, micros int64, meta string) {
		_, err := db.Exec(`INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, metric_type, idempotency_key, period_start, period_end, metadata)
			VALUES ($1::uuid, 'debit', $2, 'inference', 'chat', $3, $4, $5, $6::jsonb)`, org, micros, key, period, period.AddDate(0, 1, 0), meta)
		require.NoError(t, err)
	}
	// The one prod row (pre-seam, metadata {}), a second pre-seam row for
	// another org, and one seam-written platform debit that must stay.
	seed(acme, "inference:chat_01m0xjywqeevd95h9n2sz6ya82", -567, `{}`)
	seed(other, "inference:chat_legacy2", -1000, `{"foo":"bar"}`)
	seed(other, "inference:"+uuid.NewString(), -250, `{"provenance":"platform"}`)
	_, err = db.Exec(`INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, idempotency_key) VALUES ($1::uuid, 'grant', 5000000, $2)`, acme, "signup-grant:"+acme)
	require.NoError(t, err)

	_, err = db.Exec(string(up))
	require.NoError(t, err, "byok_correction up")

	var corrections []struct {
		Org    string `db:"organization_id"`
		Micros int64  `db:"amount_micros"`
		Key    string `db:"idempotency_key"`
		Meta   []byte `db:"metadata"`
	}
	require.NoError(t, db.SelectContext(ctx, &corrections, `SELECT organization_id::text, amount_micros, idempotency_key, metadata FROM billing.credit_ledger WHERE entry_type = 'adjustment' ORDER BY amount_micros`))
	require.Len(t, corrections, 2, "exactly the two pre-seam debits are corrected")
	require.Equal(t, int64(567), corrections[0].Micros)
	require.Equal(t, acme, corrections[0].Org)
	require.Equal(t, "byok_correction", metaField(t, corrections[0].Meta, "reason"))
	require.Equal(t, "inference:chat_01m0xjywqeevd95h9n2sz6ya82", metaField(t, corrections[0].Meta, "original_idempotency_key"))
	require.True(t, strings.HasPrefix(corrections[0].Key, "byok-correction:"))
	require.Equal(t, int64(1000), corrections[1].Micros)

	var balance int64
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, acme))
	require.Equal(t, int64(5000000), balance, "acme's grant is whole again")
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, other))
	require.Equal(t, int64(-250), balance, "only the platform debit remains")

	// Idempotent.
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	var n int
	require.NoError(t, db.Get(&n, `SELECT COUNT(*) FROM billing.credit_ledger WHERE entry_type = 'adjustment'`))
	require.Equal(t, 2, n)

	// Down removes the corrections and rebuilds the balances.
	_, err = db.Exec(string(down))
	require.NoError(t, err, "byok_correction down")
	require.NoError(t, db.Get(&n, `SELECT COUNT(*) FROM billing.credit_ledger WHERE entry_type = 'adjustment'`))
	require.Equal(t, 0, n)
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, acme))
	require.Equal(t, int64(5000000-567), balance)
	require.NoError(t, db.Get(&balance, `SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1::uuid`, other))
	require.Equal(t, int64(-1250), balance)
}

func usageMigrationDir(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "database", "migrations", "sql", "postgres", "inference_usage_counters_*"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	return matches[0]
}

// Two gateway replicas counting the same organization in the same hour land
// in one row whose values are the sum: the increment happens in the
// database. Counts file under the hour they happened in, and a window sum
// picks exactly the hours that start inside it.
func TestPGUsageCountersAcrossReplicas(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	up, err := os.ReadFile(filepath.Join(usageMigrationDir(t), "up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)

	hour := time.Date(2026, 9, 30, 10, 15, 0, 0, time.UTC)
	clock := func() time.Time { return hour }
	replicaA := newUsageCounterWithClock(db, 5*time.Millisecond, clock)
	replicaB := newUsageCounterWithClock(db, 5*time.Millisecond, clock)
	var wg sync.WaitGroup
	for _, c := range []*usageCounter{replicaA, replicaB} {
		wg.Add(1)
		go func(c *usageCounter) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				c.add("org-1", UsageDelta{Requests: 1, InputTokens: 10, OutputTokens: 5})
				time.Sleep(100 * time.Microsecond)
			}
		}(c)
	}
	wg.Wait()
	replicaB.add("org-2", UsageDelta{Requests: 1, InputTokens: 7, OutputTokens: 3})
	replicaA.add("org-1", UsageDelta{}) // zero delta is a no-op
	replicaA.close()
	replicaB.close()

	var rows int
	require.NoError(t, db.Get(&rows, `SELECT COUNT(*) FROM everstack.inference_usage_counters WHERE organization_id = 'org-1'`))
	require.Equal(t, 1, rows, "both replicas share one (org, hour) row")

	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	got, err := UsageTotalsBetween(ctx, db, "org-1", monthStart, monthEnd)
	require.NoError(t, err)
	require.Equal(t, UsageTotals{Requests: 100, InputTokens: 1000, OutputTokens: 500}, got)
	require.Equal(t, int64(1500), got.TotalTokens())

	// A later hour in the next month is a separate row and a separate period.
	require.NoError(t, UsageIncrement(ctx, db, "org-1", time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC), UsageDelta{Requests: 2, InputTokens: 4}))
	got, err = UsageTotalsBetween(ctx, db, "org-1", monthStart, monthEnd)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.Requests, "October hour must not count in September")
	got, err = UsageTotalsBetween(ctx, db, "org-1", monthEnd, monthEnd.AddDate(0, 1, 0))
	require.NoError(t, err)
	require.Equal(t, UsageTotals{Requests: 2, InputTokens: 4}, got)

	orgs, err := UsageOrgsSince(ctx, db, monthStart)
	require.NoError(t, err)
	require.Equal(t, []string{"org-1", "org-2"}, orgs)
	orgs, err = UsageOrgsSince(ctx, db, monthEnd)
	require.NoError(t, err)
	require.Equal(t, []string{"org-1"}, orgs)

	// The meter's own counting path.
	m := New(db, db)
	m.CountUsage("org-2", UsageDelta{Requests: 1, InputTokens: 1})
	m.CountUsage("", UsageDelta{Requests: 1})
	m.Shutdown(ctx)
	got, err = UsageTotalsBetween(ctx, db, "org-2", time.Time{}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Requests)

	down, err := os.ReadFile(filepath.Join(usageMigrationDir(t), "down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	var exists bool
	require.NoError(t, db.Get(&exists, `SELECT to_regclass('everstack.inference_usage_counters') IS NOT NULL`))
	require.False(t, exists)
	_, err = db.Exec(string(up))
	require.NoError(t, err, "up after down")
	_, err = db.Exec(string(up))
	require.NoError(t, err, "up is re-runnable")
}

// A flush that fails (table missing) requeues the delta in memory; once the
// table exists the next flush lands it exactly once.
func TestPGUsageFlushFailureRequeues(t *testing.T) {
	db := newScratchDB(t)
	ctx := context.Background()
	c := &usageCounter{db: db, now: time.Now, pending: map[usageKey]*UsageDelta{}}
	c.add("org-1", UsageDelta{Requests: 3, InputTokens: 30})
	c.addAt(usageKey{org: "org-1", bucket: time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)}, UsageDelta{Requests: 9})
	c.flush(ctx)
	require.Len(t, c.pending, 1, "failed flush must requeue recent counts and drop ones past the retry horizon")
	up, err := os.ReadFile(filepath.Join(usageMigrationDir(t), "up.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)
	c.flush(ctx)
	c.flush(ctx)
	got, err := UsageTotalsBetween(ctx, db, "org-1", time.Time{}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, UsageTotals{Requests: 3, InputTokens: 30}, got)
}
