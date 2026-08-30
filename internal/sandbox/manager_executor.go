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
	"errors"
	"fmt"
	"io"
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

// ExecuteStop snapshots /workspace, uploads the optional R2 VM snapshot,
// destroys the backend resource, and returns the host path of the
// workspace tarball ("" when no snapshot could be taken). The caller
// persists the returned ref on the row.
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
		cached.TrooperSnapshotRef = snapshotPath
		cached.StoppedAt = stoppedAt
		if cached.Persistent && cached.AgentID != "" {
			m.troopers[cached.AgentID] = cached
		}
	}
	m.mu.Unlock()

	m.recordEvent(sandboxID, sessionID, tenantID, EventSandboxStopped, "Sandbox stopped", map[string]interface{}{
		"snapshot": snapshotPath,
	}, nil, "")
	// Trooper read-model alignment (agent_definitions is derived by the
	// reconciler's ApplyTransition in the same tx as the row write).
	if m.db != nil {
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers SET status = 'sleeping', updated_at = NOW()
			WHERE sandbox_id = $1 AND deleted_at IS NULL`, sandboxID)
	}

	logger.WithFields("sandbox_id", sandboxID, "snapshot", snapshotPath).
		Info("sandbox_executor: stop executed")
	return snapshotPath, nil
}

// ExecuteRevive recreates the backend resource for a sleeping or
// archived sandbox and restores the workspace. snapshotRef is the local
// tarball path recorded at stop time (may be empty); archiveRef is the
// object-storage marker set by ExecuteArchive (may be empty). When the
// local tar is missing but an archive exists, it is downloaded first.
func (m *SandboxManager) ExecuteRevive(ctx context.Context, sandboxID, snapshotRef, archiveRef string) (*Instance, error) {
	savedConfig, sessionID, err := m.GetInstanceConfig(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("load saved config: %w", err)
	}
	if err := m.RequireSandboxBilling(savedConfig.TenantID); err != nil {
		return nil, err
	}
	if err := m.RequireConcurrentSandboxSlot(ctx, savedConfig.TenantID, sandboxID); err != nil {
		return nil, err
	}
	if err := m.validateInstanceMachineProfile(*savedConfig); err != nil {
		return nil, err
	}
	prevInst := m.findInstanceBySandboxID(sandboxID)

	snapshotPath := strings.TrimSpace(snapshotRef)
	if archiveRef != "" {
		if restored, err := m.materializeArchivedWorkspace(ctx, sandboxID, savedConfig.TenantID, snapshotPath); err != nil {
			return nil, fmt.Errorf("download archived workspace: %w", err)
		} else if restored != "" {
			snapshotPath = restored
		}
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

	// Restore /workspace. A missing tarball degrades to a fresh
	// workspace with a loud log (matches legacy revive semantics).
	if snapshotPath != "" {
		if _, statErr := os.Stat(snapshotPath); statErr != nil {
			logger.WithFields("sandbox_id", sandboxID, "snapshot", snapshotPath).
				Warn("sandbox_executor: snapshot file missing, reviving without workspace restore")
		} else {
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
		}
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
	}

	logger.WithFields("sandbox_id", sandboxID).Info("sandbox_executor: revive executed")
	return inst, nil
}

// ExecuteArchive moves the stopped sandbox's workspace tarball to
// object storage and deletes the local copy. Returns the archive ref
// to persist on the row ("" when archiving degraded to label-only:
// no snapshot exists or R2 is not configured).
func (m *SandboxManager) ExecuteArchive(ctx context.Context, sandboxID, snapshotRef string) (string, error) {
	snapshotPath := strings.TrimSpace(snapshotRef)
	if snapshotPath == "" {
		return "", nil
	}
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		logger.WithFields("sandbox_id", sandboxID).
			Info("sandbox_executor: R2 not configured; archive is label-only (local tar kept)")
		return "", nil
	}
	inst, err := m.findInstanceBySandboxIDWithDBFallback(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	f, err := os.Open(snapshotPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Nothing to upload; archive proceeds label-only.
			logger.WithFields("sandbox_id", sandboxID, "snapshot", snapshotPath).
				Warn("sandbox_executor: workspace tar missing at archive time")
			return "", nil
		}
		return "", fmt.Errorf("open workspace tar: %w", err)
	}
	defer f.Close()

	upCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	rec, err := store.PutStream(upCtx, inst.Config.TenantID, sandboxID, snapshot.KindWorkspace, "application/gzip", f)
	if err != nil {
		return "", fmt.Errorf("upload workspace tar: %w", err)
	}
	// Local copy is now redundant; deleting it is the point of
	// archiving (free host disk). Best-effort: a leftover file is
	// re-deleted by the next archive or terminate.
	_ = os.Remove(snapshotPath)

	m.recordEvent(sandboxID, inst.Config.SessionID, inst.Config.TenantID, EventSandboxArchived, "Sandbox archived to object storage", map[string]interface{}{
		"size_bytes": rec.SizeBytes,
	}, nil, "")
	logger.WithFields("sandbox_id", sandboxID, "size_bytes", rec.SizeBytes).
		Info("sandbox_executor: workspace archived to object storage")
	return string(snapshot.KindWorkspace), nil
}

// materializeArchivedWorkspace downloads the archived workspace tar
// back to the local snapshot path so the normal restore dispatch can
// run. Returns the local path, or "" when the archive stream is not
// available (revive proceeds with a fresh workspace).
func (m *SandboxManager) materializeArchivedWorkspace(ctx context.Context, sandboxID, tenantID, existingLocal string) (string, error) {
	if existingLocal != "" {
		if _, err := os.Stat(existingLocal); err == nil {
			return existingLocal, nil // local tar survived; no download needed
		}
	}
	store := m.snapshotStore()
	if _, disabled := store.(*snapshot.Disabled); disabled {
		return "", nil
	}
	rc, err := store.GetStream(ctx, tenantID, sandboxID, snapshot.KindWorkspace)
	if err != nil {
		if errors.Is(err, snapshot.ErrSnapshotMissing) {
			return "", nil
		}
		return "", err
	}
	defer rc.Close()

	if err := m.ensureDataDirWritable(); err != nil {
		return "", err
	}
	localPath := filepath.Join(m.dataDir(), sandboxID, "trooper.tar.gz")
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", err
	}
	tmp := localPath + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	logger.WithFields("sandbox_id", sandboxID, "path", localPath).
		Info("sandbox_executor: archived workspace downloaded for revive")
	return localPath, nil
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
		if inst != nil && inst.Config.TenantID != "" {
			delCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = store.Delete(delCtx, inst.Config.TenantID, sandboxID)
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
