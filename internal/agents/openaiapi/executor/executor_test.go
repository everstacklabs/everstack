package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type fakeBackend struct {
	mu                          sync.Mutex
	owner                       Ownership
	instance                    *sandbox.Instance
	provisionErr                error
	commands                    []Command
	execErr                     error
	verifyErr                   error
	verifyExecErr               error
	destroyErr                  error
	destroyed                   int
	absent                      bool
	rejectOwnershipAfterDestroy bool
	files                       map[string][]byte
	started                     chan struct{}
	destroyedSignal             chan struct{}
	provisionConfig             sandbox.SandboxConfig
}

func newFakeBackend(owner Ownership) *fakeBackend {
	return &fakeBackend{owner: owner, instance: &sandbox.Instance{ID: "sbx-test", Status: sandbox.StatusRunning}, files: map[string][]byte{}, started: make(chan struct{}), destroyedSignal: make(chan struct{})}
}
func (f *fakeBackend) Provision(_ context.Context, owner Ownership, _ string, cfg sandbox.SandboxConfig) (*sandbox.Instance, error) {
	if owner != f.owner {
		return nil, errors.New("foreign owner")
	}
	f.provisionConfig = cfg
	return f.instance, f.provisionErr
}
func (f *fakeBackend) VerifyOwnership(_ context.Context, owner Ownership, id string) error {
	if owner != f.owner || id != f.instance.ID {
		return errors.New("foreign owner")
	}
	f.mu.Lock()
	absent := f.absent
	reject := f.rejectOwnershipAfterDestroy
	verifyErr := f.verifyErr
	f.mu.Unlock()
	if verifyErr != nil {
		return verifyErr
	}
	if absent && reject {
		return errors.New("sandbox not found")
	}
	return nil
}
func (f *fakeBackend) Exec(ctx context.Context, owner Ownership, id string, cmd Command) (*sandbox.ExecResult, error) {
	if err := f.VerifyOwnership(ctx, owner, id); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	n := len(f.commands)
	execErr := f.execErr
	verifyExecErr := f.verifyExecErr
	f.mu.Unlock()
	if n == 1 {
		if verifyExecErr != nil {
			return nil, verifyExecErr
		}
		return &sandbox.ExecResult{Stdout: "codex-cli " + ExecutorVersion, ExitCode: 0}, nil
	}
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	if execErr != nil {
		return nil, execErr
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *fakeBackend) ReadFile(ctx context.Context, owner Ownership, id, name string) ([]byte, error) {
	if err := f.VerifyOwnership(ctx, owner, id); err != nil {
		return nil, err
	}
	return append([]byte(nil), f.files[name]...), nil
}
func (f *fakeBackend) WriteFile(ctx context.Context, owner Ownership, id, name string, data []byte) error {
	if err := f.VerifyOwnership(ctx, owner, id); err != nil {
		return err
	}
	f.files[name] = append([]byte(nil), data...)
	return nil
}
func (f *fakeBackend) ListFiles(context.Context, Ownership, string, string) ([]sandbox.FileInfo, error) {
	return nil, nil
}
func (f *fakeBackend) Destroy(_ context.Context, owner Ownership, id, _ string) error {
	f.mu.Lock()
	destroyErr := f.destroyErr
	f.mu.Unlock()
	if destroyErr != nil {
		return destroyErr
	}
	if err := f.VerifyOwnership(context.Background(), owner, id); err != nil {
		return err
	}
	f.mu.Lock()
	f.destroyed++
	f.absent = true
	select {
	case <-f.destroyedSignal:
	default:
		close(f.destroyedSignal)
	}
	f.mu.Unlock()
	return nil
}

func validRequest(owner Ownership) ProvisionRequest {
	return ProvisionRequest{Ownership: owner, LocalSessionID: "local-1", UpstreamEnvironmentID: "env-1", RemoteURL: "https://api.openai.com/v1/environments/remote?token=a%2Bb", ExecutorAPIKey: "executor-secret", WorkspaceDirectory: WorkspaceDirectory, Image: LocalExecutorImage}
}

func TestProvisionReturnsSandboxWhenOwnershipFailureCleanupFails(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
	backend := newFakeBackend(owner)
	backend.verifyErr = errors.New("ownership unavailable")
	backend.destroyErr = errors.New("destroy unavailable")
	env, err := New(backend).Provision(context.Background(), validRequest(owner))
	if err == nil || !strings.Contains(err.Error(), "ownership unavailable") || !strings.Contains(err.Error(), "destroy unavailable") {
		t.Fatalf("error = %v", err)
	}
	if env == nil || env.SandboxID != backend.instance.ID || env.Status != StatusFailed {
		t.Fatalf("partial environment = %#v", env)
	}
}

func TestProvisionCompensatesPartialBackendProvisionFailure(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
	backend := newFakeBackend(owner)
	backend.provisionErr = errors.New("persist ownership")
	backend.destroyErr = errors.New("destroy unavailable")
	env, err := New(backend).Provision(context.Background(), validRequest(owner))
	if err == nil || !strings.Contains(err.Error(), "persist ownership") || !strings.Contains(err.Error(), "destroy unavailable") {
		t.Fatalf("error = %v", err)
	}
	if env == nil || env.SandboxID != backend.instance.ID {
		t.Fatalf("partial environment = %#v", env)
	}
}

func TestProvisionPartialBackendFailureWithSuccessfulCompensationReturnsNoSandbox(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
	backend := newFakeBackend(owner)
	backend.provisionErr = errors.New("persist ownership")
	env, err := New(backend).Provision(context.Background(), validRequest(owner))
	if err == nil || !strings.Contains(err.Error(), "persist ownership") {
		t.Fatalf("error = %v", err)
	}
	if env != nil {
		t.Fatalf("environment = %#v, want nil after successful cleanup", env)
	}
}

func TestProvisionReturnsSandboxWhenExecutorVerificationCleanupFails(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
	backend := newFakeBackend(owner)
	backend.verifyExecErr = errors.New("codex unavailable")
	backend.destroyErr = errors.New("destroy unavailable")
	env, err := New(backend).Provision(context.Background(), validRequest(owner))
	if err == nil || !strings.Contains(err.Error(), "codex unavailable") || !strings.Contains(err.Error(), "destroy unavailable") {
		t.Fatalf("error = %v", err)
	}
	if env == nil || env.SandboxID != backend.instance.ID || env.Status != StatusFailed {
		t.Fatalf("partial environment = %#v", env)
	}
}

func TestProvisionSuccessfulCompensationReturnsNoSandbox(t *testing.T) {
	owner := Ownership{OrganizationID: "org", InstanceID: "instance"}
	backend := newFakeBackend(owner)
	backend.verifyExecErr = errors.New("codex unavailable")
	env, err := New(backend).Provision(context.Background(), validRequest(owner))
	if err == nil || !strings.Contains(err.Error(), "codex unavailable") {
		t.Fatalf("error = %v", err)
	}
	if env != nil {
		t.Fatalf("environment = %#v, want nil after successful cleanup", env)
	}
	if backend.destroyed != 1 {
		t.Fatalf("destroy calls = %d, want 1", backend.destroyed)
	}
}

type referenceBridge struct {
	input    []byte
	exported []byte
}

func (b *referenceBridge) ResolveInput(_ context.Context, r InputRequest) (*InputFile, error) {
	return &InputFile{ReferenceID: r.ReferenceID, Filename: "input.txt", Content: append([]byte(nil), b.input...), SHA256: checksum(b.input)}, nil
}
func (b *referenceBridge) Export(_ context.Context, r ExportRequest) (*Artifact, error) {
	b.exported = append([]byte(nil), r.Content...)
	return &Artifact{ObjectID: "object", Filename: r.Filename, SizeBytes: int64(len(r.Content)), SHA256: r.SHA256}, nil
}

func TestAlternateManagerUsesPersistedSandboxReferenceAndDeletes(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	bridge := &referenceBridge{input: []byte("input")}
	if _, err := manager.AttachInputReference(context.Background(), owner, "session", backend.instance.ID, "object", "inputs/a.txt", bridge); err != nil {
		t.Fatal(err)
	}
	backend.files[WorkspaceDirectory+"/result.txt"] = []byte("result")
	if _, err := manager.ExportWorkspaceFileReference(context.Background(), owner, "session", backend.instance.ID, "result.txt", bridge); err != nil {
		t.Fatal(err)
	}
	if string(bridge.exported) != "result" {
		t.Fatalf("exported=%q", bridge.exported)
	}
	if err := manager.DeleteReference(context.Background(), owner, "session", backend.instance.ID); err != nil {
		t.Fatal(err)
	}
	if backend.destroyed != 1 {
		t.Fatalf("destroyed=%d, want 1", backend.destroyed)
	}
}

func TestProvisionPreservesRemoteURLAndDoesNotPutCredentialInCommand(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	req := validRequest(owner)
	req.FenceStartupTimeout = func(context.Context) error { return nil }
	req.RemoteURL = "https://api.openai.com/x;touch%20/tmp/pwned?next=$HOME"
	env, err := manager.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != StatusPending {
		t.Fatalf("status = %s", env.Status)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	backend.mu.Lock()
	command := backend.commands[1]
	backend.mu.Unlock()
	want := []string{"codex", "exec-server", "--remote", req.RemoteURL, "--environment-id", "env-1"}
	if !reflect.DeepEqual(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
	if strings.Contains(strings.Join(command.Argv, " "), req.ExecutorAPIKey) {
		t.Fatal("executor key leaked into command")
	}
	if command.Env["CODEX_API_KEY"] != req.ExecutorAPIKey || !command.SilentLog {
		t.Fatal("executor key was not passed through silent process environment")
	}
	if err := manager.Delete(context.Background(), owner, req.LocalSessionID); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionRequiresExplicitVerifiedImage(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	req := validRequest(owner)
	req.Image = ""
	if _, err := New(newFakeBackend(owner)).Provision(context.Background(), req); err == nil {
		t.Fatal("provision accepted an unspecified executor image")
	}
}

func TestProvisionUsesValidManagedNanoProfile(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	req := validRequest(owner)
	if _, err := manager.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.ValidateManagedMachineProfile(backend.provisionConfig, "free"); err != nil {
		t.Fatalf("provision config is not a supported managed profile: %v", err)
	}
	if backend.provisionConfig.CPULimit != 0.5 || backend.provisionConfig.MemoryMB != 512 || backend.provisionConfig.DiskMB != 20*1024 {
		t.Fatalf("provision config = %.1f CPU / %d MiB / %d MiB disk, want nano", backend.provisionConfig.CPULimit, backend.provisionConfig.MemoryMB, backend.provisionConfig.DiskMB)
	}
	if backend.provisionConfig.TimeoutSeconds != 0 {
		t.Fatalf("provision timeout = %d seconds, want durable session-owned lifetime", backend.provisionConfig.TimeoutSeconds)
	}
	if err := manager.Delete(context.Background(), owner, req.LocalSessionID); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessOnlyChangesFromUpstreamObservation(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	manager := New(newFakeBackend(owner))
	req := validRequest(owner)
	if _, err := manager.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got, err := manager.Status(context.Background(), owner, req.LocalSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Fatalf("sandbox boot marked environment %s", got.Status)
	}
	got, err = manager.Observe(context.Background(), ObserveRequest{Ownership: owner, LocalSessionID: req.LocalSessionID, Status: StatusConnected})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConnected {
		t.Fatalf("observed status = %s", got.Status)
	}
	_ = manager.Delete(context.Background(), owner, req.LocalSessionID)
}

func TestForeignInstanceCannotControlOrReadWorkspace(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	manager := New(newFakeBackend(owner))
	req := validRequest(owner)
	if _, err := manager.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	foreign := Ownership{OrganizationID: "org-1", InstanceID: "inst-2"}
	if _, err := manager.Status(context.Background(), foreign, req.LocalSessionID); err == nil {
		t.Fatal("foreign status succeeded")
	}
	if _, err := manager.ReadWorkspaceFile(context.Background(), foreign, req.LocalSessionID, "result.txt"); err == nil {
		t.Fatal("foreign read succeeded")
	}
	if err := manager.Delete(context.Background(), foreign, req.LocalSessionID); err == nil {
		t.Fatal("foreign delete succeeded")
	}
	_ = manager.Delete(context.Background(), owner, req.LocalSessionID)
}

func TestStopWinsExecutorExitRaceAndIsIdempotent(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	req := validRequest(owner)
	if _, err := manager.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	<-backend.started
	if err := manager.Stop(context.Background(), owner, req.LocalSessionID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background(), owner, req.LocalSessionID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	got, err := manager.Status(context.Background(), owner, req.LocalSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped {
		t.Fatalf("status after race = %s", got.Status)
	}
	backend.mu.Lock()
	destroyed := backend.destroyed
	backend.mu.Unlock()
	if destroyed != 1 {
		t.Fatalf("destroy calls = %d", destroyed)
	}
}

func TestDeleteAfterSuccessfulStartupTimeoutCleanupIsIdempotent(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	backend.rejectOwnershipAfterDestroy = true
	manager := New(backend)
	manager.startupTimeout = 10 * time.Millisecond
	req := validRequest(owner)
	req.FenceStartupTimeout = func(context.Context) error { return nil }
	if _, err := manager.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.destroyedSignal:
	case <-time.After(time.Second):
		t.Fatal("startup timeout did not destroy sandbox")
	}
	waitForCleanupDone(t, manager, req.LocalSessionID)

	got, err := manager.Status(context.Background(), owner, req.LocalSessionID)
	if err != nil {
		t.Fatalf("status after timeout cleanup: %v", err)
	}
	if got.Status != StatusFailed || got.Error != "executor connection timed out" {
		t.Fatalf("status after timeout cleanup = %#v", got)
	}
	if err := manager.DeleteReference(context.Background(), owner, req.LocalSessionID, backend.instance.ID); err != nil {
		t.Fatalf("delete reference after timeout cleanup: %v", err)
	}
	backend.mu.Lock()
	destroyed := backend.destroyed
	backend.mu.Unlock()
	if destroyed != 1 {
		t.Fatalf("destroy calls = %d, want 1", destroyed)
	}
}

func waitForCleanupDone(t *testing.T, manager *Manager, localSessionID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.mu.RLock()
		b := manager.bindings[localSessionID]
		done := b != nil && b.cleanupDone
		manager.mu.RUnlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("manager did not record completed cleanup")
}

type retryCleanupBackend struct {
	*fakeBackend
	mu      sync.Mutex
	attempt int
	started chan struct{}
	release chan struct{}
}

func (b *retryCleanupBackend) Destroy(ctx context.Context, owner Ownership, id, key string) error {
	b.mu.Lock()
	b.attempt++
	attempt := b.attempt
	b.mu.Unlock()
	if attempt == 1 {
		close(b.started)
		select {
		case <-b.release:
			return errors.New("transient destroy failure")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.fakeBackend.Destroy(ctx, owner, id, key)
}

func TestCleanupWaitersObserveFailedAttemptWhileLaterRetrySucceeds(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := &retryCleanupBackend{
		fakeBackend: newFakeBackend(owner),
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	manager := New(backend)
	b := &binding{
		environment: Environment{SandboxID: backend.instance.ID},
		owner:       owner,
		sandboxKey:  "sandbox-key",
	}
	first := make(chan error, 1)
	go func() { first <- manager.cleanup(context.Background(), b) }()
	<-backend.started

	const waiterCount = 32
	waiters := make(chan error, waiterCount)
	for range waiterCount {
		go func() { waiters <- manager.cleanup(context.Background(), b) }()
	}
	// Give waiters time to join the blocked first attempt before releasing it.
	time.Sleep(10 * time.Millisecond)
	close(backend.release)
	if err := <-first; err == nil {
		t.Fatal("first cleanup unexpectedly succeeded")
	}
	if err := manager.cleanup(context.Background(), b); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	for range waiterCount {
		if err := <-waiters; err == nil || err.Error() != "transient destroy failure" {
			t.Fatalf("waiter observed wrong attempt result: %v", err)
		}
	}
	backend.mu.Lock()
	attempts := backend.attempt
	backend.mu.Unlock()
	if attempts != 2 {
		t.Fatalf("destroy attempts = %d, want 2", attempts)
	}
}

func TestReconcileRestoresBindingAndExecutorAfterManagerRestart(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	manager := New(backend)
	remoteURL := "https://api.openai.com/reconnect?opaque=a%2Bb%3D"
	env, err := manager.Reconcile(context.Background(), ReconcileRequest{
		Environment: Environment{
			SandboxID:             backend.instance.ID,
			LocalSessionID:        "local-restored",
			UpstreamEnvironmentID: "env-restored",
			Status:                StatusConnected,
			ExecutorVersion:       ExecutorVersion,
		},
		Ownership:      owner,
		RemoteURL:      remoteURL,
		ExecutorAPIKey: "fresh-executor-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Status != StatusPending {
		t.Fatalf("restored status=%s, want pending until upstream reconnect event", env.Status)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("restored executor did not start")
	}
	backend.mu.Lock()
	command := backend.commands[1]
	backend.mu.Unlock()
	want := []string{"codex", "exec-server", "--remote", remoteURL, "--environment-id", "env-restored"}
	if !reflect.DeepEqual(command.Argv, want) || command.Env["CODEX_API_KEY"] != "fresh-executor-secret" || !command.SilentLog {
		t.Fatalf("restored command=%#v", command)
	}
	if err := manager.Delete(context.Background(), owner, "local-restored"); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialIsRedactedFromProvisionError(t *testing.T) {
	owner := Ownership{OrganizationID: "org-1", InstanceID: "inst-1"}
	backend := newFakeBackend(owner)
	backend.instance = nil
	bad := &failingProvisionBackend{message: "provider rejected executor-secret"}
	manager := New(bad)
	_, err := manager.Provision(context.Background(), validRequest(owner))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "executor-secret") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("unredacted error: %v", err)
	}
}

type failingProvisionBackend struct{ message string }

func (f *failingProvisionBackend) Provision(context.Context, Ownership, string, sandbox.SandboxConfig) (*sandbox.Instance, error) {
	return nil, errors.New(f.message)
}
func (*failingProvisionBackend) VerifyOwnership(context.Context, Ownership, string) error { return nil }
func (*failingProvisionBackend) Exec(context.Context, Ownership, string, Command) (*sandbox.ExecResult, error) {
	return nil, nil
}
func (*failingProvisionBackend) ReadFile(context.Context, Ownership, string, string) ([]byte, error) {
	return nil, nil
}
func (*failingProvisionBackend) WriteFile(context.Context, Ownership, string, string, []byte) error {
	return nil
}
func (*failingProvisionBackend) ListFiles(context.Context, Ownership, string, string) ([]sandbox.FileInfo, error) {
	return nil, nil
}
func (*failingProvisionBackend) Destroy(context.Context, Ownership, string, string) error { return nil }

func TestResolveWorkspacePathRejectsTraversal(t *testing.T) {
	for _, candidate := range []string{"../secret", "/workspace/../../etc/passwd", "/etc/passwd", ""} {
		if _, err := ResolveWorkspacePath(candidate); err == nil {
			t.Errorf("ResolveWorkspacePath(%q) succeeded", candidate)
		}
	}
	if got, err := ResolveWorkspacePath("reports/out.txt"); err != nil || got != "/workspace/reports/out.txt" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolvedWorkspacePathRejectsGuestSymlinkEscape(t *testing.T) {
	// ManagerBackend feeds the guest's realpath output through this final
	// boundary. A /workspace/link -> /etc symlink therefore fails closed.
	if _, err := resolvedWorkspacePath("/etc/passwd\n"); err == nil {
		t.Fatal("guest symlink escape was accepted")
	}
}

func TestSupportedBackendsRequireEnforcedEgressAndSafeCredentialInjection(t *testing.T) {
	for _, name := range []string{"firecracker", "firecracker-agent"} {
		if !SupportedBackend(name) {
			t.Errorf("%s should be supported", name)
		}
	}
	if SupportedBackend("kubernetes") {
		t.Fatal("kubernetes exposes ExecRequest.Env through command arguments")
	}
	if SupportedBackend("docker") {
		t.Fatal("docker has no production-wired fail-closed whitelist egress controller")
	}
}
