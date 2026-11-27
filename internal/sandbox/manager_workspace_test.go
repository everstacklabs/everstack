package sandbox

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
	"github.com/everstacklabs/everstack/internal/storage"
)

// workspaceFakeBackend implements Backend + Snapshotter and records the
// order of side effects so tests can assert "upload before destroy".
type workspaceFakeBackend struct {
	Backend
	log *callLog

	tarball     []byte
	snapshotErr error
	guestGone   bool
	restored    []byte
	created     int
	destroyed   int
}

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(c string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
}

func (l *callLog) index(c string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, v := range l.calls {
		if v == c {
			return i
		}
	}
	return -1
}

func (b *workspaceFakeBackend) Name() string { return "workspace-fake" }

func (b *workspaceFakeBackend) Snapshot(_ context.Context, _ string, _ string, destPath string) error {
	b.log.add("snapshot")
	if b.snapshotErr != nil {
		return b.snapshotErr
	}
	return os.WriteFile(destPath, b.tarball, 0o600)
}

func (b *workspaceFakeBackend) Restore(_ context.Context, _ string, srcPath string, _ string) error {
	b.log.add("restore")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	b.restored = data
	return nil
}

func (b *workspaceFakeBackend) Create(_ context.Context, id string, _ InstanceConfig) (*Instance, error) {
	b.log.add("create")
	b.created++
	return &Instance{ID: id, ContainerID: "c-" + id}, nil
}

func (b *workspaceFakeBackend) Destroy(context.Context, string) error {
	b.log.add("destroy")
	b.destroyed++
	return nil
}

func (b *workspaceFakeBackend) Status(_ context.Context, id string) (*Instance, error) {
	if b.guestGone {
		return nil, errors.New("sandbox not found")
	}
	return &Instance{ID: id}, nil
}

// fakeWorkspaceStore is a snapshot.Store + snapshot.WorkspaceStore keeping
// objects in memory under the real key layout.
type fakeWorkspaceStore struct {
	snapshot.Disabled
	log *callLog

	mu         sync.Mutex
	objects    map[string][]byte
	generation int
	putErr     error
	getErr     error
	latestErr  error
	deleteErr  error
	deletedBy  []string // "tenant/sandbox"
}

func newFakeWorkspaceStore(log *callLog) *fakeWorkspaceStore {
	return &fakeWorkspaceStore{log: log, objects: map[string][]byte{}}
}

func (s *fakeWorkspaceStore) put(owner snapshot.WorkspaceOwner, data []byte) snapshot.WorkspaceRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	ref := snapshot.WorkspaceRef{
		Key:    fmt.Sprintf("%sg%03d.tar.gz", snapshot.RunPrefix(owner), s.generation),
		SHA256: storage.NewSHA256Checksum(data).Value, SizeBytes: int64(len(data)),
	}
	s.objects[ref.Key] = data
	return ref
}

func (s *fakeWorkspaceStore) PutWorkspace(_ context.Context, owner snapshot.WorkspaceOwner, localPath string) (snapshot.WorkspaceRef, error) {
	s.log.add("upload")
	if s.putErr != nil {
		return snapshot.WorkspaceRef{}, s.putErr
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return snapshot.WorkspaceRef{}, err
	}
	return s.put(owner, data), nil
}

func (s *fakeWorkspaceStore) GetWorkspace(_ context.Context, owner snapshot.WorkspaceOwner, ref snapshot.WorkspaceRef, dst io.Writer) error {
	s.log.add("download")
	if s.getErr != nil {
		return s.getErr
	}
	if !strings.HasPrefix(ref.Key, snapshot.IncarnationPrefix(owner)) {
		return snapshot.ErrWorkspaceRefForeign
	}
	s.mu.Lock()
	data, ok := s.objects[ref.Key]
	s.mu.Unlock()
	if !ok {
		return snapshot.ErrSnapshotMissing
	}
	if storage.NewSHA256Checksum(data).Value != ref.SHA256 {
		return snapshot.ErrWorkspaceCorrupt
	}
	_, err := dst.Write(data)
	return err
}

func (s *fakeWorkspaceStore) PruneWorkspaces(_ context.Context, tenantID, sandboxID string, keep snapshot.WorkspaceRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.objects {
		if strings.HasPrefix(k, snapshot.WorkspacePrefix(tenantID, sandboxID)) && k != keep.Key {
			delete(s.objects, k)
		}
	}
	return nil
}

func (s *fakeWorkspaceStore) newestUnder(prefix string) string {
	newest := ""
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) && k > newest {
			newest = k
		}
	}
	return newest
}

func (s *fakeWorkspaceStore) LatestWorkspace(_ context.Context, owner snapshot.WorkspaceOwner, dst io.Writer) (snapshot.WorkspaceRef, bool, error) {
	s.log.add("latest")
	if s.latestErr != nil {
		return snapshot.WorkspaceRef{}, false, s.latestErr
	}
	s.mu.Lock()
	k := s.newestUnder(snapshot.RunPrefix(owner))
	data := s.objects[k]
	s.mu.Unlock()
	if k == "" {
		return snapshot.WorkspaceRef{}, false, nil
	}
	if _, err := dst.Write(data); err != nil {
		return snapshot.WorkspaceRef{}, false, err
	}
	return snapshot.WorkspaceRef{Key: k, SHA256: storage.NewSHA256Checksum(data).Value, SizeBytes: int64(len(data))}, true, nil
}

func (s *fakeWorkspaceStore) HasWorkspace(_ context.Context, owner snapshot.WorkspaceOwner) (bool, error) {
	if s.latestErr != nil {
		return false, s.latestErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newestUnder(snapshot.RunPrefix(owner)) != "", nil
}

func (s *fakeWorkspaceStore) DeleteIncarnation(_ context.Context, owner snapshot.WorkspaceOwner) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.objects {
		if strings.HasPrefix(k, snapshot.IncarnationPrefix(owner)) {
			delete(s.objects, k)
		}
	}
	return nil
}

func (s *fakeWorkspaceStore) Delete(_ context.Context, tenantID, sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletedBy = append(s.deletedBy, tenantID+"/"+sandboxID)
	for k := range s.objects {
		if strings.HasPrefix(k, snapshot.WorkspacePrefix(tenantID, sandboxID)) {
			delete(s.objects, k)
		}
	}
	return nil
}

func (s *fakeWorkspaceStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

var _ snapshot.WorkspaceStore = (*fakeWorkspaceStore)(nil)

// curOwner is the owner identity the sqlmock rows report: the sandbox's
// current incarnation and run.
var curOwner = snapshot.WorkspaceOwner{TenantID: "tenant-a", SandboxID: "sbx-1", Incarnation: "inc-cur", RunID: "run-cur"}

func newWorkspaceTestManager(t *testing.T, backend Backend) *SandboxManager {
	t.Helper()
	m := &SandboxManager{
		backend:            backend,
		instances:          map[string]*Instance{},
		instancesBySandbox: map[string]*Instance{},
		troopers:           map[string]*Instance{},
	}
	m.globalConfig.DataDir = t.TempDir()
	return m
}

// withSQL attaches a sqlmock DB that answers the workspace owner lookup
// (current incarnation and run, plus the given refs) any number of times.
// Other statements are unexpected and fail, which the code under test
// treats as best-effort.
func withSQL(t *testing.T, m *SandboxManager, snapshotRef, archiveRef string) sqlmock.Sqlmock {
	t.Helper()
	return withSQLN(t, m, snapshotRef, archiveRef, 4)
}

// withSQLN is withSQL with exactly n owner lookups expected, so
// ExpectationsWereMet can assert the other statements ran.
func withSQLN(t *testing.T, m *SandboxManager, snapshotRef, archiveRef string, n int) sqlmock.Sqlmock {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rawDB.Close() })
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < n; i++ {
		mock.ExpectQuery(`SELECT tenant_id, workspace_incarnation, workspace_run_id`).
			WithArgs("sbx-1").
			WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "workspace_incarnation", "workspace_run_id", "snapshot_ref", "archive_ref"}).
				AddRow(curOwner.TenantID, curOwner.Incarnation, curOwner.RunID, snapshotRef, archiveRef))
	}
	m.db = sqlx.NewDb(rawDB, "sqlmock")
	return mock
}

func runningInstance(m *SandboxManager) {
	inst := &Instance{
		ID:     "sbx-1",
		Status: StatusRunning,
		Config: InstanceConfig{TenantID: "tenant-a", SessionID: "sess-1"},
	}
	m.instances["sess-1"] = inst
	m.instancesBySandbox["sbx-1"] = inst
}

// setTestWorkspaceStore wires a store without starting the periodic VM
// snapshot scheduler that SetSnapshotStore launches.
func setTestWorkspaceStore(m *SandboxManager, store snapshot.Store) {
	m.r2SnapshotMu.Lock()
	m.r2Snapshots = store
	m.r2SnapshotMu.Unlock()
}

func TestExecuteStopUploadsBeforeDestroy(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log, tarball: []byte("workspace-bytes")}
	m := newWorkspaceTestManager(t, backend)
	withSQL(t, m, "", "")
	store := newFakeWorkspaceStore(log)
	setTestWorkspaceStore(m, store)
	runningInstance(m)

	ref, err := m.ExecuteStop(context.Background(), "sbx-1")
	if err != nil {
		t.Fatalf("ExecuteStop: %v", err)
	}
	parsed, ok, perr := snapshot.ParseWorkspaceRef(ref)
	if !ok || perr != nil {
		t.Fatalf("ExecuteStop returned %q, want an object-storage ref (err %v)", ref, perr)
	}
	if !strings.HasPrefix(parsed.Key, snapshot.RunPrefix(curOwner)) {
		t.Fatalf("ref key %q is not under the current incarnation and run %q", parsed.Key, snapshot.RunPrefix(curOwner))
	}
	up, destroy := log.index("upload"), log.index("destroy")
	if up < 0 || destroy < 0 || up > destroy {
		t.Fatalf("call order %v: the upload must complete before the VM is destroyed", log.calls)
	}
	if _, err := os.Stat(filepath.Join(m.dataDir(), "sbx-1", "trooper.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("local tarball should be removed once the durable copy is verified (stat err %v)", err)
	}
}

// The generation the last wake consumed is kept until the next sleep's
// upload is verified, then pruned. A failed upload keeps it.
func TestConsumedGenerationKeptUntilNextVerifiedSleep(t *testing.T) {
	prevRun := curOwner
	prevRun.RunID = "run-prev"

	t.Run("upload fails: consumed copy kept", func(t *testing.T) {
		log := &callLog{}
		m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log, tarball: []byte("new")})
		withSQL(t, m, "", "")
		store := newFakeWorkspaceStore(log)
		consumed := store.put(prevRun, []byte("consumed-by-last-wake"))
		store.putErr = errors.New("r2 unavailable")
		setTestWorkspaceStore(m, store)
		runningInstance(m)

		if _, err := m.ExecuteStop(context.Background(), "sbx-1"); err == nil {
			t.Fatal("stop succeeded although the upload failed")
		}
		if !store.has(consumed.Key) {
			t.Fatal("consumed generation deleted although no newer copy was verified")
		}
	})
	t.Run("upload verified: consumed copy pruned", func(t *testing.T) {
		log := &callLog{}
		m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log, tarball: []byte("new")})
		withSQL(t, m, "", "")
		store := newFakeWorkspaceStore(log)
		consumed := store.put(prevRun, []byte("consumed-by-last-wake"))
		setTestWorkspaceStore(m, store)
		runningInstance(m)

		ref, err := m.ExecuteStop(context.Background(), "sbx-1")
		if err != nil {
			t.Fatal(err)
		}
		parsed, _, _ := snapshot.ParseWorkspaceRef(ref)
		if store.has(consumed.Key) || !store.has(parsed.Key) {
			t.Fatalf("after a verified sleep want only the new generation, have %v", store.objects)
		}
	})
}

func TestExecuteStopUploadFailureKeepsVMRunning(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log, tarball: []byte("workspace-bytes")}
	m := newWorkspaceTestManager(t, backend)
	withSQL(t, m, "", "")
	store := newFakeWorkspaceStore(log)
	store.putErr = errors.New("r2 unavailable")
	setTestWorkspaceStore(m, store)
	runningInstance(m)

	ref, err := m.ExecuteStop(context.Background(), "sbx-1")
	if err == nil {
		t.Fatalf("ExecuteStop succeeded (ref %q) although the upload failed", ref)
	}
	if backend.destroyed != 0 {
		t.Fatalf("VM destroyed %d times after a failed upload; data would be lost", backend.destroyed)
	}
	if errors.Is(err, ErrWorkspaceTooLarge) || errors.Is(err, ErrWorkspaceRestoreFailed) {
		t.Fatalf("upload failure must be a retryable error, got %v", err)
	}
	if got := m.instancesBySandbox["sbx-1"].Status; got != StatusRunning {
		t.Fatalf("in-memory status = %q, want running", got)
	}
}

func TestExecuteStopRefusesOversizedWorkspace(t *testing.T) {
	t.Run("over the configured cap", func(t *testing.T) {
		log := &callLog{}
		backend := &workspaceFakeBackend{log: log, tarball: make([]byte, 4096)}
		m := newWorkspaceTestManager(t, backend)
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		m.SetWorkspaceMaxBytes(1024)
		runningInstance(m)

		_, err := m.ExecuteStop(context.Background(), "sbx-1")
		if !errors.Is(err, ErrWorkspaceTooLarge) {
			t.Fatalf("err = %v, want ErrWorkspaceTooLarge", err)
		}
		we, ok := AsWorkspaceError(err)
		if !ok || !strings.Contains(we.CustomerMessage, "limit") || strings.Contains(we.CustomerMessage, "\u2014") {
			t.Fatalf("customer message = %+v, want a plain size-limit message", we)
		}
		if log.index("upload") >= 0 || backend.destroyed != 0 {
			t.Fatalf("calls %v: an oversized workspace must not be uploaded or have its VM destroyed", log.calls)
		}
	})
	t.Run("transport refuses the archive", func(t *testing.T) {
		log := &callLog{}
		backend := &workspaceFakeBackend{log: log, snapshotErr: errors.New("snapshot: archive 2147483648 bytes exceeds cap 1073741824")}
		m := newWorkspaceTestManager(t, backend)
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		runningInstance(m)

		_, err := m.ExecuteStop(context.Background(), "sbx-1")
		if !errors.Is(err, ErrWorkspaceTooLarge) {
			t.Fatalf("err = %v, want ErrWorkspaceTooLarge", err)
		}
		if backend.destroyed != 0 {
			t.Fatal("VM destroyed after the transport refused an oversized archive")
		}
	})
}

func TestSetWorkspaceMaxBytesClampsToTransportCeiling(t *testing.T) {
	m := &SandboxManager{}
	if got := m.WorkspaceMaxBytes(); got != DefaultWorkspaceMaxBytes {
		t.Fatalf("default = %d, want %d", got, DefaultWorkspaceMaxBytes)
	}
	m.SetWorkspaceMaxBytes(10 << 30)
	if got := m.WorkspaceMaxBytes(); got != WorkspaceSnapshotTransportMaxBytes {
		t.Fatalf("clamped = %d, want %d", got, WorkspaceSnapshotTransportMaxBytes)
	}
	m.SetWorkspaceMaxBytes(256 << 20)
	if got := m.WorkspaceMaxBytes(); got != 256<<20 {
		t.Fatalf("set = %d, want %d", got, 256<<20)
	}
}

// Unconfigured object storage keeps today's behaviour: the local tarball
// path is the ref and the stop proceeds.
func TestExecuteStopUnconfiguredFallsBackToLocalTarball(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log, tarball: []byte("workspace-bytes")}
	m := newWorkspaceTestManager(t, backend)
	runningInstance(m)

	if m.WorkspacePersistenceConfigured() {
		t.Fatal("persistence reported configured with no store")
	}
	ref, err := m.ExecuteStop(context.Background(), "sbx-1")
	if err != nil {
		t.Fatalf("ExecuteStop: %v", err)
	}
	want := filepath.Join(m.dataDir(), "sbx-1", "trooper.tar.gz")
	if ref != want {
		t.Fatalf("ref = %q, want the local tarball path %q", ref, want)
	}
	if data, err := os.ReadFile(ref); err != nil || string(data) != "workspace-bytes" {
		t.Fatalf("local tarball not kept: %v", err)
	}
	if backend.destroyed != 1 {
		t.Fatalf("destroyed = %d, want 1", backend.destroyed)
	}
}

// newReviveManager wires a manager whose saved config and workspace owner
// resolve via sqlmock. Tier "enterprise" skips the concurrency-slot query.
func newReviveManager(t *testing.T, backend Backend) *SandboxManager {
	t.Helper()
	m := newWorkspaceTestManager(t, backend)
	mock := withSQL(t, m, "", "")
	cfg, _ := json.Marshal(InstanceConfig{TenantID: "tenant-a", SessionID: "sess-1"})
	mock.ExpectQuery(`SELECT config, image FROM sandbox_instances`).
		WithArgs("sbx-1").
		WillReturnRows(sqlmock.NewRows([]string{"config", "image"}).AddRow(cfg, "img"))
	m.SetTenantTierResolver(func(string) string { return "enterprise" })
	return m
}

func writeSrc(t *testing.T, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "w.tar.gz")
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecuteReviveRestoresVerifiedWorkspace(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log}
	m := newReviveManager(t, backend)
	store := newFakeWorkspaceStore(log)
	setTestWorkspaceStore(m, store)
	ref := store.put(curOwner, []byte("saved-workspace"))

	if _, err := m.ExecuteRevive(context.Background(), "sbx-1", "", ref.String()); err != nil {
		t.Fatalf("ExecuteRevive: %v", err)
	}
	if string(backend.restored) != "saved-workspace" {
		t.Fatalf("restored %q, want the saved workspace", backend.restored)
	}
	if dl, create := log.index("download"), log.index("create"); dl < 0 || dl > create {
		t.Fatalf("calls %v: the workspace must be fetched and verified before the VM is created", log.calls)
	}
	if _, err := os.Stat(filepath.Join(m.dataDir(), "sbx-1", "workspace-restore.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("downloaded restore tarball should be removed after the restore")
	}
	if !store.has(ref.Key) {
		t.Fatal("the consumed generation must be kept after a wake")
	}
}

func TestExecuteReviveFailsLoudlyWhenWorkspaceLost(t *testing.T) {
	missing := snapshot.WorkspaceRef{
		Key:       snapshot.RunPrefix(curOwner) + "g999.tar.gz",
		SHA256:    strings.Repeat("a", 64),
		SizeBytes: 10,
	}
	oldIncarnation := curOwner
	oldIncarnation.Incarnation = "inc-old"
	for _, tc := range []struct {
		name        string
		snapshotRef string
		archiveRef  func(*fakeWorkspaceStore) string
		noStore     bool
	}{
		{name: "missing object", archiveRef: func(*fakeWorkspaceStore) string { return missing.String() }},
		{name: "checksum mismatch", archiveRef: func(s *fakeWorkspaceStore) string {
			ref := s.put(curOwner, []byte("uploaded"))
			s.objects[ref.Key] = []byte("not what was uploaded")
			return ref.String()
		}},
		{name: "ref from a previous incarnation of this id", archiveRef: func(s *fakeWorkspaceStore) string {
			return s.put(oldIncarnation, []byte("previous sandbox")).String()
		}},
		{name: "ref belongs to another tenant", archiveRef: func(s *fakeWorkspaceStore) string {
			other := curOwner
			other.TenantID = "tenant-b"
			return s.put(other, []byte("other tenant")).String()
		}},
		{name: "legacy local tarball gone after a gateway restart", snapshotRef: "/tmp/everstack/sandboxes/sbx-1/trooper.tar.gz"},
		{name: "object ref but storage unconfigured", archiveRef: func(*fakeWorkspaceStore) string { return missing.String() }, noStore: true},
		{name: "malformed ref", archiveRef: func(*fakeWorkspaceStore) string { return "objstore:v1:broken" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &callLog{}
			backend := &workspaceFakeBackend{log: log}
			m := newReviveManager(t, backend)
			store := newFakeWorkspaceStore(log)
			archiveRef := ""
			if tc.archiveRef != nil {
				archiveRef = tc.archiveRef(store)
			}
			if !tc.noStore {
				setTestWorkspaceStore(m, store)
			}

			_, err := m.ExecuteRevive(context.Background(), "sbx-1", tc.snapshotRef, archiveRef)
			if !errors.Is(err, ErrWorkspaceRestoreFailed) {
				t.Fatalf("err = %v, want ErrWorkspaceRestoreFailed", err)
			}
			we, ok := AsWorkspaceError(err)
			if !ok || we.CustomerMessage != RestoreFailedCustomerMessage {
				t.Fatalf("workspace error = %+v, want the customer restore-failed message", we)
			}
			if backend.created != 0 {
				t.Fatal("a VM was created even though the saved workspace could not be restored")
			}
		})
	}
}

func TestExecuteReviveTransientDownloadErrorIsRetryable(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log}
	m := newReviveManager(t, backend)
	store := newFakeWorkspaceStore(log)
	ref := store.put(curOwner, []byte("x"))
	store.getErr = errors.New("connection reset by peer")
	setTestWorkspaceStore(m, store)

	_, err := m.ExecuteRevive(context.Background(), "sbx-1", "", ref.String())
	if err == nil || errors.Is(err, ErrWorkspaceRestoreFailed) {
		t.Fatalf("err = %v, want a retryable (non restore-failed) error", err)
	}
	if backend.created != 0 {
		t.Fatal("VM created before the workspace download succeeded")
	}
}

func TestExecuteReviveWithNoSavedWorkspaceBootsFresh(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log}
	m := newReviveManager(t, backend)
	setTestWorkspaceStore(m, newFakeWorkspaceStore(log))

	if _, err := m.ExecuteRevive(context.Background(), "sbx-1", "", ""); err != nil {
		t.Fatalf("ExecuteRevive: %v", err)
	}
	if backend.created != 1 || log.index("restore") >= 0 {
		t.Fatalf("calls %v: want a fresh create with no restore", log.calls)
	}
}

func TestExecuteTerminateDeletesStoredWorkspace(t *testing.T) {
	log := &callLog{}
	m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log})
	store := newFakeWorkspaceStore(log)
	setTestWorkspaceStore(m, store)
	runningInstance(m)
	store.put(curOwner, []byte("saved"))

	if err := m.ExecuteTerminate(context.Background(), "sbx-1"); err != nil {
		t.Fatalf("ExecuteTerminate: %v", err)
	}
	if len(store.deletedBy) != 1 || store.deletedBy[0] != "tenant-a/sbx-1" {
		t.Fatalf("deletes = %v, want one delete scoped to tenant-a/sbx-1", store.deletedBy)
	}
	if len(store.objects) != 0 {
		t.Fatalf("objects left after terminate: %v", store.objects)
	}
}

// The one-off reconcile script writes the same customer message and error
// code as the reconciler; keep them in lockstep.
func TestLostWorkspaceScriptMatchesRestoreFailedOutcome(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "db", "sandbox-mark-lost-workspaces.sql"))
	if errors.Is(err, os.ErrNotExist) {
		// scripts/db holds operations scripts that are not part of the
		// published tree.
		t.Skip("operations script not present in this tree")
	}
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	script := string(data)
	if !strings.Contains(script, "'"+RestoreFailedCustomerMessage+"'") {
		t.Fatal("script error_reason differs from RestoreFailedCustomerMessage")
	}
	if !strings.Contains(script, "'"+WorkspaceErrorCodeRestoreFailed+": ") {
		t.Fatal("script error does not carry the restore-failed code the recovery checker skips")
	}
}

// Lost write-back: a previous stop attempt uploaded and verified the
// workspace and destroyed the VM, but the row write failed. The retry finds
// the guest gone and must adopt that upload, not report "nothing saved".
// A previous incarnation's or previous run's tarball is never adopted.
func TestExecuteStopRetryAdoptsOnlyTheCurrentRunsUpload(t *testing.T) {
	prevRun := curOwner
	prevRun.RunID = "run-prev"
	prevInc := curOwner
	prevInc.Incarnation = "inc-old"

	for _, tc := range []struct {
		name    string
		seed    func(*fakeWorkspaceStore) string // returns the expected ref ("" = none)
		wantRef bool
	}{
		{name: "current run's upload is adopted", seed: func(s *fakeWorkspaceStore) string {
			return s.put(curOwner, []byte("uploaded-by-first-attempt")).String()
		}, wantRef: true},
		{name: "previous run's consumed generation is not adopted", seed: func(s *fakeWorkspaceStore) string {
			s.put(prevRun, []byte("consumed"))
			return ""
		}},
		{name: "previous incarnation of the id is not adopted", seed: func(s *fakeWorkspaceStore) string {
			s.put(prevInc, []byte("previous sandbox"))
			return ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &callLog{}
			backend := &workspaceFakeBackend{log: log, snapshotErr: errors.New("dial unix vsock.sock: connect: no such file"), guestGone: true}
			m := newWorkspaceTestManager(t, backend)
			withSQL(t, m, "", "")
			store := newFakeWorkspaceStore(log)
			want := tc.seed(store)
			setTestWorkspaceStore(m, store)
			runningInstance(m)

			ref, err := m.ExecuteStop(context.Background(), "sbx-1")
			if err != nil {
				t.Fatalf("ExecuteStop: %v", err)
			}
			if ref != want {
				t.Fatalf("ref = %q, want %q", ref, want)
			}
		})
	}
}

// The wake-side safety net: a row with no ref adopts its current run's
// verified generation, and never a previous incarnation's or run's.
func TestExecuteReviveAdoptionIsBoundToTheCurrentRun(t *testing.T) {
	prevInc := curOwner
	prevInc.Incarnation = "inc-old"
	prevRun := curOwner
	prevRun.RunID = "run-prev"

	for _, tc := range []struct {
		name         string
		seedOwner    snapshot.WorkspaceOwner
		wantRestored string
	}{
		{name: "current run", seedOwner: curOwner, wantRestored: "orphaned-but-verified"},
		{name: "previous incarnation boots fresh", seedOwner: prevInc, wantRestored: ""},
		{name: "previous run boots fresh", seedOwner: prevRun, wantRestored: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &callLog{}
			backend := &workspaceFakeBackend{log: log}
			m := newReviveManager(t, backend)
			store := newFakeWorkspaceStore(log)
			store.put(tc.seedOwner, []byte("orphaned-but-verified"))
			setTestWorkspaceStore(m, store)

			if _, err := m.ExecuteRevive(context.Background(), "sbx-1", "", ""); err != nil {
				t.Fatalf("ExecuteRevive: %v", err)
			}
			if string(backend.restored) != tc.wantRestored {
				t.Fatalf("restored %q, want %q", backend.restored, tc.wantRestored)
			}
		})
	}
}

func TestExecuteReviveUnverifiableUnreferencedGenerationFailsLoudly(t *testing.T) {
	log := &callLog{}
	backend := &workspaceFakeBackend{log: log}
	m := newReviveManager(t, backend)
	store := newFakeWorkspaceStore(log)
	store.latestErr = snapshot.ErrWorkspaceCorrupt
	setTestWorkspaceStore(m, store)

	_, err := m.ExecuteRevive(context.Background(), "sbx-1", "", "")
	if !errors.Is(err, ErrWorkspaceRestoreFailed) {
		t.Fatalf("err = %v, want ErrWorkspaceRestoreFailed", err)
	}
	if backend.created != 0 {
		t.Fatal("VM created although the stored workspace could not be verified")
	}
}

// The legacy reaper must never fail or terminate a sandbox while storage
// may hold its only workspace copy. It parks it for attention, and the VM
// is handled so it neither leaks nor runs unbilled.
func TestReaperHoldsSandboxWithStoredWorkspace(t *testing.T) {
	if stuckTransitionAfter <= 11*time.Minute {
		t.Fatalf("stuck threshold %s must exceed the 6 min lease plus 5 min transfer budget", stuckTransitionAfter)
	}

	t.Run("nothing stored: cleanup may proceed", func(t *testing.T) {
		log := &callLog{}
		m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log})
		withSQL(t, m, "", "")
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		if m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleFailed)) {
			t.Fatal("held a sandbox with nothing stored")
		}
	})

	t.Run("row references a stored tarball", func(t *testing.T) {
		log := &callLog{}
		m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log, guestGone: true})
		mock := withSQLN(t, m, "", "objstore:v1:k#sha256=a&size=1", 1)
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*lifecycle_state = 'error'`).
			WithArgs(guardArgs(sqlmock.AnyArg(), NeedsAttentionWorkspaceMessage)...).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if !m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleFailed)) {
			t.Fatal("not held")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stalled wake destroys the half-revived VM", func(t *testing.T) {
		log := &callLog{}
		backend := &workspaceFakeBackend{log: log}
		m := newWorkspaceTestManager(t, backend)
		mock := withSQLN(t, m, "", "objstore:v1:k#sha256=a&size=1", 1)
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*lifecycle_state = 'error'`).
			WithArgs(guardArgs(sqlmock.AnyArg(), NeedsAttentionWorkspaceMessage)...).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if !m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleReviving)) {
			t.Fatal("not held")
		}
		if backend.destroyed != 1 {
			t.Fatalf("destroyed = %d, want the leaked VM destroyed", backend.destroyed)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stalled stop with a live VM goes back to running and keeps billing", func(t *testing.T) {
		log := &callLog{}
		backend := &workspaceFakeBackend{log: log}
		m := newWorkspaceTestManager(t, backend)
		mock := withSQLN(t, m, "", "", 1)
		store := newFakeWorkspaceStore(log)
		store.put(curOwner, []byte("uploaded, row write lost"))
		setTestWorkspaceStore(m, store)
		runningInstance(m)
		mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*lifecycle_state\s*= 'running'[\s\S]*billing_started_at = COALESCE\(billing_started_at, NOW\(\)\)`).
			WithArgs(guardArgs(sqlmock.AnyArg(), NeedsAttentionWorkspaceMessage)...).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if !m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleStopping)) {
			t.Fatal("not held")
		}
		if backend.destroyed != 0 {
			t.Fatal("live VM of a stalled stop was destroyed")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		if got := m.instancesBySandbox["sbx-1"].LifecycleState; got != LifecycleRunning {
			t.Fatalf("in-memory lifecycle = %q, want running", got)
		}
	})

	t.Run("guarded update loses the race: no destroy", func(t *testing.T) {
		log := &callLog{}
		backend := &workspaceFakeBackend{log: log}
		m := newWorkspaceTestManager(t, backend)
		mock := withSQLN(t, m, "", "objstore:v1:k#sha256=a&size=1", 1)
		setTestWorkspaceStore(m, newFakeWorkspaceStore(log))
		mock.ExpectExec(`UPDATE sandbox_instances SET lifecycle_state = 'error'[\s\S]*WHERE id = \$1 AND lifecycle_state = \$2 AND updated_at = \$3[\s\S]*reconcile_locked_at IS NOT DISTINCT FROM \$4`).
			WithArgs(guardArgs(sqlmock.AnyArg(), NeedsAttentionWorkspaceMessage)...).
			WillReturnResult(sqlmock.NewResult(0, 0))
		if !m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleReviving)) {
			t.Fatal("not held")
		}
		if backend.destroyed != 0 {
			t.Fatal("VM destroyed although the guarded update affected no row")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("storage check fails: held with the error, not skipped silently", func(t *testing.T) {
		log := &callLog{}
		m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log, guestGone: true})
		mock := withSQLN(t, m, "", "", 1)
		store := newFakeWorkspaceStore(log)
		store.latestErr = errors.New("r2 unreachable")
		setTestWorkspaceStore(m, store)
		mock.ExpectExec(`UPDATE sandbox_instances[\s\S]*lifecycle_state = 'error'`).
			WithArgs(guardArgs(sqlmock.AnyArg(), NeedsAttentionWorkspaceMessage)...).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if !m.holdForAttentionIfWorkspaceStored(context.Background(), cand(LifecycleFailed)) {
			t.Fatal("a failed storage check must hold the sandbox")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkspacePurgeOutboxDrains(t *testing.T) {
	for _, tc := range []struct {
		name      string
		deleteErr error
	}{
		{name: "delete succeeds"},
		{name: "delete fails and is rescheduled", deleteErr: errors.New("r2 unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer rawDB.Close()
			log := &callLog{}
			m := newWorkspaceTestManager(t, &workspaceFakeBackend{log: log})
			m.db = sqlx.NewDb(rawDB, "sqlmock")
			store := newFakeWorkspaceStore(log)
			store.deleteErr = tc.deleteErr
			old := curOwner
			old.Incarnation = "inc-old"
			oldRef := store.put(old, []byte("terminated sandbox"))
			curRef := store.put(curOwner, []byte("reused id, new sandbox"))
			setTestWorkspaceStore(m, store)

			mock.ExpectQuery(`UPDATE sandbox_workspace_purges p[\s\S]*FOR UPDATE SKIP LOCKED[\s\S]*RETURNING`).
				WillReturnRows(sqlmock.NewRows([]string{"sandbox_id", "incarnation", "tenant_id", "attempts"}).
					AddRow("sbx-1", "inc-old", "tenant-a", 1))
			if tc.deleteErr == nil {
				mock.ExpectExec(`DELETE FROM sandbox_workspace_purges`).WithArgs("sbx-1", "inc-old").
					WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec(`UPDATE sandbox_workspace_purges[\s\S]*last_error = \$3`).
					WithArgs("sbx-1", "inc-old", "r2 unavailable", sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			m.drainWorkspacePurges(context.Background())
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
			if tc.deleteErr == nil && store.has(oldRef.Key) {
				t.Fatal("terminated incarnation's tarball survived the purge")
			}
			if !store.has(curRef.Key) {
				t.Fatal("the purge deleted the current incarnation's tarball")
			}
		})
	}
}

func TestWorkspaceAttentionGauges(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	m := &SandboxManager{db: sqlx.NewDb(rawDB, "sqlmock")}
	mock.ExpectQuery(`SELECT split_part\(error, ':', 1\)`).
		WillReturnRows(sqlmock.NewRows([]string{"code", "n"}).AddRow(WorkspaceErrorCodeRestoreFailed, 2))
	mock.ExpectQuery(`FROM sandbox_workspace_purges`).
		WillReturnRows(sqlmock.NewRows([]string{"count", "failing"}).AddRow(5, 1))

	m.reportWorkspaceAttention(context.Background())
	if got := gaugeValue(workspaceAttentionGauge.WithLabelValues(WorkspaceErrorCodeRestoreFailed)); got != 2 {
		t.Fatalf("restore_failed gauge = %v, want 2", got)
	}
	if got := gaugeValue(workspaceAttentionGauge.WithLabelValues(WorkspaceErrorCodeNeedsAttention)); got != 0 {
		t.Fatalf("needs_attention gauge = %v, want 0", got)
	}
	if gaugeValue(workspacePurgeBacklogGauge) != 5 || gaugeValue(workspacePurgeFailingGauge) != 1 {
		t.Fatal("purge gauges not set")
	}
}

func gaugeValue(c prometheus.Collector) float64 {
	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	var m dto.Metric
	if err := (<-ch).Write(&m); err != nil {
		return -1
	}
	return m.GetGauge().GetValue()
}

var candUpdatedAt = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

// cand is a reaper candidate for sbx-1 observed in state.
func cand(state string) cleanupCandidate {
	return cleanupCandidate{ID: "sbx-1", State: state, UpdatedAt: candUpdatedAt}
}

// guardArgs prefixes the guarded-update condition arguments.
func guardArgs(rest ...driver.Value) []driver.Value {
	return append([]driver.Value{"sbx-1", sqlmock.AnyArg(), candUpdatedAt, sqlmock.AnyArg()}, rest...)
}
