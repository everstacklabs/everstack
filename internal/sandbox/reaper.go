package sandbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Reaper periodically cleans up expired sandbox instances.
// Follows the same pattern as runtime.StaleDetector and HeartbeatWriter.
type Reaper struct {
	manager  *SandboxManager
	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewReaper creates and starts a new reaper goroutine.
func NewReaper(manager *SandboxManager, interval time.Duration) *Reaper {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Reaper{
		manager:  manager,
		interval: interval,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go r.run(ctx)
	return r
}

func (r *Reaper) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Immediate startup sweep
	r.sweep()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *Reaper) sweep() {
	logger.Info("sandbox_reaper: sweep started")
	defer logger.Info("sandbox_reaper: sweep completed")

	// Flush in-memory LastUsedAt timestamps to the database before checking
	// for expired instances. This runs at most once per sweep interval (60s).
	r.manager.flushLastUsedTimestamps()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// When the sandbox backend is unhealthy (for example, Kubernetes API is
	// unavailable or the cluster is in a bad state), skip destructive reaper
	// actions for this sweep. Otherwise we can incorrectly transition healthy
	// persistent sandboxes into stopped/failed states simply because the backend
	// cannot answer lifecycle calls right now.
	if err := r.manager.Healthy(ctx); err != nil {
		logger.WithFields("backend", r.manager.BackendName(), "error", err.Error()).
			Warn("sandbox_reaper: backend unhealthy, skipping sweep")
		return
	}

	// Pass 1: Idle running → stop (existing idle retention logic)
	expired := r.manager.GetExpiredInstances()
	r.manager.logReaperDebug()
	if len(expired) > 0 {
		logger.WithFields("count", len(expired)).Info("sandbox_reaper: stopping idle sandboxes")
		for _, sessionID := range expired {
			inst, ok := r.manager.GetInstance(sessionID)
			if !ok {
				continue
			}
			if err := r.manager.StopSandbox(ctx, inst.ID); err != nil {
				// Only fallback to legacy destroy when lifecycle DB is unavailable.
				if r.manager.db == nil {
					// Never destroy persistent troopers — just log and skip.
					if inst.Persistent {
						logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
							Warn("sandbox_reaper: stop failed for persistent trooper (no DB); skipping")
						continue
					}
					if err2 := r.manager.DestroyWithReason(ctx, sessionID, "expired"); err2 != nil {
						logger.WithFields("session_id", sessionID, "error", err2.Error()).
							Warn("sandbox_reaper: failed to stop/destroy expired sandbox")
					}
					continue
				}

				// If stop failed (e.g. snapshot failure), check if the container is
				// still alive. If not, mark as stopped (not terminated) for persistent
				// troopers so they can be revived. Only force-terminate ephemeral sandboxes.
				if _, statusErr := r.manager.backend.Status(ctx, inst.ID); statusErr != nil {
					if inst.Persistent {
						logger.WithFields("sandbox_id", inst.ID).
							Warn("sandbox_reaper: stop failed and container is gone for persistent trooper; marking stopped")
						r.manager.db.ExecContext(ctx,
							`UPDATE sandbox_instances SET lifecycle_state = 'stopped', status = 'stopped', stopped_at = NOW(), updated_at = NOW() WHERE id = $1`,
							inst.ID)
						// Update agent lifecycle_status so frontend shows sleeping state
						if inst.AgentID != "" {
							r.manager.db.ExecContext(ctx,
								`UPDATE agent_definitions SET lifecycle_status = 'sleeping', updated_at = NOW() WHERE id = $1`,
								inst.AgentID)
						}
						r.manager.mu.Lock()
						inst.LifecycleState = LifecycleStopped
						inst.Status = StatusStopped
						r.manager.mu.Unlock()
						continue
					}
					logger.WithFields("sandbox_id", inst.ID).
						Warn("sandbox_reaper: stop failed and container is gone; force-terminating")
					if termErr := r.manager.TerminateSandbox(ctx, inst.ID); termErr != nil {
						logger.WithFields("sandbox_id", inst.ID, "error", termErr.Error()).
							Warn("sandbox_reaper: force-terminate also failed")
					}
					continue
				}

				logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
					Warn("sandbox_reaper: failed to stop expired sandbox")
			}
		}
	}

	// Pass 2: Expired stopped → terminate (revivable_until < now)
	// Persistent troopers do NOT auto-terminate when stopped — they remain
	// revivable until explicitly terminated by the user.
	if r.manager.db != nil {
		var stoppedIDs []string
		err := r.manager.db.SelectContext(ctx, &stoppedIDs, `
			SELECT id FROM sandbox_instances
			WHERE lifecycle_state = 'stopped'
			  AND persistent = false
			  AND revivable_until IS NOT NULL
			  AND revivable_until < NOW()
			LIMIT 50`)
		if err == nil && len(stoppedIDs) > 0 {
			logger.WithFields("count", len(stoppedIDs)).Info("sandbox_reaper: terminating expired stopped sandboxes")
			for _, sbxID := range stoppedIDs {
				if err := r.manager.TerminateSandbox(ctx, sbxID); err != nil {
					logger.WithFields("sandbox_id", sbxID, "error", err.Error()).
						Warn("sandbox_reaper: failed to terminate expired stopped sandbox")
				}
			}
		}

		// Pass 3: Failed → terminate.
		//
		// Match BOTH lifecycle_state='failed' AND status='failed' for
		// rows where the lifecycle drifted (e.g. the row went into
		// status='failed' from a backend.Create error but
		// lifecycle_state stuck at 'creating' / 'pending'). These
		// surface in the dashboard's "needs attention" tile and never
		// self-heal otherwise — Pass 4 below catches stuck stopping/
		// reviving but doesn't see status-failed rows in other states.
		var failed []cleanupCandidate
		err = r.manager.db.SelectContext(ctx, &failed, `
			SELECT id, lifecycle_state, updated_at, reconcile_locked_at FROM sandbox_instances
			WHERE (
			        lifecycle_state = 'failed'
			    OR  (status = 'failed' AND lifecycle_state NOT IN ('terminated','running','stopped'))
			)
			  -- A row under a live reconciler lease is being worked on.
			  AND (reconcile_locked_at IS NULL OR reconcile_locked_at < NOW() - make_interval(secs => $1))
			LIMIT 50`, stuckTransitionAfter.Seconds())
		if err == nil && len(failed) > 0 {
			logger.WithFields("count", len(failed)).Info("sandbox_reaper: terminating failed sandboxes")
			for _, cand := range failed {
				sbxID := cand.ID
				if r.manager.holdForAttentionIfWorkspaceStored(ctx, cand) {
					continue
				}
				if err := r.manager.TerminateSandbox(ctx, sbxID); err != nil {
					// Backend call probably fails because the actual
					// VM/pod is long gone. Force the DB row to
					// terminated so the dashboard health tile clears
					// even when backend cleanup is impossible.
					logger.WithFields("sandbox_id", sbxID, "error", err.Error()).
						Warn("sandbox_reaper: TerminateSandbox failed; force-marking row terminated")
					_, _ = r.manager.db.ExecContext(ctx,
						`UPDATE sandbox_instances
						 SET lifecycle_state = 'terminated',
						     status = 'terminated',
						     updated_at = NOW(),
						     destroyed_at = COALESCE(destroyed_at, NOW())
						 WHERE id = $1`, sbxID)
				}
			}
		}

		// Pass 4: Stuck intermediate states → force to failed. The
		// threshold must exceed the longest legitimate stop or wake
		// (lease plus step budget, which now includes moving the
		// workspace to or from object storage); see stuckTransitionAfter.
		stuck, err := r.manager.listStuckTransitions(ctx)
		if err == nil && len(stuck) > 0 {
			logger.WithFields("count", len(stuck)).Info("sandbox_reaper: force-failing stuck intermediate sandboxes")
			for _, cand := range stuck {
				if r.manager.holdForAttentionIfWorkspaceStored(ctx, cand) {
					continue
				}
				if ok, err := r.manager.guardedLifecycleUpdate(ctx, cand, `lifecycle_state = 'failed'`); err != nil || !ok {
					logger.WithFields("sandbox_id", cand.ID, "moved_on", !ok).
						Info("sandbox_reaper: stuck row changed since the scan; left alone")
				}
			}
		}

		// Retry stuck terminating
		var stuckTerminating []string
		err = r.manager.db.SelectContext(ctx, &stuckTerminating, `
			SELECT id FROM sandbox_instances
			WHERE lifecycle_state = 'terminating'
			  AND updated_at < NOW() - INTERVAL '10 minutes'
			LIMIT 50`)
		if err == nil && len(stuckTerminating) > 0 {
			for _, sbxID := range stuckTerminating {
				_ = r.manager.TerminateSandbox(ctx, sbxID)
			}
		}
	}
}

// stuckTransitionAfter is how long a row may sit in stopping or reviving
// before the reaper treats it as stuck. It must exceed the reconciler's
// lease (6 min) plus its transfer step budget (5 min), or a slow but
// healthy workspace upload or download would be failed mid-flight.
const stuckTransitionAfter = 20 * time.Minute

// NeedsAttentionWorkspaceMessage is the customer-facing reason for a
// sandbox held back from automatic cleanup because its saved workspace
// is the only copy.
const NeedsAttentionWorkspaceMessage = "This sandbox needs attention: a lifecycle operation did not finish, and its saved /workspace is still stored. " +
	"We kept it instead of cleaning it up. Contact support to recover it, or delete the sandbox if you no longer need it."

// cleanupCandidate is a row the reaper observed as stuck or failed, with
// the values every later write is conditioned on.
type cleanupCandidate struct {
	ID        string       `db:"id"`
	State     string       `db:"lifecycle_state"`
	UpdatedAt time.Time    `db:"updated_at"`
	LockedAt  sql.NullTime `db:"reconcile_locked_at"`
}

// listStuckTransitions returns stopping/reviving rows idle past
// stuckTransitionAfter. Idle is measured from the later of updated_at and
// the reconciler lease stamp: claiming a row stamps only the lease (an
// updated_at bump would re-fire lifecycle webhooks), so a row the
// reconciler has reclaimed counts as active.
func (m *SandboxManager) listStuckTransitions(ctx context.Context) ([]cleanupCandidate, error) {
	var out []cleanupCandidate
	err := m.db.SelectContext(ctx, &out, `
		SELECT id, lifecycle_state, updated_at, reconcile_locked_at FROM sandbox_instances
		WHERE lifecycle_state IN ('stopping', 'reviving')
		  AND GREATEST(updated_at, COALESCE(reconcile_locked_at, updated_at)) < NOW() - make_interval(secs => $1)
		LIMIT 50`, stuckTransitionAfter.Seconds())
	return out, err
}

// guardedLifecycleUpdate applies set to the candidate's row only if the
// row is exactly as the reaper observed it: same state, same updated_at,
// same lease stamp. Any progress or reclaim since the scan makes it a
// no-op. ok reports whether exactly one row changed.
func (m *SandboxManager) guardedLifecycleUpdate(ctx context.Context, c cleanupCandidate, set string, args ...any) (bool, error) {
	if m.db == nil {
		return false, fmt.Errorf("guarded update for %s: no database", c.ID)
	}
	q := `UPDATE sandbox_instances SET ` + set + `, updated_at = NOW()
		WHERE id = $1 AND lifecycle_state = $2 AND updated_at = $3
		  AND reconcile_locked_at IS NOT DISTINCT FROM $4`
	res, err := m.db.ExecContext(ctx, q, append([]any{c.ID, c.State, c.UpdatedAt, c.LockedAt}, args...)...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// holdForAttentionIfWorkspaceStored decides whether automatic cleanup may
// fail or terminate a sandbox. It returns true (skip cleanup) when object
// storage may hold the only copy of the workspace, and also when that
// cannot be checked: on doubt nothing is deleted. A held sandbox is
// parked for a human with a reason, the VM handled so it neither leaks
// nor runs unbilled (see parkForAttention).
func (m *SandboxManager) holdForAttentionIfWorkspaceStored(ctx context.Context, c cleanupCandidate) bool {
	has, err := m.HasDurableWorkspace(ctx, c.ID)
	if err != nil {
		logger.WithFields("sandbox_id", c.ID, "error", err.Error()).
			Error("sandbox_reaper: could not check for a stored workspace; holding the sandbox for attention")
		m.parkForAttention(ctx, c, "could not check object storage for a saved workspace: "+err.Error())
		return true
	}
	if !has {
		return false
	}
	logger.WithFields("sandbox_id", c.ID, "state", c.State).
		Error("sandbox_reaper: sandbox has a stored workspace; holding it for attention instead of failing or terminating it")
	m.parkForAttention(ctx, c, "automatic cleanup skipped; the stored workspace may be the only copy")
	return true
}

// parkForAttention records a held sandbox. Every write is a guarded
// update against the row as observed (see guardedLifecycleUpdate), and a
// VM is only destroyed after that update succeeded, so a wake that
// completed or was reclaimed meanwhile keeps its VM.
//   - stopping: if the VM is still alive the sandbox goes back to running,
//     so it keeps billing as running and stays usable; the reason is shown.
//   - reviving: the row is parked first; then a VM on the host is a leak
//     of the stalled wake and is destroyed (the saved workspace is kept).
//   - otherwise the row is parked in error.
func (m *SandboxManager) parkForAttention(ctx context.Context, c cleanupCandidate, detail string) {
	const parkSet = `lifecycle_state = 'error', status = 'error', error = $5, error_reason = $6, error_at = NOW()`

	if c.State == LifecycleStopping {
		if _, statusErr := m.backend.Status(ctx, c.ID); statusErr == nil {
			ok, err := m.guardedLifecycleUpdate(ctx, c, `lifecycle_state = 'running', status = 'running', desired_state = 'running',
				billing_started_at = COALESCE(billing_started_at, NOW()), error = $5, error_reason = $6, error_at = NOW()`,
				WorkspaceErrorCodeNeedsAttention+": stop stalled with the VM alive; returned to running. "+truncateError(detail),
				NeedsAttentionWorkspaceMessage)
			if err != nil || !ok {
				logger.WithFields("sandbox_id", c.ID, "moved_on", !ok).
					Warn("sandbox_reaper: stalled stop changed since the scan; left alone")
				return
			}
			m.mu.Lock()
			if inst, found := m.instancesBySandbox[c.ID]; found {
				inst.LifecycleState = LifecycleRunning
				inst.Status = StatusRunning
			}
			m.mu.Unlock()
			return
		}
	}

	ok, err := m.guardedLifecycleUpdate(ctx, c, parkSet,
		WorkspaceErrorCodeNeedsAttention+": "+truncateError(detail), NeedsAttentionWorkspaceMessage)
	if err != nil || !ok {
		logger.WithFields("sandbox_id", c.ID, "moved_on", !ok).
			Warn("sandbox_reaper: row changed since the scan; not parking it and not touching its VM")
		return
	}
	if c.State != LifecycleReviving {
		return
	}
	if _, statusErr := m.backend.Status(ctx, c.ID); statusErr != nil {
		return
	}
	if err := m.backend.Destroy(ctx, c.ID); err != nil {
		logger.WithFields("sandbox_id", c.ID, "error", err.Error()).
			Error("sandbox_reaper: could not destroy the VM of a stalled wake; it may be leaking")
		return
	}
	logger.WithFields("sandbox_id", c.ID).Info("sandbox_reaper: destroyed the half-revived VM of a stalled wake")
}

// Stop shuts down the reaper goroutine and waits for it to finish.
func (r *Reaper) Stop() {
	r.cancel()
	<-r.done
}
