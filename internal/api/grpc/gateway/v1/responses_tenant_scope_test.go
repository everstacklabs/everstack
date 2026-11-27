package v1

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	gatewayconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// asTenant mirrors what the API-key interceptor installs after it validates
// a tenant's key.
func asTenant(tenant string) context.Context {
	return contextkeys.WithAuthenticatedAPIKey(context.Background(), tenant, "hash-"+tenant)
}

func newSharedResponsesServer(t *testing.T) *Server {
	t.Helper()
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })
	return &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
}

func seedResponse(s *Server, ctx context.Context, id, text string) {
	s.storeResponse(ctx, id, &gw.CreateResponseResponse{
		ID: id, Object: "response", Status: "in_progress", Model: "m",
		Output: []gw.ResponseOutputItem{{
			ID: id + "_msg", Type: "message", Role: gw.RoleAssistant,
			Content: []gw.ContentPart{{Type: "text", Text: &text}},
		}},
	})
}

func TestResponsesStoreIsTenantScoped(t *testing.T) {
	s := newSharedResponsesServer(t)
	ctxA, ctxB := asTenant("tenant-a"), asTenant("tenant-b")
	seedResponse(s, ctxA, "resp_a", "tenant A secret output")

	// The error a nonexistent id produces; a foreign id must be identical.
	_, missingErr := s.GetResponse(ctxB, connect.NewRequest(&gatewaypb.GetResponseRequest{ResponseId: "resp_nope"}))
	if connect.CodeOf(missingErr) != connect.CodeNotFound {
		t.Fatalf("nonexistent id: want NotFound, got %v", missingErr)
	}

	t.Run("get", func(t *testing.T) {
		_, err := s.GetResponse(ctxB, connect.NewRequest(&gatewaypb.GetResponseRequest{ResponseId: "resp_a"}))
		if connect.CodeOf(err) != connect.CodeNotFound || err.Error() != missingErr.Error() {
			t.Fatalf("tenant B get of A's id: want %v, got %v", missingErr, err)
		}
		got, err := s.GetResponse(ctxA, connect.NewRequest(&gatewaypb.GetResponseRequest{ResponseId: "resp_a"}))
		if err != nil || got.Msg.GetResponse().GetId() != "resp_a" {
			t.Fatalf("tenant A get own id: %v %v", got, err)
		}
	})

	t.Run("list", func(t *testing.T) {
		got, err := s.ListResponses(ctxB, connect.NewRequest(&gatewaypb.ListResponsesRequest{}))
		if err != nil || len(got.Msg.GetData()) != 0 {
			t.Fatalf("tenant B list: want empty, got %v %v", got.Msg.GetData(), err)
		}
		got, err = s.ListResponses(ctxA, connect.NewRequest(&gatewaypb.ListResponsesRequest{}))
		if err != nil || len(got.Msg.GetData()) != 1 || got.Msg.GetData()[0].GetId() != "resp_a" {
			t.Fatalf("tenant A list: %v %v", got.Msg.GetData(), err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		_, missing := s.CancelResponse(ctxB, connect.NewRequest(&gatewaypb.CancelResponseRequest{ResponseId: "resp_nope"}))
		_, err := s.CancelResponse(ctxB, connect.NewRequest(&gatewaypb.CancelResponseRequest{ResponseId: "resp_a"}))
		if connect.CodeOf(err) != connect.CodeNotFound || err.Error() != missing.Error() {
			t.Fatalf("tenant B cancel of A's id: want %v, got %v", missing, err)
		}
		if r, _ := s.lookupResponse(ctxA, "resp_a"); r.Status != "in_progress" {
			t.Fatalf("tenant B cancel mutated A's response: status %q", r.Status)
		}
	})

	t.Run("delete", func(t *testing.T) {
		missing, err := s.DeleteResponse(ctxB, connect.NewRequest(&gatewaypb.DeleteResponseRequest{ResponseId: "resp_nope"}))
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.DeleteResponse(ctxB, connect.NewRequest(&gatewaypb.DeleteResponseRequest{ResponseId: "resp_a"}))
		if err != nil || got.Msg.GetDeleted() != missing.Msg.GetDeleted() || got.Msg.GetDeleted() {
			t.Fatalf("tenant B delete of A's id: want deleted=false like a missing id, got %v %v", got.Msg, err)
		}
		if _, ok := s.lookupResponse(ctxA, "resp_a"); !ok {
			t.Fatal("tenant B delete removed A's response")
		}
	})

	t.Run("chain", func(t *testing.T) {
		chain := func(ctx context.Context, prev string) error {
			return s.CreateResponse(ctx, connect.NewRequest(&gatewaypb.CreateResponseRequest{
				Model: "m", PreviousResponseId: prev,
			}), nil)
		}
		missing := chain(ctxB, "resp_nope")
		if connect.CodeOf(missing) != connect.CodeNotFound {
			t.Fatalf("chain from nonexistent id: want NotFound, got %v", missing)
		}
		err := chain(ctxB, "resp_a")
		if connect.CodeOf(err) != connect.CodeNotFound || err.Error() != missing.Error() {
			t.Fatalf("tenant B chain from A's id: want %v, got %v", missing, err)
		}
		// Tenant A passes the ownership gate and proceeds to provider
		// resolution (none are configured in this test).
		if err := chain(ctxA, "resp_a"); connect.CodeOf(err) == connect.CodeNotFound {
			t.Fatalf("tenant A chain from own id rejected: %v", err)
		}
		// The agentic loop hydrates from the caller's bucket only.
		if _, ok := s.lookupResponse(ctxB, "resp_a"); ok {
			t.Fatal("tenant B can hydrate A's response for chaining")
		}
	})

	t.Run("delete own", func(t *testing.T) {
		got, err := s.DeleteResponse(ctxA, connect.NewRequest(&gatewaypb.DeleteResponseRequest{ResponseId: "resp_a"}))
		if err != nil || !got.Msg.GetDeleted() {
			t.Fatalf("tenant A delete own: %v %v", got, err)
		}
	})
}

func TestResponsesSharedModeRequiresVerifiedTenant(t *testing.T) {
	s := newSharedResponsesServer(t)
	seedResponse(s, asTenant("tenant-a"), "resp_a", "x")

	// A bare tenant id with no validated key or user is not a principal.
	unverified := contextkeys.WithTenantID(context.Background(), "tenant-a")
	for name, ctx := range map[string]context.Context{"no tenant": context.Background(), "unverified tenant": unverified} {
		if _, ok := s.lookupResponse(ctx, "resp_a"); ok {
			t.Fatalf("%s: read tenant A's response", name)
		}
		seedResponse(s, ctx, "resp_anon", "y")
		if len(globalResponseStore.responses[""]) != 0 {
			t.Fatalf("%s: stored into the shared empty-owner bucket", name)
		}
		got, err := s.ListResponses(ctx, connect.NewRequest(&gatewaypb.ListResponsesRequest{}))
		if err != nil || len(got.Msg.GetData()) != 0 {
			t.Fatalf("%s: list returned %v %v", name, got.Msg.GetData(), err)
		}
	}
}

func TestResponsesSingleTenantModeUnchanged(t *testing.T) {
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })
	s := &Server{ctx: context.Background()}

	seedResponse(s, context.Background(), "resp_1", "x")
	if _, ok := s.lookupResponse(context.Background(), "resp_1"); !ok {
		t.Fatal("self-hosted caller cannot read its own response")
	}
	// Chaining on the native path is not gated outside the shared gateway.
	if err := s.authorizePreviousResponse(context.Background(), "resp_unknown"); err != nil {
		t.Fatalf("single-tenant chaining gate: %v", err)
	}
}

// TestResponsesHTTPRoutesAreTenantScoped drives the REST surface the way
// production wires it: grpc-gateway (/v1/responses...) dialing the Connect
// handler over gRPC, with an interceptor standing in for the API-key
// interceptor (bearer token -> authenticated tenant).
func TestResponsesHTTPRoutesAreTenantScoped(t *testing.T) {
	s := newSharedResponsesServer(t)
	seedResponse(s, asTenant("tenant-a"), "resp_a", "tenant A secret output")

	authn := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if tenant := strings.TrimPrefix(req.Header().Get("Authorization"), "Bearer "); tenant != "" {
				ctx = contextkeys.WithAuthenticatedAPIKey(ctx, tenant, "hash-"+tenant)
			}
			return next(ctx, req)
		}
	})
	path, handler := gatewayconnect.NewGatewayServiceHandler(s, connect.WithInterceptors(authn))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)

	gwMux := runtime.NewServeMux()
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only self-signed server
	if err := gatewaypb.RegisterGatewayServiceHandlerFromEndpoint(context.Background(), gwMux,
		strings.TrimPrefix(backend.URL, "https://"), []grpc.DialOption{grpc.WithTransportCredentials(creds)}); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(gwMux)
	t.Cleanup(front.Close)

	do := func(method, path, tenant string) (int, string) {
		req, _ := http.NewRequest(method, front.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tenant)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// Tenant A sees its own response.
	if code, body := do(http.MethodGet, "/v1/responses/resp_a", "tenant-a"); code != http.StatusOK || !strings.Contains(body, "resp_a") {
		t.Fatalf("tenant A GET own: %d %s", code, body)
	}

	missCode, missBody := do(http.MethodGet, "/v1/responses/resp_nope", "tenant-b")
	if missCode != http.StatusNotFound {
		t.Fatalf("GET nonexistent: want 404, got %d %s", missCode, missBody)
	}
	for _, tc := range []struct{ method, path, missPath string }{
		{http.MethodGet, "/v1/responses/resp_a", "/v1/responses/resp_nope"},
		{http.MethodPost, "/v1/responses/resp_a/cancel", "/v1/responses/resp_nope/cancel"},
		{http.MethodDelete, "/v1/responses/resp_a", "/v1/responses/resp_nope"},
	} {
		wantCode, wantBody := do(tc.method, tc.missPath, "tenant-b")
		code, body := do(tc.method, tc.path, "tenant-b")
		if code != wantCode || strings.Contains(body, "secret") ||
			strings.ReplaceAll(body, "resp_a", "ID") != strings.ReplaceAll(wantBody, "resp_nope", "ID") {
			t.Fatalf("tenant B %s %s: got %d %s, want same as missing id: %d %s", tc.method, tc.path, code, body, wantCode, wantBody)
		}
	}

	code, body := do(http.MethodGet, "/v1/responses", "tenant-b")
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal([]byte(body), &list)
	if code != http.StatusOK || len(list.Data) != 0 || strings.Contains(body, "resp_a") {
		t.Fatalf("tenant B list: %d %s", code, body)
	}

	// A's response survived B's cancel and delete attempts.
	if code, body := do(http.MethodGet, "/v1/responses/resp_a", "tenant-a"); code != http.StatusOK || !strings.Contains(body, "in_progress") {
		t.Fatalf("tenant A response after B's attempts: %d %s", code, body)
	}
}
