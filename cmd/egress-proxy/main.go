// Binary egress-proxy runs inside a sidecar container to enforce DNS-based
// egress controls. It reads configuration from environment variables:
//
//	EGRESS_ALLOWED_HOSTS  — comma-separated domain allowlist (supports *.example.com wildcards)
//	EGRESS_UPSTREAM       — comma-separated upstream DNS servers (default: 8.8.8.8:53,1.1.1.1:53)
//	EGRESS_SANDBOX_ID     — sandbox ID for audit logging
//	EGRESS_LISTEN_ADDR    — listen address (default: 127.0.0.1:15353)
//
// On startup it also sets an iptables REDIRECT rule to capture all outbound
// DNS traffic (UDP port 53) to the proxy's listen port.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/everstacklabs/everstack/internal/sandbox/egress"
)

func main() {
	allowedHosts := splitCSV(os.Getenv("EGRESS_ALLOWED_HOSTS"))
	upstream := splitCSV(os.Getenv("EGRESS_UPSTREAM"))
	sandboxID := os.Getenv("EGRESS_SANDBOX_ID")
	listenAddr := os.Getenv("EGRESS_LISTEN_ADDR")

	if listenAddr == "" {
		listenAddr = "127.0.0.1:15353"
	}

	// Extract port from listen address for iptables redirect
	parts := strings.Split(listenAddr, ":")
	redirectPort := parts[len(parts)-1]

	// Set up iptables redirect: all outbound DNS (UDP 53) -> our proxy
	if err := setupIPTables(redirectPort); err != nil {
		fmt.Fprintf(os.Stderr, "egress-proxy: iptables setup failed: %v\n", err)
		// Continue anyway — DNS proxy still works if clients query it directly
	}

	proxy := egress.NewDNSProxy(egress.DNSProxyConfig{
		AllowedHosts: allowedHosts,
		Upstream:     upstream,
		SandboxID:    sandboxID,
		ListenAddr:   listenAddr,
	})

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		proxy.Stop()
		cleanupIPTables(redirectPort)
		os.Exit(0)
	}()

	if err := proxy.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "egress-proxy: DNS proxy failed: %v\n", err)
		os.Exit(1)
	}
}

// setupIPTables redirects all outbound UDP DNS (port 53) to the proxy port.
func setupIPTables(port string) error {
	cmd := exec.Command("iptables",
		"-t", "nat",
		"-A", "OUTPUT",
		"-p", "udp",
		"--dport", "53",
		"-j", "REDIRECT",
		"--to-ports", port,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// cleanupIPTables removes the redirect rule.
func cleanupIPTables(port string) {
	exec.Command("iptables",
		"-t", "nat",
		"-D", "OUTPUT",
		"-p", "udp",
		"--dport", "53",
		"-j", "REDIRECT",
		"--to-ports", port,
	).Run()
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
