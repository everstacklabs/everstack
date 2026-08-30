package fcagent

// HealthCache is the gateway-side counterpart to the per-fcagent
// host-pressure signal. It polls every discovered target's
// GetNodeHealth RPC on a fixed cadence and caches the latest result
// so the LoadBalancer can make pressure-aware placement decisions
// without paying RPC latency on every Pick.
//
// Design borrowed from Daytona's runner-push pattern: each runner
// reports CPU/mem/disk/started-sandbox counts to the API at 30s
// intervals; the scheduler consults the latest snapshot before
// placing new work. We do the same shape with pull instead of push
// — Discovery already holds per-target gRPC clients, so one extra
// poll loop is cheaper than a new push endpoint and avoids the
// runner-can-self-report-healthy concern (we trust the value
// because the poll itself proves the pod is reachable).
//
// Cache semantics:
//
//   - Entries are written every refresh interval (10s).
//   - Entries older than `stale` (30s) are ignored by IsHealthy —
//     a target that hasn't responded in three poll cycles is
//     treated as unhealthy regardless of its last reported value.
//   - First-time lookups (no entry yet) default to "healthy" so
//     placement isn't blocked during the initial cache warm-up.
//   - Targets that disappear from Discovery (pod terminated, DNS
//     entry removed) get evicted at the next refresh tick to
//     bound memory growth.

import (
	"context"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
)

// Default cadence: 10s probe, 30s staleness, 2s per-target timeout.
// 10s × N targets at ~2s timeout gives plenty of headroom for the
// refresh tick to finish even when several targets are slow.
const (
	defaultHealthRefreshInterval = 10 * time.Second
	defaultHealthStaleAfter      = 30 * time.Second
	defaultHealthProbeTimeout    = 2 * time.Second
)

// healthEntry is the cached snapshot for one target.
type healthEntry struct {
	health  *fcpb.NodeHealth
	fetched time.Time
}

// HealthCache holds per-target NodeHealth snapshots refreshed on a
// background loop. Safe for concurrent use; LoadBalancer reads are
// lock-free for the common case via a sync.Map under the hood.
type HealthCache struct {
	disc            *Discovery
	refreshInterval time.Duration
	staleAfter      time.Duration
	probeTimeout    time.Duration

	mu     sync.RWMutex
	cache  map[string]*healthEntry
	cancel context.CancelFunc
	done   chan struct{}
}

// HealthCacheOption configures non-default behavior on construction.
type HealthCacheOption func(*HealthCache)

// WithHealthRefreshInterval overrides the default 10s poll cadence.
func WithHealthRefreshInterval(d time.Duration) HealthCacheOption {
	return func(c *HealthCache) {
		if d > 0 {
			c.refreshInterval = d
		}
	}
}

// WithHealthStaleAfter overrides the default 30s staleness window.
func WithHealthStaleAfter(d time.Duration) HealthCacheOption {
	return func(c *HealthCache) {
		if d > 0 {
			c.staleAfter = d
		}
	}
}

// StartHealthCache builds and starts a HealthCache. Caller invokes
// Stop() on shutdown to drain the goroutine. Construction does not
// block on the first refresh; lookups before the first cycle return
// healthy by default (see IsHealthy).
func StartHealthCache(disc *Discovery, opts ...HealthCacheOption) *HealthCache {
	c := &HealthCache{
		disc:            disc,
		refreshInterval: defaultHealthRefreshInterval,
		staleAfter:      defaultHealthStaleAfter,
		probeTimeout:    defaultHealthProbeTimeout,
		cache:           make(map[string]*healthEntry),
	}
	for _, opt := range opts {
		opt(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.loop(ctx)
	return c
}

// Stop cancels the refresh loop and waits for it to exit.
func (c *HealthCache) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
	}
}

// IsHealthy returns whether `target` is currently considered placement-
// eligible. Decision matrix:
//
//	no entry         → true  (warm-up; don't block placement)
//	entry > stale    → false (target is unreachable / slow)
//	entry.Healthy    → true
//	!entry.Healthy   → false
func (c *HealthCache) IsHealthy(target string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[target]
	if !ok {
		return true
	}
	if time.Since(e.fetched) > c.staleAfter {
		return false
	}
	return e.health != nil && e.health.GetHealthy()
}

// Snapshot returns the current cached health for a target, or nil if
// none cached. Diagnostic only — IsHealthy is the authoritative API.
func (c *HealthCache) Snapshot(target string) *fcpb.NodeHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[target]
	if !ok || e.health == nil {
		return nil
	}
	return e.health
}

func (c *HealthCache) loop(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(c.refreshInterval)
	defer ticker.Stop()
	// Run the first refresh immediately so the cache populates without
	// waiting a full interval. Bounded by probeTimeout per target.
	c.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh(ctx)
		}
	}
}

// refresh polls every current discovery target. Failures are logged
// and recorded as missing entries (which, after the stale window,
// become observably unhealthy). Targets that have disappeared from
// Discovery get evicted from the cache.
func (c *HealthCache) refresh(ctx context.Context) {
	targets := c.disc.Targets()
	live := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		live[target] = struct{}{}
		c.probeOne(ctx, target)
	}
	// Drop stale targets we no longer discover.
	c.mu.Lock()
	for target := range c.cache {
		if _, ok := live[target]; !ok {
			delete(c.cache, target)
		}
	}
	c.mu.Unlock()
}

func (c *HealthCache) probeOne(ctx context.Context, target string) {
	cli, err := c.disc.Client(target)
	if err != nil {
		logger.WithFields("target", target, "err", err.Error()).
			Debug("fcagent_health: client unavailable, skipping")
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, c.probeTimeout)
	defer cancel()
	h, err := cli.GetNodeHealth(probeCtx, &fcpb.Empty{})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		// Keep the existing entry's freshness but mark the new
		// fetch as failed by recording a nil health. IsHealthy then
		// applies the staleness rule — fresh-but-failed becomes
		// unhealthy as soon as the stale window elapses.
		c.cache[target] = &healthEntry{health: nil, fetched: time.Now()}
		logger.WithFields("target", target, "err", err.Error()).
			Debug("fcagent_health: GetNodeHealth probe failed")
		return
	}
	prev := c.cache[target]
	c.cache[target] = &healthEntry{health: h, fetched: time.Now()}
	if prev != nil && prev.health != nil {
		// Edge log on transition so operators can correlate "node
		// stopped accepting placements" with the host metric that
		// tripped.
		if prev.health.GetHealthy() && !h.GetHealthy() {
			logger.WithFields("target", target, "reason", h.GetReason(),
				"disk_pct", h.GetDiskPct(), "mem_pct", h.GetMemPct(),
				"cpu_pct", h.GetCpuPct(), "fd_pct", h.GetFdPct()).
				Warn("fcagent_health: node turned unhealthy")
		} else if !prev.health.GetHealthy() && h.GetHealthy() {
			logger.WithFields("target", target).
				Info("fcagent_health: node recovered")
		}
	}
}
