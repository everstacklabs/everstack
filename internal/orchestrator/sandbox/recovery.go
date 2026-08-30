package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// RecoveryChecker re-converges sandboxes that died while the user still
// wanted them in some state. The HealthSweeper flips a dead running VM
// to lifecycle_state='error' (error_reason='vm_not_found') and PRESERVES
// desired_state, but nothing used to act on that: the reconciler never
// claims 'error' rows (it's excluded from claimableStates and the
// partial index), and step() parks error for 365 days awaiting a manual
// Recover(). Rows were observed stuck in error/desired=running for 16+
// days in production — the dominant "my sandbox died and never came
// back" symptom.
//
// This checker is the automatic Recover(): on a fixed cadence it scans
// error rows whose backoff window has elapsed and re-enters the
// convergence state implied by desired_state (running→reviving,
// sleeping→stopping, …). The reconciler then drives the revive/stop on
// its next tick. A revive that itself fails lands the row back in error
// (RecordFailure after the per-convergence cap), at which point the
// backoff grows and recovery_attempts climbs until the cap is hit.
//
// Decoupled from the reconciler by design, mirroring IdleChecker: policy
// ("is this error row worth retrying, and how soon?") lives here; the
// state-machine transitions live in step.go / the repo. It is
// leader-locked on its own advisory key so it can run on a different pod
// if the lifecycle components ever scale out.
//
// Bounding the loop: recovery_attempts is a DISTINCT counter from
// reconcile_attempts. reconcile_attempts is reset to 0 on every
// successful transition (including the error→reviving recovery hop), so
// it cannot survive the error→reviving→error cycle and cannot bound the
// loop. recovery_attempts survives that cycle (AutoRecover increments it;
// only a successful convergence to a stable state resets it via
// ApplyTransition). Once recovery_attempts reaches MaxAttempts the
// checker stops retrying and leaves the row in error for a human to
// Recover() or terminate — a genuinely unrecoverable sandbox stops
// thrashing instead of looping forever.
type RecoveryChecker struct {
	db        *sqlx.DB
	repo      *Repository
	leaderKey string

	// Tunables.
	Interval    time.Duration // how often to scan
	MaxAttempts int           // give up auto-recovery after this many tries
	BaseBackoff time.Duration // first retry delay; doubles each attempt
	MaxBackoff  time.Duration // ceiling on the per-attempt backoff
	BatchLimit  int           // rows reconsidered per tick
}

// NewRecoveryChecker returns a checker with production defaults. A dead
// VM the user wants running is recovered ~15s after it's confirmed dead,
// backing off to a few minutes if recovery keeps failing, and giving up
// after 5 attempts.
func NewRecoveryChecker(db *sqlx.DB, repo *Repository) *RecoveryChecker {
	return &RecoveryChecker{
		db:          db,
		repo:        repo,
		leaderKey:   "sandbox-recovery-checker",
		Interval:    20 * time.Second,
		MaxAttempts: 5,
		BaseBackoff: 15 * time.Second,
		MaxBackoff:  5 * time.Minute,
		BatchLimit:  50,
	}
}

// Run blocks until ctx is cancelled.
func (rc *RecoveryChecker) Run(ctx context.Context) error {
	logger.WithFields(
		"interval_s", rc.Interval.Seconds(),
		"max_attempts", rc.MaxAttempts,
		"base_backoff_s", rc.BaseBackoff.Seconds(),
		"max_backoff_s", rc.MaxBackoff.Seconds(),
	).Info("sandbox_recovery_checker: starting")

	t := time.NewTicker(rc.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_recovery_checker: stopping")
			return ctx.Err()
		case <-t.C:
			rc.tick(ctx)
		}
	}
}

func (rc *RecoveryChecker) tick(ctx context.Context) {
	tx, err := rc.db.BeginTxx(ctx, nil)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_recovery_checker: begin failed")
		return
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, rc.leaderKey); err != nil || !locked {
		return
	}

	// Eligible: error rows under the attempt cap whose per-attempt
	// backoff has elapsed since they (re-)entered error. error_at is
	// stamped by MarkError / RecordFailure; legacy rows with a NULL
	// error_at are treated as immediately eligible. Backoff doubles per
	// recovery_attempt, capped at MaxBackoff.
	const q = `
		SELECT id, desired_state, recovery_attempts
		FROM sandbox_instances
		WHERE lifecycle_state = 'error'
		  AND COALESCE(recovery_attempts, 0) < $1
		  AND (error_at IS NULL
		       OR error_at < NOW() - make_interval(
		            secs => LEAST($2::float8,
		                          $3::float8 * power(2, COALESCE(recovery_attempts, 0)))))
		ORDER BY error_at ASC NULLS FIRST
		LIMIT $4`

	type errRow struct {
		ID               string `db:"id"`
		DesiredState     string `db:"desired_state"`
		RecoveryAttempts int    `db:"recovery_attempts"`
	}
	var rows []errRow
	if err := tx.SelectContext(ctx, &rows, q,
		rc.MaxAttempts, rc.MaxBackoff.Seconds(), rc.BaseBackoff.Seconds(), rc.BatchLimit); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_recovery_checker: query failed")
		return
	}
	if len(rows) == 0 {
		return
	}

	logger.WithFields("count", len(rows)).
		Info("sandbox_recovery_checker: re-converging error sandboxes")

	for _, row := range rows {
		if err := rc.repo.AutoRecover(ctx, row.ID); err != nil {
			// ErrNotFound means another path already moved the row out of
			// error (manual Recover, terminate) between the scan and now —
			// benign.
			if errors.Is(err, ErrNotFound) {
				continue
			}
			logger.WithFields("sandbox_id", row.ID, "error", err.Error()).
				Warn("sandbox_recovery_checker: AutoRecover failed")
			continue
		}
		logger.WithFields(
			"sandbox_id", row.ID,
			"desired_state", row.DesiredState,
			"attempt", row.RecoveryAttempts+1,
			"max_attempts", rc.MaxAttempts,
		).Info("sandbox_recovery_checker: error → re-converging toward desired_state")
	}
}
