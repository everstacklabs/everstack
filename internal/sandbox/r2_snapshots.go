package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
	"golang.org/x/sync/semaphore"
)

// snapshotPollInterval is how often the periodic scheduler walks
// running sandboxes. 30 minutes matches the trigger policy chosen
// for Phase 2 — long enough that upload bandwidth is bounded, short
// enough that a host crash loses at most ~half an hour of work.
const snapshotPollInterval = 30 * time.Minute

// snapshotConcurrency caps how many sandboxes can be snapshotted in
// parallel during a single sweep. Each snapshot pauses its VM and
// uploads multiple GB; running them serially is slow, all at once
// would saturate network.
const snapshotConcurrency = 4

// SetSnapshotStore wires the object-storage snapshot store the manager
// uses for R2-backed VM snapshots. Pass snapshot.NewDisabled() (or nil,
// which is treated as Disabled) to turn the feature off — every method
// will no-op cleanly.
//
// Starts the periodic scheduler goroutine the first time a real
// (non-Disabled) store is wired. Idempotent; safe to call once at
// startup.
func (m *SandboxManager) SetSnapshotStore(store snapshot.Store) {
	if store == nil {
		store = snapshot.NewDisabled()
	}
	m.r2SnapshotMu.Lock()
	defer m.r2SnapshotMu.Unlock()
	m.r2Snapshots = store
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return
	}
	if m.r2SnapshotCancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.r2SnapshotCancel = cancel
	go m.runSnapshotScheduler(ctx)
	logger.WithFields("interval", snapshotPollInterval.String()).
		Info("sandbox_manager: R2 snapshot scheduler started")
}

// StopSnapshotScheduler cancels the periodic snapshot + GC loops.
// Called from DestroyAll during graceful shutdown.
func (m *SandboxManager) StopSnapshotScheduler() {
	m.r2SnapshotMu.Lock()
	defer m.r2SnapshotMu.Unlock()
	if m.r2SnapshotCancel != nil {
		m.r2SnapshotCancel()
		m.r2SnapshotCancel = nil
	}
	m.stopSnapshotGC()
}

// SaveR2Snapshot dispatches a single snapshot for sandboxID. Returns
// nil with no error when the feature is disabled, the backend does
// not support VM snapshots, or the sandbox isn't currently running on
// this host. Callers can fire-and-forget without checking the result.
func (m *SandboxManager) SaveR2Snapshot(ctx context.Context, sandboxID, trigger string) (*snapshot.Manifest, error) {
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return nil, nil
	}
	if !CapabilitiesForBackend(m.backend).Features.VMSnapshot {
		return nil, nil
	}
	vmSnapshotter, ok := m.backend.(VMSnapshotter)
	if !ok {
		return nil, fmt.Errorf("backend %s advertises VM snapshots but does not implement them", m.backend.Name())
	}
	inst := m.findInstanceBySandboxID(sandboxID)
	if inst == nil || inst.Status != StatusRunning {
		return nil, nil
	}
	manifest, err := vmSnapshotter.SaveVMSnapshot(ctx, sandboxID, inst.Config.TenantID, inst.AgentID, store, trigger)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "trigger", trigger, "error", err.Error()).
			Warn("sandbox_manager: R2 snapshot failed")
		return nil, err
	}
	return manifest, nil
}

// RestoreFromR2Snapshot attempts to rehydrate a sandbox on this host
// from a previously-saved object-storage snapshot. Used by the agents
// reconciler when GetLinked has exhausted in-memory + Redis + Postgres
// + backend probe — this is the last layer before declaring the
// sandbox truly lost.
//
// Returns (nil, nil) when:
//   - The feature is disabled (no snapshot store wired)
//   - The backend doesn't implement VMRestorer (e.g. docker, k8s,
//     fcagent-gateway-side — fcagent will get a dedicated path)
//   - No snapshot exists for this sandbox (snapshot.ErrSnapshotMissing)
//
// Callers should treat any of those as "fall through to whatever
// you were going to do next" — they're informational, not failures.
func (m *SandboxManager) RestoreFromR2Snapshot(ctx context.Context, sandboxID, sessionID, tenantID, agentID string) (*Instance, error) {
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return nil, nil
	}
	if !CapabilitiesForBackend(m.backend).Features.VMRestore {
		return nil, nil
	}
	restorer, ok := m.backend.(VMRestorer)
	if !ok {
		return nil, fmt.Errorf("backend %s advertises VM restore but does not implement it", m.backend.Name())
	}
	inst, err := restorer.RestoreVMSnapshot(ctx, sandboxID, tenantID, agentID, store)
	if err != nil {
		if errors.Is(err, snapshot.ErrSnapshotMissing) {
			return nil, nil
		}
		return nil, err
	}
	if inst == nil {
		return nil, nil
	}

	// Register the restored Instance in the in-memory maps so the
	// next GetLinked / Exec call hits cache directly. Use sessionID
	// (the caller's session, typically "trp-<agentID>") if provided;
	// otherwise leave the session map untouched and rely on the
	// session-binding caller (GetLinked) to attach.
	m.mu.Lock()
	m.instancesBySandbox[sandboxID] = inst
	if sessionID != "" {
		m.instances[sessionID] = inst
	}
	if inst.Persistent && agentID != "" {
		m.troopers[agentID] = inst
	}
	m.mu.Unlock()

	// Refresh the registry so the cross-replica routing layer learns
	// about the restored sandbox immediately.
	m.registryPut(ctx, inst)

	logger.WithFields(
		"sandbox_id", sandboxID,
		"session_id", sessionID,
		"agent_id", agentID,
	).Info("sandbox_manager: restored sandbox from R2 snapshot")
	return inst, nil
}

func (m *SandboxManager) snapshotStore() snapshot.Store {
	m.r2SnapshotMu.RLock()
	defer m.r2SnapshotMu.RUnlock()
	if m.r2Snapshots == nil {
		return snapshot.NewDisabled()
	}
	return m.r2Snapshots
}

func (m *SandboxManager) runSnapshotScheduler(ctx context.Context) {
	// Stagger the first sweep so a restart doesn't immediately
	// snapshot every running sandbox at once.
	initialDelay := time.Duration(snapshotPollInterval.Nanoseconds() / 4)
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("sandbox_manager: R2 snapshot scheduler stopped")
			return
		case <-timer.C:
			m.snapshotRunningSandboxes(ctx)
			timer.Reset(snapshotPollInterval)
		}
	}
}

func (m *SandboxManager) snapshotRunningSandboxes(ctx context.Context) {
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return
	}
	if !CapabilitiesForBackend(m.backend).Features.VMSnapshot {
		return
	}

	// Take a defensive snapshot of the instance list under read lock.
	// We don't hold the lock while snapshotting because each snapshot
	// pauses the VM for hundreds of milliseconds — keeping the manager
	// mutex closed for that long would stall every request.
	m.mu.RLock()
	candidates := make([]string, 0, len(m.instancesBySandbox))
	for id, inst := range m.instancesBySandbox {
		if inst != nil && inst.Status == StatusRunning {
			candidates = append(candidates, id)
		}
	}
	m.mu.RUnlock()

	if len(candidates) == 0 {
		return
	}
	logger.WithFields("count", len(candidates)).
		Info("sandbox_manager: R2 snapshot sweep starting")

	sem := semaphore.NewWeighted(snapshotConcurrency)
	var wg sync.WaitGroup
	for _, id := range candidates {
		id := id
		if err := sem.Acquire(ctx, 1); err != nil {
			break // ctx cancelled
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer sem.Release(1)
			// Give each snapshot a generous timeout — uploading a
			// multi-GB rootfs over a slow link is the worst case.
			snapCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()
			if _, err := m.SaveR2Snapshot(snapCtx, id, "periodic"); err != nil && !errors.Is(err, context.Canceled) {
				logger.WithFields("sandbox_id", id, "error", err.Error()).
					Warn("sandbox_manager: periodic snapshot failed")
			}
		}()
	}
	wg.Wait()
	logger.Info("sandbox_manager: R2 snapshot sweep complete")
}
