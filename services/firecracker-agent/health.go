package main

// Host pressure signal for placement gating. Polled by the gateway's
// LoadBalancer via the GetNodeHealth RPC — when an fcagent reports
// unhealthy, the LoadBalancer skips it for new sandbox creates until
// the next poll cycle.
//
// Design borrowed from Daytona's runner-push pattern: each runner
// reports CPU/mem/disk/started-sandbox counts to the control plane,
// which uses them both as a liveness signal and a scheduling input.
// We pull instead of push because the gateway already has discovery
// + per-target gRPC clients; one more RPC method is cheaper than a
// new control-plane sink. The polling cadence (10s) is comparable.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker"
)

// Default thresholds reject new placement before resource pressure prevents
// the host from serving existing workloads. A conservative rejection can
// retry on a peer node; accepting placement on a saturated node can affect
// every workload on that node.
//
// Overridable via env so a small-disk dev node can tighten the
// disk threshold without a rebuild.
const (
	defaultDiskThresholdPct = 85.0
	defaultMemThresholdPct  = 90.0
	defaultCPUThresholdPct  = 95.0
	defaultFDThresholdPct   = 80.0

	// VM count uses a fraction of the configured max as the limit
	// rather than a percentage, because pool capacity is the most
	// honest upper bound — the pool reserves resources we know
	// fit, so being under the cap means the next Create has room
	// even if individual percentages are noisy.
	vmCountSlack = 1 // need at least one slot free
)

type pressureThresholds struct {
	diskPct float64
	memPct  float64
	cpuPct  float64
	fdPct   float64
}

func defaultPressureThresholds() pressureThresholds {
	return pressureThresholds{
		diskPct: getEnvFloat("FCAGENT_HEALTH_DISK_PCT", defaultDiskThresholdPct),
		memPct:  getEnvFloat("FCAGENT_HEALTH_MEM_PCT", defaultMemThresholdPct),
		cpuPct:  getEnvFloat("FCAGENT_HEALTH_CPU_PCT", defaultCPUThresholdPct),
		fdPct:   getEnvFloat("FCAGENT_HEALTH_FD_PCT", defaultFDThresholdPct),
	}
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return fallback
}

// collectHostHealth reads /proc + statfs + rlimit + backend state to
// produce a NodeHealth proto. Pure-read function: no side effects
// other than briefly opening files. Safe to call on every RPC.
func collectHostHealth(backend *firecracker.FirecrackerBackend, thresholds pressureThresholds) *fcpb.NodeHealth {
	now := time.Now().Unix()

	diskPct := diskUsedPct(backend.WorkDir())
	memPct := memUsedPct()
	cpuPct := cpuUsedPct()
	fdPct := fdUsedPct()

	vmCount := int32(len(backend.TrackedIDs()))
	vmMax := int32(backend.PoolMaxTotal())

	reasons := []string{}
	if diskPct > thresholds.diskPct {
		reasons = append(reasons, fmt.Sprintf("disk %.1f%%>%0.1f%%", diskPct, thresholds.diskPct))
	}
	if memPct > thresholds.memPct {
		reasons = append(reasons, fmt.Sprintf("mem %.1f%%>%0.1f%%", memPct, thresholds.memPct))
	}
	if cpuPct > thresholds.cpuPct {
		reasons = append(reasons, fmt.Sprintf("cpu %.1f%%>%0.1f%%", cpuPct, thresholds.cpuPct))
	}
	if fdPct > thresholds.fdPct {
		reasons = append(reasons, fmt.Sprintf("fd %.1f%%>%0.1f%%", fdPct, thresholds.fdPct))
	}
	if vmMax > 0 && vmCount >= vmMax-int32(vmCountSlack) {
		reasons = append(reasons, fmt.Sprintf("vm_count %d/%d (slack=%d)", vmCount, vmMax, vmCountSlack))
	}

	return &fcpb.NodeHealth{
		Healthy:       len(reasons) == 0,
		Reason:        strings.Join(reasons, "; "),
		DiskPct:       diskPct,
		MemPct:        memPct,
		CpuPct:        cpuPct,
		FdPct:         fdPct,
		VmCount:       vmCount,
		VmMax:         vmMax,
		TimestampUnix: now,
	}
}

// diskUsedPct returns the used percentage of the filesystem mounted
// at the given path. statfs is a single syscall, no I/O.
func diskUsedPct(path string) float64 {
	if path == "" {
		return 0
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	if st.Blocks == 0 {
		return 0
	}
	// Blocks: total; Bavail: free for unprivileged users. Match `df`.
	used := st.Blocks - st.Bavail
	return float64(used) / float64(st.Blocks) * 100.0
}

// memUsedPct returns (1 - MemAvailable/MemTotal) * 100, matching what
// the kernel exposes in /proc/meminfo. MemAvailable is the right
// signal — it accounts for reclaimable page cache, not raw "Free".
func memUsedPct() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, avail uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, _ = strconv.ParseUint(fields[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if total == 0 {
		return 0
	}
	used := total - avail
	return float64(used) / float64(total) * 100.0
}

// cpuUsedPct returns the 1-minute load average normalized by logical
// CPU count, clamped to [0, 100]. Coarse but stable — instantaneous
// usage from /proc/stat would oscillate too much for a placement
// signal that's polled every 10s.
func cpuUsedPct() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	// nproc — use _SC_NPROCESSORS_ONLN if available; fallback to a
	// /proc/cpuinfo count.
	cpus := numCPU()
	if cpus == 0 {
		return 0
	}
	pct := (load1 / float64(cpus)) * 100.0
	if pct > 100 {
		pct = 100
	}
	return pct
}

// numCPU counts logical processors from /proc/cpuinfo. Avoid runtime.NumCPU
// because it reflects GOMAXPROCS which can be capped by container limits.
// We want the host's view, not the agent's.
func numCPU() int {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "processor") {
			count++
		}
	}
	return count
}

// fdUsedPct returns the agent process's open-FD count as a percentage
// of its soft rlimit. FD pressure is a quiet killer — the agent stops
// being able to open /dev/kvm, vsock sockets, firecracker API sockets
// well before disk or memory show stress. NOFILE is read via prlimit
// to get the live kernel value (rlimit syscall reports the process's
// limit even after a runtime-set raise).
func fdUsedPct() float64 {
	used := countOpenFDs()
	if used == 0 {
		return 0
	}
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return 0
	}
	if rl.Cur == 0 {
		return 0
	}
	return float64(used) / float64(rl.Cur) * 100.0
}

func countOpenFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}
