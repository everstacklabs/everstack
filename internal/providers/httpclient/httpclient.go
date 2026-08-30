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
