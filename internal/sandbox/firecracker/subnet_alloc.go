package firecracker

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// subnetAlloc tracks the 10.0.<n>.0/30 third-octets currently in use
// by running VMs on this host. Replaces the old `rand.Intn(254)+1`
// allocator that had no collision check at all — under any non-trivial
// load that scheme eventually picked the same subnet twice, broke
// `ip addr add` for the second VM, and surfaced as "the sandbox
// randomly has no network."
//
// Range is intentionally just /16 inside RFC1918 10.0.0.0/16, sliced
// into /30 (4 addresses, 2 usable: host TAP and guest). 254 subnets
// per host is more than the practical cap for Firecracker density on
// a single node (memory budget is usually the limit well below that),
// so exhaustion is informational rather than load-bearing.
var (
	subnetAllocMu sync.Mutex
	subnetInUse   = map[int]struct{}{}
	// subnetCursor advances on each allocation so consecutive VMs land
	// on consecutive subnets — easier for operators tailing iptables
	// rules than the random scatter we had before. Wraps modulo 254.
	subnetCursor int
)

// ErrSubnetsExhausted is returned by AllocateSubnet when all 254
// per-host slots are in use. Surfaced upward so the CreateVM path
// can reject cleanly with a 503-shaped error instead of corrupting
// the network state of an existing VM.
var ErrSubnetsExhausted = fmt.Errorf("firecracker: no free /30 subnets in 10.0.0.0/16 (max 254 per host)")

// AllocateSubnet returns the lowest free third-octet in [1, 254] and
// records it as in-use. The caller must call ReleaseSubnet on VM
// teardown OR call ReserveSubnet during recovery to seed an existing
// claim — otherwise the slot leaks until the agent process exits.
//
// The in-memory subnetInUse map only tracks THIS fcagent's own
// allocations. When two fcagent DaemonSets share a node (everstack-dev
// + everstack-prod, hostNetwork=true), both allocate /30s from the same
// 10.0.0.0/16, so two independent in-memory allocators pick the same
// slot and the second VM's DNS proxy fails to bind 10.0.<n>.1:53
// ("address already in use") — a hard create failure that repeats
// forever because the failed create releases the slot and the cursor
// stays put. To stay correct on a shared node we additionally consult
// the host's actually-assigned TAP addresses, which reflect EVERY
// fcagent's VMs on the node, and skip any slot already claimed there.
func AllocateSubnet() (int, error) {
	subnetAllocMu.Lock()
	defer subnetAllocMu.Unlock()

	// Cross-process truth: 10.0.<n>.1 addresses assigned on the host
	// (this agent's VMs AND any peer DaemonSet's). Re-read each call so
	// it always reflects current reality; best-effort (empty on error →
	// falls back to the in-memory map alone, i.e. the old behavior).
	return allocateLocked(hostAssignedSubnets())
}

// allocateLocked is AllocateSubnet's core; caller holds subnetAllocMu.
// Split out so tests can inject the host-assigned set directly.
func allocateLocked(hostTaken map[int]struct{}) (int, error) {
	// Probe up to 254 positions starting at the cursor so contiguous
	// allocations stay contiguous after a series of releases.
	for attempt := 0; attempt < 254; attempt++ {
		candidate := ((subnetCursor + attempt) % 254) + 1
		if _, taken := subnetInUse[candidate]; taken {
			continue
		}
		if _, taken := hostTaken[candidate]; taken {
			// A peer fcagent (or a leaked/recovered VM) already holds
			// 10.0.<candidate>.1 on the shared host. Skip; don't persist
			// into subnetInUse — hostTaken is re-scanned every call, so a
			// slot the peer later frees becomes allocatable again.
			continue
		}
		subnetInUse[candidate] = struct{}{}
		subnetCursor = candidate
		return candidate, nil
	}
	return 0, ErrSubnetsExhausted
}

// hostTapIPRe matches a host-side per-VM TAP address (10.0.<n>.1/30) in
// `ip -o addr show` output. .1 is always the host TAP end of a /30; the
// guest is .2. Anchoring on ".1/30" avoids matching guest addresses.
var hostTapIPRe = regexp.MustCompile(`inet 10\.0\.(\d{1,3})\.1/30`)

// hostAssignedSubnets returns the set of /30 third-octets whose host
// TAP address is currently assigned on this node. Best-effort: any
// error reading host state returns nil, and AllocateSubnet degrades to
// the in-memory map alone.
func hostAssignedSubnets() map[int]struct{} {
	out, err := exec.Command("ip", "-4", "-o", "addr", "show").Output()
	if err != nil {
		return nil
	}
	return parseAssignedSubnets(string(out))
}

// parseAssignedSubnets extracts in-use /30 third-octets from the output
// of `ip -4 -o addr show`. Pure (no host calls) so it is unit-testable.
func parseAssignedSubnets(ipOutput string) map[int]struct{} {
	set := make(map[int]struct{})
	for _, m := range hostTapIPRe.FindAllStringSubmatch(ipOutput, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 254 {
			set[n] = struct{}{}
		}
	}
	return set
}

// ReleaseSubnet marks a subnet as free for reuse. Idempotent —
// releasing an unallocated subnet is not an error.
func ReleaseSubnet(n int) {
	if n <= 0 || n > 254 {
		return
	}
	subnetAllocMu.Lock()
	delete(subnetInUse, n)
	subnetAllocMu.Unlock()
}

// ReserveSubnet claims a specific subnet, returning an error if it's
// already in use. Used by recovery to repopulate the allocator from
// persisted NetworkConfig — without this, the recovered VM's subnet
// would be invisible to the allocator and a future Allocate could
// hand the same slot to a new VM.
func ReserveSubnet(n int) error {
	if n <= 0 || n > 254 {
		return fmt.Errorf("firecracker: subnet %d out of range [1, 254]", n)
	}
	subnetAllocMu.Lock()
	defer subnetAllocMu.Unlock()
	if _, taken := subnetInUse[n]; taken {
		return fmt.Errorf("firecracker: subnet 10.0.%d.0/30 already in use", n)
	}
	subnetInUse[n] = struct{}{}
	return nil
}

// subnetFromHostIP parses the third octet of a "10.0.<n>.1" host IP
// so recovery can pass it to ReserveSubnet without re-deriving the
// allocation scheme from the IP format.
func subnetFromHostIP(hostIP string) (int, error) {
	parts := strings.Split(hostIP, ".")
	if len(parts) != 4 || parts[0] != "10" || parts[1] != "0" {
		return 0, fmt.Errorf("firecracker: hostIP %q does not match 10.0.<n>.1", hostIP)
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("firecracker: hostIP %q has non-numeric third octet: %w", hostIP, err)
	}
	return n, nil
}
