package catalog_sync

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

func TestFetcherRejectsPartialManifestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/version.txt":
			_, _ = w.Write([]byte("9.0.0\n"))
		case "/changelog.yaml":
			_, _ = w.Write([]byte("versions: []\n"))
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

	fetcher := NewFetcher(server.URL, time.Second, 0, 0)
	if _, err := fetcher.FetchCatalog(context.Background()); err == nil {
		t.Fatal("FetchCatalog() error = nil, want partial release rejection")
	}
}

func TestFetcherUsesSignedDistributionWhenKeyConfigured(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundleData, digest, err := catalogdistribution.BuildBundle(
		"9.1.0",
		[]byte("providers:\n  example:\n    models:\n      - name: signed-model\n"),
		[]byte("providers:\n  example:\n    display_name: Example\n"),
		[]byte("versions: []"),
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

	fetcher := NewFetcher(server.URL+"/v1", time.Second, 0, 0)
	version, err := fetcher.FetchVersion(context.Background())
	if err != nil {
		t.Fatalf("FetchVersion() error = %v", err)
	}
	if version != "9.1.0" {
		t.Fatalf("FetchVersion() = %q", version)
	}
	files, err := fetcher.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog() error = %v", err)
	}
	if files.Version != "9.1.0" || !strings.Contains(string(files.Models), "signed-model") {
		t.Fatalf("FetchCatalog() = %#v", files)
	}
}
