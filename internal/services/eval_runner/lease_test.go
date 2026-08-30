package eval_runner

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Lease concurrency tests are gated on EVAL_RUNNER_PG_DSN, matching the
// existing eval_runner Postgres test harness. They intentionally use real
// Postgres because sqlmock/SQLite cannot verify SKIP LOCKED, ctid dedupe
// assumptions, or unique-index behavior under concurrent transactions.

func TestLeaseEnsureRunItemsConcurrentIdempotent(t *testing.T) {
	h := newLeaseHarness(t)
	db1 := h.openDB(t)
	defer db1.Close()
	db2 := h.openDB(t)
	defer db2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db1, "pending", 5)
	r1 := newLeaseTestRunner(db1, "worker-ensure-1")
	r2 := newLeaseTestRunner(db2, "worker-ensure-2")

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- r1.ensureRunItems(ctx, run)
	}()
	go func() {
		<-start
		errs <- r2.ensureRunItems(ctx, run)
	}()
	close(start)

	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("ensureRunItems %d: %v", i, err)
		}
	}

	var total, distinctItems, hashed int
	if err := db1.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT dataset_item_id), COUNT(*) FILTER (WHERE input_hash IS NOT NULL)
		FROM eval_run_items
		WHERE eval_run_id = $1
	`, run.ID).Scan(&total, &distinctItems, &hashed); err != nil {
		t.Fatalf("count run items: %v", err)
	}
	if total != 5 || distinctItems != 5 || hashed != 5 {
		t.Fatalf("items total=%d distinct=%d hashed=%d, want 5/5/5", total, distinctItems, hashed)
	}
}

func TestEnsureRunItemsUsesPinnedDatasetVersion(t *testing.T) {
	h := newLeaseHarness(t)
	db := h.openDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db, "pending", 1)
	itemID := fmt.Sprintf("item-%s-0", run.DatasetID)
	versionID := "version-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	versionItemID := "version-item-" + strings.ReplaceAll(uuid.NewString(), "-", "")

	if _, err := db.ExecContext(ctx, `
		INSERT INTO dataset_versions (
			id, dataset_id, tenant_id, version_number, label, note, item_count, created_by
		) VALUES ($1, $2, $3, 1, 'v1', '', 1, 'test')
	`, versionID, run.DatasetID, run.TenantID); err != nil {
		t.Fatalf("insert dataset version: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO dataset_version_items (
			id, dataset_version_id, tenant_id, source_dataset_item_id,
			input, expected_output, metadata, source_trace_id, source_observation_id
		) VALUES ($1, $2, $3, $4, '{"input":"frozen"}', '{"expected":"frozen"}', '{"source":"version"}', 'trace-v', 'obs-v')
	`, versionItemID, versionID, run.TenantID, itemID); err != nil {
		t.Fatalf("insert dataset version item: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE dataset_items
		SET input = '{"input":"live-mutated"}', expected_output = '{"expected":"live-mutated"}'
		WHERE id = $1 AND tenant_id = $2
	`, itemID, run.TenantID); err != nil {
		t.Fatalf("mutate live dataset item: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET dataset_version_id = $2
		WHERE id = $1 AND tenant_id = $3
	`, run.ID, versionID, run.TenantID); err != nil {
		t.Fatalf("pin eval run: %v", err)
	}
	run.DatasetVersionID = versionID

	r := newLeaseTestRunner(db, "worker-version")
	if err := r.ensureRunItems(ctx, run); err != nil {
		t.Fatalf("ensureRunItems: %v", err)
	}

	var canonical string
	if err := db.GetContext(ctx, &canonical, `
		SELECT input_canonical::text
		FROM eval_run_items
		WHERE eval_run_id = $1 AND dataset_item_id = $2
	`, run.ID, itemID); err != nil {
		t.Fatalf("select eval_run_item canonical: %v", err)
	}
	if !strings.Contains(canonical, "frozen") || strings.Contains(canonical, "live-mutated") {
		t.Fatalf("canonical input = %s, want frozen version input", canonical)
	}

	items, err := r.listPendingItems(ctx, run)
	if err != nil {
		t.Fatalf("listPendingItems: %v", err)
	}
	if len(items) != 1 || !strings.Contains(string(items[0].Input), "frozen") ||
		strings.Contains(string(items[0].Input), "live-mutated") {
		t.Fatalf("pending items = %+v, want frozen version payload", items)
	}
}

func TestLeaseClaimTwoWorkersSingleRun(t *testing.T) {
	h := newLeaseHarness(t)
	db1 := h.openDB(t)
	defer db1.Close()
	db2 := h.openDB(t)
	defer db2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db1, "pending", 3)
	r1 := newLeaseTestRunner(db1, "worker-claim-1")
	r2 := newLeaseTestRunner(db2, "worker-claim-2")

	type claimResult struct {
		r      *Runner
		claims []claimedEvalRun
		err    error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for _, r := range []*Runner{r1, r2} {
		go func(r *Runner) {
			<-start
			claims, err := r.claimRuns(ctx)
			results <- claimResult{r: r, claims: claims, err: err}
		}(r)
	}
	close(start)

	var owner *Runner
	var ownerClaim claimedEvalRun
	totalClaims := 0
	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("claim worker %d: %v", i, res.err)
		}
		totalClaims += len(res.claims)
		if len(res.claims) == 1 {
			owner = res.r
			ownerClaim = res.claims[0]
		}
	}
	if totalClaims != 1 || owner == nil || ownerClaim.ID != run.ID {
		t.Fatalf("total claims=%d owner=%v claim=%+v, want exactly one claim for %s", totalClaims, owner != nil, ownerClaim, run.ID)
	}

	processClaim(ctx, t, owner, ownerClaim)

	assertRunTerminal(ctx, t, db1, run.ID, "completed")
	var total, distinctItems int
	if err := db1.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT dataset_item_id)
		FROM eval_run_items
		WHERE eval_run_id = $1
	`, run.ID).Scan(&total, &distinctItems); err != nil {
		t.Fatalf("count run items: %v", err)
	}
	if total != 3 || distinctItems != 3 {
		t.Fatalf("items total=%d distinct=%d, want 3/3", total, distinctItems)
	}
}

func TestLeaseReclaimResetsStaleRunningItemsAndCompletes(t *testing.T) {
	h := newLeaseHarness(t)
	db := h.openDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db, "running", 2)
	seedRunner := newLeaseTestRunner(db, "worker-seed")
	if err := seedRunner.ensureRunItems(ctx, run); err != nil {
		t.Fatalf("ensureRunItems: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_run_items SET status = 'running', updated_at = NOW()
		WHERE eval_run_id = $1
	`, run.ID); err != nil {
		t.Fatalf("mark items running: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET status = 'running',
			lease_owner = 'dead-worker',
			lease_expires_at = NOW() - INTERVAL '1 second',
			lease_epoch = 7
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("seed expired lease: %v", err)
	}

	reclaimer := newLeaseTestRunner(db, "worker-reclaimer")
	claims, err := reclaimer.claimRuns(ctx)
	if err != nil {
		t.Fatalf("claimRuns: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != run.ID || claims[0].LeaseEpoch != 8 {
		t.Fatalf("claims=%+v, want one reclaim at epoch 8", claims)
	}
	if err := reclaimer.resetStaleRunningItems(ctx, run.ID); err != nil {
		t.Fatalf("reset stale items: %v", err)
	}
	var pending int
	if err := db.GetContext(ctx, &pending, `
		SELECT COUNT(*) FROM eval_run_items
		WHERE eval_run_id = $1 AND status = 'pending'
	`, run.ID); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 2 {
		t.Fatalf("pending after stale reset=%d, want 2", pending)
	}

	processClaim(ctx, t, reclaimer, claims[0])
	assertRunTerminal(ctx, t, db, run.ID, "completed")
}

func TestLeaseFencedZombieResultWriteRejectedAfterReclaim(t *testing.T) {
	h := newLeaseHarness(t)
	db := h.openDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db, "pending", 1)
	zombie := newLeaseTestRunner(db, "worker-zombie")
	claims, err := zombie.claimRuns(ctx)
	if err != nil {
		t.Fatalf("zombie claim: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("zombie claims=%+v, want one", claims)
	}
	if err := zombie.resetStaleRunningItems(ctx, run.ID); err != nil {
		t.Fatalf("zombie reset: %v", err)
	}
	claimedRun, err := zombie.getClaimedRun(ctx, run.ID, claims[0].LeaseEpoch)
	if err != nil {
		t.Fatalf("get claimed run: %v", err)
	}
	if err := zombie.ensureRunItems(ctx, claimedRun); err != nil {
		t.Fatalf("ensureRunItems: %v", err)
	}
	items, err := zombie.listPendingItems(ctx, claimedRun)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_run_items
		SET status = 'running', updated_at = NOW()
		WHERE id = $1
	`, items[0].ID); err != nil {
		t.Fatalf("mark item running: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET lease_expires_at = NOW() - INTERVAL '1 second'
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("expire zombie lease: %v", err)
	}

	reclaimer := newLeaseTestRunner(db, "worker-new-owner")
	reclaims, err := reclaimer.claimRuns(ctx)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(reclaims) != 1 || reclaims[0].LeaseEpoch != claims[0].LeaseEpoch+1 {
		t.Fatalf("reclaims=%+v, want next epoch after %+v", reclaims, claims[0])
	}
	if err := reclaimer.resetStaleRunningItems(ctx, run.ID); err != nil {
		t.Fatalf("reset stale: %v", err)
	}

	err = zombie.writeItemResult(ctx, claimedRun, items[0].ID, claims[0].LeaseEpoch, evalItemResult{
		status:     "completed",
		outputJSON: []byte(`{"zombie":true}`),
		tokenJSON:  []byte(`{}`),
		scoresJSON: []byte(`{}`),
	})
	if !errors.Is(err, errEvalRunLeaseLost) {
		t.Fatalf("zombie write error=%v, want errEvalRunLeaseLost", err)
	}

	processClaim(ctx, t, reclaimer, reclaims[0])
	assertRunTerminal(ctx, t, db, run.ID, "completed")

	var zombieOutput *string
	if err := db.QueryRowContext(ctx, `
		SELECT output->>'zombie'
		FROM eval_run_items
		WHERE id = $1
	`, items[0].ID).Scan(&zombieOutput); err != nil {
		t.Fatalf("read item output: %v", err)
	}
	if zombieOutput != nil {
		t.Fatalf("zombie output persisted: %s", *zombieOutput)
	}
}

func TestLeaseCancelThenRetryClearsLeaseAndBumpsEpochForImmediateClaim(t *testing.T) {
	h := newLeaseHarness(t)
	db := h.openDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run := h.insertRun(ctx, t, db, "running", 1)
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET lease_owner = 'old-worker',
			lease_expires_at = NOW() + INTERVAL '1 hour',
			lease_epoch = 3
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("seed future lease: %v", err)
	}

	r := newLeaseTestRunner(db, "worker-retry")
	claims, err := r.claimRuns(ctx)
	if err != nil {
		t.Fatalf("claim with live old lease: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("claims with unexpired old lease=%+v, want none", claims)
	}

	// Mirrors the cancel projection fence followed by RetryEvalRun's fence.
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET status = 'cancelled',
			lease_owner = NULL,
			lease_expires_at = NULL,
			lease_epoch = lease_epoch + 1,
			updated_at = NOW()
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("cancel fence: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_run_items
		SET status = 'pending', updated_at = NOW()
		WHERE eval_run_id = $1
	`, run.ID); err != nil {
		t.Fatalf("retry items reset: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE eval_runs
		SET status = 'pending',
			started_at = NULL,
			completed_at = NULL,
			lease_owner = NULL,
			lease_expires_at = NULL,
			lease_epoch = lease_epoch + 1,
			updated_at = NOW()
		WHERE id = $1
	`, run.ID); err != nil {
		t.Fatalf("retry fence: %v", err)
	}

	claims, err = r.claimRuns(ctx)
	if err != nil {
		t.Fatalf("claim after cancel+retry: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != run.ID || claims[0].LeaseEpoch != 6 {
		t.Fatalf("claims=%+v, want immediate claim at epoch 6", claims)
	}
}

type leaseHarness struct {
	dsn    string
	schema string
}

func newLeaseHarness(t *testing.T) *leaseHarness {
	t.Helper()

	dsn := os.Getenv("EVAL_RUNNER_PG_DSN")
	if dsn == "" {
		t.Skip("set EVAL_RUNNER_PG_DSN to run eval runner lease Postgres tests")
	}

	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	schema := "lease_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	h := &leaseHarness{
		dsn:    dsnWithSearchPath(dsn, schema),
		schema: schema,
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		_ = admin.Close()
	})

	db := h.openDB(t)
	defer db.Close()
	setupLeaseSchema(context.Background(), t, db)
	return h
}

func (h *leaseHarness) openDB(t *testing.T) *sqlx.DB {
	t.Helper()

	db, err := sqlx.Connect("pgx", h.dsn)
	if err != nil {
		t.Fatalf("connect schema db: %v", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	var searchPath string
	if err := db.Get(&searchPath, `SHOW search_path`); err != nil {
		db.Close()
		t.Fatalf("show search_path: %v", err)
	}
	if !strings.Contains(searchPath, h.schema) {
		db.Close()
		t.Fatalf("search_path=%q does not contain schema %q", searchPath, h.schema)
	}
	return db
}

func dsnWithSearchPath(dsn, schema string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		q.Set("options", "-c search_path="+schema+",public")
		u.RawQuery = q.Encode()
		return u.String()
	}
	if strings.TrimSpace(dsn) == "" {
		return dsn
	}
	return strings.TrimSpace(dsn) + " options='-c search_path=" + schema + ",public'"
}

func setupLeaseSchema(ctx context.Context, t *testing.T, db *sqlx.DB) {
	t.Helper()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE datasets (
			id VARCHAR(255) PRIMARY KEY,
			tenant_id VARCHAR(255) NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT DEFAULT '',
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			archived_at TIMESTAMPTZ
		);

		CREATE TABLE dataset_items (
			id VARCHAR(255) PRIMARY KEY,
			dataset_id VARCHAR(255) NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
			tenant_id VARCHAR(255) NOT NULL,
			input JSONB NOT NULL,
			expected_output JSONB,
			metadata JSONB DEFAULT '{}',
			source_trace_id VARCHAR(255) DEFAULT '',
			source_observation_id VARCHAR(255) DEFAULT '',
			status VARCHAR(50) NOT NULL DEFAULT 'active',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE dataset_versions (
			id TEXT PRIMARY KEY,
			dataset_id TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			version_number INT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			item_count INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_by TEXT NOT NULL DEFAULT '',
			UNIQUE(dataset_id, version_number)
		);

		CREATE TABLE dataset_version_items (
			id TEXT PRIMARY KEY,
			dataset_version_id TEXT NOT NULL REFERENCES dataset_versions(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			source_dataset_item_id TEXT,
			input JSONB NOT NULL,
			expected_output JSONB,
			metadata JSONB DEFAULT '{}',
			source_trace_id TEXT DEFAULT '',
			source_observation_id TEXT DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE eval_runs (
			id VARCHAR(255) PRIMARY KEY,
			tenant_id VARCHAR(255) NOT NULL,
			dataset_id VARCHAR(255) NOT NULL REFERENCES datasets(id),
			dataset_version_id TEXT,
			name VARCHAR(512) NOT NULL,
			description TEXT DEFAULT '',
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			eval_target_type VARCHAR(50) NOT NULL DEFAULT '',
			eval_target_id VARCHAR(255) DEFAULT '',
			eval_config JSONB DEFAULT '{}',
			scorer_config_ids TEXT[] DEFAULT '{}',
			total_items INT NOT NULL DEFAULT 0,
			completed_items INT NOT NULL DEFAULT 0,
			failed_items INT NOT NULL DEFAULT 0,
			score_summary JSONB DEFAULT '{}',
			started_at TIMESTAMPTZ,
			completed_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			lease_owner TEXT,
			lease_expires_at TIMESTAMPTZ,
			lease_epoch BIGINT NOT NULL DEFAULT 0
		);

		CREATE TABLE eval_run_items (
			id VARCHAR(255) PRIMARY KEY,
			eval_run_id VARCHAR(255) NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
			dataset_item_id VARCHAR(255) NOT NULL REFERENCES dataset_items(id),
			tenant_id VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			output JSONB,
			trace_id VARCHAR(255) DEFAULT '',
			latency_ms BIGINT DEFAULT 0,
			cost DOUBLE PRECISION DEFAULT 0,
			token_usage JSONB DEFAULT '{}',
			error TEXT DEFAULT '',
			scores JSONB DEFAULT '{}',
			input_canonical JSONB,
			input_hash TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX idx_eval_runs_status_created ON eval_runs(status, created_at);
		CREATE UNIQUE INDEX uq_eval_run_items_run_dataset_item
			ON eval_run_items(eval_run_id, dataset_item_id);
	`)
	if err != nil {
		t.Fatalf("setup schema: %v", err)
	}
}

func (h *leaseHarness) insertRun(ctx context.Context, t *testing.T, db *sqlx.DB, status string, itemCount int) evalRunRow {
	t.Helper()

	tenantID := "tenant-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	datasetID := "dataset-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runID := "run-" + strings.ReplaceAll(uuid.NewString(), "-", "")

	if _, err := db.ExecContext(ctx, `
		INSERT INTO datasets (id, tenant_id, name)
		VALUES ($1, $2, $3)
	`, datasetID, tenantID, "dataset "+datasetID); err != nil {
		t.Fatalf("insert dataset: %v", err)
	}
	for i := 0; i < itemCount; i++ {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO dataset_items (
				id, dataset_id, tenant_id, input, expected_output, metadata,
				source_trace_id, source_observation_id, status
			) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, '{}'::jsonb, '', '', 'active')
		`, fmt.Sprintf("item-%s-%d", datasetID, i), datasetID, tenantID, fmt.Sprintf(`{"input":%d}`, i), fmt.Sprintf(`{"expected":%d}`, i)); err != nil {
			t.Fatalf("insert dataset item %d: %v", i, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO eval_runs (
			id, tenant_id, dataset_id, name, status, eval_target_type,
			eval_target_id, eval_config, scorer_config_ids
		) VALUES ($1, $2, $3, $4, $5, '', '', '{}'::jsonb, '{}'::text[])
	`, runID, tenantID, datasetID, "run "+runID, status); err != nil {
		t.Fatalf("insert eval run: %v", err)
	}

	return evalRunRow{
		ID:             runID,
		TenantID:       tenantID,
		DatasetID:      datasetID,
		Status:         status,
		EvalTargetType: "",
		EvalTargetID:   "",
		EvalConfig:     []byte(`{}`),
	}
}

func newLeaseTestRunner(db *sqlx.DB, workerID string) *Runner {
	return &Runner{
		db:           db,
		pollInterval: time.Second,
		leaseTTL:     3 * time.Second,
		claimBatch:   10,
		workerID:     workerID,
	}
}

func processClaim(ctx context.Context, t *testing.T, r *Runner, claim claimedEvalRun) {
	t.Helper()

	if err := r.resetStaleRunningItems(ctx, claim.ID); err != nil {
		t.Fatalf("reset stale items: %v", err)
	}
	run, err := r.getClaimedRun(ctx, claim.ID, claim.LeaseEpoch)
	if err != nil {
		t.Fatalf("get claimed run: %v", err)
	}
	if err := r.processRun(ctx, run, claim.LeaseEpoch); err != nil {
		t.Fatalf("processRun: %v", err)
	}
}

func assertRunTerminal(ctx context.Context, t *testing.T, db *sqlx.DB, runID, want string) {
	t.Helper()

	var status string
	if err := db.GetContext(ctx, &status, `SELECT status FROM eval_runs WHERE id = $1`, runID); err != nil {
		t.Fatalf("get run status: %v", err)
	}
	if status != want {
		t.Fatalf("run status=%s, want %s", status, want)
	}
}
