package sandbox

// Durable /workspace persistence across sleep and wake.
//
// A sleeping sandbox has no VM; its /workspace exists only as a tarball.
// That tarball used to live on the gateway pod's local disk, which has no
// persistent volume, so every gateway rollout deleted it and the next wake
// silently booted an empty workspace. Now:
//
//   - Sleep uploads the tarball to object storage (the sandbox R2 store),
//     verifies it by SHA-256 and size, and only then lets the VM be
//     destroyed. Upload failure keeps the VM running and the reconciler
//     retries. A workspace over the size cap is refused with a
//     customer-facing reason and the sandbox keeps running.
//   - Wake downloads and verifies the tarball before any VM is created. A
//     missing or corrupt tarball fails the wake loudly with a
//     customer-facing reason; it never boots an empty workspace in its
//     place.
//   - With object storage unconfigured the old local-disk behaviour
//     remains, with a warning on every sleep.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

const (
	// WorkspaceSnapshotTransportMaxBytes is the hard ceiling the snapshot
	// transports enforce (fcagent MaxBytes and execSnapshot). A configured
	// cap above it is clamped.
	WorkspaceSnapshotTransportMaxBytes int64 = 1 << 30
	// DefaultWorkspaceMaxBytes is the default sleep size cap.
	DefaultWorkspaceMaxBytes = WorkspaceSnapshotTransportMaxBytes

	// WorkspaceErrorCodeRestoreFailed prefixes sandbox_instances.error for
	// a wake that could not restore the saved workspace. The recovery
	// checker skips rows carrying it: retrying cannot bring the data back.
	WorkspaceErrorCodeRestoreFailed = "workspace_restore_failed"
	// WorkspaceErrorCodeTooLarge prefixes sandbox_instances.error for a
	// sleep refused because the workspace is over the cap.
	WorkspaceErrorCodeTooLarge = "workspace_too_large"
	// WorkspaceErrorCodeNeedsAttention prefixes sandbox_instances.error for
	// a sandbox automatic cleanup refused to touch because object storage
	// holds its only workspace copy. Also skipped by the recovery checker.
	WorkspaceErrorCodeNeedsAttention = "workspace_needs_attention"

	// RestoreFailedCustomerMessage is shown to the customer (error_reason)
	// when a wake cannot restore the saved workspace.
	RestoreFailedCustomerMessage = "This sandbox could not be started because its saved /workspace from the last sleep is missing or damaged. " +
		"We did not start it with an empty workspace. Delete this sandbox and create a new one, or contact support."

	workspaceUploadTimeout   = 3 * time.Minute
	workspaceDownloadTimeout = 3 * time.Minute
)

var (
	// ErrWorkspaceRestoreFailed marks a permanent wake failure: the saved
	// workspace is missing, corrupt, or unreachable by configuration.
	ErrWorkspaceRestoreFailed = errors.New("sandbox: saved workspace could not be restored")
	// ErrWorkspaceTooLarge marks a sleep refused because the workspace
	// tarball is over the configured cap.
	ErrWorkspaceTooLarge = errors.New("sandbox: workspace exceeds the sleep size limit")
)

// WorkspaceError carries a stable code, an internal detail (safe to log:
// no credentials or URLs) and the customer-facing message.
type WorkspaceError struct {
	kind            error
	Code            string
	Detail          string
	CustomerMessage string
}

func (e *WorkspaceError) Error() string { return e.Code + ": " + e.Detail }

// Is lets errors.Is match the sentinel kind.
func (e *WorkspaceError) Is(target error) bool { return target == e.kind }

// AsWorkspaceError extracts a WorkspaceError from err.
func AsWorkspaceError(err error) (*WorkspaceError, bool) {
	var we *WorkspaceError
	if errors.As(err, &we) && we != nil {
		return we, true
	}
	return nil, false
}

func NewWorkspaceRestoreFailed(detail string) error {
	return &WorkspaceError{
		kind:            ErrWorkspaceRestoreFailed,
		Code:            WorkspaceErrorCodeRestoreFailed,
		Detail:          detail,
		CustomerMessage: RestoreFailedCustomerMessage,
	}
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func NewWorkspaceTooLarge(size, limit int64) error {
	sizeText := "larger than"
	if size > 0 {
		sizeText = fmt.Sprintf("%s compressed, over", formatBytes(size))
	}
	return &WorkspaceError{
		kind:   ErrWorkspaceTooLarge,
		Code:   WorkspaceErrorCodeTooLarge,
		Detail: fmt.Sprintf("workspace tarball %d bytes exceeds cap %d", size, limit),
		CustomerMessage: fmt.Sprintf(
			"This sandbox was not put to sleep because its /workspace is %s the %s limit for sleeping sandboxes. "+
				"It is still running. Remove files from /workspace to let it sleep.",
			sizeText, formatBytes(limit)),
	}
}

// isTransportSizeCapError recognises the size refusals the snapshot
// transports return as plain strings (fcagent server and execSnapshot).
func isTransportSizeCapError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exceeds cap") || strings.Contains(msg, "snapshot too large")
}

// refuseSleep records a refused sleep: it restarts the in-memory idle clock
// so persistent-agent idle policy does not immediately retry the same
// oversized tarball, logs, and returns err unchanged.
func (m *SandboxManager) refuseSleep(sandboxID string, err error) error {
	m.TouchActivityBySandboxID(sandboxID, "sleep_refused")
	logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
		Warn("sandbox_executor: sleep refused; workspace is over the size cap, VM kept running")
	return err
}

// SetWorkspaceMaxBytes sets the sleep size cap. Zero or negative restores
// the default; values above the transport ceiling are clamped to it.
func (m *SandboxManager) SetWorkspaceMaxBytes(n int64) {
	switch {
	case n <= 0:
		n = DefaultWorkspaceMaxBytes
	case n > WorkspaceSnapshotTransportMaxBytes:
		logger.WithFields("requested_bytes", n, "ceiling_bytes", WorkspaceSnapshotTransportMaxBytes).
			Warn("sandbox: workspace size cap above the snapshot transport ceiling; clamping")
		n = WorkspaceSnapshotTransportMaxBytes
	}
	m.workspaceMaxBytes.Store(n)
}

// WorkspaceMaxBytes returns the effective sleep size cap.
func (m *SandboxManager) WorkspaceMaxBytes() int64 {
	if n := m.workspaceMaxBytes.Load(); n > 0 {
		return n
	}
	return DefaultWorkspaceMaxBytes
}

// workspaceStore returns the verified workspace store, or false when
// object storage is not configured.
func (m *SandboxManager) workspaceStore() (snapshot.WorkspaceStore, bool) {
	ws, ok := m.snapshotStore().(snapshot.WorkspaceStore)
	return ws, ok
}

// WorkspacePersistenceConfigured reports whether sleeping workspaces are
// persisted to object storage.
func (m *SandboxManager) WorkspacePersistenceConfigured() bool {
	_, ok := m.workspaceStore()
	return ok
}

// resolveTenantID returns the tenant that owns sandboxID, preferring the
// config value and falling back to the row column.
func (m *SandboxManager) resolveTenantID(ctx context.Context, sandboxID, fromConfig string) string {
	if t := strings.TrimSpace(fromConfig); t != "" {
		return t
	}
	if m.db == nil {
		return ""
	}
	var tenantID string
	_ = m.db.GetContext(ctx, &tenantID, `SELECT tenant_id FROM sandbox_instances WHERE id = $1`, sandboxID)
	return strings.TrimSpace(tenantID)
}

// workspaceRow is the slice of sandbox_instances the workspace path needs.
type workspaceRow struct {
	TenantID    string `db:"tenant_id"`
	Incarnation string `db:"workspace_incarnation"`
	RunID       string `db:"workspace_run_id"`
	SnapshotRef string `db:"snapshot_ref"`
	ArchiveRef  string `db:"archive_ref"`
}

// loadWorkspaceRow reads the owner identity (tenant, incarnation, current
// run) and the row's refs. The incarnation and run are what bind a tarball
// to this sandbox's life; they only live in the database.
func (m *SandboxManager) loadWorkspaceRow(ctx context.Context, sandboxID string) (workspaceRow, error) {
	if m.db == nil {
		return workspaceRow{}, fmt.Errorf("workspace owner for %s: no database", sandboxID)
	}
	var r workspaceRow
	if err := m.db.GetContext(ctx, &r, `
		SELECT tenant_id, workspace_incarnation, workspace_run_id,
		       COALESCE(workspace_snapshot_ref, '') AS snapshot_ref,
		       COALESCE(workspace_archive_ref, '')  AS archive_ref
		FROM sandbox_instances WHERE id = $1`, sandboxID); err != nil {
		return workspaceRow{}, fmt.Errorf("workspace owner for %s: %w", sandboxID, err)
	}
	return r, nil
}

func (r workspaceRow) owner(sandboxID string) snapshot.WorkspaceOwner {
	return snapshot.WorkspaceOwner{
		TenantID:    strings.TrimSpace(r.TenantID),
		SandboxID:   sandboxID,
		Incarnation: strings.TrimSpace(r.Incarnation),
		RunID:       strings.TrimSpace(r.RunID),
	}
}

// workspaceOwner returns the current owner identity for sandboxID.
func (m *SandboxManager) workspaceOwner(ctx context.Context, sandboxID string) (snapshot.WorkspaceOwner, error) {
	r, err := m.loadWorkspaceRow(ctx, sandboxID)
	if err != nil {
		return snapshot.WorkspaceOwner{}, err
	}
	return r.owner(sandboxID), nil
}

// checkWorkspaceSize enforces the sleep size cap on a local tarball.
func (m *SandboxManager) checkWorkspaceSize(localPath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat workspace tarball: %w", err)
	}
	if limit := m.WorkspaceMaxBytes(); info.Size() > limit {
		return NewWorkspaceTooLarge(info.Size(), limit)
	}
	return nil
}

// persistWorkspace uploads the local tarball into the sandbox's current
// run and returns the ref to store on the row. With object storage
// unconfigured it returns the local path (legacy behaviour) and logs a
// warning. An error means nothing durable was written and the caller must
// not destroy the VM.
//
// After the new generation is verified every other generation is pruned,
// including the one the last wake consumed: that copy is kept until here,
// the next verified sleep, so a host death right after a wake still has it.
func (m *SandboxManager) persistWorkspace(ctx context.Context, sandboxID, localPath string) (string, error) {
	ws, ok := m.workspaceStore()
	if !ok {
		logger.WithFields("sandbox_id", sandboxID).
			Warn("sandbox_executor: workspace persistence is NOT configured (EVS_SANDBOX_R2_*); the sleeping workspace stays on this gateway's local disk and is lost when the gateway pod restarts")
		return localPath, nil
	}
	owner, err := m.workspaceOwner(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	upCtx, cancel := context.WithTimeout(ctx, workspaceUploadTimeout)
	defer cancel()
	ref, err := ws.PutWorkspace(upCtx, owner, localPath)
	if err != nil {
		return "", fmt.Errorf("persist workspace to object storage: %w", err)
	}

	pruneCtx, pruneCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := ws.PruneWorkspaces(pruneCtx, owner.TenantID, sandboxID, ref); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_executor: pruning older workspace tarballs failed")
	}
	pruneCancel()
	_ = os.Remove(localPath)

	logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner.TenantID, "size_bytes", ref.SizeBytes).
		Info("sandbox_executor: workspace persisted to object storage and verified")
	return ref.String(), nil
}

// isPermanentWorkspaceFetchError reports whether retrying the download
// cannot help.
func isPermanentWorkspaceFetchError(err error) bool {
	return errors.Is(err, snapshot.ErrSnapshotMissing) ||
		errors.Is(err, snapshot.ErrWorkspaceCorrupt) ||
		errors.Is(err, snapshot.ErrWorkspaceRefForeign) ||
		errors.Is(err, snapshot.ErrInvalidWorkspaceOwner) ||
		errors.Is(err, snapshot.ErrWorkspaceStoreUnsupported)
}

// materializeWorkspace resolves the row's workspace refs to a verified
// local tarball ready to restore. It returns "" with no error only when no
// workspace was saved for the current run. downloaded reports whether the
// file is a temporary download the caller should delete after restoring.
//
// Errors are either a *WorkspaceError (permanent: the saved workspace is
// gone or corrupt) or a plain error (transient: retry).
func (m *SandboxManager) materializeWorkspace(ctx context.Context, tenantID, sandboxID, snapshotRef, archiveRef string) (localPath string, downloaded bool, err error) {
	refs := make([]string, 0, 2)
	for _, r := range []string{snapshotRef, archiveRef} {
		if r = strings.TrimSpace(r); r != "" {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return m.adoptLatestWorkspaceForRestore(ctx, sandboxID)
	}

	var transient error
	var reasons []string
	for _, r := range refs {
		path, dl, fetchErr := m.fetchWorkspaceRef(ctx, tenantID, sandboxID, r)
		if fetchErr == nil {
			return path, dl, nil
		}
		if isPermanentWorkspaceFetchError(fetchErr) || errors.Is(fetchErr, ErrWorkspaceRestoreFailed) {
			reasons = append(reasons, fetchErr.Error())
			continue
		}
		transient = fetchErr
	}
	if transient != nil {
		return "", false, fmt.Errorf("download saved workspace: %w", transient)
	}
	detail := strings.Join(reasons, "; ")
	logger.WithFields("sandbox_id", sandboxID, "tenant_id", tenantID, "detail", detail).
		Error("sandbox_executor: saved workspace could not be restored; refusing to wake with an empty workspace")
	return "", false, NewWorkspaceRestoreFailed(detail)
}

// adoptLatestWorkspaceForRestore covers a row that carries no workspace
// ref although its current run stored a verified generation: the stop
// uploaded it and destroyed the VM, but the row write-back was lost. Only
// the current incarnation and run are considered, so a previous sandbox
// that reused this id, or the generation the last wake consumed, is never
// adopted.
func (m *SandboxManager) adoptLatestWorkspaceForRestore(ctx context.Context, sandboxID string) (string, bool, error) {
	ws, ok := m.workspaceStore()
	if !ok {
		return "", false, nil
	}
	owner, err := m.workspaceOwner(ctx, sandboxID)
	if err != nil {
		return "", false, err
	}
	found := false
	var adopted snapshot.WorkspaceRef
	path, downloaded, err := m.downloadWorkspace(ctx, sandboxID, func(ctx context.Context, f *os.File) error {
		ref, fnd, err := ws.LatestWorkspace(ctx, owner, f)
		found, adopted = fnd, ref
		return err
	})
	if err != nil {
		if isPermanentWorkspaceFetchError(err) {
			logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner.TenantID, "error", err.Error()).
				Error("sandbox_executor: an unreferenced workspace exists but cannot be verified; refusing to wake with an empty workspace")
			return "", false, NewWorkspaceRestoreFailed("unreferenced workspace generation failed verification: " + err.Error())
		}
		return "", false, fmt.Errorf("look up stored workspace: %w", err)
	}
	if !found {
		_ = os.Remove(path)
		return "", false, nil
	}
	logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner.TenantID, "size_bytes", adopted.SizeBytes).
		Warn("sandbox_executor: row had no workspace ref; adopted the current run's verified generation from object storage")
	return path, downloaded, nil
}

// adoptLatestWorkspaceRef returns the ref of the current run's newest
// verified generation without keeping its bytes ("" when none). Used by a
// stop retry whose guest is already gone: the previous attempt may have
// uploaded the workspace before its row write was lost.
func (m *SandboxManager) adoptLatestWorkspaceRef(ctx context.Context, sandboxID string) (string, error) {
	ws, ok := m.workspaceStore()
	if !ok {
		return "", nil
	}
	owner, err := m.workspaceOwner(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	ref, found, err := ws.LatestWorkspace(ctx, owner, io.Discard)
	if err != nil {
		if isPermanentWorkspaceFetchError(err) {
			// The wake path repeats the lookup and fails loudly.
			logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner.TenantID, "error", err.Error()).
				Error("sandbox_executor: stored workspace generation failed verification during stop")
			return "", nil
		}
		return "", fmt.Errorf("look up stored workspace: %w", err)
	}
	if !found {
		return "", nil
	}
	logger.WithFields("sandbox_id", sandboxID, "tenant_id", owner.TenantID).
		Warn("sandbox_executor: guest gone on stop retry; adopted the workspace a previous attempt already persisted")
	return ref.String(), nil
}

// HasDurableWorkspace reports whether object storage may hold the only
// copy of this sandbox's workspace: the row references a stored tarball,
// or its current run wrote one the row does not reference yet. Automatic
// cleanup must not terminate such a sandbox, since terminate deletes it.
func (m *SandboxManager) HasDurableWorkspace(ctx context.Context, sandboxID string) (bool, error) {
	ws, ok := m.workspaceStore()
	if !ok {
		return false, nil
	}
	r, err := m.loadWorkspaceRow(ctx, sandboxID)
	if err != nil {
		return false, err
	}
	if snapshot.IsWorkspaceObjectRef(r.ArchiveRef) || snapshot.IsWorkspaceObjectRef(r.SnapshotRef) ||
		r.ArchiveRef == string(snapshot.KindWorkspace) {
		return true, nil
	}
	return ws.HasWorkspace(ctx, r.owner(sandboxID))
}

func (m *SandboxManager) fetchWorkspaceRef(ctx context.Context, tenantID, sandboxID, ref string) (string, bool, error) {
	if objRef, ok, parseErr := snapshot.ParseWorkspaceRef(ref); ok {
		if parseErr != nil {
			return "", false, NewWorkspaceRestoreFailed(parseErr.Error())
		}
		ws, configured := m.workspaceStore()
		if !configured {
			return "", false, NewWorkspaceRestoreFailed("workspace is in object storage but object storage is not configured on this gateway")
		}
		owner, err := m.workspaceOwner(ctx, sandboxID)
		if err != nil {
			return "", false, err
		}
		return m.downloadWorkspace(ctx, sandboxID, func(ctx context.Context, f *os.File) error {
			return ws.GetWorkspace(ctx, owner, objRef, f)
		})
	}

	if ref == string(snapshot.KindWorkspace) {
		// Legacy archive marker: fixed key, no recorded digest.
		store := m.snapshotStore()
		if _, disabled := store.(*snapshot.Disabled); disabled {
			return "", false, NewWorkspaceRestoreFailed("workspace was archived to object storage but object storage is not configured on this gateway")
		}
		if strings.TrimSpace(tenantID) == "" {
			return "", false, NewWorkspaceRestoreFailed("sandbox has no tenant id; cannot locate its archived workspace")
		}
		return m.downloadWorkspace(ctx, sandboxID, func(ctx context.Context, f *os.File) error {
			rc, err := store.GetStream(ctx, tenantID, sandboxID, snapshot.KindWorkspace)
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = f.ReadFrom(rc)
			return err
		})
	}

	// Legacy local path written before object storage persistence, or by
	// a gateway without object storage configured.
	if _, err := os.Stat(ref); err != nil {
		return "", false, NewWorkspaceRestoreFailed("local workspace tarball is gone (gateway disk is not persistent)")
	}
	return ref, false, nil
}

// downloadWorkspace streams into <dataDir>/<id>/workspace-restore.tar.gz via
// a .partial file, so a failed download never leaves a usable-looking file.
func (m *SandboxManager) downloadWorkspace(ctx context.Context, sandboxID string, fetch func(context.Context, *os.File) error) (string, bool, error) {
	if err := m.ensureDataDirWritable(); err != nil {
		return "", false, fmt.Errorf("prepare sandbox data dir for restore: %w", err)
	}
	dest := filepath.Join(m.dataDir(), sandboxID, "workspace-restore.tar.gz")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", false, err
	}
	tmp := dest + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", false, err
	}
	dlCtx, cancel := context.WithTimeout(ctx, workspaceDownloadTimeout)
	defer cancel()
	if err := fetch(dlCtx, f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", false, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return dest, true, nil
}
