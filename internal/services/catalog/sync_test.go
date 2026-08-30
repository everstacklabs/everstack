package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

func TestSyncFromRemoteRejectsPartialManifestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.yaml":
			_, _ = w.Write([]byte(`
version: 9.0.0
schema_version: "2.0"
providers:
  - name: example
    files:
      - providers/example/provider.yaml
    models:
      - providers/example/models/available.yaml
      - providers/example/models/unavailable.yaml
`))
		case "/version.txt":
			_, _ = w.Write([]byte("9.0.0\n"))
		case "/providers/example/provider.yaml":
			_, _ = w.Write([]byte(`
name: example
display_name: Example
base_url: https://example.invalid/v1
`))
		case "/providers/example/models/available.yaml":
			_, _ = w.Write([]byte(`
name: available-model
display_name: Available Model
status: stable
`))
		case "/providers/example/models/unavailable.yaml":
			http.Error(w, "catalog edge degraded", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	service := NewRemoteSyncServiceWithTrust(server.URL, catalogdistribution.TrustConfig{RequireSignature: false})
	if _, _, _, err := service.SyncFromRemote(); err == nil {
		t.Fatal("SyncFromRemote() error = nil, want partial release rejection")
	}
}

func TestSyncFromRemoteKeepsLegacyFallbackWhenManifestIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.yaml":
			http.NotFound(response, request)
		case "/models.yaml":
			_, _ = response.Write([]byte("providers:\n  example:\n    models:\n      - name: legacy-model\n"))
		case "/providers.yaml":
			_, _ = response.Write([]byte("providers:\n  example:\n    display_name: Legacy\n"))
		case "/version.txt":
			_, _ = response.Write([]byte("8.0.0\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	service := NewRemoteSyncServiceWithTrust(server.URL, catalogdistribution.TrustConfig{RequireSignature: false})
	models, providers, version, err := service.SyncFromRemote()
	if err != nil {
		t.Fatalf("SyncFromRemote() error = %v", err)
	}
	if !strings.Contains(string(models), "legacy-model") || !strings.Contains(string(providers), "Legacy") || version != "8.0.0" {
		t.Fatalf("SyncFromRemote() = (%q, %q, %q)", models, providers, version)
	}
}

func TestSignedRemoteReleaseActivatesAfterLocalStartup(t *testing.T) {
	t.Chdir(t.TempDir())
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	remoteModels := []byte(`
providers:
  example:
    name: Example
    models:
      - name: remote-model
        display_name: Remote Model
`)
	remoteProviders := []byte(`
providers:
  example:
    name: example
    display_name: Example
`)
	bundleData, digest, err := catalogdistribution.BuildBundle("9.2.0", remoteModels, remoteProviders, nil)
	if err != nil {
		t.Fatal(err)
	}
	channelData, err := catalogdistribution.SignChannel(privateKey, catalogdistribution.Channel{
		Channel:      "stable",
		Version:      "9.2.0",
		BundlePath:   "releases/9.2.0/catalog.bundle.json",
		BundleSHA256: digest,
		BundleSize:   int64(len(bundleData)),
		PublishedAt:  time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/channels/stable.json":
			_, _ = response.Write(channelData)
		case "/v1/releases/9.2.0/catalog.bundle.json":
			_, _ = response.Write(bundleData)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_REQUIRE_SIGNATURE", "true")

	localModels := []byte(`
providers:
  example:
    name: Example
    models:
      - name: embedded-model
        display_name: Embedded Model
`)
	localProviders := []byte(`
providers:
  example:
    name: example
    display_name: Example
`)
	catalogService := NewService(nil, localModels, localProviders)
	if err := catalogService.LoadCatalog(); err != nil {
		t.Fatal(err)
	}
	remoteService := NewRemoteSyncService(server.URL + "/v1")
	if err := remoteService.downloadAndApply(context.Background(), catalogService); err != nil {
		t.Fatalf("downloadAndApply() error = %v", err)
	}

	if catalogService.GetVersion() != "9.2.0" || catalogService.GetSource() != "remote" {
		t.Fatalf("catalog state = (%q, %q)", catalogService.GetVersion(), catalogService.GetSource())
	}
	if err := catalogService.ValidateModel("example", "remote-model"); err != nil {
		t.Fatalf("remote release was not active: %v", err)
	}
}

func TestRemoteSyncServiceUsesSignedDistributionWhenKeyConfigured(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, digest, err := catalogdistribution.BuildBundle(
		"9.1.0",
		[]byte("providers:\n  example:\n    models:\n      - name: signed-model\n"),
		[]byte("providers:\n  example:\n    display_name: Example\n"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	channelData, err := catalogdistribution.SignChannel(privateKey, catalogdistribution.Channel{
		Channel:      "stable",
		Version:      "9.1.0",
		BundlePath:   "releases/9.1.0/catalog.bundle.json",
		BundleSHA256: digest,
		BundleSize:   int64(len(bundleData)),
		PublishedAt:  time.Date(2026, 8, 17, 16, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/channels/stable.json":
			_, _ = response.Write(channelData)
		case "/v1/releases/9.1.0/catalog.bundle.json":
			_, _ = response.Write(bundleData)
		default:
			http.Error(response, "legacy catalog path used", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")

	service := NewRemoteSyncService(server.URL + "/v1")
	hasUpdate, version, err := service.CheckForUpdates("9.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdates() error = %v", err)
	}
	if !hasUpdate || version != "9.1.0" {
		t.Fatalf("CheckForUpdates() = (%v, %q)", hasUpdate, version)
	}
	hasUpdate, _, err = service.CheckForUpdates("9.2.0")
	if err != nil {
		t.Fatalf("CheckForUpdates() replay check error = %v", err)
	}
	if hasUpdate {
		t.Fatal("CheckForUpdates() accepted an older signed release")
	}
	models, providers, version, err := service.SyncFromRemote()
	if err != nil {
		t.Fatalf("SyncFromRemote() error = %v", err)
	}
	if version != "9.1.0" || !strings.Contains(string(models), "signed-model") || !strings.Contains(string(providers), "Example") {
		t.Fatalf("SyncFromRemote() = (%q, %q, %q)", models, providers, version)
	}
}
