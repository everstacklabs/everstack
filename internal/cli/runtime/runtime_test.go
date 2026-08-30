package runtime_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	cliruntime "github.com/everstacklabs/everstack/internal/cli/runtime"
)

func TestResolveUsesTheActiveContextLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	cfg := &clicfg.Config{
		ActiveContext: "production",
		Contexts: map[string]clicfg.Context{
			"default":    {APIURL: "https://default.example"},
			"production": {APIURL: "https://production.example/"},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := credentials.Save("production", credentials.Token{
		AccessToken:  "production-token",
		RefreshToken: "production-refresh-token",
		OrgID:        "org-1",
	}); err != nil {
		t.Fatalf("save credentials: %v", err)
	}

	got, err := cliruntime.Resolve(cliruntime.Overrides{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.ContextName != "production" {
		t.Errorf("ContextName = %q, want production", got.ContextName)
	}
	if got.APIURL != "https://production.example" {
		t.Errorf("APIURL = %q, want active context endpoint", got.APIURL)
	}
	if got.AccessToken != "production-token" {
		t.Errorf("AccessToken = %q, want active context token", got.AccessToken)
	}
	if got.AccessTokenSource == nil {
		t.Error("AccessTokenSource = nil, want OAuth refresh source")
	}
	if got.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want active context organization", got.OrgID)
	}
	if got.TenantID != "" {
		t.Errorf("TenantID = %q, want the server-bound instance scope", got.TenantID)
	}
}

func TestResolveConnectionPrecedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "https://environment.example")
	t.Setenv("EVS_API_KEY", "environment-key")
	t.Setenv("EVS_TENANT_ID", "environment-tenant")

	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {APIURL: "https://context.example"},
		},
	}
	if err := clicfg.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := cliruntime.Resolve(cliruntime.Overrides{
		APIURL:   "https://flag.example/",
		APIKey:   "flag-key",
		TenantID: "flag-tenant",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.APIURL != "https://flag.example" {
		t.Errorf("APIURL = %q, want explicit endpoint", got.APIURL)
	}
	if got.APIKey != "flag-key" {
		t.Errorf("APIKey = %q, want explicit key", got.APIKey)
	}
	if got.TenantID != "flag-tenant" {
		t.Errorf("TenantID = %q, want explicit tenant", got.TenantID)
	}
}

func TestResolveCompleteOverridesWithoutReadingMalformedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	configPath := filepath.Join(home, ".config", "everstack", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("contexts: [not valid"), 0o600); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	got, err := cliruntime.Resolve(cliruntime.Overrides{
		APIURL: "https://ci.example",
		APIKey: "ci-key",
	})
	if err != nil {
		t.Fatalf("Resolve() with complete overrides error = %v", err)
	}
	if got.APIURL != "https://ci.example" || got.APIKey != "ci-key" {
		t.Fatalf("Resolve() = %#v, want complete explicit overrides", got)
	}
}

func TestResolveRejectsCredentialWithoutResourceEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	if err := credentials.Save("default", credentials.Token{AccessToken: "saved-token"}); err != nil {
		t.Fatalf("save credentials: %v", err)
	}

	_, err := cliruntime.Resolve(cliruntime.Overrides{})
	if err == nil || !strings.Contains(err.Error(), "api-url") {
		t.Fatalf("Resolve() error = %v, want missing endpoint guidance", err)
	}
}
