package firecracker

// RootfsGC removes stale rootfs images from FC_ROOTFS_DIR.
//
// Why it exists (2026-10-13): dev and prod share one 20 GiB btrfs VM
// store. Hand-installed executor builds and timestamped backups of them
// piled up in dev/rootfs until the store was 572 MiB from the create
// guard (DefaultMinFreeDiskMB), which refuses every create and wake on
// both environments. Nothing ever deleted from the rootfs dir: the
// workdir reconciler only owns per-VM directories.
//
// A file survives a sweep when ANY of these hold:
//
//   - it belongs to tag "base" (the base image and its markers);
//   - it belongs to a tag in Keep (FC_ROOTFS_GC_KEEP);
//   - it belongs to the image tag of a VM this agent currently tracks;
//   - it belongs to a tag the gateway reported as referenced (configured
//     executor image, or any sandbox row that can still boot or wake);
//   - it belongs to any tag and no fresh gateway report exists (fail
//     closed: the agent cannot see the DB, so without a report it cannot
//     know what a sleeping sandbox will wake into). Reports from
//     reporters outside FC_ROOTFS_GC_REPORTERS are ignored, and any
//     unexpired empty or unknown report makes the set not fresh;
//   - it is the target of a symlink in the directory;
//   - it changed within GracePeriod (newest of mtime and ctime);
//   - some process on the host holds it open.
//
// "Belongs to tag T" is an exact-name rule: `T.ext4` or `T.ext4.<marker>`
// with marker in rootfsSidecars. Everything else in the directory
// (`*.backup-*`, `*.previous`, temp files, stray binaries) is unowned:
// the create path can never resolve to it, so it is eligible once it is
// older than the grace period and not open, report or no report.
//
// Directories and symlinks are never touched, and a file is re-checked
// with lstat right before its delete. Only the top level of the rootfs
// dir is scanned. Listing and deleting go through Config.Dir, a writable
// mount of the read-only rootfs dir.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Kept reasons, reported per file in RootfsGCResult.Kept.
const (
	keepBase              = "base"
	keepConfigured        = "keep_list"
	keepTracked           = "tracked_vm"
	keepReferenced        = "referenced"
	keepNoReferenceReport = "no_reference_report"
	keepRecent            = "recent"
	keepInUse             = "in_use"
	keepUnverified        = "unverified"
	keepSymlinkTarget     = "symlink_target"
)

// rootfsSidecars are the marker files written next to an image. They
// live and die with the image they describe.
var rootfsSidecars = []string{"version", "source", "sha256"}

// RootfsTag returns the rootfs tag the create path resolves an image
// reference to: the segment after the last ':' or "base" when the
// reference has none. createRootfsOverlay boots `<tag>.ext4`, so the GC
// and the create path must agree on this mapping exactly.
func RootfsTag(imageName string) string {
	if parts := strings.Split(imageName, ":"); len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return "base"
}

// rootfsOwnerTag reports which tag a file in the rootfs dir belongs to.
func rootfsOwnerTag(name string) (string, bool) {
	if tag, ok := strings.CutSuffix(name, ".ext4"); ok {
		return tag, tag != ""
	}
	for _, sc := range rootfsSidecars {
		if tag, ok := strings.CutSuffix(name, ".ext4."+sc); ok {
			return tag, tag != ""
		}
	}
	return "", false
}

// RootfsGCConfig tunes the GC. Zero values fall back to the defaults.
type RootfsGCConfig struct {
	// Interval between sweeps. A sweep also runs once at start.
	Interval time.Duration
	// GracePeriod is how long a file is protected after it last changed,
	// so an image installed ahead of the config that points at it
	// survives until the gateway reports it.
	GracePeriod time.Duration
	// Enforce deletes eligible files. When false (the default for the
	// first release) the GC logs and counts what it would delete.
	Enforce bool
	// Keep lists tags that are never deleted, for images the gateway
	// cannot report (for example per-template images dropped by hand).
	Keep []string
	// Dir is a writable mount of the rootfs dir used for listing and
	// deleting. The agent mounts RootfsDir read-only for VM boots, so
	// deletes through it would fail; the chart mounts the same host path a
	// second time, writable, for the GC only. Empty means RootfsDir. A Dir
	// that is not the same directory as RootfsDir fails every sweep.
	Dir string
}

// DefaultRootfsGCConfig returns the production defaults: hourly, 24h
// grace, dry run.
func DefaultRootfsGCConfig() RootfsGCConfig {
	return RootfsGCConfig{
		Interval:    time.Hour,
		GracePeriod: 24 * time.Hour,
		Enforce:     false,
	}
}

// RootfsGCOwner is what the GC needs from the backend.
type RootfsGCOwner interface {
	RootfsDir() string
	// TrackedImages returns the image references of VMs this agent
	// currently tracks.
	TrackedImages() []string
}

// fileID identifies a file independently of its path, so an open file
// is recognised from any mount namespace.
type fileID struct {
	dev uint64
	ino uint64
}

type fileMeta struct {
	id        fileID
	ctime     time.Time
	allocated int64
}

// RootfsGCResult summarises one sweep.
type RootfsGCResult struct {
	Scanned     int
	Deleted     []string
	WouldDelete []string
	// Kept maps each surviving regular file to the first rule that
	// protected it.
	Kept       map[string]string
	BytesFreed int64
	Errors     int
	// RootfsBytes is the allocated size of the regular files left in the
	// directory after the sweep (what `du` reports for this env).
	RootfsBytes int64
}

// RootfsGC runs the sweep. Construct with NewRootfsGC.
type RootfsGC struct {
	owner RootfsGCOwner
	refs  *RootfsReferences
	cfg   RootfsGCConfig

	// Seams for tests. Production uses the platform implementations.
	now       func() time.Time
	fileTime  func(path string, fi os.FileInfo) time.Time
	openFiles func() (map[fileID]struct{}, error)

	cancel context.CancelFunc
	done   chan struct{}
}

// NewRootfsGC builds a GC without starting it.
func NewRootfsGC(owner RootfsGCOwner, refs *RootfsReferences, cfg RootfsGCConfig) *RootfsGC {
	def := DefaultRootfsGCConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = def.Interval
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = def.GracePeriod
	}
	if refs == nil {
		refs = NewRootfsReferences(0, nil)
	}
	return &RootfsGC{
		owner:     owner,
		refs:      refs,
		cfg:       cfg,
		now:       time.Now,
		fileTime:  newestChange,
		openFiles: openFileIDs,
	}
}

// newestChange returns the later of mtime and ctime. ctime moves on
// rename and link, so an artifact copied in with a preserved old mtime
// still counts as freshly installed.
func newestChange(path string, fi os.FileInfo) time.Time {
	t := fi.ModTime()
	if meta, err := statFile(path); err == nil && meta.ctime.After(t) {
		t = meta.ctime
	}
	return t
}

// Start runs one sweep immediately and then every Interval until Stop.
func (g *RootfsGC) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.done = make(chan struct{})
	go g.run(ctx)
}

// Stop cancels the loop and waits for it to exit.
func (g *RootfsGC) Stop() {
	if g.cancel == nil {
		return
	}
	g.cancel()
	<-g.done
}

func (g *RootfsGC) run(ctx context.Context) {
	defer close(g.done)
	logger.WithFields(
		"rootfs_dir", g.owner.RootfsDir(),
		"interval", g.cfg.Interval.String(),
		"grace_period", g.cfg.GracePeriod.String(),
		"enforce", g.cfg.Enforce,
		"keep", g.cfg.Keep,
	).Info("rootfs_gc: started")

	g.sweepAndRecord()
	ticker := time.NewTicker(g.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("rootfs_gc: stopped")
			return
		case <-ticker.C:
			g.sweepAndRecord()
		}
	}
}

func (g *RootfsGC) sweepAndRecord() {
	res, err := g.Sweep()
	if err != nil {
		recordRootfsGCOutcome("error", 1)
		logger.WithFields("error", err.Error()).Warn("rootfs_gc: sweep failed, nothing deleted")
		return
	}
	recordRootfsGCOutcome("deleted", len(res.Deleted))
	recordRootfsGCOutcome("would_delete", len(res.WouldDelete))
	recordRootfsGCOutcome("error", res.Errors)
	recordRootfsGCBytesFreed(res.BytesFreed)
	setRootfsDirBytes(res.RootfsBytes)
	logger.WithFields(
		"scanned", res.Scanned,
		"deleted", len(res.Deleted),
		"would_delete", len(res.WouldDelete),
		"kept", len(res.Kept),
		"bytes_freed", res.BytesFreed,
		"rootfs_bytes", res.RootfsBytes,
		"errors", res.Errors,
		"enforce", g.cfg.Enforce,
	).Info("rootfs_gc: sweep complete")
}

type gcCandidate struct {
	name    string
	path    string
	fi      os.FileInfo
	meta    fileMeta
	statErr error
}

// Sweep classifies every top-level regular file and deletes (or, in dry
// run, reports) the eligible ones. It returns an error, having deleted
// nothing, when the directory cannot be read or the in-use check cannot
// run.
func (g *RootfsGC) Sweep() (*RootfsGCResult, error) {
	res := &RootfsGCResult{Kept: map[string]string{}}
	rootfsDir := g.owner.RootfsDir()
	if rootfsDir == "" {
		return res, nil
	}
	dir := rootfsDir
	if g.cfg.Dir != "" && g.cfg.Dir != rootfsDir {
		// Two mounts of one host directory share its inode. Anything else
		// means the GC would judge one directory and delete in another.
		want, errWant := statFile(rootfsDir)
		got, errGot := statFile(g.cfg.Dir)
		if errWant != nil || errGot != nil || want.id != got.id {
			if errors.Is(errWant, fs.ErrNotExist) {
				return res, nil
			}
			return nil, fmt.Errorf("rootfs GC dir %s is not the rootfs dir %s (stat errors: %v, %v)",
				g.cfg.Dir, rootfsDir, errGot, errWant)
		}
		dir = g.cfg.Dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil
		}
		return nil, fmt.Errorf("read rootfs dir %s: %w", dir, err)
	}

	keep := map[string]string{"base": keepBase}
	for _, tag := range g.cfg.Keep {
		if tag = strings.TrimSpace(tag); tag != "" {
			if _, ok := keep[tag]; !ok {
				keep[tag] = keepConfigured
			}
		}
	}
	for _, img := range g.owner.TrackedImages() {
		if tag := RootfsTag(img); keep[tag] == "" {
			keep[tag] = keepTracked
		}
	}
	reported, fresh := g.refs.Tags()

	// Every file a symlink in the directory resolves to (following the
	// whole chain) is protected: something boots or reads it by another
	// name. Broken links protect nothing.
	linkTargets := map[fileID]struct{}{}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink == 0 {
			continue
		}
		if meta, err := statFile(filepath.Join(dir, e.Name())); err == nil {
			linkTargets[meta.id] = struct{}{}
		}
	}

	now := g.now()
	var candidates []gcCandidate
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue // directories, symlinks, sockets: never ours to delete
		}
		path := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				res.Errors++
			}
			continue
		}
		res.Scanned++
		meta, statErr := statFile(path)
		if meta.allocated == 0 {
			meta.allocated = fi.Size()
		}
		res.RootfsBytes += meta.allocated

		name := e.Name()
		if _, linked := linkTargets[meta.id]; linked && statErr == nil {
			res.Kept[name] = keepSymlinkTarget
			continue
		}
		if tag, owned := rootfsOwnerTag(name); owned {
			if reason := keep[tag]; reason != "" {
				res.Kept[name] = reason
				continue
			}
			if !fresh {
				res.Kept[name] = keepNoReferenceReport
				continue
			}
			if _, ok := reported[tag]; ok {
				res.Kept[name] = keepReferenced
				continue
			}
		}
		if now.Sub(g.fileTime(path, fi)) < g.cfg.GracePeriod {
			res.Kept[name] = keepRecent
			continue
		}
		candidates = append(candidates, gcCandidate{name: name, path: path, fi: fi, meta: meta, statErr: statErr})
	}
	if len(candidates) == 0 {
		return res, nil
	}

	// The in-use check runs only when there is something to delete, and
	// a failure deletes nothing: an unverifiable file is a kept file.
	open, err := g.openFiles()
	if err != nil {
		return nil, fmt.Errorf("in-use check: %w", err)
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })
	for _, c := range candidates {
		if c.statErr != nil {
			// Without an inode the in-use check cannot clear the file.
			res.Kept[c.name] = keepUnverified
			res.Errors++
			continue
		}
		if _, busy := open[c.meta.id]; busy {
			res.Kept[c.name] = keepInUse
			continue
		}
		fields := []any{
			"file", c.path,
			"size_bytes", c.fi.Size(),
			"allocated_bytes", c.meta.allocated,
			"mtime", c.fi.ModTime().UTC().Format(time.RFC3339),
		}
		if !g.cfg.Enforce {
			res.WouldDelete = append(res.WouldDelete, c.name)
			logger.WithFields(fields...).Info("rootfs_gc: would delete (dry run)")
			continue
		}
		// Re-check without following links: the name must still be the
		// regular file that was classified. os.Remove unlinks the name
		// and never follows a symlink, but a swapped-in file or link is
		// not what was judged eligible, so leave it for the next sweep.
		if cur, err := lstatFile(c.path); err != nil || !cur.regular || cur.id != c.meta.id {
			res.Errors++
			logger.WithFields(fields...).Warn("rootfs_gc: file changed since classification, not deleted")
			continue
		}
		if err := os.Remove(c.path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			res.Errors++
			logger.WithFields(append(fields, "error", err.Error())...).Warn("rootfs_gc: delete failed")
			continue
		}
		res.Deleted = append(res.Deleted, c.name)
		res.BytesFreed += c.meta.allocated
		res.RootfsBytes -= c.meta.allocated
		logger.WithFields(fields...).Info("rootfs_gc: deleted")
	}
	return res, nil
}

// RootfsReferences holds the image references gateways report as still
// needed. Reports are kept per reporter and expire after maxAge; the
// referenced set is the union of the unexpired ones. Keying by reporter
// matters because several gateway replicas (or deployments) can share
// one agent: the latest report alone would let one reporter's view
// delete an image another one still references.
//
// A reporter is "<name>/<instance>" (or just "<name>"). Only reports
// whose name is in the allowlist are recorded; an empty allowlist records
// nothing, so the GC keeps every tag image. Caller authentication is the
// transport's job; the allowlist only keeps an unexpected caller's view
// out of the GC.
//
// While any unexpired report is empty or marked unknown, the set is not
// fresh: an empty set says nothing about what is referenced, and an
// unknown reporter's part of the union is missing.
type RootfsReferences struct {
	maxAge  time.Duration
	allowed map[string]struct{}
	now     func() time.Time

	mu      sync.Mutex
	reports map[string]rootfsReport
}

type rootfsReport struct {
	tags    map[string]struct{}
	unknown bool
	at      time.Time
}

// DefaultRootfsReferenceMaxAge bounds how old a gateway report may be
// before the GC stops trusting it. Gateways report every few minutes.
const DefaultRootfsReferenceMaxAge = 30 * time.Minute

// NewRootfsReferences builds an empty store accepting reports from the
// allowed reporter names. maxAge <= 0 uses DefaultRootfsReferenceMaxAge.
func NewRootfsReferences(maxAge time.Duration, allowedReporters []string) *RootfsReferences {
	if maxAge <= 0 {
		maxAge = DefaultRootfsReferenceMaxAge
	}
	allowed := map[string]struct{}{}
	for _, name := range allowedReporters {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = struct{}{}
		}
	}
	return &RootfsReferences{maxAge: maxAge, allowed: allowed, now: time.Now, reports: map[string]rootfsReport{}}
}

// RootfsReporterName returns the allowlisted part of a reporter id.
func RootfsReporterName(reporter string) string {
	name, _, _ := strings.Cut(reporter, "/")
	return name
}

// Allowed reports whether reports from reporter are recorded.
func (r *RootfsReferences) Allowed(reporter string) bool {
	_, ok := r.allowed[RootfsReporterName(reporter)]
	return ok && reporter != ""
}

// Report replaces reporter's referenced set with images (image
// references, not tags; the store resolves them with RootfsTag). unknown
// marks a reporter that cannot vouch for its full set. It returns false,
// recording nothing, for a reporter outside the allowlist.
func (r *RootfsReferences) Report(reporter string, images []string, unknown bool) bool {
	if !r.Allowed(reporter) {
		return false
	}
	tags := make(map[string]struct{}, len(images))
	for _, img := range images {
		if img = strings.TrimSpace(img); img != "" {
			tags[RootfsTag(img)] = struct{}{}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.reports[reporter] = rootfsReport{tags: tags, unknown: unknown, at: now}
	for name, rep := range r.reports {
		if now.Sub(rep.at) > r.maxAge {
			delete(r.reports, name)
		}
	}
	return true
}

// Tags returns the union of unexpired reports. fresh is false when no
// unexpired report exists, or when any unexpired report is empty or
// unknown; callers must then treat every tag as referenced.
func (r *RootfsReferences) Tags() (map[string]struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	out := map[string]struct{}{}
	fresh := false
	for _, rep := range r.reports {
		if now.Sub(rep.at) > r.maxAge {
			continue
		}
		if rep.unknown || len(rep.tags) == 0 {
			return map[string]struct{}{}, false
		}
		fresh = true
		for tag := range rep.tags {
			out[tag] = struct{}{}
		}
	}
	return out, fresh
}
