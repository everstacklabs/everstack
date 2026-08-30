package runtime_config

import (
	"encoding/json"
	"time"
)

// SectionName represents a valid runtime configuration section
type SectionName string

const (
	SectionRateLimit    SectionName = "rate_limit"
	SectionLoadBalancer SectionName = "load_balancer"
	SectionFeatures     SectionName = "features"
	SectionCache        SectionName = "cache"
	SectionTelemetry    SectionName = "telemetry"
	SectionCORS         SectionName = "cors"
)

// ValidSections returns all valid section names
func ValidSections() []SectionName {
	return []SectionName{
		SectionRateLimit,
		SectionLoadBalancer,
		SectionFeatures,
		SectionCache,
		SectionTelemetry,
		SectionCORS,
	}
}

// IsValidSection checks if a section name is valid
func IsValidSection(section string) bool {
	for _, s := range ValidSections() {
		if string(s) == section {
			return true
		}
	}
	return false
}

// RuntimeConfigSection represents a configuration section stored in the database
type RuntimeConfigSection struct {
	ID        string          `db:"id"`
	TenantID  string          `db:"tenant_id"`
	Section   string          `db:"section"`
	Config    json.RawMessage `db:"config"`
	Version   int             `db:"version"`
	UpdatedBy *string         `db:"updated_by"`
	UpdatedAt time.Time       `db:"updated_at"`
	CreatedAt time.Time       `db:"created_at"`
}

// RateLimitConfig represents rate limiting configuration
// JSON tags use camelCase to match protobuf-es generated JSON
type RateLimitConfig struct {
	Enabled           bool   `json:"enabled"`
	RequestsPerMinute int    `json:"requestsPerMinute"`
	Burst             int    `json:"burst"`
	KeySource         string `json:"keySource"`
}

// LoadBalancerConfig represents load balancer configuration
// JSON tags use camelCase to match protobuf-es generated JSON
type LoadBalancerConfig struct {
	Enabled   bool            `json:"enabled"`
	Strategy  string          `json:"strategy"`
	KeySource string          `json:"keySource"`
	Fallback  *FallbackConfig `json:"fallback,omitempty"`
}

// FallbackConfig configures cross-provider fallback behavior
type FallbackConfig struct {
	Enabled bool                   `json:"enabled"`
	Default *FallbackModelConfig   `json:"default,omitempty"`
	Factors []FallbackFactorConfig `json:"factors,omitempty"`
}

// FallbackModelConfig represents a single fallback model target
type FallbackModelConfig struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

// FallbackFactorConfig represents a fallback strategy factor
type FallbackFactorConfig struct {
	Name        string                `json:"name"`
	Priority    int                   `json:"priority,omitempty"`
	Strategy    string                `json:"strategy"` // "priority", "round_robin", "parallel"
	Models      []FallbackModelConfig `json:"models,omitempty"`
	TimeoutMs   int                   `json:"timeoutMs,omitempty"`
	BackoffMs   int                   `json:"backoffMs,omitempty"`
	MaxAttempts int                   `json:"maxAttempts,omitempty"`
}

// FeaturesConfig represents feature flags configuration.
// JSON tags use camelCase to match protobuf-es generated JSON.
//
// The nested sub-blocks below mirror what the runtime config UI
// writes; the Go service deserializes them so consumers (sandbox
// manager, memory init, function executor, MCP routes, fastpath
// engine) can read per-tenant overrides at request time. Pointer
// types let us distinguish "not set" from "set to zero".
type FeaturesConfig struct {
	EnableStreaming       bool `json:"enableStreaming"`
	EnableEmbeddings      bool `json:"enableEmbeddings"`
	EnableFunctionCalling bool `json:"enableFunctionCalling"`
	EnableResponseCaching bool `json:"enableResponseCaching"`
	EnableSSE             bool `json:"enableSse"`
	EnableRequestLogging  bool `json:"enableRequestLogging"`
	EnableHealthChecks    bool `json:"enableHealthChecks"`
	EnableAgents          bool `json:"enableAgents"`

	EnableMemory      bool                   `json:"enableMemory,omitempty"`
	Memory            *MemoryFeatureConfig   `json:"memory,omitempty"`
	Sandbox           *SandboxFeatureConfig  `json:"sandbox,omitempty"`
	IsolatedFunctions *FunctionFeatureConfig `json:"isolatedFunctions,omitempty"`
	MCPGateway        *MCPGatewayConfig      `json:"mcpGateway,omitempty"`
	FastPath          *FastPathFeatureConfig `json:"fastpath,omitempty"`
}

// MemoryFeatureConfig holds the agents memory-defaults shape from
// the dashboard. Backend selection (pgvector/qdrant/...) stays
// deployment-time — the running gateway pod has one connection per
// backend type. Per-tenant defaults are the embedding model and the
// memory enabled flag, which is what callers can sensibly override.
type MemoryFeatureConfig struct {
	Backend         string                  `json:"backend,omitempty"`
	EmbeddingModels []MemoryEmbeddingModel  `json:"embeddingModels,omitempty"`
}

type MemoryEmbeddingModel struct {
	Model     string `json:"model,omitempty"`
	Dimension int    `json:"dimension,omitempty"`
}

// SandboxFeatureConfig holds per-tenant resource caps. Backend choice
// (firecracker/k8s/docker) is deployment-time — can't safely vary per
// tenant on the same gateway pod.
type SandboxFeatureConfig struct {
	MaxCpu            float64 `json:"maxCpu,omitempty"`
	MaxMemoryMb       int     `json:"maxMemoryMb,omitempty"`
	MaxDiskMb         int     `json:"maxDiskMb,omitempty"`
	MaxTimeoutSeconds int     `json:"maxTimeoutSeconds,omitempty"`
}

// FunctionFeatureConfig holds per-tenant defaults for serverless
// functions. Image prefix, docker host, fallback hosts are
// deployment-time (registry / docker socket choices live with the
// operator). Default timeout/memory are reasonable per-tenant knobs.
type FunctionFeatureConfig struct {
	DefaultTimeoutMs int `json:"defaultTimeoutMs,omitempty"`
	DefaultMemoryMb  int `json:"defaultMemoryMb,omitempty"`
}

// MCPGatewayConfig — flat enabled flag is the only piece that drives
// behaviour today.
type MCPGatewayConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

// FastPathFeatureConfig is structurally a parallel of validator's
// fastpath block but kept loose: only the top-level enabled flag has
// a clean per-tenant semantic. The bloom-filter sizes and cache
// max-entries are global engine state — wiring them per-tenant would
// undermine the perf model. Form fields for those are read-only.
type FastPathFeatureConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

// CacheConfig represents cache configuration
// JSON tags use camelCase to match protobuf-es generated JSON
type CacheConfig struct {
	Enabled       bool   `json:"enabled"`
	Type          string `json:"type"`
	TTL           string `json:"ttl"`
	MemoryMaxSize int    `json:"memoryMaxSize,omitempty"`
	RedisAddress  string `json:"redisAddress,omitempty"`
	RedisDB       int    `json:"redisDb,omitempty"`
	RedisPoolSize int    `json:"redisPoolSize,omitempty"`
}

// TelemetryConfig represents telemetry/OTEL configuration
// JSON tags use camelCase to match protobuf-es generated JSON
type TelemetryConfig struct {
	Enabled            bool    `json:"enabled"`
	SamplingRate       float64 `json:"samplingRate"`
	Granularity        string  `json:"granularity"`
	TraceProviderCalls bool    `json:"traceProviderCalls"`
	TraceStreamChunks  bool    `json:"traceStreamChunks"`
	TraceFallbacks     bool    `json:"traceFallbacks"`
	CollectorURL       string  `json:"collectorUrl,omitempty"`
	ServiceName        string  `json:"serviceName,omitempty"`
}

// CORSConfig represents CORS configuration
// JSON tags use camelCase to match protobuf-es generated JSON
type CORSConfig struct {
	Enabled          bool     `json:"enabled"`
	AllowedOrigins   []string `json:"allowedOrigins"`
	AllowedMethods   []string `json:"allowedMethods"`
	AllowedHeaders   []string `json:"allowedHeaders"`
	ExposedHeaders   []string `json:"exposedHeaders,omitempty"`
	AllowCredentials bool     `json:"allowCredentials"`
	MaxAge           string   `json:"maxAge,omitempty"`
}

// FullRuntimeConfig represents the complete runtime configuration
type FullRuntimeConfig struct {
	RateLimit    RateLimitConfig    `json:"rate_limit"`
	LoadBalancer LoadBalancerConfig `json:"load_balancer"`
	Features     FeaturesConfig     `json:"features"`
	Cache        CacheConfig        `json:"cache"`
	Telemetry    TelemetryConfig    `json:"telemetry"`
	CORS         CORSConfig         `json:"cors"`
	UpdatedAt    time.Time          `json:"updated_at"`
	Version      int                `json:"version"`
}

// DefaultRateLimitConfig returns the default rate limit configuration
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 500,
		Burst:             100,
		KeySource:         "correlation",
	}
}

// DefaultLoadBalancerConfig returns the default load balancer configuration
func DefaultLoadBalancerConfig() LoadBalancerConfig {
	return LoadBalancerConfig{
		Enabled:   true,
		Strategy:  "round_robin",
		KeySource: "correlation",
	}
}

// DefaultFeaturesConfig returns the default features configuration
func DefaultFeaturesConfig() FeaturesConfig {
	// Defaults must be "feature available" — the runtime gates added in
	// this round (agents path gate, SSE middleware) consult these
	// values, and a tenant who has never touched the dashboard should
	// see the same behaviour they had before the gates existed.
	// Disabling has to be an explicit user action, not the default.
	return FeaturesConfig{
		EnableStreaming:       true,
		EnableEmbeddings:      true,
		EnableFunctionCalling: true,
		EnableResponseCaching: true,
		EnableSSE:             true,
		EnableRequestLogging:  true,
		EnableHealthChecks:    true,
		EnableAgents:          true,
	}
}

// DefaultCacheConfig returns the default cache configuration
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Enabled:       true,
		Type:          "memory",
		TTL:           "10m",
		MemoryMaxSize: 50000,
		RedisAddress:  "",
		RedisDB:       0,
		RedisPoolSize: 100,
	}
}

// DefaultTelemetryConfig returns the default telemetry configuration
func DefaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{
		Enabled:            false,
		SamplingRate:       1.0,
		Granularity:        "standard",
		TraceProviderCalls: true,
		TraceStreamChunks:  false,
		TraceFallbacks:     true,
		CollectorURL:       "localhost:4317",
		ServiceName:        "everstack-gateway",
	}
}

// DefaultCORSConfig returns the default CORS configuration
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{},
		AllowCredentials: true,
		MaxAge:           "",
	}
}

// GetDefaultConfig returns the default configuration for a section
func GetDefaultConfig(section SectionName) (interface{}, error) {
	switch section {
	case SectionRateLimit:
		return DefaultRateLimitConfig(), nil
	case SectionLoadBalancer:
		return DefaultLoadBalancerConfig(), nil
	case SectionFeatures:
		return DefaultFeaturesConfig(), nil
	case SectionCache:
		return DefaultCacheConfig(), nil
	case SectionTelemetry:
		return DefaultTelemetryConfig(), nil
	case SectionCORS:
		return DefaultCORSConfig(), nil
	default:
		return nil, ErrInvalidSection
	}
}
