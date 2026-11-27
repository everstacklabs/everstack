package httpclient

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxConnsPerHostEnv overrides how many connections the gateway opens to
	// one upstream host. 0 removes the limit.
	maxConnsPerHostEnv = "EVS_PROVIDER_MAX_CONNS_PER_HOST"

	// defaultMaxConnsPerHost bounds connections to one upstream host.
	//
	// An HTTP/1.1 upstream carries one request per connection, so this is also
	// the number of requests the gateway will have in flight to that host;
	// request N+1 waits inside the transport for a connection, with no error
	// and no metric. It was 512, which capped any HTTP/1.1 upstream (a
	// self-hosted model server, another proxy) at 512 concurrent completions:
	// about 570 requests a second when a completion takes 900 ms. An HTTP/2
	// upstream multiplexes and rarely comes near either number.
	//
	// The limit stays finite so that a burst cannot exhaust file descriptors.
	defaultMaxConnsPerHost = 4096
)

// maxConnsPerHost is the per-host connection limit: the operator's override
// when it is a non-negative integer, the default otherwise.
func maxConnsPerHost() int {
	raw := strings.TrimSpace(os.Getenv(maxConnsPerHostEnv))
	if raw == "" {
		return defaultMaxConnsPerHost
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return defaultMaxConnsPerHost
	}
	return n
}

// newTransport builds the tuned provider transport. Default and Unary each
// get their own (and so their own connection pool) and differ only in
// ResponseHeaderTimeout.
func newTransport() *http.Transport {
	maxConns := maxConnsPerHost()
	// Keep as many idle connections as may be open, so traffic at a steady
	// concurrency reuses connections instead of closing and redialing every
	// one past the idle limit (a TLS handshake each time on an HTTPS
	// upstream). Idle connections still close after IdleConnTimeout.
	idlePerHost := maxConns
	if idlePerHost == 0 {
		idlePerHost = defaultMaxConnsPerHost
	}
	return &http.Transport{
		Proxy: egressProxy,
		DialContext: egressDial(&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 90 * time.Second,
		}),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          2 * idlePerHost,
		MaxIdleConnsPerHost:   idlePerHost,
		MaxConnsPerHost:       maxConns,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
	}
}

var (
	defaultTransport     *http.Transport
	defaultTransportOnce sync.Once
)

// Default returns an HTTP client on the shared tuned streaming Transport,
// suitable for high-QPS, low-latency provider requests. The client Timeout is
// left at zero so long-lived streaming requests are not forcibly terminated.
// Callers should apply per-request context timeouts for unary operations.
//
// The transport (and therefore the connection pool) is shared by every call,
// but each call returns a fresh http.Client, exactly as Unary does. Provider
// constructors assign cli.Transport = <instrumented wrapper>(cli.Transport)
// on the client they get here. When Default handed out one process-wide
// client, every provider construction stacked another wrapper onto it (a
// tenant bundle rebuild every 15 seconds in shared mode grew the chain without
// bound, so per-request overhead grew with uptime) and the assignment raced
// with requests reading the field. A per-call client keeps each wrapper on
// the caller's own client.
//
// Connection limits and reuse are set by newTransport and shared with Unary.
func Default() *http.Client {
	return &http.Client{Transport: Transport()}
}

// Transport returns the underlying shared streaming transport for advanced
// configuration. Use with caution - modifying the transport after
// initialization may cause races.
func Transport() *http.Transport {
	defaultTransportOnce.Do(func() {
		tr := newTransport()
		tr.ResponseHeaderTimeout = 30 * time.Second
		defaultTransport = tr
	})
	return defaultTransport
}

var (
	unaryTransportOnce sync.Once
	unaryTransport     *http.Transport
)

// Unary returns a client for non-streaming provider calls (chat completions,
// embeddings, moderation) which must be bounded by the caller's context
// deadline alone.
//
// It deliberately omits Default's ResponseHeaderTimeout. That guard measures
// the gap between finishing the request write and receiving response headers,
// which is exactly right for streaming — upstreams emit headers at first token
// — but wrong for unary completions, where upstreams withhold headers until the
// whole completion is generated. On a unary call the setting therefore acts as
// a hard cap on total generation time, so any request whose output is long
// enough to exceed it fails with "timeout awaiting response headers" no matter
// how generous the caller's own deadline is.
//
// Every other tuning knob matches Default. The transport (and therefore the
// connection pool) is shared across calls, but each call returns a fresh
// http.Client so that callers which wrap Transport for instrumentation affect
// only their own client rather than every other provider's.
func Unary() *http.Client {
	unaryTransportOnce.Do(func() {
		unaryTransport = newTransport()
	})
	return &http.Client{Transport: unaryTransport}
}
