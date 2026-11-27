package apikey

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
)

type ActivityRecorder interface {
	Record(keyID, tenantID string, surface Surface)
}

// Runtime is shared by every key resolver, so a bot's budget spans protocols.
// Configure it once before serving requests. Tests can install an isolated one.
type Runtime struct {
	Store       authratelimit.Store
	Recorder    ActivityRecorder
	defaults    map[Surface]int
	warnMu      sync.Mutex
	lastWarning time.Time
}

var runtimePointer atomic.Pointer[Runtime]

func SetRuntime(r *Runtime) *Runtime { return runtimePointer.Swap(r) }
func NewRuntime(store authratelimit.Store, recorder ActivityRecorder) *Runtime {
	r := &Runtime{Store: store, Recorder: recorder, defaults: map[Surface]int{}}
	for s, c := range map[Surface]struct {
		env string
		rpm int
	}{SurfaceMCP: {"EVS_MCP_RATE_LIMIT_RPM", 300}, SurfaceTelemetry: {"EVS_OTLP_RATE_LIMIT_RPM", 600}, SurfaceA2A: {"EVS_A2A_RATE_LIMIT_RPM", 60}} {
		n := c.rpm
		if raw, ok := os.LookupEnv(c.env); ok {
			if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
				n = v
			}
		}
		r.defaults[s] = n
	}
	return r
}

type processedKey struct{}
type processed struct {
	key     string
	surface Surface
}

// MarkProcessed is used only after successful admission or a signed REST hop.
func MarkProcessed(ctx context.Context, key string, s Surface) context.Context {
	return context.WithValue(ctx, processedKey{}, processed{key, s})
}
func Processed(ctx context.Context, key string, s Surface) bool {
	v, _ := ctx.Value(processedKey{}).(processed)
	return v.key == key && v.surface == s && key != ""
}

type rejectionKey struct{}

func RetryAfter(ctx context.Context) int { n, _ := ctx.Value(rejectionKey{}).(int); return n }

// Accept applies default and bot limits after authentication and surface checks.
// Store failure deliberately fails OPEN: telemetry and tools favor availability.
// It returns a private context decision, allowing existing authenticators to
// keep auth failure distinct from a rate limit without trusting client headers.
func Accept(ctx context.Context, key, tenant string, surfaces []string, s Surface, rpm *int) context.Context {
	// Anonymous self-hosted ingest has no safe shared abuse identity: a
	// tenant bucket would let anyone deny telemetry for the whole deployment.
	if key == "" {
		return ctx
	}
	if Processed(ctx, key, s) {
		return ctx
	}
	r := runtimePointer.Load()
	if r == nil {
		return ctx
	}
	consume := func(bucket string, n int) int {
		if n <= 0 || r.Store == nil {
			return 0
		}
		d, err := r.Store.Consume(ctx, bucket, authratelimit.Rule{Limit: n, Window: time.Minute})
		if err != nil {
			r.warnMu.Lock()
			if time.Since(r.lastWarning) >= time.Minute {
				slog.Warn("API key limiter unavailable; failing open")
				r.lastWarning = time.Now()
			}
			r.warnMu.Unlock()
			return 0
		}
		if !d.Allowed {
			return max(1, int(math.Ceil(d.RetryAfter.Seconds())))
		}
		return 0
	}
	identity := "key:" + key

	// The per-surface defaults apply to bot keys only. Ordinary keys had no
	// limiter on these surfaces before bots existed, and existing exporters
	// and MCP clients that share one key must not start seeing 429s.
	if surfaces != nil {
		if n := consume("surface:"+string(s)+":"+identity, r.defaults[s]); n > 0 {
			return context.WithValue(ctx, rejectionKey{}, n)
		}
	}
	if surfaces != nil && rpm != nil && key != "" {
		if n := consume("bot:"+key, *rpm); n > 0 {
			return context.WithValue(ctx, rejectionKey{}, n)
		}
	}
	if surfaces != nil && key != "" && r.Recorder != nil {
		r.Recorder.Record(key, tenant, s)
	}
	return MarkProcessed(ctx, key, s)
}
func WriteRateLimit(w http.ResponseWriter, ctx context.Context) bool {
	if n := RetryAfter(ctx); n > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(n))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return true
	}
	return false
}
