package httpclient

import (
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	defaultClient     *http.Client
	defaultClientOnce sync.Once
)

// Default returns a shared HTTP client with a tuned Transport suitable for
// high-QPS, low-latency provider requests. The client Timeout is left at zero
// so long-lived streaming requests are not forcibly terminated. Callers should
// apply per-request context timeouts for unary operations.
//
// Performance optimizations (HFT-inspired):
//   - Increased MaxIdleConns/MaxIdleConnsPerHost for connection reuse
//   - Aggressive KeepAlive to maintain warm connections
//   - MaxConnsPerHost cap to prevent connection storms
//   - Extended IdleConnTimeout for long-idle periods between bursts
func Default() *http.Client {
	defaultClientOnce.Do(func() {
		tr := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 90 * time.Second, // Increased from 60s for persistent connections
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,              // Increased from 512 for high concurrency
			MaxIdleConnsPerHost:   256,               // Increased from 128 for per-provider throughput
			MaxConnsPerHost:       512,               // NEW: cap max concurrent to prevent storms
			IdleConnTimeout:       120 * time.Second, // Increased from 90s for burst traffic patterns
			TLSHandshakeTimeout:   3 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			DisableCompression:    false,
			// WriteBufferSize and ReadBufferSize left at defaults (4KB)
			// These are typically fine for JSON payloads
		}
		defaultClient = &http.Client{Transport: tr}
	})
	return defaultClient
}

// Transport returns the underlying transport for advanced configuration.
// Use with caution - modifying the transport after initialization may cause races.
func Transport() *http.Transport {
	Default() // Ensure initialized
	return defaultClient.Transport.(*http.Transport)
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
		unaryTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 90 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   256,
			MaxConnsPerHost:       512,
			IdleConnTimeout:       120 * time.Second,
			TLSHandshakeTimeout:   3 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    false,
		}
	})
	return &http.Client{Transport: unaryTransport}
}
