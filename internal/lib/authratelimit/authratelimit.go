// Package authratelimit provides the per-caller rate limiter used on
// authentication endpoints. It lives here rather than under services/ because
// both auth implementations need it: the cloud service, and the builtin
// self-hosted server hosted by the gateway binary, which cannot import
// services/.
package authratelimit

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// AuthRateLimiter is a per-IP token-bucket rate limiter intended for the
// authentication endpoints (login, register, magic-link request, token
// exchange, OAuth callback). It exists separately from the gateway's
// tenant-aware RateLimiter because auth requests have no tenant identity
// before login completes.
//
// Limits are intentionally conservative: enough to allow a legitimate user
// to retry on slow networks, low enough to make credential stuffing or
// magic-link email bombing impractical from a single source. Distributed
// attacks across many IPs are not addressed here — that's a CDN / WAF
// concern and out of scope for the in-memory limiter.
//
// Single-pod assumption: counters live in process memory. Multi-replica
// auth service would need a Redis-backed implementation; for v1 the auth
// service is single-replica.
type AuthRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry

	// rps is requests per second (we configure as rpm/60).
	rps   rate.Limit
	burst int

	// idleTTL controls when an IP's bucket is evicted so the map doesn't
	// grow unbounded under scanner traffic.
	idleTTL time.Duration
	stopCh  chan struct{}
}

type ipEntry struct {
	limiter *rate.Limiter
	last    time.Time
}

// NewAuthRateLimiter constructs a limiter with the given per-minute rate
// and burst. burst<=0 falls back to rpm.
func NewAuthRateLimiter(rpm, burst int) *AuthRateLimiter {
	if burst <= 0 {
		burst = rpm
	}
	rl := &AuthRateLimiter{
		limiters: make(map[string]*ipEntry),
		rps:      rate.Limit(float64(rpm) / 60.0),
		burst:    burst,
		idleTTL:  10 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	go rl.sweepLoop()
	return rl
}

// Stop terminates the background sweeper. Optional — process exit is fine.
func (rl *AuthRateLimiter) Stop() {
	select {
	case <-rl.stopCh:
	default:
		close(rl.stopCh)
	}
}

// Wrap returns an http.Handler that enforces the limit per remote IP.
// On deny: 429 with Retry-After header and a small JSON body.
func (rl *AuthRateLimiter) Wrap(next http.Handler) http.Handler {
	if rl == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		lim := rl.getLimiter(ip)
		if !lim.Allow() {
			res := lim.Reserve()
			delay := res.DelayFrom(time.Now())
			res.Cancel()
			retryAfter := int(delay.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded","retry_after_seconds":` + strconv.Itoa(retryAfter) + `}`))
			logger.WithFields("ip", ip, "path", r.URL.Path, "retry_after_s", retryAfter).
				Warn("auth: rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *AuthRateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e, ok := rl.limiters[ip]
	if !ok {
		e = &ipEntry{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.limiters[ip] = e
	}
	e.last = time.Now()
	return e.limiter
}

func (rl *AuthRateLimiter) sweepLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case now := <-t.C:
			rl.mu.Lock()
			for k, e := range rl.limiters {
				if now.Sub(e.last) > rl.idleTTL {
					delete(rl.limiters, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// trustedProxies are the CIDRs of the proxies in front of this service, set
// from EVS_AUTH_TRUSTED_PROXIES at startup. See clientIP for why the list is
// load-bearing rather than cosmetic.
var trustedProxies = parseTrustedProxies(os.Getenv("EVS_AUTH_TRUSTED_PROXIES"))

func parseTrustedProxies(raw string) []*net.IPNet {
	var nets []*net.IPNet
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, network)
			continue
		}
		// Bare address: treat it as a single-host range.
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		logger.WithFields("entry", entry).Warn("auth: ignoring unparseable trusted proxy")
	}
	return nets
}

func isTrustedProxy(ip net.IP, proxies []*net.IPNet) bool {
	for _, network := range proxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP identifies the caller a rate limit should be charged against.
//
// It walks X-Forwarded-For from right to left and returns the first address
// that is not a proxy we put there. Reading the left-most entry instead is
// the tempting shortcut and it is not safe: nginx appends to the header
// rather than replacing it, so a caller who sends their own
// X-Forwarded-For puts a value of their choosing in the left-most position.
// Keying on that lets them present a new identity per request and the limit
// stops applying at all. Walking from the right means the worst a forged
// prefix can do is leave the caller's real address as the first untrusted
// hop, which is exactly what should be charged.
//
// With no trusted proxies configured it falls back to the socket peer, which
// cannot be forged. Behind an ingress that peer is the ingress itself, so
// every caller shares one bucket: coarse, and deliberately the safe
// direction, since over-limiting is recoverable and a bypass is not. Set
// EVS_AUTH_TRUSTED_PROXIES to get per-caller limits.
func clientIP(r *http.Request) string {
	return clientIPFrom(r, trustedProxies)
}

func clientIPFrom(r *http.Request, proxies []*net.IPNet) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && len(proxies) > 0 {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			ip := net.ParseIP(candidate)
			if ip == nil {
				continue
			}
			if isTrustedProxy(ip, proxies) {
				continue
			}
			return candidate
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// SensitiveProcedure reports whether a Connect RPC path names a procedure that
// mints a credential, mails a caller-supplied address, or consumes a secret
// the caller supplied. Paths look like
// "/everstack.auth.v1.AuthService/Login", so only the suffix is matched.
//
// This exists separately from the HTTP route list because the same operations
// are reachable both ways. On instance hosts the Connect surface is the only
// way to reach some of them: RequestMagicLink has no HTTP route at all, so
// limiting only the routes would leave the mail-sending entry point open.
func SensitiveProcedure(path string) bool {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return false
	}
	switch path[i+1:] {
	case "Login", "Register",
		// Mint a token and send mail to whatever address was supplied, so an
		// unlimited caller can bomb an arbitrary inbox.
		"RequestMagicLink", "RequestPasswordReset", "InviteTeamMember",
		// Consume a caller-supplied secret. CompleteEmailVerification's is a
		// six-digit code, which falls to brute force in under a minute if the
		// attempt rate is unbounded.
		"VerifyMagicLink", "CompletePasswordReset", "CompleteEmailVerification",
		"AcceptInvitation",
		// Exchange paths that hand back a session.
		"ExchangeDeviceCode", "ApproveDeviceAuthorization":
		return true
	}
	return false
}

// WrapSensitive limits only the procedures SensitiveProcedure names, letting
// hot read paths such as GetSession through untouched so the dashboard is not
// slowed for a legitimate user.
func (rl *AuthRateLimiter) WrapSensitive(next http.Handler) http.Handler {
	if rl == nil {
		return next
	}
	limited := rl.Wrap(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if SensitiveProcedure(r.URL.Path) {
			limited.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
