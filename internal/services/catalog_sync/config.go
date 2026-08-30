package catalog_sync

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds configuration for catalog sync service
type Config struct {
	// Source type: "remote" or "local"
	Source string

	// Remote catalog URL (used when Source is "remote")
	RemoteURL string

	// Signed release channel and trust anchors.
	Channel          string
	PublicKey        string
	PublicKeys       string
	RequireSignature bool

	// Local catalog path (used when Source is "local")
	LocalPath string

	// Enable automatic syncing (default: true, can be disabled via settings)
	EnableAutoSync bool

	// Sync interval (default: 5m)
	SyncInterval time.Duration

	// Local cache directory
	CacheDir string

	// HTTP client timeout
	Timeout time.Duration

	// Retry configuration
	MaxRetries int
	RetryDelay time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	// Determine source type from environment
	source := "remote"
	if envSource := os.Getenv("EVS_CATALOG_SOURCE"); envSource != "" {
		source = envSource
	}

	// The runtime authority is the independently hosted signed release channel.
	remoteURL := "https://catalog.everstack.ai/v1"
	if envURL := os.Getenv("EVS_CATALOG_REMOTE_URL"); envURL != "" {
		remoteURL = envURL
	} else if legacyURL := os.Getenv("EVS_CATALOG_URL"); legacyURL != "" {
		// Keep the old variable as a compatibility alias. The central gateway
		// configuration registry uses EVS_CATALOG_REMOTE_URL.
		remoteURL = legacyURL
	}

	// Allow override of local path via environment variable
	localPath := "model-catalog"
	if envPath := os.Getenv("EVS_CATALOG_LOCAL_PATH"); envPath != "" {
		localPath = envPath
	}

	// Check if catalog sync is enabled via the registered environment variable.
	// Keep the old name as a compatibility alias.
	enableAutoSync := true
	enableValue := os.Getenv("EVS_CATALOG_ENABLE_AUTO_SYNC")
	if enableValue == "" {
		enableValue = os.Getenv("EVS_CATALOG_SYNC_ENABLED")
	}
	if strings.EqualFold(enableValue, "false") {
		enableAutoSync = false
	}

	syncInterval := 5 * time.Minute
	if intervalValue := strings.TrimSpace(os.Getenv("EVS_CATALOG_SYNC_INTERVAL")); intervalValue != "" {
		if parsed, err := time.ParseDuration(intervalValue); err == nil && parsed > 0 {
			syncInterval = parsed
		}
	}

	channel := strings.TrimSpace(os.Getenv("EVS_CATALOG_CHANNEL"))
	if channel == "" {
		channel = "stable"
	}
	requireSignature := true
	if value := strings.TrimSpace(os.Getenv("EVS_CATALOG_REQUIRE_SIGNATURE")); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			requireSignature = parsed
		}
	}

	return &Config{
		Source:           source,
		RemoteURL:        remoteURL,
		Channel:          channel,
		PublicKey:        strings.TrimSpace(os.Getenv("EVS_CATALOG_PUBLIC_KEY")),
		PublicKeys:       strings.TrimSpace(os.Getenv("EVS_CATALOG_PUBLIC_KEYS")),
		RequireSignature: requireSignature,
		LocalPath:        localPath,
		EnableAutoSync:   enableAutoSync,
		SyncInterval:     syncInterval,
		CacheDir:         "data/catalog",
		Timeout:          30 * time.Second,
		MaxRetries:       3,
		RetryDelay:       5 * time.Second,
	}
}
