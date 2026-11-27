package firecracker

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestRootfsTag(t *testing.T) {
	cases := map[string]string{
		"":                                   "base",
		"base":                               "base",
		"node":                               "base", // no tag separator: createRootfsOverlay has always booted base
		"ghcr.io/everstacklabs/sandbox:node": "node",
		"everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3": "openai-codex-0.154.0-r3",
	}
	for in, want := range cases {
		if got := RootfsTag(in); got != want {
			t.Errorf("RootfsTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRootfsOwnerTag(t *testing.T) {
	owned := map[string]string{
		"base.ext4":                            "base",
		"base.ext4.version":                    "base",
		"openai-codex-0.154.0-r3.ext4":         "openai-codex-0.154.0-r3",
		"openai-codex-0.154.0-r3.ext4.source":  "openai-codex-0.154.0-r3",
		"openai-codex-0.154.0-r3.ext4.sha256":  "openai-codex-0.154.0-r3",
		"openai-codex-0.154.0-r3.ext4.version": "openai-codex-0.154.0-r3",
	}
	for name, want := range owned {
		got, ok := rootfsOwnerTag(name)
		if !ok || got != want {
			t.Errorf("rootfsOwnerTag(%q) = %q,%v, want %q,true", name, got, ok, want)
		}
	}
	// Anything the create path can never resolve to is unowned. In
	// particular a backup of r3 must NOT be owned by tag r3, or keeping
	// r3 would keep every backup of it forever.
	unowned := []string{
		"openai-codex-0.154.0-r3.ext4.backup-20260912T185145Z-ec9d4e1e6b66-8702",
		"openai-codex-0.154.0-r3.ext4.source.backup-20260912T185145Z-ec9d4e1e6b66-8702",
		"openai-codex-0.154.0-r3.ext4.previous",
		".r3.dedupe.tmp",
		".openai-codex-0.154.0-r3.ext4.install.123",
		"firecracker-agent-259817c",
		".ext4",
	}
	for _, name := range unowned {
		if tag, ok := rootfsOwnerTag(name); ok {
			t.Errorf("rootfsOwnerTag(%q) = %q, want unowned", name, tag)
		}
	}
}

type fakeRootfsOwner struct {
	dir    string
	images []string
}

func (f *fakeRootfsOwner) RootfsDir() string       { return f.dir }
func (f *fakeRootfsOwner) TrackedImages() []string { return f.images }

const r3Ref = "everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3"

// incidentLayout reproduces dev/rootfs as found on 2026-10-13.
var incidentLayout = []string{
	"base.ext4",
	"base.ext4.version",
	"openai-codex-0.154.0-r3.ext4",
	"openai-codex-0.154.0-r3.ext4.source",
	"openai-codex-0.154.0-r3.ext4.version",
	"openai-codex-0.154.0-r3.ext4.backup-20260912T185145Z-ec9d4e1e6b66-8702",
	"openai-codex-0.154.0-r3.ext4.source.backup-20260912T185145Z-ec9d4e1e6b66-8702",
	"openai-codex-0.154.0-r3.ext4.backup-20260912T173750Z-55b4913691be-69170",
	"openai-codex-0.154.0-r2.ext4",
	"openai-codex-0.154.0-r2.ext4.version",
	"openai-codex-0.154.0.ext4",
	"openai-codex-0.154.0.ext4.version",
	"firecracker-agent-259817c",
}

var incidentGarbage = []string{
	"firecracker-agent-259817c",
	"openai-codex-0.154.0-r2.ext4",
	"openai-codex-0.154.0-r2.ext4.version",
	"openai-codex-0.154.0-r3.ext4.backup-20260912T173750Z-55b4913691be-69170",
	"openai-codex-0.154.0-r3.ext4.backup-20260912T185145Z-ec9d4e1e6b66-8702",
	"openai-codex-0.154.0-r3.ext4.source.backup-20260912T185145Z-ec9d4e1e6b66-8702",
	"openai-codex-0.154.0.ext4",
	"openai-codex-0.154.0.ext4.version",
}

type gcFixture struct {
	dir  string
	now  time.Time
	refs *RootfsReferences
	gc   *RootfsGC
}

// newGCFixture writes files aged 30 days, so only the rules under test
// decide what survives, never the grace period.
func newGCFixture(t *testing.T, files []string, cfg RootfsGCConfig) *gcFixture {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	for _, name := range files {
		writeAged(t, filepath.Join(dir, name), old)
	}
	refs := NewRootfsReferences(30*time.Minute, []string{"everstack-gateway"})
	refs.now = func() time.Time { return now }
	gc := NewRootfsGC(&fakeRootfsOwner{dir: dir}, refs, cfg)
	gc.now = func() time.Time { return now }
	gc.fileTime = func(_ string, fi os.FileInfo) time.Time { return fi.ModTime() }
	gc.openFiles = func() (map[fileID]struct{}, error) { return map[fileID]struct{}{}, nil }
	return &gcFixture{dir: dir, now: now, refs: refs, gc: gc}
}

func writeAged(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func without(all, drop []string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, a := range all {
		if !skip[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func enforced() RootfsGCConfig {
	cfg := DefaultRootfsGCConfig()
	cfg.Enforce = true
	return cfg
}

func TestRootfsGC_EnforceRemovesIncidentGarbage(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got, want := sorted(res.Deleted), sorted(incidentGarbage); !equalStrings(got, want) {
		t.Fatalf("deleted = %v\nwant %v", got, want)
	}
	if got, want := listDir(t, f.dir), without(incidentLayout, incidentGarbage); !equalStrings(got, want) {
		t.Fatalf("left on disk = %v\nwant %v", got, want)
	}
	if res.BytesFreed <= 0 {
		t.Fatalf("BytesFreed = %d, want > 0", res.BytesFreed)
	}
}

func TestRootfsGC_DryRunDeletesNothingButReportsTheSameSet(t *testing.T) {
	f := newGCFixture(t, incidentLayout, DefaultRootfsGCConfig())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Deleted) != 0 {
		t.Fatalf("dry run deleted %v", res.Deleted)
	}
	if got, want := sorted(res.WouldDelete), sorted(incidentGarbage); !equalStrings(got, want) {
		t.Fatalf("would delete = %v\nwant %v", got, want)
	}
	if got := listDir(t, f.dir); !equalStrings(got, sorted(incidentLayout)) {
		t.Fatalf("dry run changed the directory: %v", got)
	}
}

func TestRootfsGC_DefaultIsDryRun(t *testing.T) {
	if DefaultRootfsGCConfig().Enforce {
		t.Fatal("first release must default to dry run")
	}
}

func TestRootfsGC_WithoutReferenceReportKeepsEveryImage(t *testing.T) {
	// Fail closed: with no gateway report the agent cannot know what a
	// sleeping sandbox will wake into, so every <tag>.ext4 stays. Files
	// the create path can never resolve to (backups, strays) still go.
	f := newGCFixture(t, incidentLayout, enforced())

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	wantDeleted := []string{
		"firecracker-agent-259817c",
		"openai-codex-0.154.0-r3.ext4.backup-20260912T173750Z-55b4913691be-69170",
		"openai-codex-0.154.0-r3.ext4.backup-20260912T185145Z-ec9d4e1e6b66-8702",
		"openai-codex-0.154.0-r3.ext4.source.backup-20260912T185145Z-ec9d4e1e6b66-8702",
	}
	if got := sorted(res.Deleted); !equalStrings(got, sorted(wantDeleted)) {
		t.Fatalf("deleted = %v\nwant %v", got, wantDeleted)
	}
	if res.Kept["openai-codex-0.154.0-r2.ext4"] != keepNoReferenceReport {
		t.Fatalf("r2 kept reason = %q, want %q", res.Kept["openai-codex-0.154.0-r2.ext4"], keepNoReferenceReport)
	}
}

func TestRootfsGC_StaleReferenceReportKeepsEveryImage(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.now = func() time.Time { return f.now.Add(-time.Hour) }
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	f.refs.now = func() time.Time { return f.now }

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "openai-codex-0.154.0-r2.ext4")); err != nil {
		t.Fatalf("stale report let r2 be deleted: %v (deleted %v)", err, res.Deleted)
	}
}

func TestRootfsGC_GracePeriodProtectsRecentFiles(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	recent := f.now.Add(-time.Hour)
	writeAged(t, filepath.Join(f.dir, "openai-codex-0.155.0.ext4"), recent)
	writeAged(t, filepath.Join(f.dir, "openai-codex-0.154.0-r3.ext4.previous"), recent)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, name := range []string{"openai-codex-0.155.0.ext4", "openai-codex-0.154.0-r3.ext4.previous"} {
		if res.Kept[name] != keepRecent {
			t.Errorf("%s kept reason = %q, want %q", name, res.Kept[name], keepRecent)
		}
	}
}

func TestRootfsGC_PreviousCopyAgesOut(t *testing.T) {
	f := newGCFixture(t, []string{"base.ext4", "openai-codex-0.154.0-r3.ext4.previous"}, enforced())

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !equalStrings(res.Deleted, []string{"openai-codex-0.154.0-r3.ext4.previous"}) {
		t.Fatalf("deleted = %v", res.Deleted)
	}
}

func TestRootfsGC_OpenFileIsNeverDeleted(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	busy := filepath.Join(f.dir, "openai-codex-0.154.0-r2.ext4")
	meta, err := statFile(busy)
	if err != nil {
		t.Fatal(err)
	}
	f.gc.openFiles = func() (map[fileID]struct{}, error) {
		return map[fileID]struct{}{meta.id: {}}, nil
	}

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Kept["openai-codex-0.154.0-r2.ext4"] != keepInUse {
		t.Fatalf("open file kept reason = %q, want %q", res.Kept["openai-codex-0.154.0-r2.ext4"], keepInUse)
	}
	if _, err := os.Stat(busy); err != nil {
		t.Fatalf("open file was deleted: %v", err)
	}
}

func TestRootfsGC_OpenFileScanFailureDeletesNothing(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	f.gc.openFiles = func() (map[fileID]struct{}, error) { return nil, errors.New("no /proc") }

	if _, err := f.gc.Sweep(); err == nil {
		t.Fatal("Sweep succeeded although the in-use check could not run")
	}
	if got := listDir(t, f.dir); !equalStrings(got, sorted(incidentLayout)) {
		t.Fatalf("files deleted without an in-use check: %v", got)
	}
}

func TestRootfsGC_KeepListAndTrackedVMsProtectImages(t *testing.T) {
	cfg := enforced()
	cfg.Keep = []string{"node"}
	f := newGCFixture(t, []string{"base.ext4", "node.ext4", "python.ext4", "fullstack.ext4"}, cfg)
	f.gc.owner = &fakeRootfsOwner{dir: f.dir, images: []string{"ghcr.io/everstacklabs/sandbox:python"}}
	f.refs.Report("everstack-gateway/gw-1", []string{"x:unrelated"}, false)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !equalStrings(res.Deleted, []string{"fullstack.ext4"}) {
		t.Fatalf("deleted = %v, want only fullstack.ext4", res.Deleted)
	}
	if res.Kept["node.ext4"] != keepConfigured || res.Kept["python.ext4"] != keepTracked {
		t.Fatalf("kept reasons = %v", res.Kept)
	}
}

func TestRootfsGC_SkipsDirectoriesAndSymlinks(t *testing.T) {
	f := newGCFixture(t, []string{"base.ext4"}, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{"x:unrelated"}, false)
	if err := os.Mkdir(filepath.Join(f.dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("base.ext4", filepath.Join(f.dir, "alias.ext4")); err != nil {
		t.Fatal(err)
	}

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Deleted) != 0 {
		t.Fatalf("deleted %v", res.Deleted)
	}
	if got := listDir(t, f.dir); !equalStrings(got, []string{"alias.ext4", "base.ext4", "subdir"}) {
		t.Fatalf("dir = %v", got)
	}
}

func TestRootfsGC_MissingDirIsANoop(t *testing.T) {
	gc := NewRootfsGC(&fakeRootfsOwner{dir: filepath.Join(t.TempDir(), "absent")}, NewRootfsReferences(time.Minute, nil), enforced())
	res, err := gc.Sweep()
	if err != nil || res.Scanned != 0 {
		t.Fatalf("Sweep = %+v, %v", res, err)
	}
}

func TestRootfsReferences_UnionsFreshReportsByReporter(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	refs := NewRootfsReferences(30*time.Minute, []string{"everstack-gateway"})
	refs.now = func() time.Time { return now }

	if _, fresh := refs.Tags(); fresh {
		t.Fatal("no report yet must not count as fresh")
	}

	refs.Report("everstack-gateway/gw-a", []string{"x/y:alpha", "x/y:beta"}, false)
	refs.Report("everstack-gateway/gw-b", []string{"x/y:gamma"}, false)
	// A reporter's newer report replaces its own older one only.
	refs.Report("everstack-gateway/gw-a", []string{"x/y:alpha"}, false)

	tags, fresh := refs.Tags()
	if !fresh {
		t.Fatal("expected fresh")
	}
	for _, want := range []string{"alpha", "gamma"} {
		if _, ok := tags[want]; !ok {
			t.Errorf("missing %s in %v", want, tags)
		}
	}
	if _, ok := tags["beta"]; ok {
		t.Errorf("beta survived gw-a's replacement report: %v", tags)
	}

	// gw-b goes quiet; once its report expires its tags drop out but
	// gw-a's fresh report keeps the set usable.
	now = now.Add(20 * time.Minute)
	refs.Report("everstack-gateway/gw-a", []string{"x/y:alpha"}, false)
	now = now.Add(15 * time.Minute)
	tags, fresh = refs.Tags()
	if !fresh {
		t.Fatal("gw-a report is 15m old, want fresh")
	}
	if _, ok := tags["gamma"]; ok {
		t.Errorf("expired gw-b tags still present: %v", tags)
	}

	now = now.Add(time.Hour)
	if _, fresh := refs.Tags(); fresh {
		t.Fatal("all reports expired, want not fresh")
	}
}
