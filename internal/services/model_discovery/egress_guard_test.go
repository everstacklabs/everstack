package model_discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

func TestOllamaDiscoverySSRFGuardFollowsGatewayMode(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()
	previous := enterprise.ManagedGateway()
	t.Cleanup(func() { enterprise.SetManagedGateway(previous) })

	enterprise.SetManagedGateway(true)
	if _, err := NewService().ListOllamaModels(context.Background(), srv.URL, "", ""); err == nil {
		t.Fatal("managed discovery reached a loopback URL")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("managed discovery sent %d requests to loopback", n)
	}

	enterprise.SetManagedGateway(false)
	if _, err := NewService().ListOllamaModels(context.Background(), srv.URL, "", ""); err != nil {
		t.Fatalf("self-hosted discovery could not reach local Ollama: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("self-hosted discovery sent %d requests, want 1", n)
	}
}
