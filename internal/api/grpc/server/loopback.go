package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// The REST gateway (grpc-gateway) reaches the Connect handlers by making a
// gRPC call back into this same process. That call used to be a real network
// connection to external_domain:port, which had three costs:
//
//   - It shared the public listener's HTTP/2 settings. Go's http2 server allows
//     250 concurrent streams per connection and gRPC keeps one connection, so
//     the whole REST surface, /v1/chat/completions included, could never have
//     more than 250 requests in flight. Everything past that queued inside the
//     gRPC client with no error and no metric.
//   - Every request paid a TCP round trip (and a TLS one when the server has a
//     certificate) to talk to itself.
//   - The dial target is the configured external domain, so the "loopback"
//     depended on DNS, on that name routing back to this process, and on
//     HTTPS_PROXY not capturing it.
//
// Loopback replaces the network with an in-memory pipe served by its own
// HTTP/2 server. The requests are the ones the gRPC client sent before (same
// :authority, same metadata) and they enter the same handler tree, so
// authentication, tenant resolution and every interceptor run exactly as they
// did. Only the wire underneath is different.

const (
	// loopbackTransportEnv selects the transport. Unset (the default), the
	// gateway service's REST calls run its handlers directly (see direct.go)
	// and other services use the in-memory HTTP/2 pipe. "memory" puts every
	// service on the pipe; "tcp" restores the network dial to
	// external_domain:port for an operator who needs that behaviour back.
	loopbackTransportEnv = "EVS_GATEWAY_LOOPBACK_TRANSPORT"

	// loopbackMaxStreams bounds concurrent in-process calls on one connection.
	// It is not a capacity control: the public listener and the handlers behind
	// it already admit or refuse the outer request. It only has to be high
	// enough that the pipe is never the thing a request waits on.
	loopbackMaxStreams = 1 << 20

	// loopbackBufferSize is the in-memory pipe's buffer in each direction.
	loopbackBufferSize = 1 << 20
)

// loopbackInMemory reports whether the gateway should call itself over the
// in-memory transport rather than the network.
func loopbackInMemory() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(loopbackTransportEnv)), "tcp")
}

// loopbackDirect reports whether calls that can skip HTTP/2 should.
func loopbackDirect() bool {
	v := strings.TrimSpace(os.Getenv(loopbackTransportEnv))
	switch {
	case strings.EqualFold(v, "tcp"), strings.EqualFold(v, "memory"):
		return false
	case v != "" && !strings.EqualFold(v, "direct"):
		logger.Warnf("%s=%q is not one of direct, memory or tcp; using direct", loopbackTransportEnv, v)
	}
	return true
}

type loopbackContextKey struct{}

// WithLoopback returns a context carrying a new in-memory transport for the
// REST gateway built from it, or ctx unchanged when the operator selected tcp.
//
// Only a caller that goes on to Serve the transport may use this: a gateway
// built with a transport nobody serves would wait on its own calls forever.
// A process that mounts the gateway router inside another server leaves the
// context alone and keeps the network dial.
func WithLoopback(ctx context.Context) context.Context {
	if !loopbackInMemory() {
		return ctx
	}
	l := NewLoopback()
	l.direct = loopbackDirect()
	return context.WithValue(ctx, loopbackContextKey{}, l)
}

// LoopbackFromContext returns the transport WithLoopback attached, or nil.
func LoopbackFromContext(ctx context.Context) *Loopback {
	l, _ := ctx.Value(loopbackContextKey{}).(*Loopback)
	return l
}

// Loopback is the in-memory transport between the REST gateway's gRPC client
// and this process's own handlers.
type Loopback struct {
	lis *bufconn.Listener

	// direct: the operator left calls that can skip HTTP/2 to do so.
	direct bool
	// dials counts HTTP/2 connections made through DialOptions.
	dials atomic.Int64

	// The handler Serve was given, for calls made through ClientConn.
	serveOnce sync.Once
	served    chan struct{} // closed once handler is set
	handler   http.Handler
}

// NewLoopback creates a transport nobody is serving yet. Calls dialled before
// Serve block until it is, exactly as a TCP dial waits on a listener's backlog.
func NewLoopback() *Loopback {
	return &Loopback{lis: bufconn.Listen(loopbackBufferSize), served: make(chan struct{})}
}

// servedHandler returns the handler tree calls are answered with, waiting for
// Serve if it has not been called yet.
func (l *Loopback) servedHandler(ctx context.Context) (http.Handler, error) {
	select {
	case <-l.served:
		return l.handler, nil
	default:
	}
	select {
	case <-l.served:
		return l.handler, nil
	case <-ctx.Done():
		return nil, statusFromContext(ctx)
	}
}

// Target is the gRPC target for endpoint on this transport. The passthrough
// scheme hands the address to the dialer untouched, so no DNS lookup happens,
// while the call's :authority stays the endpoint itself: host based routing
// sees the same Host header it saw over the network.
func (l *Loopback) Target(endpoint string) string {
	return "passthrough:///" + endpoint
}

// DialOptions connects a gRPC client to this transport. The address gRPC asks
// for is ignored; there is only one place to go.
func (l *Loopback) DialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		// Nothing leaves the process, so there is nothing for TLS to protect.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			l.dials.Add(1)
			conn, err := l.lis.DialContext(ctx)
			if err != nil {
				return nil, err
			}
			return loopbackConn{Conn: conn}, nil
		}),
	}
}

// Serve answers calls on this transport with handler, which must be the same
// handler tree the public listener serves. It returns once the accept loop is
// running; the returned function stops it.
//
// Requests get a background base context, as they do on the public listener:
// nothing from process start-up leaks into them, and cancelling a start-up
// context at shutdown does not cut off calls that are still being answered.
func (l *Loopback) Serve(handler http.Handler) (stop func()) {
	return l.serve(handler, newLoopbackHTTP2Server())
}

// newLoopbackHTTP2Server is the HTTP/2 server that answers in-process calls.
//
// It does not use the library's default write scheduler. That one keeps every
// open stream in a ring and, each time the connection may write, walks the
// ring until it finds a stream with a frame queued. Each REST service's calls
// share one in-process connection, so with a thousand chat requests waiting on
// a provider the walk visited a thousand idle streams per frame: a CPU profile
// at 1000 requests per second put 9% of the whole gateway in that loop. The
// random scheduler tracks only the streams that have something to send.
func newLoopbackHTTP2Server() *http2.Server {
	return &http2.Server{
		MaxConcurrentStreams: loopbackMaxStreams,
		NewWriteScheduler:    func() http2.WriteScheduler { return http2.NewRandomWriteScheduler() },
	}
}

func (l *Loopback) serve(handler http.Handler, h2 *http2.Server) (stop func()) {
	l.serveOnce.Do(func() {
		l.handler = handler
		close(l.served)
	})
	go func() {
		for {
			conn, err := l.lis.Accept()
			if err != nil {
				// Stopped, and a closed bufconn listener never recovers.
				return
			}
			go h2.ServeConn(loopbackConn{Conn: conn}, &http2.ServeConnOpts{Handler: handler})
		}
	}()
	return func() { _ = l.lis.Close() }
}

// loopbackAddr is the address both ends of the in-memory pipe report.
//
// It is a real loopback TCP address on purpose. Code behind the gateway
// decides how far to trust X-Forwarded-For, and which bucket a rate limiter
// uses, from the peer address. Over the network that peer was this host (or
// a proxy in front of it); a non-IP address here would silently reclassify
// every REST call as coming from one untrusted client.
var loopbackAddr net.Addr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}

// loopbackConn presents the in-memory pipe as a local TCP connection.
type loopbackConn struct {
	net.Conn
}

func (loopbackConn) LocalAddr() net.Addr  { return loopbackAddr }
func (loopbackConn) RemoteAddr() net.Addr { return loopbackAddr }
