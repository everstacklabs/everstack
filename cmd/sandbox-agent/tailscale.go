package main

// Tailscale VPN integration (POR-87).
//
// If SANDBOX_TAILSCALE_AUTH_KEY is set, the sandbox-agent runs `tailscale up`
// to join the customer's Tailnet. The sandbox appears as a device with a
// Tailscale IP, enabling access to private services by IP.
//
// Called once at agent startup after network policy is applied.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// applyTailscaleVPN starts Tailscale if SANDBOX_TAILSCALE_AUTH_KEY is set.
// Best-effort: failures are logged but do not kill the agent.
func applyTailscaleVPN() string {
	authKey := os.Getenv("SANDBOX_TAILSCALE_AUTH_KEY")
	if authKey == "" {
		return ""
	}

	// Find tailscale binary.
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-agent: tailscale not found (install it in the sandbox image)")
		return ""
	}

	// Determine a stable hostname from the sandbox ID env var.
	sandboxID := os.Getenv("SANDBOX_ID")
	hostname := "sandbox"
	if sandboxID != "" {
		hostname = "everstack-" + sandboxID
		if len(hostname) > 63 { // DNS label max
			hostname = hostname[:63]
		}
	}

	// Start the tailscale daemon in the background first.
	if _, err := exec.LookPath("tailscaled"); err == nil {
		daemon := exec.Command("tailscaled",
			"--tun=userspace-networking",
			"--socks5-server=localhost:1055",
			"--outbound-http-proxy-listen=localhost:1055",
		)
		daemon.Stdout = os.Stderr
		daemon.Stderr = os.Stderr
		if err := daemon.Start(); err == nil {
			time.Sleep(500 * time.Millisecond) // give daemon time to bind
		}
	}

	// Run tailscale up.
	args := []string{
		"up",
		"--authkey=" + authKey,
		"--hostname=" + hostname,
		"--accept-routes",
		"--timeout=30s",
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: tailscale up failed: %v\n", err)
		return ""
	}

	// Get our Tailscale IP.
	out, err := exec.Command(bin, "ip", "--4").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-agent: tailscale ip: failed to get IP")
		return ""
	}
	ip := strings.TrimSpace(string(out))
	fmt.Fprintf(os.Stderr, "sandbox-agent: joined tailnet as %s (IP: %s)\n", hostname, ip)
	return ip
}
