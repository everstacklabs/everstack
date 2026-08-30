package catalog_sync

import (
	"testing"
	"time"
)

func TestDefaultConfigUsesRegisteredRemoteURLVariable(t *testing.T) {
	t.Setenv("EVS_CATALOG_REMOTE_URL", "https://catalog.example.test/stable")
	t.Setenv("EVS_CATALOG_URL", "https://legacy.example.test")

	if got := DefaultConfig().RemoteURL; got != "https://catalog.example.test/stable" {
		t.Fatalf("DefaultConfig().RemoteURL = %q", got)
	}
}

func TestDefaultConfigUsesRegisteredSyncVariables(t *testing.T) {
	t.Setenv("EVS_CATALOG_ENABLE_AUTO_SYNC", "false")
	t.Setenv("EVS_CATALOG_SYNC_ENABLED", "true")
	t.Setenv("EVS_CATALOG_SYNC_INTERVAL", "5m")

	config := DefaultConfig()
	if config.EnableAutoSync {
		t.Fatal("DefaultConfig().EnableAutoSync = true")
	}
	if config.SyncInterval != 5*time.Minute {
		t.Fatalf("DefaultConfig().SyncInterval = %s", config.SyncInterval)
	}
}

func TestDefaultConfigKeepsLegacyRemoteURLAlias(t *testing.T) {
	t.Setenv("EVS_CATALOG_REMOTE_URL", "")
	t.Setenv("EVS_CATALOG_URL", "https://legacy.example.test")

	if got := DefaultConfig().RemoteURL; got != "https://legacy.example.test" {
		t.Fatalf("DefaultConfig().RemoteURL = %q", got)
	}
}

func TestDefaultConfigUsesSignedEverstackDistribution(t *testing.T) {
	t.Setenv("EVS_CATALOG_REMOTE_URL", "")
	t.Setenv("EVS_CATALOG_URL", "")
	t.Setenv("EVS_CATALOG_CHANNEL", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_REQUIRE_SIGNATURE", "")

	config := DefaultConfig()
	if config.RemoteURL != "https://catalog.everstack.ai/v1" {
		t.Fatalf("DefaultConfig().RemoteURL = %q", config.RemoteURL)
	}
	if config.Channel != "stable" || !config.RequireSignature {
		t.Fatalf("DefaultConfig() trust = channel %q, require signature %t", config.Channel, config.RequireSignature)
	}
}
