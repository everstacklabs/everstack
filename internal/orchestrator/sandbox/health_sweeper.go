package lifecycle

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// HealthSweeper detects VMs that died outside the system's control
// (guest crash, OOM kill, fcagent host loss) and surfaces them as the
// recoverable 'error' state instead of leaving the row 'running'
// forever.
//
// Before this existed, a dead VM was invisible: the DB said running,
// the admin UI offered a shell that could never connect, and nothing
// converged until a user manually terminated. The fcagent-side health
// monitor flips its OWN state, but no component mapped that onto the
// lifecycle row.
//
// Detection is poll-based: every Interval the sweeper asks the
// executor's BackendStatus for each running row. A typed NotFound (or
// route-missing-with-VM-not-found) answer is recorded as a suspect; a
// CONFIRMING second miss at least ConfirmAfter later flips the row to
// error with error_reason='vm_not_found'. The two-strike rule avoids
// flapping during fcagent rolling restarts where Status briefly errors
// for healthy VMs.
//
// Transport errors (agent unreachable, DNS blip) are NOT suspects;
// only authoritative "I do not have this VM" answers count.
type HealthSweeper struct {
	db       *sqlx.DB
	repo     *Repository
	executor Executor

	leaderKey string

	Interval     time.Duration
	ConfirmAfter time.Duration
	BatchLimit   int

	// suspects maps sandbox id to when the first authoritative miss
	// was observed. In-memory is fine: the sweeper is leader-locked,
	// and a pod restart only delays confirmation by one extra round.
	suspects map[string]time.Time
}

// missingComputeCloser is optionally implemented by the production executor.
// A confirmed-missing VM has stopped consuming compute even though its DB row
// still says running, so the open billing window must be closed before the row
// moves to error. Keeping this optional avoids coupling pure lifecycle tests to
// billing internals.
type missingComputeCloser interface {
	CloseMissingCompute(ctx context.Context, id, reason string, endedAt time.Time) error
}

// NewHealthSweeper returns a sweeper with production defaults.
//
// Cadence: a confirmed-dead row needs two consecutive authoritative
// misses at least ConfirmAfter apart, so detection latency is roughly
// one-to-two Intervals. With Interval=30s/ConfirmAfter=20s a dead VM
// reaches the recoverable 'error' state (and thus the RecoveryChecker)
// in ~30-60s instead of the old ~60-120s. ConfirmAfter stays < Interval
// so the second tick confirms rather than waiting a third; the
// two-strike rule itself is preserved to ride out fcagent rolling
// restarts where Status briefly answers NotFound for healthy VMs.
func NewHealthSweeper(db *sqlx.DB, repo *Repository, executor Executor) *HealthSweeper {
	return &HealthSweeper{
		db:           db,
		repo:         repo,
		executor:     executor,
		leaderKey:    "sandbox-health-sweeper",
		Interval:     30 * time.Second,
		ConfirmAfter: 20 * time.Second,
		BatchLimit:   200,
		suspects:     make(map[string]time.Time),
	}
}

// Run blocks until ctx is cancelled.
func (h *HealthSweeper) Run(ctx context.Context) error {
	logger.WithFields(
		"interval_s", h.Interval.Seconds(),
		"confirm_after_s", h.ConfirmAfter.Seconds(),
	).Info("sandbox_health_sweeper: starting")

	t := time.NewTicker(h.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_health_sweeper: stopping")
			return ctx.Err()
		case <-t.C:
			h.tick(ctx)
		}
	}
}

func (h *HealthSweeper) tick(ctx context.Context) {
	tx, err := h.db.BeginTxx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.GetContext(ctx, &locked,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, h.leaderKey); err != nil || !locked {
		return
	}

	const q = `
		SELECT id, tenant_id
		FROM sandbox_instances
		WHERE lifecycle_state = 'running'
		  AND desired_state   = 'running'
		ORDER BY updated_at ASC
		LIMIT $1`
	type runningRow struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	var rows []runningRow
	if err := tx.SelectContext(ctx, &rows, q, h.BatchLimit); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_health_sweeper: query failed")
		return
	}

	now := time.Now()
	alive := make(map[string]bool, len(rows))
	for _, row := range rows {
		alive[row.ID] = true
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, statusErr := h.executor.BackendStatus(probeCtx, row.ID)
		cancel()

		if statusErr == nil || !isNotFoundErr(statusErr) {
			// Healthy, or a transport-level error we don't treat as
			// authoritative. Clear any pending suspicion.
			delete(h.suspects, row.ID)
			continue
		}

		firstSeen, suspected := h.suspects[row.ID]
		if !suspected {
			h.suspects[row.ID] = now
			logger.WithFields("sandbox_id", row.ID, "error", statusErr.Error()).
				Info("sandbox_health_sweeper: VM missing; confirming next round")
			continue
		}
		if now.Sub(firstSeen) < h.ConfirmAfter {
			continue
		}

		delete(h.suspects, row.ID)
		if closer, ok := h.executor.(missingComputeCloser); ok {
			if err := closer.CloseMissingCompute(ctx, row.ID, "vm_not_found", now); err != nil {
				logger.WithFields("sandbox_id", row.ID, "error", err.Error()).
					Warn("sandbox_health_sweeper: failed to close missing VM billing window")
				continue
			}
		}
		if err := h.repo.MarkError(ctx, row.ID, "vm_not_found"); err != nil {
			logger.WithFields("sandbox_id", row.ID, "error", err.Error()).
				Warn("sandbox_health_sweeper: MarkError failed")
			continue
		}
		logger.WithFields("sandbox_id", row.ID, "tenant_id", row.TenantID).
			Warn("sandbox_health_sweeper: running VM confirmed dead; row moved to error (recoverable)")
	}

	// Drop suspicion for rows that left the running set (stopped,
	// terminated, or already errored by another path).
	for id := range h.suspects {
		if !alive[id] {
			delete(h.suspects, id)
		}
	}
}
