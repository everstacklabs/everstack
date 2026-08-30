package firecracker

import (
	"context"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// OrphanReaperConfig tunes the in-process orphan VM reaper.
//
// The reaper replaces the cluster-side firecracker-vm-cleanup
// CronJob's blind kill (`pkill firecracker if older than 30 min`).
// That cron has no idea which fcagent pod owns which VM and would
// happily terminate a healthy long-lived sandbox. The agent-owned
// reaper only kills processes that are children of THIS fcagent
// process and not in its tracked-VM set, so a peer fcagent's VMs
// are off-limits and an in-flight create's VM gets a grace window.
type OrphanReaperConfig struct {
	Interval     time.Duration // How often to scan (default: 60s)
	GracePeriod  time.Duration // Min age before kill (default: 5m)
	DryRun       bool          // Log what would be killed but don't kill
}

// DefaultOrphanReaperConfig returns sensible defaults.
func DefaultOrphanReaperConfig() OrphanReaperConfig {
	return OrphanReaperConfig{
		Interval:    60 * time.Second,
		GracePeriod: 5 * time.Minute,
		DryRun:      false,
	}
}

// PIDLister is implemented by FirecrackerBackend; declared as an
// interface so the reaper is testable without spinning up VMs.
type PIDLister interface {
	TrackedPIDs() []int
}

// OrphanReaper periodically scans for child firecracker processes
// that the backend has lost track of and kills them.
type OrphanReaper struct {
	backend PIDLister
	cfg     OrphanReaperConfig
	cancel  context.CancelFunc
	done    chan struct{}
}

// StartOrphanReaper launches the reaper in a goroutine and returns
// it. Cancellation is via the returned reaper's Stop method.
func StartOrphanReaper(backend PIDLister, cfg OrphanReaperConfig) *OrphanReaper {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 5 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &OrphanReaper{
		backend: backend,
		cfg:     cfg,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go r.run(ctx)
	return r
}

func (r *OrphanReaper) Stop() {
	r.cancel()
	<-r.done
}

func (r *OrphanReaper) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *OrphanReaper) sweep() {
	myPID := os.Getpid()
	tracked := pidSet(r.backend.TrackedPIDs())

	candidates, err := findFirecrackerChildren(myPID)
	if err != nil {
		// On non-Linux this is a no-op (returns empty slice + nil
		// error from the platform stub); a real error here is the
		// /proc filesystem being unavailable, which is fatal for
		// the reaper but not for the agent — log and skip.
		logger.WithFields("error", err.Error()).
			Debug("orphan_reaper: unable to scan /proc, skipping")
		return
	}

	now := time.Now()
	killed := 0
	for _, c := range candidates {
		if _, isOurs := tracked[c.PID]; isOurs {
			continue
		}
		age := now.Sub(c.StartedAt)
		if age < r.cfg.GracePeriod {
			// Still in the grace window — could be a VM that's
			// mid-create and not yet inserted into b.vms.
			continue
		}
		if r.cfg.DryRun {
			logger.WithFields("pid", c.PID, "age", age.String()).
				Info("orphan_reaper: would kill (dry-run)")
			continue
		}
		if err := killProcess(c.PID); err != nil {
			logger.WithFields("pid", c.PID, "error", err.Error()).
				Warn("orphan_reaper: kill failed")
			continue
		}
		killed++
		logger.WithFields("pid", c.PID, "age", age.String()).
			Info("orphan_reaper: killed orphan firecracker process")
	}
	if killed > 0 {
		logger.WithFields("count", killed).Info("orphan_reaper: sweep complete")
	}
}

// podSliceRe extracts the Kubernetes pod-level cgroup slice
// (e.g. "kubepods-burstable-pod<uid>.slice") from a /proc/<pid>/cgroup
// file. The pod UID uses underscores. This segment is identical for a
// process and every child it forks, but DIFFERS between two fcagent
// pods (two DaemonSets) colocated on one node — the exact distinction
// the reaper needs so prod's reaper never kills dev's VMs.
var podSliceRe = regexp.MustCompile(`pod[0-9a-fA-F_]+\.slice`)

// parseCgroupOwner reduces a /proc/<pid>/cgroup file to an ownership
// fingerprint. On Kubernetes it's the pod-slice segment (stable across
// the pod's containers and any child processes); otherwise it falls
// back to the cgroup v2 unified path, then the raw content. A
// firecracker VM inherits its launching fcagent's cgroup, so the VM's
// fingerprint equals its owner's; a peer DaemonSet's VM differs.
func parseCgroupOwner(content string) string {
	if m := podSliceRe.FindString(content); m != "" {
		return m
	}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.HasPrefix(line, "0::") {
			return strings.TrimPrefix(line, "0::")
		}
	}
	return strings.TrimSpace(content)
}

func pidSet(pids []int) map[int]struct{} {
	out := make(map[int]struct{}, len(pids))
	for _, p := range pids {
		out[p] = struct{}{}
	}
	return out
}

// firecrackerChild describes a candidate process the reaper might
// kill: a firecracker process that is a child of fcagent.
type firecrackerChild struct {
	PID       int
	StartedAt time.Time
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGKILL)
}

