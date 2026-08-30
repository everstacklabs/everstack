package validator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMergesCatalogDefaultsBeforeEnvironmentOverrides(t *testing.T) {
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "configured-public-key")
	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  config:\n    port: 8089\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Server.Catalog.Source != "remote" {
		t.Fatalf("catalog source = %q, want remote", config.Server.Catalog.Source)
	}
	if config.Server.Catalog.RemoteURL != "https://catalog.everstack.ai/v1" {
		t.Fatalf("catalog URL = %q", config.Server.Catalog.RemoteURL)
	}
	if config.Server.Catalog.PublicKey != "configured-public-key" {
		t.Fatalf("catalog public key = %q", config.Server.Catalog.PublicKey)
	}
	if config.Server.Catalog.Channel != "stable" || !config.Server.Catalog.EnableAutoSync {
		t.Fatalf("catalog defaults = %#v", config.Server.Catalog)
	}
}

func TestLoadConfigPreservesExplicitCatalogBooleans(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	data := []byte("server:\n  catalog:\n    source: local\n    enable_auto_sync: false\n    require_signature: true\n")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Server.Catalog.EnableAutoSync || !config.Server.Catalog.RequireSignature {
		t.Fatalf("catalog booleans = %#v", config.Server.Catalog)
	}
}

func TestCatalogSyncConfigFromEnvironmentUsesSecureDefaultsAndOverrides(t *testing.T) {
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "shared-gateway-public-key")
	t.Setenv("EVS_CATALOG_SYNC_INTERVAL", "7m")

	catalog, err := CatalogSyncConfigFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Source != "remote" || catalog.RemoteURL != "https://catalog.everstack.ai/v1" {
		t.Fatalf("catalog distribution = %#v", catalog)
	}
	if !catalog.EnableAutoSync || !catalog.RequireSignature {
		t.Fatalf("catalog security defaults = %#v", catalog)
	}
	if catalog.PublicKey != "shared-gateway-public-key" || catalog.SyncInterval != "7m" {
		t.Fatalf("catalog environment overrides = %#v", catalog)
	}
}
