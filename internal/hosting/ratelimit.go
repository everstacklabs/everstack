package hosting

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPLimiter is a token-bucket rate limiter keyed by client IP. It is
// deliberately independent of tenant runtime config: anonymous evs.run
// callers have no tenant, so the tenant-scoped middleware limiter
// (internal/api/http/middleware/ratelimit.go) cannot apply. Enforcement
// happens inside the hosting handlers so every transport (Connect, gRPC,
// REST gateway) is covered.
type IPLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*ipBucket
	rpm      rate.Limit
	burst    int
	lastSwep time.Time
}

type ipBucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// NewIPLimiter allows roughly perMinute events per IP with the given burst.
func NewIPLimiter(perMinute float64, burst int) *IPLimiter {
	return &IPLimiter{
		buckets:  make(map[string]*ipBucket),
		rpm:      rate.Limit(perMinute / 60.0),
		burst:    burst,
		lastSwep: time.Now(),
	}
}

// GlobalLimiter is a single token bucket across ALL anonymous callers. It
// backstops the per-IP limiter: client-IP headers are spoofable on paths
// that do not transit Cloudflare, so per-IP buckets can be rotated, but
// the aggregate anonymous request rate stays bounded regardless.
type GlobalLimiter struct {
	lim *rate.Limiter
}

func NewGlobalLimiter(perMinute float64, burst int) *GlobalLimiter {
	return &GlobalLimiter{lim: rate.NewLimiter(rate.Limit(perMinute/60.0), burst)}
}

func (g *GlobalLimiter) Allow() bool { return g.lim.Allow() }

// Allow reports whether the given IP may proceed. Unknown/empty IPs share
// one bucket ("unknown") rather than bypassing the limit.
func (l *IPLimiter) Allow(ip string) bool {
	if ip == "" {
		ip = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// Sweep idle buckets occasionally so the map cannot grow unbounded.
	if now.Sub(l.lastSwep) > 10*time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > 30*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastSwep = now
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{lim: rate.NewLimiter(l.rpm, l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = now
	return b.lim.Allow()
}
