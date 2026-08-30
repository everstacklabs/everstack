package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/client"
	cliv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1/cliconnect"
)

type bearerWhoamiHandler struct {
	cliconnect.UnimplementedCLIServiceHandler
	t *testing.T
}

func (h bearerWhoamiHandler) Whoami(
	_ context.Context,
	req *connect.Request[cliv1.WhoamiRequest],
) (*connect.Response[cliv1.WhoamiResponse], error) {
	h.t.Helper()
	if got := req.Header().Get("Authorization"); got != "Bearer new-access-jwt" {
		return nil, connect.NewError(
			connect.CodeUnauthenticated,
			&unexpectedTokenError{got: got},
		)
	}
	return connect.NewResponse(&cliv1.WhoamiResponse{
		UserId:  "user-1",
		Email:   "dev@example.com",
		OrgId:   "org-1",
		OrgSlug: "everstack-dev",
	}), nil
}

func TestExpiredCredentialRefreshesBeforeAuthenticatedRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh: %v", err)
			http.Error(w, "invalid refresh form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("refresh_token") != "old-refresh-token" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
			http.Error(w, "unexpected refresh token", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-jwt",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    900,
			"scope":         "cli:full",
		})
	})
	cliPath, cliHandler := cliconnect.NewCLIServiceHandler(bearerWhoamiHandler{t: t})
	mux.Handle(cliPath, cliHandler)
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	if err := Save("default", Token{
		AccessToken:  "expired-access-jwt",
		RefreshToken: "old-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	source := NewSource("default", server.URL, server.Client())
	factory := client.New(client.Options{
		APIURL:            server.URL,
		AccessTokenSource: source,
	})

	if _, err := factory.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami() error = %v", err)
	}
	saved, err := Load("default")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.AccessToken != "new-access-jwt" || saved.RefreshToken != "new-refresh-token" {
		t.Fatalf("saved tokens = access:%q refresh:%q", saved.AccessToken, saved.RefreshToken)
	}
	if !saved.ExpiresAt.After(time.Now().Add(14 * time.Minute)) {
		t.Fatalf("saved expiry = %s, want refreshed expiry", saved.ExpiresAt)
	}
}

func TestConcurrentSourcesRefreshOnceAfterLockAndReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var refreshCalls atomic.Int32
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
		refreshCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-jwt",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    900,
			"scope":         "cli:full",
		})
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := Save("default", Token{
		AccessToken:  "expired-access-jwt",
		RefreshToken: "old-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	sources := []*Source{
		NewSource("default", server.URL, server.Client()),
		NewSource("default", server.URL, server.Client()),
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(sources))
	for _, source := range sources {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := source.AccessToken(context.Background())
			if err == nil && token != "new-access-jwt" {
				err = &unexpectedTokenError{got: token}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AccessToken() error = %v", err)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

type unexpectedTokenError struct {
	got string
}

func (e *unexpectedTokenError) Error() string {
	return "unexpected token: " + e.got
}
