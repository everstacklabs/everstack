package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// RateLimiter enforces per-tenant request-rate limits read from the
// runtime_config service. Single-replica gateway deployments use the
// in-memory token bucket below; multi-replica needs a Redis-backed
// implementation, which is deferred (see docs/audits/runtime-config.md).
//
// Behaviour summary:
//   - Disabled / no rtconfig service → pass-through.
//   - Per-request: extract tenant_id and the configured key (correlation
//     id / user id / IP) from the request, find or create a token-bucket
//     limiter for that (tenant, key) pair, call Allow().
//   - On deny: 429 with Retry-After plus X-RateLimit-Limit/Remaining/Reset.
//   - Idle limiters are swept every 5 min so the map doesn't grow forever.
type RateLimiter struct {
	svc *rtconfig.Service

	mu       sync.Mutex
	limiters map[string]*entry

	// idleTTL is how long a (tenant,key) limiter survives without traffic
	// before the cleanup sweep evicts it. Long enough that a normal
	// burst-then-pause pattern doesn't lose its state, short enough that
	// noisy clients don't pin memory forever.
	idleTTL time.Duration

	stopCh chan struct{}
}

type entry struct {
	limiter *rate.Limiter
	rpm     int
	burst   int
	lastHit time.Time
}

// NewRateLimiter constructs the middleware. Pass nil svc to make every
// request a pass-through (useful in self-hosted-without-DB setups —
// keeps test rigs simple).
func NewRateLimiter(svc *rtconfig.Service) *RateLimiter {
	rl := &RateLimiter{
		svc:      svc,
		limiters: make(map[string]*entry),
		idleTTL:  10 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	go rl.sweepLoop()
	return rl
}

// Stop terminates the background cleanup goroutine. Optional; the
// process exiting is just as good.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopCh:
	default:
		close(rl.stopCh)
	}
}

// Wrap returns the middleware-wrapped handler.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	if rl == nil || rl.svc == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := contextkeys.ExtractTenantID(r.Context())
		cfg := rl.svc.GetRateLimit(tenantID)

		if !cfg.Enabled || cfg.RequestsPerMinute <= 0 {
			next.ServeHTTP(w, r)
			return
		}

		key := resolveKey(r, cfg.KeySource, tenantID)
		lim, rpm, burst := rl.getLimiter(tenantID, key, cfg)

		// Inform the client of the current limits regardless of outcome
		// — a 200 with X-RateLimit-Remaining is what dashboards plot.
		setRateLimitHeaders(w, lim, rpm, burst)

		if !lim.Allow() {
			// Reservation tells us how long until a token is available.
			res := lim.Reserve()
			delay := res.DelayFrom(time.Now())
			res.Cancel() // we're not actually waiting; just measuring
			retryAfter := int(delay.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":{"code":429,"message":"rate limit exceeded","retry_after_seconds":%d}}`, retryAfter)
			logger.WithFields(
				"tenant_id", tenantID,
				"key", key,
				"rpm", rpm,
				"retry_after_s", retryAfter,
			).Debug("ratelimit: request denied")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// getLimiter finds or creates a token-bucket limiter for (tenant,key).
// Re-creates the limiter when the tenant changes its rpm/burst so the
// next request reflects the new settings without waiting for cache
// invalidation in rtconfig (which is fast but not synchronous with
// the user clicking Save).
func (rl *RateLimiter) getLimiter(tenantID, key string, cfg rtconfig.RateLimitConfig) (*rate.Limiter, int, int) {
	cacheKey := tenantID + "|" + key
	burst := cfg.Burst
	if burst <= 0 {
		burst = cfg.RequestsPerMinute // fall back to rpm if burst missing
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	e, ok := rl.limiters[cacheKey]
	if !ok || e.rpm != cfg.RequestsPerMinute || e.burst != burst {
		e = &entry{
			limiter: rate.NewLimiter(rate.Limit(float64(cfg.RequestsPerMinute)/60.0), burst),
			rpm:     cfg.RequestsPerMinute,
			burst:   burst,
		}
		rl.limiters[cacheKey] = e
	}
	e.lastHit = time.Now()
	return e.limiter, e.rpm, e.burst
}

func (rl *RateLimiter) sweepLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case now := <-t.C:
			rl.sweep(now)
		}
	}
}

func (rl *RateLimiter) sweep(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, e := range rl.limiters {
		if now.Sub(e.lastHit) > rl.idleTTL {
			delete(rl.limiters, k)
		}
	}
}

// resolveKey extracts the rate-limit key from the request based on
// what the tenant chose in their runtime config. Unknown / empty
// sources fall back to correlation, then to a generic "anon" so
// every limiter is at least bounded somehow.
func resolveKey(r *http.Request, source, tenantID string) string {
	switch source {
	case "user":
		if uid, _, _ := contextkeys.ExtractRequestContext(r.Context()); uid != "" {
			return "u:" + uid
		}
		// fall through
	case "ip":
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return "ip:" + host
		}
		return "ip:" + r.RemoteAddr
	}
	// Default: correlation id. Generated upstream; if missing, the
	// limiter degrades to per-tenant which is still useful.
	if cid := r.Header.Get("X-Correlation-Id"); cid != "" {
		return "cid:" + cid
	}
	if tenantID != "" {
		return "t:" + tenantID
	}
	return "anon"
}

// setRateLimitHeaders writes the standard X-RateLimit-* response
// headers so clients can see remaining capacity. We use Tokens() for
// "remaining now" and rpm for the window — Reset is rounded up to
// the next 60s boundary because the bucket is continuous, not discrete.
func setRateLimitHeaders(w http.ResponseWriter, lim *rate.Limiter, rpm, burst int) {
	remaining := int(lim.Tokens())
	if remaining < 0 {
		remaining = 0
	}
	if remaining > burst {
		remaining = burst
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rpm))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(60*time.Second).Unix(), 10))
}
