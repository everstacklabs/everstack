package server

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// requestSeen is what the handler tree behind the loopback observed.
type requestSeen struct {
	mu         sync.Mutex
	host       string
	remoteAddr string
	proto      string
	tls        bool
}

// newLoopbackHealth serves a gRPC health service over a fresh Loopback and
// returns a client connected to it through the transport's dial options.
func newLoopbackHealth(t *testing.T, endpoint string) (healthpb.HealthClient, *requestSeen) {
	t.Helper()

	grpcSrv := grpc.NewServer()
	healthpb.RegisterHealthServer(grpcSrv, health.NewServer())

	seen := &requestSeen{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.host, seen.remoteAddr, seen.proto, seen.tls = r.Host, r.RemoteAddr, r.Proto, r.TLS != nil
		seen.mu.Unlock()
		grpcSrv.ServeHTTP(w, r)
	})

	loopback := NewLoopback()
	stop := loopback.Serve(handler)

	conn, err := grpc.NewClient(loopback.Target(endpoint), loopback.DialOptions()...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		stop()
		grpcSrv.Stop()
	})
	return healthpb.NewHealthClient(conn), seen
}

// The handlers must not be able to tell the in-memory call from the network
// one it replaces: same Host (host based tenant routing reads it) and a
// loopback peer address (X-Forwarded-For trust and rate-limit keys read it).
func TestLoopbackPreservesWhatHandlersSee(t *testing.T) {
	// A name that does not resolve: the call must never reach DNS or a socket.
	const endpoint = "gateway.invalid:8089"
	client, seen := newLoopbackHealth(t, endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check over loopback: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.GetStatus())
	}

	seen.mu.Lock()
	defer seen.mu.Unlock()
	if seen.host != endpoint {
		t.Fatalf("handler saw Host %q, want the configured endpoint %q", seen.host, endpoint)
	}
	host, _, err := net.SplitHostPort(seen.remoteAddr)
	if err != nil {
		t.Fatalf("RemoteAddr %q is not host:port: %v", seen.remoteAddr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("RemoteAddr %q is not a loopback IP; peer-address trust decisions would change", seen.remoteAddr)
	}
	if seen.proto != "HTTP/2.0" {
		t.Fatalf("proto = %q, want HTTP/2.0", seen.proto)
	}
}

// Go's http2 server allows 250 concurrent streams per connection and gRPC
// keeps a single connection, so over the public listener the 251st in-flight
// REST request waited for one of the first 250 to finish. Hold well over 250
// calls open at once and require every one of them to be answered.
func TestLoopbackIsNotCappedAtDefaultHTTP2Streams(t *testing.T) {
	const inFlight = 1000
	client, _ := newLoopbackHealth(t, "localhost:8089")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Watch is server-streaming and stays open after its first message, so
	// each call below occupies a stream until the test ends.
	var answered sync.WaitGroup
	errs := make(chan error, inFlight)
	for i := 0; i < inFlight; i++ {
		answered.Add(1)
		go func() {
			defer answered.Done()
			stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				errs <- err
				return
			}
			if _, err := stream.Recv(); err != nil {
				errs <- err
			}
		}()
	}

	done := make(chan struct{})
	go func() { answered.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("not all %d concurrent calls were answered: some are queued behind a stream limit", inFlight)
	}
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
}

// With the operator override the context carries no transport, which is what
// tells the gateway to keep dialing the network.
func TestWithLoopbackHonoursTCPOverride(t *testing.T) {
	t.Setenv(loopbackTransportEnv, "tcp")
	if LoopbackFromContext(WithLoopback(context.Background())) != nil {
		t.Fatalf("%s=tcp still attached an in-memory transport", loopbackTransportEnv)
	}
	t.Setenv(loopbackTransportEnv, "")
	if LoopbackFromContext(WithLoopback(context.Background())) == nil {
		t.Fatalf("default did not attach an in-memory transport")
	}
	if LoopbackFromContext(context.Background()) != nil {
		t.Fatalf("a context that never opted in carries a transport")
	}
}
