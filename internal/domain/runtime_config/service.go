package runtime_config

import (
	"context"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/events"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// Service provides runtime configuration with hot-reload support, scoped
// per tenant. The cache is lazy: tenants are loaded on first access and
// invalidated on RuntimeConfigUpdatedEvent / RuntimeConfigResetEvent.
//
// For self-hosted single-tenant deployments tenantID is typically the
// empty string (extracted from auth context) — that just becomes one
// more cache key, behaviour is unchanged.
type Service struct {
	repo     *Repository
	eventBus events.Bus

	mu      sync.RWMutex
	configs map[string]*FullRuntimeConfig // keyed by tenantID

	// Channel to signal when any tenant's config is reloaded
	reloadCh chan struct{}
}

// NewService creates a new runtime config service
func NewService(db *sqlx.DB, eventBus events.Bus) *Service {
	svc := &Service{
		repo:     NewRepository(db),
		eventBus: eventBus,
		configs:  make(map[string]*FullRuntimeConfig),
		reloadCh: make(chan struct{}, 1),
	}

	if eventBus != nil {
		eventBus.Subscribe(EventTypeRuntimeConfigUpdated, svc.handleConfigUpdated)
		eventBus.Subscribe(EventTypeRuntimeConfigReset, svc.handleConfigReset)
	}

	return svc
}

// Start is a no-op now that loads happen lazily per tenant. Kept for
// callers that still invoke it.
func (s *Service) Start(_ context.Context) error {
	return nil
}

// load fetches the latest config for a tenant from the database.
func (s *Service) load(ctx context.Context, tenantID string) *FullRuntimeConfig {
	cfg, err := s.repo.GetFullConfig(ctx, tenantID)
	if err != nil {
		logger.Warnf("runtime_config: failed to load tenant %q from database, using defaults: %v", tenantID, err)
		cfg = defaultFullConfig()
	}

	s.mu.Lock()
	s.configs[tenantID] = cfg
	s.mu.Unlock()

	select {
	case s.reloadCh <- struct{}{}:
	default:
	}

	return cfg
}

// invalidate drops a tenant's cached config so the next read reloads
// from the database. Used on update/reset events.
func (s *Service) invalidate(tenantID string) {
	s.mu.Lock()
	delete(s.configs, tenantID)
	s.mu.Unlock()

	select {
	case s.reloadCh <- struct{}{}:
	default:
	}
}

// handleConfigUpdated is called when a config update event is received.
// We invalidate that tenant's cache; the next read repopulates.
func (s *Service) handleConfigUpdated(_ context.Context, event interface{}) error {
	e, ok := event.(RuntimeConfigUpdatedEvent)
	if !ok {
		return nil
	}
	logger.Infof("runtime_config: update event tenant=%q section=%s version=%d", e.TenantID, e.Section, e.Version)
	s.invalidate(e.TenantID)
	return nil
}

// handleConfigReset is called when a config reset event is received.
func (s *Service) handleConfigReset(_ context.Context, event interface{}) error {
	e, ok := event.(RuntimeConfigResetEvent)
	if !ok {
		return nil
	}
	logger.Infof("runtime_config: reset event tenant=%q section=%s version=%d", e.TenantID, e.Section, e.Version)
	s.invalidate(e.TenantID)
	return nil
}

// GetConfig returns the cached runtime configuration for a tenant,
// loading from the database on first access. Falls back to defaults if
// the database is unreachable.
func (s *Service) GetConfig(tenantID string) *FullRuntimeConfig {
	s.mu.RLock()
	cfg, ok := s.configs[tenantID]
	s.mu.RUnlock()
	if ok {
		return cfg
	}
	return s.load(context.Background(), tenantID)
}

// GetFeatures returns the current features configuration for a tenant.
func (s *Service) GetFeatures(tenantID string) FeaturesConfig {
	return s.GetConfig(tenantID).Features
}

// GetRateLimit returns the current rate limit configuration for a tenant.
func (s *Service) GetRateLimit(tenantID string) RateLimitConfig {
	return s.GetConfig(tenantID).RateLimit
}

// GetLoadBalancer returns the current load balancer configuration for a tenant.
func (s *Service) GetLoadBalancer(tenantID string) LoadBalancerConfig {
	return s.GetConfig(tenantID).LoadBalancer
}

// GetCache returns the current cache configuration for a tenant.
func (s *Service) GetCache(tenantID string) CacheConfig {
	return s.GetConfig(tenantID).Cache
}

// GetTelemetry returns the current telemetry configuration for a tenant.
func (s *Service) GetTelemetry(tenantID string) TelemetryConfig {
	return s.GetConfig(tenantID).Telemetry
}

// GetCORS returns the current CORS configuration for a tenant.
func (s *Service) GetCORS(tenantID string) CORSConfig {
	return s.GetConfig(tenantID).CORS
}

// IsStreamingEnabled returns whether streaming is enabled for a tenant.
func (s *Service) IsStreamingEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableStreaming
}

// IsSSEEnabled returns whether SSE is enabled for a tenant.
func (s *Service) IsSSEEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableSSE
}

// IsEmbeddingsEnabled returns whether embeddings are enabled for a tenant.
func (s *Service) IsEmbeddingsEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableEmbeddings
}

// IsFunctionCallingEnabled returns whether function calling is enabled for a tenant.
func (s *Service) IsFunctionCallingEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableFunctionCalling
}

// IsResponseCachingEnabled returns whether response caching is enabled for a tenant.
func (s *Service) IsResponseCachingEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableResponseCaching
}

// IsRequestLoggingEnabled returns whether request logging is enabled for a tenant.
func (s *Service) IsRequestLoggingEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableRequestLogging
}

// IsHealthChecksEnabled returns whether health checks are enabled for a tenant.
func (s *Service) IsHealthChecksEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableHealthChecks
}

// IsAgentsEnabled returns whether agents are enabled for a tenant.
func (s *Service) IsAgentsEnabled(tenantID string) bool {
	return s.GetFeatures(tenantID).EnableAgents
}

// ReloadCh returns a channel that signals when any tenant's config is reloaded.
func (s *Service) ReloadCh() <-chan struct{} {
	return s.reloadCh
}

// SetEventBus sets the event bus (for late binding).
func (s *Service) SetEventBus(bus events.Bus) {
	s.eventBus = bus
	if bus != nil {
		bus.Subscribe(EventTypeRuntimeConfigUpdated, s.handleConfigUpdated)
		bus.Subscribe(EventTypeRuntimeConfigReset, s.handleConfigReset)
	}
}

// GetEventBus returns the event bus.
func (s *Service) GetEventBus() events.Bus {
	return s.eventBus
}

func defaultFullConfig() *FullRuntimeConfig {
	return &FullRuntimeConfig{
		RateLimit:    DefaultRateLimitConfig(),
		LoadBalancer: DefaultLoadBalancerConfig(),
		Features:     DefaultFeaturesConfig(),
		Cache:        DefaultCacheConfig(),
		Telemetry:    DefaultTelemetryConfig(),
		CORS:         DefaultCORSConfig(),
		UpdatedAt:    time.Now(),
		Version:      0,
	}
}
