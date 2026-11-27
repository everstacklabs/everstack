package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	gatewayv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

// modelsStub is a Connect GatewayService that answers ListModels and records
// what the call looked like when it arrived.
type modelsStub struct {
	gatewayconnect.UnimplementedGatewayServiceHandler

	mu     sync.Mutex
	apiKey string
	peer   string
}

func (s *modelsStub) ListModels(_ context.Context, req *connect.Request[gatewayv1.ListModelsRequest]) (*connect.Response[gatewayv1.ListModelsResponse], error) {
	s.mu.Lock()
	s.apiKey = req.Header().Get(common.EverstackApiKey)
	s.peer = req.Peer().Addr
	s.mu.Unlock()
	return connect.NewResponse(&gatewayv1.ListModelsResponse{
		Providers: []*gatewayv1.ProviderModels{{Provider: "loopback-test"}},
	}), nil
}

// connectTree mounts the stub the way the server mounts Connect services and
// records the Host each call carried.
func connectTree(stub *modelsStub, host *string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(gatewayconnect.NewGatewayServiceHandler(stub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*host = r.Host
		mux.ServeHTTP(w, r)
	})
}

// The production constructor, given a context that opted in, must carry a
// REST request all the way to a Connect handler without the network: the
// configured address here resolves to nothing and nothing listens on it.
func TestRESTGatewayCallsConnectHandlersOverLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(WithLoopback(context.Background()))
	defer cancel()

	rest, err := NewGRPCJSONGateway(ctx, "gateway.invalid", 1, nil)
	if err != nil {
		t.Fatalf("NewGRPCJSONGateway: %v", err)
	}

	stub := &modelsStub{}
	var host string
	stop := LoopbackFromContext(ctx).Serve(connectTree(stub, &host))
	defer stop()

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/models", nil)
	req.Header.Set(common.EverstackApiKey, "key-from-the-rest-caller")
	rec := httptest.NewRecorder()
	rest.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "loopback-test") {
		t.Fatalf("response did not come from the Connect handler: %s", rec.Body.String())
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	// The credential must arrive exactly as it did over the network: the
	// Connect API-key interceptor authenticates the call from this header.
	if stub.apiKey != "key-from-the-rest-caller" {
		t.Fatalf("Connect handler saw api key %q, want the REST caller's", stub.apiKey)
	}
	if host != "gateway.invalid:1" {
		t.Fatalf("Connect handler saw Host %q, want the configured endpoint", host)
	}
	peerHost, _, err := net.SplitHostPort(stub.peer)
	if err != nil {
		t.Fatalf("peer %q is not host:port: %v", stub.peer, err)
	}
	if ip := net.ParseIP(peerHost); ip == nil || !ip.IsLoopback() {
		t.Fatalf("peer %q is not a loopback IP", stub.peer)
	}
}

// Without the opt-in the constructor keeps dialing the configured address,
// which is what a process that mounts the router elsewhere relies on. With
// nothing listening there the call fails rather than finding the handlers
// some other way.
func TestRESTGatewayWithoutLoopbackDialsTheNetwork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rest, err := NewGRPCJSONGateway(ctx, "127.0.0.1", 1, nil)
	if err != nil {
		t.Fatalf("NewGRPCJSONGateway: %v", err)
	}
	rec := httptest.NewRecorder()
	rest.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/gateway/models", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 from a refused dial: %s", rec.Code, rec.Body.String())
	}
}

// By default the gateway service's REST calls reach its handlers without
// HTTP/2: no connection is ever dialled. "memory" keeps them on the in-memory
// HTTP/2 pipe. Either way the handler sees the REST caller's credential.
func TestRESTGatewayTransportFollowsTheOperatorsChoice(t *testing.T) {
	for _, tc := range []struct {
		setting   string
		wantDials bool
	}{
		{"", false},
		{"memory", true},
	} {
		t.Run("EVS_GATEWAY_LOOPBACK_TRANSPORT="+tc.setting, func(t *testing.T) {
			t.Setenv(loopbackTransportEnv, tc.setting)
			ctx, cancel := context.WithCancel(WithLoopback(context.Background()))
			defer cancel()
			rest, err := NewGRPCJSONGateway(ctx, "gateway.invalid", 1, nil)
			if err != nil {
				t.Fatalf("NewGRPCJSONGateway: %v", err)
			}
			loopback := LoopbackFromContext(ctx)
			stub := &modelsStub{}
			var host string
			stop := loopback.Serve(connectTree(stub, &host))
			defer stop()

			req := httptest.NewRequest(http.MethodGet, "/v1/gateway/models", nil)
			req.Header.Set(common.EverstackApiKey, "key-from-the-rest-caller")
			rec := httptest.NewRecorder()
			rest.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "loopback-test") {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			stub.mu.Lock()
			apiKey := stub.apiKey
			stub.mu.Unlock()
			if apiKey != "key-from-the-rest-caller" || host != "gateway.invalid:1" {
				t.Fatalf("handler saw api key %q and Host %q", apiKey, host)
			}
			if dialled := loopback.dials.Load() > 0; dialled != tc.wantDials {
				t.Fatalf("HTTP/2 connection dialled = %v, want %v", dialled, tc.wantDials)
			}
			if (GatewayDirectConn() != nil) == tc.wantDials {
				t.Fatalf("GatewayDirectConn() = %v with transport %q", GatewayDirectConn(), tc.setting)
			}
		})
	}
}
