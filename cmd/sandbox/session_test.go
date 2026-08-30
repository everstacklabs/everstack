package sandbox_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/sandbox"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
)

func TestListUsesActiveLoginWithoutConnectionFlags(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"instances":[],"total":0}`))
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

	cmd := sandbox.New()
	cmd.SetArgs([]string{"list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox list after login: %v", err)
	}
	if !requestReceived {
		t.Fatal("sandbox list did not use the API endpoint saved by login")
	}
}

func TestListUsesSavedDeviceLoginWithoutConnectionFlags(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"instances":[],"total":0}`))
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

	cmd := sandbox.New()
	cmd.SetArgs([]string{"list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox list after device login: %v", err)
	}
}

func TestListWithoutLoginFailsBeforeCallingTheAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
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

	cmd := sandbox.New()
	cmd.SetArgs([]string{"list"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "evs login") {
		t.Fatalf("sandbox list error = %v, want login guidance", err)
	}
	if requestReceived {
		t.Fatal("sandbox list called the API without a credential")
	}
}

func TestExecUsesVerifiedHostScopeFromActiveLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if got, ok := body["tenant_id"]; ok {
			t.Errorf("tenant_id = %#v, want no client-supplied tenant override", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stdout":"","stderr":"","exit_code":0}`))
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
	if err := credentials.Save("default", credentials.Token{
		AccessToken: "saved-device-token",
		OrgID:       "org-from-login",
	}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := sandbox.New()
	cmd.SetArgs([]string{"exec", "sandbox-1", "true"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox exec after login: %v", err)
	}
}

func TestExecWithAPIKeyStillRequiresExplicitTenant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_KEY", "saved-api-key")
	t.Setenv("EVS_TENANT_ID", "")

	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv("EVS_API_URL", server.URL)

	cmd := sandbox.New()
	cmd.SetArgs([]string{"exec", "sandbox-1", "true"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--tenant-id") {
		t.Fatalf("sandbox exec error = %v, want explicit tenant guidance", err)
	}
	if requestReceived {
		t.Fatal("sandbox exec called the API without an instance tenant for API-key authentication")
	}
}

func TestSSHInfoUsesSavedDeviceLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	const accessToken = "saved-device-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
			http.Error(w, "missing device login", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"host":"sandbox.example","port":22,"username":"root"}`))
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
	if err := credentials.Save("default", credentials.Token{
		AccessToken: accessToken,
		OrgID:       "org-from-login",
	}); err != nil {
		t.Fatalf("save CLI credentials: %v", err)
	}

	cmd := sandbox.New()
	cmd.SetArgs([]string{"ssh-info", "sandbox-1"})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox ssh-info after login: %v", err)
	}
}
