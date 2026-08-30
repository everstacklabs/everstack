package memory_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/cmd/memory"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
)

func TestCollectionsListUsesActiveLoginWithoutConnectionFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	const apiKey = "saved-api-key"
	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		if got := r.Header.Get("x-evs-api-key"); got != apiKey {
			t.Errorf("x-evs-api-key header = %q, want saved API key", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"collections":[]}`))
	}))
	t.Cleanup(server.Close)

	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {APIURL: server.URL},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save CLI config: %v", err)
	}
	if err := credentials.Save("default", credentials.Token{APIKey: apiKey}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := memory.New()
	cmd.SetArgs([]string{"collections", "list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("memory collections list after login: %v", err)
	}
	if !requestReceived {
		t.Fatal("memory collections list did not use the API endpoint saved by login")
	}
}

func TestCollectionsListUsesSavedDeviceLoginWithoutConnectionFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	const accessToken = "saved-device-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
			t.Errorf("Authorization header = %q, want saved device token", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"collections":[]}`))
	}))
	t.Cleanup(server.Close)

	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {APIURL: server.URL},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save CLI config: %v", err)
	}
	if err := credentials.Save("default", credentials.Token{AccessToken: accessToken}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := memory.New()
	cmd.SetArgs([]string{"collections", "list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("memory collections list after device login: %v", err)
	}
}

func TestCollectionsListUsesTenantFromEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "tenant-from-environment")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("tenant_id"); got != "tenant-from-environment" {
			t.Errorf("tenant_id query = %q, want environment tenant", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"collections":[]}`))
	}))
	t.Cleanup(server.Close)

	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {APIURL: server.URL},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save CLI config: %v", err)
	}
	if err := credentials.Save("default", credentials.Token{APIKey: "saved-api-key"}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := memory.New()
	cmd.SetArgs([]string{"collections", "list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("memory collections list: %v", err)
	}
}
