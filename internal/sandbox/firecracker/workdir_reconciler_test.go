package firecracker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeOwner satisfies WorkdirOwner with explicit state for tests.
type fakeOwner struct {
	workDir string
	ids     map[string]struct{}
}

func (f *fakeOwner) WorkDir() string                { return f.workDir }
func (f *fakeOwner) TrackedIDs() map[string]struct{} { return f.ids }

// makeWorkdir creates a per-VM workdir at the given path with optional
// state.json contents. age controls the mtime — set to a duration in
// the past so the reconciler sees it as eligible for reaping.
func makeWorkdir(t *testing.T, root, id string, age time.Duration, state *vmState) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if state != nil {
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o644); err != nil {
			t.Fatalf("write state.json: %v", err)
		}
	}
	// Backdate the dir mtime to simulate age.
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
	return dir
}

func TestSweep_LiveVMsAreNeverReaped(t *testing.T) {
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-live", 10*time.Minute, nil)

	owner := &fakeOwner{
		workDir: root,
		ids:     map[string]struct{}{"sbx-live": {}},
	}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 0 {
		t.Fatalf("live VM was reaped: %+v", res)
	}
	if res.Live != 1 {
		t.Fatalf("expected 1 live, got %d (%+v)", res.Live, res)
	}
	if _, err := os.Stat(filepath.Join(root, "sbx-live")); err != nil {
		t.Fatalf("live workdir was removed: %v", err)
	}
}

func TestSweep_OrphanWithinGracePeriodIsKept(t *testing.T) {
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-young", 10*time.Second, nil)

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	cfg := DefaultWorkdirReconcilerConfig() // 5min grace
	r := &WorkdirReconciler{owner: owner, cfg: cfg}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 0 {
		t.Fatalf("young orphan reaped: %+v", res)
	}
	if res.InFlight != 1 {
		t.Fatalf("expected 1 in-flight, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "sbx-young")); err != nil {
		t.Fatalf("young workdir was removed despite grace: %v", err)
	}
}

func TestSweep_OldOrphanIsReaped(t *testing.T) {
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-old", 10*time.Minute, nil)

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 1 {
		t.Fatalf("expected 1 reaped, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "sbx-old")); !os.IsNotExist(err) {
		t.Fatalf("orphan workdir was not removed: stat err=%v", err)
	}
}

func TestSweep_DryRunDoesNotRemove(t *testing.T) {
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-dry", 10*time.Minute, nil)

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	cfg := DefaultWorkdirReconcilerConfig()
	cfg.DryRun = true
	r := &WorkdirReconciler{owner: owner, cfg: cfg}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 1 {
		t.Fatalf("expected reap count 1 in dry-run summary, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "sbx-dry")); err != nil {
		t.Fatalf("workdir removed during dry-run: %v", err)
	}
}

func TestSweep_StateJSONWithLivePIDIsKept(t *testing.T) {
	// Our own PID is guaranteed alive — use it as a "live PID" marker
	// in state.json. The sweep should treat the workdir as live and
	// skip the reap.
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-live-pid", 10*time.Minute, &vmState{
		ID:  "sbx-live-pid",
		PID: os.Getpid(),
	})

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 0 {
		t.Fatalf("workdir with live PID was reaped: %+v", res)
	}
	if res.Live != 1 {
		t.Fatalf("expected 1 live (by PID), got %+v", res)
	}
}

func TestSweep_DeadPIDStateIsReaped(t *testing.T) {
	// PID 0 is reserved by the kernel and never refers to a real
	// process. The sweep should treat this state.json as "dead" and
	// proceed to reap.
	root := t.TempDir()
	makeWorkdir(t, root, "sbx-dead", 10*time.Minute, &vmState{
		ID:  "sbx-dead",
		PID: 0,
	})

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reaped != 1 {
		t.Fatalf("dead-PID workdir not reaped: %+v", res)
	}
}

func TestSweep_FreesBytesAccounting(t *testing.T) {
	root := t.TempDir()
	dir := makeWorkdir(t, root, "sbx-bytes", 10*time.Minute, nil)
	// Drop a 1KB blob inside to exercise the byte accounting.
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 1024), 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	// Re-backdate after the file write or the dir mtime gets bumped.
	mtime := time.Now().Add(-10 * time.Minute)
	_ = os.Chtimes(dir, mtime, mtime)

	owner := &fakeOwner{workDir: root, ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.BytesFreed < 1024 {
		t.Fatalf("expected BytesFreed >= 1024, got %d", res.BytesFreed)
	}
}

func TestSweep_EmptyWorkdirRootIsNoOp(t *testing.T) {
	owner := &fakeOwner{workDir: t.TempDir(), ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("empty dir: %v", err)
	}
	if res.Scanned != 0 || res.Reaped != 0 {
		t.Fatalf("expected zeros, got %+v", res)
	}
}

func TestSweep_MissingWorkdirRootIsNoOp(t *testing.T) {
	owner := &fakeOwner{workDir: "/tmp/does-not-exist-" + t.Name(), ids: map[string]struct{}{}}
	r := &WorkdirReconciler{owner: owner, cfg: DefaultWorkdirReconcilerConfig()}
	res, err := r.Sweep()
	if err != nil {
		t.Fatalf("missing dir should be no-op: %v", err)
	}
	if res.Scanned != 0 {
		t.Fatalf("expected scanned=0, got %+v", res)
	}
}
