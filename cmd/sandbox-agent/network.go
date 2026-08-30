package main

// applyNetworkPolicy reads SANDBOX_NETWORK_BLOCK_ALL and
// SANDBOX_NETWORK_ALLOW_CIDRS from the environment and applies
// iptables/nftables rules to enforce the requested egress policy.
//
// Called once at agent startup. Failures are logged but do not kill
// the agent -- a missed policy is worse UX than a broken policy.
//
// Environment variables:
//
//	SANDBOX_NETWORK_BLOCK_ALL=1 -- block all outbound egress (loopback + essentials kept open)
//	SANDBOX_NETWORK_ALLOW_CIDRS=10.0.0.0/8,192.168.0.0/16 -- additional CIDRs to permit
//
// Always-allowed (even with block_all):
//   - Loopback (127.0.0.0/8)
//   - Link-local (169.254.0.0/16)
//   - DNS (port 53)
//   - Our own fcagent tap IP range (172.16.0.0/12) for health probes

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func applyNetworkPolicy() {
	blockAll := os.Getenv("SANDBOX_NETWORK_BLOCK_ALL") == "1"
	if !blockAll {
		return
	}

	allowCIDRs := []string{}
	if raw := os.Getenv("SANDBOX_NETWORK_ALLOW_CIDRS"); raw != "" {
		for _, cidr := range strings.Split(raw, ",") {
			cidr = strings.TrimSpace(cidr)
			if cidr != "" {
				allowCIDRs = append(allowCIDRs, cidr)
			}
		}
	}

	// Prefer nftables if available (modern Linux), fall back to iptables.
	if _, err := exec.LookPath("nft"); err == nil {
		applyNftables(allowCIDRs)
	} else if _, err := exec.LookPath("iptables"); err == nil {
		applyIptables(allowCIDRs)
	} else {
		fmt.Fprintln(os.Stderr, "sandbox-agent: network policy: neither nft nor iptables found; skipping")
	}
}

// applyNftables creates an nftables filter that blocks all OUTPUT traffic
// except loopback, the always-allowed ranges, and caller-supplied CIDRs.
func applyNftables(allowCIDRs []string) {
	// Build accept rules for always-allowed and user CIDRs.
	var acceptRules strings.Builder
	for _, cidr := range alwaysAllowedCIDRs(allowCIDRs) {
		fmt.Fprintf(&acceptRules, "\t\tip daddr %s accept\n", cidr)
	}

	nft := fmt.Sprintf(`
table inet sandbox_policy {
  chain output {
    type filter hook output priority 0; policy drop;
    oif "lo" accept
    ip daddr 127.0.0.0/8 accept
    ip daddr 169.254.0.0/16 accept
    ip daddr 172.16.0.0/12 accept
    udp dport 53 accept
    tcp dport 53 accept
%s
  }
}`, acceptRules.String())

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nft)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: nftables policy apply failed: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "sandbox-agent: nftables egress policy applied (block_all=true)")
}

// applyIptables implements the same policy via iptables.
func applyIptables(allowCIDRs []string) {
	ipt := func(args ...string) {
		cmd := exec.Command("iptables", args...)
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}

	// Flush existing OUTPUT rules to start clean.
	ipt("-F", "OUTPUT")

	// Always-allow rules.
	for _, cidr := range alwaysAllowedCIDRs(allowCIDRs) {
		ipt("-A", "OUTPUT", "-d", cidr, "-j", "ACCEPT")
	}
	// Loopback.
	ipt("-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT")
	// DNS.
	ipt("-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT")
	ipt("-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
	// Default drop.
	ipt("-P", "OUTPUT", "DROP")

	fmt.Fprintln(os.Stderr, "sandbox-agent: iptables egress policy applied (block_all=true)")
}

// alwaysAllowedCIDRs returns the combined list of always-permitted ranges
// and caller-supplied ranges, deduplicated.
func alwaysAllowedCIDRs(extra []string) []string {
	always := []string{
		"127.0.0.0/8",    // loopback
		"169.254.0.0/16", // link-local (metadata, fcagent TAP)
		"172.16.0.0/12",  // private: fcagent TAP default range
	}
	seen := make(map[string]bool)
	for _, c := range always {
		seen[c] = true
	}
	result := append([]string(nil), always...)
	for _, c := range extra {
		if !seen[c] {
			seen[c] = true
			result = append(result, c)
		}
	}
	return result
}
