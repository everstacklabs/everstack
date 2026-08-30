package sandbox

import (
	"context"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// gcInterval is how often the snapshot GC loop runs. Hourly is cheap
// (a small DB query + N deletes) and bounds how long stale snapshots
// linger after termination. Tunable via SetSnapshotGCInterval if a
// deployment wants something different.
const gcDefaultInterval = 1 * time.Hour

// terminatedSandboxAge is how long we wait after a sandbox row goes
// terminal before purging its R2 snapshot. The window gives an
// operator time to investigate an unexpected termination + manually
// restore from snapshot if needed.
const terminatedSandboxAge = 7 * 24 * time.Hour

// deletedAgentAge is the same idea but scoped to agent deletion:
// when the owning agent is soft-deleted, we keep its sandboxes'
// snapshots for 30 days before purging.
const deletedAgentAge = 30 * 24 * time.Hour

// StartSnapshotGC begins the periodic sweep that deletes R2 prefixes
// for sandboxes that have been terminated long enough. Idempotent;
// calling more than once is a no-op. The loop stops when the manager
// is destroyed (StopSnapshotScheduler also drops this).
func (m *SandboxManager) StartSnapshotGC() {
	m.r2SnapshotMu.Lock()
	defer m.r2SnapshotMu.Unlock()
	if m.snapshotGCCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.snapshotGCCancel = cancel
	go m.runSnapshotGCLoop(ctx)
	logger.WithFields("interval", gcDefaultInterval.String()).
		Info("sandbox_manager: snapshot GC loop started")
}

// stopSnapshotGC cancels the GC loop. Called from StopSnapshotScheduler.
func (m *SandboxManager) stopSnapshotGC() {
	// Note: caller holds r2SnapshotMu.
	if m.snapshotGCCancel != nil {
		m.snapshotGCCancel()
		m.snapshotGCCancel = nil
	}
}

func (m *SandboxManager) runSnapshotGCLoop(ctx context.Context) {
	// Don't sweep immediately on startup — the manager may still be
	// rehydrating instances and we don't want to race the registry.
	initialDelay := gcDefaultInterval / 4
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_manager: snapshot GC loop stopped")
			return
		case <-timer.C:
			m.runSnapshotGCSweep(ctx)
			timer.Reset(gcDefaultInterval)
		}
	}
}

// runSnapshotGCSweep deletes R2 prefixes for sandboxes whose lifecycle
// row is terminated longer than terminatedSandboxAge, or whose owning
// agent has been soft-deleted longer than deletedAgentAge. Best-
// effort: a Postgres or R2 failure logs the error and moves on so the
// next sweep retries the remainder.
func (m *SandboxManager) runSnapshotGCSweep(ctx context.Context) {
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return
	}
	if m.db == nil {
		return
	}

	type victim struct {
		ID       string `db:"id"`
		TenantID string `db:"tenant_id"`
	}
	// Pull terminated sandboxes that are stale enough, plus sandboxes
	// whose owning agent has been soft-deleted long enough. Limit to
	// 500 per sweep so a backlog can't lock up the DB for minutes.
	const q = `
		SELECT DISTINCT si.id, si.tenant_id
		FROM sandbox_instances si
		-- agent_definitions.id is UUID but sandbox_instances.agent_id is
		-- VARCHAR(255) (nullable, not a real FK), so a bare ad.id = si.agent_id
		-- raises "operator does not exist: uuid = character varying" and the
		-- whole GC sweep fails. Cast the uuid to text (NOT agent_id::uuid, which
		-- would throw on any empty/non-uuid agent_id value).
		LEFT JOIN agent_definitions ad ON ad.id::text = si.agent_id
		WHERE
			(
				COALESCE(si.lifecycle_state, '') IN ('terminated', 'failed')
				AND si.updated_at < NOW() - $1::interval
			)
			OR
			(
				ad.deleted_at IS NOT NULL
				AND ad.deleted_at < NOW() - $2::interval
			)
		LIMIT 500`

	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var victims []victim
	if err := m.db.SelectContext(qCtx, &victims, q,
		terminatedSandboxAge.String(),
		deletedAgentAge.String(),
	); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("sandbox_manager: snapshot GC query failed")
		return
	}
	if len(victims) == 0 {
		return
	}
	logger.WithFields("count", len(victims)).
		Info("sandbox_manager: snapshot GC sweep starting")

	var wg sync.WaitGroup
	gcSem := make(chan struct{}, 8)
	for _, v := range victims {
		v := v
		gcSem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-gcSem }()
			delCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := store.Delete(delCtx, v.TenantID, v.ID); err != nil {
				logger.WithFields("sandbox_id", v.ID, "error", err.Error()).
					Warn("sandbox_manager: snapshot GC delete failed")
			}
		}()
	}
	wg.Wait()
	logger.WithFields("count", len(victims)).
		Info("sandbox_manager: snapshot GC sweep complete")
}
