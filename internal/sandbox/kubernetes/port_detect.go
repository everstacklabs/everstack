package kubernetes

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// Compile-time check that KubernetesBackend implements PortDetector.
var _ sandbox.PortDetector = (*KubernetesBackend)(nil)

// DetectListeningPorts discovers TCP ports listening inside the sandbox pod
// by exec-ing `ss -tlnp` into the container. Falls back to parsing
// /proc/net/tcp and /proc/net/tcp6 if ss is unavailable.
func (b *KubernetesBackend) DetectListeningPorts(ctx context.Context, id string) ([]sandbox.ListeningPort, error) {
	// Try ss first (more reliable, includes process info)
	ssCtx, ssCancel := context.WithTimeout(ctx, 5*time.Second)
	defer ssCancel()
	ssResult, err := b.Exec(ssCtx, id, sandbox.ExecRequest{
		Command: []string{"ss", "-tlnp"},
		Timeout: 5 * time.Second,
	})
	if err == nil && ssResult.ExitCode == 0 && ssResult.Stdout != "" {
		return parseSSOutput(ssResult.Stdout), nil
	}

	// Fallback: parse /proc/net/tcp and /proc/net/tcp6
	var ports []sandbox.ListeningPort
	for _, proto := range []struct {
		file     string
		protocol string
	}{
		{"/proc/net/tcp", "tcp"},
		{"/proc/net/tcp6", "tcp6"},
	} {
		content, err := b.ReadFile(ctx, id, proto.file)
		if err != nil {
			continue
		}
		ports = append(ports, parseProcNetTCP(string(content), proto.protocol)...)
	}

	return ports, nil
}

// parseSSOutput parses the output of `ss -tlnp` into ListeningPort entries.
// Example line: "LISTEN 0 128 0.0.0.0:5173 0.0.0.0:* users:(("node",pid=42,fd=19))"
func parseSSOutput(output string) []sandbox.ListeningPort {
	var ports []sandbox.ListeningPort
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LISTEN") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		addr, portStr := parseSSAddr(fields[3])
		portNum, err := strconv.Atoi(portStr)
		if err != nil || portNum == 0 {
			continue
		}

		protocol := "tcp"
		if strings.HasPrefix(addr, "[") || addr == "::" || addr == "::1" {
			protocol = "tcp6"
		}

		lp := sandbox.ListeningPort{
			Port:     portNum,
			Protocol: protocol,
			Address:  addr,
		}

		for _, f := range fields {
			if strings.HasPrefix(f, "users:") {
				lp.Process, lp.PID = parseSSUsers(f)
				break
			}
		}

		ports = append(ports, lp)
	}
	return ports
}

// parseSSAddr splits an ss local address field like "0.0.0.0:5173" or "[::]:8080".
func parseSSAddr(s string) (addr, port string) {
	if idx := strings.LastIndex(s, "]:"); idx >= 0 {
		return s[1:idx], s[idx+2:]
	}
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

// parseSSUsers extracts process name and PID from ss users field.
// Format: users:(("node",pid=42,fd=19))
func parseSSUsers(s string) (process string, pid int) {
	if idx := strings.Index(s, "((\""); idx >= 0 {
		rest := s[idx+3:]
		if end := strings.Index(rest, "\""); end >= 0 {
			process = rest[:end]
		}
	}
	if idx := strings.Index(s, "pid="); idx >= 0 {
		rest := s[idx+4:]
		end := strings.IndexAny(rest, ",)")
		if end < 0 {
			end = len(rest)
		}
		pid, _ = strconv.Atoi(rest[:end])
	}
	return
}

// parseProcNetTCP parses /proc/net/tcp or /proc/net/tcp6 content.
// Only returns entries in LISTEN state (0A).
func parseProcNetTCP(content, protocol string) []sandbox.ListeningPort {
	var ports []sandbox.ListeningPort
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if fields[0] == "sl" {
			continue
		}
		if fields[3] != "0A" {
			continue
		}

		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		portNum64, err := strconv.ParseInt(parts[1], 16, 32)
		if err != nil || portNum64 == 0 {
			continue
		}

		addr := hexToIP(parts[0], protocol)
		ports = append(ports, sandbox.ListeningPort{
			Port:     int(portNum64),
			Protocol: protocol,
			Address:  addr,
		})
	}
	return ports
}

// hexToIP converts a hex-encoded IP from /proc/net/tcp to a readable string.
// IPv4: "0100007F" → "127.0.0.1" (little-endian)
// IPv6: 32-hex-char → "::" or "::1" etc.
func hexToIP(hex, protocol string) string {
	if protocol == "tcp6" {
		if len(hex) == 32 {
			allZero := true
			for _, c := range hex {
				if c != '0' {
					allZero = false
					break
				}
			}
			if allZero {
				return "::"
			}
			// Check for ::1
			if hex == "00000000000000000000000001000000" {
				return "::1"
			}
		}
		return "::"
	}

	// IPv4: little-endian hex
	if len(hex) != 8 {
		return "0.0.0.0"
	}
	var octets [4]uint64
	for i := 0; i < 4; i++ {
		octets[i], _ = strconv.ParseUint(hex[6-2*i:8-2*i], 16, 8)
	}
	return strconv.FormatUint(octets[0], 10) + "." +
		strconv.FormatUint(octets[1], 10) + "." +
		strconv.FormatUint(octets[2], 10) + "." +
		strconv.FormatUint(octets[3], 10)
}
