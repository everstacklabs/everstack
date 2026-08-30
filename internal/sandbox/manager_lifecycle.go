package sandbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/edition"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

var (
	// ErrLifecycleTransitionRejected indicates the state transition could not be
	// claimed or finalized due to an invalid/raced current lifecycle state.
	ErrLifecycleTransitionRejected = errors.New("sandbox lifecycle transition rejected")
)

// Lifecycle state constants.
const (
	LifecycleRunning          = "running"
	LifecycleRepoProvisioning = "repo_provisioning"
	LifecycleStopping         = "stopping"
	LifecycleStopped          = "stopped"
	LifecycleReviving         = "reviving"
	LifecycleTerminating      = "terminating"
	LifecycleTerminated       = "terminated"
	LifecycleFailed           = "failed"
)

// delegateLifecycleToReconciler reports whether user-facing lifecycle
// mutations should write desired_state and let the reconciler converge,
// instead of running the legacy synchronous claim cascade below. Under
// the reconciler every caller of StopSandbox/ReviveSandbox/
// TerminateSandbox (CLI server, trooper wake, agent reprovision) is
// transparently routed through desired-state, so there is exactly one
// lifecycle writer in the system.
func (m *SandboxManager) delegateLifecycleToReconciler() bool {
	return ReconcilerEnabled() && m.db != nil
}

// setDesiredStateSQL writes desired_state and atomically advances
// lifecycle_state from a quiescent state to the matching transitional
// one. MUST stay in lockstep with the reconciler repository's
// SetDesiredState (internal/orchestrator/sandbox/repository.go); the
// SQL is duplicated here because importing the orchestrator package
// from the manager would create an import cycle.
func (m *SandboxManager) setDesiredStateSQL(ctx context.Context, sandboxID, desired string) error {
	res, err := m.db.ExecContext(ctx, `
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
		WHERE id = $1`, sandboxID, desired)
	if err != nil {
		return fmt.Errorf("set desired state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: sandbox not found", ErrLifecycleTransitionRejected)
	}
	return nil
}

// waitForLifecycleState polls the DB until the sandbox reaches the
// wanted lifecycle_state or the timeout elapses. Used by callers that
// need a converged sandbox before proceeding (trooper wake).
func (m *SandboxManager) waitForLifecycleState(ctx context.Context, sandboxID, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var state string
		if err := m.db.QueryRowxContext(ctx,
			`SELECT COALESCE(lifecycle_state, '') FROM sandbox_instances WHERE id = $1`,
			sandboxID).Scan(&state); err == nil {
			switch state {
			case want:
				return nil
			case LifecycleFailed, "error":
				return fmt.Errorf("sandbox entered %s while waiting for %s", state, want)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for sandbox %s to reach %s", sandboxID, want)
}

// StopSandbox stops a running sandbox: snapshots the workspace, destroys the container,
// but preserves the host-side data dir (repo clone + snapshot) for revival.
//
// Reconciler mode: writes desired_state='sleeping' and returns; the
// reconciler executes the stop (snapshot + destroy) asynchronously.
func (m *SandboxManager) StopSandbox(ctx context.Context, sandboxID string) error {
	if m.db == nil {
		return fmt.Errorf("lifecycle operations require a database connection")
	}
	if m.delegateLifecycleToReconciler() {
		return m.setDesiredStateSQL(ctx, sandboxID, "sleeping")
	}

	// Phase 1: Claim (running → stopping). Some older/self-healed rows may have
	// drift between lifecycle_state and status; handle that gracefully.
	claimRunning := func() error {
		var id string
		return m.db.QueryRowxContext(ctx, `
			UPDATE sandbox_instances
			SET lifecycle_state = 'stopping'
			WHERE id = $1 AND COALESCE(NULLIF(lifecycle_state, ''), 'running') = 'running'
			RETURNING id`, sandboxID).Scan(&id)
	}

	err := claimRunning()
	if errors.Is(err, sql.ErrNoRows) {
		// Backfill missing/drifted DB rows from current in-memory instance, then retry.
		if inst := m.findInstanceBySandboxID(sandboxID); inst != nil {
			m.persistInstance(inst)
			err = claimRunning()
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Legacy recovery: if row says status=running but lifecycle_state drifted,
		// still allow stop-claim unless it's already in a terminal transition.
		var id string
		err = m.db.QueryRowxContext(ctx, `
			UPDATE sandbox_instances
			SET lifecycle_state = 'stopping'
			WHERE id = $1
			  AND status = 'running'
			  AND COALESCE(NULLIF(lifecycle_state, ''), 'running') NOT IN ('stopping','reviving','terminating','terminated')
			RETURNING id`, sandboxID).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Final recovery: trust runtime state for currently running instances and
		// force-claim stop unless another active transition already owns it.
		inst := m.findInstanceBySandboxID(sandboxID)
		if inst != nil && (inst.Status == StatusRunning || inst.LifecycleState == "" || inst.LifecycleState == LifecycleRunning) {
			var id string
			err = m.db.QueryRowxContext(ctx, `
				UPDATE sandbox_instances
				SET lifecycle_state = 'stopping'
				WHERE id = $1
				  AND COALESCE(NULLIF(lifecycle_state, ''), 'running') NOT IN ('stopping','reviving','terminating')
				RETURNING id`, sandboxID).Scan(&id)
		}
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var lifecycleState, status string
			_ = m.db.QueryRowxContext(ctx, `
				SELECT COALESCE(lifecycle_state, ''), COALESCE(status, '')
				FROM sandbox_instances
				WHERE id = $1`, sandboxID).Scan(&lifecycleState, &status)
			logger.WithFields(
				"sandbox_id", sandboxID,
				"lifecycle_state", lifecycleState,
				"status", status,
			).Warn("sandbox_manager: stop claim rejected by lifecycle gate")
			return fmt.Errorf("%w: stop requires running state", ErrLifecycleTransitionRejected)
		}
		return fmt.Errorf("failed to claim stop transition: %w", err)
	}

	// Find the instance to get session ID + container ID. After a gateway
	// pod restart the in-memory cache is empty for any sandbox the gateway
	// didn't directly create; fall back to the DB row so Stop still works.
	// The actual VM lives on fcagent; the DB has enough metadata to drive
	// the transition.
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		// Compensate: revert to running
		m.compensateLifecycle(ctx, sandboxID, LifecycleRunning, LifecycleStopping)
		return err
	}
	wasBillable := !inst.BillingStartedAt.IsZero()

	sessionID := inst.Config.SessionID
	tenantID := inst.Config.TenantID

	// Ensure lifecycle snapshot path uses the same writable data-dir fallback
	// logic as git clone operations.
	if err := m.ensureDataDirWritable(); err != nil {
		m.compensateLifecycle(ctx, sandboxID, LifecycleRunning, LifecycleStopping)
		return fmt.Errorf("failed to prepare sandbox data dir for snapshot: %w", err)
	}

	// Sample network counters while the VM is still alive — Destroy below
	// (Phase 2b) frees them, and metering happens afterward.
	if wasBillable {
		m.captureNetworkBytes(ctx, inst)
	}

	// Phase 2a: Snapshot the workspace → <data-dir>/<sandbox-id>/trooper.tar.gz
	snapshotPath := filepath.Join(m.dataDir(), sandboxID, "trooper.tar.gz")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0755); err != nil {
		m.compensateLifecycle(ctx, sandboxID, LifecycleRunning, LifecycleStopping)
		return fmt.Errorf("failed to create snapshot dir: %w", err)
	}

	snapshotCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Snapshot dispatch order:
	//   1. Backend implements Snapshotter (K8s today; could add for
	//      others if a backend has a dedicated fast path).
	//   2. Universal exec-based snapshotter — runs `tar -czf` inside
	//      the sandbox via Exec, then ReadFile pulls the archive out.
	//      Works on ANY backend that has Exec + ReadFile (fcagent,
	//      firecracker, kubernetes), so persistent troopers can
	//      hibernate regardless of runtime and pool slots / pod
	//      memory release as soon as Destroy fires.
	//   3. Docker cp fallback — only for the legacy docker backend
	//      where we manage containers directly. Kept so single-host
	//      docker-compose dev setups still snapshot correctly.
	var snapshotErr error
	caps := CapabilitiesForBackend(m.backend)
	switch {
	case backendImplementsSnapshotter(m.backend):
		snapshotter := m.backend.(Snapshotter)
		snapshotErr = snapshotter.Snapshot(snapshotCtx, sandboxID, DefaultWorkDir, snapshotPath)
	case caps.Features.DockerCPSnapshot:
		snapshotErr = m.dockerSnapshot(snapshotCtx, inst.ContainerID, snapshotPath)
	default:
		snapshotErr = m.execSnapshot(snapshotCtx, sandboxID, snapshotPath)
	}

	if snapshotErr != nil {
		_ = os.Remove(snapshotPath)
		snapshotPath = ""

		// Check if the container is still alive. If it's gone, the snapshot
		// failure is expected (container already dead) — proceed to finalize
		// without a snapshot rather than reverting to "running" and looping forever.
		if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", snapshotErr.Error()).
				Warn("sandbox_manager: snapshot failed and container is gone; finalizing stop without snapshot")
			goto finalize
		}

		m.compensateLifecycle(ctx, sandboxID, LifecycleRunning, LifecycleStopping)
		return fmt.Errorf("snapshot failed: %w", snapshotErr)
	}

	// Phase 2a.5: R2 full-VM snapshot (host-loss survival). Best-
	// effort — a failure here does not block the stop. If R2 isn't
	// configured or the backend doesn't support VM snapshots, this
	// is a fast no-op. Runs AFTER the workspace tarball so we don't
	// double-pause the VM, and BEFORE Destroy so the VM is still
	// alive to be snapshotted.
	if _, err := m.SaveR2Snapshot(ctx, sandboxID, "sleep"); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: R2 snapshot on sleep failed; continuing stop")
	}

	// Phase 2b: Destroy the Docker container
	if err := m.backend.Destroy(ctx, sandboxID); err != nil {
		// Container destroy failed but snapshot exists — retry destroy
		retryErr := m.backend.Destroy(ctx, sandboxID)
		if retryErr != nil {
			// If backend no longer reports the sandbox, treat container as gone and
			// continue finalization with the snapshot we already captured.
			if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr != nil {
				logger.WithFields("sandbox_id", sandboxID).
					Warn("sandbox_manager: destroy retries failed but sandbox no longer exists; continuing stop finalization")
			} else {
				_ = os.Remove(snapshotPath)
				m.compensateLifecycle(ctx, sandboxID, LifecycleRunning, LifecycleStopping)
				return fmt.Errorf("failed to destroy container (snapshot rolled back): %w", retryErr)
			}
		}
	}

finalize:
	// Phase 3: Finalize (stopping → stopped)
	revivableUntil := m.resolveRevivableUntil(tenantID)
	stoppedAt := time.Now()
	if wasBillable {
		if !m.recordUsageForInstance(ctx, inst, EventSandboxStopped, "stopped", stoppedAt) {
			return fmt.Errorf("failed to close sandbox compute billing window")
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}

	// Zero time = no expiration, stored as NULL so the reaper skips it.
	var revivableParam interface{}
	if !revivableUntil.IsZero() {
		revivableParam = revivableUntil
	}
	res, err := m.db.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET lifecycle_state = 'stopped', status = 'stopped', stopped_at = NOW(),
		    workspace_snapshot_ref = $2, revivable_until = $3,
		    billing_started_at = NULL, billing_ended_at = NULL
		WHERE id = $1 AND lifecycle_state = 'stopping'`,
		sandboxID, snapshotPath, revivableParam)
	if err != nil {
		return fmt.Errorf("failed to finalize stop: %w", err)
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr != nil || rows == 0 {
		return fmt.Errorf("%w: stop finalize rejected", ErrLifecycleTransitionRejected)
	}

	// Update in-memory state
	m.mu.Lock()
	if inst, ok := m.instances[sessionID]; ok {
		inst.Status = StatusStopped
		inst.LifecycleState = LifecycleStopped
		inst.TrooperSnapshotRef = snapshotPath
		inst.RevivableUntil = revivableUntil
		inst.StoppedAt = stoppedAt
		m.instancesBySandbox[inst.ID] = inst
		// Persistent troopers: keep ref in troopers map (now stopped)
		if inst.Persistent && inst.AgentID != "" {
			m.troopers[inst.AgentID] = inst
		}
	}
	m.mu.Unlock()

	// Close port mappings
	m.closeAllPortMappings(sandboxID)

	m.recordEvent(sandboxID, sessionID, tenantID, EventSandboxStopped, "Sandbox stopped", map[string]interface{}{
		"snapshot": snapshotPath,
	}, nil, "")
	if m.db != nil {
		// Keep trooper read-model status aligned with automatic lifecycle stop.
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers
			SET status = 'sleeping', updated_at = NOW()
			WHERE sandbox_id = $1 AND deleted_at IS NULL
		`, sandboxID)

		// Update agent lifecycle_status so the frontend shows the correct state.
		if inst != nil && inst.Persistent && inst.AgentID != "" {
			_, _ = m.db.ExecContext(ctx, `
				UPDATE agent_definitions SET lifecycle_status = 'sleeping', updated_at = NOW()
				WHERE id = $1
			`, inst.AgentID)
		}
	}

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_manager: sandbox stopped")
	return nil
}

// ReviveSandbox restores a stopped sandbox from its trooper snapshot.
//
// Reconciler mode: writes desired_state='running', waits for the
// reconciler to converge the row to running (callers exec into the
// sandbox immediately after), and returns the live instance.
func (m *SandboxManager) ReviveSandbox(ctx context.Context, sandboxID string) (*Instance, error) {
	if m.db == nil {
		return nil, fmt.Errorf("lifecycle operations require a database connection")
	}
	savedConfigForBilling, _, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load sandbox billing identity: %w", err)
	}
	if err := m.RequireSandboxBilling(savedConfigForBilling.TenantID); err != nil {
		return nil, err
	}
	if err := m.RequireConcurrentSandboxSlot(ctx, savedConfigForBilling.TenantID, sandboxID); err != nil {
		return nil, err
	}
	if err := m.validateInstanceMachineProfile(*savedConfigForBilling); err != nil {
		return nil, err
	}
	if m.delegateLifecycleToReconciler() {
		if err := m.setDesiredStateSQL(ctx, sandboxID, "running"); err != nil {
			return nil, err
		}
		if err := m.waitForLifecycleState(ctx, sandboxID, LifecycleRunning, 90*time.Second); err != nil {
			return nil, err
		}
		inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
		if err != nil {
			return nil, err
		}
		return inst, nil
	}

	// Phase 1: Claim (stopped → reviving, also check revivable_until).
	// workspace_snapshot_ref is nullable in the schema — most stops happen
	// before any snapshot is taken (or with snapshotting disabled), so the
	// column is NULL for those rows. Scan into sql.NullString so a NULL
	// doesn't error with "converting NULL to string is unsupported".
	var snapshotRef sql.NullString
	err = m.db.QueryRowxContext(ctx, `
		UPDATE sandbox_instances
		SET lifecycle_state = 'reviving', status = 'reviving'
		WHERE id = $1 AND lifecycle_state = 'stopped'
		  AND (revivable_until IS NULL OR revivable_until > NOW())
		RETURNING workspace_snapshot_ref`, sandboxID).Scan(&snapshotRef)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: revive requires stopped and non-expired state", ErrLifecycleTransitionRejected)
		}
		return nil, fmt.Errorf("failed to claim revive transition: %w", err)
	}
	// snapshotRef may be NULL when no snapshot was taken at stop time
	// (snapshotting disabled or async stop that didn't snapshot). The
	// snapshot-restore branch below handles empty as "no snapshot".
	snapshotPath := ""
	if snapshotRef.Valid {
		snapshotPath = snapshotRef.String
	}

	// Get saved config from DB
	savedConfig, sessionID, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		m.compensateLifecycle(ctx, sandboxID, LifecycleStopped, LifecycleReviving)
		return nil, fmt.Errorf("failed to get saved config: %w", err)
	}
	prevInst := m.findInstanceBySandboxID(sandboxID)

	// Phase 2a: Create new container with same config
	inst, err := m.backend.Create(ctx, sandboxID, *savedConfig)
	if err != nil {
		m.compensateLifecycle(ctx, sandboxID, LifecycleStopped, LifecycleReviving)
		return nil, fmt.Errorf("failed to create container for revive: %w", err)
	}
	// Preserve trooper metadata across revive. Backend.Create returns a fresh
	// instance without manager-only fields like Persistent/AgentID.
	if prevInst != nil {
		inst.Persistent = prevInst.Persistent
		if strings.TrimSpace(inst.AgentID) == "" {
			inst.AgentID = strings.TrimSpace(prevInst.AgentID)
		}
		if inst.IdleRetentionSecs == 0 && prevInst.IdleRetentionSecs > 0 {
			inst.IdleRetentionSecs = prevInst.IdleRetentionSecs
		}
		inst.KeepWarm = prevInst.KeepWarm
	}
	if strings.TrimSpace(inst.AgentID) == "" {
		inst.AgentID = strings.TrimSpace(savedConfig.AgentID)
	}
	if strings.TrimSpace(inst.AgentID) != "" {
		inst.Persistent = true
	}
	m.seedRouteFromInstance(inst)

	// Phase 2b: Restore the workspace from snapshot tarball
	//
	// The snapshot is a gzipped tar created by `docker cp container:/workspace -`
	// which produces entries rooted at `workspace/`. The container's /workspace is
	// a tmpfs mount, so `docker cp - container:/` can fail when it tries to
	// overwrite the mount point directory entry. Instead, we decompress and pipe
	// directly into `docker exec tar` which extracts inside the running container,
	// using --strip-components=1 to drop the leading `workspace/` prefix and write
	// files directly into /workspace.
	if snapshotPath != "" {
		if _, statErr := os.Stat(snapshotPath); statErr != nil {
			logger.WithFields("sandbox_id", sandboxID, "snapshot", snapshotPath).
				Warn("sandbox_manager: snapshot file missing, reviving without trooper restore")
		} else {
			restoreCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			caps := CapabilitiesForBackend(m.backend)

			// Mirror the snapshot dispatch so revive uses the same
			// path the stop wrote with. Mismatched paths leave the
			// archive irretrievable (e.g. fcagent stop wrote via
			// execSnapshot, revive then tries dockerRestore which
			// can't find the container).
			var restoreErr error
			switch {
			case backendImplementsSnapshotter(m.backend):
				snapshotter := m.backend.(Snapshotter)
				restoreErr = snapshotter.Restore(restoreCtx, sandboxID, snapshotPath, DefaultWorkDir)
			case caps.Features.DockerCPSnapshot:
				restoreErr = m.dockerRestore(restoreCtx, inst.ContainerID, snapshotPath)
			default:
				restoreErr = m.execRestore(restoreCtx, sandboxID, snapshotPath)
			}
			if restoreErr != nil {
				m.compensateRevive(ctx, sandboxID, inst)
				return nil, fmt.Errorf("restore failed: %w", restoreErr)
			}
		}
	}

	// Phase 2c: If git-imported, verify repo still exists and SHA matches
	if savedConfig.RepoHostPath != "" {
		repoDir := savedConfig.RepoHostPath
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			m.compensateRevive(ctx, sandboxID, inst)
			return nil, fmt.Errorf("repo directory missing: %s", repoDir)
		}

		shaCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
		shaOutput, shaErr := shaCmd.Output()
		if shaErr != nil {
			m.compensateRevive(ctx, sandboxID, inst)
			return nil, fmt.Errorf("failed to read HEAD SHA for verification: %w", shaErr)
		}
		actualSHA := strings.TrimSpace(string(shaOutput))
		if savedConfig.GitCommitSHA != "" && actualSHA != savedConfig.GitCommitSHA {
			m.compensateRevive(ctx, sandboxID, inst)
			return nil, fmt.Errorf("repo SHA mismatch: expected %s, got %s", savedConfig.GitCommitSHA, actualSHA)
		}
	}

	// Phase 3: Finalize (reviving → running)
	billingStartedAt := time.Now().UTC()
	inst.BillingStartedAt = billingStartedAt
	inst.BillingEndedAt = time.Time{}
	res, err := m.db.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET lifecycle_state = 'running', status = 'running', container_id = $2,
		    agent_target = COALESCE($3, agent_target),
		    stopped_at = NULL, workspace_snapshot_ref = NULL,
		    billing_started_at = $4, billing_ended_at = NULL,
		    last_used_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND lifecycle_state = 'reviving'`,
		sandboxID, inst.ContainerID, nullableString(strings.TrimSpace(inst.AgentTarget)), billingStartedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to finalize revive: %w", err)
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr != nil || rows == 0 {
		m.compensateRevive(ctx, sandboxID, inst)
		return nil, fmt.Errorf("%w: revive finalize rejected", ErrLifecycleTransitionRejected)
	}

	// Update in-memory
	inst.LifecycleState = LifecycleRunning
	inst.Status = StatusRunning
	inst.LastUsedAt = time.Now()
	inst.Name = savedConfig.Name

	m.mu.Lock()
	m.instances[sessionID] = inst
	m.instancesBySandbox[inst.ID] = inst
	// Persistent troopers: update trooper ref to revived instance
	if inst.Persistent && inst.AgentID != "" {
		m.troopers[inst.AgentID] = inst
	}
	m.mu.Unlock()
	m.persistInstance(inst)

	m.recordEvent(sandboxID, sessionID, savedConfig.TenantID, EventSandboxRevived, "Sandbox revived", nil, nil, "")
	if m.db != nil {
		// Keep trooper read-model status aligned with automatic/on-demand revive.
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers
			SET status = 'running', updated_at = NOW()
			WHERE sandbox_id = $1 AND deleted_at IS NULL
		`, sandboxID)
	}

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_manager: sandbox revived")
	return inst, nil
}

// TerminateSandbox permanently destroys a sandbox and all its data.
//
// Reconciler mode: writes desired_state='terminated' and returns; the
// reconciler executes the destroy + data cleanup asynchronously.
// Idempotent like the legacy path: terminating an already-terminated
// sandbox is a no-op success.
func (m *SandboxManager) TerminateSandbox(ctx context.Context, sandboxID string) error {
	if m.db == nil {
		return fmt.Errorf("lifecycle operations require a database connection")
	}
	if m.delegateLifecycleToReconciler() {
		err := m.setDesiredStateSQL(ctx, sandboxID, "terminated")
		if errors.Is(err, ErrLifecycleTransitionRejected) {
			// Row missing entirely: fall through to the legacy best-effort
			// cleanup so terminate stays usable for orphaned runtimes.
			return m.terminateWithoutLifecycleRow(ctx, sandboxID)
		}
		return err
	}

	// Phase 1: Claim (running/stopped/failed → terminating)
	var (
		prevState        string
		sessionID        string
		tenantID         string
		backend          string
		billingStartedAt sql.NullTime
		configRaw        []byte
		agentID          sql.NullString
	)
	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin terminate transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowxContext(ctx, `
		SELECT lifecycle_state, session_id, tenant_id, backend, billing_started_at, config, agent_id
		FROM sandbox_instances
		WHERE id = $1
		FOR UPDATE`, sandboxID).Scan(&prevState, &sessionID, &tenantID, &backend, &billingStartedAt, &configRaw, &agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// DB row can be missing when instance persistence failed or schema is stale.
			// Fall back to best-effort runtime/backend cleanup so terminate remains usable.
			return m.terminateWithoutLifecycleRow(ctx, sandboxID)
		}
		return fmt.Errorf("failed to load lifecycle state for terminate: %w", err)
	}
	// Already terminated — terminate is idempotent; do nothing and return success.
	// This branch used to DELETE the record (and child rows) so a second call
	// from the UI doubled as "purge". That conflated two distinct operations
	// and made the user-facing Terminate destructive on retry. Purging a
	// terminated record now lives in PurgeTerminatedSandbox, called from a
	// separate UI action with its own data-loss confirmation.
	//
	// Stuck-in-terminating rows used to fall into the same DELETE branch;
	// those are now also a no-op here. The reaper / orchestrator handles
	// driving terminating → terminated; user-initiated retries should not
	// race with that and lose data.
	if prevState == LifecycleTerminated || prevState == LifecycleTerminating {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit terminate no-op: %w", err)
		}
		logger.WithFields("sandbox_id", sandboxID, "prev_state", prevState).
			Info("sandbox_manager: terminate is a no-op for already-terminated sandbox")
		return nil
	}

	if prevState != LifecycleRunning && prevState != LifecycleStopped && prevState != LifecycleFailed {
		return fmt.Errorf("%w: terminate requires running/stopped/failed state", ErrLifecycleTransitionRejected)
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET lifecycle_state = 'terminating', status = 'terminating'
		WHERE id = $1 AND lifecycle_state = $2`, sandboxID, prevState); err != nil {
		return fmt.Errorf("failed to claim terminate transition: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit terminate claim: %w", err)
	}

	// Phase 2: Always destroy the backend resource regardless of prev state.
	// Stop normally destroys the container, but the stop path has branches
	// where destroy is skipped (snapshot succeeded but backend reported the
	// sandbox as missing). A `failed` row may also still own a live
	// container/VM. Calling Destroy unconditionally guarantees the slot
	// (CPU/memory/disk/IP) is released back to the host. Destroy is
	// expected to be idempotent — backends should treat "already gone"
	// as a no-op and not return an error in that case.
	if destroyErr := m.backend.Destroy(ctx, sandboxID); destroyErr != nil {
		if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr == nil {
			return fmt.Errorf("failed to destroy sandbox during terminate: %w", destroyErr)
		}
		logger.WithFields("sandbox_id", sandboxID, "prev_state", prevState, "error", destroyErr.Error()).
			Warn("sandbox_manager: destroy errored but backend confirms sandbox is gone")
	}
	terminatedAt := time.Now()
	if billingStartedAt.Valid {
		cfg, parseErr := parseInstanceConfig(configRaw)
		if parseErr != nil {
			return fmt.Errorf("parse sandbox config for usage metering on terminate: %w", parseErr)
		}
		if !m.recordUsageSnapshot(
			ctx, sandboxID, sessionID, tenantID, backend, cfg,
			billingStartedAt.Time, terminatedAt,
			EventSandboxTerminated, "terminated", 0, 0,
		) {
			return fmt.Errorf("failed to close sandbox compute billing window")
		}
	}

	// Phase 3: Delete data directory
	m.cleanupSandboxData(sandboxID)

	// Phase 4: Finalize (terminating → terminated)
	res, err := m.db.ExecContext(ctx, `
		UPDATE sandbox_instances
		SET lifecycle_state = 'terminated', status = 'terminated', destroyed_at = NOW(),
		    destroy_reason = 'terminated', billing_started_at = NULL, billing_ended_at = NULL
		WHERE id = $1 AND lifecycle_state = 'terminating'`,
		sandboxID)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to finalize terminate (will retry on next sweep)")
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr != nil || rows == 0 {
		return fmt.Errorf("%w: terminate finalize rejected", ErrLifecycleTransitionRejected)
	}

	// Clean up in-memory
	m.mu.Lock()
	for sid, inst := range m.instances {
		if inst.ID == sandboxID {
			delete(m.instances, sid)
			delete(m.instancesBySandbox, sandboxID)
			// Remove from persistent troopers map
			if inst.Persistent && inst.AgentID != "" {
				delete(m.troopers, inst.AgentID)
			}
			break
		}
	}
	// Also check troopers map directly (trooper may have been stopped with no session)
	for agentID, inst := range m.troopers {
		if inst.ID == sandboxID {
			delete(m.troopers, agentID)
			break
		}
	}
	m.mu.Unlock()

	m.closeAllPortMappings(sandboxID)

	// Cascade: when this sandbox is bound to a persistent agent (trooper),
	// flip the agent's lifecycle_status to 'terminated' so it stops being
	// scheduled / woken / treated as live. The agent_definitions row is
	// preserved (only the status changes) — terminate must never delete
	// the agent record. The sandbox row is also preserved with
	// lifecycle_state='terminated'; a separate Delete action purges both.
	if agentID.Valid && agentID.String != "" {
		if _, agentErr := m.db.ExecContext(ctx, `
			UPDATE agent_definitions
			SET lifecycle_status = 'terminated', updated_at = NOW()
			WHERE id = $1 AND lifecycle_status NOT IN ('terminated')`,
			agentID.String); agentErr != nil {
			logger.WithFields("sandbox_id", sandboxID, "agent_id", agentID.String, "error", agentErr.Error()).
				Warn("sandbox_manager: failed to cascade terminate to agent; sandbox is already terminated")
		} else {
			logger.WithFields("sandbox_id", sandboxID, "agent_id", agentID.String).
				Info("sandbox_manager: cascaded terminate to associated agent")
		}
	}

	m.recordEvent(sandboxID, sessionID, tenantID, EventSandboxTerminated, "Sandbox terminated", nil, nil, "")

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_manager: sandbox terminated")
	return nil
}

// PurgeTerminatedSandbox permanently deletes a terminated sandbox row and all
// its dependent records. This is the explicit "delete" action exposed to the
// UI; it requires the row already be in lifecycle_state='terminated' so a
// purge cannot accidentally destroy a live sandbox.
//
// This used to be a side-effect of calling TerminateSandbox a second time —
// which made the user-facing terminate destructive on retry. The two
// operations are now strictly separate: TerminateSandbox is idempotent,
// PurgeTerminatedSandbox is the only path that DELETEs.
func (m *SandboxManager) PurgeTerminatedSandbox(ctx context.Context, sandboxID string) error {
	if m.db == nil {
		return fmt.Errorf("lifecycle operations require a database connection")
	}

	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin purge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var lifecycleState string
	if err := tx.QueryRowxContext(ctx,
		`SELECT lifecycle_state FROM sandbox_instances WHERE id = $1 FOR UPDATE`,
		sandboxID,
	).Scan(&lifecycleState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Already gone — purge is idempotent.
			return nil
		}
		return fmt.Errorf("failed to load lifecycle state for purge: %w", err)
	}

	if lifecycleState != LifecycleTerminated {
		return fmt.Errorf("%w: purge requires terminated state (was %q); call TerminateSandbox first",
			ErrLifecycleTransitionRejected, lifecycleState)
	}

	for _, child := range []string{
		"sandbox_events",
		"sandbox_executions",
		"sandbox_crons",
		"sandbox_webhooks",
	} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+child+` WHERE sandbox_id = $1`, sandboxID); err != nil {
			return fmt.Errorf("failed to delete from %s: %w", child, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sandbox_instances WHERE id = $1`, sandboxID); err != nil {
		return fmt.Errorf("failed to delete sandbox record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit sandbox record deletion: %w", err)
	}

	// Belt-and-braces in-memory cleanup. The terminate path already removes
	// the entry, but if the row was purged from a stale state (e.g., reaper
	// finalized terminated while the in-memory map kept a reference) this
	// drops it.
	m.mu.Lock()
	for sid, inst := range m.instances {
		if inst.ID == sandboxID {
			delete(m.instances, sid)
			delete(m.instancesBySandbox, sandboxID)
			break
		}
	}
	m.mu.Unlock()

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_manager: purged terminated sandbox record")
	return nil
}

// terminateWithoutLifecycleRow performs best-effort termination when the lifecycle
// row is missing from sandbox_instances.
func (m *SandboxManager) terminateWithoutLifecycleRow(ctx context.Context, sandboxID string) error {
	resolvedID := sandboxID
	foundRuntime := false
	var runtimeInst *Instance

	// Support callers accidentally passing session_id instead of sandbox_id.
	m.mu.RLock()
	if inst, ok := m.instances[sandboxID]; ok && inst != nil && inst.ID != "" {
		resolvedID = inst.ID
		foundRuntime = true
		runtimeInst = inst
	}
	if inst, ok := m.instancesBySandbox[resolvedID]; ok {
		foundRuntime = true
		runtimeInst = inst
	}
	m.mu.RUnlock()

	// Try runtime map by prefixed sandbox ID when caller passed an unprefixed value.
	if !strings.HasPrefix(resolvedID, "sbx_") {
		candidate := "sbx_" + resolvedID
		m.mu.RLock()
		if inst, ok := m.instancesBySandbox[candidate]; ok {
			resolvedID = candidate
			foundRuntime = true
			runtimeInst = inst
		}
		m.mu.RUnlock()
	}

	if runtimeInst != nil && !runtimeInst.BillingStartedAt.IsZero() {
		m.captureNetworkBytes(ctx, runtimeInst)
	}

	// Best-effort container teardown. If status also fails, container is already gone.
	if destroyErr := m.backend.Destroy(ctx, resolvedID); destroyErr != nil {
		if _, statusErr := m.backend.Status(ctx, resolvedID); statusErr == nil {
			return fmt.Errorf("failed to terminate sandbox %s: %w", resolvedID, destroyErr)
		}
		if !foundRuntime {
			return fmt.Errorf("%w: sandbox not found", ErrLifecycleTransitionRejected)
		}
		logger.WithFields("sandbox_id", resolvedID, "error", destroyErr.Error()).
			Warn("sandbox_manager: terminate fallback destroy failed but sandbox is already absent in backend")
	}
	if runtimeInst != nil && !runtimeInst.BillingStartedAt.IsZero() {
		if !m.recordUsageForInstance(ctx, runtimeInst, EventSandboxTerminated, "terminated_without_lifecycle_row", time.Now().UTC()) {
			return fmt.Errorf("failed to close sandbox compute billing window")
		}
		runtimeInst.BillingStartedAt = time.Time{}
		runtimeInst.BillingEndedAt = time.Time{}
	}

	m.cleanupSandboxData(resolvedID)
	m.closeAllPortMappings(resolvedID)
	m.disableCronsWebhooks(resolvedID)

	// Clean in-memory references.
	m.mu.Lock()
	delete(m.instancesBySandbox, resolvedID)
	for sid, inst := range m.instances {
		if sid == sandboxID || (inst != nil && inst.ID == resolvedID) {
			delete(m.instances, sid)
		}
	}
	m.mu.Unlock()

	m.recordEvent(resolvedID, "", "", EventSandboxTerminated, "Sandbox terminated", map[string]interface{}{
		"fallback": "missing_lifecycle_row",
	}, nil, "")

	logger.WithFields("sandbox_id", resolvedID).
		Warn("sandbox_manager: terminated sandbox without lifecycle DB row")
	return nil
}

// GetBySandboxID returns the instance for a given sandbox ID.
func (m *SandboxManager) GetBySandboxID(sandboxID string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instancesBySandbox[sandboxID]
	return inst, ok
}

// GetBySandboxIDOrName returns the instance matching a sandbox ID or name.
// Tries exact ID match first, then falls back to name search.
//
// SECURITY: this lookup is NOT tenant-scoped — the in-memory map holds every
// tenant/instance's sandboxes in a shared gateway process, and the name match
// is "first running wins" across all of them. Callers that serve an
// authenticated request MUST use GetBySandboxIDOrNameScoped so a caller cannot
// resolve another tenant/instance's sandbox by id or by a colliding name. This
// unscoped form is retained only for internal/system callers that have no
// tenant context (reconciler, sweepers).
func (m *SandboxManager) GetBySandboxIDOrName(idOrName string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Exact ID match
	if inst, ok := m.instancesBySandbox[idOrName]; ok {
		return inst, true
	}
	// Name match (first running instance wins)
	for _, inst := range m.instancesBySandbox {
		if inst.Name == idOrName && (inst.LifecycleState == "" || inst.LifecycleState == LifecycleRunning) {
			return inst, true
		}
	}
	return nil, false
}

// SeedInstancesForTest replaces the in-memory instance map. It exists so other
// packages' tests can exercise the tenant-scoped lookups without standing up a
// full manager. Not for production use.
func (m *SandboxManager) SeedInstancesForTest(instances map[string]*Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instancesBySandbox = instances
}

// GetBySandboxIDOrNameScoped is the tenant-scoped form of GetBySandboxIDOrName.
// It only returns an instance whose Config.TenantID equals tenantID, so a
// caller authenticated for one tenant/instance cannot resolve another's
// sandbox — neither by exact id nor by a colliding name. The name match
// remains "first running wins", but only among instances in the caller's
// tenant, so collisions across tenants are no longer reachable.
//
// In cloud multi-instance mode the context tenant_id is the instance_id, so
// scoping by it enforces instance isolation; in self-hosted it is the org id.
//
// If tenantID is empty the lookup fails closed (returns false): an
// authenticated request must carry a resolved tenant, and an empty value must
// never match a row whose tenant is also empty.
func (m *SandboxManager) GetBySandboxIDOrNameScoped(idOrName, tenantID string) (*Instance, bool) {
	if tenantID == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Exact ID match, scoped to the caller's tenant.
	if inst, ok := m.instancesBySandbox[idOrName]; ok && inst.Config.TenantID == tenantID {
		return inst, true
	}
	// Name match (first running instance wins) within the caller's tenant.
	for _, inst := range m.instancesBySandbox {
		if inst.Config.TenantID != tenantID {
			continue
		}
		if inst.Name == idOrName && (inst.LifecycleState == "" || inst.LifecycleState == LifecycleRunning) {
			return inst, true
		}
	}
	return nil, false
}

// GetBySandboxIDOrNameInScope is the canonical scope-shaped wrapper for public
// callers. It delegates to the legacy tenant-scoped lookup using the current
// sandbox table owner (`tenant_id`, or instance_id in cloud deployments).
func (m *SandboxManager) GetBySandboxIDOrNameInScope(idOrName string, scope TenantInstanceScope) (*Instance, bool) {
	return m.GetBySandboxIDOrNameScoped(idOrName, scope.SandboxTenantID())
}

// findInstanceBySandboxID returns the instance for a sandbox ID (unlocked, for internal use).
func (m *SandboxManager) findInstanceBySandboxID(sandboxID string) *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instancesBySandbox[sandboxID]
}

// findInstanceBySandboxIDWithDBFallback returns the in-memory instance if
// present; otherwise reconstructs a minimal Instance from the DB row so
// lifecycle ops (Stop, Revive, Terminate) keep working after a gateway pod
// restart drops the in-memory cache. The actual VM state lives on fcagent —
// the DB row + a backend Status probe is enough to drive transitions.
//
// The returned Instance is not added to m.instances; the caller is expected
// to be a transition-handler that's about to mutate state anyway. The
// background restoreInstances loop will repopulate m.instances properly.
func (m *SandboxManager) findInstanceBySandboxIDWithDBFallback(ctx context.Context, sandboxID string) (*Instance, error) {
	if inst := m.findInstanceBySandboxID(sandboxID); inst != nil {
		return inst, nil
	}
	if m.db == nil {
		return nil, fmt.Errorf("sandbox %s not found in memory and no DB to fall back on", sandboxID)
	}

	cfg, _, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("sandbox %s not found: %w", sandboxID, err)
	}

	// Pull the surrounding row fields the lifecycle path reads.
	var row struct {
		ContainerID    sql.NullString `db:"container_id"`
		Backend        string         `db:"backend"`
		Status         string         `db:"status"`
		LifecycleState sql.NullString `db:"lifecycle_state"`
		CreatedAt      time.Time      `db:"created_at"`
		BillingStarted sql.NullTime   `db:"billing_started_at"`
		BillingEnded   sql.NullTime   `db:"billing_ended_at"`
		Name           string         `db:"name"`
		AgentID        sql.NullString `db:"agent_id"`
		Persistent     sql.NullBool   `db:"persistent"`
	}
	const q = `
		SELECT container_id, backend, status, lifecycle_state, created_at, billing_started_at, billing_ended_at, name, agent_id, persistent
		FROM sandbox_instances WHERE id = $1`
	if err := m.db.GetContext(ctx, &row, q, sandboxID); err != nil {
		return nil, fmt.Errorf("sandbox %s row lookup failed: %w", sandboxID, err)
	}

	inst := &Instance{
		ID:               sandboxID,
		ContainerID:      row.ContainerID.String,
		Backend:          row.Backend,
		Status:           Status(row.Status),
		LifecycleState:   row.LifecycleState.String,
		CreatedAt:        row.CreatedAt,
		BillingStartedAt: nullTimeValue(row.BillingStarted),
		BillingEndedAt:   nullTimeValue(row.BillingEnded),
		Config:           *cfg,
		Name:             row.Name,
		AgentID:          row.AgentID.String,
		Persistent:       row.Persistent.Bool,
	}
	if inst.Name == "" {
		inst.Name = cfg.Name
	}
	return inst, nil
}

// compensateLifecycle reverts a lifecycle state on failure.
// When expectedCurrent is provided, compensation only applies if the current
// lifecycle_state matches one of those values.
func (m *SandboxManager) compensateLifecycle(ctx context.Context, sandboxID, targetState string, expectedCurrent ...string) {
	q := `
		UPDATE sandbox_instances
		SET lifecycle_state = $2
		WHERE id = $1`
	args := []interface{}{sandboxID, targetState}
	if len(expectedCurrent) > 0 {
		q += ` AND lifecycle_state = ANY($3)`
		args = append(args, expectedCurrent)
	}

	_, err := m.db.ExecContext(ctx, q, args...)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "target_state", targetState, "error", err.Error()).
			Error("sandbox_manager: lifecycle compensation failed")
	}
}

// compensateRevive handles cleanup on revive failure: destroy new container, revert to stopped.
func (m *SandboxManager) compensateRevive(ctx context.Context, sandboxID string, inst *Instance) {
	if inst != nil {
		_ = m.backend.Destroy(ctx, sandboxID)
	}
	m.compensateLifecycle(ctx, sandboxID, LifecycleStopped, LifecycleReviving)
}

// resolveRevivableUntil calculates when a stopped sandbox should be
// auto-terminated. A zero time means no expiration (stored as NULL, which the
// reaper never terminates) — used for plans whose SESSION_RETENTION_DAYS is
// -1 and for dev builds.
func (m *SandboxManager) resolveRevivableUntil(tenantID string) time.Time {
	if resolver, ok := m.retentionResolver.(StopRetentionResolver); ok && resolver != nil {
		d := resolver.ResolveStopRetention(tenantID)
		if d == 0 {
			return time.Time{} // no expiration
		}
		if d > 0 {
			return time.Now().Add(d)
		}
	}
	// No stop-retention resolver wired: dev builds keep sandboxes forever,
	// everything else gets CE (free plan) retention.
	if edition.IsDev() {
		return time.Time{}
	}
	return time.Now().Add(ResolveStopRetention("free"))
}

// backendImplementsSnapshotter returns true if the manager's backend
// has a native Snapshotter implementation. Just a typed wrapper so the
// dispatch reads cleanly.
func backendImplementsSnapshotter(b Backend) bool {
	_, ok := b.(Snapshotter)
	return ok
}

// execSnapshot captures the workspace from inside the sandbox by running
// `tar -czf` over Exec and then ReadFile-ing the archive out. Works on
// any backend that supports those two operations — fcagent, firecracker,
// kubernetes — without needing a per-backend Snapshotter.
//
// Memory shape: ReadFile returns a []byte, so the entire archive lives
// in gateway memory once before writing to disk. For typical /workspace
// workspaces (<200MB compressed) this is fine; if/when sandboxes start
// carrying multi-GB workspaces we'd swap this for a streaming variant
// (a new ReadFileStream RPC). The size guard below caps it at 1GB so a
// runaway workspace can't OOM the gateway pod.
func (m *SandboxManager) execSnapshot(ctx context.Context, sandboxID, destPath string) error {
	const archivePath = "/tmp/everstack_snapshot.tar.gz"
	const maxSnapshotBytes = 1 << 30 // 1GB

	// Step 1: tar+gzip the workspace inside the sandbox.
	res, err := m.backend.Exec(ctx, sandboxID, ExecRequest{
		Command: []string{"sh", "-lc", fmt.Sprintf("tar -czf %s -C / workspace && stat -c %%s %s", archivePath, archivePath)},
		Timeout: 90 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("exec snapshot tar: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tar exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	sizeStr := strings.TrimSpace(res.Stdout)
	if sz, _ := strconv.ParseInt(sizeStr, 10, 64); sz > maxSnapshotBytes {
		// Try to clean up the runaway archive inside the sandbox.
		_, _ = m.backend.Exec(ctx, sandboxID, ExecRequest{
			Command: []string{"rm", "-f", archivePath},
			Timeout: 5 * time.Second,
		})
		return fmt.Errorf("snapshot too large: %d bytes (cap %d)", sz, maxSnapshotBytes)
	}

	// Step 2: ReadFile pulls the archive bytes out of the sandbox.
	data, err := m.backend.ReadFile(ctx, sandboxID, archivePath)
	if err != nil {
		return fmt.Errorf("readfile snapshot: %w", err)
	}

	// Step 3: best-effort cleanup of the in-sandbox archive so the
	// next snapshot has a clean slate. Failures are logged-only.
	go func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = m.backend.Exec(cleanCtx, sandboxID, ExecRequest{
			Command: []string{"rm", "-f", archivePath},
			Timeout: 5 * time.Second,
		})
	}()

	// Step 4: write to host disk.
	if err := os.WriteFile(destPath, data, 0o600); err != nil {
		return fmt.Errorf("write snapshot file: %w", err)
	}
	return nil
}

// execRestore restores a tar.gz snapshot into the sandbox by writing
// the archive to /tmp via WriteFile and unpacking it via Exec.
// Counterpart to execSnapshot; works on the same set of backends.
func (m *SandboxManager) execRestore(ctx context.Context, sandboxID, snapshotPath string) error {
	const archivePath = "/tmp/everstack_restore.tar.gz"

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("read snapshot file: %w", err)
	}
	if err := m.backend.WriteFile(ctx, sandboxID, archivePath, data); err != nil {
		return fmt.Errorf("write archive into sandbox: %w", err)
	}
	res, err := m.backend.Exec(ctx, sandboxID, ExecRequest{
		Command: []string{"sh", "-lc", fmt.Sprintf("tar -xzf %s -C / --no-same-owner --no-same-permissions && rm -f %s", archivePath, archivePath)},
		Timeout: 90 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("exec restore tar: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tar restore exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// dockerSnapshot creates a tar.gz snapshot using `docker cp` piped to `gzip`.
// This is the fallback when the backend doesn't implement Snapshotter.
func (m *SandboxManager) dockerSnapshot(ctx context.Context, containerID string, destPath string) error {
	cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+DefaultWorkDir, "-")
	gzCmd := exec.CommandContext(ctx, "gzip")

	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create snapshot file: %w", err)
	}
	defer outFile.Close()

	pipe, err := cpCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to pipe docker cp: %w", err)
	}
	gzCmd.Stdin = pipe
	gzCmd.Stdout = outFile

	if err := cpCmd.Start(); err != nil {
		return fmt.Errorf("docker cp start failed: %w", err)
	}
	if err := gzCmd.Start(); err != nil {
		_ = cpCmd.Process.Kill()
		return fmt.Errorf("gzip start failed: %w", err)
	}

	cpErr := cpCmd.Wait()
	gzErr := gzCmd.Wait()
	if cpErr != nil || gzErr != nil {
		return fmt.Errorf("docker cp: %v, gzip: %v", cpErr, gzErr)
	}
	return nil
}

// dockerRestore restores a tar.gz snapshot into a container using docker exec + tar.
func (m *SandboxManager) dockerRestore(ctx context.Context, containerID string, snapshotFile string) error {
	gunzipCmd := exec.CommandContext(ctx, "gunzip", "-c", snapshotFile)
	tarCmd := exec.CommandContext(ctx, "docker", "exec", "-i", containerID,
		"tar", "xf", "-", "--strip-components=1", "-C", DefaultWorkDir, "--no-same-owner", "--no-same-permissions")

	pipe, err := gunzipCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to pipe gunzip: %w", err)
	}
	tarCmd.Stdin = pipe

	if err := gunzipCmd.Start(); err != nil {
		return fmt.Errorf("gunzip start failed: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = gunzipCmd.Process.Kill()
		return fmt.Errorf("docker exec tar start failed: %w", err)
	}

	gzErr := gunzipCmd.Wait()
	tarErr := tarCmd.Wait()
	if gzErr != nil || tarErr != nil {
		return fmt.Errorf("gunzip: %v, tar: %v", gzErr, tarErr)
	}
	return nil
}
