package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
)

// internalServer stands in for a cluster service a tenant should never reach
// and counts the requests that arrive.
func internalServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPublicEndpointsRequired_BlocksInternalTargets(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(true)()

	for _, tt := range []TransportType{TransportStreamableHTTP, TransportSSE} {
		tp, err := NewTransport(ServerConfig{URL: srv.URL, TransportType: tt})
		if err != nil {
			t.Fatalf("%s: NewTransport: %v", tt, err)
		}
		_, err = tp.Send(context.Background(), &JsonRpcRequest{Method: "tools/list"})
		if !errors.Is(err, outboundguard.ErrBlocked) {
			t.Fatalf("%s: Send to loopback = %v, want ErrBlocked", tt, err)
		}
		_ = tp.Close()
	}
	if _, err := DiscoverOAuth(context.Background(), srv.URL); !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("DiscoverOAuth to loopback = %v, want ErrBlocked", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests", n)
	}
}

// A public-looking host name that resolves to a private address is refused
// before any connection is made.
func TestPublicEndpointsRequired_BlocksHostResolvingPrivate(t *testing.T) {
	defer SetPublicEndpointsForTest(true)()
	prev := guardDialConfig
	guardDialConfig = outboundguard.Config{Resolver: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.43.0.10")}, nil
	}}
	t.Cleanup(func() { guardDialConfig = prev })

	tp, err := NewTransport(ServerConfig{URL: "http://mcp.attacker.example/mcp", TransportType: TransportStreamableHTTP})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	defer tp.Close()
	if _, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "tools/list"}); !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("Send = %v, want ErrBlocked", err)
	}
}

// Self-hosted gateways never turn the switch on and keep reaching local and
// private MCP servers.
func TestPublicEndpointsNotRequired_ReachesLocalServers(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(false)()

	tp, err := NewTransport(ServerConfig{URL: srv.URL, TransportType: TransportStreamableHTTP})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	defer tp.Close()
	if _, err := tp.Send(context.Background(), &JsonRpcRequest{Method: "tools/list"}); err != nil {
		t.Fatalf("Send = %v, want success", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}
