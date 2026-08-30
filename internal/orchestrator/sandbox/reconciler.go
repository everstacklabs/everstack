package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Reconciler drives the sandbox lifecycle state machine. Rows are
// LEASED (claim stamps reconcile_locked_by/at and commits immediately)
// and processed by a bounded worker pool, so one slow VM create no
// longer serializes every other lifecycle operation behind it. The
// advisory lock only serializes claiming, held for milliseconds; with
// multiple gateway pods, whichever pod claims first runs the row and
// expired leases are reclaimable by anyone.
//
// Failures during convergence states (creating/stopping/reviving/
// archiving/terminating) increment reconcile_attempts with backoff and
// land in the recoverable 'error' state after the cap. Failures on any
// other state (running/sleeping) are NEVER allowed to flip the row to
// error — see the "Critical corollary" in
// docs/design/sandbox-reconciler-plan.md.
type Reconciler struct {
	repo     *Repository
	executor Executor
	db       *sqlx.DB

	// Tunables — exported so callers (start_api.go) can override for
	// dev / tests without re-building.
	Interval      time.Duration
	BatchSize     int
	Workers       int           // max rows in flight at once
	PerRowTimeout time.Duration // Step budget per row
	LeaseTimeout  time.Duration // lease validity; expired leases are reclaimable
	MaxAttempts   int
	LeaderLockKey string
	LeaderID      string
	OnTransition  func(row Row) // optional hook for tests / metrics

	inflight atomic.Int64
}

// NewReconciler returns a Reconciler with sensible production defaults.
// Callers can override fields on the returned struct before calling Run.
func NewReconciler(repo *Repository, executor Executor, db *sqlx.DB) *Reconciler {
	return &Reconciler{
		repo:     repo,
		executor: executor,
		db:       db,
		Interval: 200 * time.Millisecond,
		BatchSize: 20,
		Workers:   4,
		// 90s covers the worst observed fcagent create (rootfs clone +
		// boot under load); the lease outlives the row budget so a
		// healthy in-flight step is never reclaimed mid-run.
		PerRowTimeout: 90 * time.Second,
		LeaseTimeout:  2 * time.Minute,
		MaxAttempts:   6,
		LeaderLockKey: "sandbox-reconciler",
		LeaderID:      defaultLeaderID(),
	}
}

// defaultLeaderID builds a stable id for this gateway pod. Used only
// for the reconcile_locked_by fingerprint (operator visibility) — the
// leader election itself is keyed on LeaderLockKey, not LeaderID, so
// uniqueness here is informational, not semantic. Kept as
// hostname/full-uuid to make 'who held this lock last?' answerable
// by ops without reverse-engineering pod restart timelines.
func defaultLeaderID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%s", host, uuid.New().String())
}

// Run blocks until ctx is cancelled. Each tick:
//  1. Compute free worker capacity; skip the tick when saturated.
//  2. ClaimDue leases up to that many due rows (commit immediately).
//  3. Dispatch each row to a goroutine; processRow runs Step with a
//     bounded ctx and writes back via lease-guarded ApplyTransition /
//     RecordFailure.
//
// The ticker keeps claiming while workers run, so a slow create on
// one row no longer blocks stop/terminate on every other row.
//
// A panic in any row's processing is caught per-row so the reconciler
// goroutine survives bad rows; the panicked row's lease expires and it
// is reclaimed on a later tick.
func (r *Reconciler) Run(ctx context.Context) error {
	logger.WithFields(
		"leader_id", r.LeaderID,
		"interval_ms", r.Interval.Milliseconds(),
		"batch_size", r.BatchSize,
		"workers", r.Workers,
	).Info("sandbox_reconciler: starting")

	t := time.NewTicker(r.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_reconciler: stopping")
			return ctx.Err()
		case <-t.C:
			r.tick(ctx)
		}
	}
}

// tick leases due rows up to free worker capacity and dispatches them.
func (r *Reconciler) tick(ctx context.Context) {
	workers := r.Workers
	if workers <= 0 {
		workers = 1
	}
	free := workers - int(r.inflight.Load())
	if free <= 0 {
		return
	}
	limit := r.BatchSize
	if limit > free {
		limit = free
	}

	rows, err := r.repo.ClaimDue(ctx, r.LeaderLockKey, r.LeaderID, limit, r.LeaseTimeout)
	if err != nil {
		if errors.Is(err, ErrNotLeader) {
			// Another pod is claiming right now — quiet skip.
			return
		}
		logger.WithFields("error", err.Error()).
			Warn("sandbox_reconciler: claim failed")
		return
	}
	for _, row := range rows {
		r.inflight.Add(1)
		go func(row Row) {
			defer r.inflight.Add(-1)
			defer func() {
				if rec := recover(); rec != nil {
					logger.WithFields(
						"panic", fmt.Sprintf("%v", rec),
						"sandbox_id", row.ID,
						"leader_id", r.LeaderID,
					).Error("sandbox_reconciler: recovered from panic in row processing")
				}
			}()
			r.processRow(ctx, row)
		}(row)
	}
}

// processRow runs Step on one leased row and writes the result back.
// All write-backs are guarded by lease ownership; ErrLeaseLost means
// another claimer took over after our lease expired and our result is
// discarded (steps are idempotent, so the new owner converges).
func (r *Reconciler) processRow(ctx context.Context, row Row) {
	rctx, cancel := context.WithTimeout(ctx, r.PerRowTimeout)
	defer cancel()

	logger.WithFields(
		"sandbox_id", row.ID,
		"from_state", row.LifecycleState,
		"desired_state", row.DesiredState,
		"attempts", row.ReconcileAttempts,
	).Info("sandbox_reconciler: stepping row")

	next, err := Step(rctx, r.executor, row)
	if err != nil {
		// Convergence states retry with backoff; non-convergence
		// states just park (the row's actual VM is fine, we just
		// couldn't probe it this time, no reason to escalate).
		if IsConvergenceState(row.LifecycleState) {
			if recErr := r.repo.RecordFailure(ctx, r.LeaderID, row, err, r.MaxAttempts); recErr != nil {
				if errors.Is(recErr, ErrLeaseLost) {
					logger.WithFields("sandbox_id", row.ID).
						Info("sandbox_reconciler: lease lost during failure record; discarding")
					return
				}
				logger.WithFields("sandbox_id", row.ID, "error", recErr.Error()).
					Warn("sandbox_reconciler: RecordFailure failed")
			}
			logger.WithFields(
				"sandbox_id", row.ID,
				"state", row.LifecycleState,
				"attempts", row.ReconcileAttempts+1,
				"error", err.Error(),
			).Warn("sandbox_reconciler: convergence step failed")
			return
		}
		// Non-convergence error: park the row and move on. Don't
		// touch reconcile_attempts.
		parked := parkRow(row, 30*time.Second)
		if applyErr := r.repo.ApplyTransition(ctx, r.LeaderID, parked); applyErr != nil && !errors.Is(applyErr, ErrLeaseLost) {
			logger.WithFields("sandbox_id", row.ID, "error", applyErr.Error()).
				Warn("sandbox_reconciler: park ApplyTransition failed")
		}
		logger.WithFields(
			"sandbox_id", row.ID,
			"state", row.LifecycleState,
			"error", err.Error(),
		).Info("sandbox_reconciler: non-convergence step error, parked")
		return
	}

	if err := r.repo.ApplyTransition(ctx, r.LeaderID, next); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			logger.WithFields("sandbox_id", row.ID).
				Info("sandbox_reconciler: lease lost during write-back; discarding result")
			return
		}
		logger.WithFields("sandbox_id", row.ID, "error", err.Error()).
			Warn("sandbox_reconciler: ApplyTransition failed")
		return
	}
	logger.WithFields(
		"sandbox_id", row.ID,
		"from_state", row.LifecycleState,
		"to_state", next.LifecycleState,
	).Info("sandbox_reconciler: transition committed")

	if r.OnTransition != nil {
		r.OnTransition(next)
	}
}
