package main

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker"
)

// hostPortGuardRecheck is how often the INPUT guard rules are
// re-asserted. Matches the preflight re-check cadence.
const hostPortGuardRecheck = 30 * time.Second

// maintainHostPortGuard installs the host firewall rules that drop
// guest-originated traffic to this agent's listeners, and keeps them
// installed until ctx is done. Failures are logged on every tick so a
// node that lost its iptables binary or had the rules flushed shows up
// in the logs, but they never take the agent down.
//
// One exception: if a rule installs cleanly but cannot be seen in the
// chain afterwards, re-asserting would stack a new copy at INPUT
// position 1 every tick. That is logged once and the loop stops; the
// other two layers (token, narrow bind) still hold.
func maintainHostPortGuard(ctx context.Context, ports []int) {
	if len(ports) == 0 {
		return
	}
	// assert returns false when the loop must stop re-asserting.
	assert := func() bool {
		err := firecracker.EnsureHostPortGuard(ports)
		if err == nil {
			return true
		}
		if errors.Is(err, firecracker.ErrHostPortGuardNotPersisted) {
			log.Printf("firecracker-agent: ERROR host port guard cannot be verified, not retrying (check iptables -S INPUT by hand): %v", err)
			return false
		}
		log.Printf("firecracker-agent: WARN host port guard not in place: %v", err)
		return true
	}
	if !assert() {
		return
	}
	ticker := time.NewTicker(hostPortGuardRecheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !assert() {
				return
			}
		}
	}
}

// resolveListenAddr narrows a wildcard listen address to the node's own
// IP when one is known. The agent runs with hostNetwork, so ":9090"
// binds every interface on the node: the pod/CNI bridge, any overlay
// (Tailscale, flannel), and the per-VM TAP gateways. Legitimate callers
// only ever dial the node IP (the headless Service resolves to it), so
// that is the only address worth listening on.
//
// Returns the address to bind and whether it is still a wildcard, so
// the caller can log loudly when it could not narrow it.
func resolveListenAddr(addr, nodeIP string) (string, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, false
	}
	wildcard := host == "" || host == "0.0.0.0" || host == "::"
	if !wildcard {
		return addr, false
	}
	nodeIP = strings.TrimSpace(nodeIP)
	if nodeIP == "" {
		return addr, true
	}
	return net.JoinHostPort(nodeIP, port), false
}

// listenPorts extracts the TCP ports from the given listen addresses,
// skipping anything that does not parse. Used to size the host firewall
// guard to exactly the ports this agent serves.
func listenPorts(addrs ...string) []int {
	var ports []int
	for _, a := range addrs {
		_, portStr, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 || p > 65535 {
			continue
		}
		ports = append(ports, p)
	}
	return ports
}
