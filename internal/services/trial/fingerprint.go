package trial

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// installTimestampFile stores the first-run timestamp for fingerprint stability
	installTimestampFile = ".everstack_install_ts"
)

// GenerateFingerprint creates a stable machine fingerprint for trial tracking.
// The fingerprint is a hash of:
// - Hostname
// - First available MAC address
// - Installation timestamp (persisted to survive restarts)
//
// This creates an identifier that:
// - Survives container/process restarts
// - Changes on complete reinstalls (desired for trial reset protection)
// - Is unique per machine/instance
func GenerateFingerprint() string {
	components := []string{}

	// Component 1: Hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	components = append(components, hostname)

	// Component 2: First available MAC address
	macAddr := getFirstMACAddress()
	components = append(components, macAddr)

	// Component 3: Installation timestamp
	installTS := getOrCreateInstallTimestamp()
	components = append(components, installTS)

	// Hash all components together
	combined := strings.Join(components, "|")
	hash := sha256.Sum256([]byte(combined))
	fingerprint := hex.EncodeToString(hash[:])

	// Return full 64-character SHA256 hash (required by license service validation)
	logger.Debugf("trial: generated fingerprint from hostname=%s, mac=%s, install_ts=%s",
		hostname, maskMAC(macAddr), installTS[:10]+"...")

	return fingerprint
}

// getFirstMACAddress returns the first non-loopback MAC address found
func getFirstMACAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "no-mac-available"
	}

	for _, iface := range interfaces {
		// Skip loopback and interfaces without MAC
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		// Skip virtual/docker interfaces
		if strings.HasPrefix(iface.Name, "docker") ||
			strings.HasPrefix(iface.Name, "veth") ||
			strings.HasPrefix(iface.Name, "br-") {
			continue
		}
		return iface.HardwareAddr.String()
	}

	// Fallback: return any MAC we can find
	for _, iface := range interfaces {
		if len(iface.HardwareAddr) > 0 {
			return iface.HardwareAddr.String()
		}
	}

	return "no-mac-available"
}

// getOrCreateInstallTimestamp returns a persistent installation timestamp.
// If no timestamp file exists, creates one with the current time.
func getOrCreateInstallTimestamp() string {
	// Try multiple locations for the timestamp file
	locations := []string{
		filepath.Join(getDataDir(), installTimestampFile),
		filepath.Join(os.TempDir(), installTimestampFile),
	}

	// Check existing files first
	for _, path := range locations {
		if data, err := os.ReadFile(path); err == nil {
			ts := strings.TrimSpace(string(data))
			if ts != "" {
				return ts
			}
		}
	}

	// Create new timestamp
	ts := fmt.Sprintf("%d", time.Now().UnixNano())

	// Try to persist it
	for _, path := range locations {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			continue
		}
		if err := os.WriteFile(path, []byte(ts), 0644); err != nil {
			continue
		}
		logger.Debugf("trial: created install timestamp at %s", path)
		break
	}

	return ts
}

// getDataDir returns the data directory for persistent storage
func getDataDir() string {
	// Check environment variable first
	if dir := os.Getenv("EVS_DATA_DIR"); dir != "" {
		return dir
	}

	// Default locations
	if dir := os.Getenv("HOME"); dir != "" {
		return filepath.Join(dir, ".everstack")
	}

	// Fallback to current directory
	return "."
}

// maskMAC masks a MAC address for logging (privacy)
func maskMAC(mac string) string {
	if len(mac) < 8 {
		return "***"
	}
	return mac[:8] + ":XX:XX:XX"
}
