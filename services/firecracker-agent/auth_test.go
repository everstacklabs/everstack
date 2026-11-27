package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const testToken = "test-shared-secret-0123456789"

// startAuthTestServer brings up the real interceptor chain (auth first,
// then readiness) over an in-memory listener and returns a connected
// client. ready is left false on purpose: a call that passes auth then
// hits the readiness gate and returns Unavailable, which is the signal
// that authentication succeeded without needing a live backend.
func startAuthTestServer(t *testing.T, cfg authConfig) (fcpb.FirecrackerAgentClient, healthpb.HealthClient) {
	t.Helper()
	auth := newAuthenticator(cfg)
	ready := &readiness{}
	srv := grpc.NewServer(serverInterceptors(auth, ready)...)
	fcpb.RegisterFirecrackerAgentServer(srv, NewAgentServer(nil, "test-node"))
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus(readinessName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fcpb.NewFirecrackerAgentClient(conn), healthpb.NewHealthClient(conn)
}

func withBearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("status = %v (%v), want %v", status.Code(err), err, want)
	}
}

func TestAuth_Enforced_RejectsMissingToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	_, err := client.NodeInfo(context.Background(), &fcpb.Empty{})
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_Enforced_RejectsWrongToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	_, err := client.NodeInfo(withBearer(context.Background(), "not-the-token"), &fcpb.Empty{})
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_Enforced_RejectsMalformedHeader(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", testToken) // no "Bearer "
	_, err := client.NodeInfo(ctx, &fcpb.Empty{})
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_Enforced_AcceptsRightToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	_, err := client.NodeInfo(withBearer(context.Background(), testToken), &fcpb.Empty{})
	// Auth passed; the readiness gate (ready=false) is what answers.
	wantCode(t, err, codes.Unavailable)
}

func TestAuth_Enforced_StreamRejectsMissingToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	stream, err := client.Logs(context.Background(), &fcpb.LogsRequest{SandboxId: "x"})
	if err == nil {
		_, err = stream.Recv()
	}
	wantCode(t, err, codes.Unauthenticated)
}

func TestAuth_Enforced_StreamAcceptsRightToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken})
	stream, err := client.Logs(withBearer(context.Background(), testToken), &fcpb.LogsRequest{SandboxId: "x"})
	if err == nil {
		_, err = stream.Recv()
	}
	wantCode(t, err, codes.Unavailable)
}

func TestAuth_HealthIsExemptForKubeletProbes(t *testing.T) {
	_, healthClient := startAuthTestServer(t, authConfig{token: testToken})
	resp, err := healthClient.Check(context.Background(), &healthpb.HealthCheckRequest{Service: readinessName})
	if err != nil {
		t.Fatalf("health check without token should pass: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, want SERVING", resp.Status)
	}
}

func TestAuth_AllowUnauthenticated_AcceptsMissingToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken, allowUnauthenticated: true})
	_, err := client.NodeInfo(context.Background(), &fcpb.Empty{})
	wantCode(t, err, codes.Unavailable)
}

func TestAuth_AllowUnauthenticated_StillAcceptsRightToken(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{token: testToken, allowUnauthenticated: true})
	_, err := client.NodeInfo(withBearer(context.Background(), testToken), &fcpb.Empty{})
	wantCode(t, err, codes.Unavailable)
}

func TestAuth_LegacyNoTokenAllowFlag_AcceptsEverything(t *testing.T) {
	client, _ := startAuthTestServer(t, authConfig{allowUnauthenticated: true})
	_, err := client.NodeInfo(context.Background(), &fcpb.Empty{})
	wantCode(t, err, codes.Unavailable)
}

func TestLoadAuthConfig_FailsClosedWithoutTokenOrFlag(t *testing.T) {
	t.Setenv("FC_AUTH_TOKEN", "")
	t.Setenv("FC_AUTH_ALLOW_UNAUTHENTICATED", "")
	if _, err := loadAuthConfig(); err == nil {
		t.Fatal("expected an error when no token is configured and unauthenticated calls are not allowed")
	}
}

func TestLoadAuthConfig_ReadsTokenAndFlag(t *testing.T) {
	t.Setenv("FC_AUTH_TOKEN", "  "+testToken+"\n")
	t.Setenv("FC_AUTH_ALLOW_UNAUTHENTICATED", "true")
	cfg, err := loadAuthConfig()
	if err != nil {
		t.Fatalf("loadAuthConfig: %v", err)
	}
	if cfg.token != testToken {
		t.Fatalf("token not trimmed: %q", cfg.token)
	}
	if !cfg.allowUnauthenticated {
		t.Fatal("allowUnauthenticated should be true")
	}
}

func TestLoadAuthConfig_TokenAloneEnforces(t *testing.T) {
	t.Setenv("FC_AUTH_TOKEN", testToken)
	t.Setenv("FC_AUTH_ALLOW_UNAUTHENTICATED", "")
	cfg, err := loadAuthConfig()
	if err != nil {
		t.Fatalf("loadAuthConfig: %v", err)
	}
	if cfg.allowUnauthenticated {
		t.Fatal("allowUnauthenticated must default to false")
	}
}

// The /logs/ SSE endpoint on the metrics listener streams sandbox
// output; it must be gated by the same bearer token. /metrics and
// /healthz stay open for Prometheus and operators.
func TestAuth_HTTP_LogsRequireBearer(t *testing.T) {
	auth := newAuthenticator(authConfig{token: testToken})
	called := false
	h := auth.requireHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/vm-1", nil))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("no token: status=%d called=%v, want 401 and not called", rec.Code, called)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs/vm-1", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("wrong token: status=%d called=%v, want 401 and not called", rec.Code, called)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logs/vm-1", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("right token: status=%d called=%v, want 204 and called", rec.Code, called)
	}
}

// In allow mode the WARN line is emitted once per window and names
// every caller seen in that window, so a fleet of legacy callers shows
// up as a list, not as whichever one happened to be first.
func TestAuth_AllowMode_AggregatesCallersPerWindow(t *testing.T) {
	auth := newAuthenticator(authConfig{token: testToken, allowUnauthenticated: true})
	auth.warnInterval = time.Hour // never auto-flush during the test

	if err := auth.authorize("", "10.42.0.90:51234", "/everstack.firecracker.v1.FirecrackerAgent/NodeInfo"); err != nil {
		t.Fatalf("allow mode rejected: %v", err)
	}
	_ = auth.authorize("Bearer wrong", "10.42.0.90:51299", "/everstack.firecracker.v1.FirecrackerAgent/Exec")
	_ = auth.authorize("", "10.42.0.100:40000", "/everstack.firecracker.v1.FirecrackerAgent/NodeInfo")

	line, ok := auth.flushUnauthenticated()
	if !ok {
		t.Fatal("expected a pending summary")
	}
	for _, want := range []string{"calls=3", "peers=10.42.0.100,10.42.0.90", "Exec", "NodeInfo"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "51234") {
		t.Errorf("summary should aggregate by host, not by ephemeral port: %q", line)
	}
	if _, ok := auth.flushUnauthenticated(); ok {
		t.Fatal("second flush must find nothing pending")
	}
}

func TestAuth_Enforced_RecordsNothing(t *testing.T) {
	auth := newAuthenticator(authConfig{token: testToken})
	auth.warnInterval = time.Hour
	_ = auth.authorize("", "10.42.0.90:1", "/x/Y")
	if _, ok := auth.flushUnauthenticated(); ok {
		t.Fatal("enforced mode rejects; it must not accumulate allow-mode summaries")
	}
}

func TestAuth_HTTP_AllowUnauthenticatedPassesThrough(t *testing.T) {
	auth := newAuthenticator(authConfig{token: testToken, allowUnauthenticated: true})
	h := auth.requireHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/vm-1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204 in allow mode", rec.Code)
	}
}
