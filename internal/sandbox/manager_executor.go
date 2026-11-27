package sandbox

// Executor methods: the side-effect layer the sandbox lifecycle
// reconciler (internal/orchestrator/sandbox) calls to do actual work.
//
// Contract with the reconciler:
//   - These methods perform backend/filesystem/object-storage side
//     effects, in-memory cache updates, usage metering, and event
//     recording. They NEVER write sandbox_instances lifecycle columns
//     (lifecycle_state, status, desired_state, stopped_at, ...); the
//     reconciler's ApplyTransition is the single writer.
//   - Every method is idempotent: re-running a half-completed
//     operation after a lease expiry or leader crash must converge,
//     not double-apply. Destroy treats "already gone" as success;
//     snapshot/restore re-run cleanly.
//   - Errors are returned raw; the reconciler decides between retry
//     with backoff (convergence states) and parking.
//
// The legacy synchronous lifecycle methods in manager_lifecycle.go
// (StopSandbox/ReviveSandbox/TerminateSandbox) remain for deployments
// with the reconciler flag OFF and are scheduled for deletion once the
// reconciler is the sole driver in production.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// ExecuteCreate provisions the backend resource for a pending sandbox
// and registers it in the manager's routing caches. The reconciler owns
// the row; this owns the VM.
func (m *SandboxManager) ExecuteCreate(ctx context.Context, sandboxID string, cfg InstanceConfig) (*Instance, error) {
	m.shadowAdmitCompute(cfg.TenantID, ComputeSurfaceSandboxCreate)
	if err := m.RequireSandboxBilling(cfg.TenantID); err != nil {
		return nil, err
	}
	if err := m.validateInstanceMachineProfile(cfg); err != nil {
		return nil, err
	}
	inst, err := m.backend.Create(ctx, sandboxID, cfg)
	if err != nil {
		return nil, err
	}
	// Backend.Create returns a fresh instance without manager-only
	// fields; carry them over from the config so trooper handling and
	// idle policy see the right flags.
	if strings.TrimSpace(inst.AgentID) == "" {
		inst.AgentID = strings.TrimSpace(cfg.AgentID)
	}
	if inst.AgentID != "" {
		inst.Persistent = true
	}
	// Meter allocated compute from the point provisioning has actually
	// completed. The durable row receives this exact boundary from Step;
	// CreatedAt remains the sandbox identity timestamp.
	inst.BillingStartedAt = time.Now().UTC()
	inst.BillingEndedAt = time.Time{}
	m.seedRouteFromInstance(inst)
	m.registerInstanceInMemory(inst, cfg.SessionID)
	m.recordEvent(sandboxID, cfg.SessionID, cfg.TenantID, EventCreated, "Sandbox created", nil, nil, "")
	return inst, nil
}

// ExecuteStop snapshots /workspace, persists the tarball to object
// storage and verifies it, destroys the backend resource, and returns the
// workspace ref to store on the row: an object-storage ref (see
// snapshot.WorkspaceRef), the local tarball path when object storage is
// not configured, or "" when no snapshot could be taken from a dead guest.
//
// The VM is destroyed only after the workspace is durable. An upload
// failure returns an error with the VM still running (the reconciler
// retries); a workspace over the size cap returns ErrWorkspaceTooLarge.
func (m *SandboxManager) ExecuteStop(ctx context.Context, sandboxID string) (string, error) {
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	sessionID := inst.Config.SessionID
	tenantID := inst.Config.TenantID
	wasBillable := !inst.BillingStartedAt.IsZero()

	// Execution-time re-check of the trooper turn guard: the idle
	// policy checks this when MARKING the row for sleep, but a turn can
	// start in the window before the reconciler executes. Erroring here
	// makes the convergence retry later, by which time the turn has
	// ended (or activity moved last_used_at and the user revives).
	if inst.Persistent && inst.AgentID != "" && m.hasActiveTrooperSession(inst.AgentID) {
		return "", fmt.Errorf("trooper %s has an active session; stop deferred", inst.AgentID)
	}

	if err := m.ensureDataDirWritable(); err != nil {
		return "", fmt.Errorf("prepare sandbox data dir for snapshot: %w", err)
	}

	// Sample network counters while the VM is still alive; Destroy
	// below frees them and metering happens afterward.
	if wasBillable {
		m.captureNetworkBytes(ctx, inst)
	}

	snapshotPath := filepath.Join(m.dataDir(), sandboxID, "trooper.tar.gz")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0755); err != nil {
		return "", fmt.Errorf("create snapshot dir: %w", err)
	}

	snapshotCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Same dispatch order as the legacy stop: native Snapshotter,
	// docker cp fallback, universal exec-based tar for everything else.
	var snapshotErr error
	switch {
	case backendImplementsSnapshotter(m.backend):
		snapshotErr = m.backend.(Snapshotter).Snapshot(snapshotCtx, sandboxID, "/workspace", snapshotPath)
	case m.backend.Name() == "docker":
		snapshotErr = m.dockerSnapshot(snapshotCtx, inst.ContainerID, snapshotPath)
	default:
		snapshotErr = m.execSnapshot(snapshotCtx, sandboxID, snapshotPath)
	}
	if snapshotErr != nil {
		_ = os.Remove(snapshotPath)
		snapshotPath = ""
		if isTransportSizeCapError(snapshotErr) {
			return "", m.refuseSleep(sandboxID, NewWorkspaceTooLarge(0, m.WorkspaceMaxBytes()))
		}
		// A dead VM cannot be snapshotted; that is not a reason to
		// wedge the stop. Degrade to "stop without snapshot" when EITHER:
		//   - the backend reports the VM gone, OR
		//   - the snapshot error itself is a dead-guest signature
		//     (vsock socket missing, guest agent unreachable, EOF).
		// The second clause matters because fcagent's Status can still
		// report a sandbox as present from a stale route after its
		// firecracker process died, so relying on Status alone made
		// ExecuteStop retry the doomed snapshot until reconcile_attempts
		// hit the cap and the row wedged in error (observed on dev:
		// "dial unix .../vsock.sock: connect: no such file").
		_, statusErr := m.backend.Status(ctx, sandboxID)
		if statusErr == nil && !isDeadGuestSnapshotError(snapshotErr) {
			return "", fmt.Errorf("snapshot failed: %w", snapshotErr)
		}
		logger.WithFields("sandbox_id", sandboxID, "error", snapshotErr.Error()).
			Warn("sandbox_executor: snapshot failed and guest is gone; stopping without snapshot")
	}

	// Make the workspace durable BEFORE the VM is destroyed. Nothing below
	// this block may run unless the tarball is safely stored (or object
	// storage is unconfigured and the legacy local copy is all we have).
	workspaceRef := ""
	if snapshotPath == "" {
		// Guest already gone. A previous attempt of this stop may have
		// uploaded and verified the workspace and destroyed the VM before
		// its row write was lost; adopt that copy instead of reporting
		// "nothing saved".
		ref, err := m.adoptLatestWorkspaceRef(ctx, sandboxID)
		if err != nil {
			return "", err
		}
		workspaceRef = ref
	}
	if snapshotPath != "" {
		if err := m.checkWorkspaceSize(snapshotPath); err != nil {
			_ = os.Remove(snapshotPath)
			if errors.Is(err, ErrWorkspaceTooLarge) {
				return "", m.refuseSleep(sandboxID, err)
			}
			return "", err
		}
		ref, err := m.persistWorkspace(ctx, sandboxID, snapshotPath)
		if err != nil {
			_ = os.Remove(snapshotPath)
			logger.WithFields("sandbox_id", sandboxID, "tenant_id", tenantID, "error", err.Error()).
				Error("sandbox_executor: workspace upload failed; keeping the VM running and retrying")
			return "", err
		}
		workspaceRef = ref
	}

	// Best-effort full-VM snapshot for host-loss survival.
	if snapshotPath != "" {
		if _, err := m.SaveR2Snapshot(ctx, sandboxID, "sleep"); err != nil {
			logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
				Warn("sandbox_executor: R2 snapshot on stop failed; continuing")
		}
	}

	if err := m.backend.Destroy(ctx, sandboxID); err != nil {
		// One retry, then accept "already gone" as success.
		if retryErr := m.backend.Destroy(ctx, sandboxID); retryErr != nil {
			if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr == nil {
				return "", fmt.Errorf("destroy during stop: %w", retryErr)
			}
		}
	}

	stoppedAt := time.Now()
	if wasBillable {
		if !m.recordUsageForInstance(ctx, inst, EventSandboxStopped, "stopped", stoppedAt) {
			return "", fmt.Errorf("close sandbox compute billing window")
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}
	m.closeAllPortMappings(sandboxID)

	// In-memory: mark stopped so routing-layer callers (shell, exec)
	// fail fast instead of dialing a destroyed VM.
	m.mu.Lock()
	if cached, ok := m.instances[sessionID]; ok && cached.ID == sandboxID {
		cached.Status = StatusStopped
		cached.LifecycleState = LifecycleStopped
		cached.TrooperSnapshotRef = workspaceRef
		cached.StoppedAt = stoppedAt
		if cached.Persistent && cached.AgentID != "" {
			m.troopers[cached.AgentID] = cached
		}
	}
	m.mu.Unlock()

	m.recordEvent(sandboxID, sessionID, tenantID, EventSandboxStopped, "Sandbox stopped", map[string]interface{}{
		"workspace_saved":   workspaceRef != "",
		"workspace_durable": snapshot.IsWorkspaceObjectRef(workspaceRef),
	}, nil, "")
	// Trooper read-model alignment (agent_definitions is derived by the
	// reconciler's ApplyTransition in the same tx as the row write).
	if m.db != nil {
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers SET status = 'sleeping', updated_at = NOW()
			WHERE sandbox_id = $1 AND deleted_at IS NULL`, sandboxID)
	}

	logger.WithFields("sandbox_id", sandboxID, "workspace_durable", snapshot.IsWorkspaceObjectRef(workspaceRef)).
		Info("sandbox_executor: stop executed")
	return workspaceRef, nil
}

// ExecuteRevive recreates the backend resource for a sleeping or
// archived sandbox and restores the workspace. snapshotRef and archiveRef
// are the row's workspace refs (object-storage ref, legacy local path, or
// legacy archive marker; either may be empty). The saved workspace is
// fetched and verified BEFORE any VM is created; if a workspace was saved
// but cannot be restored, the revive fails with ErrWorkspaceRestoreFailed
// instead of booting an empty workspace.
func (m *SandboxManager) ExecuteRevive(ctx context.Context, sandboxID, snapshotRef, archiveRef string) (*Instance, error) {
	savedConfig, sessionID, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load saved config: %w", err)
	}
	m.shadowAdmitCompute(savedConfig.TenantID, ComputeSurfaceSandboxRevive)
	if err := m.RequireSandboxBilling(savedConfig.TenantID); err != nil {
		return nil, err
	}
	if err := m.RequireConcurrentSandboxSlot(ctx, savedConfig.TenantID, sandboxID); err != nil {
		return nil, err
	}
	fittedConfig, err := m.fitExistingInstanceMachineProfile(*savedConfig)
	if err != nil {
		return nil, err
	}
	sizeChanged := fittedConfig.CPULimit != savedConfig.CPULimit || fittedConfig.MemoryMB != savedConfig.MemoryMB
	savedConfig = &fittedConfig
	prevInst := m.findInstanceBySandboxID(sandboxID)

	owner := m.resolveTenantID(ctx, sandboxID, savedConfig.TenantID)
	snapshotPath, downloaded, err := m.materializeWorkspace(ctx, owner, sandboxID, snapshotRef, archiveRef)
	if err != nil {
		return nil, err
	}
	if downloaded {
		defer func() { _ = os.Remove(snapshotPath) }()
	}

	inst, err := m.backend.Create(ctx, sandboxID, *savedConfig)
	if err != nil {
		return nil, fmt.Errorf("create for revive: %w", err)
	}
	// Preserve trooper metadata across revive.
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
	inst.BillingStartedAt = time.Now().UTC()
	inst.BillingEndedAt = time.Time{}
	m.seedRouteFromInstance(inst)

	// Restore /workspace from the verified tarball. "" means no workspace
	// was ever saved (first boot after a dead-guest stop), not a lost one.
	if snapshotPath != "" {
		restoreCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		var restoreErr error
		switch {
		case backendImplementsSnapshotter(m.backend):
			restoreErr = m.backend.(Snapshotter).Restore(restoreCtx, sandboxID, snapshotPath, "/workspace")
		case m.backend.Name() == "docker":
			restoreErr = m.dockerRestore(restoreCtx, inst.ContainerID, snapshotPath)
		default:
			restoreErr = m.execRestore(restoreCtx, sandboxID, snapshotPath)
		}
		if restoreErr != nil {
			// Self-clean the half-created VM; the reconciler retries
			// the whole revive from scratch.
			_ = m.backend.Destroy(ctx, sandboxID)
			return nil, fmt.Errorf("workspace restore failed: %w", restoreErr)
		}
	} else {
		logger.WithFields("sandbox_id", sandboxID).
			Info("sandbox_executor: no saved workspace on the row; reviving with a fresh /workspace")
	}

	// Git-imported sandboxes: verify the host-side repo still matches.
	if savedConfig.RepoHostPath != "" {
		repoDir := savedConfig.RepoHostPath
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			_ = m.backend.Destroy(ctx, sandboxID)
			return nil, fmt.Errorf("repo directory missing: %s", repoDir)
		}
		shaOutput, shaErr := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD").Output()
		if shaErr != nil {
			_ = m.backend.Destroy(ctx, sandboxID)
			return nil, fmt.Errorf("read HEAD SHA for verification: %w", shaErr)
		}
		actualSHA := strings.TrimSpace(string(shaOutput))
		if savedConfig.GitCommitSHA != "" && actualSHA != savedConfig.GitCommitSHA {
			_ = m.backend.Destroy(ctx, sandboxID)
			return nil, fmt.Errorf("repo SHA mismatch: expected %s, got %s", savedConfig.GitCommitSHA, actualSHA)
		}
	}

	inst.LifecycleState = LifecycleRunning
	inst.Status = StatusRunning
	inst.LastUsedAt = time.Now()
	inst.Name = savedConfig.Name
	m.registerInstanceInMemory(inst, sessionID)

	m.recordEvent(sandboxID, sessionID, savedConfig.TenantID, EventSandboxRevived, "Sandbox revived", nil, nil, "")
	if m.db != nil {
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers SET status = 'running', updated_at = NOW()
			WHERE sandbox_id = $1 AND deleted_at IS NULL`, sandboxID)
		// The reconciler never rewrites the config column, and the usage
		// meter prices open windows from it, so record the size the VM
		// actually got when the plan clamped it.
		if sizeChanged {
			if configJSON, err := json.Marshal(*savedConfig); err == nil {
				if _, err := m.db.ExecContext(ctx,
					`UPDATE sandbox_instances SET config = $2 WHERE id = $1`,
					sandboxID, configJSON); err != nil {
					logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
						Warn("sandbox_executor: failed to persist plan-clamped revive size")
				}
			}
		}
	}

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_executor: revive executed")
	return inst, nil
}

// ExecuteArchive makes sure the stopped sandbox's workspace lives in
// object storage. Returns the archive ref to persist on the row ("" when
// archiving degraded to label-only: no local tarball to move, or object
// storage is not configured). A workspace already persisted at sleep has
// no local tarball, so archiving it is a label change.
func (m *SandboxManager) ExecuteArchive(ctx context.Context, sandboxID, snapshotRef string) (string, error) {
	snapshotPath := strings.TrimSpace(snapshotRef)
	if snapshotPath == "" || snapshot.IsWorkspaceObjectRef(snapshotPath) || snapshotPath == string(snapshot.KindWorkspace) {
		return "", nil
	}
	if !m.WorkspacePersistenceConfigured() {
		logger.WithFields("sandbox_id", sandboxID).
			Warn("sandbox_executor: object storage not configured; archive is label-only and the workspace stays on local gateway disk")
		return "", nil
	}
	if _, err := os.Stat(snapshotPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing to upload; the revive path will report the loss.
			logger.WithFields("sandbox_id", sandboxID).
				Warn("sandbox_executor: workspace tar missing at archive time")
			return "", nil
		}
		return "", fmt.Errorf("stat workspace tar: %w", err)
	}
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	ref, err := m.persistWorkspace(ctx, sandboxID, snapshotPath)
	if err != nil {
		return "", fmt.Errorf("archive workspace: %w", err)
	}

	m.recordEvent(sandboxID, inst.Config.SessionID, inst.Config.TenantID, EventSandboxArchived, "Sandbox archived to object storage", nil, nil, "")
	logger.WithFields("sandbox_id", sandboxID).
		Info("sandbox_executor: workspace archived to object storage")
	return ref, nil
}

// ExecuteTerminate destroys the backend resource and all host-side
// data. Idempotent: a missing VM or already-deleted data dir is
// success.
func (m *SandboxManager) ExecuteTerminate(ctx context.Context, sandboxID string) error {
	// Capture the instance before teardown. The billing window is closed only
	// after Destroy succeeds (or the backend confirms the VM is already gone),
	// never before: a failed destroy still owns billable compute.
	inst := m.findInstanceBySandboxID(sandboxID)
	if inst == nil {
		// Direct cleanup removes the in-memory route before destroying the
		// backend. If its backend or ledger close then fails, the reconciler
		// retries from the durable row and must retain the pinned billing window.
		if persisted, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID); err == nil {
			inst = persisted
		}
	}
	if inst != nil && !inst.BillingStartedAt.IsZero() {
		m.captureNetworkBytes(ctx, inst)
	}

	if err := m.backend.Destroy(ctx, sandboxID); err != nil {
		// Tolerate "already gone"; everything else gets surfaced so
		// the reconciler retries (step treats NotFound as success).
		if _, statusErr := m.backend.Status(ctx, sandboxID); statusErr == nil {
			return fmt.Errorf("destroy during terminate: %w", err)
		}
	}
	terminatedAt := time.Now()
	if inst != nil && !inst.BillingStartedAt.IsZero() {
		if !m.recordUsageForInstance(ctx, inst, EventSandboxTerminated, "terminated", terminatedAt) {
			return fmt.Errorf("close sandbox compute billing window")
		}
		inst.BillingStartedAt = time.Time{}
		inst.BillingEndedAt = time.Time{}
	}

	// Host-side data (workspace tar, git clone, logs) and any archived
	// copy in object storage.
	m.cleanupSandboxData(sandboxID)
	if store := m.snapshotStore(); store != nil {
		owner := ""
		if inst != nil {
			owner = inst.Config.TenantID
		}
		if owner = m.resolveTenantID(ctx, sandboxID, owner); owner != "" {
			delCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := store.Delete(delCtx, owner, sandboxID); err != nil {
				// The hourly snapshot GC deletes the same prefix for
				// terminated rows, so a failure here is retried there.
				logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner, "error", err.Error()).
					Warn("sandbox_executor: deleting stored workspace and snapshots failed; snapshot GC will retry")
			}
			cancel()
		}
	}
	m.closeAllPortMappings(sandboxID)

	sessionID, tenantID := "", ""
	if inst != nil {
		sessionID, tenantID = inst.Config.SessionID, inst.Config.TenantID
	}

	m.mu.Lock()
	for sid, cached := range m.instances {
		if cached.ID == sandboxID {
			delete(m.instances, sid)
			if cached.Persistent && cached.AgentID != "" {
				delete(m.troopers, cached.AgentID)
			}
			break
		}
	}
	delete(m.instancesBySandbox, sandboxID)
	for agentID, cached := range m.troopers {
		if cached.ID == sandboxID {
			delete(m.troopers, agentID)
			break
		}
	}
	m.mu.Unlock()

	m.recordEvent(sandboxID, sessionID, tenantID, EventSandboxTerminated, "Sandbox terminated", nil, nil, "")
	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_executor: terminate executed")
	return nil
}

// ExecBySandboxID runs a one-shot command in a sandbox addressed by
// its concrete id (the REST fs/process surface). Survives gateway
// restarts via the DB fallback; records the exec event and touches
// activity like the session-keyed Exec.
func (m *SandboxManager) ExecBySandboxID(ctx context.Context, sandboxID string, req ExecRequest) (*ExecResult, error) {
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	m.TouchActivityBySandboxID(sandboxID, "exec")
	start := time.Now()
	result, execErr := m.backend.Exec(ctx, sandboxID, req)
	durationMs := time.Since(start).Milliseconds()
	cmdStr := strings.Join(req.Command, " ")
	if execErr != nil {
		m.recordEvent(sandboxID, inst.Config.SessionID, inst.Config.TenantID, EventExecDone, "Execution failed", map[string]interface{}{"command": cmdStr, "error": execErr.Error()}, &durationMs, execErr.Error())
		return nil, execErr
	}
	m.recordEvent(sandboxID, inst.Config.SessionID, inst.Config.TenantID, EventExecDone, "Execution completed", map[string]interface{}{
		"command":   cmdStr,
		"exit_code": result.ExitCode,
		"timed_out": result.TimedOut,
	}, &durationMs, "")
	return result, nil
}

// ReadFileBySandboxID reads a file from a sandbox addressed by id.
func (m *SandboxManager) ReadFileBySandboxID(ctx context.Context, sandboxID, path string) ([]byte, error) {
	if _, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID); err != nil {
		return nil, err
	}
	m.TouchActivityBySandboxID(sandboxID, "file_read")
	return m.backend.ReadFile(ctx, sandboxID, path)
}

// WriteFileBySandboxID writes a file into a sandbox addressed by id.
func (m *SandboxManager) WriteFileBySandboxID(ctx context.Context, sandboxID, path string, content []byte) error {
	if _, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID); err != nil {
		return err
	}
	m.TouchActivityBySandboxID(sandboxID, "file_write")
	return m.backend.WriteFile(ctx, sandboxID, path, content)
}

// ListFilesBySandboxID lists a directory in a sandbox addressed by id.
func (m *SandboxManager) ListFilesBySandboxID(ctx context.Context, sandboxID, path string) ([]FileInfo, error) {
	if _, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID); err != nil {
		return nil, err
	}
	return m.backend.ListFiles(ctx, sandboxID, path)
}

// isDeadGuestSnapshotError reports whether a snapshot failure is the
// signature of a guest that is already gone — the in-VM tar can't run
// because the vsock channel / guest agent is unreachable. Such a stop
// must proceed without a snapshot rather than retry into the
// reconcile-attempt cap.
func isDeadGuestSnapshotError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, sig := range []string{
		"vsock.sock",
		"connect: no such file",
		"failed to connect to guest agent",
		"connection refused",
		"use of closed network connection",
		"EOF",
	} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// registerInstanceInMemory inserts an instance into the manager's
// routing caches (session map, sandbox-id map, troopers map). The
// caches are routing/config lookups only; lifecycle truth lives in the
// DB row.
func (m *SandboxManager) registerInstanceInMemory(inst *Instance, sessionID string) {
	if inst == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if sessionID != "" {
		m.instances[sessionID] = inst
	}
	m.instancesBySandbox[inst.ID] = inst
	if inst.Persistent && inst.AgentID != "" {
		m.troopers[inst.AgentID] = inst
	}
}
