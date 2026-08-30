package sandbox

import (
	"context"
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
		var failedIDs []string
		err = r.manager.db.SelectContext(ctx, &failedIDs, `
			SELECT id FROM sandbox_instances
			WHERE (
			        lifecycle_state = 'failed'
			    OR  (status = 'failed' AND lifecycle_state NOT IN ('terminated','running','stopped'))
			)
			LIMIT 50`)
		if err == nil && len(failedIDs) > 0 {
			logger.WithFields("count", len(failedIDs)).Info("sandbox_reaper: terminating failed sandboxes")
			for _, sbxID := range failedIDs {
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

		// Pass 4: Stuck intermediate states → force to failed (for >10min stuck)
		var stuckIDs []string
		err = r.manager.db.SelectContext(ctx, &stuckIDs, `
			SELECT id FROM sandbox_instances
			WHERE lifecycle_state IN ('stopping', 'reviving')
			  AND updated_at < NOW() - INTERVAL '10 minutes'
			LIMIT 50`)
		if err == nil && len(stuckIDs) > 0 {
			logger.WithFields("count", len(stuckIDs)).Info("sandbox_reaper: force-failing stuck intermediate sandboxes")
			for _, sbxID := range stuckIDs {
				r.manager.compensateLifecycle(ctx, sbxID, LifecycleFailed, LifecycleStopping, LifecycleReviving)
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

// Stop shuts down the reaper goroutine and waits for it to finish.
func (r *Reaper) Stop() {
	r.cancel()
	<-r.done
}
