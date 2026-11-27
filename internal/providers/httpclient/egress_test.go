package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/outboundguard"
)

func internalServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func allClients() map[string]*http.Client {
	return map[string]*http.Client{
		"default": Default(),
		"unary":   Unary(),
		"egress":  NewEgressClient(5 * time.Second),
	}
}

func TestPublicEndpointsRequiredBlocksInternalTargets(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(true)()

	for name, client := range allClients() {
		resp, err := client.Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, outboundguard.ErrBlocked) {
			t.Fatalf("%s: GET loopback = %v, want ErrBlocked", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests", n)
	}
}

func TestMetadataAddressIsBlocked(t *testing.T) {
	defer SetPublicEndpointsForTest(true)()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Default().Do(req)
	if !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("GET metadata address = %v, want ErrBlocked", err)
	}
}

// A self-hosted gateway has to keep reaching private addresses: a local Ollama
// is a supported setup, and this is why the guard is a switch.
func TestInternalTargetsReachableWithoutTheSwitch(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(false)()

	for name, client := range allClients() {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("%s: GET loopback: %v", name, err)
		}
		_ = resp.Body.Close()
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("server received %d requests, want 3", got)
	}
}

// useFakeDNS makes every hostname resolve to addr while really connecting to
// the loopback server, so a "public" host can be tested without a network.
func useFakeDNS(t *testing.T, addr string, target *httptest.Server) {
	t.Helper()
	targetAddr := target.Listener.Addr().String()
	prev := guardDialConfig
	guardDialConfig = outboundguard.Config{
		Resolver: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr(addr)}, nil
		},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, targetAddr)
		},
	}
	t.Cleanup(func() { guardDialConfig = prev })
}

// A public custom endpoint stays allowed: customers legitimately front their
// own router with a public URL.
func TestPublicHostnameIsAllowed(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(true)()
	useFakeDNS(t, "93.184.216.34", srv)

	resp, err := NewEgressClient(5 * time.Second).Get("http://customer-router.example/v1/models")
	if err != nil {
		t.Fatalf("GET public host: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatal("public host was not reached")
	}
}

// A hostname that resolves to a private address is the DNS form of the attack.
func TestHostnameResolvingToPrivateAddressIsBlocked(t *testing.T) {
	srv, hits := internalServer(t)
	defer SetPublicEndpointsForTest(true)()
	useFakeDNS(t, "10.43.0.1", srv)

	resp, err := NewEgressClient(5 * time.Second).Get("http://looks-public.example/v1/models")
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, outboundguard.ErrBlocked) {
		t.Fatalf("GET rebinding host = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Fatal("private target was reached")
	}
}

func TestProxyIsIgnoredWhenPublicEndpointsRequired(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.internal:3128")
	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "customer-router.example"}}

	defer SetPublicEndpointsForTest(true)()
	got, err := egressProxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("proxy = %v, want none: it would connect on the guard's behalf", got)
	}
}
