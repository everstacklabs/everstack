package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	clicfg "github.com/everstacklabs/everstack/internal/cli/config"
	"github.com/everstacklabs/everstack/internal/cli/credentials"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
)

type refreshingAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	t *testing.T
}

func (h refreshingAgentsHandler) ListAgents(
	_ context.Context,
	req *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	h.t.Helper()
	if got := req.Header().Get("Authorization"); got != "Bearer refreshed-access-token" {
		h.t.Errorf("Authorization = %q, want refreshed bearer token", got)
	}
	return connect.NewResponse(&agentsv1.ListAgentsResponse{}), nil
}

func TestRequireFactoryUsesRefreshableOAuthSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EVS_API_URL", "")
	t.Setenv("EVS_API_KEY", "")
	t.Setenv("EVS_TENANT_ID", "")

	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/oauth/authorize",
			"token_endpoint":                   server.URL + "/oauth/token",
			"grant_types_supported":            []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "rotated-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    900,
		})
	})
	agentsPath, agentsHandler := agentsconnect.NewAgentsServiceHandler(refreshingAgentsHandler{t: t})
	mux.Handle(agentsPath, agentsHandler)
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	if err := clicfg.Save(&clicfg.Config{
		ActiveContext: "test",
		Contexts: map[string]clicfg.Context{
			"test": {APIURL: server.URL},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := credentials.Save("test", credentials.Token{
		AccessToken:  "expired-access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute),
		OrgID:        "org-1",
	}); err != nil {
		t.Fatalf("save credentials: %v", err)
	}

	factory, err := requireFactory(connectionOptions{})
	if err != nil {
		t.Fatalf("requireFactory() error = %v", err)
	}
	if _, err := factory.Agents().ListAgents(
		context.Background(),
		connect.NewRequest(&agentsv1.ListAgentsRequest{}),
	); err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}

	got, err := credentials.Load("test")
	if err != nil {
		t.Fatalf("load refreshed credentials: %v", err)
	}
	if got.AccessToken != "refreshed-access-token" || got.RefreshToken != "rotated-refresh-token" {
		t.Fatalf("refreshed credentials = access:%q refresh:%q", got.AccessToken, got.RefreshToken)
	}
}

func TestConnectionOverridesAreCommandLocal(t *testing.T) {
	first := New()
	if err := first.PersistentFlags().Set("api-url", "https://first.example"); err != nil {
		t.Fatal(err)
	}
	second := New()

	firstValue, err := first.PersistentFlags().GetString("api-url")
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := second.PersistentFlags().GetString("api-url")
	if err != nil {
		t.Fatal(err)
	}
	if firstValue != "https://first.example" || secondValue != "" {
		t.Fatalf("command-local values = first:%q second:%q", firstValue, secondValue)
	}
}
