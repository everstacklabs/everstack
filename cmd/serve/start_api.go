package serve

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	nethttputil "net/http/httputil"
	_ "net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	a2aserver "github.com/everstacklabs/everstack/internal/a2a/server"
	adk "github.com/everstacklabs/everstack/internal/adk"
	"github.com/everstacklabs/everstack/internal/agents/agentrun"
	agentdeploy "github.com/everstacklabs/everstack/internal/agents/deployment"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttrigger "github.com/everstacklabs/everstack/internal/agents/trigger"
	alertpkg "github.com/everstacklabs/everstack/internal/alerts"
	"github.com/everstacklabs/everstack/internal/api"
	agents "github.com/everstacklabs/everstack/internal/api/grpc/agents/v1"
	alertssvc "github.com/everstacklabs/everstack/internal/api/grpc/alerts/v1"
	annotations "github.com/everstacklabs/everstack/internal/api/grpc/annotations/v1"
	api_key "github.com/everstacklabs/everstack/internal/api/grpc/api_key/v1"
	catalog "github.com/everstacklabs/everstack/internal/api/grpc/catalog/v1"
	channelssvc "github.com/everstacklabs/everstack/internal/api/grpc/channels/v1"
	clisvc "github.com/everstacklabs/everstack/internal/api/grpc/cli/v1"
	configsvc "github.com/everstacklabs/everstack/internal/api/grpc/config/v1"
	datasets "github.com/everstacklabs/everstack/internal/api/grpc/datasets/v1"
	events "github.com/everstacklabs/everstack/internal/api/grpc/events/v1"
	everstackv1 "github.com/everstacklabs/everstack/internal/api/grpc/everstack/v1"
	functions "github.com/everstacklabs/everstack/internal/api/grpc/functions/v1"
	gateway "github.com/everstacklabs/everstack/internal/api/grpc/gateway/v1"
	issuessvc "github.com/everstacklabs/everstack/internal/api/grpc/issues/v1"
	logs "github.com/everstacklabs/everstack/internal/api/grpc/logs/v1"
	mcpsvc "github.com/everstacklabs/everstack/internal/api/grpc/mcp/v1"
	memorysvc "github.com/everstacklabs/everstack/internal/api/grpc/memory/v1"
	model_discovery "github.com/everstacklabs/everstack/internal/api/grpc/model_discovery/v1"
	modelmetricssvc "github.com/everstacklabs/everstack/internal/api/grpc/modelmetrics/v1"
	observability "github.com/everstacklabs/everstack/internal/api/grpc/observability/v1"
	onboardingsvc "github.com/everstacklabs/everstack/internal/api/grpc/onboarding/v1"
	prompts "github.com/everstacklabs/everstack/internal/api/grpc/prompts/v1"
	providers "github.com/everstacklabs/everstack/internal/api/grpc/providers/v1"
	grpcmw "github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	traces "github.com/everstacklabs/everstack/internal/api/grpc/traces/v1"
	voicesvc "github.com/everstacklabs/everstack/internal/api/grpc/voice/v1"
	workflows "github.com/everstacklabs/everstack/internal/api/grpc/workflows/v1"
	"github.com/everstacklabs/everstack/internal/api/http/middleware"
	http_mw "github.com/everstacklabs/everstack/internal/api/http/middleware/interceptors"
	otlphttp "github.com/everstacklabs/everstack/internal/api/http/otlp"
	"github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/api/service/registry"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/auth/m2m"
	selfhostedauth "github.com/everstacklabs/everstack/internal/auth/selfhosted"
	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	channelpkg "github.com/everstacklabs/everstack/internal/channels"
	discordpkg "github.com/everstacklabs/everstack/internal/channels/discord"
	slackpkg "github.com/everstacklabs/everstack/internal/channels/slack"
	telegrampkg "github.com/everstacklabs/everstack/internal/channels/telegram"
	"github.com/everstacklabs/everstack/internal/classificationrules"
	"github.com/everstacklabs/everstack/internal/commands"
	catalogCmdHandler "github.com/everstacklabs/everstack/internal/commands/handlers/catalog"
	licenseCmd "github.com/everstacklabs/everstack/internal/commands/handlers/license"
	internalcfg "github.com/everstacklabs/everstack/internal/config"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/customcolumns"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	"github.com/everstacklabs/everstack/internal/enterprise"
	internalevents "github.com/everstacklabs/everstack/internal/events"
	edgefeatures "github.com/everstacklabs/everstack/internal/features"
	ingressgw "github.com/everstacklabs/everstack/internal/gateway"
	ghpkg "github.com/everstacklabs/everstack/internal/github"
	"github.com/everstacklabs/everstack/internal/interop"
	issuepkg "github.com/everstacklabs/everstack/internal/issues"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	domain "github.com/everstacklabs/everstack/internal/lib/domain"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/region"
	"github.com/everstacklabs/everstack/internal/logcolumns"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcpserver "github.com/everstacklabs/everstack/internal/mcp/server"
	mcpserverauth "github.com/everstacklabs/everstack/internal/mcp/serverauth"
	mcpserverprovider "github.com/everstacklabs/everstack/internal/mcp/serverprovider"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/modelmetrics"
	internalnet "github.com/everstacklabs/everstack/internal/net"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/providers/httpclient"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/query"
	licenseQuery "github.com/everstacklabs/everstack/internal/query/handlers/license"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	sandboxdocker "github.com/everstacklabs/everstack/internal/sandbox/docker"
	sandboxfcagent "github.com/everstacklabs/everstack/internal/sandbox/fcagent"
	sandboxfirecracker "github.com/everstacklabs/everstack/internal/sandbox/firecracker"
	sandboxk8s "github.com/everstacklabs/everstack/internal/sandbox/kubernetes"
	sandboxlcwebhooks "github.com/everstacklabs/everstack/internal/sandbox/lcwebhooks"
	sandboxmetrics "github.com/everstacklabs/everstack/internal/sandbox/metrics"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
	sandboxsnapshots "github.com/everstacklabs/everstack/internal/sandbox/snapshots"
	"github.com/everstacklabs/everstack/internal/sandbox/volstore"
	"github.com/everstacklabs/everstack/internal/semanticmappings"
	catalogsvc "github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/services/catalog_sync"
	servicescfg "github.com/everstacklabs/everstack/internal/services/config"
	evalrunner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
	"github.com/everstacklabs/everstack/internal/services/trial"
	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	s3pkg "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/everstacklabs/everstack/internal/telemetry/aggregator"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
	"github.com/everstacklabs/everstack/internal/telemetry/traceoverlays"
	sechttp "github.com/everstacklabs/everstack/internal/tls"
	"github.com/everstacklabs/everstack/internal/traceviews"
	"github.com/everstacklabs/everstack/internal/trooper"
	"github.com/everstacklabs/everstack/internal/usage"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/everstacklabs/everstack/ui"
	"github.com/gorilla/mux"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	cryptossh "golang.org/x/crypto/ssh"
)

// Package-level shutdown functions for OTEL cleanup
var (
	globalOTELShutdown         func()
	globalCollectorShutdown    func() error
	globalAgentsShutdown       func(context.Context) error
	globalSandboxProxyShutdown func(context.Context) error
)

// cookieSessionAuthDB is the DB pool used by cookieSessionAuthMiddleware
// to validate cloud session cookies and resolve the user_id. Set once
// during StartAPI; the middleware reads it via a closure and treats
// nil as "skip enrichment" so requests received during init are no-ops.
//
// IMPORTANT: this MUST point at the platform DB (everstack.sessions
// table lives there). The gateway's primary DB is the per-tenant
// gateway DB and does NOT have everstack.sessions — pointing the
// middleware at it would silently return zero rows on every lookup.
// Configured via EVS_PLATFORM_DSN env var; falls back to the gateway
// primary if unset (CE single-binary mode where both DBs collapse).
var cookieSessionAuthDB *sqlx.DB

// dbHealth implements the API health check by delegating to a provided function.
type dbHealth struct{ check func(context.Context) error }

func (d dbHealth) Health(c context.Context) error { return d.check(c) }

func newStorageCredentialStore(db *sqlx.DB, config *validator.Config) (storagecredentials.Store, error) {
	if db == nil {
		return nil, storagecredentials.ErrStoreNotConfigured
	}
	if config == nil || config.SecretManager == nil || config.SecretManager.StorageCredentials == nil {
		return storagecredentials.NewConfiguredPostgresStore(db)
	}
	configured := config.SecretManager.StorageCredentials
	backend := strings.ToLower(strings.TrimSpace(configured.Backend))
	if backend == "" || backend == "inherit" {
		if strings.EqualFold(strings.TrimSpace(config.SecretManager.Type), "vault") {
			backend = "vault"
		} else {
			backend = "postgres"
		}
	}
	if backend == "vault" {
		if config.SecretManager.Vault == nil {
			return nil, fmt.Errorf("vault storage credential backend is missing vault configuration")
		}
		vault := config.SecretManager.Vault
		vaultStore, err := storagecredentials.NewVaultStore(db, storagecredentials.VaultConfig{
			Address: vault.Address, Token: vault.Token, Namespace: vault.Namespace,
			MountPath: vault.MountPath, PathPrefix: configured.PathPrefix,
		}, nil)
		if err != nil {
			return nil, err
		}
		// Keep previously backfilled PostgreSQL references readable while new
		// writes move to Vault. Once those connections are rotated, operators
		// can remove the local keyring.
		if strings.TrimSpace(configured.MasterKey) != "" {
			keyring, err := storagecredentials.NewKeyringConfig(configured.KeyID, configured.MasterKey, configured.PreviousKeys)
			if err != nil {
				return nil, err
			}
			cipher, err := storagecredentials.NewEnvelopeCipherFromConfig(keyring)
			if err != nil {
				return nil, err
			}
			fallback, err := storagecredentials.NewPostgresStore(db, cipher)
			if err != nil {
				return nil, err
			}
			vaultStore.SetFallback(fallback)
		}
		return vaultStore, nil
	}
	if backend != "postgres" {
		return nil, fmt.Errorf("unsupported storage credential backend %q", backend)
	}
	keyring, err := storagecredentials.NewKeyringConfig(configured.KeyID, configured.MasterKey, configured.PreviousKeys)
	if err != nil {
		return nil, err
	}
	cipher, err := storagecredentials.NewEnvelopeCipherFromConfig(keyring)
	if err != nil {
		return nil, err
	}
	return storagecredentials.NewPostgresStore(db, cipher)
}

// statusRecorder captures status code and response size
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }
func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// stdLogger returns a stdlib logger backed by our logger
func stdLogger() *log.Logger {
	l := log.New(os.Stderr, "proxy ", log.LstdFlags)
	return l
}

// convertTracingConfig converts validator.OTELTracingConfig to telemetry.TracingConfig
func convertTracingConfig(cfg *validator.OTELTracingConfig) *telemetry.TracingConfig {
	if cfg == nil {
		logger.Debug("convertTracingConfig: no tracing config provided (cfg is nil)")
		return nil
	}

	logger.WithFields(
		"sampling_rate", cfg.SamplingRate,
		"granularity", cfg.Granularity,
		"trace_provider_calls", cfg.TraceProviderCalls,
		"trace_stream_chunks", cfg.TraceStreamChunks,
		"trace_fallbacks", cfg.TraceFallbacks,
		"trace_key_rotation", cfg.TraceKeyRotation,
	).Debug("convertTracingConfig: loaded tracing config from YAML")

	// Create tracing config with values from YAML
	tc := &telemetry.TracingConfig{
		SamplingRate:       cfg.SamplingRate,
		Granularity:        cfg.Granularity,
		TraceProviderCalls: cfg.TraceProviderCalls,
		TraceStreamChunks:  cfg.TraceStreamChunks,
		TraceFallbacks:     cfg.TraceFallbacks,
		TraceKeyRotation:   cfg.TraceKeyRotation,
	}

	// Apply granularity-based defaults if granularity is set
	if tc.Granularity != "" {
		tc.ApplyGranularity()
		logger.WithFields(
			"granularity", tc.Granularity,
			"trace_provider_calls", tc.TraceProviderCalls,
			"trace_stream_chunks", tc.TraceStreamChunks,
			"trace_fallbacks", tc.TraceFallbacks,
			"trace_key_rotation", tc.TraceKeyRotation,
		).Debug("convertTracingConfig: applied granularity-based defaults")
	}

	return tc
}

// buildDirectExportConfig builds the DirectExportConfig from database.clickhouse config
// This ensures ClickHouse connection details are defined in only one place
func buildDirectExportConfig(config *validator.Config) telemetry.DirectExportConfig {
	// Check if direct export is enabled in OTEL config
	if config == nil || !config.Server.Telemetry.OTEL.DirectExport.Enabled {
		return telemetry.DirectExportConfig{Enabled: false}
	}

	// Check if ClickHouse is configured in database config
	if config.Database == nil || config.Database.ClickHouse.DSN == "" {
		logger.Warn("buildDirectExportConfig: direct_export enabled but database.clickhouse.dsn not configured")
		return telemetry.DirectExportConfig{Enabled: false}
	}

	// Parse ClickHouse DSN to extract connection parameters
	// Format: clickhouse://user:password@host:port/database
	dsn := config.Database.ClickHouse.DSN
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		logger.WithError(err).Warn("buildDirectExportConfig: failed to parse ClickHouse DSN")
		return telemetry.DirectExportConfig{Enabled: false}
	}

	// Extract host (first address without port)
	host := "localhost"
	if len(opts.Addr) > 0 {
		// Addr format is "host:port", extract just the host
		addr := opts.Addr[0]
		if colonIdx := strings.LastIndex(addr, ":"); colonIdx > 0 {
			host = addr[:colonIdx]
		} else {
			host = addr
		}
	}

	cfg := telemetry.DirectExportConfig{
		Enabled:        true,
		ClickHouseHost: host,
		Database:       opts.Auth.Database,
		Username:       opts.Auth.Username,
		Password:       opts.Auth.Password,
	}

	logger.WithFields(
		"host", host,
		"database", opts.Auth.Database,
		"username", opts.Auth.Username,
	).Debug("buildDirectExportConfig: derived ClickHouse config from database.clickhouse.dsn")

	return cfg
}

// StartAPI is the exported entry point for building the gateway API.
// It can be called by the cloud control plane with a pre-opened SharedDB.
func StartAPI(ctx context.Context, config *validator.Config, configPath string, embeddedDefaults *EmbeddedDefaults, router *mux.Router) (*api.API, error) {
	apis, _, err := StartAPIWithRuntime(ctx, config, configPath, embeddedDefaults, router)
	return apis, err
}

// GatewayAPIRuntime exposes request wrappers configured while building the
// gateway API. Embedders that serve the router themselves must install these
// wrappers after their tenant-authentication middleware.
type GatewayAPIRuntime struct {
	managedStorageDefaults storagepkg.ManagedDefaultEnsurer
}

// StartAPIWithRuntime builds the gateway API and returns the request runtime
// needed by embedders such as the shared cloud control plane.
func StartAPIWithRuntime(ctx context.Context, config *validator.Config, configPath string, embeddedDefaults *EmbeddedDefaults, router *mux.Router) (*api.API, *GatewayAPIRuntime, error) {
	runtime := &GatewayAPIRuntime{}
	apis, err := startAPI(ctx, config, configPath, embeddedDefaults, router, runtime)
	if err != nil {
		return nil, nil, err
	}
	return apis, runtime, nil
}

// WrapManagedStorageTenantBootstrap provisions a stable managed default after
// an upstream middleware has established a verified tenant context.
func (r *GatewayAPIRuntime) WrapManagedStorageTenantBootstrap(next http.Handler) http.Handler {
	var defaults storagepkg.ManagedDefaultEnsurer
	if r != nil {
		defaults = r.managedStorageDefaults
	}
	return newManagedStorageTenantBootstrap(defaults).Wrap(next)
}

// isManagedGateway reports whether this process is a managed/cloud gateway
// serving other people's tenants, as opposed to a standalone self-hosted one.
//
// SharedDB is itself a managed-gateway signal, but it is not the only one: some
// supported embedders inject that pool directly, and conversely a gateway can
// be handed a platform DSN without a pre-opened shared pool — which is exactly
// how the deployed cloud gateways run. Keying only off SharedDB there made them
// look standalone, so LocalScopeResolver injected a single local tenant into
// every request and OTLP ingest attributed all spans to it.
func isManagedGateway(sharedMode bool) bool {
	return sharedMode || strings.TrimSpace(os.Getenv("EVS_PLATFORM_DSN")) != ""
}

// gatewayRuntimeContext marks every managed gateway as multi-tenant for
// request-scoped runtime components. Cloud deployments can be managed either
// through an injected SharedDB or EVS_PLATFORM_DSN; provider routing must use
// tenant bundles in both topologies.
func gatewayRuntimeContext(ctx context.Context, sharedMode bool) (context.Context, bool) {
	managedGateway := isManagedGateway(sharedMode)
	if managedGateway {
		ctx = context.WithValue(ctx, contextkeys.SharedGatewayMode, true)
	}
	return ctx, managedGateway
}

func startAPI(ctx context.Context, config *validator.Config, configPath string, embeddedDefaults *EmbeddedDefaults, router *mux.Router, runtime *GatewayAPIRuntime) (*api.API, error) {
	// In shared gateway mode (SharedDB provided), per-tenant API key secrets
	// come from context so we skip the global secret check.
	sharedMode := embeddedDefaults != nil && embeddedDefaults.SharedDB != nil
	if !sharedMode && !apikeylib.SecretPresent() {
		return nil, fmt.Errorf("api key hash secret is required; set server.security.api_key_hash_secret or EVS_API_KEY_HASH_SECRET")
	}
	// Store managed mode in context so request-scoped components, including
	// provider routing, use tenant bundles for both supported cloud topologies.
	ctx, managedGateway := gatewayRuntimeContext(ctx, sharedMode)
	// Entitlement resolution runs on request contexts that do not always
	// descend from this one, so the managed flag is process state rather than
	// a context value.
	enterprise.SetManagedGateway(managedGateway)

	// Load services config early so bypass policies are available in global viper
	// This must happen before any policy.FromGlobal() calls
	if _, err := servicescfg.Load(""); err != nil {
		logger.WithError(err).Warn("Failed to load services config, using defaults")
	}

	// Safely apply origin and security middleware via shared helper
	var tlsConfig *tls.Config
	if config != nil && config.Server != nil {
		var err error
		tlsConfig, err = sechttp.ConfigureFromServerConfig(router, config.Server)
		if err != nil {
			return nil, err
		}

		// Enforce external_domain: reject access via other hosts (e.g., localhost)
		// Only enabled when enforce_external_domain is true (opt-in for production)
		if config.Server.Config.EnforceExternalDomain {
			if host := strings.TrimSpace(config.Server.Config.ExternalDomain); host != "" {
				router.Use(domain.EnforceExternalDomain(host))
			}
		}

		// Per-tenant feature gates for /v1/agents/* and /v1/mcp/*. Single
		// middleware on the mux is cheaper than threading a gate through
		// every handler. Reads runtime_config.features.{enable_agents,
		// mcp_gateway.enabled} on the request hot path. Pass-through if
		// the rtconfig service isn't wired (CE single-binary, tests).
		router.Use(featurePathGate)
	}

	// NOTE: cookie-session enrichment is wrapped around the full
	// handler tree in listenAndServe (not via router.Use here),
	// because gorilla/mux Use() doesn't propagate to subrouters and
	// Connect-RPC services register on subrouters via
	// RegisterHandlerPrefixes.

	// In shared gateway mode, use the pre-opened tenant-aware DB pool.
	// IMPORTANT: CQRS events always stay in Postgres (per-tenant via SET search_path).
	// ClickHouse is only used for analytics/traces, NOT for the event store.
	// Using hybrid CQRS in shared mode would read events from ClickHouse (which is
	// empty for existing tenants) and show the activation screen.
	var initRes *database.InitResult
	var evalClickHouseConn clickhouse.Conn
	if sharedMode {
		initRes = &database.InitResult{
			Mode:    "single",
			Primary: embeddedDefaults.SharedDB,
		}
		// Attach ClickHouse for analytics/traces only (not for CQRS event store)
		if embeddedDefaults.SharedAnalytics != nil {
			initRes.Analytics = embeddedDefaults.SharedAnalytics
		}
	} else {
		var err error
		initRes, err = database.InitializeFromConfig(ctx, config)
		if err != nil {
			return nil, err
		}
	}

	// Initialize OpenTelemetry if enabled
	var collectorMgr *telemetry.CollectorManager

	if config.Server.Telemetry.OTEL.Enabled {
		// Build DirectExportConfig from database.clickhouse config (single source of truth)
		directExportCfg := buildDirectExportConfig(config)

		// Start embedded collector sidecar if in embedded mode
		if config.Server.Telemetry.OTEL.Mode == "embedded" && directExportCfg.Enabled {
			mgr, err := telemetry.StartEmbeddedCollector(directExportCfg)
			if err != nil {
				logger.WithFields("error", err.Error()).Warn("Failed to start embedded OTEL collector - logs will not be exported")
			}
			if mgr != nil {
				collectorMgr = mgr
			}
		}

		otelCfg := telemetry.Config{
			ServiceName:           config.Server.Telemetry.OTEL.ServiceName,
			ServiceVersion:        "1.0.0",
			Mode:                  config.Server.Telemetry.OTEL.Mode,
			CollectorURL:          config.Server.Telemetry.OTEL.CollectorURL,
			TenantID:              "", // Dynamically populated from license context
			TenantType:            config.Server.Telemetry.OTEL.TenantType,
			InstanceOwner:         config.Server.Telemetry.OTEL.InstanceOwner,
			DeploymentEnvironment: getEnvOrDefault("EVS_ENV", "production"),
			TracingConfig:         convertTracingConfig(config.Server.Telemetry.OTEL.Tracing),
			DirectExport:          directExportCfg,
		}

		tel, shutdown, err := telemetry.InitOTEL(otelCfg)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("Failed to initialize OpenTelemetry - continuing without OTEL")
		} else {
			globalOTELShutdown = shutdown // Store for graceful shutdown
			ctx = telemetry.WithLoggerProvider(ctx, tel.LoggerProvider)

			// Set the OTEL provider in the logger package (to avoid import cycles)
			logger.SetOTELProvider(tel.LoggerProvider)

			// Enable automatic forwarding of logrus logs to OTEL
			logger.EnableOTELForwarding()

			tracesEnabled := false
			if otelCfg.TracingConfig != nil {
				tracesEnabled = otelCfg.TracingConfig.IsEnabled()
			}

			logger.WithFields(
				"service_name", otelCfg.ServiceName,
				"mode", otelCfg.Mode,
				"collector_url", otelCfg.CollectorURL,
				"tenant_type", otelCfg.TenantType,
				"traces_enabled", tracesEnabled,
				"tracing_config_present", otelCfg.TracingConfig != nil,
			).Info("OpenTelemetry initialized")

			// Keep one span per minute flowing into otel_traces even with
			// zero traffic, so pipeline-freshness alerts fire on real
			// ingestion breakage rather than on an idle environment.
			if tracesEnabled {
				stopHeartbeat := telemetry.StartHeartbeat(tel, telemetry.DefaultHeartbeatInterval)
				base := globalOTELShutdown
				globalOTELShutdown = func() {
					stopHeartbeat()
					base()
				}
			}
		}
	}

	// Store collector manager for graceful shutdown
	if collectorMgr != nil {
		globalCollectorShutdown = collectorMgr.Shutdown
	}

	// Initialize Redis client early if configured (shared across components)
	// This prevents duplicate Redis connections for license monitoring and caching
	var redisClient *cache.RedisClient
	if config != nil && config.Cache != nil && config.Cache.Type == "redis" && config.Cache.Redis.Address != "" {
		var err error
		redisClient, err = cache.NewRedisClient(config.Cache.Redis)
		if err != nil {
			logger.WithError(err).Warn("Failed to connect to Redis, continuing with in-memory storage")
		} else {
			logger.Info("Redis client connected for usage persistence")
		}
	}

	// Wire the cookie-session middleware to the right DB. Cloud-mode
	// auth keeps everstack.sessions on a separate platform DB; the
	// gateway's primary pool is the tenant DB and doesn't have that
	// schema. EVS_PLATFORM_DSN is set on the gateway pod by helm
	// (services-deployment.yaml uses the same secret). Fall back to
	// the primary pool when unset so single-binary CE deployments —
	// where both DBs collapse to one — still work.
	if platformDSN := strings.TrimSpace(os.Getenv("EVS_PLATFORM_DSN")); platformDSN != "" {
		platformConn, platformErr := database.Open(ctx, database.Config{
			Type: database.TypePostgres,
			DSN:  platformDSN,
		})
		if platformErr != nil {
			logger.WithError(platformErr).Warn("cookie_session_auth: EVS_PLATFORM_DSN open failed, falling back to primary pool")
		} else if platformConn != nil && platformConn.RW != nil {
			cookieSessionAuthDB = platformConn.RW
			logger.Info("cookie_session_auth: using platform DB for cloud session lookups")
		}
	}
	if cookieSessionAuthDB == nil && initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		cookieSessionAuthDB = initRes.Primary.RW
	}

	// Managed gateways never receive tenant config, so until now every
	// authenticated cloud tenant resolved entitlements as "managed-bypass" and
	// ran with no plan limits at all. Give the resolver a way to look the plan
	// tier up directly. organizations.plan_tier lives on the platform DB, which
	// is the same handle the cookie-session middleware just resolved (and which
	// collapses to the primary pool on single-binary deployments).
	//
	// Enforcement stays OFF by default. These tenants have been running
	// uncapped, so switching straight to enforcement would deny workloads that
	// are legal today; shadow mode reports what would apply instead. Set
	// EVS_PLAN_LIMIT_ENFORCEMENT=enforce once that report has been reviewed.
	if managedGateway && cookieSessionAuthDB != nil {
		enterprise.SetPlanTierResolver(enterprise.NewDBPlanTierResolver(cookieSessionAuthDB, time.Minute))
		enforce := strings.EqualFold(strings.TrimSpace(os.Getenv("EVS_PLAN_LIMIT_ENFORCEMENT")), "enforce")
		enterprise.SetShadowEnforcement(!enforce)
		if enforce {
			logger.Info("entitlements: managed plan limits are ENFORCED")
		} else {
			logger.Info("entitlements: managed plan limits resolve in shadow mode (reported, not enforced)")
		}
	}

	// Initialize CQRS system and inject into context for downstream services/middleware
	if initRes != nil && initRes.Primary != nil {
		var sys *cqrs.System
		if strings.ToLower(initRes.Mode) == "hybrid" && initRes.Analytics != nil {
			if s, e := cqrs.NewHybridSystem(ctx, initRes.Primary, initRes.Analytics); e == nil {
				sys = s
			} else {
				return nil, e
			}
		} else {
			if s, e := cqrs.NewSystem(ctx, initRes.Primary); e == nil {
				sys = s
			} else {
				return nil, e
			}
		}

		// Register trace handlers if ClickHouse is available (hybrid mode, or shared mode with analytics)
		hasClickHouse := (strings.ToLower(initRes.Mode) == "hybrid" || initRes.Analytics != nil) && config != nil && config.Database != nil && config.Database.ClickHouse.DSN != ""
		if hasClickHouse {
			// Parse ClickHouse DSN to extract connection parameters
			// Format: clickhouse://user:password@host:port/database
			dsn := config.Database.ClickHouse.DSN

			// Use clickhouse.ParseDSN to properly parse the DSN
			opts, err := clickhouse.ParseDSN(dsn)
			if err != nil {
				logger.WithError(err).Warn("Failed to parse ClickHouse DSN for traces")
			} else {
				// Create native ClickHouse connection for trace handlers
				chConn, err := clickhouse.Open(opts)
				if err != nil {
					logger.WithError(err).Warn("Failed to create native ClickHouse connection for traces")
				} else {
					evalClickHouseConn = chConn
					// ClickHouse: shared single-database with tenant_id column filtering.
					// No per-tenant USE {database} wrapping needed.
					if err := sys.RegisterTraceHandlers(chConn); err != nil {
						logger.WithError(err).Warn("Failed to register trace handlers")
					} else {
						logger.Info("Trace handlers registered successfully")
					}

					// Start background observability aggregator (sessions + users)
					obsAggregator := aggregator.New(chConn)
					obsAggregator.Start(ctx)

					// OTLP/HTTP receiver — Langfuse-compatible customer ingestion path.
					// Mount at /api/public/otel/v1/traces (mirrors Langfuse's
					// /api/public/otel/v1/traces) so existing OTel clients work
					// without an Everstack-specific SDK. Tenant comes from
					// the API-key middleware; clients cannot impersonate other
					// tenants — the handler stamps the authenticated tenant on
					// every received span before insert.
					// OTLP receivers resolve the tenant from the presented
					// Everstack API key (Authorization: Bearer <key>), exactly
					// like the inbound MCP server. This works on the shared
					// gateway, where no per-request tenant is injected into
					// context and the api-key interceptor would otherwise reject
					// the Bearer token (see isOTLPIngestPath bypass).
					otlpAuth := mcpserverauth.NewAPIKeyAuthenticator(initRes.Primary.RW, nil)

					// Per-tenant processed-bytes meter: buffers decompressed
					// OTLP ingest bytes and flushes them to billing_usage_records
					// once a minute. It records raw bytes per request tenant_id
					// only; the billing side resolves tenant_id -> organization_id
					// and applies the rate card (the gateway DB cannot map a
					// managed-instance tenant to its org).
					otlpMeter := usage.NewProcessedBytesMeter(initRes.Primary.RW, time.Minute)
					go otlpMeter.Run(ctx)
					logger.Info("OTLP processed-bytes meter started (1m flush)")

					otlpHandler := otlphttp.NewTracesHandler(chConn)
					otlpHandler.SetByteRecorder(otlpMeter.AddIngestBytes)
					router.Handle(
						"/api/public/otel/v1/traces",
						otlphttp.WithTenantAuth(otlpHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)
					// Common alternate path that some OTel SDKs default to —
					// expose both so we don't surprise customers who set
					// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://api.everstack.ai/v1/traces.
					router.Handle(
						"/v1/traces",
						otlphttp.WithTenantAuth(otlpHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)
					logger.Info("OTLP/HTTP traces receiver mounted at /api/public/otel/v1/traces")

					// OTLP/HTTP logs + metrics receivers — companions to the
					// traces receiver above so an external OTel source (e.g.
					// Claude Code) can ship all three signals to one
					// API-key-authenticated, tenant-isolated endpoint family.
					// Same tenant-stamping isolation as traces.
					otlpLogsHandler := otlphttp.NewLogsHandler(chConn)
					otlpLogsHandler.SetByteRecorder(otlpMeter.AddIngestBytes)
					router.Handle(
						"/api/public/otel/v1/logs",
						otlphttp.WithTenantAuth(otlpLogsHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)
					router.Handle(
						"/v1/logs",
						otlphttp.WithTenantAuth(otlpLogsHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)

					otlpMetricsHandler := otlphttp.NewMetricsHandler(chConn)
					otlpMetricsHandler.SetByteRecorder(otlpMeter.AddIngestBytes)
					router.Handle(
						"/api/public/otel/v1/metrics",
						otlphttp.WithTenantAuth(otlpMetricsHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)
					router.Handle(
						"/v1/metrics",
						otlphttp.WithTenantAuth(otlpMetricsHandler, otlpAuth),
					).Methods(http.MethodPost, http.MethodOptions)
					logger.Info("OTLP/HTTP logs + metrics receivers mounted at /api/public/otel/v1/{logs,metrics}")
				}
			}
		}

		// Eval runner is started later, after memory and sandbox initialization.

		ctx = cqrs.WithSystem(ctx, sys)
		if initRes.Primary.RW != nil {
			ctx = context.WithValue(ctx, contextkeys.PrimaryDB, initRes.Primary.RW)
		}
		if router != nil {
			// isManagedGateway, not sharedMode: a cloud gateway that was handed
			// a platform DSN rather than a pre-opened SharedDB still serves
			// other people's tenants, and must not have a single local tenant
			// injected into every request.
			router.Use(middleware.NewLocalTenantMiddleware(initRes.Primary.RW, isManagedGateway(sharedMode)).Wrap)
		}

		// In managed cloud mode, license/trial/activation is managed by the
		// control plane. Skip the self-hosted enforcer, monitor, trial manager,
		// fingerprint, and edge feature poller whether managed mode came from a
		// pre-opened SharedDB or EVS_PLATFORM_DSN.
		if managedGateway {
			logger.Info("managed gateway mode: skipping self-hosted license/trial/activation")
		} else if !shouldStartSelfHostedCloudLicensing(managedGateway) {
			logger.Info("community edition: cloud license, trial, activation, and usage sync disabled")
		} else {
			// Load license policy from services config if available, else viper
			lp := policy.FromGlobal()
			if svcCfg, err := servicescfg.Load(""); err == nil && svcCfg != nil {
				// Check both root-level security config and nested services.security config
				// for backward compatibility
				var policyToUse *servicescfg.SecurityPolicy
				if len(svcCfg.Security.Policy.BypassServices) > 0 || len(svcCfg.Security.Policy.BypassPrefixes) > 0 {
					policyToUse = &svcCfg.Security.Policy
				} else if len(svcCfg.Services.Security.Policy.BypassServices) > 0 || len(svcCfg.Services.Security.Policy.BypassPrefixes) > 0 {
					policyToUse = &svcCfg.Services.Security.Policy
				}

				if policyToUse != nil {
					// Build policy from services config
					p := policy.NewDefaultPolicy()
					if len(policyToUse.BypassServices) > 0 {
						p.BypassServices = make(map[string]struct{}, len(policyToUse.BypassServices))
						for _, s := range policyToUse.BypassServices {
							p.BypassServices[s] = struct{}{}
						}
					}
					if len(policyToUse.BypassPrefixes) > 0 {
						p.BypassPrefixes = append([]string{}, policyToUse.BypassPrefixes...)
					}
					lp = p
					logger.WithFields(
						"bypass_services", len(p.BypassServices),
						"bypass_prefixes", len(p.BypassPrefixes),
					).Debug("License enforcement policy loaded from config")
				}
			}

			sharedLE := enterprise.NewLicenseEnforcer(sys, lp)
			if cfg, err := servicescfg.Load(""); err == nil && cfg != nil {
				// Use new unified security config
				le := cfg.Security.LicenseEnforcement
				if le.CacheTTL > 0 {
					sharedLE.SetCacheTTL(le.CacheTTL)
				}
				sharedLE.SetEnabled(le.Enabled)
				sharedLE.SetDryRun(le.DryRun)
				// Set License Service URL for remote validation using unified config method
				if licenseURL := cfg.GetLicenseServiceURL(); licenseURL != "" {
					sharedLE.SetLicenseServiceURL(licenseURL)
				}
				// Note: M2M provider setup is deferred until after signing key is loaded from database
			}
			// Note: sharedLE.Start() is deferred until after M2M provider is configured

			// Generate device fingerprint early - used for:
			// 1. Trial tracking (anonymous usage before activation)
			// 2. Pre-activation usage reporting to License Service
			// 3. Linking historical usage to instance on activation
			// 4. License refresh validation (prevents token theft)
			fingerprint := trial.GenerateFingerprint()
			ctx = context.WithValue(ctx, contextkeys.DeviceFingerprint, fingerprint)
			sharedLE.SetDeviceFingerprint(fingerprint) // Enable device fingerprint validation on refresh
			logger.WithFields("fingerprint", fingerprint[:8]+"...").Debug("Device fingerprint generated")

			// Initialize trial manager for anonymous trial mode.
			// This allows a SELF-HOSTED gateway to run without a license with
			// limited usage: try the EE binary before buying a licence.
			//
			// Never on a managed gateway. The trial's allowances are
			// instance-wide (one bucket of requests and tokens for the whole
			// process), so on a shared cloud gateway every tenant draws down
			// the same bucket and the first to fill it throttles the rest.
			// Nobody there is trialling anything either: cloud tenants have
			// plans, resolved from tenant_config, not from a licence.
			trialMgr, trialErr := newTrialManager(ctx, managedGateway, redisClient)
			if trialErr != nil {
				logger.WithError(trialErr).Warn("Failed to initialize trial manager")
			}
			if trialMgr != nil {
				sharedLE.SetTrialManager(trialMgr)
				if trialMgr.IsActive() && !trialMgr.IsExpired() {
					logger.Info("Trial mode initialized - gateway will allow limited usage without license")
				}
			}
			ctx = context.WithValue(ctx, contextkeys.LicenseEnforcer, sharedLE)
			// Request contexts are built fresh per request and never inherit the
			// startup context, so also register the process-global backstop that
			// enterprise.LicenseEnforcerFromContext falls back to.
			enterprise.SetGlobalLicenseEnforcer(sharedLE)

			// Initialize license monitor for usage tracking and feature gates
			// Use PersistentMonitor with Redis for operational caching (rate limiting, UI display)
			svcCfg, err := servicescfg.Load("")
			var licenseServiceURL string
			if err != nil {
				logger.WithError(err).Warn("Failed to load services config for license monitor")
			} else if svcCfg != nil {
				licenseServiceURL = svcCfg.GetLicenseServiceURL()
			}

			baseMonitor := enterprise.NewLicenseMonitor(sharedLE, enterprise.MonitorConfig{
				CheckInterval:     1 * time.Hour,
				WarnBefore:        7 * 24 * time.Hour,
				LicenseServiceURL: licenseServiceURL,
			})

			// Wire up features callback: when enforcer fetches license, update monitor with available features
			sharedLE.SetFeaturesCallback(func(features map[string]*enterprise.FeatureRelease) {
				baseMonitor.SetAvailableFeatures(features)
			})

			// Wire up spend limit config callback: when JWT is refreshed, push spend limit config to monitor
			sharedLE.SetSpendLimitConfigCallback(func(amount float64, action string, enabled bool) {
				if baseMonitor == nil {
					return
				}
				baseMonitor.SetSpendLimitConfig(amount, action, enabled)
			})

			// Note: License refresh is deferred until after M2M provider is configured
			// (will be triggered after signing key is loaded and M2M provider is set up)

			// Wrap in PersistentMonitor for Redis-based usage persistence
			persistentMonitor := enterprise.NewPersistentMonitor(baseMonitor, redisClient, enterprise.StorageConfig{
				InstanceID:   fingerprint, // Use fingerprint as instance ID until activated
				SyncInterval: 10 * time.Second,
			})

			// Initialize usage syncer to report usage to license service
			// This enables spend limit tracking and billing usage reporting
			persistentMonitor.SetRedisClient(redisClient)
			persistentMonitor.InitUsageSyncer(licenseServiceURL)

			// Wire resource count provider so the syncer can include per-instance
			// snapshots in every usage report. The billing service uses these to
			// build historical charts.
			if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
				countsDB := initRes.Primary.RW
				persistentMonitor.SetSyncerCountsProvider(func(cctx context.Context) enterprise.ResourceCounts {
					c := collectResourceCounts(cctx, countsDB)
					return enterprise.ResourceCounts{
						Agents:            c.Agents,
						PersistentAgents:  c.PersistentAgents,
						ConcurrentRunning: c.ConcurrentRunning,
						DatasetItems:      c.DatasetItems,
						EvalRunsMonthly:   c.EvalRunsMonthly,
						AnnotationQueues:  c.AnnotationQueues,
						ChannelBindings:   c.ChannelBindings,
						MessagesMonthly:   c.MessagesMonthly,
						StorageBytes:      c.StorageBytes,
						NetworkRxBytes:    c.NetworkRxBytes,
						NetworkTxBytes:    c.NetworkTxBytes,
					}
				})
			}

			// Wire up limits update callback to receive authoritative limits from License Service
			// The License Service returns limits with each usage report, allowing it to control trial limits
			persistentMonitor.SetLimitsUpdateCallback(func(limitsAny, _ any, exceeded bool) {
				// Update trial manager with authoritative limits from License Service
				if limits, ok := limitsAny.(*licv1.EnforcementLimits); ok && limits != nil && trialMgr != nil {
					if limits.IsTrial {
						expiresAt := limits.GetTrialExpiresAt().AsTime()
						trialMgr.UpdateLimitsFromService(
							limits.GetDailyRequestLimit(),
							limits.GetMonthlyRequestLimit(),
							limits.GetRpmLimit(),
							limits.GetMonthlyTokenLimit(),
							expiresAt,
							exceeded,
						)
					} else if exceeded {
						trialMgr.SetLimitsExceeded()
					}
				}

				if exceeded {
					logger.Warn("license: usage limits exceeded - blocking new requests")
				}
			})

			// Note: M2M config for usage syncer is deferred until after signing key is loaded from database

			persistentMonitor.Start(ctx)

			ctx = context.WithValue(ctx, contextkeys.LicenseMonitor, persistentMonitor)
			// Same backstop as the enforcer above: without this, every
			// enterprise.LicenseMonitorFromContext call on a request path got
			// the no-op monitor and licensed instances fell back to CE limits.
			enterprise.SetGlobalLicenseMonitor(persistentMonitor)
			// Inject license service URL into context for GetPlans endpoint
			ctx = context.WithValue(ctx, contextkeys.LicenseServiceURL, licenseServiceURL)

			// Initialize edge manifest poller.
			// The poller auto-starts when EVS_MANIFEST_PUBLIC_KEY is set.
			// Edge defaults are baked into the binary — users don't configure these.
			publicKeys := edgefeatures.LoadPublicKeysFromEnv()
			if len(publicKeys) > 0 {
				edgeCfg := edgefeatures.PollerConfig{
					EdgeURL:      edgefeatures.DefaultEdgeURL(),
					PollInterval: edgefeatures.DefaultPollInterval(),
					CacheDir:     edgefeatures.DefaultCacheDir(),
				}
				// Allow YAML config to override baked-in defaults (internal use only)
				if config != nil && config.Features != nil && config.Features.Edge.URL != "" {
					edgeCfg.EdgeURL = config.Features.Edge.URL
				}
				if config != nil && config.Features != nil && config.Features.Edge.PollInterval > 0 {
					edgeCfg.PollInterval = config.Features.Edge.PollInterval
				}
				if config != nil && config.Features != nil && config.Features.Edge.CacheDir != "" {
					edgeCfg.CacheDir = config.Features.Edge.CacheDir
				}

				manifestPoller := edgefeatures.NewManifestPoller(edgeCfg, publicKeys)

				// Subscribe: when manifest features change, update the license monitor
				manifestPoller.Subscribe(func(resolved map[string]*edgefeatures.ResolvedFeature) {
					monitorFeatures := make(map[string]*enterprise.FeatureRelease, len(resolved))
					for key, f := range resolved {
						monitorFeatures[key] = &enterprise.FeatureRelease{
							Name:        f.Name,
							Description: f.Description,
							Status:      f.Status,
							Categories:  f.Categories,
						}
					}
					baseMonitor.SetAvailableFeatures(monitorFeatures)
				})

				manifestPoller.Start(ctx)
				logger.WithFields("edge_url", edgeCfg.EdgeURL, "poll_interval", edgeCfg.PollInterval).Info("edge manifest poller started")
			}
		} // end else (!managedGateway) — self-hosted license/trial/activation
	}

	// Instance activation: ensure a pending row and start async activation manager
	// Skip in every managed cloud topology — the control plane manages instance lifecycle.
	var instanceMgr enterprise.InstanceManager
	if shouldStartSelfHostedCloudLicensing(managedGateway) && initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		actCfg := enterprise.InstanceConfig{}
		if svcCfg, err := servicescfg.Load(""); err == nil && svcCfg != nil {
			actCfg.PlatformURL = svcCfg.GetLicenseServiceURL()
			actCfg.InstanceSalt = svcCfg.Activation.InstanceSalt
			actCfg.ActivateInterval = svcCfg.Activation.Interval
			actCfg.ActivateTimeout = svcCfg.Activation.Timeout
		}
		if fp, ok := ctx.Value(contextkeys.DeviceFingerprint).(string); ok && fp != "" {
			actCfg.DeviceFingerprint = fp
		}
		instanceMgr = enterprise.NewInstanceManager(initRes.Primary.RW, actCfg)
		// Inject command bus if available for license command dispatching
		if sys, err := cqrs.GetSystemFromContext(ctx); err == nil && sys != nil && sys.CommandBus != nil {
			instanceMgr.SetCommandBus(sys.CommandBus)
		}

		// Ensure M2M signing key exists before starting M2M providers
		// This auto-generates and encrypts a signing key on first startup if not present
		// Priority: 1) Environment variable EVS_M2M_SIGNING_KEY, 2) Database (auto-generated)
		if svcCfg, err := servicescfg.Load(""); err == nil && svcCfg != nil {
			// Only auto-generate if environment variable is not set
			if svcCfg.Security.M2M.Enabled && svcCfg.Security.M2M.Simple.SigningKey == "" {
				signingKey, err := instanceMgr.EnsureM2MSigningKey(ctx)
				if err != nil {
					logger.WithError(err).Warn("m2m: failed to ensure signing key, M2M authentication may not work")
				} else {
					// Inject the signing key into the environment so subsequent config loads pick it up
					// This is a runtime-only injection and doesn't persist to .envrc
					base64Key := base64.StdEncoding.EncodeToString(signingKey)
					os.Setenv("EVS_M2M_SIGNING_KEY", base64Key)
					logger.WithFields("key_length", len(signingKey)).Info("m2m: auto-generated signing key loaded from database")

					// Note: This auto-generated key is for INTERNAL gateway M2M (gateway ↔ local services).
					// For License Service M2M, we use the activation-derived signing key which is
					// stored in instance_states during activation. The syncer uses SetCredentials()
					// to configure M2M with the instance_id and activation-derived key.
				}
			}

			// Now that signing key is loaded, reload config to pick up the environment variable
			// and then set up M2M providers for all components
			svcCfg, err = servicescfg.Load("")
			if err != nil {
				logger.WithError(err).Warn("m2m: failed to reload config after signing key setup")
			}

			// Debug: Check if signing key was picked up
			if svcCfg != nil && svcCfg.Security.M2M.Enabled {
				signingKeyLen := 0
				signingKeyB64 := svcCfg.Security.M2M.Simple.SigningKey
				if signingKeyB64 != "" {
					decoded, err := base64.StdEncoding.DecodeString(signingKeyB64)
					if err == nil {
						signingKeyLen = len(decoded)
					} else {
						logger.WithFields("b64_len", len(signingKeyB64)).Warn("m2m: failed to decode signing key")
					}
				}
				logger.WithFields("signing_key_length", signingKeyLen, "m2m_enabled", svcCfg.Security.M2M.Enabled, "has_signing_key_b64", signingKeyB64 != "").Debug("m2m: config reloaded")
			}

			m2mConfigured := false
			if svcCfg != nil && svcCfg.Security.M2M.Enabled {
				m2mCfgRaw := svcCfg.Security.M2M.ToM2MConfig()
				if m2mCfgRaw != nil {
					logger.WithFields("has_simple_config", m2mCfgRaw.SimpleConfig != nil, "provider", m2mCfgRaw.Provider).Debug("license_enforcer: creating M2M config")

					// Set up M2M provider for license enforcer
					m2mAuthCfg, err := m2m.ConfigFromServices(&m2m.ServicesM2MConfig{
						Enabled:         true,
						Provider:        m2mCfgRaw.Provider,
						SimpleConfig:    convertSimpleConfig(m2mCfgRaw.SimpleConfig),
						OIDCConfig:      convertOIDCConfig(m2mCfgRaw.OIDCConfig),
						Clients:         convertClients(m2mCfgRaw.Clients),
						OIDCClients:     convertClients(m2mCfgRaw.OIDCClients),
						PublicEndpoints: m2mCfgRaw.PublicEndpoints,
						EndpointScopes:  m2mCfgRaw.EndpointScopes,
						ScopePolicy:     convertScopePolicy(m2mCfgRaw.ScopePolicy),
					})
					if err == nil {
						// Use "gateway" client for license enforcement (needs instance:read, instance:write)
						m2mProvider, err := m2m.NewTokenProvider(m2mAuthCfg, "gateway")
						if err == nil {
							// Set M2M provider on shared license enforcer
							enterprise.LicenseEnforcerFromContext(ctx).SetM2MProvider(m2mProvider)
							logger.Info("license_enforcer: M2M provider configured for gateway client")
							m2mConfigured = true
						} else {
							logger.WithError(err).Warn("license_enforcer: failed to create M2M provider")
						}
					} else {
						logger.WithError(err).Warn("license_enforcer: failed to create M2M config")
					}

					// Configure M2M on the usage syncer now that signing key is loaded
					if pm, ok := enterprise.LicenseMonitorFromContext(ctx).(enterprise.PersistentMonitor); ok {
						pm.SetSyncerM2MConfig(m2mAuthCfg)
					}
				}
			}

			// Start license enforcer now that M2M is configured (or if M2M is disabled)
			{
				le := enterprise.LicenseEnforcerFromContext(ctx)
				if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
					le.SetDB(initRes.Primary.RW)
				}
				// Offline license file for air-gapped installs (verified
				// against the compiled-in vendor keyring; hot-reloaded).
				if licenseFile := os.Getenv("EVS_LICENSE_FILE"); licenseFile != "" {
					le.SetLicenseFile(licenseFile)
				}
				le.Start(ctx.Done())
				if m2mConfigured {
					logger.Info("license_enforcer: started with M2M authentication")
				} else {
					logger.Info("license_enforcer: started without M2M authentication (will use trial mode)")
				}

				// Refresh the license monitor now that the enforcer has fetched fresh state.
				enterprise.LicenseMonitorFromContext(ctx).Refresh()
				logger.Debug("license_monitor: refreshed after enforcer fetch")
			}
		}

		// Note: No need to create pending records - activation is done via Admin UI
		// which calls ActivateGatewayInstance -> StoreActivation to create the record
		instanceMgr.Start(ctx)

		// Check for instance data tampering/loss: if device was previously activated
		// but local instance data is missing, emit an audit event for tracking.
		// Uses device fingerprint (not activation token) to detect this scenario.
		go func() {
			deviceStatus, err := instanceMgr.CheckInstanceDataOnStartup(ctx)
			if err != nil {
				logger.WithError(err).Debug("instance data check: failed to verify device status")
				return
			}
			if deviceStatus != nil && deviceStatus.IsBound() {
				// Device was previously activated but local data is missing
				// Dispatch event for audit trail (async, non-blocking)
				if sys, sysErr := cqrs.GetSystemFromContext(ctx); sysErr == nil && sys != nil && sys.CommandBus != nil {
					fingerprint := ""
					if fp, ok := ctx.Value(contextkeys.DeviceFingerprint).(string); ok {
						fingerprint = fp
					}
					cmd := licenseCmd.NewInstanceDataMissingCommand(
						fingerprint, // Use device fingerprint as identifier (activation token is gone)
						deviceStatus.BoundInstanceID,
						fingerprint,
						"", // traceID
					)
					if dispatchErr := sys.CommandBus.Dispatch(ctx, cmd); dispatchErr != nil {
						logger.WithError(dispatchErr).Warn("instance data check: failed to dispatch audit event")
					} else {
						logger.Info("instance data check: audit event dispatched for missing instance data")
					}
				}
				logger.Warn("license: instance data missing but device was previously activated - operating in free mode until re-activated")
			}
		}()

		// Inject instance manager into context for Gateway service
		ctx = context.WithValue(ctx, contextkeys.InstanceManager, instanceMgr)
		enterprise.SetGlobalInstanceManager(instanceMgr)

		// If already activated, set the instance ID on the monitor
		if info, err := instanceMgr.GetActiveInstance(ctx); err == nil && info != nil && info.InstanceID != "" {
			monitor := enterprise.LicenseMonitorFromContext(ctx)

			// Get tenant_id from enforcer's cached state (already fetched during Start())
			tenantID := ""
			if cached := enterprise.LicenseEnforcerFromContext(ctx).GetCached(); cached != nil && cached.TenantId != "" {
				tenantID = cached.TenantId
			}
			monitor.SetOrganizationAndInstanceID(tenantID, info.InstanceID)
			logger.WithFields("instance_id", info.InstanceID, "tenant_id", tenantID).Debug("license_monitor: set IDs from stored activation")

			// Set M2M credentials for real-time spend limit updates and usage syncing
			var signingKey []byte
			if sys, err := cqrs.GetSystemFromContext(ctx); err == nil && sys != nil && sys.QueryBus != nil {
				credsQuery := licenseQuery.GetInstanceCredentials{}
				if resp, err := sys.QueryBus.Execute(ctx, credsQuery); err == nil && resp != nil {
					var credsAny interface{} = resp
					if qr, ok := resp.(*query.Response); ok && qr != nil {
						credsAny = qr.Data
					}
					if creds, ok := credsAny.(*licenseQuery.InstanceCredentials); ok && creds != nil {
						signingKey = creds.SigningKey
					}
				}
			}

			if len(signingKey) > 0 {
				monitor.SetM2MCredentials(signingKey)
			}
			if pm, ok := monitor.(enterprise.PersistentMonitor); ok && info.RefreshToken != "" {
				pm.SetSyncerCredentials(info.InstanceID, info.RefreshToken, signingKey)
			}
			logger.WithFields("has_signing_key", len(signingKey) > 0).Debug("license_monitor: set M2M credentials for spend tracking")
		}

		// Inject cloud public key for callback verification (if configured)
		if svcCfg, err := servicescfg.Load(""); err == nil && svcCfg != nil {
			if cloudPubKey := svcCfg.Services.Gateway.Callback.CloudPublicKey; cloudPubKey != "" {
				ctx = context.WithValue(ctx, contextkeys.CloudPublicKey, cloudPubKey)
				logger.Info("gateway: cloud callback public key configured")
			}
		}
	}

	// Thread validated configs via context for downstream behavior (SSE, streaming defaults).
	if config != nil {
		ctx = context.WithValue(ctx, contextkeys.GatewayConfig, config.Gateway)
		ctx = context.WithValue(ctx, contextkeys.ServerConfig, config.Server)
		ctx = context.WithValue(ctx, contextkeys.FeaturesConfig, config.Features)
	}

	// Initialize runtime config service for hot-reload configuration
	// This service caches config in memory and subscribes to update events
	var runtimeConfigEventBus internalevents.Bus
	var runtimeConfigSvc *rtconfig.Service
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		runtimeConfigEventBus = internalevents.NewInMemoryBus()
		runtimeConfigSvc = rtconfig.NewService(initRes.Primary.RW, runtimeConfigEventBus)
		if err := runtimeConfigSvc.Start(ctx); err != nil {
			logger.Warnf("runtime_config: failed to start service, using static config: %v", err)
		} else {
			ctx = context.WithValue(ctx, contextkeys.RuntimeConfigService, runtimeConfigSvc)
			logger.Info("runtime_config: service started with hot-reload support")
			// Inject rtconfig into every gorilla mux request so the
			// featurePathGate, runtime CORS, and rate-limit middleware
			// can read it from r.Context(). Connect interceptor handles
			// the gRPC paths separately via NewRuntimeConfigInjector.
			injectSvc := runtimeConfigSvc
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := context.WithValue(r.Context(), contextkeys.RuntimeConfigService, injectSvc)
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			})
		}
	}

	// Initialize encryption service for API key encryption/decryption
	// Use the same secret as API key hashing (already loaded by viper).
	// In shared gateway mode, per-tenant secrets come from context so skip this check.
	if !sharedMode && apikeylib.GetSecret() == "" {
		return nil, fmt.Errorf("api key hash secret is required; set server.security.api_key_hash_secret in config or EVS_API_KEY_HASH_SECRET env var")
	}

	// Initialize catalog service BEFORE gateway creation for model/provider validation
	// Create catalog repository if database is available
	var catalogRepo *catalogsvc.Repository
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		catalogRepo = catalogsvc.NewRepository(initRes.Primary.RW)
	}

	catalogSvc := catalogsvc.NewService(catalogRepo, embeddedDefaults.Models, embeddedDefaults.Providers)
	if err := catalogSvc.LoadCatalog(); err != nil {
		return nil, fmt.Errorf("failed to load catalog: %w", err)
	}
	logger.Debugf("catalog: version %s loaded from %s", catalogSvc.GetVersion(), catalogSvc.GetSource())
	ctx = context.WithValue(ctx, contextkeys.CatalogService, catalogSvc)

	// Initialize provider mapper for OTEL telemetry
	// This builds a fast lookup cache for model->provider mapping
	providerMapper := telemetry.NewProviderMapper(catalogSvc)
	telemetry.SetGlobalProviderMapper(providerMapper)

	// Initialize global cost calculator with catalog — this is the single source of truth
	// for all cost calculations across gateway, agents, and metrics.
	metrics.SetGlobalCalculator(metrics.NewCostCalculator(catalogSvc))

	// Start background catalog sync only after the local catalog has made the
	// gateway ready. The configured distribution URL applies to every runtime
	// catalog consumer; remote availability is never a startup dependency.
	if config != nil && config.Server != nil && config.Server.Catalog.EnableAutoSync {
		remoteSyncSvc := catalogsvc.NewRemoteSyncServiceWithTrust(
			config.Server.Catalog.RemoteURL,
			catalogdistribution.TrustConfig{
				Channel:          config.Server.Catalog.Channel,
				PublicKey:        config.Server.Catalog.PublicKey,
				PublicKeys:       config.Server.Catalog.PublicKeys,
				RequireSignature: config.Server.Catalog.RequireSignature,
			},
		)
		syncInterval := config.Server.Catalog.SyncInterval
		if syncInterval == "" {
			syncInterval = "5m"
		}
		catalogSvc.StartBackgroundSync(ctx, syncInterval, remoteSyncSvc)
	}

	// Build API with safe defaults if server config is partially missing
	port := uint16(8089)
	externalDomain := "localhost"
	allowedHeaders := []string{"*"}
	if config != nil && config.Server != nil {
		if config.Server.Config.Port != 0 {
			port = uint16(config.Server.Config.Port)
		}
		if config.Server.Config.ExternalDomain != "" {
			externalDomain = config.Server.Config.ExternalDomain
		}
		if config.Server.CORS.AllowedHeaders != nil {
			allowedHeaders = append([]string{}, append(config.Server.CORS.AllowedHeaders, config.Server.Config.InstanceHostHeaders...)...)
		}
	}

	// Pre-register sandbox WebSocket/SSE routes directly on the gorilla mux.
	// These MUST be registered BEFORE the /v1 prefix handler (set up by NewAPI)
	// because WebSocket connections cannot send custom HTTP headers (like API keys)
	// and SSE streams from the admin UI don't include API key headers.
	// Gorilla mux matches routes in registration order, so these specific routes
	// take priority over the generic /v1 prefix handler.
	var sandboxShellHandler, sandboxShellByIDHandler, sandboxLogsHandler, sandboxStatsHandler, sandboxEventsHandler, sandboxFilesHandler, sandboxFileSearchHandler, sandboxSSHInfoHandler http.Handler
	var sandboxShellSessionsHandler, sandboxKillShellSessionHandler, sandboxRestoreHandler, sandboxRecoverHandler http.Handler
	var sandboxStartVerbHandler, sandboxStopVerbHandler, sandboxArchiveVerbHandler, sandboxDeleteVerbHandler http.Handler
	var sandboxFSListHandler, sandboxFSUploadHandler, sandboxFSDownloadHandler, sandboxFSMkdirHandler, sandboxFSDeleteHandler, sandboxFSMoveHandler, sandboxFSPermissionsHandler http.Handler
	var sandboxProcessSessionsHandler, sandboxProcessSessionHandler, sandboxProcessExecHandler, sandboxProcessCommandHandler, sandboxProcessLogsHandler http.Handler
	// Snapshot handlers (POR-75: named sandbox snapshots)
	var snapshotListHandler, snapshotCreateHandler, snapshotGetHandler, snapshotDeleteHandler http.Handler
	// Lifecycle webhook handlers (POR-78: customer-facing outgoing webhooks)
	var lcWebhookListHandler, lcWebhookCreateHandler, lcWebhookDeleteHandler, lcWebhookDeliveriesHandler, lcWebhookTestHandler http.Handler
	var imageBuildHandler http.Handler
	// Image build endpoint (POR-86)
	router.HandleFunc("/v1/images/build", func(w http.ResponseWriter, r *http.Request) {
		if h := imageBuildHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// OTLP export config (POR-90)
	var otlpGetHandler, otlpUpsertHandler, otlpTestHandler http.Handler
	router.HandleFunc("/v1/settings/otlp", func(w http.ResponseWriter, r *http.Request) {
		if h := otlpGetHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/settings/otlp", func(w http.ResponseWriter, r *http.Request) {
		if h := otlpUpsertHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("PUT")
	router.HandleFunc("/v1/settings/otlp/test", func(w http.ResponseWriter, r *http.Request) {
		if h := otlpTestHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// Computer Use (POR-91-94) and LSP (POR-84, POR-85) handlers.
	// Computer Use + LSP are served via ConnectRPC (see agents_service.proto).
	// MCP setup endpoint (POR-95): GET /v1/mcp/config?client=claude
	var mcpConfigHandler http.Handler
	router.HandleFunc("/v1/mcp/config", func(w http.ResponseWriter, r *http.Request) {
		if h := mcpConfigHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("GET")
	// Volumes (POR-77) are served via ConnectRPC / grpc-gateway at
	// /v1/volumes (ListSandboxVolumes / CreateSandboxVolume /
	// DeleteSandboxVolume), not raw mux routes.
	// Tenant-wide sandbox lifecycle SSE — distinct from the per-sandbox
	// events stream above. Wired only when the reconciler flag is on.
	var sandboxLifecycleEventsHandler http.Handler
	var sandboxCommandHandler, sandboxExecuteCodeHandler, sandboxMetricsWatchHandler http.Handler
	var sandboxPingHandler, sandboxCommandInterruptHandler, sandboxCommandStatusHandler, sandboxCommandLogsHandler http.Handler
	var sandboxCreateCodeCtxHandler, sandboxListCodeCtxHandler, sandboxGetCodeCtxHandler, sandboxDeleteCodeCtxHandler, sandboxDeleteCodeCtxByLangHandler http.Handler
	var sandboxInterruptCodeHandler http.Handler
	var sandboxFileInfoHandler, sandboxDeleteFilesHandler, sandboxFilePermissionsHandler, sandboxMoveFilesHandler http.Handler
	var sandboxSearchFilesExecdHandler, sandboxReplaceFileHandler, sandboxUploadFileHandler, sandboxDownloadFileHandler http.Handler
	var sandboxBulkUploadHandler http.Handler
	var sandboxCreateDirsHandler, sandboxDeleteDirsHandler http.Handler
	var sandboxMetricsHandler, sandboxRenewExpirationHandler http.Handler
	var browserStreamHandler, browserStreamByIDHandler http.Handler
	var userInputHandler, stopTurnHandler, agentCapabilitiesHandler http.Handler
	var deployInvokeHandler, deployStreamHandler http.Handler
	var deployInvoker *agentdeploy.Invoker
	var triggerWebhookRouteHandler http.Handler
	sandboxNotReady := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"sandbox not available"}`, http.StatusServiceUnavailable)
	})
	// Computer Use (POR-91-94) is served by the gRPC Gateway via the ComputerUse /
	// ComputerUseInfo RPCs (POST /v1/sandbox/instances/{sandbox_id}/computer/{op=**},
	// GET .../computer), behind the AgentsService interceptor chain.
	router.HandleFunc("/v1/sandbox/{session_id}/shell", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxShellHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})
	router.HandleFunc("/v1/sandbox/{session_id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxLogsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})
	router.HandleFunc("/v1/sandbox/{session_id}/stats/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxStatsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})
	router.HandleFunc("/v1/sandbox/{sandbox_id}/events/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxEventsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})
	// Tenant-wide lifecycle SSE — replaces 1.5–5s polling on the
	// admin sandboxes page with NOTIFY-driven push updates.
	router.HandleFunc("/v1/sandboxes/events", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxLifecycleEventsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"sandbox lifecycle events not configured"}`, http.StatusServiceUnavailable)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{session_id}/files/search", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxFileSearchHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{session_id}/files", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxFilesHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")

	// Persistent shell sessions: list + kill. Same auth path as the
	// shell WebSocket (same-origin cookie or signed SSH key). The
	// session_id in the path is the SANDBOX session, not the shell
	// session — shell session IDs are returned in the list body.
	router.HandleFunc("/v1/sandbox/{session_id}/shell-sessions", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxShellSessionsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{session_id}/shell-sessions/{shell_session_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxKillShellSessionHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")

	// Same persistent shell-session APIs, addressed by concrete sandbox
	// instance ID/name/short-code. The sandboxes UI uses these so multiple
	// sandboxes that share an agent/session lineage don't collapse onto the
	// latest row for that session_id.
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/shell-sessions", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxShellSessionsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/shell-sessions/{shell_session_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxKillShellSessionHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")

	// Customer-facing outgoing lifecycle webhooks.
	router.HandleFunc("/v1/sandbox-webhooks", func(w http.ResponseWriter, r *http.Request) {
		if h := lcWebhookListHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox-webhooks", func(w http.ResponseWriter, r *http.Request) {
		if h := lcWebhookCreateHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox-webhooks/{webhook_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := lcWebhookDeleteHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox-webhooks/{webhook_id}/deliveries", func(w http.ResponseWriter, r *http.Request) {
		if h := lcWebhookDeliveriesHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox-webhooks/{webhook_id}/test", func(w http.ResponseWriter, r *http.Request) {
		if h := lcWebhookTestHandler; h != nil {
			h.ServeHTTP(w, r)
		}
	}).Methods("POST")

	// Sandbox SSH info — registered directly on the gorilla mux to bypass
	// Dynamic resize (POR-83) is served by the gRPC Gateway via the
	// ResizeSandbox RPC (POST /v1/sandbox/instances/{sandbox_id}/resize,
	// google.api.http annotation in agents_service.proto) — auth + ownership
	// enforced by the AgentsService interceptor chain. No manual route needed.

	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/ssh-info", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxSSHInfoHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")

	// Named sandbox snapshots.
	router.HandleFunc("/v1/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if h := snapshotListHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if h := snapshotCreateHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// Restore a sandbox from archive (archived→running).
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/restore", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxRestoreHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// Recover a sandbox from the error state (re-enter convergence
	// toward its desired state; Daytona's recover()).
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/recover", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxRecoverHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// Daytona-style lifecycle verbs (aliases over desired-state):
	// start, stop, archive, and DELETE on the instance itself.
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/start", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxStartVerbHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxStopVerbHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/archive", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxArchiveVerbHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDeleteVerbHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	// File system API (Daytona parity): list/upload/download/mkdir/
	// delete/move/permissions inside the sandbox.
	registerOptionalRoute := func(pattern, method string, h *http.Handler) {
		router.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if handler := *h; handler != nil {
				handler.ServeHTTP(w, r)
			} else {
				sandboxNotReady.ServeHTTP(w, r)
			}
		}).Methods(method)
	}
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/list", "GET", &sandboxFSListHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/upload", "POST", &sandboxFSUploadHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/download", "GET", &sandboxFSDownloadHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/mkdir", "POST", &sandboxFSMkdirHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/delete", "POST", &sandboxFSDeleteHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/move", "POST", &sandboxFSMoveHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/fs/permissions", "POST", &sandboxFSPermissionsHandler)
	// Exec sessions (Daytona process API): persistent shell-state
	// sessions with sync/async command execution and per-command logs.
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions", "POST", &sandboxProcessSessionsHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions", "GET", &sandboxProcessSessionsHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions/{exec_session_id}", "DELETE", &sandboxProcessSessionHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions/{exec_session_id}/exec", "POST", &sandboxProcessExecHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions/{exec_session_id}/commands/{command_id}", "GET", &sandboxProcessCommandHandler)
	registerOptionalRoute("/v1/sandbox/instances/{sandbox_id}/process/sessions/{exec_session_id}/commands/{command_id}/logs", "GET", &sandboxProcessLogsHandler)
	router.HandleFunc("/v1/snapshots/{snapshot_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := snapshotGetHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/snapshots/{snapshot_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := snapshotDeleteHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")

	// NOTE: POST /v1/sandbox/instances/{sandbox_id}/preview-url is served by the
	// gRPC Gateway via GetSandboxPreviewUrl RPC (google.api.http annotation in
	// agents_service.proto). No manual route registration needed.

	// Sandbox shell by sandbox ID or name — allows CLI WebSocket shell access
	// without needing to resolve a session ID first.
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/shell", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxShellByIDHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})

	// Browser stream — WebSocket relay to the browser-streamer sidecar for live viewport streaming.
	router.HandleFunc("/v1/sandbox/{session_id}/browser/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := browserStreamHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})
	router.HandleFunc("/v1/sandbox/instances/{sandbox_id}/browser/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := browserStreamByIDHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	})

	// Sandbox Execution API — SSE streaming routes (pre-middleware, same pattern as stats/logs).
	router.HandleFunc("/v1/sandbox/{sandbox_id}/command", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCommandHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxExecuteCodeHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/metrics/watch", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxMetricsWatchHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")

	// Sandbox Execution API — JSON routes (pre-middleware for simplicity; they use sandbox-id-based auth).
	router.HandleFunc("/v1/sandbox/{sandbox_id}/ping", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxPingHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/command", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCommandInterruptHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/command/status/{cmd_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCommandStatusHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/command/{cmd_id}/logs", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCommandLogsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code/context", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCreateCodeCtxHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code/contexts", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxListCodeCtxHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code/contexts/{context_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxGetCodeCtxHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code/contexts/{context_id}", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDeleteCodeCtxHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code/contexts", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDeleteCodeCtxByLangHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/code", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxInterruptCodeHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/info", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxFileInfoHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDeleteFilesHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/permissions", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxFilePermissionsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/mv", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxMoveFilesHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	// LSP endpoints (POR-84/85) are served by the gRPC Gateway via the
	// SandboxLSP / SandboxLSPInfo RPCs (POST /v1/sandbox/instances/{sandbox_id}/lsp/{lang}/{op},
	// GET .../lsp), behind the AgentsService interceptor chain.

	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/search", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxSearchFilesExecdHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	// content-search and global-replace are served by the gRPC Gateway via the
	// ContentSearch / GlobalReplace RPCs (POST /v1/sandbox/instances/{sandbox_id}/files/...,
	// agents_service.proto) — auth + ownership via the AgentsService interceptor.
	// Bulk upload stays a raw handler (multipart, not expressible as ConnectRPC).
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/bulk-upload", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxBulkUploadHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/replace", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxReplaceFileHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/upload", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxUploadFileHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/files/download", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDownloadFileHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/directories", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxCreateDirsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/directories", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxDeleteDirsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("DELETE")
	router.HandleFunc("/v1/sandbox/{sandbox_id}/metrics", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxMetricsHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("GET")
	// metrics/history and metrics/batch are served by the gRPC Gateway via the
	// SandboxMetricsHistory / SandboxMetricsBatch RPCs (agents_service.proto),
	// behind the AgentsService interceptor chain. The point-in-time /metrics and
	// the SSE /metrics/watch stream stay raw (streaming).
	router.HandleFunc("/v1/sandbox/{sandbox_id}/renew-expiration", func(w http.ResponseWriter, r *http.Request) {
		if h := sandboxRenewExpirationHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			sandboxNotReady.ServeHTTP(w, r)
		}
	}).Methods("POST")

	// User input (ask_user) endpoint — registered directly on the gorilla mux
	// to bypass API key validation (admin UI doesn't send API key headers).
	router.HandleFunc("/v1/agents/sessions/{session_id}/user-input", func(w http.ResponseWriter, r *http.Request) {
		if h := userInputHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"agent session manager not available"}`, http.StatusServiceUnavailable)
		}
	}).Methods("POST")
	// Turn stop endpoint — registered directly for admin UI compatibility.
	router.HandleFunc("/v1/agents/sessions/{session_id}/turns/stop", func(w http.ResponseWriter, r *http.Request) {
		if h := stopTurnHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"agent session manager not available"}`, http.StatusServiceUnavailable)
		}
	}).Methods("POST")
	// Agent capabilities endpoint — registered directly for admin UI compatibility.
	router.HandleFunc("/v1/agents/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if h := agentCapabilitiesHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"web_search_available":false}`))
		}
	}).Methods("GET")

	// Agent deployment invoke endpoints — registered before api.NewAPI() so they
	// take precedence over the /v1 gRPC-gateway catch-all. These use their own
	// Bearer-token auth (deployment API keys), not admin API keys.
	router.HandleFunc("/v1/deploy/{agent_id}/invoke", func(w http.ResponseWriter, r *http.Request) {
		if h := deployInvokeHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"agent deployment handler not available"}`, http.StatusServiceUnavailable)
		}
	}).Methods("POST", "OPTIONS")
	router.HandleFunc("/v1/deploy/{agent_id}/stream", func(w http.ResponseWriter, r *http.Request) {
		if h := deployStreamHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"agent deployment handler not available"}`, http.StatusServiceUnavailable)
		}
	}).Methods("POST", "OPTIONS")

	// Agent trigger webhook endpoint — registered before api.NewAPI() so it
	// takes precedence over the /v1 gRPC-gateway catch-all. Uses HMAC auth.
	router.HandleFunc("/v1/triggers/webhook/{path}", func(w http.ResponseWriter, r *http.Request) {
		if h := triggerWebhookRouteHandler; h != nil {
			h.ServeHTTP(w, r)
		} else {
			http.Error(w, `{"error":"trigger webhook handler not available"}`, http.StatusServiceUnavailable)
		}
	}).Methods("POST")

	apis, err := api.NewAPI(ctx, port, externalDomain, allowedHeaders, tlsConfig, router)
	if err != nil {
		return nil, err
	}

	// Wire readiness/health checks: in hybrid, ping both Postgres and ClickHouse
	if config != nil && strings.ToLower(config.Database.Mode) == "hybrid" {
		pgDSN := config.Database.Postgres.DSN
		chDSN := config.Database.ClickHouse.DSN
		apis.SetHealth(dbHealth{check: func(c context.Context) error {
			ctxPing, cancel := context.WithTimeout(c, 3*time.Second)
			defer cancel()
			if pgDSN == "" {
				return fmt.Errorf("postgres dsn missing for hybrid health")
			}
			pgConn, err := database.Open(ctxPing, database.Config{Type: database.TypePostgres, DSN: pgDSN})
			if err != nil {
				return err
			}
			defer pgConn.Close(ctxPing)
			if pgConn.RW == nil {
				return fmt.Errorf("postgres connection not initialized")
			}
			if err := pgConn.RW.PingContext(ctxPing); err != nil {
				return err
			}
			if chDSN == "" {
				return fmt.Errorf("clickhouse dsn missing for hybrid health")
			}
			chConn, err := database.Open(ctxPing, database.Config{Type: database.TypeClickHouse, DSN: chDSN})
			if err != nil {
				return err
			}
			defer chConn.Close(ctxPing)
			if chConn.RW == nil {
				return fmt.Errorf("clickhouse connection not initialized")
			}
			if err := chConn.RW.PingContext(ctxPing); err != nil {
				return err
			}
			return nil
		}})
	}

	// Initialize registry BEFORE registering services so all services get proper interceptors
	serviceRegistry := registry.New(registry.AuthPolicy{
		M2MServices: []string{
			"/everstack.gateway.v1.GatewayService/",
		},
		JWTServices: []string{""},
	})
	// isManagedGateway, not sharedMode: this gates LocalTenantInterceptor, and a
	// cloud gateway handed a platform DSN rather than a pre-opened SharedDB
	// still serves other people's tenants. Keying it off sharedMode alone left
	// the Connect path injecting a local tenant into every unauthenticated
	// request, which is how internal spans ended up stamped with an id that
	// exists in no database. The HTTP router's equivalent was fixed in #449;
	// this is the second injector.
	serviceRegistry.SetManagedMode(isManagedGateway(sharedMode))
	if initRes != nil && initRes.Primary != nil {
		serviceRegistry.SetDatabase(initRes.Primary, nil)
		// Wire the central ReBAC enforcement point. No-op (nil) unless
		// EVS_AUTHZ_ENABLED=true; defaults to shadow mode (log-only) so it can
		// be validated against real traffic before EVS_AUTHZ_ENFORCE=true.
		serviceRegistry.SetAuthzInterceptor(registry.BuildAuthzInterceptor(initRes.Primary.RW))
	}
	serviceRegistry.SetCLIAuthorizationDB(cookieSessionAuthDB)
	apis.SetRegistry(serviceRegistry)
	// Provide shared license enforcer/policy to Connect interceptor registry.
	// The registry expects concrete types — unwrap from the adapter in EE builds.
	if reg := apis.GetRegistry(); reg != nil {
		type enforcerUnwrapper interface {
			Unwrap() *middleware.LicenseEnforcer
		}
		if u, ok := enterprise.LicenseEnforcerFromContext(ctx).(enforcerUnwrapper); ok {
			reg.SetLicense(u.Unwrap(), policy.FromGlobal())
		}
		type monitorUnwrapper interface {
			Unwrap() *licensemonitor.Monitor
		}
		if u, ok := enterprise.LicenseMonitorFromContext(ctx).(monitorUnwrapper); ok {
			reg.SetLicenseMonitor(u.Unwrap())
		}
	}

	if err := apis.RegisterService(ctx, everstackv1.CreateServer(apis.ListGrpcMethods, apis.ListGrpcServices)); err != nil {
		return nil, err
	}

	// Initialize provider and API key repositories BEFORE creating gateway server
	// so the gateway can load from database instead of YAML
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		providerRepo := provider_config.NewRepository(initRes.Primary.RW)
		apiKeyRepo := provider_api_keys.NewPostgresRepository(initRes.Primary.RW)

		// Add repositories to context for Gateway server to use
		ctx = context.WithValue(ctx, contextkeys.ProviderRepo, providerRepo)
		ctx = context.WithValue(ctx, contextkeys.APIKeyRepo, apiKeyRepo)

		// Add PrimaryDB to context for toolloop/functions to use
		ctx = context.WithValue(ctx, contextkeys.PrimaryDB, initRes.Primary.RW)

		// Expose the billing/platform DB to the gateway handler. cookieSessionAuthDB
		// is already resolved to the platform pool (EVS_PLATFORM_DSN) when set, else
		// the primary pool for CE — exactly the DB that owns the billing.* schema.
		// Gateway-side wallet / sandbox-entitlement lookups must use this, not the
		// tenant gateway PrimaryDB, which lacks billing.* .
		if cookieSessionAuthDB != nil {
			ctx = context.WithValue(ctx, contextkeys.BillingDB, cookieSessionAuthDB)
		}
	}

	gwServer := gateway.CreateServerWithContext(ctx, config.Gateway, config.Features)

	// Set isolation availability in context for functions service to check
	isolationAvailable := false
	if tlm := gwServer.GetToolLoopManager(); tlm != nil && tlm.HasIsolationBackend() {
		isolationAvailable = true
	}
	ctx = context.WithValue(ctx, contextkeys.IsolationAvailable, isolationAvailable)

	if err := apis.RegisterService(ctx, gwServer); err != nil {
		return nil, err
	}

	// Initialize fast-path engine with cache manager if enabled
	if config.Features.FastPath.Enabled {
		logger.Info("Initializing fast-path engine with unified cache manager")

		engineCfg := fastpath.EngineConfig{
			Features: config.Features.FastPath,
		}

		// Get router from gateway server for embedding model resolution
		gwRouter := gwServer.GetRouter(ctx)

		// Create engine with cache manager (passes router for embedding resolution)
		var engine *fastpath.Engine
		var err error

		// Debug logging
		logger.WithFields(
			"router_available", gwRouter != nil,
			"cache_config_available", config.Cache != nil,
		).Debug("Checking cache manager prerequisites")

		if config.Cache != nil {
			logger.WithFields(
				"cache_enabled", config.Cache.Enabled,
				"cache_type", config.Cache.Type,
				"semantic_enabled", config.Cache.Semantic.Enabled,
				"embedding_model", config.Cache.Semantic.Embedding.Model,
			).Info("Cache configuration loaded")
		}

		if gwRouter != nil && config.Cache != nil {
			// Adapt the gateway router to the cache router interface
			cacheRouter := cache.NewRouterAdapter(gwRouter)
			// Reuse the existing Redis client to avoid duplicate connections
			engine, err = fastpath.NewEngineWithCacheManagerAndRedis(engineCfg, *config.Cache, cacheRouter, redisClient)
			if err != nil {
				logger.WithError(err).Error("Failed to initialize fast-path engine with cache manager, continuing without it")
			}
		} else {
			logger.WithFields(
				"router_nil", gwRouter == nil,
				"cache_nil", config.Cache == nil,
			).Warn("Skipping cache manager initialization - missing prerequisites")
		}

		// Fallback to legacy engine if cache manager initialization failed
		if engine == nil {
			if err := fastpath.InitGlobalEngine(engineCfg); err != nil {
				logger.WithError(err).Error("Failed to initialize fast-path engine, continuing without it")
			} else {
				engine = fastpath.GetGlobalEngine()
			}
		}

		if engine != nil {
			// Set as global engine
			fastpath.SetGlobalEngine(engine)

			// Inject tracing hooks for proper span hierarchy in semantic cache
			engine.SetSemanticCacheTracingHooks(gateway.NewCacheTracingHooks())

			// Per-tenant cache gate: lets a tenant disable response
			// caching from the dashboard without touching the global
			// engine. Reads runtime_config.cache.enabled per request.
			if runtimeConfigSvc != nil {
				rcsvc := runtimeConfigSvc
				engine.SetTenantEnabledFn(func(c context.Context) bool {
					return rcsvc.GetCache(contextkeys.ExtractTenantID(c)).Enabled
				})
			}

			// Add engine to context for handlers
			ctx = fastpath.WithEngine(ctx, engine)

			// Start cleanup routine for caches (runs every 5 minutes)
			go func() {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						engine.Cleanup()
					case <-ctx.Done():
						return
					}
				}
			}()

			// Log initialization details
			logFields := map[string]interface{}{
				"json_lib":         fastpath.JSONName,
				"auth_cache_size":  config.Features.FastPath.Auth.BloomFilterSize,
				"exact_cache_size": config.Features.FastPath.Cache.Exact.MaxEntries,
			}

			// Add semantic cache info if enabled
			if config.Cache != nil && config.Cache.Semantic.Enabled {
				logFields["semantic_cache_enabled"] = true
				logFields["embedding_model"] = config.Cache.Semantic.Embedding.Model
				logFields["similarity_threshold"] = config.Cache.Semantic.SimilarityThreshold
				logFields["cache_backend"] = config.Cache.Semantic.Backend
			}

			logger.WithFields(logFields).Info("Fast-path engine with cache manager initialized successfully")
		}
	}

	// Warm the fast-path router cache after gateway server is initialized
	if config.Features.FastPath.Enabled {
		if engine := fastpath.GetGlobalEngine(); engine != nil {
			router := gwServer.GetRouter(ctx)
			registry := gwServer.GetRegistry(ctx)

			if router != nil && registry != nil {
				// Create warming function that extracts routes from the gateway
				warmFn := func() map[string]fastpath.RouteInfo {
					routes := make(map[string]fastpath.RouteInfo)

					// Get all providers from registry
					for providerName, provider := range registry.All() {
						// For each provider, we need to discover which models it supports
						// This is done by checking the provider's catalog or configuration

						// Add provider info
						routes[providerName] = fastpath.RouteInfo{
							ProviderName: providerName,
							ModelName:    providerName, // Use provider name as fallback
							IsCustom:     false,
							Provider:     provider,
						}
					}

					return routes
				}

				engine.WarmRouterCache(warmFn)
				logger.WithFields(
					"models_cached", engine.RouterCache().Size(),
				).Info("Router cache warmed successfully")
			}

			// Pre-warm HTTP connections to providers if enabled
			if config.Features.FastPath.ConnectionPool.PrewarmOnStartup && registry != nil {
				endpoints := make([]httpclient.ProviderEndpoint, 0)

				// Extract provider endpoints from registry
				for providerName := range registry.All() {
					// Map provider names to their base URLs
					// This is a simplified mapping - in production you'd want to get these from config
					var baseURL, healthPath string

					switch providerName {
					case "openai":
						baseURL = "https://api.openai.com"
						healthPath = "/v1/models"
					case "anthropic":
						baseURL = "https://api.anthropic.com"
						healthPath = "/v1/messages"
					case "google":
						baseURL = "https://generativelanguage.googleapis.com"
						healthPath = "/v1/models"
					case "cohere":
						baseURL = "https://api.cohere.ai"
						healthPath = "/v1/models"
					case "mistral":
						baseURL = "https://api.mistral.ai"
						healthPath = "/v1/models"
					default:
						// Skip unknown providers or those without known endpoints
						continue
					}

					endpoints = append(endpoints, httpclient.ProviderEndpoint{
						Name:       providerName,
						BaseURL:    baseURL,
						HealthPath: healthPath,
					})
				}

				if len(endpoints) > 0 {
					prewarmCfg := httpclient.PrewarmConfig{
						ConnectionsPerProvider: config.Features.FastPath.ConnectionPool.PrewarmConnections,
						Timeout:                5 * time.Second,
						RetryInterval:          30 * time.Second,
						MaxRetries:             3,
					}

					// Pre-warm connections synchronously to ensure they're ready
					results := httpclient.PrewarmConnectionsSync(ctx, endpoints, prewarmCfg)

					// Log results
					for _, result := range results {
						if result.SuccessCount > 0 {
							logger.WithFields(
								"provider", result.Provider,
								"connections", result.ConnectionsWarmed,
								"duration_ms", result.Duration.Milliseconds(),
							).Info("Provider connections pre-warmed")
						} else if result.LastError != nil {
							logger.WithFields(
								"provider", result.Provider,
								"error", result.LastError,
							).Warn("Failed to pre-warm provider connections")
						}
					}
				}
			}
		}
	}

	// Register Config validation service (REST via grpc-gateway under /v1)
	{
		// Load schemas from existing files to power validation endpoints
		schemaFiles := map[string]string{
			"config": "cmd/config/gateway/schemas/config.json",
			"models": "cmd/config/gateway/schemas/models.json",
		}
		var cfgServer *configsvc.Server
		var err error
		// Use DB-enabled server if database is available (for runtime config management)
		// If runtime config service is available, use the shared event bus for hot-reload
		if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
			if runtimeConfigEventBus != nil {
				cfgServer, err = configsvc.CreateServerWithSchemasDBAndEventBus(schemaFiles, initRes.Primary.RW, runtimeConfigEventBus)
			} else {
				cfgServer, err = configsvc.CreateServerWithSchemasAndDB(schemaFiles, initRes.Primary.RW)
			}
		} else {
			cfgServer, err = configsvc.CreateServerWithSchemas(schemaFiles)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to initialize config validation service: %w", err)
		}
		if err := apis.RegisterService(ctx, cfgServer); err != nil {
			return nil, err
		}
	}

	// Register ApiKey gRPC service with same context (for CQRS access)
	apikeyServer := api_key.CreateServerWithContext(ctx)
	if err := apis.RegisterService(ctx, apikeyServer); err != nil {
		return nil, err
	}

	// Register Onboarding gRPC service. Persists tenant-scoped launch-center
	// state so it survives a cleared browser cache. Connect-only. Always
	// registered so the ConnectRPC path exists (avoids a 404 in the admin UI);
	// handlers return CodeUnavailable until the Postgres connection is wired.
	onboardingServer := onboardingsvc.CreateServer(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		onboardingServer.SetDB(initRes.Primary.RW)
	}
	if err := apis.RegisterService(ctx, onboardingServer); err != nil {
		return nil, err
	}

	// The auth service issues and the CLI service verifies with the same
	// manager. Hosted instances derive a CLI key from their sealed M2M key;
	// local self-hosted installs can fall back to the session secret.
	cliDeviceTokens := newCLIDeviceTokenManager(
		config,
		embeddedDefaults != nil && embeddedDefaults.AuthProvider != nil,
	)
	serviceRegistry.SetCLIDeviceTokenManager(cliDeviceTokens)

	// Register CLI gRPC service (evs push/sandbox/whoami)
	var cliServer *clisvc.CLIServer
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		cliServer = clisvc.NewCLIServer(initRes.Primary.RW, cliDeviceTokens)
		if err := apis.RegisterService(ctx, cliServer); err != nil {
			return nil, err
		}
	}

	// Register Functions gRPC service (for serverless function management)
	functionsServer := functions.CreateServerWithContext(ctx)
	if err := apis.RegisterService(ctx, functionsServer); err != nil {
		return nil, err
	}

	// Register Workflows gRPC service (for workflow pipeline management)
	// Inject gateway deps (registry + router) for workflow execution engine
	var workflowsDB *sqlx.DB
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		workflowsDB = initRes.Primary.RW
	}
	workflowsServer := workflows.CreateServerWithDeps(ctx, gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx), gwServer.GetToolLoopManager(), workflowsDB)
	if err := apis.RegisterService(ctx, workflowsServer); err != nil {
		return nil, err
	}

	// Register Agents gRPC service (for agent orchestration)
	// Pass CQRS system for session checkpointing and the session manager
	var agentsCqrsSystem *cqrs.System
	if sys, sysErr := cqrs.GetSystemFromContext(ctx); sysErr == nil {
		agentsCqrsSystem = sys
	}
	var agentsDB *sqlx.DB
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		agentsDB = initRes.Primary.RW
	}
	var storageCredentialStore storagecredentials.Store
	if agentsDB != nil {
		configured, credentialErr := newStorageCredentialStore(agentsDB, config)
		if credentialErr != nil {
			logger.WithError(credentialErr).Warn("storage credential backend unavailable")
		} else {
			storageCredentialStore = configured
		}
	}
	if agentsCqrsSystem != nil && agentsCqrsSystem.ProjectionManager != nil {
		agentsCqrsSystem.ProjectionManager.SetStorageCredentialStore(storageCredentialStore)
	}
	// Pass the shared Redis client (if available) to enable cross-instance
	// session routing and approval delivery.
	var agentsRedis *redis.Client
	if redisClient != nil {
		agentsRedis = redisClient.Client()
	}
	agentsServer := agents.CreateServerWithDeps(ctx, gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx), gwServer.GetToolLoopManager(), agentsCqrsSystem, agentsDB, agentsRedis)
	agentsServer.SetStorageCredentialStore(storageCredentialStore)
	agentsServer.SetProviderRefresher(gwServer)

	// Sandbox ownership enforcement: reject (instead of audit-logging)
	// cross-tenant sandbox RPCs. Default stays audit-only so production
	// can observe would-reject logs before the env flip.
	if sandboxOwnershipEnforceEnabled() {
		agentsServer.SetSandboxOwnershipEnforce(true)
		logger.Info("sandbox ownership: hard enforcement enabled")
	}

	// Initialize persistent agent memory store
	if agentsDB != nil {
		agentMemStore := agentmem.NewPostgresStore(agentsDB)
		agentsServer.SetAgentMemoryStore(agentMemStore)
		logger.Info("agent memory: persistent memory store initialized")
	}

	// Initialize branch store for persisting full conversation traces
	if agentsDB != nil {
		branchStore := agentrt.NewBranchStore(agentsDB)
		agentsServer.SetBranchStore(branchStore)
		logger.Info("agent branches: branch store initialized")
	}

	// Initialize agent deployment store, invoker, and HTTP invoke handlers
	if agentsDB != nil {
		revisionStore := agentrevision.NewPostgresStore(agentsDB)
		agentsServer.SetRevisionStore(revisionStore)
		logger.Info("agent revisions: immutable project store initialized")

		deployStore := agentdeploy.NewPostgresStore(agentsDB)
		agentsServer.SetDeploymentStore(deployStore)
		var cmdBus commands.CommandBus
		var qryBus query.QueryBus
		if agentsCqrsSystem != nil {
			cmdBus = agentsCqrsSystem.CommandBus
			qryBus = agentsCqrsSystem.QueryBus
		}
		deployInvoker = agentdeploy.NewInvoker(agentsServer.GetSessionManager(), cmdBus, qryBus)
		dh := agentdeploy.NewHandler(deployStore, deployInvoker)
		deployInvokeHandler = http.HandlerFunc(dh.HandleInvoke)
		deployStreamHandler = http.HandlerFunc(dh.HandleStream)
		logger.Info("agent deployments: invoke endpoints wired at /v1/deploy/{agent_id}/invoke|stream")
	}

	// Initialize agent trigger store, executor, scheduler, webhook handler, event subscriber
	if agentsDB != nil {
		triggerStore := agenttrigger.NewPostgresStore(agentsDB)
		agentsServer.SetTriggerStore(triggerStore)

		var triggerCmdBus commands.CommandBus
		var triggerQryBus query.QueryBus
		if agentsCqrsSystem != nil {
			triggerCmdBus = agentsCqrsSystem.CommandBus
			triggerQryBus = agentsCqrsSystem.QueryBus
		}
		triggerExecutor := agenttrigger.NewExecutor(triggerStore, agentsServer.GetSessionManager(), triggerCmdBus, triggerQryBus)
		agentsServer.SetTriggerExecutor(triggerExecutor)

		// Cron scheduler
		triggerScheduler := agenttrigger.NewScheduler(triggerStore, triggerExecutor, agentsDB)
		go triggerScheduler.Start(ctx)

		// Webhook handler
		triggerWebhookHandler := agenttrigger.NewWebhookHandler(triggerStore, triggerExecutor)
		triggerWebhookRouteHandler = http.HandlerFunc(triggerWebhookHandler.Handle)

		// Event subscriber
		if agentsCqrsSystem != nil && agentsCqrsSystem.EventBus != nil {
			triggerEventSub := agenttrigger.NewEventSubscriber(triggerStore, triggerExecutor, agentsCqrsSystem.EventBus)
			triggerEventSub.Start(ctx)
		}

		logger.Info("agent triggers: scheduler, webhook handler, and event subscriber initialized")
	}

	// Wire user input (ask_user) HTTP handler (pre-registered on router before /v1 middleware chain)
	userInputHandler = http.HandlerFunc(agentsServer.HandleSubmitUserInputHTTP)
	stopTurnHandler = http.HandlerFunc(agentsServer.HandleStopSessionTurnHTTP)
	agentCapabilitiesHandler = http.HandlerFunc(agentsServer.HandleAgentCapabilitiesHTTP)

	// Initialize sandbox backend if Docker or Kubernetes is available
	var sandboxCronScheduler *sandbox.CronScheduler
	var sandboxCfg *validator.SandboxFeaturesConfig
	if config.Features != nil {
		sandboxCfg = &config.Features.Sandbox
	}
	sandboxMgr := initSandboxManager(sandboxCfg, managedGateway)
	var browserPool *browserpool.Pool
	var browserUsageMeter *browserpool.PostgresUsageMeter
	browserPoolEnabled := os.Getenv("EVS_BROWSER_POOL_ENABLED")
	if browserPoolEnabled == "1" || strings.EqualFold(browserPoolEnabled, "true") {
		// Pod caps default low because browser pods share the sandboxes
		// namespace ResourceQuota with sandbox pods. Raise these only after
		// raising that quota, or browser pods will starve sandbox creation.
		poolCfg := browserpool.Config{
			Namespace: getEnvOrDefault("EVS_BROWSER_POOL_NAMESPACE", ""),
			Image:     getEnvOrDefault("EVS_BROWSER_POOL_IMAGE", ""),
		}
		if v, err := strconv.Atoi(os.Getenv("EVS_BROWSER_POOL_MAX_PODS")); err == nil && v > 0 {
			poolCfg.MaxPodsTotal = v
		}
		if v, err := strconv.Atoi(os.Getenv("EVS_BROWSER_POOL_MAX_IDLE_PER_TENANT")); err == nil && v >= 0 {
			poolCfg.MaxIdlePerTenant = v
		}
		pool, err := browserpool.New(poolCfg)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("browser pool: failed to initialize; using sandbox sidecar")
		} else {
			browserPool = pool
			logger.Info("browser pool: standalone Chromium pool enabled")
		}
	}
	if browserPool != nil && agentsDB != nil {
		pricing, err := loadBrowserUsagePricing(sandboxPlansConfigPath())
		if err != nil {
			logger.WithFields("error", err.Error()).
				Warn("browser billing: failed to load pricing; managed allocations will fail closed")
		} else {
			meter, meterErr := browserpool.NewPostgresUsageMeter(agentsDB, pricing)
			if meterErr != nil {
				logger.WithFields("error", meterErr.Error()).
					Warn("browser billing: failed to initialize usage meter; managed allocations will fail closed")
			} else {
				recoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				recoveryErr := meter.RecoverStale(recoveryCtx, time.Now(), browserPool.LeaseTTL())
				cancel()
				if recoveryErr != nil {
					logger.WithFields("error", recoveryErr.Error()).
						Warn("browser billing: failed to recover stale lease windows")
				}
				browserUsageMeter = meter
				SetLiveBrowserUsageMeter(meter)
				logger.Info("browser billing: durable lease meter initialized")
			}
		}
	}
	if browserPool != nil {
		if managedGateway {
			// The shared wrapper below also verifies the tenant's active
			// usage-billing subscription. Until that wrapper is installed,
			// fail closed so a startup/configuration error cannot create
			// unmetered hosted-browser time.
			browserPool.SetUsageRecorder(nil, true)
			browserPool.SetLimitsResolver(nil, true)
		} else {
			// Self-hosted installations can use the browser runtime without
			// Everstack-operated billing. When a DB is present we still retain
			// accurate usage for license telemetry and local visibility.
			browserPool.SetUsageRecorder(browserUsageMeter, false)
		}
	}
	agentsServer.SetBrowserPool(browserPool)
	workflowsServer.SetBrowserPool(browserPool)
	if sandboxMgr != nil {
		// Everstack-hosted compute exposes only the fixed machine catalog used
		// by pricing. Self-hosted runtimes keep custom resource sizing because
		// the customer owns the underlying infrastructure.
		sandboxMgr.SetManagedMachineProfilesRequired(managedGateway)
		if agentsDB != nil {
			sandboxMgr.SetDB(agentsDB)
		}
		// Per-tenant sandbox resource caps from runtime_config.features.sandbox.
		// Tenant values are clamped against the global cap (smaller wins),
		// so a tenant can never exceed the operator-set ceiling.
		if runtimeConfigSvc != nil {
			rcsvc := runtimeConfigSvc
			sandboxMgr.SetTenantCapResolver(func(tenantID string) sandbox.GlobalSandboxConfig {
				feats := rcsvc.GetFeatures(tenantID)
				if feats.Sandbox == nil {
					return sandbox.GlobalSandboxConfig{}
				}
				return sandbox.GlobalSandboxConfig{
					MaxCPU:         feats.Sandbox.MaxCpu,
					MaxMemoryMB:    int64(feats.Sandbox.MaxMemoryMb),
					MaxDiskMB:      int64(feats.Sandbox.MaxDiskMb),
					MaxTimeoutSecs: feats.Sandbox.MaxTimeoutSeconds,
				}
			})
		}
		// Wire plan-tier-based idle retention resolver from the license monitor.
		type monitorUnwrapper interface {
			Unwrap() *licensemonitor.Monitor
		}
		if u, ok := enterprise.LicenseMonitorFromContext(ctx).(monitorUnwrapper); ok {
			m := u.Unwrap()
			sandboxMgr.SetRetentionResolver(licensemonitor.NewSandboxRetentionAdapter(m))
			sandboxMgr.SetTrooperLimitResolver(licensemonitor.NewTrooperLimitAdapter(m))
		}

		// Per-tenant plan tier resolver — billing uses this to apply
		// the configured SandboxPricingConfig.TierMultipliers discount.
		// Source of truth is everstack.organizations.plan_tier (same
		// field the auth and control-plane code paths consult). For
		// gateways without a DB the resolver returns "" and the
		// manager defaults to "free" / multiplier 1.0 — no behaviour
		// change.
		if agentsDB != nil {
			tdb := agentsDB
			billingDB := tdb
			if managedGateway && cookieSessionAuthDB != nil {
				billingDB = cookieSessionAuthDB
			}
			sandboxMgr.SetTenantTierResolver(func(tenantID string) string {
				if tenantID == "" {
					return ""
				}
				lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				organization, err := billingidentity.ResolveActiveOrganization(lookupCtx, billingDB, tenantID)
				if err != nil {
					return ""
				}
				return organization.Tier
			})

			// Managed cloud has no instance-wide license monitor: each request
			// belongs to a different tenant. Resolve the sandbox billing
			// entitlement from active Stripe billing or the tenant's one-time
			// starter credit, and establish the cumulative meter before
			// allocation. Enterprise is covered by its negotiated contract.
			if managedGateway {
				billingUsageURL := managedBillingUsageURL(embeddedDefaults)
				if browserPool != nil {
					limitsByTier, limitsErr := loadBrowserTenantLimits(sandboxPlansConfigPath())
					if limitsErr != nil {
						logger.WithFields("error", limitsErr.Error()).
							Warn("browser limits: failed to load plan limits; managed allocations will fail closed")
					} else {
						browserPool.SetLimitsResolver(func(limitCtx context.Context, tenantID string) (browserpool.TenantLimits, error) {
							queryCtx, cancel := context.WithTimeout(limitCtx, 2*time.Second)
							defer cancel()
							organization, err := billingidentity.ResolveActiveOrganization(queryCtx, billingDB, tenantID)
							if err != nil {
								return browserpool.TenantLimits{}, fmt.Errorf("resolve organization browser plan: %w", err)
							}
							limits, ok := limitsByTier[organization.Tier]
							if !ok {
								return browserpool.TenantLimits{}, fmt.Errorf("browser limits are not configured for plan %q", organization.Tier)
							}
							return limits, nil
						}, true)
					}
				}
				reporter := newSharedSandboxBillingReporter(tdb, billingDB, sandboxMgr, billingUsageURL)
				if strings.TrimSpace(billingUsageURL) != "" {
					billingHTTPClient, clientErr := newManagedBillingHTTPClient()
					if clientErr != nil {
						logger.WithFields("error", clientErr.Error()).
							Warn("usage billing: M2M client unavailable; managed allocation will fail closed")
						reporter.endpoint = ""
					} else {
						reporter.client = billingHTTPClient
					}
				}
				if browserUsageMeter != nil {
					reporter.SetBrowserUsage(browserUsageMeter)
				}
				if browserPool != nil && browserUsageMeter != nil {
					browserPool.SetUsageRecorder(&sharedBrowserUsageRecorder{
						db:       tdb,
						reporter: reporter,
						meter:    browserUsageMeter,
					}, true)
				}
				if strings.TrimSpace(billingUsageURL) == "" {
					logger.Warn("usage billing: internal endpoint missing; public-tier sandbox and browser allocation will fail closed")
				} else {
					go reporter.Run(ctx)
					logger.WithFields("endpoint", billingUsageURL, "source_id", reporter.instanceID).
						Info("usage billing: shared sandbox and browser reporter started")
				}

				sandboxMgr.SetSandboxBillingResolver(func(tenantID string) bool {
					lookupCtx, cancel := context.WithTimeout(context.Background(), sharedSandboxBillingRequestTimeout)
					defer cancel()
					enabled, err := resolveSharedSandboxBilling(lookupCtx, billingDB, reporter, tenantID)
					if err != nil {
						logger.WithFields("tenant_id", tenantID, "error", err.Error()).
							Warn("sandbox billing: entitlement lookup failed closed")
						return false
					}
					return enabled
				})
			}
		}
		agentsServer.SetSandboxManager(sandboxMgr)
		workflowsServer.SetSandboxManager(sandboxMgr)
		if cliServer != nil {
			cliServer.SetSandboxManager(sandboxMgr)
		}
		// Register the manager for live sandbox network accrual in the usage
		// syncer's resource-count provider (set up earlier; reads this atomically).
		SetLiveSandboxManager(sandboxMgr)

		// Start the persistent-shell idle reaper. The reaper walks
		// every running sandbox once per Interval, asks the fcagent
		// for its tmux sessions, and kills any that have been idle
		// (zero attached clients, no activity) past IdleTTL. Without
		// this, agents and forgotten browser tabs accumulate tmux
		// sessions indefinitely. Env knobs:
		//   EVERSTACK_SHELL_SESSION_IDLE_TTL          (default 24h)
		//   EVERSTACK_SHELL_SESSION_REAPER_INTERVAL   (default 1h)
		//   EVERSTACK_SHELL_SESSION_REAPER_ENABLED    (default true)
		go sandbox.NewShellSessionReaper(sandboxMgr, sandbox.NewShellSessionReaperFromEnv()).Run(ctx)

		// Wire the shared Redis-backed sandbox registry so GetLinked and
		// the persistent-agent reconciler can recover linked sandboxes
		// across process restarts and across control-plane replicas
		// without losing user state.
		if agentsRedis != nil {
			sandboxMgr.SetRegistry(sandbox.NewRedisRegistry(agentsRedis))
			logger.Info("sandbox_manager: redis-backed registry enabled")

			// Wire the Redis lifecycle bus so the agents server broadcasts
			// status transitions (recovery, idle, provisioning) to per-agent
			// channels. The UI subscribes via /v1/agents/{id}/lifecycle/events
			// to render live status badges instead of polling.
			agentsServer.SetLifecycleBus(agentrt.NewRedisLifecycleBus(agentsRedis))
			logger.Info("agents: redis lifecycle bus enabled")
		}

		// Phase 2: R2-backed snapshot/restore for host-loss survival.
		// Env-driven config keeps the schema change small for the first
		// ship — YAML/validator wiring lands when we promote this out
		// of "experimental" status.
		if endpoint := os.Getenv("EVS_SANDBOX_R2_ENDPOINT"); endpoint != "" {
			bucket := os.Getenv("EVS_SANDBOX_R2_BUCKET")
			accessKey := os.Getenv("EVS_SANDBOX_R2_ACCESS_KEY")
			secretKey := os.Getenv("EVS_SANDBOX_R2_SECRET_KEY")
			if bucket == "" || accessKey == "" || secretKey == "" {
				logger.Warn("sandbox: EVS_SANDBOX_R2_ENDPOINT set but bucket/access_key/secret_key missing; snapshots disabled")
			} else {
				objectStore, err := s3pkg.New(ctx, s3pkg.Config{
					Endpoint:        endpoint,
					Region:          os.Getenv("EVS_SANDBOX_R2_REGION"), // "auto" for R2
					Bucket:          bucket,
					AccessKeyID:     accessKey,
					SecretAccessKey: secretKey,
				})
				if err != nil {
					logger.WithError(err).Warn("sandbox: failed to construct R2 client; snapshots disabled")
				} else {
					sandboxMgr.SetSnapshotStore(snapshot.NewFromObjectStore(objectStore, bucket))
					sandboxMgr.StartSnapshotGC()
					logger.WithFields("bucket", bucket).Info("sandbox: R2 snapshot store enabled")

					// Persistent volumes share the sandbox R2 store. Wire it
					// for create/delete purge + start the hourly usage-metering
					// sweep (POR-77). Meter is nil (no-op) if store/bucket unset.
					//
					// REQUIREMENT: for metering/purge to see per-tenant volume
					// buckets (created by the CF provisioner, not this store's
					// key), the EVS_SANDBOX_R2_* key MUST be account-scoped — a
					// key scoped only to the snapshot bucket makes every volume
					// measurement return 0 (under-billing, logged not errored).
					agentsServer.SetVolumeStore(objectStore, bucket)
					if vm := agents.NewVolumeUsageMeter(agentsDB, objectStore, bucket, sandboxMgr.EffectiveDiskRateUSD); vm != nil {
						go vm.Run(ctx)
						logger.WithFields("bucket", bucket).Info("sandbox: volume usage meter started")
					}

					// Per-tenant R2 bucket/token provisioner for everstack-volume
					// mounts. Needs a Cloudflare account id + API token (token +
					// bucket create/list perms); nil when unset (attach disabled).
					if prov := volstore.New(agentsDB, volstore.Config{
						AccountID: os.Getenv("EVS_SANDBOX_R2_ACCOUNT_ID"),
						APIToken:  os.Getenv("EVS_SANDBOX_R2_CF_API_TOKEN"),
					}, storageCredentialStore); prov != nil {
						agentsServer.SetVolumeProvisioner(prov)
						logger.Info("sandbox: volume bucket provisioner enabled")
					}
				}
			}
		}

		// Sandbox lifecycle reconciler — phase 1 + phase 2.
		// Flag-gated by EVS_SANDBOX_RECONCILER_ENABLED. When enabled,
		// CreateSandbox writes a pending row and returns in <50ms; a
		// leader-elected loop in this gateway pod converges the row
		// through creating → running. See docs/design/sandbox-reconciler.md.
		// Existing sync sandboxMgr.GetOrCreate path stays as the fallback
		// until phase 3 cutover.
		if reconcilerEnabled() && agentsDB != nil {
			repo := sandboxlc.NewRepository(agentsDB)
			// The reconciler drives the MANAGER's executor methods, not
			// the raw backend: stop snapshots /workspace before destroy,
			// revive restores it, archive moves it to object storage.
			// Wiring the raw backend here (the original phase-1 shape)
			// silently lost workspace data on every reconciler-driven
			// stop/revive cycle.
			rec := sandboxlc.NewReconciler(repo, sandboxMgr, agentsDB)
			agentsServer.SetLifecycleRepo(repo)

			// One-shot backfill: assign short_code to legacy rows that
			// predate the column. Cheap when already populated (single
			// SELECT), so safe to run on every gateway boot. Detached
			// from boot path so a slow DB doesn't delay the listener.
			go func() {
				bfCtx, bfCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer bfCancel()
				n, err := repo.BackfillShortCodes(bfCtx)
				if err != nil {
					logger.WithError(err).Warn("sandbox: short_code backfill failed")
					return
				}
				if n > 0 {
					logger.WithFields("rows", n).Info("sandbox: short_code backfill complete")
				}
			}()
			go func() {
				if err := rec.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("sandbox_reconciler: stopped with error")
				}
			}()
			log.Printf("[sandbox] reconciler enabled (leader_id=%s, tick=%s)",
				rec.LeaderID, rec.Interval)

			// Phase 2: LISTEN/NOTIFY → SSE bridge. One PG LISTEN per
			// gateway pod, fanned out to per-tenant SSE subscribers via
			// /v1/sandboxes/events. Replaces the FE's 1.5–5s polling.
			if pgDSN := postgresDSNForListener(config); pgDSN != "" {
				bus := sandboxlc.NewEventBus(pgDSN)
				agentsServer.SetEventBus(bus)
				go func() {
					if err := bus.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						logger.WithError(err).Error("sandbox_eventbus: stopped with error")
					}
				}()
				log.Printf("[sandbox] event bus enabled (LISTEN sandbox_events)")
			} else {
				log.Printf("[sandbox] event bus skipped: no postgres DSN available for LISTEN")
			}

			// Phase 1.5: IdleChecker. Separate component from the
			// reconciler — scans running rows, writes desired_state=
			// 'sleeping' on idle ones, and lets the reconciler converge.
			// Decoupled so policy changes don't touch the state machine.
			idle := sandboxlc.NewIdleChecker(agentsDB, repo)
			// Persistent troopers and keep-warm sandboxes have idle
			// policy the SQL pass can't express (active-session and
			// trigger checks); the manager supplies those candidates.
			idle.SpecialCandidates = sandboxMgr.IdleSleepCandidatesSpecial
			go func() {
				if err := idle.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("sandbox_idle_checker: stopped with error")
				}
			}()
			log.Printf("[sandbox] idle checker enabled (interval=%s)", idle.Interval)

			// Lifecycle webhook notifier: scans recently-changed sandboxes
			// and delivers outgoing webhooks to customer-registered endpoints.
			if agentsDB != nil {
				lcRepo := sandboxlcwebhooks.NewRepository(agentsDB)
				notifier := sandboxlcwebhooks.NewNotifier(lcRepo, agentsServer.EventBus())
				go notifier.Run(ctx)
				logger.Info("sandbox_lc_webhook_notifier: started")
			}

			// ArchiveChecker: scans sleeping rows past auto_archive_after_days
			// and marks them archived; also auto-deletes rows past
			// auto_delete_after_days. Runs every 5 min, leader-elected.
			archiveChecker := sandboxlc.NewArchiveChecker(agentsDB, repo)
			go func() {
				if err := archiveChecker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("sandbox_archive_checker: stopped with error")
				}
			}()
			log.Printf("[sandbox] archive checker enabled (interval=%s)", archiveChecker.Interval)

			// HealthSweeper: detects VMs that died outside our control
			// (guest crash, OOM, host loss) by probing running rows via
			// backend Status. Confirmed-dead rows move to the recoverable
			// 'error' state with error_reason='vm_not_found' instead of
			// staying 'running' forever.
			healthSweeper := sandboxlc.NewHealthSweeper(agentsDB, repo, sandboxMgr)
			go func() {
				if err := healthSweeper.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("sandbox_health_sweeper: stopped with error")
				}
			}()
			log.Printf("[sandbox] health sweeper enabled (interval=%s)", healthSweeper.Interval)

			// RecoveryChecker: the automatic counterpart to a manual
			// Recover(). Re-converges rows the HealthSweeper moved to the
			// recoverable 'error' state (e.g. error_reason='vm_not_found')
			// back toward their preserved desired_state, with bounded
			// per-row backoff and an attempt cap. Without this a dead VM
			// the user wanted running parks in 'error' for 365 days.
			recoveryChecker := sandboxlc.NewRecoveryChecker(agentsDB, repo)
			go func() {
				if err := recoveryChecker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.WithError(err).Error("sandbox_recovery_checker: stopped with error")
				}
			}()
			log.Printf("[sandbox] recovery checker enabled (interval=%s, max_attempts=%d)",
				recoveryChecker.Interval, recoveryChecker.MaxAttempts)
		}

		// Wire trooper manager for trooper provisioning/lifecycle
		agentsServer.SetTrooperManager(trooper.NewManager(sandboxMgr, agentsDB))
		// Wire sandbox cleanup and plan tier resolver into session manager
		if mgr := agentsServer.GetSessionManager(); mgr != nil {
			mgr.SetSandboxCleanup(sandboxMgr.Destroy)
			// Wire plan-tier resolver for session hibernation timeouts
			type monitorUnwrapper2 interface {
				Unwrap() *licensemonitor.Monitor
			}
			if u, ok := enterprise.LicenseMonitorFromContext(ctx).(monitorUnwrapper2); ok {
				adapter := licensemonitor.NewTrooperLimitAdapter(u.Unwrap())
				mgr.SetPlanTierResolver(adapter.ResolvePlanTier)
			}
		}
		// Delay startup recovery until the server and sandbox backend have had a
		// chance to finish booting. On restart, this prevents reconciliation from
		// acting on stale persisted state before the reaper and backend are ready.
		go func() {
			const startupRecoveryDelay = 20 * time.Second
			const maxHealthWait = 5 * time.Minute
			select {
			case <-ctx.Done():
				return
			case <-time.After(startupRecoveryDelay):
			}

			// Wait for sandbox backend to become healthy, but don't block forever.
			// If it never becomes healthy, start the reconciliation loop anyway —
			// it skips ticks when the backend is unhealthy.
			healthDeadline := time.After(maxHealthWait)
			backendHealthy := false
			for {
				if err := sandboxMgr.Healthy(ctx); err == nil {
					backendHealthy = true
					break
				}
				logger.Warn("agents: waiting for sandbox backend before startup recovery")
				select {
				case <-ctx.Done():
					return
				case <-healthDeadline:
					logger.Warn("agents: sandbox backend not healthy after timeout, starting reconciliation loop anyway")
					goto startReconciliation
				case <-time.After(5 * time.Second):
				}
			}
		startReconciliation:
			if backendHealthy {
				logger.Info("agents: running startup sandbox recovery sweep")
				sandboxMgr.TriggerRecoverySweep()
			}
			logger.Info("agents: starting persistent agent reconciliation loop")
			agentsServer.ReconcilePersistentAgents(ctx)
		}()

		// Wire sandbox HTTP handlers (pre-registered on router before /v1 middleware chain)
		sandboxShellHandler = http.HandlerFunc(agentsServer.HandleSandboxShell)
		sandboxShellByIDHandler = http.HandlerFunc(agentsServer.HandleSandboxShellByIDOrName)
		sandboxLifecycleEventsHandler = http.HandlerFunc(agentsServer.HandleSandboxEvents)
		sandboxLogsHandler = http.HandlerFunc(agentsServer.HandleSandboxLogsHTTP)
		sandboxStatsHandler = http.HandlerFunc(agentsServer.HandleSandboxStatsHTTP)
		sandboxEventsHandler = http.HandlerFunc(agentsServer.HandleSandboxEventsHTTP)
		sandboxFilesHandler = http.HandlerFunc(agentsServer.HandleSandboxFilesHTTP)
		sandboxFileSearchHandler = http.HandlerFunc(agentsServer.HandleSearchSandboxFilesHTTP)
		sandboxSSHInfoHandler = http.HandlerFunc(agentsServer.HandleSandboxSSHInfoHTTP)
		sandboxShellSessionsHandler = http.HandlerFunc(agentsServer.HandleListSandboxShellSessions)
		sandboxKillShellSessionHandler = http.HandlerFunc(agentsServer.HandleKillSandboxShellSession)
		sandboxRestoreHandler = http.HandlerFunc(agentsServer.HandleRestoreFromArchive)
		sandboxRecoverHandler = http.HandlerFunc(agentsServer.HandleRecoverSandbox)
		sandboxStartVerbHandler = http.HandlerFunc(agentsServer.HandleStartSandbox)
		sandboxStopVerbHandler = http.HandlerFunc(agentsServer.HandleStopSandboxVerb)
		sandboxArchiveVerbHandler = http.HandlerFunc(agentsServer.HandleArchiveSandbox)
		sandboxDeleteVerbHandler = http.HandlerFunc(agentsServer.HandleDeleteSandboxVerb)
		sandboxFSListHandler = http.HandlerFunc(agentsServer.HandleSandboxFSList)
		sandboxFSUploadHandler = http.HandlerFunc(agentsServer.HandleSandboxFSUpload)
		sandboxFSDownloadHandler = http.HandlerFunc(agentsServer.HandleSandboxFSDownload)
		sandboxFSMkdirHandler = http.HandlerFunc(agentsServer.HandleSandboxFSMkdir)
		sandboxFSDeleteHandler = http.HandlerFunc(agentsServer.HandleSandboxFSDelete)
		sandboxFSMoveHandler = http.HandlerFunc(agentsServer.HandleSandboxFSMove)
		sandboxFSPermissionsHandler = http.HandlerFunc(agentsServer.HandleSandboxFSPermissions)
		sandboxProcessSessionsHandler = http.HandlerFunc(agentsServer.HandleProcessSessions)
		sandboxProcessSessionHandler = http.HandlerFunc(agentsServer.HandleProcessSessionDelete)
		sandboxProcessExecHandler = http.HandlerFunc(agentsServer.HandleProcessSessionExec)
		sandboxProcessCommandHandler = http.HandlerFunc(agentsServer.HandleProcessCommandStatus)
		sandboxProcessLogsHandler = http.HandlerFunc(agentsServer.HandleProcessCommandLogs)
		imageBuildHandler = http.HandlerFunc(agentsServer.HandleBuildImage)
		otlpGetHandler = http.HandlerFunc(agentsServer.HandleGetOTLPConfig)
		otlpUpsertHandler = http.HandlerFunc(agentsServer.HandleUpsertOTLPConfig)
		otlpTestHandler = http.HandlerFunc(agentsServer.HandleTestOTLPConfig)
		mcpConfigHandler = http.HandlerFunc(agentsServer.HandleMCPConfig)
		// Volumes (POR-77) migrated to ConnectRPC — no raw handler wiring.
		browserStreamHandler = http.HandlerFunc(agentsServer.HandleBrowserStream)

		// Lifecycle webhooks (POR-78)
		if agentsDB != nil {
			lcRepo := sandboxlcwebhooks.NewRepository(agentsDB)
			agentsServer.SetLCWebhookRepository(lcRepo)
			lcWebhookListHandler = http.HandlerFunc(agentsServer.HandleListLCWebhooks)
			lcWebhookCreateHandler = http.HandlerFunc(agentsServer.HandleCreateLCWebhook)
			lcWebhookDeleteHandler = http.HandlerFunc(agentsServer.HandleDeleteLCWebhook)
			lcWebhookDeliveriesHandler = http.HandlerFunc(agentsServer.HandleListLCWebhookDeliveries)
			lcWebhookTestHandler = http.HandlerFunc(agentsServer.HandleTestLCWebhook)
		}
		// Named snapshots
		if agentsDB != nil {
			snapRepo := sandboxsnapshots.NewRepository(agentsDB)
			agentsServer.SetSnapshotRepository(snapRepo)
			snapshotListHandler = http.HandlerFunc(agentsServer.HandleListSnapshots)
			snapshotCreateHandler = http.HandlerFunc(agentsServer.HandleCreateSnapshot)
			snapshotGetHandler = http.HandlerFunc(agentsServer.HandleGetSnapshot)
			snapshotDeleteHandler = http.HandlerFunc(agentsServer.HandleDeleteSnapshot)
		}
		browserStreamByIDHandler = http.HandlerFunc(agentsServer.HandleBrowserStreamByIDOrName)

		// Wire sandbox execution API handlers.
		//
		// NOTE: tenant/instance-ownership enforcement (RequireSandboxOwnership)
		// is intentionally NOT wired here yet. It depends on the request
		// resolving deterministically to the caller's instance, but the cookie
		// path resolves instance via `tenant_config ... LIMIT 1` with no
		// ORDER BY, which is nondeterministic for a one-org-many-instances org
		// and would cause intermittent 404s across the 2 gateway replicas.
		// The middleware + scoped resolvers are implemented and tested; wiring
		// is gated on the deterministic-instance-resolution fix. See the
		// rectification plan.
		sandboxCommandHandler = http.HandlerFunc(agentsServer.HandleSandboxCommand)
		sandboxExecuteCodeHandler = http.HandlerFunc(agentsServer.HandleExecuteCode)
		sandboxMetricsWatchHandler = http.HandlerFunc(agentsServer.HandleSandboxMetricsStream)
		sandboxPingHandler = http.HandlerFunc(agentsServer.HandleSandboxPing)
		sandboxCommandInterruptHandler = http.HandlerFunc(agentsServer.HandleSandboxCommandInterrupt)
		sandboxCommandStatusHandler = http.HandlerFunc(agentsServer.HandleSandboxCommandStatus)
		sandboxCommandLogsHandler = http.HandlerFunc(agentsServer.HandleSandboxCommandLogs)
		sandboxCreateCodeCtxHandler = http.HandlerFunc(agentsServer.HandleCreateCodeContext)
		sandboxListCodeCtxHandler = http.HandlerFunc(agentsServer.HandleListCodeContexts)
		sandboxGetCodeCtxHandler = http.HandlerFunc(agentsServer.HandleGetCodeContext)
		sandboxDeleteCodeCtxHandler = http.HandlerFunc(agentsServer.HandleDeleteCodeContext)
		sandboxDeleteCodeCtxByLangHandler = http.HandlerFunc(agentsServer.HandleDeleteCodeContextsByLang)
		sandboxInterruptCodeHandler = http.HandlerFunc(agentsServer.HandleInterruptCode)
		sandboxFileInfoHandler = http.HandlerFunc(agentsServer.HandleFileInfo)
		sandboxDeleteFilesHandler = http.HandlerFunc(agentsServer.HandleDeleteFiles)
		sandboxFilePermissionsHandler = http.HandlerFunc(agentsServer.HandleFilePermissions)
		sandboxMoveFilesHandler = http.HandlerFunc(agentsServer.HandleMoveFiles)
		sandboxSearchFilesExecdHandler = http.HandlerFunc(agentsServer.HandleSearchFilesExecd)
		sandboxBulkUploadHandler = http.HandlerFunc(agentsServer.HandleBulkUpload)
		sandboxReplaceFileHandler = http.HandlerFunc(agentsServer.HandleReplaceFileContent)
		sandboxUploadFileHandler = http.HandlerFunc(agentsServer.HandleUploadFile)
		sandboxDownloadFileHandler = http.HandlerFunc(agentsServer.HandleDownloadFile)
		sandboxCreateDirsHandler = http.HandlerFunc(agentsServer.HandleCreateDirectories)
		sandboxDeleteDirsHandler = http.HandlerFunc(agentsServer.HandleDeleteDirectories)
		sandboxMetricsHandler = http.HandlerFunc(agentsServer.HandleSandboxMetrics)
		sandboxRenewExpirationHandler = http.HandlerFunc(agentsServer.HandleRenewExpiration)

		// Metrics history (POR-81)
		if agentsDB != nil {
			mRepo := sandboxmetrics.NewRepository(agentsDB)
			agentsServer.SetMetricsRepository(mRepo)
			// history + batch are served via ConnectRPC (SandboxMetricsHistory /
			// SandboxMetricsBatch). Only the background collector is wired here.
			// Start background collector.
			mCollector := sandboxmetrics.NewCollector(agentsDB, sandboxMgr)
			go mCollector.Run(ctx)
			logger.Info("sandbox_metrics_collector: started")
		}

		// Configure max ports per sandbox
		if sandboxCfg != nil && sandboxCfg.PortExposure.MaxPortsPerSandbox > 0 {
			sandboxMgr.SetMaxPortsPerSandbox(sandboxCfg.PortExposure.MaxPortsPerSandbox)
		}

		// Start unified ingress gateway (replaces SandboxProxy + WebhookRouter)
		{
			// Region/env-aware default. Each gateway pod composes its own
			// FQDN from EVS_REGION + EVS_ENV so previews land at
			// <code>-<port>.[<env>.]<region>.evs.run. Explicit YAML / env
			// override always wins.
			rg := region.FromEnv()
			gatewayCfg := ingressgw.Config{
				BaseDomain: rg.Compose("evs.run"),
				ListenAddr: ":8443",
			}
			if sandboxCfg != nil && sandboxCfg.PortExposure.BaseDomain != "" {
				gatewayCfg.BaseDomain = sandboxCfg.PortExposure.BaseDomain
			}
			if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_BASE_DOMAIN")); v != "" {
				gatewayCfg.BaseDomain = v
			}
			if sandboxCfg != nil && sandboxCfg.PortExposure.ListenAddr != "" {
				gatewayCfg.ListenAddr = sandboxCfg.PortExposure.ListenAddr
			}
			if sandboxCfg != nil && sandboxCfg.PortExposure.MaxPortsPerSandbox > 0 {
				gatewayCfg.MaxPortsPerSandbox = sandboxCfg.PortExposure.MaxPortsPerSandbox
			}
			if sandboxCfg != nil {
				gatewayCfg.RequirePreviewToken = sandboxCfg.PortExposure.RequirePreviewToken
				gatewayCfg.TLS = ingressgw.TLSConfig{
					Enabled:     sandboxCfg.PortExposure.TLS.Enabled,
					CertPath:    sandboxCfg.PortExposure.TLS.CertPath,
					KeyPath:     sandboxCfg.PortExposure.TLS.KeyPath,
					Autocert:    sandboxCfg.PortExposure.TLS.Autocert,
					AutocertDir: sandboxCfg.PortExposure.TLS.AutocertDir,
				}
				// Env-var overrides for the cert paths so cert-manager-
				// issued wildcards (mounted from a Secret) can be wired
				// without rewriting the YAML config blob. Setting either
				// path implies enabled=true.
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_TLS_CERT_PATH")); v != "" {
					gatewayCfg.TLS.CertPath = v
					gatewayCfg.TLS.Enabled = true
				}
				if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_TLS_KEY_PATH")); v != "" {
					gatewayCfg.TLS.KeyPath = v
					gatewayCfg.TLS.Enabled = true
				}
				gatewayCfg.CORS = ingressgw.CORSConfig{
					Enabled:        sandboxCfg.PortExposure.CORS.Enabled,
					AllowedOrigins: sandboxCfg.PortExposure.CORS.AllowedOrigins,
					AllowedMethods: sandboxCfg.PortExposure.CORS.AllowedMethods,
					AllowedHeaders: sandboxCfg.PortExposure.CORS.AllowedHeaders,
					MaxAgeSecs:     sandboxCfg.PortExposure.CORS.MaxAgeSecs,
				}
				gatewayCfg.RequestTimeoutSecs = sandboxCfg.PortExposure.RequestTimeoutSecs
				gatewayCfg.MaxRequestBodyMB = sandboxCfg.PortExposure.MaxRequestBodyMB
			}
			if v, ok := envBoolValue("EVS_SANDBOX_PREVIEW_REQUIRE_TOKEN"); ok {
				gatewayCfg.RequirePreviewToken = v
			}

			// Build lookup adapter
			lookup := ingressgw.NewSimpleLookup(
				sandboxMgr.LookupPortMapping,
				sandboxMgr.LookupPortMappingByPort,
			)

			// Webhook handler (reuses existing WebhookRouter on the gateway)
			var webhookHandler http.Handler
			if agentsDB != nil {
				webhookHandler = sandbox.NewWebhookRouter(sandboxMgr)
			}

			// Wire preview token signer from env var. If not set, a random
			// ephemeral key is generated (tokens valid only within this process
			// lifetime -- fine for short-lived preview URLs).
			previewSecret := []byte(strings.TrimSpace(os.Getenv("EVS_SANDBOX_PREVIEW_TOKEN_SECRET")))
			if pvSigner, pvErr := previewtoken.NewSigner(previewSecret); pvErr == nil {
				agentsServer.SetPreviewSigner(pvSigner)
				gatewayCfg.PreviewSigner = pvSigner
				logger.Info("gateway: preview token signer initialized")
			} else {
				logger.WithError(pvErr).Warn("gateway: preview token signer failed; signed preview URLs disabled")
			}

			gw, err := ingressgw.New(gatewayCfg, lookup, webhookHandler, nil)
			if err != nil {
				logger.WithError(err).Error("gateway: failed to create")
			} else {
				globalSandboxProxyShutdown = gw.Shutdown

				// Extract port from listen address for URL construction
				listenPort := ""
				if _, p, err := net.SplitHostPort(gatewayCfg.ListenAddr); err == nil {
					listenPort = p
				}
				agentsServer.SetPortExposureConfig(gatewayCfg.BaseDomain, gatewayCfg.TLS.Enabled, listenPort)

				go func() {
					if err := gw.Start(); err != nil && err != http.ErrServerClosed {
						logger.WithError(err).Error("gateway: server failed")
					}
				}()
			}
		}

		// Start cron scheduler if DB is available
		if agentsDB != nil {
			sandboxCronScheduler = sandbox.NewCronScheduler(sandboxMgr, 10*time.Second)
		}
	}

	// Initialize GitHub App integration if env vars are configured.
	// Webhook endpoint is registered before auth middleware (uses own HMAC verification).
	{
		if agentsDB == nil {
			logger.Warn("github: agents DB is unavailable; skipping GitHub integration initialization")
		} else {
			ghStore := ghpkg.NewStore(agentsDB)
			var ghApp *ghpkg.App

			// Legacy static app credentials (optional). Manifest mode does not need these env vars.
			ghAppID := os.Getenv("EVS_GITHUB_APP_ID")
			ghPrivateKey := os.Getenv("EVS_GITHUB_APP_PRIVATE_KEY")
			ghWebhookSecret := os.Getenv("EVS_GITHUB_WEBHOOK_SECRET")
			if ghAppID != "" && ghPrivateKey != "" && ghWebhookSecret != "" {
				appID := int64(0)
				if _, err := fmt.Sscanf(ghAppID, "%d", &appID); err == nil && appID > 0 {
					appClient, err := ghpkg.NewApp(appID, ghPrivateKey, ghWebhookSecret)
					if err != nil {
						logger.WithError(err).Error("github: failed to initialize legacy GitHub App client")
					} else {
						ghApp = appClient
						if sandboxMgr != nil {
							sandboxMgr.SetGitHubApp(ghApp)
						}
						logger.WithFields("app_id", appID).Info("github: legacy static app credentials loaded")
					}
				} else {
					logger.Warnf("github: invalid EVS_GITHUB_APP_ID: %s", ghAppID)
				}
			}

			agentsServer.SetGitHubApp(ghApp, ghStore)

			// Manifest flow endpoints (Dokploy-style): start + callback.
			manifestHandler := ghpkg.NewManifestHandler(ghStore)
			router.HandleFunc("/integrations/github/manifest/start", manifestHandler.Start).Methods("GET")
			router.HandleFunc("/integrations/github/callback", manifestHandler.Callback).Methods("GET")

			// Dynamic per-tenant webhook path for manifest-created apps.
			// Example: /webhooks/github/<webhook_key>
			webhookHandler := ghpkg.NewWebhookHandler(ghApp, ghStore)
			router.Handle("/webhooks/github/{webhook_key}", webhookHandler).Methods("POST")

			// Legacy static webhook path (optional compatibility mode).
			if ghApp != nil {
				router.Handle("/webhooks/github", webhookHandler).Methods("POST")
			}

			// Start delivery ID garbage collection
			ghStore.StartDeliveryGC(ctx)
		}
	}

	// Initialize SSH key store and proxy
	{
		// Always initialize SSH key store when database is available so
		// users can manage their SSH keys regardless of whether the proxy is running.
		if agentsDB != nil {
			sshKeyStore := sshpkg.NewKeyStore(agentsDB)
			agentsServer.SetSSHKeyStore(sshKeyStore)

			// Surface the pod's region in SSH info responses so the FE
			// can show "Frankfurt (eu-fra-1)" alongside the connection
			// string. Empty for region-unaware deployments.
			agentsServer.SetRegion(region.FromEnv().Region)

			// Configure SSH endpoint reporting whenever sandboxes are enabled.
			// HTTP gateway pods may advertise a dedicated SSH proxy endpoint
			// without owning a local listener.
			if sandboxMgr != nil {
				sshCfg := resolveSSHRuntimeConfig(sandboxCfg)
				if signer, err := loadSSHHostKeySigner(false); err == nil {
					agentsServer.SetSSHEndpoint(sshCfg.Host, sshCfg.PublicPort, cryptossh.FingerprintSHA256(signer.PublicKey()))
				} else if strings.TrimSpace(os.Getenv("EVS_SSH_HOST_KEY_PATH")) != "" {
					logger.WithError(err).Error("ssh: failed to load configured gateway host key for endpoint reporting")
				}

				if sshCfg.ListenAddr != "disabled" {
					hostKeySigner, err := loadSSHHostKeySigner(true)
					if err != nil {
						logger.WithError(err).Error("ssh: failed to generate/load gateway host key")
					} else {
						proxy := sshpkg.NewProxy(sshpkg.ProxyConfig{
							ListenAddr:     sshCfg.ListenAddr,
							HostKeySigner:  hostKeySigner,
							KeyStore:       sshKeyStore,
							SandboxManager: sandboxMgr,
						})

						if err := proxy.Start(); err != nil {
							logger.WithError(err).Error("ssh: failed to start SSH proxy")
						} else {
							agentsServer.SetSSHProxy(proxy, sshCfg.Host, sshCfg.PublicPort)
							logger.WithFields("addr", sshCfg.ListenAddr, "host", sshCfg.Host, "fingerprint", proxy.Fingerprint()).
								Info("ssh: SSH proxy initialized")
						}
					}
				}
			}
		}
	}

	// Initialize memory backend if enabled in features config
	var memStore memory.VectorStore
	var memEmbedder memory.EmbedderInterface
	var memDefaultModel string
	var memDefaultDim int

	// Debug: log memory config state
	if config.Features != nil {
		logger.WithFields(
			"enable_memory", config.Features.EnableMemory,
			"memory_backend", config.Features.Memory.Backend,
			"embedding_models_count", len(config.Features.Memory.EmbeddingModels),
			"agents_db_available", agentsDB != nil,
		).Info("memory: config check")
	} else {
		logger.Warn("memory: config.Features is nil")
	}

	if config.Features != nil && config.Features.EnableMemory && agentsDB != nil {
		memoryCfg := config.Features.Memory
		backend := memoryCfg.Backend
		if backend == "" {
			backend = "pgvector"
		}

		// Default embedding model is the first entry in the list
		memDefaultModel, memDefaultDim = memoryCfg.DefaultEmbeddingModel()

		switch backend {
		case "pgvector":
			if err := memory.EnsurePgVector(ctx, agentsDB); err != nil {
				logger.WithFields("error", err.Error()).Warn("memory: pgvector setup failed — memory tools disabled")
			} else {
				store, memErr := memory.NewPgVectorStore(agentsDB)
				if memErr != nil {
					logger.WithFields("error", memErr.Error()).Warn("memory: pgvector store init failed — memory tools disabled")
				} else {
					memStore = store
					memEmbedder = memory.NewEmbedder(gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx))
					logger.WithFields("backend", backend, "default_model", memDefaultModel, "default_dimension", memDefaultDim, "models", len(memoryCfg.EmbeddingModels)).
						Info("memory backend initialized")
				}
			}
		case "qdrant":
			qdrantAddr := memoryCfg.Qdrant.Address
			qdrantKey := memoryCfg.Qdrant.APIKey
			store := memory.NewQdrantStore(qdrantAddr, qdrantKey)
			if err := store.HealthCheck(ctx); err != nil {
				logger.WithFields("error", err.Error(), "address", qdrantAddr).
					Warn("memory: qdrant health check failed — memory tools may not work")
			}
			memStore = store
			memEmbedder = memory.NewEmbedder(gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx))
			logger.WithFields("backend", backend, "address", qdrantAddr, "default_model", memDefaultModel, "default_dimension", memDefaultDim).
				Info("memory backend initialized")
		case "pinecone":
			pineconeCfg := memoryCfg.Pinecone
			store := memory.NewPineconeStore(memory.PineconeConfig{
				APIKey:      pineconeCfg.APIKey,
				Cloud:       pineconeCfg.Cloud,
				Region:      pineconeCfg.Region,
				Environment: pineconeCfg.Environment,
			})
			if err := store.HealthCheck(ctx); err != nil {
				logger.WithFields("error", err.Error()).
					Warn("memory: pinecone health check failed — memory tools may not work")
			}
			memStore = store
			memEmbedder = memory.NewEmbedder(gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx))
			logger.WithFields("backend", backend, "default_model", memDefaultModel, "default_dimension", memDefaultDim).
				Info("memory backend initialized")
		case "weaviate":
			weaviateCfg := memoryCfg.Weaviate
			store := memory.NewWeaviateStore(memory.WeaviateConfig{
				URL:    weaviateCfg.URL,
				APIKey: weaviateCfg.APIKey,
			})
			if err := store.HealthCheck(ctx); err != nil {
				logger.WithFields("error", err.Error(), "url", weaviateCfg.URL).
					Warn("memory: weaviate health check failed — memory tools may not work")
			}
			memStore = store
			memEmbedder = memory.NewEmbedder(gwServer.GetRegistry(ctx), gwServer.GetRouter(ctx))
			logger.WithFields("backend", backend, "url", weaviateCfg.URL, "default_model", memDefaultModel, "default_dimension", memDefaultDim).
				Info("memory backend initialized")
		default:
			logger.WithFields("backend", backend).Warn("memory: unsupported backend — memory tools disabled")
		}

		// Wire memory into agents and workflows servers
		if memStore != nil && memEmbedder != nil {
			agentsServer.SetMemoryBackend(memStore, memEmbedder)
			agentsServer.SetMemoryConfig(memDefaultModel, memDefaultDim)
			workflowsServer.SetMemoryBackend(memStore, memEmbedder, memDefaultModel, memDefaultDim)
		}
	}

	// Always register the Memory service so the ConnectRPC path exists
	// (avoids 404 in admin UI). Handlers return clear errors when backend is nil.
	memoryServer := memorysvc.CreateServer(ctx)
	if memStore != nil && memEmbedder != nil {
		memoryServer.SetBackend(memStore, memEmbedder, memDefaultModel, memDefaultDim)
		logger.Info("memory: service registered with backend at /v1/memory/collections")
	} else {
		logger.Warn("memory: service registered without backend — enable_memory is false or backend init failed")
	}
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		memoryServer.SetDB(initRes.Primary.RW)
	}
	if err := apis.RegisterService(ctx, memoryServer); err != nil {
		return nil, err
	}

	if err := apis.RegisterService(ctx, agentsServer); err != nil {
		return nil, err
	}

	// evs.run hosting (SitesService). The service and its libraries have been
	// on master for a while with nothing constructing them, so the route was
	// never registered and /v1/sites answered 404 everywhere. Registration is
	// unconditional: when EVS_HOSTING_R2_* is unset, buildHostingServer returns
	// a server that answers Unavailable rather than nothing at all, which is a
	// far better signal than a 404 for a service that is supposed to exist.
	hostingServer, hostingEnabled := buildHostingServer(ctx, initRes, embeddedDefaults)
	if err := apis.RegisterService(ctx, hostingServer); err != nil {
		return nil, err
	}
	if !hostingEnabled {
		logger.Info("hosting: evs.run control plane registered but disabled (set EVS_HOSTING_R2_ENDPOINT and EVS_HOSTING_R2_BUCKET to enable)")
	}

	// ─── Eval Runner (started after memory + sandbox init) ──────────
	var evalRunner *evalrunner.Runner
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		evalOpts := evalrunner.RunnerOpts{
			DB:     initRes.Primary.RW,
			CHConn: evalClickHouseConn,
		}
		if memStore != nil {
			evalOpts.VectorStore = memStore
		}
		if memEmbedder != nil {
			evalOpts.Embedder = memEmbedder
		}
		if sandboxMgr != nil {
			evalOpts.SandboxMgr = sandboxMgr
		}
		// Code scorers run user code: sandbox-only. When no sandbox manager
		// exists, code scorers fail closed by design (no in-process fallback).
		evalOpts.AllowUnsandboxedScorers = false
		evalRunner = evalrunner.Start(ctx, evalOpts)
		logger.Info("Eval runner started")

		// Start eval scheduler for cron-based eval runs
		evalrunner.StartScheduler(ctx, initRes.Primary.RW)
	}

	// ─── Channel Connectors (Discord, Slack, Telegram) ──────────────
	var channelMgr *channelpkg.ChannelManager
	if agentsDB != nil {
		channelStore := channelpkg.NewPostgresStore(agentsDB)
		hostName, _ := os.Hostname()
		agentLister := channelpkg.NewDBAgentLister(agentsDB)
		dispatcher := channelpkg.NewAgentDispatcher(agentLister, agentsDB)
		channelMgr = channelpkg.NewChannelManager(channelStore, agentsServer, agentsDB, hostName, nil, dispatcher, agentLister)
		// Connector events carry no request context, so the manager resolves
		// MESSAGES_MONTHLY from the monitor directly rather than from a tenant
		// config it will never see.
		channelMgr.SetLicenseMonitor(enterprise.LicenseMonitorFromContext(ctx))

		// Register platform connector factories
		channelMgr.RegisterFactory(channelpkg.PlatformDiscord, discordpkg.Factory)
		channelMgr.RegisterFactory(channelpkg.PlatformSlack, slackpkg.Factory)
		channelMgr.RegisterFactory(channelpkg.PlatformTelegram, telegrampkg.Factory)

		// Start connectors in background
		go func() {
			if err := channelMgr.Start(ctx); err != nil {
				logger.WithError(err).Error("channels: failed to start channel manager")
			}
		}()

		// Register gRPC service
		channelsServer := channelssvc.CreateServer(ctx, channelStore, channelMgr, agentsDB)
		if err := apis.RegisterService(ctx, channelsServer); err != nil {
			logger.WithError(err).Warn("channels: failed to register channels service")
		}
		logger.Info("channels: channel manager and gRPC service initialized")

		// Wire cron scheduler → channel manager for notifications
		if sandboxCronScheduler != nil {
			sandboxCronScheduler.SetNotifier(channelMgr)
		}

		// ─── Alerting System ─────────────────────────────────────────
		alertStore := alertpkg.NewPostgresStore(agentsDB)

		// Build dashboard URL for deep-linking in alert notifications.
		var alertDashboardURL string
		if config != nil && config.Server != nil {
			scheme := "http"
			if config.Server.Config.ExternalSecure || config.Server.TLS.Enabled {
				scheme = "https"
			}
			host := config.Server.Config.ExternalDomain
			if host == "" {
				host = "localhost"
			}
			ePort := config.Server.Config.ExternalPort
			if ePort == 0 {
				ePort = config.Server.Config.Port
				if ePort == 0 {
					ePort = 8089
				}
			}
			// Omit port for standard scheme ports.
			if (scheme == "https" && ePort == 443) || (scheme == "http" && ePort == 80) {
				alertDashboardURL = fmt.Sprintf("%s://%s", scheme, host)
			} else {
				alertDashboardURL = fmt.Sprintf("%s://%s:%d", scheme, host, ePort)
			}
		}

		alertNotifier := alertpkg.NewNotificationRouter(alertStore, channelMgr, alertDashboardURL)
		alertEvaluator := alertpkg.NewEvaluator(alertStore, evalClickHouseConn, alertNotifier)

		go func() {
			if err := alertEvaluator.Start(ctx); err != nil {
				logger.WithError(err).Error("alerts: failed to start evaluator")
			}
		}()

		alertsServer := alertssvc.CreateServer(ctx, alertStore, alertEvaluator)
		alertsServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
			enterprise.LicenseMonitorFromContext(ctx),
			"alerts",
			"Alerts",
			enterprise.Edition(),
		))
		if err := apis.RegisterService(ctx, alertsServer); err != nil {
			logger.WithError(err).Warn("alerts: failed to register alerts service")
		}
		logger.Info("alerts: alerting system initialized")

		// Wire regression detection → alert system
		if evalRunner != nil {
			regNotifier := evalrunner.NewAlertRegressionNotifier(alertStore, alertNotifier)
			evalRunner.SetRegressionNotifier(regNotifier)
			logger.Info("alerts: eval regression notifier wired")
		}

		// ─── Issues (error tracking) ─────────────────────────────────
		// Derives issue groups from error spans in the trace store; triage
		// state lives in Postgres (agentsDB). Needs the eval ClickHouse conn.
		if evalClickHouseConn != nil {
			issuesSvc := issuepkg.NewService(evalClickHouseConn, agentsDB)
			issuesServer := issuessvc.CreateServer(ctx, issuesSvc)
			if err := apis.RegisterService(ctx, issuesServer); err != nil {
				logger.WithError(err).Warn("issues: failed to register issues service")
			} else {
				logger.Info("issues: error-tracking service initialized")
			}
		}
	}

	// ─── Storage service (created early so voice + workflows can use it) ──
	storageServer := storagesvc.CreateServerWithSecurityDeps(ctx, nil, initRes.Primary.RW, storageCredentialStore, nil)
	managedStorage, err := buildManagedStorageRuntime(
		ctx,
		initRes.Primary.RW,
		managedGateway,
		os.Getenv,
		defaultManagedStorageS3Factory,
	)
	if err != nil {
		return nil, err
	}
	if managedStorage != nil {
		storageServer.SetManagedStorage(managedStorage.defaults, managedStorage.resolver)
		if runtime != nil {
			runtime.managedStorageDefaults = managedStorage.defaults
		}
		logger.WithFields("cell_id", managedStorage.cellID).Info("managed storage: Everstack Storage default enabled")
	}
	storageUploadReconciler := storagepkg.NewUploadReconciler(
		storagepkg.NewPostgresUploadLifecycle(initRes.Primary.RW),
		storageServer,
	)
	go func() {
		if err := storageUploadReconciler.Run(ctx); err != nil {
			logger.WithError(err).Error("storage upload reconciler stopped")
		}
	}()
	logger.Info("storage upload reconciler enabled")

	// ─── Voice Clone Profiles ────────────────────────────────────────
	if agentsDB != nil {
		voiceRepo := voice_clone.NewPostgresRepository(agentsDB)
		// Always inject repo so TTS/STT workflow executors can resolve profile IDs
		gwServer.SetVoiceCloneRepo(voiceRepo)
		workflowsServer.SetVoiceCloneRepo(voiceRepo)
		workflowsServer.SetStorageServer(storageServer)

		// Register voice CRUD gRPC API — gated by voice feature interceptor
		voiceServer := voicesvc.CreateServer(ctx, voiceRepo)
		voiceServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
			enterprise.LicenseMonitorFromContext(ctx),
			"voice",
			"Voice",
			enterprise.Edition(),
		))
		voiceServer.SetRegistry(gwServer.GetRegistry(ctx))
		voiceServer.SetStorageServer(storageServer)
		if err := apis.RegisterService(ctx, voiceServer); err != nil {
			logger.WithError(err).Warn("voice: failed to register voice service")
		}
		logger.Info("voice: voice clone service initialized")
	}

	// Register graceful shutdown for agent sessions, sandboxes, channels, and cron scheduler
	if mgr := agentsServer.GetSessionManager(); mgr != nil {
		origShutdown := mgr.GracefulShutdown
		globalAgentsShutdown = func(shutdownCtx context.Context) error {
			if channelMgr != nil {
				_ = channelMgr.Stop(shutdownCtx)
			}
			if sandboxCronScheduler != nil {
				sandboxCronScheduler.Stop()
			}
			err := origShutdown(shutdownCtx)
			if sandboxMgr != nil {
				_ = sandboxMgr.DestroyAll(shutdownCtx)
			}
			if browserPool != nil {
				if closeErr := browserPool.Close(); closeErr != nil {
					logger.WithFields("error", closeErr.Error()).Warn("browser pool: failed to close")
				}
			}
			return err
		}
	}

	// Register MCP Gateway gRPC service (Connect)
	if agentsDB != nil {
		mcpRegistry := mcp.NewRegistry(nil)
		mcpHealth := mcp.NewHealthChecker(mcpRegistry, 30*time.Second, 5*time.Second, nil)
		// Persist health status to DB on every background check
		mcpHealth.SetOnHealthUpdate(func(serverID string, status mcp.HealthStatus) {
			_, _ = agentsDB.ExecContext(ctx,
				`UPDATE mcp_servers SET health_status = $1, health_last_check = NOW() WHERE id = $2`,
				string(status), serverID,
			)
		})
		mcpHealth.Start()
		mcpServer := mcpsvc.CreateServer(ctx, agentsDB, mcpRegistry, mcpHealth)
		if err := apis.RegisterService(ctx, mcpServer); err != nil {
			return nil, err
		}
		// Expose federated MCP tools to the agents runtime, plus a
		// hydrator so per-session top-up restores the tenant's servers
		// from DB after a gateway restart (the registry is in-memory only).
		agentsServer.SetMcpRegistry(mcpRegistry)
		agentsServer.SetMcpRegistryHydrator(mcpServer)
		// Mirror the MCP wiring into the deployment invoker so /v1/deploy/{id}/invoke
		// also gets federated MCP tools — without this the agent answers "I don't
		// have access to MCP" via the deployment URL even when configured.
		if deployInvoker != nil {
			deployInvoker.SetMcpRegistry(mcpRegistry)
			deployInvoker.SetMcpRegistryHydrator(mcpServer)
		}
		// Boot-time hydration: restore every enabled MCP server across
		// every tenant in the background so they're ready before the
		// first session lands. Errors are non-fatal; per-session
		// hydration is the safety net.
		go func(srv *mcpsvc.Server, parentCtx context.Context) {
			hydrateCtx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
			defer cancel()
			if err := srv.HydrateRegistryAll(hydrateCtx); err != nil {
				logger.WithError(err).Warn("mcp: boot-time registry hydration partially failed; per-session hydration will fill gaps")
			}
		}(mcpServer, ctx)
		// Register OAuth REST endpoints via the API router for correct priority
		apis.HandleFunc("/mcp/oauth/initiate", mcpServer.HandleOAuthInitiate())
		apis.HandleFunc("/mcp/oauth/callback", mcpServer.HandleOAuthCallback())
		logger.Info("mcp: service registered")

		// Expose Everstack itself as an MCP server (inbound counterpart to the
		// federated client above): external MCP clients (Claude Desktop, Cursor,
		// Google ADK, ...) connect to /mcp and call a tenant-scoped slice of
		// Everstack tools. Each request authenticates via the tenant's Everstack
		// API key as a Bearer token; the memory backend (if configured) adds the
		// memory_query / memory_store tools on top of the builtins.
		if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
			// Shared agent runner: lets run_agent (MCP) and the A2A server invoke
			// deployed agents. Only built when the deployment invoker is wired.
			var agentRunner *agentrun.Runner
			if deployInvoker != nil {
				agentRunner = agentrun.New(deployInvoker, agentdeploy.NewPostgresStore(agentsDB))
			}
			// ADK runtime: run Google ADK agents in tenant-scoped sandboxes.
			// ON BY DEFAULT wherever a sandbox backend exists - ADK is a universal
			// capability, NOT gated by plan or an enable flag, and there is no
			// off-switch. Egress is the safety control (not a gate): on multi-tenant
			// cloud (sharedMode) the sandbox is "whitelist"-only - never wide-open
			// "allow" - so arbitrary caller code can reach only the package
			// registries + the model endpoint, never the metadata service, internal
			// cluster, or other sandboxes. Self-hosted is single-tenant, so it
			// defers to the operator's EVS_ADK_NETWORK_MODE (default = manager's).
			var adkRuntime *adk.Runtime
			if sandboxMgr != nil {
				networkMode := os.Getenv("EVS_ADK_NETWORK_MODE")
				if sharedMode && networkMode == "" {
					networkMode = "whitelist" // never wide-open egress on shared infra
				}
				// Route ADK model calls through the Everstack gateway so agents reach
				// the instance's configured providers (with metering + traces) instead
				// of opening egress to arbitrary model vendors. The sandbox-reachable
				// gateway URL is deployment-specific config (set by the operator
				// self-hosted, or the control plane per managed instance - never by an
				// end user); its host is added to the egress allowlist.
				var allowedHosts []string
				var adkEnv map[string]string
				if base := strings.TrimSpace(os.Getenv("EVS_ADK_MODEL_BASE_URL")); base != "" {
					adkEnv = map[string]string{"OPENAI_BASE_URL": base, "OPENAI_API_BASE": base}
					if key := strings.TrimSpace(os.Getenv("EVS_ADK_MODEL_API_KEY")); key != "" {
						adkEnv["OPENAI_API_KEY"] = key
					}
					if u, err := url.Parse(base); err == nil && u.Host != "" {
						allowedHosts = append(sandbox.DefaultAllowedHosts(), u.Host)
					}
				}
				adkRuntime = adk.New(adk.NewSandboxManagerAdapter(sandboxMgr, adk.AdapterConfig{
					NetworkMode:  networkMode,
					AllowedHosts: allowedHosts,
					EnvVars:      adkEnv,
				}))
				logger.Infof("mcp: run_adk_agent enabled (ADK runtime on; network_mode=%q, model_routing=%t)", networkMode, adkEnv != nil)
			}

			// Interop control plane: per-tenant tool exposure, A2A publish flags,
			// and the saved-remote registry. Backed by the primary DB (== agentsDB).
			interopStore := interop.NewStore(agentsDB)
			agentsServer.SetInteropStore(interopStore)

			mcpToolProvider := mcpserverprovider.New(mcpserverprovider.Deps{
				MemStore:     memStore,
				MemEmbedder:  memEmbedder,
				MemModel:     memDefaultModel,
				MemDim:       memDefaultDim,
				HTTPClient:   http.DefaultClient,
				WebSearchURL: os.Getenv("EVS_SEARXNG_URL"),
				JinaAPIKey:   os.Getenv("EVS_JINA_API_KEY"),
				AgentsDB:     agentsDB,
				Runner:       agentRunner,
				ADK:          adkRuntime,
				ToolSettings: interopStore,
				// ADKEnabled deliberately unset: ADK is ungated (universal), so the
				// provider exposes run_adk_agent whenever the runtime exists.
			})
			mcpAuth := mcpserverauth.NewAPIKeyAuthenticator(initRes.Primary.RW, nil)
			mcpInbound := mcpserver.New(mcpToolProvider, mcpAuth, nil)
			apis.HandleFunc("/mcp", mcpInbound.ServeHTTP)
			logger.Info("mcp: inbound server mounted at /mcp (authenticate with an Everstack API key as a Bearer token)")

			// Admin control-plane endpoints for the interop UI (first-party,
			// tenant resolved from request context).
			interop.NewHandlers(interopStore, sharedMode, adkRuntime != nil).Mount(apis)

			// A2A server: expose deployed Everstack agents as A2A agents so
			// external A2A clients (Google ADK, ...) can use them as sub-agents.
			// A2A is opt-in per agent via the interop publish flag.
			if agentRunner != nil {
				a2aSrv := a2aserver.New(a2aserver.Deps{
					Agents:    a2aserver.NewDBAgentLookup(agentsDB),
					Runner:    agentRunner,
					Auth:      mcpAuth,
					Publisher: interopStore,
					// PublicURL left empty: Agent Card URLs are derived from the
					// inbound request (scheme + host), which is correct behind the
					// reverse proxy without hardcoding a base URL.
				})
				a2aSrv.Mount(apis)
				logger.Info("a2a: server mounted at /a2a (agent cards + message/send; authenticate with an Everstack API key as a Bearer token)")
			}
		} else {
			logger.Warn("mcp: inbound server not mounted (no primary DB for API-key auth)")
		}
	}

	// Register Events gRPC service (Connect) — gated by audit_logs feature
	eventsServer := events.CreateServerWithContext(ctx)
	eventsServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"audit_logs",
		"Audit Logs",
		enterprise.Edition(),
	))
	if err := apis.RegisterService(ctx, eventsServer); err != nil {
		return nil, err
	}

	// Register Auth gRPC service (Connect) - for admin dashboard authentication
	// Supports auth modes: none, builtin, oidc
	authMode := ""
	if config.Auth != nil {
		authMode = config.Auth.Mode
	}

	switch authMode {
	case "builtin":
		// Check if an external auth provider was injected (cloud control plane)
		if embeddedDefaults != nil && embeddedDefaults.AuthProvider != nil {
			provider := embeddedDefaults.AuthProvider
			if err := apis.RegisterService(ctx, provider); err != nil {
				logger.WithError(err).Warn("auth: failed to register injected auth provider")
			} else {
				logger.Info("auth: external auth provider enabled (cloud mode)")
				provider.RegisterHTTPRoutes(router)
			}
		} else if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
			// Built-in email/password + magic link authentication (self-hosted)
			builtinCfg := config.Auth.Builtin
			sessionMaxAge := builtinCfg.SessionMaxAge
			if sessionMaxAge <= 0 {
				sessionMaxAge = 86400 * 7 // Default: 7 days
			}
			// Default to secure cookies (HTTPS only), but allow override for local dev
			sessionSecure := true
			if builtinCfg.SessionSecure != nil {
				sessionSecure = *builtinCfg.SessionSecure
			}
			sessionSameSite := strings.ToLower(strings.TrimSpace(builtinCfg.SessionSameSite))
			if sessionSameSite == "" {
				sessionSameSite = "lax"
			}
			authConfig := &selfhostedauth.Config{
				SessionSecret:     builtinCfg.SessionSecret,
				SessionCookieName: "everstack_session",
				SessionMaxAge:     sessionMaxAge,
				SessionSecure:     sessionSecure,
				SessionHTTPOnly:   true,
				SessionSameSite:   sessionSameSite,
				ExternalURL:       authExternalURL(config.Server),
				DeviceTokens:      cliDeviceTokens,
			}
			seatLimit := builtinCfg.SeatLimit
			authServer, err := selfhostedauth.CreateServer(initRes.Primary.RW, authConfig, seatLimit)
			if err != nil {
				logger.WithError(err).Warn("auth: failed to initialize builtin auth service, skipping")
			} else if authServer != nil {
				if err := apis.RegisterService(ctx, authServer); err != nil {
					logger.WithError(err).Warn("auth: failed to register builtin auth service")
				} else {
					logger.Info("auth: builtin authentication enabled")
					// Register HTTP routes for login/register (needed because ConnectRPC can't set cookies)
					authServer.RegisterHTTPRoutes(router)
					// Register OrganizationService alongside the auth service
					if orgSrv := authServer.OrgService(); orgSrv != nil {
						if err := apis.RegisterService(ctx, orgSrv); err != nil {
							logger.WithError(err).Warn("auth: failed to register organization service")
						} else {
							logger.Info("auth: organization service registered")
						}
					}
				}
			}
		} else {
			logger.Warn("auth: builtin mode requires database, falling back to no auth")
		}

	case "oidc":
		// OpenID Connect authentication (Keycloak, Auth0, Okta, Authelia, etc.)
		oidcCfg := config.Auth.OIDC
		if oidcCfg.IssuerURL == "" || oidcCfg.ClientID == "" {
			logger.Warn("auth: oidc mode requires issuer_url and client_id, falling back to no auth")
		} else {
			logger.WithFields(
				"issuer_url", oidcCfg.IssuerURL,
				"client_id", oidcCfg.ClientID,
			).Info("auth: oidc authentication enabled")
			// TODO: Register OIDC auth middleware and callback routes
		}

	case "none", "":
		logger.Info("auth: authentication disabled (mode=none)")

	default:
		logger.WithFields("mode", authMode).Warn("auth: unknown auth mode, falling back to no auth")
	}

	// Register Logs gRPC service (Connect)
	logsServer := logs.CreateServerWithContext(ctx)
	if err := apis.RegisterService(ctx, logsServer); err != nil {
		return nil, err
	}

	// Register Traces gRPC service (Connect)
	tracesServer := traces.CreateServerWithContext(ctx)
	// Wire score recorder if ClickHouse is available
	if config != nil && config.Database != nil && config.Database.ClickHouse.DSN != "" {
		chScoreDB, err := sql.Open("clickhouse", config.Database.ClickHouse.DSN)
		if err != nil {
			logger.WithError(err).Warn("Failed to open ClickHouse for score recorder")
		} else {
			scoreRecorder := scores.NewRecorder(chScoreDB)
			overlayRecorder := traceoverlays.NewRecorder(chScoreDB)
			tracesServer.SetScoreRecorder(scoreRecorder)
			tracesServer.SetOverlayRecorder(overlayRecorder)
			tracesServer.SetCustomColumnStore(customcolumns.NewStore(chScoreDB))
			// Custom log columns reuse the same overlay ClickHouse handle.
			logsServer.SetLogColumnStore(logcolumns.NewStore(chScoreDB))
			tracesServer.SetTraceViewStore(traceviews.NewStore(chScoreDB))
			tracesServer.SetSemanticMappingStore(semanticmappings.NewStore(chScoreDB))
			tracesServer.SetClassificationRuleStore(classificationrules.NewStore(chScoreDB))
			// Wire auto-scoring into agent turns
			agentsServer.SetScoreRecorder(scoreRecorder)
			// Wire the sampling-eval-rules runner so it can write to
			// otel_trace_scores. Start the polling scheduler too —
			// without it the rules sit dormant until manually triggered
			// via RunSamplingEvalRuleNow.
			if evalRunner != nil {
				evalRunner.SetScoreRecorder(scoreRecorder)
				evalrunner.StartSamplingScheduler(ctx, evalRunner)
				logger.Info("sampling eval scheduler enabled (online eval on production traces)")
			}
			logger.Info("autoscorer: outcome scoring pipeline enabled for agent turns")
		}
	}
	if err := apis.RegisterService(ctx, tracesServer); err != nil {
		return nil, err
	}

	// Register Observability gRPC service (Connect) - metrics/sessions/users dashboards
	observabilityServer := observability.CreateServerWithContext(ctx)
	if err := apis.RegisterService(ctx, observabilityServer); err != nil {
		return nil, err
	}

	// Public model activity is a managed-cloud surface backed by the durable
	// hourly ClickHouse aggregate. Never register it for self-hosted gateways:
	// those deployments do not have the cross-tenant population required for
	// privacy-safe public reporting.
	if publicModelMetricsEnabled(managedGateway) && evalClickHouseConn != nil {
		publicMetricsConfig := publicModelMetricsConfig(time.Now().UTC())
		if !publicMetricsConfig.TestingThresholdsUntil.IsZero() {
			logger.WithFields(
				"minimum_tenants", publicMetricsConfig.MinimumTenants,
				"minimum_requests", publicMetricsConfig.MinimumRequests,
				"testing_until", publicMetricsConfig.TestingThresholdsUntil.Format(time.RFC3339),
			).Warn("time-bound public model metrics testing thresholds enabled")
		}
		publicMetricsService := modelmetrics.NewService(
			modelmetrics.NewClickHouseRepository(evalClickHouseConn),
			publicMetricsConfig,
		)
		if err := apis.RegisterService(ctx, modelmetricssvc.NewServer(publicMetricsService)); err != nil {
			return nil, err
		}
		// The gateway-wide CORS policy answers with
		// `Access-Control-Allow-Origin: *`, which lets any site read these
		// aggregates in a browser. These three routes are public but they are
		// meant for the Everstack catalog, so narrow them to an explicit
		// origin allowlist on the way out.
		// MountGRPCGatewayPrefixes no-ops when the grpc-gateway is absent;
		// keep that guard, otherwise the wrapper registers a handler that
		// panics on the first request.
		if gatewayHandler := apis.GRPCGateway(); gatewayHandler != nil {
			apis.RegisterHandlerPrefixes(
				publicModelMetricsCORS(gatewayHandler),
				"/api/model-metrics/v1",
			)
		}
		logger.Info("public model metrics API enabled")
	}

	// Register Storage gRPC service (Connect) - object storage management
	// (storageServer was created earlier for voice + workflows injection)
	if err := apis.RegisterService(ctx, storageServer); err != nil {
		return nil, err
	}
	var withStorageHTTPAuth func(http.Handler) http.Handler
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		storageAPIKeyInterceptor := middleware.NewAPIKeyInterceptorWithSessionDB(false, initRes.Primary.RW)
		withStorageHTTPAuth = storageAPIKeyInterceptor.WithAPIKeyValidation
	} else {
		withStorageHTTPAuth = func(handler http.Handler) http.Handler {
			return middleware.WithAPIKeyValidation(handler, false)
		}
	}
	// HTTP upload proxy — bypasses presigned URLs to avoid browser CORS issues with R2/S3
	apis.HandleFunc("/api/v1/storage/upload", withStorageHTTPAuth(storageServer.UploadProxyHandler()).ServeHTTP)
	// HTTP sync endpoint — imports existing bucket objects into the database
	apis.HandleFunc("/api/v1/storage/sync", withStorageHTTPAuth(storageServer.SyncHandler()).ServeHTTP)

	// Register Datasets gRPC service (Connect) - gated by evaluations feature
	datasetServer := datasets.CreateDatasetServerWithContext(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		datasetServer.SetDB(initRes.Primary.RW)
	}
	datasetServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"evaluations",
		"Evaluations",
		enterprise.Edition(),
	))
	if err := apis.RegisterService(ctx, datasetServer); err != nil {
		return nil, err
	}
	evalServer := datasets.CreateEvalServerWithContext(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		evalServer.SetDB(initRes.Primary.RW)
	}
	evalServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"evaluations",
		"Evaluations",
		enterprise.Edition(),
	))
	// Wire the sampling-rule runner so RunSamplingEvalRuleNow can
	// actually execute. Without this the RPC returns Unavailable.
	if evalRunner != nil {
		evalServer.SetSamplingRunner(evalRunner)
	}
	if err := apis.RegisterService(ctx, evalServer); err != nil {
		return nil, err
	}

	// Register Playground gRPC service (Connect) - persists playground documents
	// via direct SQL, gated by the evaluations feature.
	playgroundServer := datasets.CreatePlaygroundServerWithContext(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		playgroundServer.SetDB(initRes.Primary.RW)
	}
	playgroundServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"evaluations",
		"Evaluations",
		enterprise.Edition(),
	))
	if err := apis.RegisterService(ctx, playgroundServer); err != nil {
		return nil, err
	}

	// Register Prompts gRPC service (Connect) - gated by prompt management feature
	promptServer := prompts.CreatePromptServerWithContext(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		promptServer.SetDB(initRes.Primary.RW)
	}
	promptServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"prompt_management",
		"Prompt Management",
		enterprise.Edition(),
	))
	if err := apis.RegisterService(ctx, promptServer); err != nil {
		return nil, err
	}

	// Register Annotations gRPC service (Connect) - gated by evaluations feature
	annotationsServer := annotations.CreateServerWithContext(ctx)
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		annotationsServer.SetDB(initRes.Primary.RW)
	}
	annotationsServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
		enterprise.LicenseMonitorFromContext(ctx),
		"evaluations",
		"Evaluations",
		enterprise.Edition(),
	))
	if err := apis.RegisterService(ctx, annotationsServer); err != nil {
		return nil, err
	}

	// Register Catalog gRPC service (Connect) - BEFORE providers so catalog sync is available
	catalogServer, err := catalog.CreateServerWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize catalog service: %w", err)
	}
	if err := apis.RegisterService(ctx, catalogServer); err != nil {
		return nil, err
	}

	// Store catalog sync service in context for provider catalog to use
	if catalogServer != nil && catalogServer.GetSyncService() != nil {
		ctx = context.WithValue(ctx, contextkeys.CatalogSync, catalogServer.GetSyncService())

		// Wire the atomic catalog projection reconciler when the database and
		// command infrastructure are available.
		if sys, err := cqrs.GetSystemFromContext(ctx); err == nil && sys != nil && sys.CommandBus != nil {
			if initRes != nil && supportsCatalogProjection(initRes.Primary) {
				// Reuse provider repo from context (already created earlier for gateway)
				providerRepo := ctx.Value(contextkeys.ProviderRepo).(*provider_config.Repository)

				// Create model repo
				modelRepo := provider_config.NewModelRepository(initRes.Primary.RW)

				// Register catalog command handlers in CQRS system
				catalogHandlers := catalogCmdHandler.NewCatalogCommandHandlers(providerRepo, modelRepo)
				for _, handler := range catalogHandlers {
					sys.CommandBus.RegisterHandler(handler)
				}

				// Provider/model rows, the release journal, and catalog audit events
				// commit in one PostgreSQL transaction before in-process publication.
				dbReconciler := catalog_sync.NewDBReconciler(initRes.Primary.RW, sys.EventBus)
				catalogServer.GetSyncService().SetDBReconciler(dbReconciler)

				// Start freshness update job
				freshnessJob := catalog_sync.NewFreshnessJob(modelRepo)
				go freshnessJob.Start(ctx)

				// Start cleanup job
				cleanupJob := catalog_sync.NewCleanupJob(providerRepo, modelRepo)
				go cleanupJob.Start(ctx)
			}
		}
	}

	// Register Providers gRPC service (Connect) - AFTER catalog so it can use catalog sync
	providersServer, err := providers.CreateServerWithContext(ctx, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize providers service: %w", err)
	}
	if err := apis.RegisterService(ctx, providersServer); err != nil {
		return nil, err
	}

	// Wire up catalog refresher so provider catalog reloads when catalog sync updates
	if catalogServer != nil && catalogServer.GetSyncService() != nil && providersServer != nil {
		catalogServer.GetSyncService().SetCatalogRefresher(providersServer.GetCatalog())
		logger.Info("catalog_refresh: provider catalog will auto-refresh when catalog sync updates")
	}

	// Register Model Discovery gRPC service (Connect) — gated by custom_models feature
	if initRes != nil && initRes.Primary != nil && initRes.Primary.RW != nil {
		modelDiscoveryServer, err := model_discovery.CreateServerWithContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize model discovery service: %w", err)
		}
		modelDiscoveryServer.WithInterceptors(grpcmw.NewFeatureGateInterceptor(
			enterprise.LicenseMonitorFromContext(ctx),
			"custom_models",
			"Custom Models",
			enterprise.Edition(),
		))
		if err := apis.RegisterService(ctx, modelDiscoveryServer); err != nil {
			return nil, err
		}
	}

	// Mount SSE streaming route for curl testing
	path, h := gwServer.SSEChatHandler()
	apis.RegisterHandlerOnPrefix(path, h)
	router.Handle(path, h)

	// If enabled, add exact-path SSE negotiation wrappers so they take precedence.
	// We require BOTH streaming and SSE to be enabled in features.gateway for browser EventSource.
	// For backward compatibility, if features are absent, fall back to gateway.enable_sse alone.
	enableStreaming := false
	enableSSE := false
	if config.Features != nil {
		enableStreaming = config.Features.Gateway.EnableStreaming
		enableSSE = config.Features.Gateway.EnableSSE
	}

	if (enableStreaming && enableSSE) || (config.Features == nil && config.Gateway != nil && config.Gateway.EnableSSE) {
		// Ensure Accept header is auto-set and stream:true injected when defaulting
		defaultStream := enableStreaming
		wrapped := middleware.WithSSEAcceptForStreamingDefault(
			middleware.WrapWithSSENegotiation(apis.GRPCGateway()),
			defaultStream,
		)
		// Mount on SSE routes from defaults so users cannot alter them
		if defs, err := validator.LoadDefaultConfigs(); err == nil && len(defs.Gateway) > 0 {
			if defCfg, e := validator.ParseGatewayDefaults(defs.Gateway); e == nil && defCfg != nil {
				for _, rt := range defCfg.SSE.Routes {
					router.Handle(rt.Path, wrapped)
				}
			}
		}
	}

	// Reverse proxy for internal services based on defaults server.yaml (generic)
	reflectionEnabled := wireReverseProxies(ctx, apis, router)

	// Ensure reflection is enabled even if no defaults.server proxy config was provided
	if !reflectionEnabled {
		apis.EnableServerReflection()
	}

	// License Service proxy for spend limits and other license management endpoints
	// This allows the Admin UI to call the license service through the gateway
	// The proxy uses M2M authentication with the new provider-agnostic M2M system
	if licenseURL := ctx.Value(contextkeys.LicenseServiceURL); licenseURL != nil && licenseURL.(string) != "" {
		licenseURLStr := licenseURL.(string)

		// Create M2M provider for the "portal" client (for user-facing admin operations)
		// The portal client has appropriate scopes for spend limit operations
		var m2mProvider m2m.TokenProvider
		if proxySvcCfg, err := servicescfg.Load(""); err == nil && proxySvcCfg != nil && proxySvcCfg.Security.M2M.Enabled {
			// Debug: Check signing key availability
			signingKeyLen := 0
			signingKeyB64 := proxySvcCfg.Security.M2M.Simple.SigningKey
			if signingKeyB64 != "" {
				decoded, err := base64.StdEncoding.DecodeString(signingKeyB64)
				if err == nil {
					signingKeyLen = len(decoded)
				}
			}
			logger.WithFields("signing_key_length", signingKeyLen, "has_signing_key_b64", signingKeyB64 != "").Debug("license_proxy: checking M2M config")

			m2mCfgRaw := proxySvcCfg.Security.M2M.ToM2MConfig()
			if m2mCfgRaw != nil {
				logger.WithFields("has_simple_config", m2mCfgRaw.SimpleConfig != nil, "provider", m2mCfgRaw.Provider).Debug("license_proxy: creating M2M config")

				m2mAuthCfg, err := m2m.ConfigFromServices(&m2m.ServicesM2MConfig{
					Enabled:         true,
					Provider:        m2mCfgRaw.Provider,
					SimpleConfig:    convertSimpleConfig(m2mCfgRaw.SimpleConfig),
					OIDCConfig:      convertOIDCConfig(m2mCfgRaw.OIDCConfig),
					Clients:         convertClients(m2mCfgRaw.Clients),
					OIDCClients:     convertClients(m2mCfgRaw.OIDCClients),
					PublicEndpoints: m2mCfgRaw.PublicEndpoints,
					EndpointScopes:  m2mCfgRaw.EndpointScopes,
					ScopePolicy:     convertScopePolicy(m2mCfgRaw.ScopePolicy),
				})
				if err == nil {
					// Use "portal" client for admin UI operations (has license:read, license:write scopes)
					m2mProvider, err = m2m.NewTokenProvider(m2mAuthCfg, "portal")
					if err != nil {
						logger.WithError(err).Warn("license_proxy: failed to create M2M provider")
					} else {
						logger.Info("license_proxy: created M2M provider for portal client")
					}
				} else {
					logger.WithError(err).Warn("license_proxy: failed to create M2M config")
				}
			}
		}

		// Create M2M authenticated proxy handler
		licensePrefixes := []string{
			"/everstack.license.v1.LicenseService/",
		}

		for _, prefix := range licensePrefixes {
			// Capture prefix in closure
			p := prefix
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Add CORS headers for browser requests
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, X-User-Agent")
				w.Header().Set("Access-Control-Expose-Headers", "Connect-Content-Encoding, Connect-Timeout-Ms")

				if r.Method == "OPTIONS" {
					w.WriteHeader(http.StatusOK)
					return
				}

				// Create M2M authenticated HTTP client
				var httpClient *http.Client
				if m2mProvider != nil {
					httpClient = m2m.NewHTTPClient(m2mProvider, 30*time.Second)
					logger.WithFields("client", "portal", "path", r.URL.Path).Debug("license_proxy: using new M2M authenticated client")
				} else {
					// Fallback: try without authentication (will likely fail)
					httpClient = &http.Client{Timeout: 30 * time.Second}
					logger.WithFields("path", r.URL.Path).Warn("license_proxy: no M2M provider available, request may fail")
				}

				// Create reverse proxy for this request
				targetURL, _ := url.Parse(licenseURLStr)
				proxy := nethttputil.NewSingleHostReverseProxy(targetURL)
				proxy.Transport = httpClient.Transport

				// Fix the director to properly set the host
				originalDirector := proxy.Director
				proxy.Director = func(req *http.Request) {
					originalDirector(req)
					req.Host = targetURL.Host
					req.Header.Del("Origin")
					req.Header.Del("Referer")
				}

				proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
					logger.WithFields("path", r.URL.Path, "error", e.Error()).Error("license proxy error")
					http.Error(w, "License service unavailable", http.StatusBadGateway)
				}

				proxy.ServeHTTP(w, r)
				_ = p // use captured prefix (for logging if needed)
			})

			apis.RegisterHandlerPrefixes(handler, prefix)
			apis.AddExternalServicePrefixes(prefix)
		}
		logger.WithFields("url", licenseURLStr).Info("License service proxy registered with M2M authentication")
	}

	// Auth Service proxy for managed (cloud) instances.
	// Proxies browser auth, OAuth PKCE, and AuthService RPCs to the auth service.
	// OAuth callbacks therefore set cookies on the instance's domain.
	if authServiceURL := os.Getenv("EVS_AUTH_SERVICE_URL"); authServiceURL != "" {
		authTarget, err := url.Parse(authServiceURL)
		if err != nil {
			logger.WithError(err).Warn("auth_proxy: invalid EVS_AUTH_SERVICE_URL, skipping")
		} else {
			authProxy := nethttputil.NewSingleHostReverseProxy(authTarget)
			originalDirector := authProxy.Director
			authProxy.Director = func(req *http.Request) {
				// Preserve the original instance domain so the auth service knows
				// where to redirect (for cookie domain and redirect URIs).
				// Prefer existing X-Forwarded-Host (set by control plane proxy)
				// over req.Host (which may be the container's internal address).
				fwdHost := req.Header.Get("X-Forwarded-Host")
				if fwdHost == "" {
					fwdHost = req.Host
				}
				fwdProto := req.Header.Get("X-Forwarded-Proto")
				if fwdProto == "" {
					if req.TLS != nil {
						fwdProto = "https"
					} else {
						fwdProto = "http"
					}
				}
				originalDirector(req)
				req.Header.Set("X-Forwarded-Host", fwdHost)
				req.Header.Set("X-Forwarded-Proto", fwdProto)
				req.Host = authTarget.Host
			}
			authProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
				logger.WithFields("path", r.URL.Path, "error", e.Error()).Error("auth proxy error")
				http.Error(w, "Auth service unavailable", http.StatusBadGateway)
			}

			// Proxy HTTP auth endpoints (/api/auth/signin, /api/auth/callback, etc.)
			// Register on the top-level router with a PathPrefix to ensure gorilla mux
			// matches these before the SPA catch-all.
			authHTTPHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authProxy.ServeHTTP(w, r)
			})
			router.PathPrefix("/api/auth/").Handler(authHTTPHandler)
			router.PathPrefix("/oauth/").Handler(authHTTPHandler)
			router.Handle("/.well-known/oauth-authorization-server", authHTTPHandler)

			// Proxy AuthService ConnectRPC endpoints
			apis.RegisterHandlerPrefixes(authHTTPHandler, "/everstack.auth.v1.AuthService/")
			apis.AddExternalServicePrefixes("/everstack.auth.v1.AuthService/")

			logger.WithFields("url", authServiceURL).Info("Auth service proxy registered for managed instance")
		}
	}

	// Serve the web UI (SPA) at "/". This will serve files from apps/web/dist
	// or EVS_UI_DIST if provided, with index.html fallback for client routes.

	// Initialize rate limit monitoring with provider catalog
	if cat, err := ratelimit.NewCatalogFromDefaults(); err == nil {
		ratelimit.GlobalMonitor.SetProviderCatalog(cat)
		logger.Infof("Rate limit monitoring initialized with provider catalog")
		logger.Infof("Providers initialized: %v", cat.GetAllProviderNames())
	} else {
		logger.Warnf("Failed to load provider catalog for rate limiting, using fallback: %v", err)
	}

	// Note: License monitor endpoints are now served via the GatewayService proto:
	// - GetLicenseMonitorStatus (replaces /api/v1/license/status)
	// - RefreshLicenseMonitor (replaces /api/v1/license/refresh)
	// - GetPlans (replaces /api/v1/license/plans)
	// - GetTrialStatus (replaces /api/v1/trial/status)

	// SPA catch-all MUST be last — use a MatcherFunc to exclude known API paths
	spaHandler := ui.NewSPAHandler()
	router.PathPrefix("/").MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
		// Don't match paths handled by explicit routes (OAuth, webhooks, etc.)
		p := r.URL.Path
		if strings.HasPrefix(p, "/mcp/oauth/") {
			return false
		}
		if strings.HasPrefix(p, "/api/auth/") {
			return false
		}
		if strings.HasPrefix(p, "/oauth/") || p == "/.well-known/oauth-authorization-server" {
			return false
		}
		return true
	}).Handler(spaHandler)

	// Watch the config file and hot-reload gateway/features without restart
	startConfigWatcher(ctx, configPath, gwServer)

	return apis, nil
}

func supportsCatalogProjection(primary *database.Conn) bool {
	return primary != nil && primary.RW != nil && primary.Type == database.TypePostgres
}

func newTrialManager(ctx context.Context, managedGateway bool, redisClient *cache.RedisClient) (*trial.Manager, error) {
	if managedGateway {
		return nil, nil
	}

	var rawRedisClient *redis.Client
	if redisClient != nil {
		rawRedisClient = redisClient.Client()
	}

	trialMgr := trial.NewManager(trial.DefaultConfig(), trial.NewHybridStorage(rawRedisClient))
	if err := trialMgr.Initialize(ctx); err != nil {
		return nil, err
	}
	return trialMgr, nil
}

func shouldStartSelfHostedCloudLicensing(managedGateway bool) bool {
	return !managedGateway && enterprise.Edition() != "ce"
}

// legacy loader removed; activation now uses services/config/config.yaml

func listenAndServe(ctx context.Context, router *mux.Router, port uint16, externalPort uint16, tlsConfig *tls.Config, shutdown <-chan os.Signal, managedStorageDefaults storagepkg.ManagedDefaultEnsurer) error {
	http2Server := &http2.Server{}
	// Cookie-session enrichment must wrap the WHOLE handler tree, not
	// be installed via router.Use(). gorilla/mux's Use() does not
	// propagate to subrouters, and Connect-RPC services register on
	// subrouters created via router.PathPrefix(...).Subrouter() in
	// internal/api/api.go's RegisterHandlerPrefixes. Without this
	// wrap, calls to /everstack.agents.v1.AgentsService/* never see
	// the middleware → cloud_user_id stays empty → resolveUserID
	// falls through to "admin" on every cookie-authenticated request.
	cookieAuth := cookieSessionAuthMiddleware(func() *sqlx.DB { return cookieSessionAuthDB })
	managedStorageBootstrap := newManagedStorageTenantBootstrap(managedStorageDefaults)
	wrapped := withInFlight(http_mw.CallDurationHandler(cookieAuth(managedStorageBootstrap.Wrap(router))))
	http1Server := &http.Server{Handler: h2c.NewHandler(wrapped, http2Server), TLSConfig: tlsConfig}

	lc := internalnet.ListenConfig()
	lis, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("tcp listener on %d failed: %w", port, err)
	}

	errCh := make(chan error)

	go func() {
		logger.Debugf("Port: %d, ExternalPort: %d", port, externalPort)
		if externalPort != 0 && externalPort != port {
			logger.Infof("server is listening on [::]:%d", externalPort)
		} else {
			logger.Infof("server is listening on %s", lis.Addr().String())
		}
		if tlsConfig != nil {
			// we don't need to pass the files here, because we already initialized the TLS config on the server
			errCh <- http1Server.ServeTLS(lis, "", "")
		} else {
			errCh <- http1Server.Serve(lis)
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("error starting server: %w", err)
	case <-shutdown:
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return shutdownServer(ctx, http1Server)
	case <-ctx.Done():
		return shutdownServer(ctx, http1Server)
	}
}

func shutdownServer(ctx context.Context, server *http.Server) error {
	// Shut down sandbox proxy first (before agents, so in-flight proxy requests drain)
	if globalSandboxProxyShutdown != nil {
		fmt.Printf("[Shutdown] Shutting down sandbox proxy...\n")
		if err := globalSandboxProxyShutdown(ctx); err != nil {
			logger.WithFields("error", err.Error()).Warn("Error shutting down sandbox proxy")
		}
	}

	// Gracefully shut down agent sessions (checkpoint in-flight turns)
	if globalAgentsShutdown != nil {
		fmt.Printf("[Shutdown] Shutting down agent sessions...\n")
		if err := globalAgentsShutdown(ctx); err != nil {
			logger.WithFields("error", err.Error()).Warn("Error shutting down agent sessions")
		}
	}

	// Shutdown OTEL and collector
	if globalOTELShutdown != nil {
		fmt.Printf("[Shutdown] Shutting down OpenTelemetry...\n")
		globalOTELShutdown()
	}
	if globalCollectorShutdown != nil {
		fmt.Printf("[Shutdown] Shutting down embedded collector...\n")
		if err := globalCollectorShutdown(); err != nil {
			logger.WithFields("error", err.Error()).Warn("Error shutting down collector")
		}
	}

	err := server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("could not shutdown gracefully: %w", err)
	}
	logger.New().Info("server shutdown gracefully")
	return nil
}

func newCLIDeviceTokenManager(config *validator.Config, cloudAuthProvider bool) *deviceauth.TokenManager {
	var masterKey []byte

	// Hosted gateways already receive this key from everstack-secrets. Use it
	// first so device tokens do not depend on the chart's cookie/session value.
	if svcCfg, err := servicescfg.Load(""); err == nil && svcCfg != nil {
		encodedKey := strings.TrimSpace(svcCfg.Security.M2M.Simple.SigningKey)
		if encodedKey != "" {
			decoded, decodeErr := base64.StdEncoding.DecodeString(encodedKey)
			if decodeErr != nil {
				logger.WithError(decodeErr).Warn("cli: could not decode the M2M signing key")
			} else {
				masterKey = decoded
			}
		}
	}

	if len(masterKey) == 0 && !cloudAuthProvider && config != nil && config.Auth != nil && config.Auth.Mode == "builtin" {
		masterKey = []byte(config.Auth.Builtin.SessionSecret)
	}
	if len(masterKey) == 0 {
		return nil
	}

	manager, err := deviceauth.NewTokenManager(masterKey, 90*24*time.Hour)
	if err != nil {
		logger.WithError(err).Warn("cli: device token signing and validation are not configured")
		return nil
	}
	return manager
}

func authExternalURL(server *validator.ServerConfig) string {
	if server == nil {
		return ""
	}
	domain := strings.TrimSpace(server.Config.ExternalDomain)
	if domain == "" {
		return ""
	}
	scheme := "http"
	if server.Config.ExternalSecure || server.TLS.Enabled {
		scheme = "https"
	}
	parsed := &url.URL{Scheme: scheme, Host: domain}
	port := server.Config.ExternalPort
	if port > 0 && parsed.Port() == "" && !((scheme == "https" && port == 443) || (scheme == "http" && port == 80)) {
		parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port))
	}
	return strings.TrimRight(parsed.String(), "/")
}

// Helper functions for M2M config conversion

func convertSimpleConfig(cfg *servicescfg.M2MSimpleConfig) *m2m.ServicesSimpleConfig {
	if cfg == nil {
		return nil
	}
	// The signing key in the config is base64-encoded, but the M2M package expects
	// the raw bytes as a string. We need to keep it as the base64 string here because
	// the m2m.ConfigFromServices function will decode it.
	return &m2m.ServicesSimpleConfig{
		SigningKey: cfg.SigningKey,
		Issuer:     cfg.Issuer,
		Audience:   cfg.Audience,
		TokenTTL:   cfg.TokenTTL,
	}
}

func convertOIDCConfig(cfg *servicescfg.M2MOIDCConfig) *m2m.ServicesOIDCConfig {
	if cfg == nil {
		return nil
	}
	return &m2m.ServicesOIDCConfig{
		IssuerURL:       cfg.IssuerURL,
		TokenURL:        cfg.TokenURL,
		JWKSURL:         cfg.JWKSURL,
		Audience:        cfg.Audience,
		Scopes:          cfg.Scopes,
		SkipIssuerCheck: cfg.SkipIssuerCheck,
	}
}

func convertClients(clients map[string]servicescfg.M2MClientCredentials) map[string]m2m.ServicesClientCredentials {
	if clients == nil {
		return nil
	}
	result := make(map[string]m2m.ServicesClientCredentials, len(clients))
	for name, cred := range clients {
		result[name] = m2m.ServicesClientCredentials{
			ClientID:     cred.ClientID,
			ClientSecret: cred.ClientSecret,
			Scopes:       cred.Scopes,
		}
	}
	return result
}

func convertScopePolicy(policy *servicescfg.M2MScopePolicyConfig) *m2m.ServicesScopePolicyConfig {
	if policy == nil {
		return nil
	}
	result := &m2m.ServicesScopePolicyConfig{
		AutoDerive:     policy.AutoDerive,
		ActionPatterns: make([]m2m.ServicesActionPattern, len(policy.ActionPatterns)),
	}
	for i, p := range policy.ActionPatterns {
		result.ActionPatterns[i] = m2m.ServicesActionPattern{
			Prefix: p.Prefix,
			Action: p.Action,
		}
	}
	return result
}

// getEnvOrDefault returns the environment variable value or the default.
func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envBoolValue(key string) (bool, bool) {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return false, false
	}
	switch v {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func publicModelMetricsEnabled(managedGateway bool) bool {
	if !managedGateway {
		return false
	}
	enabled, configured := envBoolValue("EVS_PUBLIC_MODEL_METRICS_ENABLED")
	return !configured || enabled
}

func publicModelMetricsConfig(now time.Time) modelmetrics.Config {
	testingUntil := publicModelMetricsTestingUntil(now.UTC())
	thresholdValue := envUint64AtLeast
	if !testingUntil.IsZero() {
		thresholdValue = positiveEnvUint64
	}
	return modelmetrics.Config{
		MinimumTenants: thresholdValue(
			"EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS",
			modelmetrics.MinimumPublicTenants,
		),
		MinimumRequests: thresholdValue(
			"EVS_PUBLIC_MODEL_METRICS_MIN_REQUESTS",
			modelmetrics.MinimumPublicRequests,
		),
		TestingThresholdsUntil: testingUntil,
		FirstPartyTenants:      publicModelMetricsFirstPartyTenants(),
	}
}

// publicModelMetricsFirstPartyTenants reads the comma-separated list of tenant
// ids that Everstack operates for itself. Buckets built only from these
// tenants are self-disclosure and skip the k-anonymity floor, which is what
// lets the public catalog show real activity before the platform has enough
// distinct tenants to anonymise anyone. Listing a customer tenant here would
// publish that customer's usage, so the value is deliberately operator-only
// and never derived from request data.
// DefaultPublicModelMetricsOrigin is the only site allowed to read the public
// model metrics responses from a browser by default.
const DefaultPublicModelMetricsOrigin = "https://everstack.ai"

// publicModelMetricsOrigins returns the browser origins allowed to read the
// public model metrics routes. Override with a comma-separated
// EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS for preview deployments.
func publicModelMetricsOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS"))
	if raw == "" {
		return []string{DefaultPublicModelMetricsOrigin}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" && trimmed != "*" {
			origins = append(origins, trimmed)
		}
	}
	if len(origins) == 0 {
		return []string{DefaultPublicModelMetricsOrigin}
	}
	return origins
}

// publicModelMetricsCORS replaces the gateway-wide `*` CORS headers with an
// exact-origin allowlist for the public model metrics routes.
//
// Two things to know about the scope of this:
//
//   - CORS is not access control. It stops other *sites* from reading these
//     responses in a browser; it does nothing about a direct client. The
//     privacy floor in internal/modelmetrics is what actually bounds what
//     these routes disclose.
//   - The outer rs/cors middleware answers preflight itself and never calls
//     through to this handler, so OPTIONS still gets the gateway-wide policy.
//     Simple GETs, which is all these routes accept, do not preflight.
//
// Credentials are always denied: these routes are anonymous reads, and
// `*`-with-credentials is the combination browsers reject outright.
func publicModelMetricsCORS(next http.Handler) http.Handler {
	allowed := publicModelMetricsOrigins()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Del("Access-Control-Allow-Origin")
		header.Del("Access-Control-Allow-Credentials")
		// Caches keyed without Origin would hand one origin's response to
		// another requester.
		header.Add("Vary", "Origin")

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			for _, candidate := range allowed {
				if strings.EqualFold(origin, candidate) {
					header.Set("Access-Control-Allow-Origin", candidate)
					break
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func publicModelMetricsFirstPartyTenants() []string {
	raw := strings.TrimSpace(os.Getenv("EVS_PUBLIC_MODEL_METRICS_FIRST_PARTY_TENANTS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tenants := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			tenants = append(tenants, trimmed)
		}
	}
	if len(tenants) == 0 {
		return nil
	}
	logger.WithFields(
		"tenant_count", len(tenants),
	).Warn("public model metrics first-party disclosure enabled; buckets built only from these tenants bypass the privacy floor")
	return tenants
}

func publicModelMetricsTestingUntil(now time.Time) time.Time {
	raw := strings.TrimSpace(os.Getenv("EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL"))
	if raw == "" {
		return time.Time{}
	}
	testingUntil, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		logger.WithFields(
			"key", "EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL",
			"value", raw,
		).Warn("invalid RFC3339 model metrics testing expiry; using public privacy floors")
		return time.Time{}
	}
	testingUntil = testingUntil.UTC()
	if !testingUntil.After(now) {
		logger.WithFields(
			"key", "EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL",
			"value", raw,
		).Warn("model metrics testing expiry is not in the future; using public privacy floors")
		return time.Time{}
	}
	if testingUntil.Sub(now) > modelmetrics.MaximumTestingThresholdWindow {
		logger.WithFields(
			"key", "EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL",
			"value", raw,
			"maximum_window", modelmetrics.MaximumTestingThresholdWindow.String(),
		).Warn("model metrics testing window is too long; using public privacy floors")
		return time.Time{}
	}
	return testingUntil
}

func positiveEnvUint64(key string, fallback uint64) uint64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		logger.WithFields("key", key, "value", raw).Warn("invalid positive integer environment value; using fallback")
		return fallback
	}
	return value
}

func envUint64AtLeast(key string, minimum uint64) uint64 {
	value := positiveEnvUint64(key, minimum)
	if value < minimum {
		logger.WithFields(
			"key", key,
			"value", value,
			"minimum", minimum,
		).Warn("environment value is below the public privacy floor; using minimum")
		return minimum
	}
	return value
}

// reconcilerEnabled reports whether the async sandbox lifecycle
// reconciler should be wired up. Off by default; flip
// EVS_SANDBOX_RECONCILER_ENABLED=true to opt in. Behind a flag so
// dev can iterate independently of production rollout.
func reconcilerEnabled() bool {
	enabled, _ := envBoolValue("EVS_SANDBOX_RECONCILER_ENABLED")
	return enabled
}

// sandboxOwnershipEnforceEnabled reports whether the sandbox-ownership
// interceptor should hard-reject cross-tenant access instead of only
// audit-logging would-be rejections. Off by default; flip
// EVS_SANDBOX_OWNERSHIP_ENFORCE=true after the audit logs run clean.
func sandboxOwnershipEnforceEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("EVS_SANDBOX_OWNERSHIP_ENFORCE")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// postgresDSNForListener returns the connection string for the
// sandbox EventBus's LISTEN connection. Prefers the validated config
// value, falls back to the env var (matching how the rest of the
// gateway resolves Postgres). Returns empty when neither is set —
// the SSE feature stays off in that case.
func postgresDSNForListener(config *validator.Config) string {
	if config != nil {
		if dsn := strings.TrimSpace(config.Database.Postgres.DSN); dsn != "" {
			return dsn
		}
	}
	return strings.TrimSpace(os.Getenv("EVS_POSTGRES_DSN"))
}

// getEnvIntOrDefault returns a parsed int env var value or the default.
func getEnvIntOrDefault(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		logger.WithFields("key", key, "value", v).Warn("sandbox: invalid integer env var; using default")
		return defaultVal
	}
	return n
}

func ensureWritableDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	f, err := os.CreateTemp(path, ".mf-fc-writecheck-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func validateFirecrackerConfig(cfg sandboxfirecracker.FirecrackerConfig) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("firecracker backend requires Linux host, got %s", runtime.GOOS)
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return fmt.Errorf("firecracker binary path is empty")
	}
	if strings.TrimSpace(cfg.KernelPath) == "" {
		return fmt.Errorf("firecracker kernel path is empty")
	}
	if strings.TrimSpace(cfg.RootfsDir) == "" {
		return fmt.Errorf("firecracker rootfs_dir is empty")
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		return fmt.Errorf("firecracker work_dir is empty")
	}
	if _, err := os.Stat(cfg.KernelPath); err != nil {
		return fmt.Errorf("kernel image not accessible at %s: %w", cfg.KernelPath, err)
	}
	baseRootfs := filepath.Join(cfg.RootfsDir, "base.ext4")
	if _, err := os.Stat(baseRootfs); err != nil {
		return fmt.Errorf("base rootfs image not accessible at %s: %w", baseRootfs, err)
	}
	if err := ensureWritableDir(cfg.WorkDir); err != nil {
		return fmt.Errorf("work_dir not writable (%s): %w", cfg.WorkDir, err)
	}
	return nil
}

// resolveSandboxDataDir returns a writable sandbox data directory.
// If EVS_SANDBOX_DATA_DIR is set, it must be writable.
// If not set, it falls back from /var/lib/everstack/sandboxes to a temp dir for local dev.
// featurePathGate is a gorilla mux middleware that rejects requests to
// /v1/agents/* or /v1/mcp/* when the tenant has those features
// disabled in runtime_config. Returns 404 (mirrors "feature not
// available" semantics rather than leaking that the route exists but
// is gated).
func featurePathGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isAgents := strings.HasPrefix(path, "/v1/agents/")
		isMCP := strings.HasPrefix(path, "/v1/mcp/")
		if !isAgents && !isMCP {
			next.ServeHTTP(w, r)
			return
		}
		svcAny := r.Context().Value(contextkeys.RuntimeConfigService)
		svc, ok := svcAny.(*rtconfig.Service)
		if !ok || svc == nil {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := contextkeys.ExtractTenantID(r.Context())
		feats := svc.GetFeatures(tenantID)
		if isAgents && !feats.EnableAgents {
			http.Error(w, `{"error":"agents are disabled for this tenant"}`, http.StatusNotFound)
			return
		}
		if isMCP && feats.MCPGateway != nil && !feats.MCPGateway.Enabled {
			http.Error(w, `{"error":"mcp gateway is disabled for this tenant"}`, http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type sshRuntimeConfig struct {
	ListenAddr string
	Host       string
	PublicPort int
}

func resolveSSHRuntimeConfig(cfg *validator.SandboxFeaturesConfig) sshRuntimeConfig {
	listenAddr := ""
	host := ""
	if cfg != nil {
		listenAddr = strings.TrimSpace(cfg.SSH.ListenAddr)
		host = strings.TrimSpace(cfg.SSH.Host)
	}
	if v := strings.TrimSpace(os.Getenv("EVS_SSH_LISTEN_ADDR")); v != "" {
		listenAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("EVS_SSH_HOST")); v != "" {
		host = v
	}
	if listenAddr == "" {
		listenAddr = ":2222"
	}
	if host == "" {
		if composed := region.FromEnv().ComposeHost("ssh", "evs.run"); composed != "" && composed != "ssh.evs.run" {
			host = composed
		} else {
			host = "localhost"
		}
	}

	publicPort := 2222
	if v := strings.TrimSpace(os.Getenv("EVS_SSH_PUBLIC_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			publicPort = p
		}
	} else if _, portStr, err := net.SplitHostPort(listenAddr); err == nil {
		if p, pErr := strconv.Atoi(portStr); pErr == nil && p > 0 {
			publicPort = p
		}
	}
	return sshRuntimeConfig{ListenAddr: listenAddr, Host: host, PublicPort: publicPort}
}

func resolveSSHHostKeyPath() (string, bool, error) {
	if path := strings.TrimSpace(os.Getenv("EVS_SSH_HOST_KEY_PATH")); path != "" {
		return path, true, nil
	}
	dataDir, err := resolveSandboxDataDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(dataDir, "ssh_gateway_host_ed25519_key"), false, nil
}

func loadSSHHostKeySigner(allowGenerate bool) (cryptossh.Signer, error) {
	hostKeyPath, explicit, err := resolveSSHHostKeyPath()
	if err != nil {
		return nil, fmt.Errorf("resolve host key path: %w", err)
	}
	if explicit || !allowGenerate {
		return sshpkg.LoadGatewayHostKey(hostKeyPath)
	}
	return sshpkg.GenerateGatewayHostKey(hostKeyPath)
}

func resolveSandboxDataDir() (string, error) {
	const defaultDir = "/var/lib/everstack/sandboxes"
	configuredDir := strings.TrimSpace(os.Getenv("EVS_SANDBOX_DATA_DIR"))
	dir := configuredDir
	if dir == "" {
		dir = defaultDir
	}

	ensureWritable := func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if mkErr := os.MkdirAll(path, 0755); mkErr != nil {
					return mkErr
				}
				info, err = os.Stat(path)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}
		if !info.IsDir() {
			return fmt.Errorf("sandbox data dir is not a directory: %s", path)
		}
		f, err := os.CreateTemp(path, ".mf-writecheck-*")
		if err != nil {
			return err
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return nil
	}

	if err := ensureWritable(dir); err == nil {
		return dir, nil
	} else if configuredDir != "" {
		return "", fmt.Errorf("sandbox data dir unavailable (%s): %w. Set EVS_SANDBOX_DATA_DIR to a writable directory", dir, err)
	}

	fallback := filepath.Join(os.TempDir(), "everstack", "sandboxes")
	if err := ensureWritable(fallback); err != nil {
		return "", fmt.Errorf("sandbox data dir unavailable (%s) and fallback failed (%s): %w. Set EVS_SANDBOX_DATA_DIR to a writable directory", dir, fallback, err)
	}

	logger.WithFields("from", dir, "to", fallback).
		Warn("sandbox: default sandbox data dir unavailable, using temp fallback")
	return fallback, nil
}

// resolveSandboxBackendType resolves sandbox backend in this order:
//  1. Explicit features.sandbox.backend config
//  2. EVS_SANDBOX_BACKEND environment variable
//  3. Runtime default (shared/cloud -> kubernetes, self-hosted -> docker)
func resolveSandboxBackendType(cfg *validator.SandboxFeaturesConfig, sharedMode bool) string {
	if cfg != nil {
		if backend := strings.TrimSpace(cfg.Backend); backend != "" {
			return normalizeSandboxBackend(backend, sharedMode)
		}
	}
	if backend := strings.TrimSpace(os.Getenv("EVS_SANDBOX_BACKEND")); backend != "" {
		return normalizeSandboxBackend(backend, sharedMode)
	}
	if sharedMode {
		return "kubernetes"
	}
	return "docker"
}

func normalizeSandboxBackend(backend string, sharedMode bool) string {
	resolved := strings.ToLower(strings.TrimSpace(backend))
	if sharedMode && resolved == "docker" {
		logger.WithFields("backend", resolved).Warn(
			"sandbox: docker backend is not supported in shared mode; using kubernetes",
		)
		return "kubernetes"
	}
	return resolved
}

// initSandboxManager creates a sandbox manager backed by Docker, Kubernetes, or Firecracker.
// If cfg is non-nil and enabled, the YAML features.sandbox config is used.
// Otherwise, falls back to env vars (EVS_SANDBOX_BACKEND, etc.) for backward compat.
// In shared/cloud mode, default backend is Kubernetes. In self-hosted mode, default is Docker.
// Returns nil if the selected backend is not available.
func initSandboxManager(cfg *validator.SandboxFeaturesConfig, sharedMode bool) *sandbox.SandboxManager {
	backendType := resolveSandboxBackendType(cfg, sharedMode)

	var backend sandbox.Backend
	var err error

	switch backendType {
	case "firecracker":
		if runtime.GOOS != "linux" {
			err = fmt.Errorf("firecracker backend requires Linux host, got %s", runtime.GOOS)
			break
		}

		fcCfg := sandboxfirecracker.DefaultFirecrackerConfig()

		// YAML config takes precedence, then env vars.
		if cfg != nil {
			if cfg.Firecracker.BinaryPath != "" {
				fcCfg.BinaryPath = cfg.Firecracker.BinaryPath
			}
			if cfg.Firecracker.KernelPath != "" {
				fcCfg.KernelPath = cfg.Firecracker.KernelPath
			}
			if cfg.Firecracker.RootfsDir != "" {
				fcCfg.RootfsDir = cfg.Firecracker.RootfsDir
			}
			if cfg.Firecracker.WorkDir != "" {
				fcCfg.WorkDir = cfg.Firecracker.WorkDir
			}
			if cfg.Firecracker.PoolMinSize > 0 {
				fcCfg.Pool.MinSize = cfg.Firecracker.PoolMinSize
			}
			if cfg.Firecracker.PoolMaxSize > 0 {
				fcCfg.Pool.MaxSize = cfg.Firecracker.PoolMaxSize
			}
			if cfg.Firecracker.PoolMaxTotal > 0 {
				fcCfg.Pool.MaxTotal = cfg.Firecracker.PoolMaxTotal
			}
			if cfg.Firecracker.ReplenishIntervalMs > 0 {
				fcCfg.Pool.ReplenishInterval = time.Duration(cfg.Firecracker.ReplenishIntervalMs) * time.Millisecond
			}
			if cfg.Firecracker.ReplenishBatch > 0 {
				fcCfg.Pool.ReplenishBatch = cfg.Firecracker.ReplenishBatch
			}
			if cfg.Firecracker.WarmupOnStart {
				fcCfg.Pool.WarmupOnStart = true
			}
		}

		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_BINARY")); v != "" {
			fcCfg.BinaryPath = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_KERNEL")); v != "" {
			fcCfg.KernelPath = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_ROOTFS_DIR")); v != "" {
			fcCfg.RootfsDir = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_WORK_DIR")); v != "" {
			fcCfg.WorkDir = v
		}
		fcCfg.Pool.MinSize = getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_POOL_MIN_SIZE", fcCfg.Pool.MinSize)
		fcCfg.Pool.MaxSize = getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_POOL_MAX_SIZE", fcCfg.Pool.MaxSize)
		fcCfg.Pool.MaxTotal = getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_POOL_MAX_TOTAL", fcCfg.Pool.MaxTotal)
		fcCfg.Pool.ReplenishBatch = getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_REPLENISH_BATCH", fcCfg.Pool.ReplenishBatch)
		if intervalMs := getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_REPLENISH_INTERVAL_MS", int(fcCfg.Pool.ReplenishInterval.Milliseconds())); intervalMs > 0 {
			fcCfg.Pool.ReplenishInterval = time.Duration(intervalMs) * time.Millisecond
		}

		// If a global sandbox data dir is explicitly set and firecracker-specific
		// paths are not, derive sensible defaults under that directory.
		if dataDir := strings.TrimSpace(os.Getenv("EVS_SANDBOX_DATA_DIR")); dataDir != "" {
			if cfg == nil || cfg.Firecracker.RootfsDir == "" {
				if strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_ROOTFS_DIR")) == "" {
					fcCfg.RootfsDir = filepath.Join(dataDir, "rootfs")
				}
			}
			if cfg == nil || cfg.Firecracker.WorkDir == "" {
				if strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_WORK_DIR")) == "" {
					fcCfg.WorkDir = filepath.Join(dataDir, "vms")
				}
			}
		}

		if fcCfg.Pool.MinSize > fcCfg.Pool.MaxSize {
			err = fmt.Errorf("invalid firecracker pool config: min_size (%d) > max_size (%d)", fcCfg.Pool.MinSize, fcCfg.Pool.MaxSize)
			break
		}
		if fcCfg.Pool.MaxSize > fcCfg.Pool.MaxTotal {
			err = fmt.Errorf("invalid firecracker pool config: max_size (%d) > max_total (%d)", fcCfg.Pool.MaxSize, fcCfg.Pool.MaxTotal)
			break
		}
		if err = validateFirecrackerConfig(fcCfg); err != nil {
			break
		}

		backend, err = sandboxfirecracker.New(fcCfg)

	case "firecracker-agent":
		fcaCfg := sandboxfcagent.Config{
			Port:            9090,
			RefreshInterval: 10 * time.Second,
			DialTimeout:     5 * time.Second,
		}
		if cfg != nil {
			if cfg.FirecrackerAgent.Service != "" {
				fcaCfg.Service = cfg.FirecrackerAgent.Service
			}
			if cfg.FirecrackerAgent.Port > 0 {
				fcaCfg.Port = cfg.FirecrackerAgent.Port
			}
			if cfg.FirecrackerAgent.RefreshIntervalMs > 0 {
				fcaCfg.RefreshInterval = time.Duration(cfg.FirecrackerAgent.RefreshIntervalMs) * time.Millisecond
			}
			if cfg.FirecrackerAgent.DialTimeoutMs > 0 {
				fcaCfg.DialTimeout = time.Duration(cfg.FirecrackerAgent.DialTimeoutMs) * time.Millisecond
			}
		}
		if cfg != nil {
			fcaCfg.TLS = sandboxfcagent.TLSConfig{
				ClientCert: cfg.FirecrackerAgent.TLSClientCert,
				ClientKey:  cfg.FirecrackerAgent.TLSClientKey,
				ServerCA:   cfg.FirecrackerAgent.TLSServerCA,
				ServerName: cfg.FirecrackerAgent.TLSServerName,
			}
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_SERVICE")); v != "" {
			fcaCfg.Service = v
		}
		fcaCfg.Port = getEnvIntOrDefault("EVS_SANDBOX_FIRECRACKER_AGENT_PORT", fcaCfg.Port)
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_TLS_CLIENT_CERT")); v != "" {
			fcaCfg.TLS.ClientCert = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_TLS_CLIENT_KEY")); v != "" {
			fcaCfg.TLS.ClientKey = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_TLS_SERVER_CA")); v != "" {
			fcaCfg.TLS.ServerCA = v
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_FIRECRACKER_AGENT_TLS_SERVER_NAME")); v != "" {
			fcaCfg.TLS.ServerName = v
		}
		if fcaCfg.Service == "" {
			err = fmt.Errorf("firecracker-agent backend requires features.sandbox.firecracker_agent.service or EVS_SANDBOX_FIRECRACKER_AGENT_SERVICE")
			break
		}
		backend, err = sandboxfcagent.New(fcaCfg)

	case "kubernetes":
		k8sCfg := sandboxk8s.KubernetesConfig{
			LabelPrefix: "everstack.sandbox",
		}
		// YAML config takes precedence, then env vars
		if cfg != nil && cfg.Kubernetes.Kubeconfig != "" {
			k8sCfg.Kubeconfig = cfg.Kubernetes.Kubeconfig
		} else if v := os.Getenv("EVS_SANDBOX_KUBECONFIG"); v != "" {
			k8sCfg.Kubeconfig = v
		}
		if cfg != nil && cfg.Kubernetes.Namespace != "" {
			k8sCfg.Namespace = cfg.Kubernetes.Namespace
		} else {
			k8sCfg.Namespace = getEnvOrDefault("EVS_SANDBOX_K8S_NAMESPACE", "everstack-sandboxes")
		}
		if cfg != nil && cfg.Kubernetes.ImagePullPolicy != "" {
			k8sCfg.ImagePullPolicy = cfg.Kubernetes.ImagePullPolicy
		}
		if cfg != nil && cfg.Kubernetes.ServiceAccount != "" {
			k8sCfg.ServiceAccount = cfg.Kubernetes.ServiceAccount
		}
		if cfg != nil && len(cfg.Kubernetes.NodeSelector) > 0 {
			k8sCfg.NodeSelector = cfg.Kubernetes.NodeSelector
		}
		if cfg != nil && len(cfg.Kubernetes.ImagePullSecrets) > 0 {
			k8sCfg.ImagePullSecrets = cfg.Kubernetes.ImagePullSecrets
		}
		if v := strings.TrimSpace(os.Getenv("EVS_SANDBOX_K8S_IMAGE_PULL_SECRETS")); v != "" {
			// Comma-separated env-var override so a cluster operator
			// can attach a registry credential without rebuilding the
			// gateway config blob. Splits / trims here so the typed
			// path stays []string.
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s != "" {
					k8sCfg.ImagePullSecrets = append(k8sCfg.ImagePullSecrets, s)
				}
			}
		}
		backend, err = sandboxk8s.New(k8sCfg)

	default: // "docker"
		backendType = "docker"
		dockerCfg := sandboxdocker.DockerConfig{
			LabelPrefix: "everstack.sandbox",
			AutoPull:    true,
			AutoBuild:   true,
		}
		if cfg != nil {
			if cfg.Docker.Host != "" {
				dockerCfg.Host = cfg.Docker.Host
			}
			dockerCfg.AutoPull = cfg.Docker.IsAutoPullEnabled()
			dockerCfg.AutoBuild = cfg.Docker.IsAutoBuildEnabled()
		}
		backend, err = sandboxdocker.New(dockerCfg)
	}

	if err != nil {
		log.Printf("[sandbox] %s sandbox backend unavailable: %v — sandbox feature disabled", backendType, err)
		return nil
	}

	globalCfg := sandbox.DefaultGlobalSandboxConfig()
	if err := applySandboxPricingFromPlans(&globalCfg.Pricing, sandboxPlansConfigPath()); err != nil {
		log.Printf("[sandbox] failed to load sandbox pricing from plans.json: %v", err)
	}
	globalCfg.Enabled = true
	globalCfg.Backend = backendType
	if dataDir := strings.TrimSpace(os.Getenv("EVS_SANDBOX_DATA_DIR")); dataDir != "" {
		globalCfg.DataDir = dataDir
	}

	// Override resource limits from YAML config when set.
	// max_sandboxes is the gateway-wide host-protection cap (count of live
	// instances on this process); per-tenant quotas are separately enforced
	// by the plan-tier resolver (checkPersistentAgentLimit). Idle-retention
	// limits are plan-tier-based via the license monitor's RetentionResolver.
	if cfg != nil {
		if cfg.MaxSandboxes > 0 {
			globalCfg.MaxSandboxes = cfg.MaxSandboxes
		}
		if cfg.DefaultImage != "" {
			globalCfg.DefaultImage = cfg.DefaultImage
		}
		if len(cfg.AllowedImages) > 0 {
			globalCfg.AllowedImages = cfg.AllowedImages
		}
		if cfg.MaxCPU > 0 {
			globalCfg.MaxCPU = cfg.MaxCPU
		}
		if cfg.MaxMemoryMB > 0 {
			globalCfg.MaxMemoryMB = cfg.MaxMemoryMB
		}
		if cfg.MaxDiskMB > 0 {
			globalCfg.MaxDiskMB = cfg.MaxDiskMB
		}
		if cfg.MaxTimeoutSecs > 0 {
			globalCfg.MaxTimeoutSecs = cfg.MaxTimeoutSecs
		}
		if len(cfg.DNSServers) > 0 {
			globalCfg.DNSServers = cfg.DNSServers
		}
		if cfg.DefaultKeepWarmIdleSecs > 0 {
			globalCfg.DefaultKeepWarmIdleSecs = cfg.DefaultKeepWarmIdleSecs
		}
		if cfg.MaxConcurrentCreates > 0 {
			globalCfg.MaxConcurrentCreates = cfg.MaxConcurrentCreates
		}
		if !sharedMode {
			applySandboxPricingOverrides(&globalCfg.Pricing, cfg.Pricing)
		}
	}
	if sharedMode {
		// Hosted sandboxes are never a free resource, including for tenants on
		// the Free platform plan. Deployment-level pricing overrides are ignored;
		// rates come only from the centrally shipped plans contract.
		globalCfg.Pricing.Enabled = true
	}

	mgr := sandbox.NewManager(backend, globalCfg)

	// Wire the fcagent backend's route-recovery persistence hook to
	// the manager. Every time withRoute finds a new host:port for a
	// sandbox (because its previous fcagent's pod was replaced),
	// setRoute fire-and-forgets a DB write through this hook so the
	// authoritative sandbox_instances.agent_target stays in sync.
	// Without it, gateway restarts re-seed in-memory routes from a
	// stale DB row — see RouteUpdater doc in fcagent/backend.go.
	if fc, ok := backend.(*sandboxfcagent.FCAgentBackend); ok {
		fc.SetRouteUpdater(mgr)
	}

	log.Printf("[sandbox] Sandbox manager initialized (backend=%s, max=%d)", backendType, globalCfg.MaxSandboxes)
	return mgr
}

func sandboxPlansConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("EVS_PLANS_CONFIG_PATH")); path != "" {
		return path
	}
	for _, path := range []string{
		internalcfg.DefaultPlansPath,
		filepath.Join("..", internalcfg.DefaultPlansPath),
		filepath.Join("..", "..", internalcfg.DefaultPlansPath),
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return internalcfg.DefaultPlansPath
}

func applySandboxPricingFromPlans(dst *sandbox.SandboxPricingConfig, plansPath string) error {
	if dst == nil {
		return nil
	}
	plans, err := internalcfg.LoadPlansConfig(plansPath)
	if err != nil {
		return err
	}
	pricing := plans.SandboxComputePricing
	if pricing == nil {
		return fmt.Errorf("sandbox_compute_pricing missing from %s", plansPath)
	}
	if pricing.CPUPerVCPUHour <= 0 || pricing.MemoryPerGiBHour <= 0 {
		return fmt.Errorf("sandbox_compute_pricing in %s must define positive CPU and memory rates", plansPath)
	}

	dst.Enabled = true
	if currency := strings.TrimSpace(pricing.Currency); currency != "" {
		dst.Currency = currency
	}
	dst.CPUPerHourUSD = pricing.CPUPerVCPUHour
	dst.MemoryGBPerHourUSD = pricing.MemoryPerGiBHour
	dst.DiskGBPerHourUSD = pricing.DiskPerGiBHour
	dst.PlatformFeePerHourUSD = pricing.PlatformPerSandboxHour
	// Storage allowance + tiering. Guarded with >0 so a plans.json that
	// omits these can't silently zero the included allowance (which would
	// bill the full root disk from the first GiB); the DefaultGlobalSandboxConfig
	// seed (20 GiB included in the machine rate, 50 GiB tier-2 threshold,
	// 1.25x) stays in effect.
	if pricing.IncludedDiskGiB > 0 {
		dst.IncludedDiskGiB = pricing.IncludedDiskGiB
	}
	if pricing.DiskTier2ThresholdGiB > 0 {
		dst.DiskTier2ThresholdGiB = pricing.DiskTier2ThresholdGiB
	}
	if pricing.DiskTier2Multiplier > 0 {
		dst.DiskTier2Multiplier = pricing.DiskTier2Multiplier
	}
	return nil
}

func loadBrowserUsagePricing(plansPath string) (browserpool.UsagePricing, error) {
	plans, err := internalcfg.LoadPlansConfig(plansPath)
	if err != nil {
		return browserpool.UsagePricing{}, err
	}
	pricing := plans.BrowserRuntimePricing
	if pricing == nil {
		return browserpool.UsagePricing{}, fmt.Errorf("browser_runtime_pricing missing from %s", plansPath)
	}
	if pricing.IdlePoolBilling {
		return browserpool.UsagePricing{}, fmt.Errorf("browser_runtime_pricing in %s must not bill idle pool capacity", plansPath)
	}
	if currency := strings.TrimSpace(pricing.Currency); !strings.EqualFold(currency, "USD") {
		return browserpool.UsagePricing{}, fmt.Errorf("browser_runtime_pricing in %s must use USD", plansPath)
	}
	return browserpool.NewUsagePricing(
		pricing.BrowserHour,
		pricing.BillingIncrementSeconds,
		pricing.MinimumSessionSeconds,
	)
}

func loadBrowserTenantLimits(plansPath string) (map[string]browserpool.TenantLimits, error) {
	plans, err := internalcfg.LoadPlansConfig(plansPath)
	if err != nil {
		return nil, err
	}
	limitsByTier := make(map[string]browserpool.TenantLimits, len(plans.Plans))
	for tier, plan := range plans.Plans {
		var (
			concurrent    int64
			sessionSecs   int64
			hasConcurrent bool
			hasSession    bool
		)
		for _, limit := range plan.UsageLimits {
			switch strings.ToUpper(strings.TrimSpace(limit.Type)) {
			case "CONCURRENT_BROWSERS":
				concurrent = limit.Value
				hasConcurrent = true
			case "BROWSER_SESSION_MAX_SECONDS":
				sessionSecs = limit.Value
				hasSession = true
			}
		}
		if !hasConcurrent || !hasSession {
			return nil, fmt.Errorf("browser limits missing for plan %q in %s", tier, plansPath)
		}
		if sessionSecs < -1 {
			return nil, fmt.Errorf("browser session limit for plan %q must be -1 or non-negative", tier)
		}
		maxSession := time.Duration(-1)
		if sessionSecs >= 0 {
			maxSession = time.Duration(sessionSecs) * time.Second
		}
		limits := browserpool.TenantLimits{
			MaxConcurrent: int(concurrent),
			MaxSession:    maxSession,
		}
		if err := limits.Validate(); err != nil {
			return nil, fmt.Errorf("invalid browser limits for plan %q: %w", tier, err)
		}
		limitsByTier[strings.ToLower(strings.TrimSpace(tier))] = limits
	}
	return limitsByTier, nil
}

func applySandboxPricingOverrides(dst *sandbox.SandboxPricingConfig, src validator.SandboxPricingConfig) {
	if dst == nil || !sandboxPricingConfigPresent(src) {
		return
	}

	// validator.SandboxPricingConfig uses zero values, so an omitted
	// pricing block looks like "disabled with all rates set to zero".
	// Only merge after sandboxPricingConfigPresent proves the operator
	// actually configured pricing.
	dst.Enabled = src.Enabled
	if currency := strings.TrimSpace(src.Currency); currency != "" {
		dst.Currency = currency
	}
	if src.CPUPerHourUSD > 0 {
		dst.CPUPerHourUSD = src.CPUPerHourUSD
	}
	if src.MemoryGBPerHourUSD > 0 {
		dst.MemoryGBPerHourUSD = src.MemoryGBPerHourUSD
	}
	if src.DiskGBPerHourUSD > 0 {
		dst.DiskGBPerHourUSD = src.DiskGBPerHourUSD
	}
	if src.PlatformFeePerHourUSD > 0 {
		dst.PlatformFeePerHourUSD = src.PlatformFeePerHourUSD
	}
	if src.IncludedDiskGiB > 0 {
		dst.IncludedDiskGiB = src.IncludedDiskGiB
	}
	if src.DiskTier2ThresholdGiB > 0 {
		dst.DiskTier2ThresholdGiB = src.DiskTier2ThresholdGiB
	}
	if src.DiskTier2Multiplier > 0 {
		dst.DiskTier2Multiplier = src.DiskTier2Multiplier
	}
	if len(src.TierMultipliers) > 0 {
		dst.TierMultipliers = src.TierMultipliers
	}
}

func sandboxPricingConfigPresent(src validator.SandboxPricingConfig) bool {
	return src.Enabled ||
		strings.TrimSpace(src.Currency) != "" ||
		src.CPUPerHourUSD != 0 ||
		src.MemoryGBPerHourUSD != 0 ||
		src.DiskGBPerHourUSD != 0 ||
		src.PlatformFeePerHourUSD != 0 ||
		len(src.TierMultipliers) > 0
}
