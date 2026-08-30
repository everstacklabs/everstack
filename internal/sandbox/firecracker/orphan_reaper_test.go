package firecracker

import (
	"testing"
	"time"
)

type fakeBackend struct{ pids []int }

func (f *fakeBackend) TrackedPIDs() []int { return f.pids }

// TestPidSet verifies the set construction the reaper uses to
// distinguish tracked from untracked PIDs.
func TestPidSet(t *testing.T) {
	s := pidSet([]int{1, 2, 3, 1})
	if len(s) != 3 {
		t.Fatalf("expected 3 unique pids, got %d", len(s))
	}
	for _, want := range []int{1, 2, 3} {
		if _, ok := s[want]; !ok {
			t.Fatalf("missing pid %d", want)
		}
	}
}

// TestParseCgroupOwner is the regression guard for the cross-DaemonSet
// reaper kill (the real "5-minute sandbox death"): the prod fcagent's
// reaper SIGKILLed dev VMs because a comm-only /proc scan can't tell
// dev's firecracker processes from prod's. Ownership is now decided by
// cgroup fingerprint. This proves the two invariants that fix relies on:
// (1) a fcagent and the VM it forks share a fingerprint (same pod), and
// (2) dev and prod fingerprints DIFFER (different pods).
func TestParseCgroupOwner(t *testing.T) {
	// Real cgroup-v2 shapes from the node (vps-b5be49f6).
	devAgent := "0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod8e09f4cb_416c_46fe_b149_ec936cc238da.slice/cri-containerd-aaaa.scope\n"
	// A firecracker VM forked by the dev agent inherits the same cgroup.
	devVM := "0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod8e09f4cb_416c_46fe_b149_ec936cc238da.slice/cri-containerd-aaaa.scope\n"
	prodVM := "0::/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod402fe918_ff71_48c6_8b95_7151ef62d158.slice/cri-containerd-bbbb.scope\n"

	dAgent, dVM, pVM := parseCgroupOwner(devAgent), parseCgroupOwner(devVM), parseCgroupOwner(prodVM)

	if dAgent == "" {
		t.Fatal("dev agent fingerprint empty (would fail-safe to no-reap)")
	}
	if dAgent != dVM {
		t.Fatalf("agent and its own VM must match: %q vs %q", dAgent, dVM)
	}
	if dVM == pVM {
		t.Fatalf("dev and prod VMs must NOT match — cross-DS kill would recur: both %q", dVM)
	}

	// Empty / unreadable content → "" so callers fail safe (never kill).
	if got := parseCgroupOwner(""); got != "" {
		t.Fatalf("empty cgroup should yield empty fingerprint, got %q", got)
	}

	// Non-k8s host (bare cgroup v2 path) still yields a stable, usable id.
	if got := parseCgroupOwner("0::/system.slice/firecracker.service\n"); got != "/system.slice/firecracker.service" {
		t.Fatalf("v2 fallback path wrong: %q", got)
	}
}

// TestStartOrphanReaper_DryRunNoOp boots the reaper on a backend
// with no tracked PIDs and a tiny interval. On non-Linux builds
// findFirecrackerChildren is a stub returning empty, so no kill is
// attempted. We just verify the goroutine starts and stops cleanly.
func TestStartOrphanReaper_DryRunNoOp(t *testing.T) {
	r := StartOrphanReaper(&fakeBackend{pids: []int{42}}, OrphanReaperConfig{
		Interval:    50 * time.Millisecond,
		GracePeriod: 1 * time.Second,
		DryRun:      true,
	})
	time.Sleep(150 * time.Millisecond)
	r.Stop()
}
