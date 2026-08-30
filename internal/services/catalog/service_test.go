package catalog

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLoadCatalogUsesEmbeddedBeforeRemote(t *testing.T) {
	t.Chdir(t.TempDir())

	var remoteRequests atomic.Int32
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		remoteRequests.Add(1)
		return nil, errors.New("catalog distribution unavailable")
	})
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	embeddedModels := []byte(`
providers:
  example:
    name: Example
    models:
      - name: example-model
        display_name: Example Model
`)
	embeddedProviders := []byte(`
providers:
  example:
    name: example
    display_name: Example
`)

	service := NewService(nil, embeddedModels, embeddedProviders)
	if err := service.LoadCatalog(); err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}

	if got := service.GetSource(); got != "embedded" {
		t.Fatalf("GetSource() = %q, want embedded", got)
	}
	if got := remoteRequests.Load(); got != 0 {
		t.Fatalf("remote catalog requests during startup = %d, want 0", got)
	}
	if err := service.ValidateModel("example", "example-model"); err != nil {
		t.Fatalf("embedded catalog was not usable: %v", err)
	}
}
