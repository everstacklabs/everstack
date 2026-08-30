package v1

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/functions/isolation/docker"
	functionsfcagent "github.com/everstacklabs/everstack/internal/functions/isolation/fcagent"
	firecrackeriso "github.com/everstacklabs/everstack/internal/functions/isolation/firecracker"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	_ "github.com/everstacklabs/everstack/internal/providers/anthropic"
	_ "github.com/everstacklabs/everstack/internal/providers/aws_bedrock"
	_ "github.com/everstacklabs/everstack/internal/providers/azure_openai"
	_ "github.com/everstacklabs/everstack/internal/providers/cerebras"
	_ "github.com/everstacklabs/everstack/internal/providers/cohere"
	_ "github.com/everstacklabs/everstack/internal/providers/deepseek"
	_ "github.com/everstacklabs/everstack/internal/providers/fireworks"
	_ "github.com/everstacklabs/everstack/internal/providers/google"
	_ "github.com/everstacklabs/everstack/internal/providers/groq"
	_ "github.com/everstacklabs/everstack/internal/providers/huggingface"
	_ "github.com/everstacklabs/everstack/internal/providers/minimax"
	_ "github.com/everstacklabs/everstack/internal/providers/mistral"
	_ "github.com/everstacklabs/everstack/internal/providers/moonshot"
	_ "github.com/everstacklabs/everstack/internal/providers/nvidia_nim"
	_ "github.com/everstacklabs/everstack/internal/providers/ollama"
	_ "github.com/everstacklabs/everstack/internal/providers/openai"
	_ "github.com/everstacklabs/everstack/internal/providers/openrouter"
	_ "github.com/everstacklabs/everstack/internal/providers/perplexity"
	_ "github.com/everstacklabs/everstack/internal/providers/qwen"
	_ "github.com/everstacklabs/everstack/internal/providers/together"
	_ "github.com/everstacklabs/everstack/internal/providers/vertex_ai"
	_ "github.com/everstacklabs/everstack/internal/providers/voyage"
	_ "github.com/everstacklabs/everstack/internal/providers/xai"
	_ "github.com/everstacklabs/everstack/internal/providers/zai"
	sandboxfcagent "github.com/everstacklabs/everstack/internal/sandbox/fcagent"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
	"github.com/everstacklabs/everstack/internal/telemetry"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	gatewayconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ gatewayconnect.GatewayServiceHandler = (*Server)(nil)

// Also expose a classic gRPC wrapper
var _ gatewaypb.GatewayServiceServer = (*GrpcServer)(nil)

type Server struct {
	cfg  *validator.GatewayConfig
	feat *validator.FeaturesConfig
	// Per-tenant provider bundles. In shared multi-tenant mode every
	// request resolves its own bundle via providersFor(ctx) so concurrent
	// requests from different tenants never share registry/router state.
	// In single-tenant mode the defaultBundle pointer holds the one bundle
	// configured at boot. See tenant_providers.go for the rationale —
	// before this split, all tenants shared a single Registry/Router on
	// the Server and the LLM-key cross-tenant leak rode that race.
	tenantBundles  tenantBundleCache
	defaultBundle  atomic.Pointer[tenantBundle]
	ctx            context.Context // Context containing CQRS system
	failedKeys     map[string]bool // Track temporarily failed API keys (keyID -> failed)
	failedKeyMutex sync.RWMutex    // Mutex for failedKeys map

	// Cache for default model to avoid DB queries on every request
	defaultModelCache      string
	defaultModelCacheMutex sync.RWMutex

	// OpenTelemetry logger provider (nil if OTEL is disabled)
	otelProvider *sdklog.LoggerProvider
	// Cached OTEL logger (created once, reused for all requests)
	otelLogger log.Logger

	// Tool loop manager for serverless function execution
	toolLoop *toolloop.LoopManager

	// Voice clone profile repository for resolving profile IDs to provider voice IDs
	voiceCloneRepo voice_clone.Repository

	mu sync.RWMutex
}

const providerRefreshStaleness = 15 * time.Second

// contextKey is a private type for context keys in this package.
type contextKey string

const stickyKeyContextKey contextKey = "everstack/sticky-key"

// GrpcServer wraps the Connect-backed Server to implement the classic gRPC service.
type GrpcServer struct {
	gatewaypb.UnimplementedGatewayServiceServer
	base *Server
}

// CreateServer builds the Connect handler server
func CreateServer(cfg *validator.GatewayConfig, features *validator.FeaturesConfig) *Server {
	s := &Server{
		cfg:        cfg,
		feat:       features,
		failedKeys: make(map[string]bool),
	}
	s.bootstrapFromConfig()
	return s
}

// CreateServerWithContext builds the Connect handler server with context
func CreateServerWithContext(ctx context.Context, cfg *validator.GatewayConfig, features *validator.FeaturesConfig) *Server {
	otelProvider := telemetry.GetLoggerProvider(ctx) // Extract OTEL provider from context (nil if not enabled)
	var otelLogger log.Logger
	if otelProvider != nil {
		otelLogger = otelProvider.Logger("everstack-gateway")
	}

	// Initialize tool loop manager if DB is available
	var toolLoopMgr *toolloop.LoopManager
	if dbAny := ctx.Value(contextkeys.PrimaryDB); dbAny != nil {
		if db, ok := dbAny.(*sqlx.DB); ok {
			// Check if tool loop is enabled via context or features
			toolLoopEnabled := true // Enabled by default if DB is available
			if enabledAny := ctx.Value(contextkeys.ToolLoopEnabled); enabledAny != nil {
				if enabled, ok := enabledAny.(bool); ok {
					toolLoopEnabled = enabled
				}
			}

			// Initialize isolation backend for isolated functions.
			// Docker remains the only implementation today; backend selection is explicit
			// so cloud configs can disable this path predictably.
			var isolationBackend isolation.Backend
			var backendMgr *docker.BackendManager

			// Default the functions backend to whatever can actually run
			// on this topology. Functions share the sandbox fleet: when
			// sandboxes use the remote firecracker-agent (the VPS/managed
			// case — gateway pod has no Docker daemon and no KVM),
			// defaulting to Docker would silently disable functions, so
			// inherit firecracker-agent. Explicit config/env still wins.
			isolationBackendType := "docker"
			if features != nil {
				sandboxBackend := strings.ToLower(strings.TrimSpace(features.Sandbox.Backend))
				if sandboxBackend == "firecracker-agent" || strings.TrimSpace(features.Sandbox.FirecrackerAgent.Service) != "" {
					isolationBackendType = "firecracker-agent"
				}
			}
			if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_BACKEND")); v != "" {
				isolationBackendType = strings.ToLower(v)
			}
			if features != nil && strings.TrimSpace(features.IsolatedFunctions.Backend) != "" {
				isolationBackendType = strings.ToLower(strings.TrimSpace(features.IsolatedFunctions.Backend))
			}

			switch isolationBackendType {
			case "", "docker":
				dockerCfg := docker.DefaultConfig()
				if features != nil {
					if features.IsolatedFunctions.DockerHost != "" {
						dockerCfg.Host = features.IsolatedFunctions.DockerHost
					}
					dockerCfg.AutoDetect = features.IsolatedFunctions.IsDockerAutoDetectEnabled()
					if len(features.IsolatedFunctions.DockerFallbackHosts) > 0 {
						dockerCfg.FallbackHosts = features.IsolatedFunctions.DockerFallbackHosts
					}
					if features.IsolatedFunctions.ImagePrefix != "" {
						dockerCfg.ImagePrefix = features.IsolatedFunctions.ImagePrefix
					}
					if features.IsolatedFunctions.DefaultTimeoutMs > 0 {
						dockerCfg.DefaultTimeoutMS = features.IsolatedFunctions.DefaultTimeoutMs
					}
					if features.IsolatedFunctions.DefaultMemoryMb > 0 {
						dockerCfg.DefaultMemoryMB = features.IsolatedFunctions.DefaultMemoryMb
					}
					// Per-tenant default override. Late-binds against the
					// runtime config service in ctx — works whether or
					// not rtconfig is wired at server-construction time.
					capturedCtx := ctx
					dockerCfg.TenantDefaults = func(tenantID string) (int, int) {
						svc, ok := capturedCtx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service)
						if !ok || svc == nil {
							return 0, 0
						}
						f := svc.GetFeatures(tenantID)
						if f.IsolatedFunctions == nil {
							return 0, 0
						}
						return f.IsolatedFunctions.DefaultTimeoutMs, f.IsolatedFunctions.DefaultMemoryMb
					}

					// Apply container pool configuration
					poolCfg := features.IsolatedFunctions.Pool
					dockerCfg.Pool.Enabled = poolCfg.Enabled
					if poolCfg.MinContainersPerRuntime > 0 {
						dockerCfg.Pool.MinContainersPerRuntime = poolCfg.MinContainersPerRuntime
					}
					if poolCfg.MaxContainersPerRuntime > 0 {
						dockerCfg.Pool.MaxContainersPerRuntime = poolCfg.MaxContainersPerRuntime
					}
					if poolCfg.IdleTimeoutSeconds > 0 {
						dockerCfg.Pool.IdleTimeout = time.Duration(poolCfg.IdleTimeoutSeconds) * time.Second
					}
					if poolCfg.MaxUses > 0 {
						dockerCfg.Pool.MaxUses = poolCfg.MaxUses
					}
					dockerCfg.Pool.WarmupOnStart = poolCfg.IsWarmupOnStartEnabled()
				}

				dockerBackend, err := docker.New(dockerCfg)
				if err != nil {
					logger.WithFields("error", err.Error()).Warn("gateway: docker not available - isolated functions will be disabled")
				} else {
					isolationBackend = dockerBackend
					backendMgr = docker.NewBackendManager(dockerBackend)
					logger.Info("gateway: docker isolation backend initialized for isolated functions")
				}
			case "none", "disabled", "off":
				logger.Info("gateway: isolated functions backend disabled by config")
			case "firecracker":
				fcCfg := firecrackeriso.DefaultConfig()

				// Apply generic isolated defaults
				if features != nil {
					if features.IsolatedFunctions.DefaultTimeoutMs > 0 {
						fcCfg.DefaultTimeoutMS = features.IsolatedFunctions.DefaultTimeoutMs
					}
					if features.IsolatedFunctions.DefaultMemoryMb > 0 {
						fcCfg.DefaultMemoryMB = features.IsolatedFunctions.DefaultMemoryMb
					}
					capturedCtx := ctx
					fcCfg.TenantDefaults = func(tenantID string) (int, int) {
						svc, ok := capturedCtx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service)
						if !ok || svc == nil {
							return 0, 0
						}
						f := svc.GetFeatures(tenantID)
						if f.IsolatedFunctions == nil {
							return 0, 0
						}
						return f.IsolatedFunctions.DefaultTimeoutMs, f.IsolatedFunctions.DefaultMemoryMb
					}

					// Reuse Firecracker host-level settings from sandbox feature config.
					if features.Sandbox.Firecracker.BinaryPath != "" {
						fcCfg.Firecracker.BinaryPath = features.Sandbox.Firecracker.BinaryPath
					}
					if features.Sandbox.Firecracker.KernelPath != "" {
						fcCfg.Firecracker.KernelPath = features.Sandbox.Firecracker.KernelPath
					}
					if features.Sandbox.Firecracker.RootfsDir != "" {
						fcCfg.Firecracker.RootfsDir = features.Sandbox.Firecracker.RootfsDir
					}
					if features.Sandbox.Firecracker.WorkDir != "" {
						fcCfg.Firecracker.WorkDir = features.Sandbox.Firecracker.WorkDir
					}
					if len(features.Sandbox.DNSServers) > 0 {
						fcCfg.DNSServers = features.Sandbox.DNSServers
					}
				}

				// Env overrides mirror sandbox Firecracker config.
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_BINARY")); v != "" {
					fcCfg.Firecracker.BinaryPath = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_KERNEL")); v != "" {
					fcCfg.Firecracker.KernelPath = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_ROOTFS_DIR")); v != "" {
					fcCfg.Firecracker.RootfsDir = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_WORK_DIR")); v != "" {
					fcCfg.Firecracker.WorkDir = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FIRECRACKER_GUEST_WORKDIR")); v != "" {
					fcCfg.GuestWorkDir = v
				}

				// Per-runtime rootfs overrides. Values are image names without ".ext4".
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FIRECRACKER_ROOTFS_NODEJS20")); v != "" {
					fcCfg.RuntimeRootfs[isolation.RuntimeNodeJS20] = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FIRECRACKER_ROOTFS_DENO")); v != "" {
					fcCfg.RuntimeRootfs[isolation.RuntimeDeno] = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FIRECRACKER_ROOTFS_PYTHON3")); v != "" {
					fcCfg.RuntimeRootfs[isolation.RuntimePython3] = v
				}

				firecrackerBackend, err := firecrackeriso.New(fcCfg)
				if err != nil {
					logger.WithFields("error", err.Error()).Warn("gateway: firecracker not available - isolated functions will be disabled")
				} else {
					isolationBackend = firecrackerBackend
					logger.Info("gateway: firecracker isolation backend initialized for isolated functions")
				}
			case "firecracker-agent", "fcagent":
				// Remote-agent execution: the gateway has no KVM, so it
				// dials the firecracker-agent fleet (the SAME agents the
				// sandbox backend uses) and issues InvokeFunction. This is
				// the VPS/managed path — Docker and in-gateway Firecracker
				// both require host capabilities the gateway pod lacks.
				fcaCfg := functionsfcagent.Config{Config: isolation.DefaultConfig()}
				if features != nil {
					if features.IsolatedFunctions.DefaultTimeoutMs > 0 {
						fcaCfg.DefaultTimeoutMS = features.IsolatedFunctions.DefaultTimeoutMs
					}
					if features.IsolatedFunctions.DefaultMemoryMb > 0 {
						fcaCfg.DefaultMemoryMB = features.IsolatedFunctions.DefaultMemoryMb
					}
					capturedCtx := ctx
					fcaCfg.TenantDefaults = func(tenantID string) (int, int) {
						svc, ok := capturedCtx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service)
						if !ok || svc == nil {
							return 0, 0
						}
						f := svc.GetFeatures(tenantID)
						if f.IsolatedFunctions == nil {
							return 0, 0
						}
						return f.IsolatedFunctions.DefaultTimeoutMs, f.IsolatedFunctions.DefaultMemoryMb
					}

					// Reuse the sandbox firecracker-agent fleet address —
					// functions and sandboxes share the same KVM nodes.
					agent := features.Sandbox.FirecrackerAgent
					fcaCfg.Agent.Service = agent.Service
					fcaCfg.Agent.Port = agent.Port
					fcaCfg.Agent.RefreshInterval = time.Duration(agent.RefreshIntervalMs) * time.Millisecond
					fcaCfg.Agent.DialTimeout = time.Duration(agent.DialTimeoutMs) * time.Millisecond
					fcaCfg.Agent.TLS = sandboxfcagent.TLSConfig{
						ClientCert: agent.TLSClientCert,
						ClientKey:  agent.TLSClientKey,
						ServerCA:   agent.TLSServerCA,
						ServerName: agent.TLSServerName,
					}
				}

				// Env overrides. Default to the sandbox agent fleet vars so
				// a single service address configures both; a functions-
				// specific override wins if set.
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_SERVICE")); v != "" {
					fcaCfg.Agent.Service = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FCAGENT_SERVICE")); v != "" {
					fcaCfg.Agent.Service = v
				}
				if v := strings.TrimSpace(os.Getenv("EVS_ISOLATED_FUNCTIONS_FCAGENT_PORT")); v != "" {
					if p, perr := strconv.Atoi(v); perr == nil && p > 0 {
						fcaCfg.Agent.Port = p
					}
				}

				agentBackend, err := functionsfcagent.New(fcaCfg)
				if err != nil {
					logger.WithFields("error", err.Error()).Warn("gateway: firecracker-agent backend unavailable - isolated functions will be disabled")
				} else {
					isolationBackend = agentBackend
					logger.WithFields("service", fcaCfg.Agent.Service).
						Info("gateway: firecracker-agent isolation backend initialized for isolated functions")
				}
			default:
				logger.WithFields("backend", isolationBackendType).
					Warn("gateway: unknown isolated functions backend; isolated functions are disabled")
			}

			toolLoopMgr = toolloop.NewLoopManagerWithBackend(db, toolLoopEnabled, isolationBackend)
			// Set backend resolver for per-function Docker host targeting
			if backendMgr != nil {
				toolLoopMgr.SetBackendResolver(backendMgr)
			}

			// Start isolation backend
			if isolationBackend != nil {
				if err := toolLoopMgr.StartIsolationBackend(ctx); err != nil {
					logger.WithFields("error", err.Error()).Warn("gateway: failed to start isolation backend")
				}
			}

			if toolLoopMgr.IsEnabled() {
				logger.Info("gateway: tool loop manager initialized")
			}
		}
	}

	s := &Server{
		cfg:          cfg,
		feat:         features,
		ctx:          ctx,
		failedKeys:   make(map[string]bool),
		otelProvider: otelProvider,
		otelLogger:   otelLogger,
		toolLoop:     toolLoopMgr,
	}

	// Try to load providers from database first (if repos available in context)
	if providerRepoAny := ctx.Value(contextkeys.ProviderRepo); providerRepoAny != nil {
		if apiKeyRepoAny := ctx.Value(contextkeys.APIKeyRepo); apiKeyRepoAny != nil {
			providerRepo := providerRepoAny.(*provider_config.Repository)
			apiKeyRepo := apiKeyRepoAny.(provider_api_keys.Repository)

			// Seed when there are no usable provider configurations.
			// Catalog sync can create placeholder rows (is_active=false, enabled_models=[]),
			// so row count alone is not sufficient to decide.
			configs, err := providerRepo.List(ctx)
			if err == nil && !hasUsableProviderConfig(configs) {
				logger.Info("gateway: no usable provider configs found, seeding from YAML/catalog")
				if err := s.seedDatabaseFromYAML(ctx, providerRepo, apiKeyRepo); err != nil {
					logger.Warnf("gateway: failed to seed from YAML: %v", err)
				}
			}

			// Load from database (either existing or just seeded). At
			// boot we install as the default bundle — shared multi-tenant
			// requests will overwrite it per-tenant via
			// EnsureProvidersForRequest.
			bundle, err := s.bootstrapFromDatabase(ctx, providerRepo, apiKeyRepo)
			if err != nil {
				logger.Errorf("gateway: failed to load from database: %v", err)
				return s // Return with error, don't fall back to config
			}
			s.installBundle("", bundle)
			logger.Info("gateway: successfully loaded providers from database")

			// Subscribe to provider events for auto-refresh
			logger.Debug("gateway: attempting to subscribe to provider events")
			if err := s.subscribeToProviderEvents(); err != nil {
				logger.Warnf("gateway: failed to subscribe to provider events: %v", err)
			}

			return s
		}
	}

	// No DB available - log warning and use config as emergency fallback
	logger.Warn("gateway: no database available, using YAML config (not recommended for production)")
	s.bootstrapFromConfig()
	return s
}

// CreateClassicServer builds the classic gRPC server backed by the same implementation.
func CreateClassicServer(cfg *validator.GatewayConfig, features *validator.FeaturesConfig) gatewaypb.GatewayServiceServer {
	return &GrpcServer{base: CreateServer(cfg, features)}
}

func hasUsableProviderConfig(configs []*provider_config.Configuration) bool {
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		if cfg.IsActive && len(cfg.EnabledModels) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return gatewayconnect.NewGatewayServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return gatewaypb.File_everstack_gateway_v1_gateway_service_proto
}

func (s *Server) AppName() string { return gatewayconnect.GatewayServiceName }

func (s *Server) MethodPrefix() string { return gatewayconnect.GatewayServiceName }

// RegisterGateway allows the Gateway service to self-register its grpc-gateway handlers.
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return gatewaypb.RegisterGatewayServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

func (s *Server) defaultModel() string {
	// Check cache first (fast path)
	s.defaultModelCacheMutex.RLock()
	if s.defaultModelCache != "" {
		cached := s.defaultModelCache
		s.defaultModelCacheMutex.RUnlock()
		return cached
	}
	s.defaultModelCacheMutex.RUnlock()

	// Cache miss - query database
	var resolvedModel string
	if s.ctx != nil {
		if providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo); providerRepoAny != nil {
			providerRepo := providerRepoAny.(*provider_config.Repository)
			configs, err := providerRepo.List(s.ctx)
			if err == nil {
				logger.Debugf("gateway: checking %d providers for default model", len(configs))
				// Find provider marked as default
				for _, config := range configs {
					if !config.IsActive {
						logger.Debugf("gateway: skipping inactive provider: %s", config.ProviderName)
						continue
					}
					logger.Debugf("gateway: checking provider %s, custom_settings: %v", config.ProviderName, config.CustomSettings)
					if defaultVal, ok := config.CustomSettings["default"]; ok && defaultVal == "true" {
						logger.Debugf("gateway: found default provider: %s", config.ProviderName)

						// Determine which model to check
						var testModel string
						if alias, ok := config.CustomSettings["default_alias"]; ok && alias != "" {
							testModel = alias
						} else if len(config.EnabledModels) > 0 {
							testModel = config.EnabledModels[0]
						}

						// Check if this model can actually be routed (verifies provider is loaded)
						if testModel != "" && s.routerFor(s.ctx) != nil {
							_, _, err := s.routerFor(s.ctx).ResolveWithContext(s.ctx, testModel)
							if err != nil {
								logger.Warnf("gateway: default provider %s model %s cannot be routed (likely provider not loaded): %v, trying next provider", config.ProviderName, testModel, err)
								continue
							}
						}

						// Model is routable - use it
						resolvedModel = testModel
						logger.Debugf("gateway: resolved default model: %s", resolvedModel)
						break
					}
				}
				// No default found - use first active provider's first model that's actually loaded
				if resolvedModel == "" {
					logger.Warn("gateway: no loaded provider with default=true found, using first loaded provider")
					for _, config := range configs {
						if !config.IsActive || len(config.EnabledModels) == 0 {
							continue
						}

						testModel := config.EnabledModels[0]

						// Check if model can be routed (verifies provider is loaded)
						if s.routerFor(s.ctx) != nil {
							_, _, err := s.routerFor(s.ctx).ResolveWithContext(s.ctx, testModel)
							if err != nil {
								logger.Debugf("gateway: provider %s model %s cannot be routed, skipping", config.ProviderName, testModel)
								continue
							}
						}

						resolvedModel = testModel
						logger.Debugf("gateway: selected first loaded provider %s, model: %s", config.ProviderName, resolvedModel)
						break
					}
				}
			}
		}
	}

	// No hardcoded fallback - user must configure at least one provider
	if resolvedModel == "" {
		logger.Error("gateway: no default model configured - please configure at least one active provider with default=true")
		return "" // Return empty string - caller should handle this error condition
	}

	logger.Debugf("gateway: resolved default model from DB: %s", resolvedModel)

	// Update cache
	s.defaultModelCacheMutex.Lock()
	s.defaultModelCache = resolvedModel
	s.defaultModelCacheMutex.Unlock()

	return resolvedModel
}

// invalidateDefaultModelCache clears the cached default model
// Call this when provider configurations change (add/update/delete)
func (s *Server) invalidateDefaultModelCache() {
	s.defaultModelCacheMutex.Lock()
	s.defaultModelCache = ""
	s.defaultModelCacheMutex.Unlock()
	logger.Debug("gateway: default model cache invalidated")
}

// getCQRSSystem retrieves the CQRS system from context
func (s *Server) getCQRSSystem() (*cqrs.System, error) {
	return cqrs.GetSystemFromContext(s.ctx)
}

// emitModelNotFoundEvent emits an event when a requested model is not found/activated
func (s *Server) emitModelNotFoundEvent(ctx context.Context, requestedModel, userID, apiKeyHash, correlationID string) {
	sys, err := s.getCQRSSystem()
	if err != nil || sys == nil {
		return // CQRS is optional, silently skip
	}

	event := chat.EmitModelNotFoundEvent(ctx, requestedModel, userID, apiKeyHash, correlationID)

	// Persist event to database (ClickHouse/Postgres)
	if sys.Writer != nil {
		if err := sys.Writer.Append(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to persist model.not_found event")
		}
	}

	// Publish to event bus for in-memory handlers
	if sys.EventBus != nil {
		if err := sys.EventBus.Publish(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to publish model.not_found event")
		} else {
			logger.WithFields("requested_model", requestedModel, "correlation_id", correlationID).Debug("emitted model.not_found event")
		}
	}

	// Operational logging (bridges to OTEL automatically via logger)
	logger.WithFields(
		"event_type", "model.not_found",
		"requested_model", requestedModel,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"correlation_id", correlationID,
		"error_type", "model_not_found",
	).Warn("model not found - configuration issue")
}

// emitFallbackTriggeredEvent emits an event when fallback routing is initiated
func (s *Server) emitFallbackTriggeredEvent(ctx context.Context, requestedModel, reason, userID, apiKeyHash, correlationID string) {
	sys, err := s.getCQRSSystem()
	if err != nil || sys == nil {
		return
	}

	event := chat.EmitFallbackTriggeredEvent(ctx, requestedModel, reason, userID, apiKeyHash, correlationID)

	// Persist event to database (ClickHouse/Postgres)
	if sys.Writer != nil {
		if err := sys.Writer.Append(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to persist fallback.triggered event")
		}
	}

	// Publish to event bus for in-memory handlers
	if sys.EventBus != nil {
		if err := sys.EventBus.Publish(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to publish fallback.triggered event")
		} else {
			logger.WithFields("requested_model", requestedModel, "reason", reason, "correlation_id", correlationID).Debug("emitted fallback.triggered event")
		}
	}

	// Operational logging (bridges to OTEL automatically via logger)
	logger.WithFields(
		"event_type", "fallback.triggered",
		"requested_model", requestedModel,
		"reason", reason,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"correlation_id", correlationID,
	).Info("fallback routing triggered")
}

// emitFallbackSucceededEvent emits an event when fallback routing succeeds
func (s *Server) emitFallbackSucceededEvent(ctx context.Context, requestedModel, actualModel, reason string, attempts int32, userID, apiKeyHash, correlationID string, durationMs int64) {
	sys, err := s.getCQRSSystem()
	if err != nil || sys == nil {
		return
	}

	event := chat.EmitFallbackSucceededEvent(ctx, requestedModel, actualModel, reason, attempts, userID, apiKeyHash, correlationID, durationMs)

	// Persist event to database (ClickHouse/Postgres)
	if sys.Writer != nil {
		if err := sys.Writer.Append(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to persist fallback.succeeded event")
		}
	}

	// Publish to event bus for in-memory handlers
	if sys.EventBus != nil {
		if err := sys.EventBus.Publish(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to publish fallback.succeeded event")
		} else {
			logger.WithFields(
				"requested_model", requestedModel,
				"actual_model", actualModel,
				"attempts", attempts,
				"correlation_id", correlationID,
			).Info("emitted fallback.succeeded event")
		}
	}

	// Operational logging (bridges to OTEL automatically via logger)
	logger.WithFields(
		"event_type", "fallback.succeeded",
		"requested_model", requestedModel,
		"actual_model", actualModel,
		"reason", reason,
		"attempts", attempts,
		"duration_ms", durationMs,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"correlation_id", correlationID,
	).Info("fallback routing succeeded")
}

// emitFallbackFailedEvent emits an event when all fallback attempts are exhausted
func (s *Server) emitFallbackFailedEvent(ctx context.Context, requestedModel, reason string, attempts int32, userID, apiKeyHash, correlationID string, lastError string) {
	sys, err := s.getCQRSSystem()
	if err != nil || sys == nil {
		return
	}

	event := chat.EmitFallbackFailedEvent(ctx, requestedModel, reason, attempts, userID, apiKeyHash, correlationID, lastError)

	// Persist event to database (ClickHouse/Postgres)
	if sys.Writer != nil {
		if err := sys.Writer.Append(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to persist fallback.failed event")
		}
	}

	// Publish to event bus for in-memory handlers
	if sys.EventBus != nil {
		if err := sys.EventBus.Publish(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to publish fallback.failed event")
		} else {
			logger.WithFields(
				"requested_model", requestedModel,
				"attempts", attempts,
				"last_error", lastError,
				"correlation_id", correlationID,
			).Error("emitted fallback.failed event")
		}
	}

	// Operational logging (bridges to OTEL automatically via logger)
	logger.WithFields(
		"event_type", "fallback.failed",
		"requested_model", requestedModel,
		"reason", reason,
		"attempts", attempts,
		"last_error", lastError,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"correlation_id", correlationID,
	).Error("all fallback attempts exhausted")
}

// emitSessionErrorEvent emits an event when a chat session fails before processing starts
func (s *Server) emitSessionErrorEvent(ctx context.Context, sessionID, requestedModel, errorType, errorMessage, userID, apiKeyHash, correlationID, procedureType string) {
	sys, err := s.getCQRSSystem()
	if err != nil || sys == nil {
		return
	}

	event := chat.EmitSessionErrorEvent(ctx, sessionID, requestedModel, errorType, errorMessage, userID, apiKeyHash, correlationID)

	// Persist event to database (ClickHouse/Postgres)
	if sys.Writer != nil {
		if err := sys.Writer.Append(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to persist chat.session.error event")
		}
	}

	// Publish to event bus for in-memory handlers
	if sys.EventBus != nil {
		if err := sys.EventBus.Publish(ctx, event); err != nil {
			logger.WithFields("event_type", event.Type, "error", err.Error()).Warn("failed to publish chat.session.error event")
		} else {
			logger.WithFields(
				"session_id", sessionID,
				"requested_model", requestedModel,
				"error_type", errorType,
				"error_message", errorMessage,
				"correlation_id", correlationID,
			).Warn("emitted chat.session.error event")
		}
	}

	// Operational logging with structured payload for logs dashboard
	payloadData := map[string]interface{}{
		"error": map[string]interface{}{
			"gateway.error.type":    errorType,
			"gateway.error.message": errorMessage,
			"gateway.error.model":   requestedModel,
		},
		"session": map[string]interface{}{
			"gateway.session.id": sessionID,
		},
		"correlation": map[string]interface{}{
			"correlation.id": correlationID,
		},
		"gateway": map[string]interface{}{
			"gateway.procedure.type": procedureType,
		},
	}
	payloadJSON, _ := json.Marshal(payloadData)

	logger.WithCategory(logger.CategoryOperational).SetFields(
		"event", "gateway.error",
		"command_type", procedureType,
		"correlation_id", correlationID,
		"payload", string(payloadJSON),
	).Error("gateway error - model not found")
}

// subscribeToProviderEvents subscribes to provider configuration and API key events
// to automatically refresh providers when changes occur
func (s *Server) subscribeToProviderEvents() error {
	if s.ctx == nil {
		return nil // No context, skip subscription
	}

	// Get CQRS system from context
	sys, err := s.getCQRSSystem()
	if err != nil {
		logger.Warnf("gateway: CQRS system not available, auto-refresh disabled: %v", err)
		return nil
	}

	if sys.EventBus == nil {
		logger.Warn("gateway: event bus not available in CQRS system, auto-refresh disabled")
		return nil
	}

	// Get repositories from context for refresh
	providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo)
	apiKeyRepoAny := s.ctx.Value(contextkeys.APIKeyRepo)
	if providerRepoAny == nil || apiKeyRepoAny == nil {
		logger.Warn("gateway: repositories not available, auto-refresh disabled")
		return nil
	}

	providerRepo := providerRepoAny.(*provider_config.Repository)
	apiKeyRepo := apiKeyRepoAny.(provider_api_keys.Repository)

	// Handler function that refreshes providers
	refreshHandler := func(ctx context.Context, event database.Event) error {
		logger.WithFields("event_type", event.Type, "event_id", event.ID).Info("gateway: provider event received, refreshing providers")

		// Invalidate cache first
		s.invalidateDefaultModelCache()
		logger.Debug("gateway: cache invalidated, reloading from database")

		bundle, err := s.bootstrapFromDatabase(ctx, providerRepo, apiKeyRepo)
		if err != nil {
			logger.Errorf("gateway: failed to refresh providers after event: %v", err)
			return err
		}
		s.installBundle(tenantKeyFromContext(ctx), bundle)

		logger.Info("gateway: providers refreshed successfully after event")
		return nil
	}

	// Subscribe to provider configuration events
	if err := sys.EventBus.Subscribe("gateway-provider-configured", "provider.configured", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.configured: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.configured events")

	// Subscribe to API key events
	if err := sys.EventBus.Subscribe("gateway-api-key-added", "provider.api_key.added", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.api_key.added: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.api_key.added events")

	if err := sys.EventBus.Subscribe("gateway-api-key-toggled", "provider.api_key.toggled", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.api_key.toggled: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.api_key.toggled events")

	if err := sys.EventBus.Subscribe("gateway-api-key-weight-updated", "provider.api_key.weight_updated", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.api_key.weight_updated: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.api_key.weight_updated events")

	if err := sys.EventBus.Subscribe("gateway-api-key-deleted", "provider.api_key.deleted", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.api_key.deleted: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.api_key.deleted events")

	if err := sys.EventBus.Subscribe("gateway-provider-deleted", "provider.deleted", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.deleted: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.deleted events")

	if err := sys.EventBus.Subscribe("gateway-config-reloaded", "provider.config.reloaded", "providers", refreshHandler); err != nil {
		logger.Errorf("gateway: failed to subscribe to provider.config.reloaded: %v", err)
		return err
	}
	logger.Debug("gateway: subscribed to provider.config.reloaded events")

	logger.Info("gateway: subscribed to 7 provider events for auto-refresh")
	return nil
}

// FallbackStep represents a single fallback attempt step.
type FallbackStep struct {
	Aliases     []string // one or many aliases; many means try in parallel
	Strategy    string   // "priority" | "round_robin" | "parallel"
	TimeoutMs   int      // per-step timeout (ms)
	BackoffMs   int      // per-attempt backoff (ms)
	MaxAttempts int      // attempts per alias
	Name        string   // reason for fallback (e.g., "model_unavailable", "rate_limit")
}

// fallbackPlan builds an ordered plan of fallback steps based on config factors.
//
// Source order:
//  1. Per-tenant runtime_config (Settings → Configuration → Load Balancer).
//     Wins when rtconfig.LoadBalancer.Enabled && Fallback.Enabled.
//  2. Static gateway.yaml (s.cfg.LoadBalancer.Fallback). Used when no per-
//     tenant override exists, including self-hosted single-tenant setups.
func (s *Server) fallbackPlan(ctx context.Context) []FallbackStep {
	fb, ok := s.effectiveFallbackConfig(ctx)
	if !ok {
		return nil
	}
	steps := make([]FallbackStep, 0)
	// Default block first if enabled
	if fb.Default.Enabled && fb.Default.Model != "" {
		steps = append(steps, FallbackStep{
			Aliases:  []string{strings.ToLower(fb.Default.Model)},
			Strategy: "priority",
			Name:     "default_fallback",
		})
	}
	// Factors ordered by priority asc
	factors := append([]validator.FallbackFactorConfig(nil), fb.Factors...)
	sort.Slice(factors, func(i, j int) bool { return factors[i].Priority < factors[j].Priority })
	for _, f := range factors {
		aliases := make([]string, 0, len(f.Models))
		for _, m := range f.Models {
			if m.Model != "" {
				aliases = append(aliases, strings.ToLower(m.Model))
			}
		}
		if len(aliases) == 0 {
			continue
		}
		switch strings.ToLower(f.Strategy) {
		case "parallel":
			steps = append(steps, FallbackStep{
				Aliases:     aliases,
				Strategy:    "parallel",
				TimeoutMs:   f.TimeoutMs,
				BackoffMs:   f.BackoffMs,
				MaxAttempts: f.MaxAttempts,
				Name:        f.Name,
			})
		case "round_robin":
			// rotate per request using correlation ID
			rot := 0
			key := correlation.GetCorrelationID(ctx)
			if key == "" {
				key = time.Now().UTC().Format(time.RFC3339Nano)
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(key))
			rot = int(h.Sum32() % uint32(len(aliases)))
			rotAliases := append(aliases[rot:], aliases[:rot]...)
			// enqueue sequentially after rotation
			for _, a := range rotAliases {
				steps = append(steps, FallbackStep{
					Aliases:     []string{a},
					Strategy:    "priority",
					TimeoutMs:   f.TimeoutMs,
					BackoffMs:   f.BackoffMs,
					MaxAttempts: f.MaxAttempts,
					Name:        f.Name,
				})
			}
		default:
			// priority: enqueue sequential one-by-one
			for _, a := range aliases {
				steps = append(steps, FallbackStep{
					Aliases:     []string{a},
					Strategy:    "priority",
					TimeoutMs:   f.TimeoutMs,
					BackoffMs:   f.BackoffMs,
					MaxAttempts: f.MaxAttempts,
					Name:        f.Name,
				})
			}
		}
	}
	return steps
}

// effectiveFallbackConfig picks the right fallback source for the request:
// per-tenant runtime config wins when present and enabled; otherwise the
// static gateway.yaml. Returns (config, true) when a usable plan exists.
func (s *Server) effectiveFallbackConfig(ctx context.Context) (validator.FallbackConfig, bool) {
	// Per-tenant runtime override
	if rt := s.runtimeConfigFromCtx(ctx); rt != nil {
		tenantID := contextkeys.ExtractTenantID(ctx)
		lb := rt.GetLoadBalancer(tenantID)
		if lb.Enabled && lb.Fallback != nil && lb.Fallback.Enabled {
			return rtconfigFallbackToValidator(*lb.Fallback), true
		}
	}
	// Static gateway.yaml
	if s.cfg != nil && s.cfg.LoadBalancer.Fallback.Enabled {
		return s.cfg.LoadBalancer.Fallback, true
	}
	return validator.FallbackConfig{}, false
}

// runtimeConfigFromCtx pulls the per-tenant runtime config service from
// either the request or server context. Returns nil if neither carries
// it (CE single-binary, tests).
func (s *Server) runtimeConfigFromCtx(ctx context.Context) *rtconfig.Service {
	if svc, ok := ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
		return svc
	}
	if s.ctx != nil {
		if svc, ok := s.ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
			return svc
		}
	}
	return nil
}

// rtconfigFallbackToValidator converts the per-tenant runtime fallback
// shape (rtconfig.FallbackConfig) into the validator shape that
// fallbackPlan already consumes. Field-for-field, only Default.Enabled
// requires a presence translation: the runtime model exposes a pointer
// rather than a flag, so a non-nil Default with a non-empty Model
// counts as enabled.
func rtconfigFallbackToValidator(in rtconfig.FallbackConfig) validator.FallbackConfig {
	out := validator.FallbackConfig{Enabled: in.Enabled}
	if in.Default != nil {
		out.Default = validator.FallbackModelConfig{
			Enabled:     in.Default.Model != "",
			Provider:    in.Default.Provider,
			Model:       in.Default.Model,
			MaxTokens:   in.Default.MaxTokens,
			Temperature: in.Default.Temperature,
		}
	}
	out.Factors = make([]validator.FallbackFactorConfig, 0, len(in.Factors))
	for _, f := range in.Factors {
		models := make([]validator.FallbackModelConfig, 0, len(f.Models))
		for _, m := range f.Models {
			models = append(models, validator.FallbackModelConfig{
				Enabled:     true,
				Provider:    m.Provider,
				Model:       m.Model,
				MaxTokens:   m.MaxTokens,
				Temperature: m.Temperature,
			})
		}
		out.Factors = append(out.Factors, validator.FallbackFactorConfig{
			Name:        f.Name,
			Priority:    f.Priority,
			Strategy:    f.Strategy,
			Models:      models,
			TimeoutMs:   f.TimeoutMs,
			BackoffMs:   f.BackoffMs,
			MaxAttempts: f.MaxAttempts,
		})
	}
	return out
}

// fallbackAliasOrder flattens the fallback plan into a de-duplicated sequential
// list of model aliases to try for unary requests. Aliases are lower-cased.
func (s *Server) fallbackAliasOrder(ctx context.Context) []string {
	steps := s.fallbackPlan(ctx)
	if len(steps) == 0 {
		return nil
	}
	ordered := make([]string, 0, len(steps))
	seen := make(map[string]struct{})
	for _, step := range steps {
		for _, alias := range step.Aliases {
			al := strings.ToLower(alias)
			if _, ok := seen[al]; ok {
				continue
			}
			seen[al] = struct{}{}
			ordered = append(ordered, al)
		}
	}
	return ordered
}

// hashKeyFromContext derives a sticky key; we rely on correlation ID for now.
func (s *Server) hashKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(stickyKeyContextKey).(string); ok && v != "" {
		return v
	}
	return correlation.GetCorrelationID(ctx)
}

// extractStickyKey chooses a sticky key from a getter based on configured key source.
func (s *Server) extractStickyKey(source string, get func(string) string) string {
	switch strings.ToLower(source) {
	case "api_key":
		key := get(common.EverstackApiKey)
		return key
	case "user_id":
		return get(common.XUserID)
	case "ip":
		ff := get(common.ForwardedFor)
		if ff != "" {
			parts := strings.Split(ff, ",")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
		if v := get("x-real-ip"); v != "" {
			return v
		}
	}
	return ""
}

// effectiveLoadBalancerToggle returns (enabled, strategyOverride) for
// the request. Per-tenant runtime config wins; static gateway.yaml is
// the fallback. strategyOverride is empty when the static config
// should win for that field.
func (s *Server) effectiveLoadBalancerToggle(ctx context.Context) (bool, string) {
	if rt := s.runtimeConfigFromCtx(ctx); rt != nil {
		lb := rt.GetLoadBalancer(contextkeys.ExtractTenantID(ctx))
		// A tenant explicitly enabling load balancing wins over the
		// static config; explicit disable is harder to detect against
		// "tenant hasn't touched this section yet" so we treat the
		// rtconfig value as authoritative when its strategy is set.
		if lb.Strategy != "" || lb.Enabled {
			return lb.Enabled, lb.Strategy
		}
	}
	if s.cfg != nil {
		return s.cfg.LoadBalancer.Enabled, s.cfg.LoadBalancer.Strategy
	}
	return false, ""
}

// effectiveKeySource resolves the LB key_source for this request: tenant
// override (when load balancer is enabled in their runtime config) or
// the static gateway.yaml. Empty string when neither configures one.
func (s *Server) effectiveKeySource(ctx context.Context) string {
	if rt := s.runtimeConfigFromCtx(ctx); rt != nil {
		lb := rt.GetLoadBalancer(contextkeys.ExtractTenantID(ctx))
		if lb.Enabled && lb.KeySource != "" {
			return lb.KeySource
		}
	}
	if s.cfg != nil {
		return s.cfg.LoadBalancer.KeySource
	}
	return ""
}

// withKeySourceFromHeaders injects a sticky key into context based on configured key_source.
func (s *Server) withKeySourceFromHeaders(ctx context.Context, headers http.Header) context.Context {
	src := s.effectiveKeySource(ctx)
	if src == "" {
		return ctx
	}
	key := s.extractStickyKey(src, headers.Get)
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, stickyKeyContextKey, key)
}

// withKeySourceFromMetadata injects a sticky key into context from gRPC metadata.
func (s *Server) withKeySourceFromMetadata(ctx context.Context, md metadata.MD) context.Context {
	src := s.effectiveKeySource(ctx)
	if src == "" {
		return ctx
	}
	getter := func(k string) string {
		v := md.Get(strings.ToLower(k))
		if len(v) > 0 {
			return v[0]
		}
		return ""
	}
	key := s.extractStickyKey(src, getter)
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, stickyKeyContextKey, key)
}

// selectOmittedModel chooses a model alias when the request omits it.
// Stateless: derives choice from config and a stable hash (sticky key) per request.
func (s *Server) selectOmittedModel(ctx context.Context) string {
	enabled, strategyOverride := s.effectiveLoadBalancerToggle(ctx)
	if !enabled {
		return s.defaultModel()
	}

	// Get providers from database instead of config. Scope to the
	// request's tenant via ctx — using s.ctx would leak choices across
	// tenants because the server context has no per-request tenant id.
	var configs []*provider_config.Configuration
	if s.ctx != nil {
		if providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo); providerRepoAny != nil {
			providerRepo := providerRepoAny.(*provider_config.Repository)
			var err error
			configs, err = listProviderConfigs(ctx, providerRepo)
			if err != nil || len(configs) == 0 {
				return s.defaultModel()
			}
		}
	}

	if len(configs) == 0 {
		return s.defaultModel()
	}

	// Strategy: tenant runtime override wins, else fall back to gateway.yaml.
	strategy := strategyOverride
	if strategy == "" && s.cfg != nil {
		strategy = s.cfg.LoadBalancer.Strategy
	}
	strategy = strings.ToLower(strategy)

	// priority: always use default
	if strategy == "priority" {
		return s.defaultModel()
	}

	// Build provider->aliases map from DB configs
	provToAliases := make(map[string][]string)
	providerList := make([]string, 0)
	seenProv := make(map[string]struct{})
	aliasList := make([]string, 0)

	for _, config := range configs {
		if !config.IsActive {
			continue
		}
		prov := strings.ToLower(config.ProviderName)
		for _, model := range config.EnabledModels {
			al := strings.ToLower(model)
			provToAliases[prov] = append(provToAliases[prov], al)
			aliasList = append(aliasList, al)
		}
		if _, ok := seenProv[prov]; !ok {
			providerList = append(providerList, prov)
			seenProv[prov] = struct{}{}
		}
	}

	if len(providerList) == 0 {
		return s.defaultModel()
	}

	// round_robin: rotate across all models
	if strategy == "round_robin" && len(aliasList) > 0 {
		key := s.hashKeyFromContext(ctx)
		if key == "" {
			key = time.Now().UTC().Format(time.RFC3339Nano)
		}
		h := fnv.New32a()
		h.Write([]byte(key))
		idx := int(h.Sum32() % uint32(len(aliasList)))
		return aliasList[idx]
	}

	// weighted: use provider weights from config
	weights := s.cfg.LoadBalancer.Weights
	cum := make([]int, 0, len(providerList))
	provs := make([]string, 0, len(providerList))
	total := 0
	sort.Strings(providerList)
	for _, prov := range providerList {
		w := 1
		if v, ok := weights[prov]; ok && v > 0 {
			w = v
		}
		total += w
		cum = append(cum, total)
		provs = append(provs, prov)
	}

	key := s.hashKeyFromContext(ctx)
	if key == "" {
		key = time.Now().UTC().Format(time.RFC3339Nano)
	}
	h := fnv.New32a()
	h.Write([]byte(key))
	r := int(h.Sum32() % uint32(total))
	idx := sort.SearchInts(cum, r+1)
	if idx < 0 || idx >= len(provs) {
		return s.defaultModel()
	}
	prov := provs[idx]
	aliases := provToAliases[prov]
	if len(aliases) > 0 {
		return aliases[0]
	}
	return s.defaultModel()
}

// GetRouter returns the server's router for the given request context.
// In shared multi-tenant mode the answer depends on the tenant in ctx;
// in single-tenant mode it returns the default bundle's router.
//
// Callers that previously held GetRouter() at startup must migrate to
// pass ctx every time — the function used to return a stable pointer
// that was rewritten on the fly per request, which is exactly the race
// the per-tenant bundles were introduced to close.
func (s *Server) GetRouter(ctx context.Context) *gw.Router {
	return s.routerFor(ctx)
}

// GetRegistry returns the server's provider registry for the given
// request context. See GetRouter for the per-tenant rationale.
func (s *Server) GetRegistry(ctx context.Context) *gw.Registry {
	return s.regFor(ctx)
}

// EnsureProvidersForRequest makes sure the per-tenant bundle for this
// request is loaded and fresh. The bundle is keyed by tenant schema (or
// org id for non-schema-per-tenant deployments). Concurrent requests for
// the same tenant share a single load via tenantBundleCache's loadLocks;
// requests for different tenants run in parallel without contending.
//
// Before this rewrote-in-place the gateway kept one Registry/Router on the
// Server and rebuilt them whenever the active tenant changed. With
// concurrent multi-tenant traffic the rebuild stomped on a request mid
// flight — that race produced cross-tenant LLM-key reuse. The cached
// bundle pointer is now stable for the life of any one request.
func (s *Server) EnsureProvidersForRequest(ctx context.Context) error {
	if s.ctx == nil {
		return nil
	}
	if !s.isSharedMode() {
		return nil
	}

	key := tenantKeyFromContext(ctx)
	if key == "" {
		// Shared mode but no tenant identity in context — refuse to load
		// a neighbour's bundle. The handler will see a nil registry and
		// surface a 4xx, matching the auth layer's fail-closed posture.
		return nil
	}

	if existing := s.tenantBundles.get(key, providerRefreshStaleness); existing != nil {
		return nil
	}

	providerRepoAny := s.ctx.Value(contextkeys.ProviderRepo)
	apiKeyRepoAny := s.ctx.Value(contextkeys.APIKeyRepo)
	if providerRepoAny == nil || apiKeyRepoAny == nil {
		return nil
	}
	providerRepo := providerRepoAny.(*provider_config.Repository)
	apiKeyRepo := apiKeyRepoAny.(provider_api_keys.Repository)

	loadLock := s.tenantBundles.loadLockFor(key)
	loadLock.Lock()
	defer loadLock.Unlock()

	// Re-check after acquiring the load lock: a concurrent request may
	// have populated the cache while we were waiting.
	if existing := s.tenantBundles.get(key, providerRefreshStaleness); existing != nil {
		return nil
	}

	bundle, err := s.bootstrapFromDatabase(ctx, providerRepo, apiKeyRepo)
	if err != nil {
		return err
	}
	s.tenantBundles.store(key, bundle)
	logger.WithFields("tenant_key", key).Debug("gateway: cached providers for tenant")
	return nil
}

// recordUsageMetrics records request metrics to the license monitor if available.
// Also records token usage to the trial manager when in trial mode.
// Returns a SpendLimitExceededError if the spend limit is exceeded with BLOCK action,
// which can be used to terminate the request/stream.
func (s *Server) recordUsageMetrics(inputTokens, outputTokens int64, estimatedCost, cacheSavings float64, cacheHit bool) error {
	if s.ctx == nil {
		return nil
	}

	totalTokens := inputTokens + outputTokens

	// Record tokens to trial manager if in trial mode
	le := enterprise.LicenseEnforcerFromContext(s.ctx)
	if le.IsInTrialMode() {
		if err := le.RecordTrialTokens(context.Background(), totalTokens); err != nil {
			logger.Warnf("trial: failed to record tokens: %v", err)
		}
	}

	// Record to license monitor (no-op in CE builds)
	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	metrics := enterprise.RequestMetrics{
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		EstimatedCost: estimatedCost,
		CacheSavings:  cacheSavings,
		CacheHit:      cacheHit,
	}
	if err := monitor.RecordRequestWithMetrics(metrics); err != nil {
		if licensemonitor.IsSpendLimitExceeded(err) {
			logger.Warn("gateway: spend limit exceeded - request should be terminated")
			return err
		}
	}
	return nil
}

// GetToolLoopManager returns the tool loop manager for serverless function execution.
func (s *Server) GetToolLoopManager() *toolloop.LoopManager {
	return s.toolLoop
}

// SetVoiceCloneRepo sets the voice clone profile repository for resolving
// voice clone profile IDs to provider voice IDs in audio requests.
func (s *Server) SetVoiceCloneRepo(repo voice_clone.Repository) {
	s.voiceCloneRepo = repo
}

// GetOrganizationID returns the organization ID from the license monitor.
// This is the tenant ID for the gateway instance (single-tenant per gateway).
// DefaultSelfHostedTenantID is the fallback tenant ID for self-hosted single-tenant deployments.
// This UUID is deterministic: uuid.NewSHA1(uuid.NameSpaceDNS, []byte("everstack.self-hosted"))
const DefaultSelfHostedTenantID = "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"

func (s *Server) GetOrganizationID() string {
	if s.ctx == nil {
		return DefaultSelfHostedTenantID
	}

	if orgID := enterprise.LicenseMonitorFromContext(s.ctx).GetOrganizationID(); orgID != "" {
		return orgID
	}
	return DefaultSelfHostedTenantID
}

// ShouldExecuteToolLoop checks if the response contains tool calls that need execution.
func (s *Server) ShouldExecuteToolLoop(resp *gw.ChatCompletionResponse) bool {
	if s.toolLoop == nil {
		return false
	}
	return s.toolLoop.ShouldExecuteToolLoop(resp)
}

// ExecuteToolLoop runs the tool execution loop and returns messages to append.
func (s *Server) ExecuteToolLoop(ctx context.Context, tenantID, requestID, correlationID string, resp *gw.ChatCompletionResponse) ([]gw.Message, error) {
	if s.toolLoop == nil || !s.toolLoop.IsEnabled() {
		return nil, nil
	}

	execCtx := &toolloop.ExecutionContext{
		RequestID:     requestID,
		TenantID:      tenantID,
		CorrelationID: correlationID,
	}

	return s.toolLoop.ExecuteToolLoop(ctx, execCtx, resp)
}
