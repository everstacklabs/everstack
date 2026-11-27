package firecracker

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Host port guard: keeps guests off the agent's own listeners.
//
// Every guest's default route is its host-side TAP (.1 of its /30), and
// anything the guest sends to one of the host's own addresses (the TAP
// gateway, the node IP, the CNI bridge, an overlay IP) is delivered
// through the INPUT chain, not FORWARD. The per-VM chain in network.go
// only governs FORWARD, so without these rules the only thing between a
// guest and the agent's gRPC control plane is whatever INPUT policy the
// node happens to ship. Add explicit drops so the agent protects itself
// on any host, firewalled or not.
//
// Two rules per port set: one keyed on the guest-facing interfaces
// (catches any source the guest spoofs), one keyed on the guest pool
// subnet (catches a packet that somehow arrives on another interface).
// Both are inserted at INPUT position 1, ahead of the node's accept
// rules. The per-VM DNS accepts network.go installs at position 1 match
// port 53 only, so relative ordering between them is irrelevant.

// guestTapWildcard matches every per-VM TAP (see tapNameForVM). The
// subnet rule uses the configured guest pool (guest_pool.go), so a host
// whose own networks live in 10.0.0.0/16 can move the pool elsewhere
// and keep kubelet and node traffic unaffected.
const guestTapWildcard = "tap-+"

// HostPortGuardRules returns the iptables argument lists that drop
// guest-originated traffic to the given host TCP ports. Pure; safe to
// call on any platform.
func HostPortGuardRules(ports []int) [][]string {
	seen := map[int]struct{}{}
	var clean []int
	for _, p := range ports {
		if p <= 0 || p > 65535 {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil
	}
	sort.Ints(clean)
	strs := make([]string, len(clean))
	for i, p := range clean {
		strs[i] = strconv.Itoa(p)
	}
	dports := strings.Join(strs, ",")
	return [][]string{
		{"-I", "INPUT", "1", "-i", guestTapWildcard, "-p", "tcp", "-m", "multiport", "--dports", dports, "-j", "DROP"},
		{"-I", "INPUT", "1", "-s", GuestPoolCIDR(), "-p", "tcp", "-m", "multiport", "--dports", dports, "-j", "DROP"},
	}
}

// ErrHostPortGuardNotPersisted is returned when a guard rule was
// installed without error but does not show up in the INPUT dump
// afterwards. Callers must stop re-asserting on it: the likely cause is
// a canonicalisation mismatch between what we pass and what -S prints,
// and retrying would insert a fresh copy at position 1 on every tick.
var ErrHostPortGuardNotPersisted = fmt.Errorf("host port guard: rule did not persist")

// hostPortGuardPresent reports whether an `iptables -S INPUT` dump
// already contains the given guard rule. Fragment matching rather than
// `iptables -C`, for the reasons spelled out on verifyIptables.
func hostPortGuardPresent(dump string, rule []string) bool {
	// rule[3:] is everything after "-I INPUT 1"; -S prints it after
	// "-A INPUT " in the same order we passed it.
	body := strings.Join(rule[3:], " ")
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "-A INPUT ") && strings.Contains(line, body) {
			return true
		}
	}
	return false
}

// EnsureHostPortGuard installs the guard rules for the given ports if
// they are not already present, then verifies they landed. Idempotent;
// intended to be called at agent start and re-asserted periodically,
// since a host firewall reload can flush INPUT.
func EnsureHostPortGuard(ports []int) error {
	rules := HostPortGuardRules(ports)
	if len(rules) == 0 {
		return nil
	}
	dump, err := dumpInputChain()
	if err != nil {
		return fmt.Errorf("host port guard: read INPUT: %w", err)
	}
	installed := 0
	for _, rule := range rules {
		if hostPortGuardPresent(dump, rule) {
			continue
		}
		if err := runIptables(rule...); err != nil {
			return fmt.Errorf("host port guard: install %v: %w", rule, err)
		}
		installed++
	}
	if installed == 0 {
		return nil
	}
	dump, err = dumpInputChain()
	if err != nil {
		return fmt.Errorf("host port guard: verify INPUT: %w", err)
	}
	for _, rule := range rules {
		if !hostPortGuardPresent(dump, rule) {
			return fmt.Errorf("%w: %v", ErrHostPortGuardNotPersisted, rule)
		}
	}
	logger.WithFields("ports", ports, "rules", installed).Info("firecracker_network: host port guard installed")
	return nil
}

func dumpInputChain() (string, error) {
	out, err := exec.Command("iptables", "-w", strconv.Itoa(iptablesWaitSeconds), "-S", "INPUT").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("iptables -S INPUT: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
