package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// IdleChecker scans running sandboxes on a fixed cadence and writes
// desired_state='sleeping' on rows that have been idle past their
// retention window. The reconciler then converges them to the
// sleeping state on its next tick.
//
// Decoupled from the Reconciler by design: policy decisions ('is
// this row idle?') live here; state machine transitions
// ('running → stopping → sleeping') live in step.go. Mirrors Fly's
// flyd separation of lifecycle reconciler from idle policy.
//
// Critical corollary from the plan: this component CANNOT directly
// transition lifecycle_state. It can only write desired_state, which
// the reconciler picks up. That keeps the "running rows never enter
// the failure path" invariant intact — the worst this checker can do
// is mark a still-active row as sleeping, which the user can revive.
type IdleChecker struct {
	db        *sqlx.DB
	repo      *Repository
	leaderKey string

	// Tunables.
	Interval           time.Duration // how often to scan
	DefaultIdleMinutes int           // tier fallback when row's auto_stop_minutes is NULL

	// SpecialCandidates returns sandbox IDs whose idle policy the SQL
	// pass cannot express: persistent troopers (active-session check +
	// plan-tier window) and keep-warm sandboxes (trigger check). The
	// manager implements this (IdleSleepCandidatesSpecial); the legacy
	// reaper's Pass 1 used to own these rows.
	SpecialCandidates func() []string
}

// NewIdleChecker returns an IdleChecker with sensible defaults.
// The 15 minute default auto-stop matches Daytona's; per-plan-tier
// overrides come in via SetIdleWindow / the row's own
// auto_stop_minutes.
func NewIdleChecker(db *sqlx.DB, repo *Repository) *IdleChecker {
	return &IdleChecker{
		db:                 db,
		repo:               repo,
		leaderKey:          "sandbox-idle-checker",
		Interval:           15 * time.Second,
		DefaultIdleMinutes: 15,
	}
}

// Run blocks until ctx is cancelled. Each tick:
//  1. Acquire leader advisory lock (separate key from the reconciler
//     so the two can run on different pods if we ever scale out).
//  2. Find running rows whose last_used_at is older than their
//     effective idle window.
//  3. For each, write desired_state='sleeping' via the repo. The
//     reconciler picks up the change on its next tick (200ms).
func (i *IdleChecker) Run(ctx context.Context) error {
	logger.WithFields(
		"interval_s", i.Interval.Seconds(),
		"default_idle_minutes", i.DefaultIdleMinutes,
	).Info("sandbox_idle_checker: starting")

	t := time.NewTicker(i.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_idle_checker: stopping")
			return ctx.Err()
		case <-t.C:
			i.tick(ctx)
		}
	}
}

// tick scans for idle rows and bumps desired_state. The leader lock
// uses pg_try_advisory_xact_lock inside the scan transaction: the old
// session-scoped pg_try_advisory_lock acquired on one pooled
// connection and released on another, silently leaking the lock and
// disabling the checker once a second gateway pod existed.
//
// Idle window per row: auto_stop_minutes (NULL = the tier default
// passed in, 0 = auto-stop disabled). The legacy idle_retention_secs
// semantics had 0 meaning both "disabled" (legacy reaper) and "use
// default" (old query here); the minutes column makes them distinct.
//
// Important: persistent troopers (persistent=true) and rows with
// keep_warm=true are EXCLUDED here. The trooper layer manages those
// via its own resolver and a different cadence; double-managing
// would race.
func (i *IdleChecker) tick(ctx context.Context) {
	tx, err := i.db.BeginTxx(ctx, nil)
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_idle_checker: begin failed")
		return
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, i.leaderKey); err != nil || !locked {
		return
	}

	const q = `
		SELECT id, tenant_id
		FROM sandbox_instances
		WHERE lifecycle_state = $1
		  AND desired_state   = $2
		  AND COALESCE(persistent, false) = false
		  AND COALESCE(keep_warm,  false) = false
		  AND last_used_at IS NOT NULL
		  AND COALESCE(auto_stop_minutes, $3) > 0
		  AND last_used_at < NOW() - make_interval(mins => COALESCE(auto_stop_minutes, $3))
		LIMIT 100`

	type idleRow struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	var rows []idleRow
	if err := tx.SelectContext(ctx, &rows, q,
		StateRunning, DesireRunning, i.DefaultIdleMinutes); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_idle_checker: query failed")
		return
	}
	if len(rows) > 0 {
		logger.WithFields("count", len(rows)).
			Info("sandbox_idle_checker: marking idle sandboxes for sleep")
	}

	for _, row := range rows {
		if err := i.repo.SetDesiredState(ctx, row.ID, DesireSleeping); err != nil {
			logger.WithFields(
				"sandbox_id", row.ID,
				"tenant_id", row.TenantID,
				"error", err.Error(),
			).Warn("sandbox_idle_checker: SetDesiredState failed")
			continue
		}
		logger.WithFields(
			"sandbox_id", row.ID,
			"tenant_id", row.TenantID,
		).Info("sandbox_idle_checker: marked idle → desired_state=sleeping")
	}

	// Special rows (persistent troopers, keep-warm) come from the
	// manager's in-memory policy hook. SetDesiredState only advances
	// running rows, so duplicates and stale candidates are no-ops.
	if i.SpecialCandidates != nil {
		for _, id := range i.SpecialCandidates() {
			if err := i.repo.SetDesiredState(ctx, id, DesireSleeping); err != nil {
				logger.WithFields("sandbox_id", id, "error", err.Error()).
					Warn("sandbox_idle_checker: special candidate SetDesiredState failed")
				continue
			}
			logger.WithFields("sandbox_id", id).
				Info("sandbox_idle_checker: trooper/keep-warm idle → desired_state=sleeping")
		}
	}
}

// SetIdleWindow lets callers override the default idle window for
// rows whose auto_stop_minutes is unset. Mostly useful for tests.
func (i *IdleChecker) SetIdleWindow(d time.Duration) {
	mins := int(d.Minutes())
	if mins < 1 {
		mins = 1
	}
	i.DefaultIdleMinutes = mins
}

// describe returns a one-line summary for log lines / metrics labels.
func (i *IdleChecker) describe() string {
	return fmt.Sprintf("IdleChecker{interval=%s default=%dm}",
		i.Interval, i.DefaultIdleMinutes)
}
