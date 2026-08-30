package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
	cliv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/cli/v1/cliconnect"
)

type whoamiCLIHandler struct {
	cliconnect.UnimplementedCLIServiceHandler
	t      *testing.T
	apiKey string
}

func (h whoamiCLIHandler) Whoami(
	_ context.Context,
	req *connect.Request[cliv1.WhoamiRequest],
) (*connect.Response[cliv1.WhoamiResponse], error) {
	h.t.Helper()
	if got := req.Header().Get("x-evs-api-key"); got != h.apiKey {
		h.t.Errorf("x-evs-api-key header = %q, want %q", got, h.apiKey)
	}
	return connect.NewResponse(&cliv1.WhoamiResponse{
		UserId:  "user-1",
		Email:   "owner@example.com",
		OrgId:   "org-1",
		OrgSlug: "example-org",
	}), nil
}

func TestWhoamiWithAPIKeyUsesCLIIdentityEndpoint(t *testing.T) {
	t.Parallel()

	const apiKey = "test-api-key"
	mux := http.NewServeMux()
	cliPath, cliHandler := cliconnect.NewCLIServiceHandler(whoamiCLIHandler{
		t:      t,
		apiKey: apiKey,
	})
	mux.Handle(cliPath, cliHandler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	got, err := New(Options{APIURL: server.URL, APIKey: apiKey}).Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami() error = %v", err)
	}

	if got.UserID != "user-1" {
		t.Errorf("Whoami().UserID = %q, want %q", got.UserID, "user-1")
	}
	if got.Email != "owner@example.com" {
		t.Errorf("Whoami().Email = %q, want %q", got.Email, "owner@example.com")
	}
	if got.OrgID != "org-1" {
		t.Errorf("Whoami().OrgID = %q, want %q", got.OrgID, "org-1")
	}
	if got.OrgSlug != "example-org" {
		t.Errorf("Whoami().OrgSlug = %q, want %q", got.OrgSlug, "example-org")
	}
}

type fixedAccessTokenSource struct {
	token string
}

func (s fixedAccessTokenSource) AccessToken(context.Context) (string, error) {
	return s.token, nil
}

func TestHTTPClientInjectsRefreshingBearerAndTenantHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
			t.Errorf("Authorization = %q, want refreshed bearer token", got)
		}
		if got := r.Header.Get("x-evs-org-id"); got != "org-1" {
			t.Errorf("x-evs-org-id = %q, want org-1", got)
		}
		if got := r.Header.Get("x-evs-tenant-id"); got != "tenant-1" {
			t.Errorf("x-evs-tenant-id = %q, want tenant-1", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	factory := New(Options{
		AccessToken:       "stale-token",
		AccessTokenSource: fixedAccessTokenSource{token: "refreshed-token"},
		OrgID:             "org-1",
		TenantID:          "tenant-1",
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := factory.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("HTTPClient().Do() error = %v", err)
	}
	resp.Body.Close()
}

type streamingAgentsHandler struct {
	agentsconnect.UnimplementedAgentsServiceHandler
	t *testing.T
}

func (h streamingAgentsHandler) RunTurnStream(
	_ context.Context,
	req *connect.Request[agentsv1.RunTurnStreamRequest],
	stream *connect.ServerStream[agentsv1.AgentEvent],
) error {
	h.t.Helper()
	if got := req.Header().Get("Authorization"); got != "Bearer stream-token" {
		h.t.Errorf("Authorization = %q, want configured stream bearer token", got)
	}
	if got := req.Header().Get("x-evs-org-id"); got != "org-stream" {
		h.t.Errorf("x-evs-org-id = %q, want org-stream", got)
	}
	if got := req.Header().Get("x-evs-tenant-id"); got != "tenant-stream" {
		h.t.Errorf("x-evs-tenant-id = %q, want tenant-stream", got)
	}
	return stream.Send(&agentsv1.AgentEvent{})
}

func TestAgentsStreamingInjectsBearerAndTenantHeaders(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	path, handler := agentsconnect.NewAgentsServiceHandler(streamingAgentsHandler{t: t})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	factory := New(Options{
		APIURL:      server.URL,
		AccessToken: "stream-token",
		OrgID:       "org-stream",
		TenantID:    "tenant-stream",
	})
	stream, err := factory.AgentsStreaming().RunTurnStream(
		context.Background(),
		connect.NewRequest(&agentsv1.RunTurnStreamRequest{}),
	)
	if err != nil {
		t.Fatalf("RunTurnStream() error = %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("RunTurnStream() did not receive an event: %v", stream.Err())
	}
}
