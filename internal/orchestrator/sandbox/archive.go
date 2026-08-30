package lifecycle

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// ArchiveChecker runs two sweeps on a fixed cadence:
//
//  1. Archive sweep: sleeping sandboxes past their auto_archive_after_days
//     threshold transition to desired_state='archived'. The reconciler drives
//     sleeping→archiving→archived (no backend calls -- VM already gone).
//
//  2. Delete sweep: sandboxes in sleeping or archived states past their
//     auto_delete_after_days threshold transition to desired_state='terminated'.
//
// Decoupled from the Reconciler by design: policy decisions here;
// state machine transitions in step.go.
type ArchiveChecker struct {
	db        *sqlx.DB
	repo      *Repository
	leaderKey string

	Interval time.Duration
}

// NewArchiveChecker returns an ArchiveChecker with defaults.
func NewArchiveChecker(db *sqlx.DB, repo *Repository) *ArchiveChecker {
	return &ArchiveChecker{
		db:        db,
		repo:      repo,
		leaderKey: "sandbox-archive-checker",
		Interval:  5 * time.Minute,
	}
}

// Run blocks until ctx is cancelled.
func (c *ArchiveChecker) Run(ctx context.Context) error {
	logger.WithFields("interval_m", c.Interval.Minutes()).
		Info("sandbox_archive_checker: starting")

	t := time.NewTicker(c.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_archive_checker: stopping")
			return ctx.Err()
		case <-t.C:
			c.withLeaderLock(ctx, func() {
				c.tickArchive(ctx)
				c.tickDelete(ctx)
			})
		}
	}
}

// withLeaderLock runs fn while holding an xact-scoped advisory lock.
// The previous session-scoped pg_try_advisory_lock acquired on one
// pooled connection and released on another, silently leaking the lock
// (and disabling the checker entirely) once more than one gateway pod
// existed. The xact-scoped variant ties acquire and release to the
// same connection by construction.
func (c *ArchiveChecker) withLeaderLock(ctx context.Context, fn func()) {
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, c.leaderKey); err != nil || !locked {
		return
	}
	fn()
}

// tickArchive finds sleeping sandboxes past their auto-archive window
// and transitions them to desired_state='archived'.
//
// Window resolution: auto_archive_minutes when set (0 = disabled),
// else legacy auto_archive_after_days for rows that predate the
// minutes migration.
func (c *ArchiveChecker) tickArchive(ctx context.Context) {
	// stopped_at records when the VM was stopped (set during stopping→sleeping).
	// We use it to measure how long the sandbox has been in the sleeping state.
	const q = `
		SELECT id, tenant_id
		FROM sandbox_instances
		WHERE lifecycle_state          = 'sleeping'
		  AND desired_state            = 'sleeping'
		  AND COALESCE(persistent, false) = false
		  AND COALESCE(auto_archive_minutes, auto_archive_after_days * 1440, 0) > 0
		  AND stopped_at IS NOT NULL
		  AND stopped_at < NOW() - make_interval(mins => COALESCE(auto_archive_minutes, auto_archive_after_days * 1440))
		LIMIT 100`

	type row struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	var rows []row
	if err := c.db.SelectContext(ctx, &rows, q); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_archive_checker: archive query failed")
		return
	}
	for _, r := range rows {
		if err := c.repo.SetDesiredState(ctx, r.ID, DesireArchived); err != nil {
			logger.WithFields("sandbox_id", r.ID, "error", err.Error()).
				Warn("sandbox_archive_checker: archive SetDesiredState failed")
			continue
		}
		logger.WithFields("sandbox_id", r.ID, "tenant_id", r.TenantID).
			Info("sandbox_archive_checker: marked for archive")
	}
}

// tickDelete finds sandboxes past their auto-delete window and
// transitions them to desired_state='terminated'.
//
// Window resolution: auto_delete_minutes when set (-1 = never,
// 0 = ephemeral: delete as soon as stopped), else legacy
// auto_delete_after_days.
//
// Elapsed time is measured from stopped_at for sleeping rows,
// archived_at for archived rows, and error_at for error rows (an
// abandoned error row should not outlive its retention any more than
// a sleeping one would).
//
// Expired legacy revivable_until rows are also covered here: the
// legacy reaper's Pass 2 used to terminate them; folding the clause in
// keeps pre-migration rows on their promised retention schedule.
func (c *ArchiveChecker) tickDelete(ctx context.Context) {
	const q = `
		SELECT id, tenant_id
		FROM sandbox_instances
		WHERE lifecycle_state IN ('sleeping', 'archived', 'error')
		  AND desired_state   IN ('sleeping', 'archived', 'running')
		  AND COALESCE(persistent, false) = false
		  AND (
		      (COALESCE(auto_delete_minutes, auto_delete_after_days * 1440, -1) >= 0
		       AND (
		           (lifecycle_state = 'sleeping'
		               AND stopped_at IS NOT NULL
		               AND stopped_at < NOW() - make_interval(mins => COALESCE(auto_delete_minutes, auto_delete_after_days * 1440)))
		        OR (lifecycle_state = 'archived'
		               AND archived_at IS NOT NULL
		               AND archived_at < NOW() - make_interval(mins => COALESCE(auto_delete_minutes, auto_delete_after_days * 1440)))
		        OR (lifecycle_state = 'error'
		               AND error_at IS NOT NULL
		               AND error_at < NOW() - make_interval(mins => GREATEST(COALESCE(auto_delete_minutes, auto_delete_after_days * 1440), 1440)))
		       ))
		   OR (lifecycle_state = 'sleeping'
		           AND revivable_until IS NOT NULL
		           AND revivable_until < NOW())
		  )
		LIMIT 100`

	type row struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	var rows []row
	if err := c.db.SelectContext(ctx, &rows, q); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_archive_checker: delete query failed")
		return
	}
	for _, r := range rows {
		if err := c.repo.SetDesiredState(ctx, r.ID, DesireTerminated); err != nil {
			logger.WithFields("sandbox_id", r.ID, "error", err.Error()).
				Warn("sandbox_archive_checker: delete SetDesiredState failed")
			continue
		}
		logger.WithFields("sandbox_id", r.ID, "tenant_id", r.TenantID).
			Info("sandbox_archive_checker: marked for auto-delete")
	}
}
