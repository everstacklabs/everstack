package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	cliv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1/cliconnect"
)

func TestBuildDeviceVerificationURLAddsUserCode(t *testing.T) {
	got, err := buildDeviceVerificationURL("https://everstack.example/device?source=cli", "ABCD-EFGH")
	if err != nil {
		t.Fatalf("buildDeviceVerificationURL() error = %v", err)
	}
	const want = "https://everstack.example/device?code=ABCD-EFGH&source=cli"
	if got != want {
		t.Fatalf("buildDeviceVerificationURL() = %q, want %q", got, want)
	}
}

func TestDeviceAuthSlowDownIncreasesPollInterval(t *testing.T) {
	t.Parallel()

	if got := nextDevicePollInterval(5*time.Second, "slow_down"); got != 10*time.Second {
		t.Fatalf("nextDevicePollInterval(slow_down) = %s, want 10s", got)
	}
	if got := nextDevicePollInterval(10*time.Second, "authorization_pending"); got != 10*time.Second {
		t.Fatalf("nextDevicePollInterval(pending) = %s, want unchanged", got)
	}
}

func TestResolveDeviceVerificationURIUsesAPIURLForRelativePath(t *testing.T) {
	got, err := resolveDeviceVerificationURI(
		"https://instance.example.com/",
		"/device",
	)
	if err != nil {
		t.Fatalf("resolveDeviceVerificationURI() error = %v", err)
	}
	if got != "https://instance.example.com/device" {
		t.Fatalf("resolveDeviceVerificationURI() = %q", got)
	}
}

func TestDeviceAuthPendingStatusCompatibility(t *testing.T) {
	for _, status := range []string{"authorization_pending", "pending", "slow_down"} {
		if !isDeviceAuthPendingStatus(status) {
			t.Errorf("isDeviceAuthPendingStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"authorized", "denied", "expired", ""} {
		if isDeviceAuthPendingStatus(status) {
			t.Errorf("isDeviceAuthPendingStatus(%q) = true, want false", status)
		}
	}
}

func TestSaveLoginContextPersistsEndpointAndOrganization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &clicfg.Config{
		ActiveContext: "default",
		Contexts: map[string]clicfg.Context{
			"default": {Workspace: "workspace-a"},
		},
	}

	if err := saveLoginContext(cfg, "https://instance.example.com/", "org-a"); err != nil {
		t.Fatalf("saveLoginContext() error = %v", err)
	}

	active := cfg.ActiveCtx()
	if active.APIURL != "https://instance.example.com" {
		t.Fatalf("APIURL = %q, want instance endpoint", active.APIURL)
	}
	if active.OrgSlug != "org-a" {
		t.Fatalf("OrgSlug = %q, want org-a", active.OrgSlug)
	}
	if active.Workspace != "workspace-a" {
		t.Fatalf("Workspace = %q, want existing value preserved", active.Workspace)
	}

	path, err := clicfg.Path()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("saved config: %v", err)
	}
}

func TestBrowserAuthAvailableCanBeDisabledForHeadlessLogin(t *testing.T) {
	t.Setenv("EVS_NO_BROWSER", "true")
	if browserAuthAvailable() {
		t.Fatal("browserAuthAvailable() = true with EVS_NO_BROWSER=true")
	}
}

type loginCLIHandler struct {
	cliconnect.UnimplementedCLIServiceHandler
	t      *testing.T
	apiKey string
}

func (h loginCLIHandler) Whoami(
	_ context.Context,
	req *connect.Request[cliv1.WhoamiRequest],
) (*connect.Response[cliv1.WhoamiResponse], error) {
	h.t.Helper()
	if got := req.Header().Get("x-evs-api-key"); got != h.apiKey {
		h.t.Errorf("x-evs-api-key header = %q, want environment API key", got)
	}
	return connect.NewResponse(&cliv1.WhoamiResponse{
		UserId:  "user-1",
		Email:   "owner@example.com",
		OrgId:   "org-1",
		OrgSlug: "example-org",
	}), nil
}

func TestLoginUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const apiKey = "environment-api-key"
	t.Setenv("EVS_API_KEY", apiKey)

	mux := http.NewServeMux()
	path, handler := cliconnect.NewCLIServiceHandler(loginCLIHandler{t: t, apiKey: apiKey})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Setenv("EVS_API_URL", server.URL)

	cmd := New()
	cmd.SetArgs([]string{"login"})
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("login with EVS_API_KEY: %v", err)
	}

	token, err := credentials.Load("default")
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if token.APIKey != apiKey {
		t.Fatalf("saved API key = %q, want environment API key", token.APIKey)
	}
}

func TestFirstLoginRequiresAResourceEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")

	cmd := New()
	cmd.SetArgs([]string{"login", "--api-key", "test-api-key"})
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--api-url") {
		t.Fatalf("login error = %v, want first-login endpoint guidance", err)
	}
}
