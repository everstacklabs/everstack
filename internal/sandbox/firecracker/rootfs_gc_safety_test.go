package firecracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Enforce-mode safety rules added before FC_ROOTFS_GC_ENFORCE may be
// turned on: empty and unknown reports, reporter allowlist, symlink
// targets, and the separate writable GC mount.

const r2 = "openai-codex-0.154.0-r2.ext4"

func TestRootfsGC_EmptyReportKeepsEveryImage(t *testing.T) {
	// The review repro: Report("gw", nil) followed by a sweep deleted every
	// tag image except base. An empty set says nothing about what is
	// referenced, so it must not count as a report.
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", nil, false)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, name := range []string{r2, "openai-codex-0.154.0-r3.ext4", "openai-codex-0.154.0.ext4"} {
		if res.Kept[name] != keepNoReferenceReport {
			t.Errorf("%s kept reason = %q, want %q", name, res.Kept[name], keepNoReferenceReport)
		}
	}
}

func TestRootfsGC_UnknownReportPoisonsCompleteOnes(t *testing.T) {
	// One reporter vouching for its full set cannot outvote another that
	// says it does not know: the union would miss the unknown part.
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	f.refs.Report("everstack-gateway/gw-2", []string{r3Ref}, true)

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Kept[r2] != keepNoReferenceReport {
		t.Fatalf("r2 kept reason = %q, want %q", res.Kept[r2], keepNoReferenceReport)
	}

	// Once the unknown report expires the complete one is usable again.
	f.refs.now = func() time.Time { return f.now.Add(20 * time.Minute) }
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	f.refs.now = func() time.Time { return f.now.Add(35 * time.Minute) }
	if _, fresh := f.refs.Tags(); !fresh {
		t.Fatal("expired unknown report still poisons the set")
	}
}

func TestRootfsReferences_IgnoresReportersOutsideAllowlist(t *testing.T) {
	refs := NewRootfsReferences(30*time.Minute, []string{"everstack-gateway"})
	for _, reporter := range []string{"rogue/pod-1", "everstack-gateway-evil/pod-1", "", "unknown"} {
		if refs.Report(reporter, []string{r3Ref}, false) {
			t.Errorf("report from %q accepted", reporter)
		}
	}
	if _, fresh := refs.Tags(); fresh {
		t.Fatal("non-allowlisted reports made the set fresh")
	}
	if !refs.Report("everstack-gateway/pod-1", []string{r3Ref}, false) {
		t.Fatal("allowlisted reporter refused")
	}
	if !refs.Report("everstack-gateway", []string{r3Ref}, false) {
		t.Fatal("bare allowlisted name refused")
	}
}

func TestRootfsReferences_EmptyAllowlistAcceptsNothing(t *testing.T) {
	refs := NewRootfsReferences(30*time.Minute, nil)
	if refs.Report("everstack-gateway/pod-1", []string{r3Ref}, false) {
		t.Fatal("empty allowlist accepted a report")
	}
}

func TestRootfsGC_SymlinkTargetsAreKept(t *testing.T) {
	f := newGCFixture(t, incidentLayout, enforced())
	f.refs.Report("everstack-gateway/gw-1", []string{r3Ref}, false)
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	link(r2, "relative.ext4")                                           // relative
	link(filepath.Join(f.dir, "openai-codex-0.154.0.ext4"), "abs.ext4") // absolute
	link("firecracker-agent-259817c", "chain-end")                      // chain:
	link("chain-end", "chain-start")                                    // start -> end -> file
	link("does-not-exist", "broken.ext4")                               // ignored

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, name := range []string{r2, "openai-codex-0.154.0.ext4", "firecracker-agent-259817c"} {
		if res.Kept[name] != keepSymlinkTarget {
			t.Errorf("%s kept reason = %q, want %q", name, res.Kept[name], keepSymlinkTarget)
		}
		if _, err := os.Stat(filepath.Join(f.dir, name)); err != nil {
			t.Errorf("symlink target %s deleted", name)
		}
	}
	// Non-targets still go.
	if _, err := os.Stat(filepath.Join(f.dir, "openai-codex-0.154.0.ext4.version")); err == nil {
		t.Error("unprotected sidecar survived")
	}
	for _, name := range []string{"relative.ext4", "abs.ext4", "chain-start", "chain-end", "broken.ext4"} {
		if _, err := os.Lstat(filepath.Join(f.dir, name)); err != nil {
			t.Errorf("symlink %s removed", name)
		}
	}
}

func TestRootfsGC_FileSwappedForSymlinkIsNotFollowed(t *testing.T) {
	// Between classification and delete a candidate is replaced by a
	// symlink to base.ext4. The delete must neither follow it nor remove
	// the link: the file is no longer the one that was classified.
	f := newGCFixture(t, []string{"base.ext4", "stray.bin"}, enforced())
	stray := filepath.Join(f.dir, "stray.bin")
	f.gc.openFiles = func() (map[fileID]struct{}, error) {
		if err := os.Remove(stray); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("base.ext4", stray); err != nil {
			t.Fatal(err)
		}
		return map[fileID]struct{}{}, nil
	}

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Deleted) != 0 || res.Errors != 1 {
		t.Fatalf("deleted=%v errors=%d, want nothing deleted and one error", res.Deleted, res.Errors)
	}
	if fi, err := os.Lstat(stray); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("swapped-in symlink was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "base.ext4")); err != nil {
		t.Fatal("symlink target deleted")
	}
}

func TestRootfsGC_DeletesThroughTheWritableGCDir(t *testing.T) {
	// The agent mounts the rootfs dir read-only for boots and a second,
	// writable mount of the same directory for the GC.
	f := newGCFixture(t, []string{"base.ext4", "stray.bin"}, enforced())
	gcDir := filepath.Join(t.TempDir(), "rootfs-gc")
	if err := os.Symlink(f.dir, gcDir); err != nil {
		t.Fatal(err)
	}
	f.gc.cfg.Dir = gcDir

	res, err := f.gc.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !equalStrings(res.Deleted, []string{"stray.bin"}) {
		t.Fatalf("deleted = %v", res.Deleted)
	}
}

func TestRootfsGC_RefusesAGCDirThatIsNotTheRootfsDir(t *testing.T) {
	f := newGCFixture(t, []string{"base.ext4", "stray.bin"}, enforced())
	other := t.TempDir()
	writeAged(t, filepath.Join(other, "stray.bin"), f.now.Add(-30*24*time.Hour))
	f.gc.cfg.Dir = other

	if _, err := f.gc.Sweep(); err == nil {
		t.Fatal("Sweep accepted a GC dir that is a different directory")
	}
	if _, err := os.Stat(filepath.Join(other, "stray.bin")); err != nil {
		t.Fatal("deleted from the wrong directory")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "stray.bin")); err != nil {
		t.Fatal("deleted from the rootfs dir via the wrong mount")
	}
}
