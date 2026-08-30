package firecracker

// WorkdirReconciler periodically sweeps the fcagent's work directory and
// reaps orphan workdirs — per-VM subdirectories that no longer have a
// corresponding entry in the backend's tracked-VM set.
//
// Failure mode this addresses (lived through 2026-05-20): a half-created
// VM leaves a rootfs overlay, vsock socket, and (sometimes) state.json
// in /var/lib/everstack/vms/<id>/. The TAP + iptables rules may also
// linger. Each leaked workdir consumes hundreds of MB. Across enough
// failed creates the host hits disk pressure, vsock writes start
// timing out, and every NEW create fails too — a cascading outage
// from one accumulated mistake.
//
// This complements OrphanReaper (which kills orphan firecracker
// processes). Process orphans and workdir orphans are independent
// failure classes: a process can die cleanly leaving its workdir
// behind, and a workdir can leak from a half-create whose firecracker
// process never started. We reap both, separately.
//
// Design: 60-second tick + 5-minute grace period. The grace window
// keeps us from killing a workdir mid-Create — Create writes
// state.json near the end of its happy path, so a workdir that's
// 30s old without state.json is just an in-flight create, not an
// orphan. e2b uses a 1-minute grace; we run with 5 here because
// our Create path can be slower (rootfs overlay creation + first-
// boot warmup) and we don't want false positives on slow nodes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// WorkdirReconcilerConfig tunes the reconciler. Zero values fall back
// to sensible defaults via DefaultWorkdirReconcilerConfig.
type WorkdirReconcilerConfig struct {
	// Interval between sweeps. Default 60s.
	Interval time.Duration

	// GracePeriod is the minimum workdir age before it's eligible to
	// be reaped. Default 5 minutes. Workdirs whose mtime is newer
	// than now-GracePeriod are skipped — they're likely in-flight
	// creates whose state.json hasn't been written yet.
	GracePeriod time.Duration

	// DryRun logs what would be reaped without actually removing
	// anything. Useful for bringing the reconciler up on a node with
	// pre-existing leaks before letting it act.
	DryRun bool
}

// DefaultWorkdirReconcilerConfig returns the production defaults.
func DefaultWorkdirReconcilerConfig() WorkdirReconcilerConfig {
	return WorkdirReconcilerConfig{
		Interval:    60 * time.Second,
		GracePeriod: 5 * time.Minute,
		DryRun:      false,
	}
}

// WorkdirOwner is the contract WorkdirReconciler needs from the backend.
// Kept narrow so tests can mock it without spinning up VMs.
type WorkdirOwner interface {
	TrackedIDs() map[string]struct{}
	WorkDir() string
}

// WorkdirReconciler runs the periodic sweep.
type WorkdirReconciler struct {
	owner  WorkdirOwner
	cfg    WorkdirReconcilerConfig
	cancel context.CancelFunc
	done   chan struct{}
}

// StartWorkdirReconciler launches the reconciler in a goroutine and
// returns the handle. Stop via the handle's Stop method.
func StartWorkdirReconciler(owner WorkdirOwner, cfg WorkdirReconcilerConfig) *WorkdirReconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &WorkdirReconciler{
		owner:  owner,
		cfg:    cfg,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go r.run(ctx)
	return r
}

// Stop cancels the reconciler goroutine and blocks until it exits.
func (r *WorkdirReconciler) Stop() {
	r.cancel()
	<-r.done
}

func (r *WorkdirReconciler) run(ctx context.Context) {
	defer close(r.done)

	// Don't sweep on the very first tick — give Recover() a chance to
	// rehydrate b.vms first, so the initial sweep doesn't see every
	// recovered VM's workdir as an orphan. The ticker fires after one
	// Interval has elapsed, which is plenty.
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	logger.WithFields(
		"interval", r.cfg.Interval.String(),
		"grace_period", r.cfg.GracePeriod.String(),
		"workdir", r.owner.WorkDir(),
		"dry_run", r.cfg.DryRun,
	).Info("workdir_reconciler: started")

	for {
		select {
		case <-ctx.Done():
			logger.Info("workdir_reconciler: stopped")
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

// SweepResult summarizes one pass. Returned from Sweep so tests and
// future metrics endpoints can introspect what happened.
type SweepResult struct {
	Scanned     int
	Live        int
	InFlight    int
	Reaped      int
	BytesFreed  int64
	Errors      int
	ReapedIDs   []string
	InFlightIDs []string
}

func (r *WorkdirReconciler) sweep() {
	res, err := r.Sweep()
	if err != nil {
		logger.WithFields("error", err.Error()).
			Warn("workdir_reconciler: sweep failed")
		return
	}
	// Surface every sweep's classification to metrics, even quiet
	// ones with zero reaps — a flatline gauge tells the operator
	// "reconciler is running and finding nothing to do" which is the
	// healthy steady state.
	recordWorkdirOutcome("live", res.Live)
	recordWorkdirOutcome("in_flight", res.InFlight)
	recordWorkdirOutcome("reaped", res.Reaped)
	recordWorkdirOutcome("error", res.Errors)
	recordWorkdirBytesFreed(res.BytesFreed)

	if res.Reaped > 0 || res.Errors > 0 {
		logger.WithFields(
			"scanned", res.Scanned,
			"live", res.Live,
			"in_flight", res.InFlight,
			"reaped", res.Reaped,
			"bytes_freed", res.BytesFreed,
			"errors", res.Errors,
			"reaped_ids", res.ReapedIDs,
		).Info("workdir_reconciler: sweep complete")
	}
}

// Sweep runs one classification + reap pass and returns counters.
// Exposed (capitalized) so tests can drive it directly.
func (r *WorkdirReconciler) Sweep() (*SweepResult, error) {
	workRoot := r.owner.WorkDir()
	if workRoot == "" {
		return &SweepResult{}, nil
	}

	entries, err := os.ReadDir(workRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &SweepResult{}, nil
		}
		return nil, fmt.Errorf("read workdir %s: %w", workRoot, err)
	}

	tracked := r.owner.TrackedIDs()
	now := time.Now()
	res := &SweepResult{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		res.Scanned++

		// Tracked → never reap. The VM is live in memory.
		if _, ok := tracked[id]; ok {
			res.Live++
			continue
		}

		vmDir := filepath.Join(workRoot, id)
		info, err := os.Stat(vmDir)
		if err != nil {
			res.Errors++
			logger.WithFields("dir", vmDir, "error", err.Error()).
				Warn("workdir_reconciler: stat failed")
			continue
		}

		age := now.Sub(info.ModTime())
		if age < r.cfg.GracePeriod {
			// Could be an in-flight create that hasn't been
			// inserted into b.vms yet, or a VM whose state.json
			// is mid-write. Skip — next sweep will catch it if
			// it's a real orphan.
			res.InFlight++
			res.InFlightIDs = append(res.InFlightIDs, id)
			continue
		}

		// One more guard: if state.json exists and the recorded PID
		// is still alive, this is a recovered-but-not-yet-rehydrated
		// VM. Skip and let Recover handle it on the next agent start.
		// (Shouldn't happen in steady state — Recover runs at boot —
		// but cheap to check and prevents data loss from a misfire.)
		//
		// CROSS-DAEMONSET SAFETY: when two fcagent DaemonSets share a
		// node (everstack-dev + everstack-prod) they also share this
		// hostPath workdir, so a peer's live VM appears here as an
		// "untracked" dir. This aliveness check is what stops us from
		// RemoveAll-ing a peer's running VM out from under it — but only
		// because hostPID=true lets Signal(0) see the peer's PID across
		// pods. If fcagent ever drops hostPID, this guard goes blind and
		// cross-DS workdir reaping re-opens; scope by cgroup ownership
		// (see cgroupOwnerID in orphan_reaper) or split the hostPaths.
		if isAliveByStateFile(vmDir) {
			res.Live++
			continue
		}

		// Real orphan. Reap.
		freed, err := r.reap(vmDir)
		if err != nil {
			res.Errors++
			logger.WithFields(
				"sandbox_id", id,
				"dir", vmDir,
				"age", age.String(),
				"error", err.Error(),
			).Warn("workdir_reconciler: reap failed")
			continue
		}
		res.Reaped++
		res.BytesFreed += freed
		res.ReapedIDs = append(res.ReapedIDs, id)
		if r.cfg.DryRun {
			logger.WithFields("sandbox_id", id, "dir", vmDir, "age", age.String(), "would_free_bytes", freed).
				Info("workdir_reconciler: would reap (dry-run)")
		} else {
			logger.WithFields("sandbox_id", id, "dir", vmDir, "age", age.String(), "freed_bytes", freed).
				Info("workdir_reconciler: reaped orphan")
		}
	}
	return res, nil
}

// reap removes one orphan workdir's resources: TAP + iptables (from
// state.json if present), rootfs overlay (handled implicitly by
// RemoveAll), and the directory itself. Returns the total bytes
// freed. Best-effort: partial failures log but the function
// continues so the workdir doesn't half-survive.
func (r *WorkdirReconciler) reap(vmDir string) (int64, error) {
	freed := dirSize(vmDir) // measure before delete

	// Try to parse state.json so we can clean up TAP + iptables
	// even when the firecracker process is long gone. Missing or
	// corrupt state.json isn't fatal — the workdir on its own
	// still consumes disk and we want it gone.
	statePath := filepath.Join(vmDir, "state.json")
	if raw, err := os.ReadFile(statePath); err == nil {
		var s vmState
		if jerr := json.Unmarshal(raw, &s); jerr == nil && s.Network != nil {
			CleanupNetwork(s.Network)
		}
	}

	if r.cfg.DryRun {
		return freed, nil
	}
	if err := os.RemoveAll(vmDir); err != nil {
		return 0, fmt.Errorf("remove workdir: %w", err)
	}
	return freed, nil
}

// isAliveByStateFile returns true if the workdir's state.json points
// to a still-running firecracker process. Best-effort — any parse or
// stat failure returns false (treat as dead).
func isAliveByStateFile(vmDir string) bool {
	raw, err := os.ReadFile(filepath.Join(vmDir, "state.json"))
	if err != nil {
		return false
	}
	var s vmState
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	if s.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(s.PID)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// dirSize returns the total bytes consumed by a directory tree.
// Best-effort: unreadable entries are skipped, the partial total is
// returned. Used for the "freed bytes" metric, not for accounting,
// so eventual consistency is fine.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// Ensure tracked-ID lookup doesn't surface IDs that look like
// internal/temporary names (e.g., the agent's own "_tmp" scratch).
// Kept here as a no-op for now — VM IDs are well-formed UUIDs/prefixed
// strings, not subject to this concern — but the hook is here for
// when the workdir hierarchy grows.
var _ = strings.HasPrefix
