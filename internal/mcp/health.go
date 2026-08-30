package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// HealthUpdateFunc is called whenever a server's health status changes.
// serverID is the registry ID and status is the new health status.
type HealthUpdateFunc func(serverID string, status HealthStatus)

// HealthChecker periodically pings all registered MCP servers and updates
// their health status in the registry.
type HealthChecker struct {
	registry      *Registry
	interval      time.Duration
	timeout       time.Duration
	logger        *slog.Logger
	onHealthUpdate HealthUpdateFunc

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewHealthChecker creates a health checker for the given registry.
func NewHealthChecker(registry *Registry, interval, timeout time.Duration, logger *slog.Logger) *HealthChecker {
	if interval == 0 {
		interval = 30 * time.Second
	}
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthChecker{
		registry: registry,
		interval: interval,
		timeout:  timeout,
		logger:   logger.With("component", "mcp_health"),
	}
}

// SetOnHealthUpdate sets a callback that fires whenever a server's health
// status is updated by the background checker. This is typically used to
// persist health status to the database.
func (hc *HealthChecker) SetOnHealthUpdate(fn HealthUpdateFunc) {
	hc.onHealthUpdate = fn
}

// Start begins the background health check loop.
func (hc *HealthChecker) Start() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.cancel != nil {
		return // already running
	}

	ctx, cancel := context.WithCancel(context.Background())
	hc.cancel = cancel
	go hc.loop(ctx)
}

// Stop terminates the background health check loop.
func (hc *HealthChecker) Stop() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.cancel != nil {
		hc.cancel()
		hc.cancel = nil
	}
}

func (hc *HealthChecker) loop(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	// Run immediately on start
	hc.checkAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hc.checkAll(ctx)
		}
	}
}

func (hc *HealthChecker) checkAll(ctx context.Context) {
	entries := hc.registry.All()
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		go hc.checkOne(ctx, entry)
	}
}

func (hc *HealthChecker) checkOne(ctx context.Context, entry *ServerEntry) {
	checkCtx, cancel := context.WithTimeout(ctx, hc.timeout)
	defer cancel()

	err := entry.Client.Ping(checkCtx)
	if err != nil {
		hc.registry.UpdateHealth(entry.Config.ID, HealthStatusUnhealthy)
		if hc.onHealthUpdate != nil {
			hc.onHealthUpdate(entry.Config.ID, HealthStatusUnhealthy)
		}
		hc.logger.Warn("mcp server health check failed",
			"server_id", entry.Config.ID,
			"name", entry.Config.Name,
			"error", err,
		)
		return
	}
	hc.registry.UpdateHealth(entry.Config.ID, HealthStatusHealthy)
	if hc.onHealthUpdate != nil {
		hc.onHealthUpdate(entry.Config.ID, HealthStatusHealthy)
	}
}

// CheckServer performs an on-demand health check for a single server.
func (hc *HealthChecker) CheckServer(ctx context.Context, serverID string) (HealthStatus, error) {
	entry, ok := hc.registry.Get(serverID)
	if !ok {
		return HealthStatusUnknown, nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, hc.timeout)
	defer cancel()

	if err := entry.Client.Ping(checkCtx); err != nil {
		hc.registry.UpdateHealth(serverID, HealthStatusUnhealthy)
		return HealthStatusUnhealthy, err
	}
	hc.registry.UpdateHealth(serverID, HealthStatusHealthy)
	return HealthStatusHealthy, nil
}
