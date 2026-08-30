// Package lifecycle implements the async sandbox lifecycle reconciler.
//
// Architecture overview lives in docs/design/sandbox-reconciler.md. The
// short version: CreateSandbox writes a `pending` row and returns in
// <50ms; a leader-elected loop in this package picks up due rows, drives
// them through a pure state-machine `step()` function, and writes back.
// Reads are pure SELECTs; the in-memory map in internal/sandbox keeps
// existing fast-path callers working but is no longer the source of
// truth for whether a sandbox is alive.
//
// Package name `lifecycle` (not matching directory `sandbox`) avoids a
// collision with `internal/sandbox`'s own `package sandbox`. Most callers
// only need one or the other so import aliases stay rare.
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	runtimesandbox "github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// Row is the reconciler's view of a sandbox row. Mirrors the columns
// the state machine cares about. Other columns (config, image, name,
// created_at, etc.) are read-only here — they're set at insert time
// and never re-written by the reconciler.
type Row struct {
	ID                string         `db:"id"`
	TenantID          string         `db:"tenant_id"`
	SessionID         string         `db:"session_id"`
	Status            string         `db:"status"`
	LifecycleState    string         `db:"lifecycle_state"`
	DesiredState      string         `db:"desired_state"`
	ReconcileAfter    time.Time      `db:"reconcile_after"`
	ReconcileAttempts int            `db:"reconcile_attempts"`
	ReconcileLockedBy sql.NullString `db:"reconcile_locked_by"`
	ReconcileLockedAt sql.NullTime   `db:"reconcile_locked_at"`
	AgentTarget       sql.NullString `db:"agent_target"`
	ContainerID       sql.NullString `db:"container_id"`
	Error             sql.NullString `db:"error"`
	Backend           string         `db:"backend"`
	Name              string         `db:"name"`
	Image             string         `db:"image"`
	AgentID           sql.NullString `db:"agent_id"`
	Persistent        bool           `db:"persistent"`
	Config            []byte         `db:"config"` // serialized SandboxConfig JSON
	Labels            []byte         `db:"labels"` // arbitrary k/v metadata, serialized as JSON object
	// Auto-lifecycle fields (POR-72): auto-archive and auto-delete thresholds.
	AutoArchiveAfterDays int          `db:"auto_archive_after_days"` // days before sleeping→archived; 0=disabled
	AutoDeleteAfterDays  int          `db:"auto_delete_after_days"`  // days before →terminated; -1=never
	ArchivedAt           sql.NullTime `db:"archived_at"`
	CreatedAt            time.Time    `db:"created_at"`
	// BillingStartedAt is the authoritative start of the current allocated-
	// compute window. It is independent from CreatedAt, which is immutable
	// across sleep/revive cycles.
	BillingStartedAt sql.NullTime `db:"billing_started_at"`
	// BillingEndedAt pins the backend-confirmed end while ledger persistence is
	// pending, so reconciler retries cannot extend the charge.
	BillingEndedAt sql.NullTime   `db:"billing_ended_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
	ShortCode      sql.NullString `db:"short_code"`
	// Workspace data refs threaded through stop/archive/revive so the
	// reconciler-driven lifecycle is data-preserving.
	WorkspaceSnapshotRef sql.NullString `db:"workspace_snapshot_ref"` // local tarball path written at stop
	WorkspaceArchiveRef  sql.NullString `db:"workspace_archive_ref"`  // object-storage marker written at archive
	// Recoverable error state plumbing.
	ErrorReason sql.NullString `db:"error_reason"`
	ErrorAt     sql.NullTime   `db:"error_at"`
	// RecoveryAttempts counts consecutive auto-recovery attempts
	// (RecoveryChecker) that have not yet produced a successful
	// convergence. Distinct from ReconcileAttempts (the per-convergence
	// retry budget, reset to 0 on every successful transition including
	// the error→reviving recovery hop); recovery_attempts survives the
	// error→reviving→error cycle so the recovery loop can be bounded.
	RecoveryAttempts int `db:"recovery_attempts"`
	// Daytona-style minute intervals (NULL auto_stop = plan-tier default).
	AutoStopMinutes    sql.NullInt64 `db:"auto_stop_minutes"`
	AutoArchiveMinutes sql.NullInt64 `db:"auto_archive_minutes"`
	AutoDeleteMinutes  sql.NullInt64 `db:"auto_delete_minutes"`
}

// Repository is the only file in the new system that touches SQL.
// Keep it dumb — typed queries, no business logic. Lifecycle decisions
// belong in step(); orchestration belongs in Reconciler.
type Repository struct {
	db *sqlx.DB
}

// NewRepository binds a Repository to a database handle.
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// ErrNotFound signals that a sandbox row doesn't exist (caller's choice
// whether to treat as 404 or InvalidArgument).
var ErrNotFound = errors.New("sandbox row not found")

// ErrTerminalRow signals that an InsertPending hit a conflict against
// an existing row in a terminal state (failed, terminated). Caller
// should generate a fresh id rather than silently reuse the dead row.
var ErrTerminalRow = errors.New("sandbox row exists in terminal state")

const insertPendingQuery = `
	INSERT INTO sandbox_instances (
		id, session_id, tenant_id, backend, image, status, config,
		lifecycle_state, desired_state, name, created_at, updated_at,
		reconcile_after, reconcile_attempts, persistent, short_code, labels,
		auto_archive_after_days, auto_delete_after_days,
		auto_stop_minutes, auto_archive_minutes, auto_delete_minutes
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, NOW(), NOW(),
		NOW(), 0, $11, $12, $13,
		$14, $15,
		$16, $17, $18
	)
	ON CONFLICT (id) DO NOTHING
	RETURNING id`

// InsertPending writes a new pending row and returns it.
//
// Idempotency:
//   - id collision in a non-terminal state → returns the existing row
//     unchanged (handles FE retry storms / at-least-once delivery).
//   - id collision in a terminal state (failed, terminated) → returns
//     ErrTerminalRow so the caller can generate a fresh id rather
//     than handing back a dead row that will never run again.
func (r *Repository) InsertPending(ctx context.Context, row Row) (Row, error) {
	// Generate a short_code if the caller didn't supply one. Retry on the
	// (vanishingly rare) unique-violation against the existing index.
	if !row.ShortCode.Valid || row.ShortCode.String == "" {
		code, genErr := GenerateShortCode()
		if genErr != nil {
			return Row{}, fmt.Errorf("InsertPending: generate short_code: %w", genErr)
		}
		row.ShortCode = sql.NullString{String: code, Valid: true}
	}

	// Default labels to an empty JSON object if not supplied so the
	// NOT NULL DEFAULT '{}' constraint is satisfied on insert.
	if len(row.Labels) == 0 {
		row.Labels = []byte("{}")
	}

	var insertedID string
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = r.db.QueryRowxContext(ctx, insertPendingQuery,
			row.ID, row.SessionID, row.TenantID, row.Backend, row.Image,
			row.Status, row.Config, row.LifecycleState, row.DesiredState,
			row.Name, row.Persistent, row.ShortCode, row.Labels,
			row.AutoArchiveAfterDays, row.AutoDeleteAfterDays,
			row.AutoStopMinutes, row.AutoArchiveMinutes, row.AutoDeleteMinutes,
		).Scan(&insertedID)
		if !isShortCodeCollision(err) {
			break
		}
		// Collision on short_code: regenerate and retry. Doesn't affect
		// id-based idempotency since id stays the same.
		code, genErr := GenerateShortCode()
		if genErr != nil {
			return Row{}, fmt.Errorf("InsertPending: regenerate short_code: %w", genErr)
		}
		row.ShortCode = sql.NullString{String: code, Valid: true}
	}
	freshlyInserted := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Row{}, fmt.Errorf("InsertPending: insert: %w", err)
	}

	// Either we just inserted, or a row already exists. Read it back.
	existing, err := r.GetByID(ctx, row.ID)
	if err != nil {
		return Row{}, err
	}
	if !freshlyInserted && IsTerminal(existing.LifecycleState) {
		// Caller should generate a fresh id rather than silently
		// resurrect a dead row. The reconciler doesn't claim terminal
		// rows; returning this would deadlock the caller's flow.
		return existing, ErrTerminalRow
	}
	return existing, nil
}

// InsertPendingWithLimit serializes plan-slot reservations per tenant across
// gateway replicas. The advisory lock and pending-row insert share one
// transaction, so two simultaneous requests for the final slot cannot both
// pass the count.
func (r *Repository) InsertPendingWithLimit(
	ctx context.Context,
	row Row,
	limit int,
) (Row, error) {
	if limit == runtimesandbox.UnlimitedSandboxLimit {
		return r.InsertPending(ctx, row)
	}
	if limit <= 0 {
		return Row{}, fmt.Errorf("%w: no sandbox slots are available", runtimesandbox.ErrConcurrentSandboxLimit)
	}
	if !row.ShortCode.Valid || row.ShortCode.String == "" {
		code, err := GenerateShortCode()
		if err != nil {
			return Row{}, fmt.Errorf("InsertPendingWithLimit: generate short_code: %w", err)
		}
		row.ShortCode = sql.NullString{String: code, Valid: true}
	}
	if len(row.Labels) == 0 {
		row.Labels = []byte("{}")
	}

	for attempt := 0; attempt < 5; attempt++ {
		tx, err := r.db.BeginTxx(ctx, nil)
		if err != nil {
			return Row{}, fmt.Errorf("InsertPendingWithLimit: begin: %w", err)
		}
		rollback := func() { _ = tx.Rollback() }

		if _, err := tx.ExecContext(
			ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`,
			"sandbox-concurrency:"+row.TenantID,
		); err != nil {
			rollback()
			return Row{}, fmt.Errorf("InsertPendingWithLimit: lock tenant: %w", err)
		}

		var existing Row
		err = tx.GetContext(ctx, &existing, selectColumns+` WHERE id = $1`, row.ID)
		switch {
		case err == nil:
			if IsTerminal(existing.LifecycleState) {
				rollback()
				return existing, ErrTerminalRow
			}
			if err := tx.Commit(); err != nil {
				return Row{}, fmt.Errorf("InsertPendingWithLimit: commit idempotent read: %w", err)
			}
			return existing, nil
		case !errors.Is(err, sql.ErrNoRows):
			rollback()
			return Row{}, fmt.Errorf("InsertPendingWithLimit: read existing: %w", err)
		}

		var used int
		if err := tx.GetContext(ctx, &used, `
			SELECT COUNT(*)
			FROM sandbox_instances
			WHERE tenant_id = $1
			  AND (
			    lifecycle_state IN (
			      'pending', 'creating', 'provisioning', 'running',
			      'stopping', 'reviving', 'terminating'
			    )
			    OR (billing_started_at IS NOT NULL AND billing_ended_at IS NULL)
			  )`, row.TenantID); err != nil {
			rollback()
			return Row{}, fmt.Errorf("InsertPendingWithLimit: count slots: %w", err)
		}
		if used >= limit {
			rollback()
			return Row{}, fmt.Errorf(
				"%w: %d of %d slots are allocated; stop or sleep a sandbox before starting another",
				runtimesandbox.ErrConcurrentSandboxLimit,
				used,
				limit,
			)
		}

		var insertedID string
		err = tx.QueryRowxContext(ctx, insertPendingQuery,
			row.ID, row.SessionID, row.TenantID, row.Backend, row.Image,
			row.Status, row.Config, row.LifecycleState, row.DesiredState,
			row.Name, row.Persistent, row.ShortCode, row.Labels,
			row.AutoArchiveAfterDays, row.AutoDeleteAfterDays,
			row.AutoStopMinutes, row.AutoArchiveMinutes, row.AutoDeleteMinutes,
		).Scan(&insertedID)
		if isShortCodeCollision(err) {
			rollback()
			code, genErr := GenerateShortCode()
			if genErr != nil {
				return Row{}, fmt.Errorf("InsertPendingWithLimit: regenerate short_code: %w", genErr)
			}
			row.ShortCode = sql.NullString{String: code, Valid: true}
			continue
		}
		if err != nil {
			rollback()
			return Row{}, fmt.Errorf("InsertPendingWithLimit: insert: %w", err)
		}
		if err := tx.GetContext(ctx, &existing, selectColumns+` WHERE id = $1`, insertedID); err != nil {
			rollback()
			return Row{}, fmt.Errorf("InsertPendingWithLimit: read inserted: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Row{}, fmt.Errorf("InsertPendingWithLimit: commit: %w", err)
		}
		return existing, nil
	}
	return Row{}, fmt.Errorf("InsertPendingWithLimit: could not allocate a unique short code")
}

// GetByID returns a row by id, or ErrNotFound.
func (r *Repository) GetByID(ctx context.Context, id string) (Row, error) {
	const q = selectColumns + ` WHERE id = $1`
	var row Row
	if err := r.db.GetContext(ctx, &row, q, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("GetByID: %w", err)
	}
	return row, nil
}

// BackfillShortCodes assigns a short_code to every sandbox_instances row
// that doesn't have one yet. Runs once at gateway startup; cheap when
// already done (single SELECT). Safe to run concurrently from multiple
// pods — the UNIQUE index serializes any racing inserts and the WHERE
// clause skips rows that another pod already filled.
//
// Returns the number of rows successfully backfilled.
func (r *Repository) BackfillShortCodes(ctx context.Context) (int, error) {
	const listQ = `SELECT id FROM sandbox_instances WHERE short_code IS NULL ORDER BY created_at ASC LIMIT 5000`
	var ids []string
	if err := r.db.SelectContext(ctx, &ids, listQ); err != nil {
		return 0, fmt.Errorf("BackfillShortCodes: list: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	const updateQ = `UPDATE sandbox_instances SET short_code = $1 WHERE id = $2 AND short_code IS NULL`
	filled := 0
	for _, id := range ids {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return filled, ctxErr
		}
		// Retry on the rare collision; bound at 5 attempts so a wedged
		// generator can't loop forever.
		for attempt := 0; attempt < 5; attempt++ {
			code, genErr := GenerateShortCode()
			if genErr != nil {
				return filled, fmt.Errorf("BackfillShortCodes: generate: %w", genErr)
			}
			_, err := r.db.ExecContext(ctx, updateQ, code, id)
			if err == nil {
				filled++
				break
			}
			if !isShortCodeCollision(err) {
				return filled, fmt.Errorf("BackfillShortCodes: update %s: %w", id, err)
			}
			// Collision — try again with a new code.
		}
	}
	return filled, nil
}

// GetByShortCode resolves a sandbox by its public short code (the
// bitly-style identifier used as the SSH username and the preview URL
// subdomain on *.evs.run). Returns ErrNotFound if no row matches.
func (r *Repository) GetByShortCode(ctx context.Context, code string) (Row, error) {
	if code == "" {
		return Row{}, ErrNotFound
	}
	const q = selectColumns + ` WHERE short_code = $1`
	var row Row
	if err := r.db.GetContext(ctx, &row, q, code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("GetByShortCode: %w", err)
	}
	return row, nil
}

// isShortCodeCollision returns true when err is a Postgres unique-violation
// against the short_code index, which means the generator picked an
// already-taken value and the caller should regenerate.
func isShortCodeCollision(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	if pqErr.Code != "23505" { // unique_violation
		return false
	}
	return pqErr.Constraint == "sandbox_instances_short_code_unique_idx" ||
		strings.Contains(pqErr.Message, "short_code")
}

// ErrNotLeader is returned by ClaimDue when the leader advisory lock
// can't be acquired this tick. Caller should skip the tick — another
// pod's reconciler is currently running.
var ErrNotLeader = errors.New("not the reconciler leader this tick")

// ErrLeaseLost is returned by the write-back paths when the row's
// lease no longer belongs to this leader (the lease expired and
// another claimer took over). The caller discards its result; the
// current lease owner re-runs the idempotent step.
var ErrLeaseLost = errors.New("reconcile lease lost")

// ClaimDue leases up to `limit` due rows and returns them. Unlike the
// original design (claim tx held open across the whole Step, leader
// lock serialized ALL lifecycle work behind the slowest VM operation),
// the claim now stamps a lease (reconcile_locked_by/at) and COMMITS
// IMMEDIATELY. Claimed rows are processed concurrently by the caller;
// write-back is guarded by lease ownership (see ApplyTransition).
//
// A row whose lease is older than leaseTimeout is reclaimable: the
// previous owner crashed mid-step or is wedged. Steps are idempotent,
// so a re-run after lease expiry converges instead of double-applying.
//
// The advisory xact-lock only serializes the claim itself (held for
// milliseconds), not the work.
//
// Returns:
//
//	(rows, nil)          — leased rows (possibly empty)
//	(nil, ErrNotLeader)  — another pod is claiming right now; skip
//	(nil, err)           — DB error; skip this tick, log
func (r *Repository) ClaimDue(ctx context.Context, leaderKey, leaderID string, limit int, leaseTimeout time.Duration) ([]Row, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("ClaimDue: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, leaderKey); err != nil {
		return nil, fmt.Errorf("ClaimDue: try lock: %w", err)
	}
	if !locked {
		return nil, ErrNotLeader
	}

	claimQ := selectColumns + `
		WHERE reconcile_after <= NOW()
		  AND lifecycle_state = ANY($2::text[])
		  AND (reconcile_locked_at IS NULL
		       OR reconcile_locked_at < NOW() - make_interval(secs => $3))
		ORDER BY reconcile_after ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	var rows []Row
	if err := tx.SelectContext(ctx, &rows, claimQ, limit, pq.Array(claimableStates), leaseTimeout.Seconds()); err != nil {
		return nil, fmt.Errorf("ClaimDue: select: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	const stampQ = `
		UPDATE sandbox_instances
		SET reconcile_locked_by = $1, reconcile_locked_at = NOW()
		WHERE id = ANY($2)`
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	if _, err := tx.ExecContext(ctx, stampQ, leaderID, pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("ClaimDue: stamp: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ClaimDue: commit: %w", err)
	}
	return rows, nil
}

// ReleaseLease clears a lease without applying a transition. Used when
// the reconciler decides not to act on a claimed row (e.g. shutdown
// mid-batch) so the row is immediately reclaimable instead of waiting
// out the lease timeout.
func (r *Repository) ReleaseLease(ctx context.Context, id, leaderID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET reconcile_locked_by = NULL, reconcile_locked_at = NULL
		WHERE id = $1 AND reconcile_locked_by = $2`, id, leaderID)
	return err
}

// ApplyTransition writes the new state in its own transaction, guarded
// by lease ownership: the UPDATE only applies while reconcile_locked_by
// still equals leaderID. A lost lease (expired and reclaimed by another
// pod) returns ErrLeaseLost and the result is discarded; the current
// owner re-runs the idempotent step.
//
// Resets reconcile_attempts, clears the lease and any error breadcrumbs,
// and stamps the lifecycle timestamps:
//   - stopped_at set when entering sleeping, cleared when running
//   - billing_started_at set only when ENTERING running, preserved for a
//     running→running maintenance write, and cleared after compute is gone
//   - billing_ended_at cleared when a new window starts or a closed state lands;
//     while a close is retrying it retains the first backend-confirmed end
//   - last_used_at reset when entering running so the idle clock
//     restarts after a revive
//   - archived_at set when entering archived
//
// When the row carries a non-empty agent_id, the matching
// agent_definitions.lifecycle_status is derived in the same tx
// (issue-9 invariant: agent lifecycle is a view of sandbox lifecycle).
func (r *Repository) ApplyTransition(ctx context.Context, leaderID string, next Row) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ApplyTransition: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		UPDATE sandbox_instances
		SET lifecycle_state     = $2::text,
		    status              = $3,
		    desired_state       = $4,
		    container_id        = $5,
		    agent_target        = $6,
		    error               = $7,
		    reconcile_after     = $8,
		    workspace_snapshot_ref = $9,
		    workspace_archive_ref  = $10,
		    reconcile_attempts  = 0,
		    -- Forward progress clears the auto-recovery counter: any
		    -- successful transition (incl. error→reviving→running) means
		    -- the row is healthy again, so a future death starts clean.
		    recovery_attempts   = 0,
		    reconcile_locked_by = NULL,
		    reconcile_locked_at = NULL,
		    error_reason        = NULL,
		    error_at            = NULL,
		    stopped_at          = CASE
		        WHEN $2::text = 'sleeping' THEN NOW()
		        WHEN $2::text = 'running'  THEN NULL
		        ELSE stopped_at END,
			    archived_at         = CASE
			        WHEN $2::text = 'archived' THEN NOW()
			        WHEN $2::text = 'running'  THEN NULL
			        ELSE archived_at END,
			    billing_started_at = CASE
			        WHEN $2::text = 'running' AND lifecycle_state IS DISTINCT FROM 'running'
			            THEN COALESCE($12, NOW())
			        WHEN $2::text IN ('sleeping', 'archived', 'terminated') THEN NULL
			        ELSE billing_started_at END,
			    billing_ended_at = CASE
			        WHEN $2::text = 'running' AND lifecycle_state IS DISTINCT FROM 'running' THEN NULL
			        WHEN $2::text IN ('sleeping', 'archived', 'terminated') THEN NULL
			        ELSE billing_ended_at END,
			    last_used_at        = CASE WHEN $2::text = 'running' THEN NOW() ELSE last_used_at END,
		    updated_at          = NOW()
		WHERE id = $1 AND ($11 = '' OR reconcile_locked_by = $11)`
	res, err := tx.ExecContext(ctx, q,
		next.ID, next.LifecycleState, next.Status, next.DesiredState,
		next.ContainerID, next.AgentTarget, next.Error,
		next.ReconcileAfter,
		next.WorkspaceSnapshotRef, next.WorkspaceArchiveRef,
		leaderID, next.BillingStartedAt,
	)
	if err != nil {
		return fmt.Errorf("ApplyTransition: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseLost
	}

	// Issue-9: derive agent lifecycle from sandbox lifecycle. Best-
	// effort — failure here doesn't roll back the sandbox transition
	// (the agent row may not exist for non-trooper sandboxes, and we'd
	// rather have a slightly stale agent_definitions row than a wedged
	// reconciler). Logged but not surfaced.
	if next.AgentID.Valid && next.AgentID.String != "" {
		if err := r.applyAgentLifecycleInTx(ctx, tx, next.AgentID.String, next.LifecycleState); err != nil {
			// We could log here; keep the repo dependency-free and let
			// callers wrap with their own logger if needed.
			_ = err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ApplyTransition: commit: %w", err)
	}
	return nil
}

// applyAgentLifecycleInTx writes the derived agent lifecycle_status
// inside the caller's tx. The mapping (sandbox state → agent state)
// is in AgentLifecycleFor; the SQL guard below ensures we never
// demote an in-flight turn back to idle (turn-start/turn-end handlers
// own the running ↔ idle transitions).
func (r *Repository) applyAgentLifecycleInTx(
	ctx context.Context,
	tx *sqlx.Tx,
	agentID, sandboxState string,
) error {
	desired := AgentLifecycleFor(sandboxState)
	if desired == "" {
		return nil
	}
	// The 'AND NOT (running AND idle)' clause is the structural
	// guarantee that turn handlers, not the reconciler, own the
	// running ↔ idle transitions. Without it, a reconciler tick on a
	// transition that maps to 'idle' (e.g., creating→running) would
	// clobber an active turn that started in the brief window
	// between the sandbox flipping running and the reconciler
	// writing the agent row.
	const q = `
		UPDATE agent_definitions
		SET lifecycle_status = $2,
		    updated_at       = NOW()
		WHERE id = $1
		  AND lifecycle_status IS DISTINCT FROM $2
		  AND NOT (lifecycle_status = 'running' AND $2 = 'idle')`
	_, err := tx.ExecContext(ctx, q, agentID, desired)
	return err
}

// RecordFailure increments attempts and sets reconcile_after with
// backoff. After maxAttempts the row enters the recoverable 'error'
// state (NOT the legacy terminal 'failed'): error_reason/error_at are
// stamped, desired_state is PRESERVED so Recover() can re-converge,
// and no sweep auto-terminates it.
//
// Lease-guarded like ApplyTransition: a lost lease returns ErrLeaseLost.
//
// Backoff: min(15s, 2^attempts s).
func (r *Repository) RecordFailure(
	ctx context.Context,
	leaderID string,
	row Row,
	cause error,
	maxAttempts int,
) error {
	nextAttempt := row.ReconcileAttempts + 1
	terminal := nextAttempt >= maxAttempts

	const q = `
		UPDATE sandbox_instances
		SET reconcile_attempts = $2,
		    reconcile_after    = $3,
		    lifecycle_state    = $4,
		    status             = $5,
		    error              = $6,
		    error_reason       = CASE WHEN $7 THEN $6 ELSE error_reason END,
		    error_at           = CASE WHEN $7 THEN NOW() ELSE error_at END,
		    reconcile_locked_by = NULL,
		    reconcile_locked_at = NULL,
		    updated_at         = NOW()
		WHERE id = $1 AND ($8 = '' OR reconcile_locked_by = $8)`

	state := row.LifecycleState
	status := row.Status
	if terminal {
		state = StateError
		status = StateError
	}

	res, err := r.db.ExecContext(ctx, q,
		row.ID,
		nextAttempt,
		nextBackoffAt(nextAttempt),
		state,
		status,
		cause.Error(),
		terminal,
		leaderID,
	)
	if err != nil {
		return fmt.Errorf("RecordFailure: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Recover re-enters convergence for a row in the error (or legacy
// failed) state, toward its preserved desired_state. The reconciler
// picks the row up on its next tick. Returns ErrNotFound when the row
// doesn't exist or isn't in a recoverable state.
func (r *Repository) Recover(ctx context.Context, id string) error {
	const q = `
		UPDATE sandbox_instances
		SET lifecycle_state = CASE desired_state
		        WHEN 'running'  THEN 'reviving'
		        WHEN 'sleeping' THEN 'stopping'
		        WHEN 'archived' THEN 'archiving'
		        ELSE 'terminating' END,
		    status = CASE desired_state
		        WHEN 'running'  THEN 'reviving'
		        WHEN 'sleeping' THEN 'stopping'
		        WHEN 'archived' THEN 'archiving'
		        ELSE 'terminating' END,
		    reconcile_attempts  = 0,
		    reconcile_after     = NOW(),
		    reconcile_locked_by = NULL,
		    reconcile_locked_at = NULL,
		    error               = NULL,
		    error_reason        = NULL,
		    error_at            = NULL,
		    updated_at          = NOW()
		WHERE id = $1 AND lifecycle_state IN ('error', 'failed')`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("Recover: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AutoRecover re-enters convergence for an error/failed row toward its
// preserved desired_state, exactly like Recover, but INCREMENTS
// recovery_attempts instead of resetting it. The RecoveryChecker calls
// this on each automatic retry so the recovery loop is bounded: a row
// that keeps dying (error→reviving→error) accumulates recovery_attempts
// until the checker's cap stops retrying, while a successful convergence
// resets the counter via ApplyTransition.
//
// reconcile_attempts IS reset to 0 so the convergence sub-attempt (the
// revive/stop/terminate) gets its full per-transition retry budget.
// Returns ErrNotFound when the row isn't in a recoverable state.
func (r *Repository) AutoRecover(ctx context.Context, id string) error {
	const q = `
		UPDATE sandbox_instances
		SET lifecycle_state = CASE desired_state
		        WHEN 'running'  THEN 'reviving'
		        WHEN 'sleeping' THEN 'stopping'
		        WHEN 'archived' THEN 'archiving'
		        ELSE 'terminating' END,
		    status = CASE desired_state
		        WHEN 'running'  THEN 'reviving'
		        WHEN 'sleeping' THEN 'stopping'
		        WHEN 'archived' THEN 'archiving'
		        ELSE 'terminating' END,
		    recovery_attempts   = recovery_attempts + 1,
		    reconcile_attempts  = 0,
		    reconcile_after     = NOW(),
		    reconcile_locked_by = NULL,
		    reconcile_locked_at = NULL,
		    error               = NULL,
		    error_reason        = NULL,
		    error_at            = NULL,
		    updated_at          = NOW()
		WHERE id = $1 AND lifecycle_state IN ('error', 'failed')`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("AutoRecover: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAutoIntervals changes the Daytona-style auto-lifecycle
// intervals on a live sandbox. Nil pointers leave the corresponding
// column untouched. Returns ErrNotFound for unknown ids.
func (r *Repository) UpdateAutoIntervals(ctx context.Context, id string, stop, archive, del *int32) error {
	const q = `
		UPDATE sandbox_instances
		SET auto_stop_minutes    = COALESCE($2, auto_stop_minutes),
		    auto_archive_minutes = COALESCE($3, auto_archive_minutes),
		    auto_delete_minutes  = COALESCE($4, auto_delete_minutes),
		    updated_at = NOW()
		WHERE id = $1`
	res, err := r.db.ExecContext(ctx, q, id, stop, archive, del)
	if err != nil {
		return fmt.Errorf("UpdateAutoIntervals: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkError flips a row into the recoverable error state with a reason.
// Used by the health sweeper when the backend reports the VM gone for
// a row the DB believes is running. desired_state is preserved.
func (r *Repository) MarkError(ctx context.Context, id, reason string) error {
	const q = `
		UPDATE sandbox_instances
		SET lifecycle_state = 'error',
		    status          = 'error',
		    error           = $2,
		    error_reason    = $2,
		    error_at        = NOW(),
		    reconcile_locked_by = NULL,
		    reconcile_locked_at = NULL,
		    updated_at      = NOW()
		WHERE id = $1 AND lifecycle_state = 'running'`
	res, err := r.db.ExecContext(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("MarkError: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDesiredState is the write API used by Stop / Revive / Terminate
// RPCs. Updates desired_state AND atomically advances lifecycle_state
// from a quiescent state (running, sleeping) to the matching
// transitional state.
//
// Why the atomic lifecycle_state advance: the reconciler's partial
// index covers only pending/creating/stopping/reviving/archiving/terminating.
// Running and sleeping rows are NEVER claimed by the reconciler.
// Without bumping lifecycle_state here, calling Stop on a running row
// would set desired_state='sleeping' but the row would stay in
// lifecycle_state='running' forever and the user's action would
// silently never apply.
//
// Mappings (current → next when desired diverges):
//
//	desired=sleeping   + current=running  → lifecycle=stopping
//	desired=running    + current=sleeping → lifecycle=reviving
//	desired=archived   + current=sleeping → lifecycle=archiving
//	desired=terminated + current in (running, sleeping, archived) → lifecycle=terminating
//
// All other (current, desired) combinations leave lifecycle_state
// unchanged: the reconciler is already converging it, OR the row is
// already in the desired terminal state, OR the row is in a state
// that user actions don't transition (failed, terminated).
//
// Idempotent: calling Stop twice on the same row is fine — the
// second call's CASE doesn't match (current is 'stopping', not
// 'running') so lifecycle_state stays put.
const setDesiredStateQuery = `
		UPDATE sandbox_instances
		SET desired_state = $2::text,
		    lifecycle_state = CASE
		        WHEN $2::text = 'sleeping'   AND lifecycle_state = 'running'           THEN 'stopping'
		        WHEN $2::text = 'archived'   AND lifecycle_state IN ('sleeping','stopped') THEN 'archiving'
		        WHEN $2::text = 'running'    AND lifecycle_state IN ('sleeping','stopped','archived') THEN 'reviving'
		        WHEN $2::text = 'terminated' AND lifecycle_state IN ('running', 'sleeping', 'stopped', 'archived', 'error', 'failed') THEN 'terminating'
		        ELSE lifecycle_state
		    END,
		    status = CASE
		        WHEN $2::text = 'sleeping'   AND lifecycle_state = 'running'           THEN 'stopping'
		        WHEN $2::text = 'archived'   AND lifecycle_state IN ('sleeping','stopped') THEN 'archiving'
		        WHEN $2::text = 'running'    AND lifecycle_state IN ('sleeping','stopped','archived') THEN 'reviving'
		        WHEN $2::text = 'terminated' AND lifecycle_state IN ('running', 'sleeping', 'stopped', 'archived', 'error', 'failed') THEN 'terminating'
		        ELSE status
		    END,
		    reconcile_after = NOW(),
		    updated_at = NOW()
		WHERE id = $1`

func (r *Repository) SetDesiredState(ctx context.Context, id, desired string) error {
	res, err := r.db.ExecContext(ctx, setDesiredStateQuery, id, desired)
	if err != nil {
		return fmt.Errorf("SetDesiredState: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDesiredStateWithLimit applies a running/revive request under the same
// cross-replica tenant lock used by creates. Non-running desired states do not
// consume a new slot and use the ordinary path.
func (r *Repository) SetDesiredStateWithLimit(
	ctx context.Context,
	id, desired string,
	limit int,
) error {
	if desired != DesireRunning || limit == runtimesandbox.UnlimitedSandboxLimit {
		return r.SetDesiredState(ctx, id, desired)
	}
	if limit <= 0 {
		return fmt.Errorf("%w: no sandbox slots are available", runtimesandbox.ErrConcurrentSandboxLimit)
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("SetDesiredStateWithLimit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var tenantID string
	if err := tx.GetContext(ctx, &tenantID, `SELECT tenant_id FROM sandbox_instances WHERE id = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("SetDesiredStateWithLimit: resolve tenant: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`,
		"sandbox-concurrency:"+tenantID,
	); err != nil {
		return fmt.Errorf("SetDesiredStateWithLimit: lock tenant: %w", err)
	}

	var lifecycleState string
	if err := tx.GetContext(
		ctx,
		&lifecycleState,
		`SELECT lifecycle_state FROM sandbox_instances WHERE id = $1 FOR UPDATE`,
		id,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("SetDesiredStateWithLimit: lock sandbox: %w", err)
	}

	// An already-active or already-converging sandbox owns its existing slot;
	// idempotent retries must not be rejected merely because the tenant is at
	// capacity.
	needsNewSlot := lifecycleState == StateSleeping ||
		lifecycleState == "stopped" ||
		lifecycleState == StateArchived
	if needsNewSlot {
		var used int
		if err := tx.GetContext(ctx, &used, `
			SELECT COUNT(*)
			FROM sandbox_instances
			WHERE tenant_id = $1
			  AND id <> $2
			  AND (
			    lifecycle_state IN (
			      'pending', 'creating', 'provisioning', 'running',
			      'stopping', 'reviving', 'terminating'
			    )
			    OR (billing_started_at IS NOT NULL AND billing_ended_at IS NULL)
			  )`, tenantID, id); err != nil {
			return fmt.Errorf("SetDesiredStateWithLimit: count slots: %w", err)
		}
		if used >= limit {
			return fmt.Errorf(
				"%w: %d of %d slots are allocated; stop or sleep a sandbox before starting another",
				runtimesandbox.ErrConcurrentSandboxLimit,
				used,
				limit,
			)
		}
	}

	res, err := tx.ExecContext(ctx, setDesiredStateQuery, id, desired)
	if err != nil {
		return fmt.Errorf("SetDesiredStateWithLimit: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SetDesiredStateWithLimit: commit: %w", err)
	}
	return nil
}

// ListByTenant is the read API used by ListSandboxInstances. Pure SELECT,
// no merge with anything else.
func (r *Repository) ListByTenant(
	ctx context.Context,
	tenantID string,
	limit, offset int,
) ([]Row, int, error) {
	return r.ListByTenantFiltered(ctx, tenantID, limit, offset, nil)
}

// ListByTenantFiltered extends ListByTenant with optional label containment
// filtering. labelFilter is a map of required key=value pairs; only sandboxes
// that have ALL supplied pairs in their labels column are returned.
// A nil or empty labelFilter behaves identically to ListByTenant.
func (r *Repository) ListByTenantFiltered(
	ctx context.Context,
	tenantID string,
	limit, offset int,
	labelFilter map[string]string,
) ([]Row, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var (
		rows  []Row
		total int
	)

	if len(labelFilter) == 0 {
		const q = selectColumns + `
			WHERE tenant_id = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3`
		if err := r.db.SelectContext(ctx, &rows, q, tenantID, limit, offset); err != nil {
			return nil, 0, fmt.Errorf("ListByTenantFiltered: %w", err)
		}
		const countQ = `SELECT COUNT(*) FROM sandbox_instances WHERE tenant_id = $1`
		if err := r.db.GetContext(ctx, &total, countQ, tenantID); err != nil {
			return rows, len(rows), fmt.Errorf("ListByTenantFiltered: count: %w", err)
		}
		return rows, total, nil
	}

	// Marshal the label filter to JSON for the @> containment operator.
	// labels @> '{"key":"val"}'::jsonb means "labels contains all these pairs."
	filterJSON, err := json.Marshal(labelFilter)
	if err != nil {
		return nil, 0, fmt.Errorf("ListByTenantFiltered: marshal label filter: %w", err)
	}
	const q = selectColumns + `
		WHERE tenant_id = $1 AND labels @> $4::jsonb
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	if err := r.db.SelectContext(ctx, &rows, q, tenantID, limit, offset, filterJSON); err != nil {
		return nil, 0, fmt.Errorf("ListByTenantFiltered: %w", err)
	}
	const countQ = `SELECT COUNT(*) FROM sandbox_instances WHERE tenant_id = $1 AND labels @> $2::jsonb`
	if err := r.db.GetContext(ctx, &total, countQ, tenantID, filterJSON); err != nil {
		return rows, len(rows), fmt.Errorf("ListByTenantFiltered: count: %w", err)
	}
	return rows, total, nil
}

// nextBackoffAt computes the next reconcile_after for an attempt count.
// min(15s, 2^attempts s) — total budget after maxAttempts=6 is ~45s
// before terminal failed. Tight enough that users see failures fast,
// wide enough that transient blips don't burn the row.
func nextBackoffAt(attempts int) time.Time {
	secs := 1
	for i := 1; i < attempts; i++ {
		secs *= 2
		if secs >= 15 {
			secs = 15
			break
		}
	}
	return time.Now().Add(time.Duration(secs) * time.Second)
}

// (pq.Array is used directly in ClaimDue's stampQ now — the local
// shim was reinventing what lib/pq already does correctly.)

// selectColumns is the canonical projection for Row scans. Kept as a
// const so every read query stays in lockstep with the struct.
const selectColumns = `
	SELECT id, tenant_id, session_id, status, lifecycle_state,
	       COALESCE(desired_state, 'running') AS desired_state,
	       COALESCE(reconcile_after, NOW())   AS reconcile_after,
	       COALESCE(reconcile_attempts, 0)    AS reconcile_attempts,
	       reconcile_locked_by, reconcile_locked_at, agent_target,
	       container_id, error, COALESCE(backend, '') AS backend,
	       COALESCE(name, '') AS name, COALESCE(image, '') AS image,
	       agent_id, COALESCE(persistent, false) AS persistent,
	       COALESCE(config, '{}'::jsonb) AS config,
	       COALESCE(labels, '{}'::jsonb) AS labels,
	       COALESCE(auto_archive_after_days, 7)  AS auto_archive_after_days,
	       COALESCE(auto_delete_after_days,  -1) AS auto_delete_after_days,
	       archived_at,
		       created_at, billing_started_at, billing_ended_at,
		       COALESCE(updated_at, created_at) AS updated_at,
	       short_code,
	       workspace_snapshot_ref, workspace_archive_ref,
	       error_reason, error_at,
	       COALESCE(recovery_attempts, 0) AS recovery_attempts,
	       auto_stop_minutes, auto_archive_minutes, auto_delete_minutes
	FROM sandbox_instances`
