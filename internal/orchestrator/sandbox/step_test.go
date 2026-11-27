package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// fakeBackend is a minimal Executor mock for step() tests. Each
// method's behavior is configured per test via the per-call *fn
// fields; the legacy field names (createFn/destroyFn) are kept so the
// pre-Executor tests read unchanged.
type fakeBackend struct {
	createFn  func(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error)
	destroyFn func(ctx context.Context, id string) error
	statusFn  func(ctx context.Context, id string) (*sandbox.Instance, error)
	stopFn    func(ctx context.Context, id string) (string, error)
	reviveFn  func(ctx context.Context, id, snapshotRef, archiveRef string) (*sandbox.Instance, error)
	archiveFn func(ctx context.Context, id, snapshotRef string) (string, error)
}

func (f *fakeBackend) ExecuteCreate(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error) {
	if f.createFn != nil {
		return f.createFn(ctx, id, cfg)
	}
	return &sandbox.Instance{
		ID:          id,
		ContainerID: "container-" + id,
		Backend:     "fake",
		AgentTarget: "fake-host:9090",
	}, nil
}
func (f *fakeBackend) ExecuteStop(ctx context.Context, id string) (string, error) {
	if f.stopFn != nil {
		return f.stopFn(ctx, id)
	}
	// The default mirrors a successful stop with a workspace snapshot,
	// exercising the destroy hook tests still set via destroyFn.
	if f.destroyFn != nil {
		if err := f.destroyFn(ctx, id); err != nil {
			return "", err
		}
	}
	return "/data/" + id + "/trooper.tar.gz", nil
}
func (f *fakeBackend) ExecuteRevive(ctx context.Context, id, snapshotRef, archiveRef string) (*sandbox.Instance, error) {
	if f.reviveFn != nil {
		return f.reviveFn(ctx, id, snapshotRef, archiveRef)
	}
	if f.createFn != nil {
		return f.createFn(ctx, id, sandbox.InstanceConfig{})
	}
	return &sandbox.Instance{ID: id, ContainerID: "container-" + id}, nil
}
func (f *fakeBackend) ExecuteArchive(ctx context.Context, id, snapshotRef string) (string, error) {
	if f.archiveFn != nil {
		return f.archiveFn(ctx, id, snapshotRef)
	}
	return "workspace.tar.gz", nil
}
func (f *fakeBackend) ExecuteTerminate(ctx context.Context, id string) error {
	if f.destroyFn != nil {
		return f.destroyFn(ctx, id)
	}
	return nil
}
func (f *fakeBackend) BackendStatus(ctx context.Context, id string) (*sandbox.Instance, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func newRow(state, desired string) Row {
	return Row{
		ID:             "sbx_test",
		TenantID:       "tenant-1",
		SessionID:      "session-1",
		LifecycleState: state,
		DesiredState:   desired,
		Status:         state,
		ReconcileAfter: time.Now(),
		Config:         []byte(`{}`),
	}
}

func TestStep_Pending_TransitionsToCreating(t *testing.T) {
	row := newRow(StatePending, DesireRunning)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateCreating {
		t.Fatalf("want LifecycleState=%q, got %q", StateCreating, got.LifecycleState)
	}
	if got.Status != StateCreating {
		t.Fatalf("want Status=%q, got %q", StateCreating, got.Status)
	}
}

func TestStep_Creating_CallsBackendCreateAndTransitionsToRunning(t *testing.T) {
	called := false
	b := &fakeBackend{
		createFn: func(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error) {
			called = true
			if id != "sbx_test" {
				t.Fatalf("want id=sbx_test, got %q", id)
			}
			if cfg.TenantID != "tenant-1" {
				t.Fatalf("want tenant_id forwarded; got %q", cfg.TenantID)
			}
			return &sandbox.Instance{
				ID:          id,
				ContainerID: "ctr-1",
				Backend:     "fcagent",
				AgentTarget: "10.0.0.1:9090",
			}, nil
		},
	}
	row := newRow(StateCreating, DesireRunning)
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !called {
		t.Fatal("Create was not called")
	}
	if got.LifecycleState != StateRunning {
		t.Fatalf("want LifecycleState=%q, got %q", StateRunning, got.LifecycleState)
	}
	if got.ContainerID.String != "ctr-1" {
		t.Fatalf("want ContainerID=ctr-1, got %q", got.ContainerID.String)
	}
	if got.AgentTarget.String != "10.0.0.1:9090" {
		t.Fatalf("want AgentTarget=10.0.0.1:9090, got %q", got.AgentTarget.String)
	}
}

func TestStep_Creating_BackendErrorReturnsRowUnchanged(t *testing.T) {
	b := &fakeBackend{
		createFn: func(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error) {
			return nil, errors.New("agent unreachable")
		},
	}
	row := newRow(StateCreating, DesireRunning)
	got, err := Step(context.Background(), b, row)
	if err == nil {
		t.Fatal("expected error from Create; got nil")
	}
	if got.LifecycleState != StateCreating {
		t.Fatalf("on error, row should be unchanged; got LifecycleState=%q", got.LifecycleState)
	}
}

func TestStep_Running_DesireRunning_Parks(t *testing.T) {
	row := newRow(StateRunning, DesireRunning)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateRunning {
		t.Fatalf("want unchanged LifecycleState=running; got %q", got.LifecycleState)
	}
	if !got.ReconcileAfter.After(time.Now().Add(23 * time.Hour)) {
		t.Fatal("running rows must park reconcile_after far in future")
	}
}

func TestStep_Running_DesireSleeping_TransitionsToStopping(t *testing.T) {
	row := newRow(StateRunning, DesireSleeping)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateStopping {
		t.Fatalf("want LifecycleState=stopping; got %q", got.LifecycleState)
	}
}

func TestStep_Running_DesireTerminated_TransitionsToTerminating(t *testing.T) {
	row := newRow(StateRunning, DesireTerminated)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateTerminating {
		t.Fatalf("want LifecycleState=terminating; got %q", got.LifecycleState)
	}
}

func TestStep_Stopping_CallsDestroyAndTransitionsToSleeping(t *testing.T) {
	called := false
	b := &fakeBackend{
		destroyFn: func(ctx context.Context, id string) error {
			called = true
			return nil
		},
	}
	row := newRow(StateStopping, DesireSleeping)
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !called {
		t.Fatal("Destroy was not called")
	}
	if got.LifecycleState != StateSleeping {
		t.Fatalf("want LifecycleState=sleeping; got %q", got.LifecycleState)
	}
}

func TestStep_Sleeping_DesireRunning_TransitionsToReviving(t *testing.T) {
	row := newRow(StateSleeping, DesireRunning)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateReviving {
		t.Fatalf("want LifecycleState=reviving; got %q", got.LifecycleState)
	}
}

func TestStep_Sleeping_DesireArchived_TransitionsToArchiving(t *testing.T) {
	row := newRow(StateSleeping, DesireArchived)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateArchiving {
		t.Fatalf("want LifecycleState=archiving; got %q", got.LifecycleState)
	}
	if got.Status != StateArchiving {
		t.Fatalf("want Status=archiving; got %q", got.Status)
	}
}

func TestStep_Archiving_TransitionsToArchived(t *testing.T) {
	row := newRow(StateArchiving, DesireArchived)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateArchived {
		t.Fatalf("want LifecycleState=archived; got %q", got.LifecycleState)
	}
	if got.Status != StateArchived {
		t.Fatalf("want Status=archived; got %q", got.Status)
	}
}

func TestStep_Reviving_CallsCreateAndTransitionsToRunning(t *testing.T) {
	b := &fakeBackend{
		createFn: func(ctx context.Context, id string, cfg sandbox.InstanceConfig) (*sandbox.Instance, error) {
			return &sandbox.Instance{ID: id, ContainerID: "ctr-revived"}, nil
		},
	}
	row := newRow(StateReviving, DesireRunning)
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateRunning {
		t.Fatalf("want LifecycleState=running; got %q", got.LifecycleState)
	}
	if got.ContainerID.String != "ctr-revived" {
		t.Fatalf("want ContainerID=ctr-revived; got %q", got.ContainerID.String)
	}
}

func TestStep_Terminating_CallsDestroyAndTransitionsToTerminated(t *testing.T) {
	row := newRow(StateTerminating, DesireTerminated)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateTerminated {
		t.Fatalf("want LifecycleState=terminated; got %q", got.LifecycleState)
	}
}

func TestStep_Terminating_NotFoundIsTreatedAsSuccess(t *testing.T) {
	b := &fakeBackend{
		destroyFn: func(ctx context.Context, id string) error {
			return errors.New("rpc error: code = NotFound desc = sandbox gone")
		},
	}
	row := newRow(StateTerminating, DesireTerminated)
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("NotFound should be tolerated; got err: %v", err)
	}
	if got.LifecycleState != StateTerminated {
		t.Fatalf("want LifecycleState=terminated even on NotFound; got %q", got.LifecycleState)
	}
}

func TestStep_Stopping_PersistsWorkspaceSnapshotRef(t *testing.T) {
	b := &fakeBackend{
		stopFn: func(ctx context.Context, id string) (string, error) {
			return "/data/sbx_test/trooper.tar.gz", nil
		},
	}
	row := newRow(StateStopping, DesireSleeping)
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.WorkspaceSnapshotRef.String != "/data/sbx_test/trooper.tar.gz" {
		t.Fatalf("want snapshot ref persisted; got %q", got.WorkspaceSnapshotRef.String)
	}
}

func TestStep_Archiving_StoresArchiveRefAndClearsLocal(t *testing.T) {
	b := &fakeBackend{
		archiveFn: func(ctx context.Context, id, snapshotRef string) (string, error) {
			if snapshotRef != "/data/sbx_test/trooper.tar.gz" {
				t.Fatalf("want local snapshot ref forwarded; got %q", snapshotRef)
			}
			return "workspace.tar.gz", nil
		},
	}
	row := newRow(StateArchiving, DesireArchived)
	row.WorkspaceSnapshotRef = nullable("/data/sbx_test/trooper.tar.gz")
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateArchived {
		t.Fatalf("want LifecycleState=archived; got %q", got.LifecycleState)
	}
	if got.WorkspaceArchiveRef.String != "workspace.tar.gz" {
		t.Fatalf("want archive ref persisted; got %q", got.WorkspaceArchiveRef.String)
	}
	if got.WorkspaceSnapshotRef.Valid {
		t.Fatalf("local snapshot ref should be cleared after archive; got %q", got.WorkspaceSnapshotRef.String)
	}
}

func TestStep_Archiving_LabelOnlyKeepsLocalRef(t *testing.T) {
	b := &fakeBackend{
		archiveFn: func(ctx context.Context, id, snapshotRef string) (string, error) {
			return "", nil // R2 unconfigured: label-only
		},
	}
	row := newRow(StateArchiving, DesireArchived)
	row.WorkspaceSnapshotRef = nullable("/data/sbx_test/trooper.tar.gz")
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.WorkspaceSnapshotRef.String != "/data/sbx_test/trooper.tar.gz" {
		t.Fatal("label-only archive must keep the local snapshot ref")
	}
}

func TestStep_Reviving_ForwardsRefsAndClearsThem(t *testing.T) {
	b := &fakeBackend{
		reviveFn: func(ctx context.Context, id, snapshotRef, archiveRef string) (*sandbox.Instance, error) {
			if snapshotRef != "/data/sbx_test/trooper.tar.gz" || archiveRef != "workspace.tar.gz" {
				t.Fatalf("want refs forwarded; got snapshot=%q archive=%q", snapshotRef, archiveRef)
			}
			return &sandbox.Instance{ID: id, ContainerID: "ctr-revived"}, nil
		},
	}
	row := newRow(StateReviving, DesireRunning)
	row.WorkspaceSnapshotRef = nullable("/data/sbx_test/trooper.tar.gz")
	row.WorkspaceArchiveRef = nullable("workspace.tar.gz")
	got, err := Step(context.Background(), b, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.WorkspaceSnapshotRef.Valid || got.WorkspaceArchiveRef.Valid {
		t.Fatal("refs should be cleared after a successful revive")
	}
}

func TestStep_Error_Parks(t *testing.T) {
	row := newRow(StateError, DesireRunning)
	got, err := Step(context.Background(), &fakeBackend{}, row)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LifecycleState != StateError {
		t.Fatalf("error state should not change without Recover; got %q", got.LifecycleState)
	}
	if !got.ReconcileAfter.After(time.Now().Add(364 * 24 * time.Hour)) {
		t.Fatal("error rows should park indefinitely")
	}
}

func TestStep_TerminalStates_ParkIndefinitely(t *testing.T) {
	for _, state := range []string{StateFailed, StateTerminated} {
		t.Run(state, func(t *testing.T) {
			row := newRow(state, DesireRunning)
			got, err := Step(context.Background(), &fakeBackend{}, row)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.LifecycleState != state {
				t.Fatalf("terminal state should not change; got %q", got.LifecycleState)
			}
			if !got.ReconcileAfter.After(time.Now().Add(364 * 24 * time.Hour)) {
				t.Fatal("terminal rows should park indefinitely")
			}
		})
	}
}

func TestStep_UnknownStateReturnsError(t *testing.T) {
	row := newRow("garbage", DesireRunning)
	_, err := Step(context.Background(), &fakeBackend{}, row)
	if err == nil {
		t.Fatal("unknown state should error")
	}
}

func TestAgentLifecycleFor(t *testing.T) {
	cases := []struct {
		sandboxState string
		want         string
	}{
		{StatePending, AgentStateProvisioning},
		{StateCreating, AgentStateProvisioning},
		{StateRunning, AgentStateIdle}, // running ↔ idle is owned by turn handlers; SQL guard prevents clobber
		{StateStopping, AgentStateSleeping},
		{StateSleeping, AgentStateSleeping},
		{StateArchiving, AgentStateSleeping},
		{StateArchived, AgentStateSleeping},
		{StateReviving, AgentStateWaking},
		{StateTerminating, AgentStateTerminated},
		{StateTerminated, AgentStateTerminated},
		{StateFailed, AgentStateFailed},
		{StateError, AgentStateFailed},
		{"unknown_state", ""}, // unknown leaves agent alone
	}
	for _, c := range cases {
		t.Run(c.sandboxState, func(t *testing.T) {
			got := AgentLifecycleFor(c.sandboxState)
			if got != c.want {
				t.Fatalf("AgentLifecycleFor(%q) = %q, want %q",
					c.sandboxState, got, c.want)
			}
		})
	}
}

func TestIsConvergenceState(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{StatePending, false},
		{StateCreating, true},
		{StateRunning, false}, // CRITICAL: running rows never enter failure path
		{StateStopping, true},
		{StateSleeping, false},
		{StateArchived, false},
		{StateReviving, true},
		{StateArchiving, true}, // real work (R2 upload) since the executor change
		{StateTerminating, true},
		{StateTerminated, false},
		{StateFailed, false},
		{StateError, false},
	}
	for _, c := range cases {
		t.Run(c.state, func(t *testing.T) {
			if got := IsConvergenceState(c.state); got != c.want {
				t.Fatalf("IsConvergenceState(%q) = %v, want %v", c.state, got, c.want)
			}
		})
	}
}

func TestClaimableStatesIncludeStateOnlyTransitions(t *testing.T) {
	if !containsLifecycleState(claimableStates, StateArchiving) {
		t.Fatalf("claimableStates must include %q so sleeping to archiving can converge", StateArchiving)
	}
}

func containsLifecycleState(states []string, want string) bool {
	for _, state := range states {
		if state == want {
			return true
		}
	}
	return false
}

// A sleep only commits once the workspace is durable. An object-storage ref
// goes to workspace_archive_ref and no local path is kept on the row.
func TestStep_Stopping_ObjectRefStoredAsArchiveRef(t *testing.T) {
	const objRef = "objstore:v1:tenants/tenant-1/sandboxes/sbx_test/workspace/g1.tar.gz#sha256=" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&size=10"
	fb := &fakeBackend{stopFn: func(context.Context, string) (string, error) { return objRef, nil }}
	row := newRow(StateStopping, DesireSleeping)
	row.WorkspaceSnapshotRef = nullable("/tmp/stale/trooper.tar.gz")

	next, err := Step(context.Background(), fb, row)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if next.LifecycleState != StateSleeping {
		t.Fatalf("state = %q, want sleeping", next.LifecycleState)
	}
	if next.WorkspaceArchiveRef.String != objRef || next.WorkspaceSnapshotRef.Valid {
		t.Fatalf("refs = (%v, %v), want archive=%q and no local ref", next.WorkspaceSnapshotRef, next.WorkspaceArchiveRef, objRef)
	}
}

// Upload failure must not produce a sleeping row: Step returns the error so
// the reconciler retries with the VM still running.
func TestStep_Stopping_UploadFailureDoesNotSleep(t *testing.T) {
	fb := &fakeBackend{stopFn: func(context.Context, string) (string, error) {
		return "", errors.New("persist workspace to object storage: upload failed")
	}}
	row := newRow(StateStopping, DesireSleeping)
	next, err := Step(context.Background(), fb, row)
	if err == nil {
		t.Fatal("Step succeeded although the workspace upload failed")
	}
	if next.LifecycleState != StateStopping {
		t.Fatalf("state = %q, want the row unchanged (stopping)", next.LifecycleState)
	}
}

// A workspace over the cap is refused: back to running, desired running,
// with the customer-facing reason as the notice.
func TestStep_Stopping_TooLargeReturnsToRunningWithNotice(t *testing.T) {
	fb := &fakeBackend{stopFn: func(context.Context, string) (string, error) {
		return "", sandbox.NewWorkspaceTooLarge(2<<30, 1<<30)
	}}
	row := newRow(StateStopping, DesireSleeping)
	next, err := Step(context.Background(), fb, row)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if next.LifecycleState != StateRunning || next.DesiredState != DesireRunning {
		t.Fatalf("got (%q, desired %q), want running/running", next.LifecycleState, next.DesiredState)
	}
	if !strings.Contains(next.Notice, "1 GiB limit") || !strings.HasPrefix(next.Error.String, sandbox.WorkspaceErrorCodeTooLarge) {
		t.Fatalf("notice=%q error=%q, want the size-cap reason", next.Notice, next.Error.String)
	}
}

// A missing or corrupt saved workspace parks the row in error with the
// customer message and the restore-failed code; it never retries into an
// empty boot.
func TestStep_Reviving_RestoreFailedParksInErrorWithNotice(t *testing.T) {
	fb := &fakeBackend{reviveFn: func(context.Context, string, string, string) (*sandbox.Instance, error) {
		return nil, sandbox.NewWorkspaceRestoreFailed("snapshot: not found")
	}}
	row := newRow(StateReviving, DesireRunning)
	row.WorkspaceArchiveRef = nullable("objstore:v1:k#sha256=x&size=1")
	next, err := Step(context.Background(), fb, row)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if next.LifecycleState != StateError || next.DesiredState != DesireRunning {
		t.Fatalf("got (%q, desired %q), want error with desired preserved", next.LifecycleState, next.DesiredState)
	}
	if next.Notice != sandbox.RestoreFailedCustomerMessage {
		t.Fatalf("notice = %q, want the customer restore-failed message", next.Notice)
	}
	if !strings.HasPrefix(next.Error.String, sandbox.WorkspaceErrorCodeRestoreFailed) {
		t.Fatalf("error = %q, want the restore-failed code prefix", next.Error.String)
	}
	if next.WorkspaceArchiveRef != row.WorkspaceArchiveRef {
		t.Fatal("refs must be kept on a restore-failed row for forensics")
	}
}

// A transient revive error (network) still retries through the
// convergence path rather than being marked permanently failed.
func TestStep_Reviving_TransientErrorRetries(t *testing.T) {
	fb := &fakeBackend{reviveFn: func(context.Context, string, string, string) (*sandbox.Instance, error) {
		return nil, errors.New("download saved workspace: connection reset")
	}}
	row := newRow(StateReviving, DesireRunning)
	if _, err := Step(context.Background(), fb, row); err == nil {
		t.Fatal("transient revive error must be returned for retry")
	}
}

// States that move the workspace to or from object storage get the longer
// transfer budget; every other state keeps the default.
func TestReconcilerTransferStatesGetLongerBudget(t *testing.T) {
	r := NewReconciler(nil, &fakeBackend{}, nil)
	if r.LeaseTimeout <= r.TransferRowTimeout {
		t.Fatalf("lease %s must outlive the transfer budget %s", r.LeaseTimeout, r.TransferRowTimeout)
	}
	for _, s := range []string{StateStopping, StateReviving, StateArchiving} {
		if got := r.rowTimeout(s); got != r.TransferRowTimeout {
			t.Fatalf("rowTimeout(%s) = %s, want %s", s, got, r.TransferRowTimeout)
		}
	}
	if got := r.rowTimeout(StateCreating); got != r.PerRowTimeout {
		t.Fatalf("rowTimeout(creating) = %s, want %s", got, r.PerRowTimeout)
	}
}

// A stop that saved nothing new (guest already gone, nothing adoptable)
// must not erase a ref the row already carries: it may be the only copy.
func TestStep_Stopping_EmptyRefNeverOverwritesExistingRef(t *testing.T) {
	fb := &fakeBackend{stopFn: func(context.Context, string) (string, error) { return "", nil }}
	row := newRow(StateStopping, DesireSleeping)
	row.WorkspaceArchiveRef = nullable("objstore:v1:k#sha256=x&size=1")
	row.WorkspaceSnapshotRef = nullable("/data/local.tar.gz")
	next, err := Step(context.Background(), fb, row)
	if err != nil {
		t.Fatal(err)
	}
	if next.WorkspaceArchiveRef != row.WorkspaceArchiveRef || next.WorkspaceSnapshotRef != row.WorkspaceSnapshotRef {
		t.Fatalf("refs = (%v, %v), want the existing refs kept", next.WorkspaceSnapshotRef, next.WorkspaceArchiveRef)
	}
}
