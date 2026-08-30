package validator

import "time"

// FeaturesConfig mirrors the `features` section of gateway.yaml for flagging
// optional capabilities at runtime.
type FeaturesConfig struct {
	Server            ServerFeaturesConfig            `mapstructure:"server"`
	Gateway           GatewayFeaturesConfig           `mapstructure:"gateway"`
	FastPath          FastPathFeaturesConfig          `mapstructure:"fastpath"`
	IsolatedFunctions IsolatedFunctionsFeaturesConfig `mapstructure:"isolated_functions"`

	// Edge feature distribution
	Edge EdgeFeaturesConfig `mapstructure:"edge"`

	// Memory (vector store) feature flag and configuration
	EnableMemory bool                 `mapstructure:"enable_memory"`
	Memory       MemoryFeaturesConfig `mapstructure:"memory"`

	// Voice clone, TTS, STT feature flag
	EnableVoice bool `mapstructure:"enable_voice"`

	// Sandbox backend configuration
	Sandbox SandboxFeaturesConfig `mapstructure:"sandbox"`

	// MCP gateway feature flag
	McpGateway McpGatewayFeaturesConfig `mapstructure:"mcp_gateway"`

	// Compact configures gateway-side context compaction for chat
	// completion requests. The middleware lives at
	// internal/lib/handlers/gateway/compact and decorates every
	// provider built through the factory. Defaults are sensible for
	// most chat workloads — see CompactFeaturesConfig.
	Compact CompactFeaturesConfig `mapstructure:"compact"`
}

// McpGatewayFeaturesConfig configures the MCP gateway feature.
type McpGatewayFeaturesConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// CompactFeaturesConfig mirrors the compact.Config knobs. Kept as its
// own struct here (rather than embedding compact.Config directly) so
// the validator package doesn't pull in handlers/gateway/compact and
// we keep the import graph one-way.
type CompactFeaturesConfig struct {
	Enabled             bool     `mapstructure:"enabled"`
	MaxContextTokens    int      `mapstructure:"max_context_tokens"`
	BackgroundThreshold float64  `mapstructure:"background_threshold"`
	AggressiveThreshold float64  `mapstructure:"aggressive_threshold"`
	EmergencyThreshold  float64  `mapstructure:"emergency_threshold"`
	SummarizationModel  string   `mapstructure:"summarization_model"`
	EnabledForProviders []string `mapstructure:"enabled_for_providers"`
}

// EdgeFeaturesConfig configures edge-distributed feature flags via Cloudflare KV
type EdgeFeaturesConfig struct {
	// URL is the edge endpoint URL (e.g., https://features.everstack.com)
	// If empty, edge features are disabled and the gateway relies on license refresh.
	URL string `mapstructure:"url"`

	// PollInterval is how often to fetch feature manifests from the edge.
	// Default: 60s
	PollInterval time.Duration `mapstructure:"poll_interval"`

	// CacheDir is where to cache feature manifests locally.
	// Default: ~/.everstack/
	CacheDir string `mapstructure:"cache_dir"`
}

// IsolatedFunctionsFeaturesConfig configures isolated function execution.
type IsolatedFunctionsFeaturesConfig struct {
	// Backend selects the isolated-function execution backend.
	// Options: "docker" (default), "none". "firecracker" is reserved but not implemented yet.
	Backend string `mapstructure:"backend"`

	// DockerHost is the Docker daemon socket (default: auto-detect)
	// Can also be set via DOCKER_HOST environment variable.
	// Examples: "unix:///var/run/docker.sock", "tcp://localhost:2375"
	DockerHost string `mapstructure:"docker_host"`

	// DockerAutoDetect enables automatic detection of Docker socket locations.
	// When true (default), tries common socket paths for Docker Desktop, OrbStack, Colima, etc.
	// Set to false to only use DockerHost or DOCKER_HOST env var.
	DockerAutoDetect *bool `mapstructure:"docker_auto_detect"`

	// DockerFallbackHosts is a list of additional Docker hosts to try if auto-detection fails.
	// Useful for custom setups or TCP connections.
	// Example: ["tcp://host.docker.internal:2375", "unix:///custom/docker.sock"]
	DockerFallbackHosts []string `mapstructure:"docker_fallback_hosts"`

	// ImagePrefix is the Docker image prefix for runtime images (default: ghcr.io/everstacklabs/runtime)
	ImagePrefix string `mapstructure:"image_prefix"`

	// DefaultTimeoutMs is the default execution timeout in milliseconds (default: 30000)
	DefaultTimeoutMs int `mapstructure:"default_timeout_ms"`

	// DefaultMemoryMb is the default memory limit in MB (default: 512)
	DefaultMemoryMb int `mapstructure:"default_memory_mb"`

	// Pool configures warm container pooling for improved performance.
	Pool ContainerPoolFeaturesConfig `mapstructure:"pool"`
}

// ContainerPoolFeaturesConfig configures warm container pooling.
type ContainerPoolFeaturesConfig struct {
	// Enabled enables warm container pooling (default: false for backward compatibility)
	// When enabled, containers are reused for multiple function executions.
	Enabled bool `mapstructure:"enabled"`

	// MinContainersPerRuntime is the minimum warm containers per runtime (default: 1)
	// Containers are pre-warmed at startup and maintained at this level.
	MinContainersPerRuntime int `mapstructure:"min_per_runtime"`

	// MaxContainersPerRuntime is the maximum containers per runtime (default: 10)
	// Limits resource usage under high load.
	MaxContainersPerRuntime int `mapstructure:"max_per_runtime"`

	// IdleTimeoutSeconds is how long a container can be idle before removal (default: 300)
	// Set to 0 to disable idle timeout (containers only removed on max uses).
	IdleTimeoutSeconds int `mapstructure:"idle_timeout_seconds"`

	// MaxUses is how many times a container can be reused before recycling (default: 100)
	// Set to 0 for unlimited reuse (containers only removed on idle timeout).
	MaxUses int `mapstructure:"max_uses"`

	// WarmupOnStart pre-creates minimum containers at startup (default: false)
	// When true, reduces cold start latency for first requests at the cost of
	// creating MinContainersPerRuntime * len(runtimes) Docker containers on startup.
	WarmupOnStart *bool `mapstructure:"warmup_on_start"`
}

// IsWarmupOnStartEnabled returns whether warmup on start is enabled (default: false)
func (c *ContainerPoolFeaturesConfig) IsWarmupOnStartEnabled() bool {
	if c.WarmupOnStart == nil {
		return false
	}
	return *c.WarmupOnStart
}

// IsDockerAutoDetectEnabled returns whether Docker auto-detection is enabled (default: true)
func (c *IsolatedFunctionsFeaturesConfig) IsDockerAutoDetectEnabled() bool {
	if c.DockerAutoDetect == nil {
		return true // Default to enabled
	}
	return *c.DockerAutoDetect
}

// AuthConfig configures authentication for the admin dashboard.
// This is a top-level configuration section.
type AuthConfig struct {
	// Mode controls the authentication method.
	// Options: "none" (default), "builtin", "oidc"
	Mode string `mapstructure:"mode"`

	// Builtin configures the built-in email/password authentication
	Builtin AuthBuiltinConfig `mapstructure:"builtin"`

	// OIDC configures OpenID Connect authentication
	OIDC AuthOIDCConfig `mapstructure:"oidc"`
}

// Enabled returns true if authentication is enabled (mode != "none")
func (c *AuthConfig) Enabled() bool {
	if c == nil {
		return false
	}
	return c.Mode != "" && c.Mode != "none"
}

// AuthBuiltinConfig configures built-in email/password + magic link authentication
type AuthBuiltinConfig struct {
	// SessionSecret is the secret used to sign session cookies.
	// If empty, a random secret is generated at startup (sessions won't persist across restarts).
	SessionSecret string `mapstructure:"session_secret"`

	// SessionMaxAge is the session duration in seconds. Default: 604800 (7 days)
	SessionMaxAge int `mapstructure:"session_max_age"`

	// SessionSecure sets the Secure flag on cookies. Default: true
	// Set to false for local development over HTTP (not HTTPS).
	SessionSecure *bool `mapstructure:"session_secure"`

	// SessionSameSite controls the cookie SameSite attribute.
	// Options: "lax" (default), "strict", "none".
	// Use "none" when the admin UI and API are on different origins and cookies
	// must be sent on cross-site requests.
	SessionSameSite string `mapstructure:"session_same_site"`

	// SeatLimit is the maximum number of users allowed (0 = unlimited).
	SeatLimit int `mapstructure:"seat_limit"`
}

// AuthOIDCConfig configures OpenID Connect authentication.
// Works with Keycloak, Auth0, Okta, Azure AD, Authelia, Authentik, and more.
type AuthOIDCConfig struct {
	// IssuerURL is the OIDC issuer URL (required)
	IssuerURL string `mapstructure:"issuer_url"`

	// ClientID is the OIDC client ID (required)
	ClientID string `mapstructure:"client_id"`

	// ClientSecret is the OIDC client secret (required)
	// Can also be set via EVS_AUTH_OIDC_CLIENT_SECRET env var
	ClientSecret string `mapstructure:"client_secret"`

	// RedirectURI is the OAuth callback URL (required)
	RedirectURI string `mapstructure:"redirect_uri"`

	// Scopes are the OIDC scopes to request. Default: ["openid", "profile", "email"]
	Scopes []string `mapstructure:"scopes"`

	// AdminClaim is the JWT claim containing roles/groups for admin check (optional)
	AdminClaim string `mapstructure:"admin_claim"`

	// AdminValue is the value in AdminClaim that grants admin access (optional)
	AdminValue string `mapstructure:"admin_value"`
}

// ServerFeaturesConfig captures server-level feature flags.
type ServerFeaturesConfig struct {
	EnableNewLoadBalancer   bool `mapstructure:"enable_new_load_balancer"`
	EnableExperimentalAPIV2 bool `mapstructure:"enable_experimental_api_v2"`
	EnableDebugEndpoints    bool `mapstructure:"enable_debug_endpoints"`
	EnableProfiling         bool `mapstructure:"enable_profiling"`
}

// GatewayFeaturesConfig captures gateway-level feature flags.
// We only map the ones we currently consume in code; more can be added later.
type GatewayFeaturesConfig struct {
	EnableFunctionCalling       bool `mapstructure:"enable_function_calling"`
	EnableEmbeddings            bool `mapstructure:"enable_embeddings"`
	EnableAgents                bool `mapstructure:"enable_agents"`
	EnableStreaming             bool `mapstructure:"enable_streaming"`
	EnableModelFineTuning       bool `mapstructure:"enable_model_fine_tuning"`
	EnableCustomModels          bool `mapstructure:"enable_custom_models"`
	EnableModelMetrics          bool `mapstructure:"enable_model_metrics"`
	EnableCostTracking          bool `mapstructure:"enable_cost_tracking"`
	EnableResponseCaching       bool `mapstructure:"enable_response_caching"`
	EnableRequestBatching       bool `mapstructure:"enable_request_batching"`
	EnableConnectionPooling     bool `mapstructure:"enable_connection_pooling"`
	EnableRequestLogging        bool `mapstructure:"enable_request_logging"`
	EnablePerformanceMonitoring bool `mapstructure:"enable_performance_monitoring"`
	EnableHealthChecks          bool `mapstructure:"enable_health_checks"`

	// Optional: whether to expose SSE formatting on top of streaming
	// This is intentionally duplicated here to keep all gateway feature
	// toggles under a single section in the YAML.
	EnableSSE bool `mapstructure:"enable_sse"`

	// Fast-path optimizations (HFT-inspired)
	EnableFastPath      bool `mapstructure:"enable_fastpath"`
	EnableExactCache    bool `mapstructure:"enable_exact_cache"`
	EnableSemanticCache bool `mapstructure:"enable_semantic_cache"`
	EnableBufferPooling bool `mapstructure:"enable_buffer_pooling"`
}

// MemoryFeaturesConfig configures the vector memory backend.
type MemoryFeaturesConfig struct {
	// Backend selects the vector store implementation.
	// Options: "pgvector" (default), "qdrant", "pinecone", "weaviate"
	Backend string `mapstructure:"backend"`

	// EmbeddingModels lists the available embedding models and their dimensions.
	// The first entry is used as the default when creating new collections.
	// Each model must be routable via the gateway's provider configuration.
	EmbeddingModels []EmbeddingModelConfig `mapstructure:"embedding_models"`

	// Qdrant backend configuration (only used when backend = "qdrant")
	Qdrant QdrantFeaturesConfig `mapstructure:"qdrant"`

	// Pinecone backend configuration (only used when backend = "pinecone")
	Pinecone PineconeFeaturesConfig `mapstructure:"pinecone"`

	// Weaviate backend configuration (only used when backend = "weaviate")
	Weaviate WeaviateFeaturesConfig `mapstructure:"weaviate"`
}

// DefaultEmbeddingModel returns the first configured embedding model (the default).
// Returns empty strings and 0 if no models are configured.
func (c *MemoryFeaturesConfig) DefaultEmbeddingModel() (model string, dimension int) {
	if len(c.EmbeddingModels) == 0 {
		return "", 0
	}
	return c.EmbeddingModels[0].Model, c.EmbeddingModels[0].Dimension
}

// EmbeddingModelConfig defines a single embedding model and its vector dimension.
type EmbeddingModelConfig struct {
	// Model is the model name (e.g., "text-embedding-3-small")
	Model string `mapstructure:"model"`

	// Dimension is the output vector dimension (e.g., 1536)
	Dimension int `mapstructure:"dimension"`
}

// QdrantFeaturesConfig configures the Qdrant vector store backend.
type QdrantFeaturesConfig struct {
	// Address is the Qdrant server address (e.g., "localhost:6334")
	Address string `mapstructure:"address"`

	// APIKey is the optional Qdrant API key for authentication
	APIKey string `mapstructure:"api_key"`
}

// PineconeFeaturesConfig configures the Pinecone vector store backend.
type PineconeFeaturesConfig struct {
	// APIKey is the Pinecone API key
	APIKey string `mapstructure:"api_key"`

	// Cloud is the cloud provider (e.g., "aws", "gcp", "azure"). Default: "aws"
	Cloud string `mapstructure:"cloud"`

	// Region is the cloud region (e.g., "us-east-1"). Default: "us-east-1"
	Region string `mapstructure:"region"`

	// Environment is the Pinecone environment (for pod-based indexes)
	Environment string `mapstructure:"environment"`
}

// WeaviateFeaturesConfig configures the Weaviate vector store backend.
type WeaviateFeaturesConfig struct {
	// URL is the Weaviate server URL (e.g., "http://localhost:8080")
	URL string `mapstructure:"url"`

	// APIKey is the optional Weaviate API key for authentication
	APIKey string `mapstructure:"api_key"`
}

// FastPathFeaturesConfig captures configuration for the fast-path engine.
// These settings control low-latency optimizations.
type FastPathFeaturesConfig struct {
	// Enabled enables/disables the fast-path engine globally
	Enabled bool `mapstructure:"enabled"`

	// Auth contains authentication cache configuration
	Auth FastPathAuthConfig `mapstructure:"auth"`

	// Cache contains response caching configuration
	Cache FastPathCacheConfig `mapstructure:"cache"`

	// Streaming contains streaming optimization configuration
	Streaming FastPathStreamingConfig `mapstructure:"streaming"`

	// ConnectionPool contains HTTP connection pool configuration
	ConnectionPool FastPathConnectionPoolConfig `mapstructure:"connection_pool"`
}

// FastPathAuthConfig configures the authentication fast-path.
type FastPathAuthConfig struct {
	// BloomFilterSize is the expected number of API keys for Bloom filter sizing
	BloomFilterSize uint `mapstructure:"bloom_filter_size"`
	// BloomFalsePositiveRate is the target false positive rate (0.001 = 0.1%)
	BloomFalsePositiveRate float64 `mapstructure:"bloom_false_positive_rate"`
	// CacheTTL is how long validated keys remain cached
	CacheTTL time.Duration `mapstructure:"cache_ttl"`
}

// FastPathCacheConfig configures response caching.
type FastPathCacheConfig struct {
	// Exact contains exact-match cache configuration
	Exact FastPathExactCacheConfig `mapstructure:"exact"`
	// Semantic contains semantic cache configuration
	Semantic FastPathSemanticCacheConfig `mapstructure:"semantic"`
}

// FastPathExactCacheConfig configures the exact-match response cache.
type FastPathExactCacheConfig struct {
	// Enabled enables/disables exact-match caching
	Enabled bool `mapstructure:"enabled"`
	// MaxEntries is the maximum number of cached responses
	MaxEntries int `mapstructure:"max_entries"`
	// TTL is how long responses remain valid
	TTL time.Duration `mapstructure:"ttl"`
}

// FastPathSemanticCacheConfig configures semantic similarity caching.
type FastPathSemanticCacheConfig struct {
	// Enabled enables/disables semantic caching
	Enabled bool `mapstructure:"enabled"`
	// MaxEntries is the maximum number of cached entries
	MaxEntries int `mapstructure:"max_entries"`
	// TTL is how long cached responses remain valid
	TTL time.Duration `mapstructure:"ttl"`
	// SimilarityThreshold is the minimum similarity score for cache hits (0.0-1.0)
	SimilarityThreshold float64 `mapstructure:"similarity_threshold"`
	// Algorithm is the similarity algorithm ("minhash" or "onnx")
	Algorithm string `mapstructure:"algorithm"`
	// NumHashes is the number of MinHash hash functions (more = more accurate, slower)
	NumHashes int `mapstructure:"num_hashes"`
	// ShingleSize is the n-gram size for MinHash (typically 2-4)
	ShingleSize int `mapstructure:"shingle_size"`
}

// FastPathStreamingConfig configures streaming optimizations.
type FastPathStreamingConfig struct {
	// BufferSize is the size of pooled buffers in bytes (default: 32KB)
	BufferSize int `mapstructure:"buffer_size"`
	// PoolSize is the number of buffers to pre-allocate
	PoolSize int `mapstructure:"pool_size"`
}

// FastPathConnectionPoolConfig configures HTTP connection pooling.
type FastPathConnectionPoolConfig struct {
	// MaxIdlePerHost is the maximum idle connections per provider
	MaxIdlePerHost int `mapstructure:"max_idle_per_host"`
	// PrewarmConnections is how many connections to pre-warm per provider
	PrewarmConnections int `mapstructure:"prewarm_connections"`
	// PrewarmOnStartup enables connection pre-warming at startup
	PrewarmOnStartup bool `mapstructure:"prewarm_on_startup"`
}

// SandboxFeaturesConfig configures the sandbox backend for agent code execution.
type SandboxFeaturesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Backend string `mapstructure:"backend"` // "docker", "kubernetes", or "firecracker" (runtime default varies by mode)

	Docker     SandboxDockerFeaturesConfig     `mapstructure:"docker"`
	Kubernetes SandboxKubernetesFeaturesConfig `mapstructure:"kubernetes"`
	Pricing    SandboxPricingConfig            `mapstructure:"pricing"`

	// Port exposure configuration
	PortExposure SandboxPortExposureConfig `mapstructure:"port_exposure"`

	// SSH proxy configuration
	SSH SandboxSSHConfig `mapstructure:"ssh"`

	// Firecracker backend pool tuning
	Firecracker SandboxFirecrackerConfig `mapstructure:"firecracker"`

	// FirecrackerAgent configures the gRPC proxy to remote Firecracker agents
	// (used when backend = "firecracker-agent"). The gateway discovers agents
	// via DNS of a headless K8s service and dispatches sandbox ops over gRPC.
	FirecrackerAgent SandboxFirecrackerAgentConfig `mapstructure:"firecracker_agent"`

	// Global resource limits (applied regardless of backend)
	DefaultImage            string   `mapstructure:"default_image"`
	AllowedImages           []string `mapstructure:"allowed_images"`
	MaxCPU                  float64  `mapstructure:"max_cpu"`
	MaxMemoryMB             int64    `mapstructure:"max_memory_mb"`
	MaxDiskMB               int64    `mapstructure:"max_disk_mb"`
	MaxTimeoutSecs          int      `mapstructure:"max_timeout_seconds"`
	DNSServers              []string `mapstructure:"dns_servers"`
	DefaultKeepWarmIdleSecs int      `mapstructure:"default_keep_warm_idle_seconds"`
	MaxConcurrentCreates    int      `mapstructure:"max_concurrent_creates"`
	MaxSandboxes            int      `mapstructure:"max_sandboxes"`
}

// SandboxPricingConfig defines compute pricing rates used for sandbox cost estimates/metering.
type SandboxPricingConfig struct {
	Enabled bool `mapstructure:"enabled"`

	// Currency is the display currency code (currently informational; costs are computed in USD).
	Currency string `mapstructure:"currency"`

	// Resource rates (USD/hour)
	CPUPerHourUSD         float64 `mapstructure:"cpu_per_hour_usd"`
	MemoryGBPerHourUSD    float64 `mapstructure:"memory_gb_per_hour_usd"`
	DiskGBPerHourUSD      float64 `mapstructure:"disk_gb_per_hour_usd"`
	PlatformFeePerHourUSD float64 `mapstructure:"platform_fee_per_hour_usd"`

	// Storage allowance + marginal overage tiering. The first
	// IncludedDiskGiB of a sandbox's root disk is free; disk up to
	// DiskTier2ThresholdGiB bills at DiskGBPerHourUSD, and disk beyond it
	// bills at that rate times DiskTier2Multiplier.
	IncludedDiskGiB       float64 `mapstructure:"included_disk_gib"`
	DiskTier2ThresholdGiB float64 `mapstructure:"disk_tier2_threshold_gib"`
	DiskTier2Multiplier   float64 `mapstructure:"disk_tier2_multiplier"`

	// Optional multipliers by tenant tier (free/basic/pro/enterprise).
	TierMultipliers map[string]float64 `mapstructure:"tier_multipliers"`
}

// SandboxFirecrackerConfig configures Firecracker-specific pool tuning.
type SandboxFirecrackerConfig struct {
	BinaryPath          string `mapstructure:"binary"` // Path to firecracker binary
	KernelPath          string `mapstructure:"kernel"` // Path to guest kernel image (vmlinux)
	RootfsDir           string `mapstructure:"rootfs_dir"`
	WorkDir             string `mapstructure:"work_dir"`
	PoolMinSize         int    `mapstructure:"pool_min_size"`
	PoolMaxSize         int    `mapstructure:"pool_max_size"`
	PoolMaxTotal        int    `mapstructure:"pool_max_total"`
	ReplenishIntervalMs int    `mapstructure:"replenish_interval_ms"`
	ReplenishBatch      int    `mapstructure:"replenish_batch"`
	WarmupOnStart       bool   `mapstructure:"warmup_on_start"`
}

// SandboxFirecrackerAgentConfig configures the gRPC firecracker-agent backend.
type SandboxFirecrackerAgentConfig struct {
	// Discovery strategy: "kubernetes" (DNS lookup of the headless service)
	// or "static" (treat Service as a comma-separated host:port list).
	Discovery string `mapstructure:"discovery"`

	// Service is the K8s headless service name for DNS discovery, or a
	// comma-separated list of host:port targets for static discovery.
	Service string `mapstructure:"service"`

	// Port is the gRPC port agents listen on (default 9090).
	Port int `mapstructure:"port"`

	// RefreshIntervalMs controls how often DNS is re-resolved (default 30000).
	RefreshIntervalMs int `mapstructure:"refresh_interval_ms"`

	// DialTimeoutMs is the per-dial timeout to an agent (default 5000).
	DialTimeoutMs int `mapstructure:"dial_timeout_ms"`

	// mTLS (optional — empty = plain gRPC)
	TLSClientCert string `mapstructure:"tls_client_cert"`
	TLSClientKey  string `mapstructure:"tls_client_key"`
	TLSServerCA   string `mapstructure:"tls_server_ca"`
	TLSServerName string `mapstructure:"tls_server_name"`
}

// SandboxSSHConfig configures the SSH proxy for sandbox access.
type SandboxSSHConfig struct {
	// ListenAddr is the address the SSH proxy listens on (e.g., ":2223").
	// Default: ":2222". Set to "disabled" to disable the SSH proxy.
	ListenAddr string `mapstructure:"listen_addr"`

	// Host is the hostname advertised to clients for SSH connections.
	// Default: "localhost".
	Host string `mapstructure:"host"`
}

// SandboxPortExposureConfig configures subdomain-based port exposure for sandboxes.
type SandboxPortExposureConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	BaseDomain          string `mapstructure:"base_domain"`           // e.g., "evs.run"
	ListenAddr          string `mapstructure:"listen_addr"`           // e.g., ":8443"
	MaxPortsPerSandbox  int    `mapstructure:"max_ports_per_sandbox"` // default: 5
	RequirePreviewToken bool   `mapstructure:"require_preview_token"` // require signed preview token for subdomain access

	// TLS configuration for the port exposure proxy
	TLS SandboxPortExposureTLS `mapstructure:"tls"`

	// CORS configuration for the port exposure proxy
	CORS SandboxPortExposureCORS `mapstructure:"cors"`

	// RequestTimeoutSecs is the per-request timeout in seconds (default: 120).
	// WebSocket upgrades are exempt from this timeout.
	RequestTimeoutSecs int `mapstructure:"request_timeout_seconds"`

	// MaxRequestBodyMB is the maximum request body size in MB (default: 50).
	// WebSocket upgrades are exempt from this limit.
	MaxRequestBodyMB int `mapstructure:"max_request_body_mb"`
}

// SandboxPortExposureTLS configures TLS for the sandbox port exposure proxy.
type SandboxPortExposureTLS struct {
	// Enabled enables TLS on the proxy listener.
	Enabled bool `mapstructure:"enabled"`

	// CertPath is the path to the TLS certificate (PEM). Used in manual mode.
	CertPath string `mapstructure:"cert_path"`

	// KeyPath is the path to the TLS private key (PEM). Used in manual mode.
	KeyPath string `mapstructure:"key_path"`

	// Autocert enables automatic certificate provisioning via Let's Encrypt (ACME HTTP-01).
	// When enabled, CertPath and KeyPath are ignored and a separate :80 listener is started.
	Autocert bool `mapstructure:"autocert"`

	// AutocertDir is the directory for caching autocert certificates.
	// Default: ~/.everstack/autocert
	AutocertDir string `mapstructure:"autocert_dir"`
}

// SandboxPortExposureCORS configures CORS for the sandbox port exposure proxy.
type SandboxPortExposureCORS struct {
	// Enabled enables CORS headers on proxy responses (default: true).
	Enabled *bool `mapstructure:"enabled"`

	// AllowedOrigins is the list of allowed origins. Default: ["*"].
	AllowedOrigins []string `mapstructure:"allowed_origins"`

	// AllowedMethods is the list of allowed HTTP methods.
	AllowedMethods []string `mapstructure:"allowed_methods"`

	// AllowedHeaders is the list of allowed request headers.
	AllowedHeaders []string `mapstructure:"allowed_headers"`

	// MaxAgeSecs is the preflight cache duration in seconds (default: 3600).
	MaxAgeSecs int `mapstructure:"max_age_seconds"`
}

// IsCORSEnabled returns whether CORS is enabled (default: true).
func (c *SandboxPortExposureCORS) IsCORSEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// SandboxDockerFeaturesConfig configures the Docker sandbox backend.
type SandboxDockerFeaturesConfig struct {
	Host      string `mapstructure:"host"`       // Docker socket (empty = auto-detect)
	AutoPull  *bool  `mapstructure:"auto_pull"`  // Pull images if not present (default: true)
	AutoBuild *bool  `mapstructure:"auto_build"` // Build from embedded Dockerfile if pull fails (default: true)
}

// IsAutoPullEnabled returns whether auto-pull is enabled (default: true).
func (c *SandboxDockerFeaturesConfig) IsAutoPullEnabled() bool {
	if c.AutoPull == nil {
		return true
	}
	return *c.AutoPull
}

// IsAutoBuildEnabled returns whether auto-build is enabled (default: true).
func (c *SandboxDockerFeaturesConfig) IsAutoBuildEnabled() bool {
	if c.AutoBuild == nil {
		return true
	}
	return *c.AutoBuild
}

// SandboxKubernetesFeaturesConfig configures the Kubernetes sandbox backend.
type SandboxKubernetesFeaturesConfig struct {
	Kubeconfig      string            `mapstructure:"kubeconfig"`        // Path to kubeconfig (empty = in-cluster)
	Namespace       string            `mapstructure:"namespace"`         // Default: "everstack-sandboxes"
	ImagePullPolicy string            `mapstructure:"image_pull_policy"` // Default: "IfNotPresent"
	ServiceAccount  string            `mapstructure:"service_account"`   // Default: "default"
	NodeSelector    map[string]string `mapstructure:"node_selector"`     // Optional node targeting
	// ImagePullSecrets are Secrets in Namespace used to pull private
	// container images (e.g. ghcr.io/<org>/sandbox:base when the
	// repo is private). Empty = anonymous pulls only.
	ImagePullSecrets []string `mapstructure:"image_pull_secrets"`
}

// DefaultFastPathFeaturesConfig returns sensible defaults for fast-path configuration.
func DefaultFastPathFeaturesConfig() FastPathFeaturesConfig {
	return FastPathFeaturesConfig{
		Enabled: true,
		Auth: FastPathAuthConfig{
			BloomFilterSize:        100000,
			BloomFalsePositiveRate: 0.001,
			CacheTTL:               60 * time.Second,
		},
		Cache: FastPathCacheConfig{
			Exact: FastPathExactCacheConfig{
				Enabled:    true,
				MaxEntries: 50000,
				TTL:        5 * time.Minute,
			},
			Semantic: FastPathSemanticCacheConfig{
				Enabled:             false, // Enable for semantic similarity caching
				MaxEntries:          10000,
				TTL:                 5 * time.Minute,
				SimilarityThreshold: 0.35, // 35% for MinHash near-duplicate detection
				Algorithm:           "minhash",
				NumHashes:           128,
				ShingleSize:         2, // 2-grams work better for short queries
			},
		},
		Streaming: FastPathStreamingConfig{
			BufferSize: 32 * 1024, // 32KB
			PoolSize:   1024,
		},
		ConnectionPool: FastPathConnectionPoolConfig{
			MaxIdlePerHost:     256,
			PrewarmConnections: 10,
			PrewarmOnStartup:   true,
		},
	}
}
