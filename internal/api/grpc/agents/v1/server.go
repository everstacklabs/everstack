package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	agentdeploy "github.com/everstacklabs/everstack/internal/agents/deployment"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentprojectruntime "github.com/everstacklabs/everstack/internal/agents/projectruntime"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	agenttrigger "github.com/everstacklabs/everstack/internal/agents/trigger"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	ghpkg "github.com/everstacklabs/everstack/internal/github"
	"github.com/everstacklabs/everstack/internal/interop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/memory"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	"github.com/everstacklabs/everstack/internal/sandbox/lcwebhooks"
	sandboxmetrics "github.com/everstacklabs/everstack/internal/sandbox/metrics"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
	"github.com/everstacklabs/everstack/internal/sandbox/previewurl"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshots"
	"github.com/everstacklabs/everstack/internal/sandbox/volstore"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
	sshpkg "github.com/everstacklabs/everstack/internal/ssh"
	"github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/everstacklabs/everstack/internal/telemetry/autoscorer"
	"github.com/everstacklabs/everstack/internal/trooper"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	ErrGitHubNotConfigured  = errors.New("GitHub App integration is not configured")
	ErrSandboxNotConfigured = errors.New("sandbox feature is not configured")
	ErrSandboxIDRequired    = errors.New("sandbox_id is required")
	ErrSSHNotConfigured     = errors.New("SSH feature is not configured")
)

// A revision may contain up to 8 MiB of source plus bounded paths, function
// metadata, and parameter schemas. Cap the decoded protobuf message before the
// handler allocates the much larger per-file maximum across all 512 files.
const maxCreateAgentRevisionReadBytes = 20 << 20

var _ agentspb.AgentsServiceServer = (*GrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

// Server implements the AgentsService Connect/gRPC service.
type Server struct {
	ctx                      context.Context          // Context containing CQRS system
	engine                   *agentrt.Engine          // Agent runtime engine
	sessionMgr               *agentrt.SessionManager  // Process-level session registry
	db                       *sqlx.DB                 // Database for direct queries (approvals, etc.)
	storageCredentialStore   storagecredentials.Store // Encrypted external storage credentials (may be nil)
	sandboxMgr               *sandbox.SandboxManager  // Sandbox environment manager (may be nil)
	browserPool              *browserpool.Pool        // Standalone browser pool (may be nil)
	sandboxOwnershipEnforce  bool                     // When true, the sandbox-ownership interceptor rejects cross-tenant access; when false it audit-logs only.
	memoryStore              memory.VectorStore       // Vector memory store (may be nil)
	memoryEmbedder           memory.EmbedderInterface // Embedding generator for memory (may be nil)
	memoryEmbeddingModel     string                   // Configured embedding model name
	memoryEmbeddingDimension int                      // Configured embedding dimension
	interopStore             *interop.Store           // Interop control plane (saved A2A remotes, etc.; may be nil)

	portExposureBaseDomain string               // Base domain for sandbox port URLs (e.g., "sandbox.run")
	portExposureTLSEnabled bool                 // Whether port exposure proxy uses TLS
	portExposureListenPort string               // Port the sandbox proxy listens on (e.g., "8443"); empty = default 80/443
	previewTokenSigner     *previewtoken.Signer // HMAC signer for signed preview URL tokens (may be nil)

	// snapshotRepo handles named sandbox snapshot storage (may be nil).
	snapshotRepo *snapshots.Repository

	// lcWebhookRepo manages customer-facing outgoing lifecycle webhook endpoints (may be nil).
	lcWebhookRepo *lcwebhooks.Repository

	// metricsRepo stores time-series sandbox resource metrics (may be nil).
	metricsRepo *sandboxmetrics.Repository

	// volumeStore/volumeBucket back persistent sandbox volumes with object
	// storage (reuses the sandbox R2 store). nil/empty = metadata-only volumes.
	volumeStore  storage.ObjectStore
	volumeBucket string

	// volumeProvisioner mints per-tenant R2 buckets + bucket-scoped credentials
	// for everstack-volume mounts (may be nil when R2/CF is unconfigured).
	volumeProvisioner *volstore.Provisioner

	// GitHub App integration (may be nil if not configured)
	githubApp   *ghpkg.App
	githubStore *ghpkg.Store

	// SSH integration (may be nil if not configured)
	sshKeyStore           *sshpkg.KeyStore
	sshProxy              *sshpkg.Proxy
	sshEndpointConfigured bool
	sshHost               string
	sshPort               int
	sshHostKeyFingerprint string
	// region slug (e.g. "eu-fra-1") for the pod that owns this server.
	// Surfaced in SSH info responses so the FE can render a region
	// label without a second lookup.
	region string

	// Persistent agent memory store (may be nil if not configured)
	agentMemStore agentmem.Store

	// Agent deployment store (may be nil if not configured)
	deploymentStore agentdeploy.Store

	// Revision store and runner back immutable, project-local agent functions.
	revisionStore  agentrevision.Store
	projectRuntime agentprojectruntime.Runner

	// Agent trigger store and executor (may be nil if not configured)
	triggerStore    agenttrigger.Store
	triggerExecutor *agenttrigger.Executor

	// Branch store for persisting full conversation traces (may be nil)
	branchStore *agentrt.BranchStore

	// Sandbox execution support (may be nil if sandbox feature is disabled)
	commandTracker *sandbox.CommandTracker
	codeContextMgr *sandbox.CodeContextManager

	// Trooper manager (may be nil if trooper feature is disabled)
	trooperMgr *trooper.Manager

	// Phase 5: Cross-agent message bus (may be nil if not configured)
	messageBus *agentrt.AgentMessageBus

	// lifecycleBus publishes per-agent lifecycle transitions (provisioning,
	// idle, recovery_pending, etc.) so the UI can render status changes
	// live instead of polling. Defaults to a no-op; replaced with a
	// RedisLifecycleBus when Redis is configured. See lifecycle_bus.go.
	lifecycleBus agentrt.LifecycleBus

	// Auto-scoring pipeline for outcome observability (may be nil)
	scoreRecorder autoscorer.ScoreRecorder

	// Sandbox lifecycle reconciler repo. When non-nil, CreateSandbox /
	// RecreateSandbox / Stop / Revive / Terminate use the async reconciler
	// path (write desired_state, return; reconciler converges). When nil,
	// they fall through to the legacy sync path against sandboxMgr.
	// See docs/design/sandbox-reconciler.md.
	lifecycleRepo *sandboxlc.Repository

	// EventBus fan-out for the SSE endpoint. Wired at startup when
	// EVS_SANDBOX_RECONCILER_ENABLED is on. Powers the per-tenant
	// /v1/sandboxes/events stream; nil disables the endpoint.
	eventBus *sandboxlc.EventBus

	// MCP gateway registry. When set, federated MCP tools are exposed to
	// agents as synthetic tool handlers via NewMcpToolHandlers.
	mcpRegistry *mcp.Registry
	// MCP registry hydrator. When set, called before building MCP tool
	// handlers for a session so the registry is repopulated from DB if the
	// gateway has restarted since the tenant's servers were last registered.
	mcpHydrator McpRegistryHydrator

	// Cached single-tenant fallback decision. resolveTenantID consults this
	// when the auth context lacks a tenant id; the fallback only fires when
	// there is exactly one organization in the database. See agents.go for
	// the full rationale — this is the cross-tenant leak guard.
	singleTenantCache atomic.Pointer[singleTenantCacheEntry]
}

// singleTenantCacheEntry is the cached result of "is this instance a
// single-tenant deployment, and if so, which org id is it". `ok=false`
// means there are zero or multiple orgs and the fallback must not fire.
type singleTenantCacheEntry struct {
	tenantID string
	ok       bool
}

// GrpcServer wraps Server for classic gRPC compatibility.
type GrpcServer struct {
	agentspb.UnimplementedAgentsServiceServer
	base *Server
}

// tenantProviderSource exposes the gateway's request-scoped provider
// bundle. Refreshing alone is insufficient: the gateway replaces bundles on
// configuration changes, so agents must resolve the refreshed pointers for
// each tenant request instead of retaining the startup router.
type tenantProviderSource interface {
	agentrt.ProviderSource
}

func CreateServer() *Server {
	return &Server{sandboxOwnershipEnforce: true}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx, sandboxOwnershipEnforce: true}
}

// CreateServerWithDeps creates a server with gateway dependencies for the runtime engine.
// db may be nil for environments without Postgres; it is used for HITL approval operations.
// redisClient is optional; when provided it enables cross-instance session routing.
func CreateServerWithDeps(ctx context.Context, registry *gw.Registry, router *gw.Router, tl *toolloop.LoopManager, sys *cqrs.System, db *sqlx.DB, redisClient ...*redis.Client) *Server {
	engine := agentrt.NewEngine(registry, router, tl)
	sessionMgr := agentrt.NewSessionManager(engine, tl, sys, db, redisClient...)
	return &Server{
		ctx:                     ctx,
		engine:                  engine,
		sessionMgr:              sessionMgr,
		db:                      db,
		sandboxOwnershipEnforce: true,
		lifecycleBus:            agentrt.NewLocalLifecycleBus(),
	}
}

// SetStorageCredentialStore injects the shared, layered-config credential
// backend used by external connections and sandbox-volume artifacts.
func (s *Server) SetStorageCredentialStore(store storagecredentials.Store) {
	s.storageCredentialStore = store
}

// SetLifecycleBus replaces the default no-op lifecycle bus with a
// (typically Redis-backed) implementation. Safe to call once during
// startup before the server handles traffic. When the Redis variant
// is wired, lifecycle transitions broadcast to per-agent channels
// so the UI receives live status updates instead of polling.
func (s *Server) SetLifecycleBus(bus agentrt.LifecycleBus) {
	if bus == nil {
		bus = agentrt.NewLocalLifecycleBus()
	}
	s.lifecycleBus = bus
}

// publishLifecycle emits a single lifecycle transition on a short
// timeout so a Redis hiccup never stalls a reconciler step. Failures
// are logged at debug — the UI degrades to polling.
func (s *Server) publishLifecycle(evt agentrt.LifecycleEvent) {
	if s.lifecycleBus == nil || evt.AgentID == "" {
		return
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := s.lifecycleBus.Publish(ctx, evt); err != nil {
		logger.WithFields("agent_id", evt.AgentID, "new_status", evt.NewStatus, "error", err.Error()).
			Debug("agents: lifecycle publish failed")
	}
}

// SetProviderRefresher wires the gateway's request-scoped provider source so
// agent turns see runtime configuration changes without sharing credentials
// across tenants. The method name is retained for startup-call compatibility.
func (s *Server) SetProviderRefresher(source tenantProviderSource) {
	if s.engine != nil {
		s.engine.SetProviderSource(source)
	}
}

// SetSandboxManager sets the sandbox manager on the server.
// Called during startup if sandbox feature is enabled.
func (s *Server) SetSandboxManager(mgr *sandbox.SandboxManager) {
	s.sandboxMgr = mgr
	if mgr != nil {
		s.commandTracker = sandbox.NewCommandTracker()
		s.codeContextMgr = sandbox.NewCodeContextManager(mgr)
	}
}

// SetBrowserPool sets the standalone browser pool on the server.
func (s *Server) SetBrowserPool(pool *browserpool.Pool) {
	s.browserPool = pool
}

// SetLifecycleRepo wires the async sandbox lifecycle repository.
// When set, CreateSandbox / lifecycle RPCs route through the
// reconciler instead of the legacy sync sandboxMgr.GetOrCreate path.
// Pass nil to disable the reconciler path (default).
func (s *Server) SetLifecycleRepo(repo *sandboxlc.Repository) {
	s.lifecycleRepo = repo
}

// LifecycleRepoEnabled reports whether the async reconciler path is
// active for this server. CreateSandbox checks this to decide between
// the new and legacy code paths.
func (s *Server) LifecycleRepoEnabled() bool {
	return s.lifecycleRepo != nil
}

// GetSandboxManager returns the sandbox manager (may be nil).
func (s *Server) GetSandboxManager() *sandbox.SandboxManager {
	return s.sandboxMgr
}

// SetMemoryBackend sets the memory vector store and embedder on the server.
// Called during startup if the memory feature is available.
func (s *Server) SetMemoryBackend(store memory.VectorStore, embedder memory.EmbedderInterface) {
	s.memoryStore = store
	s.memoryEmbedder = embedder
}

// SetInteropStore wires the interop control-plane store, enabling the
// call_external_agent tool to resolve saved remote agents by name.
func (s *Server) SetInteropStore(store *interop.Store) {
	s.interopStore = store
}

// SetMemoryConfig sets the embedding model and dimension for memory tools.
func (s *Server) SetMemoryConfig(model string, dimension int) {
	s.memoryEmbeddingModel = model
	s.memoryEmbeddingDimension = dimension
}

// SetAgentMemoryStore sets the persistent agent memory store.
func (s *Server) SetAgentMemoryStore(store agentmem.Store) {
	s.agentMemStore = store
}

// SetDeploymentStore sets the agent deployment store.
func (s *Server) SetDeploymentStore(store agentdeploy.Store) {
	s.deploymentStore = store
}

// SetRevisionStore enables immutable agent source revisions and project-local
// function execution.
func (s *Server) SetRevisionStore(store agentrevision.Store) {
	s.revisionStore = store
	if store != nil && s.projectRuntime == nil {
		s.projectRuntime = agentprojectruntime.New(agentprojectruntime.Config{CleanupOnExit: true})
	}
}

// SetProjectRuntime replaces the project runner, primarily for tests.
func (s *Server) SetProjectRuntime(runner agentprojectruntime.Runner) {
	s.projectRuntime = runner
}

// GetDeploymentStore returns the agent deployment store (may be nil).
func (s *Server) GetDeploymentStore() agentdeploy.Store {
	return s.deploymentStore
}

// SetTriggerStore sets the agent trigger store.
func (s *Server) SetTriggerStore(store agenttrigger.Store) {
	s.triggerStore = store
}

// SetTriggerExecutor sets the agent trigger executor.
func (s *Server) SetTriggerExecutor(executor *agenttrigger.Executor) {
	s.triggerExecutor = executor
}

// SetBranchStore sets the branch store for persisting full conversation traces.
func (s *Server) SetBranchStore(store *agentrt.BranchStore) {
	s.branchStore = store
}

// SetTrooperManager sets the trooper manager on the server.
// Called during startup if trooper feature is enabled.
func (s *Server) SetTrooperManager(mgr *trooper.Manager) {
	s.trooperMgr = mgr
}

// SetMcpRegistry wires the MCP gateway registry so federated MCP tools are
// exposed to agents as synthetic tool handlers during RunTurnStream.
func (s *Server) SetMcpRegistry(registry *mcp.Registry) {
	s.mcpRegistry = registry
}

// McpRegistryHydrator is the contract the MCP service satisfies so the
// agents server can ask it to restore a tenant's enabled servers into the
// in-memory registry on session start. This avoids a circular import
// (agents → mcp service v1) and lets the agents server stay agnostic of
// the persistence layer.
type McpRegistryHydrator interface {
	HydrateRegistryForTenant(ctx context.Context, tenantID string) error
}

// SetMcpRegistryHydrator wires a callback that the agents server invokes
// before building MCP tool handlers for an agent session. The hydrator
// reads enabled MCP servers for the tenant from DB and registers them in
// the live registry — necessary because the registry is in-memory only
// and would otherwise be empty after a gateway restart, leaving every
// session with zero MCP tools.
func (s *Server) SetMcpRegistryHydrator(h McpRegistryHydrator) {
	s.mcpHydrator = h
}

// SetPortExposureConfig sets the base domain, TLS flag, and listen port for constructing port URLs.
func (s *Server) SetPortExposureConfig(baseDomain string, tlsEnabled bool, listenPort string) {
	s.portExposureBaseDomain = baseDomain
	s.portExposureTLSEnabled = tlsEnabled
	s.portExposureListenPort = listenPort
}

// SetLCWebhookRepository wires the outgoing lifecycle webhook repository.
func (s *Server) SetLCWebhookRepository(repo *lcwebhooks.Repository) {
	s.lcWebhookRepo = repo
}

// EventBus returns the sandbox lifecycle event bus (may be nil).
func (s *Server) EventBus() *sandboxlc.EventBus {
	return s.eventBus
}

// SetSnapshotRepository wires the named snapshot repository.
func (s *Server) SetSnapshotRepository(repo *snapshots.Repository) {
	s.snapshotRepo = repo
}

// SetMetricsRepository wires the sandbox metrics history repository.
func (s *Server) SetMetricsRepository(repo *sandboxmetrics.Repository) {
	s.metricsRepo = repo
}

// SetScoreRecorder wires the ClickHouse score recorder for auto-scoring.
// When set, every agent turn is automatically scored by heuristic scorers.
func (s *Server) SetScoreRecorder(recorder autoscorer.ScoreRecorder) {
	s.scoreRecorder = recorder
}

// injectPortExposureDomain ensures the port-exposure base domain is included
// in AllowedHosts when the sandbox uses whitelist network mode. This lets
// sandbox-hosted apps reach their own exposed URLs (e.g. SSR callbacks).
func (s *Server) injectPortExposureDomain(cfg *sandbox.SandboxConfig) {
	if cfg.NetworkMode != "whitelist" {
		return
	}
	baseDomain := s.portExposureBaseDomain
	if baseDomain == "" {
		return
	}
	wildcard := "*." + baseDomain
	for _, h := range cfg.AllowedHosts {
		if h == wildcard || h == baseDomain {
			return
		}
	}
	cfg.AllowedHosts = append(cfg.AllowedHosts, wildcard)
}

// buildPortURL constructs the URL for an exposed sandbox port.
// For localhost/loopback, uses path-based routing (/_sandbox/{id}/port/{port}/)
// since wildcard subdomains don't resolve locally. For real domains, uses subdomain routing.
func (s *Server) buildPortURL(subdomain string, sandboxID string, port int) string {
	return previewurl.DirectURL(previewurl.Config{
		BaseDomain: s.portExposureBaseDomain,
		TLSEnabled: s.portExposureTLSEnabled,
		ListenPort: s.portExposureListenPort,
	}, subdomain, sandboxID, port)
}

// GetSessionManager returns the session manager for shutdown hooks.
func (s *Server) GetSessionManager() *agentrt.SessionManager {
	return s.sessionMgr
}

func CreateClassicServer() agentspb.AgentsServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) agentspb.AgentsServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	// Prepend the sandbox-ownership interceptor so every AgentsService RPC that
	// targets a specific sandbox is scoped to the caller's tenant/instance
	// before the handler runs. This is the ConnectRPC-native counterpart of the
	// RequireSandboxOwnership HTTP middleware (used only for the non-RPC
	// streaming/multipart routes).
	all := append([]connect.Interceptor{s.sandboxOwnershipInterceptor()}, interceptors...)
	return agentsconnect.NewAgentsServiceHandler(
		s,
		connect.WithInterceptors(all...),
		connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
			if spec.Procedure == agentsconnect.AgentsServiceCreateAgentRevisionProcedure {
				return []connect.HandlerOption{connect.WithReadMaxBytes(maxCreateAgentRevisionReadBytes)}
			}
			return nil
		}),
	)
}

// SetSandboxOwnershipEnforce toggles hard enforcement of the sandbox-ownership
// interceptor. Constructors enable enforcement by default; tests and explicit
// compatibility paths can temporarily disable it to return to audit-log mode.
func (s *Server) SetSandboxOwnershipEnforce(enforce bool) {
	s.sandboxOwnershipEnforce = enforce
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return agentspb.File_everstack_agents_v1_agents_service_proto
}

func (s *Server) AppName() string {
	return agentsconnect.AgentsServiceName
}

func (s *Server) MethodPrefix() string {
	return agentsconnect.AgentsServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway.
// Unary RPCs use the auto-generated in-process handlers. The server-streaming
// RunTurnStream RPC is overridden with a direct SSE handler because
// grpc-gateway's in-process transport does not support server-streaming.
func (s *Server) RegisterGateway(_ context.Context, mux *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	if err := agentspb.RegisterAgentsServiceHandlerServer(context.Background(), mux, &GrpcServer{base: s}); err != nil {
		return err
	}
	// Override the streaming endpoint with our SSE handler so the request
	// never reaches the in-process transport (which would return 501).
	if err := mux.HandlePath("POST", "/v1/agents/sessions/{session_id}/turns/stream", s.handleRunTurnStreamGateway); err != nil {
		return err
	}

	// Subscribe to events for an already-running session (late join / reconnect).
	if err := mux.HandlePath("GET", "/v1/agents/{agent_id}/lifecycle/subscribe", s.handleSubscribeAgentLifecycle); err != nil {
		return fmt.Errorf("failed to register agent lifecycle subscribe handler: %w", err)
	}
	if err := mux.HandlePath("GET", "/v1/agents/sessions/{session_id}/events/subscribe", s.handleSubscribeSessionEvents); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/agents/crons/{cron_id}/run", s.handleRunCronNow); err != nil {
		return err
	}
	// Stop only the active turn, keeping the session open for future turns.
	if err := mux.HandlePath("POST", "/v1/agents/sessions/{session_id}/turns/stop", s.handleStopSessionTurn); err != nil {
		return err
	}

	// Sandbox template endpoints (read-only catalog)
	if err := mux.HandlePath("GET", "/v1/sandbox/templates", s.handleListSandboxTemplatesGateway); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/sandbox/templates/{template_id}", s.handleGetSandboxTemplateGateway); err != nil {
		return err
	}

	// Memory setup endpoint (also handled via ConnectRPC wrapper, this provides REST access)
	if err := mux.HandlePath("POST", "/v1/agents/memory/setup", s.handleMemorySetupGateway); err != nil {
		return err
	}

	// Agent capabilities (feature flags for the UI, e.g. web search availability)
	if err := mux.HandlePath("GET", "/v1/agents/capabilities", s.handleAgentCapabilities); err != nil {
		return err
	}

	// Skills.sh integration — resolve from GitHub, browse & search the registry
	if err := mux.HandlePath("POST", "/v1/agents/skills/resolve", s.handleResolveSkill); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/agents/skills/registry/browse", s.handleRegistryBrowse); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/agents/skills/registry/search", s.handleRegistrySearch); err != nil {
		return err
	}

	// GitHub repo tree listing (for @ file mentions before sandbox is ready)
	if err := mux.HandlePath("GET", "/v1/integrations/github/tree", s.handleListGitHubRepoTree); err != nil {
		return err
	}

	// NOTE: User input (ask_user) endpoint is registered directly on the gorilla mux
	// router in start_api.go to bypass API key validation (admin UI doesn't send API keys).

	// NOTE: Sandbox WebSocket (shell) and SSE (logs, stats) endpoints are registered
	// directly on the gorilla mux router in start_api.go, BEFORE the /v1 prefix handler.
	// This bypasses the API key validation middleware that would reject WebSocket upgrades
	// (browsers cannot send custom headers on WebSocket connections) and unauthenticated
	// SSE streams from the admin UI.
	// NOTE: CreateSandbox is handled via ConnectRPC (see RegisterConnectServer wrapper),
	// not via grpc-gateway, so it doesn't need API key headers.

	// Trooper session endpoints
	if err := mux.HandlePath("POST", "/v1/troopers/{trooper_id}/sessions/stream", s.handleCreateTrooperSessionStream); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/troopers/sessions/{session_id}/turns/stream", s.handleSteerTrooperSessionStream); err != nil {
		return err
	}

	// Gateway & Egress REST endpoints
	if err := mux.HandlePath("GET", "/v1/gateway/status", s.handleGetGatewayStatus); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/sandbox/egress/events", s.handleListEgressEvents); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/sandbox/egress/policy", s.handleGetEgressPolicy); err != nil {
		return err
	}

	return nil
}

// GrpcServer wrapper methods for classic gRPC

func (g *GrpcServer) CreateAgent(ctx context.Context, req *agentspb.CreateAgentRequest) (*agentspb.CreateAgentResponse, error) {
	cReq := &connect.Request[agentspb.CreateAgentRequest]{Msg: req}
	resp, err := g.base.CreateAgent(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetAgent(ctx context.Context, req *agentspb.GetAgentRequest) (*agentspb.GetAgentResponse, error) {
	cReq := &connect.Request[agentspb.GetAgentRequest]{Msg: req}
	resp, err := g.base.GetAgent(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListAgents(ctx context.Context, req *agentspb.ListAgentsRequest) (*agentspb.ListAgentsResponse, error) {
	cReq := &connect.Request[agentspb.ListAgentsRequest]{Msg: req}
	resp, err := g.base.ListAgents(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateAgent(ctx context.Context, req *agentspb.UpdateAgentRequest) (*agentspb.UpdateAgentResponse, error) {
	cReq := &connect.Request[agentspb.UpdateAgentRequest]{Msg: req}
	resp, err := g.base.UpdateAgent(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteAgent(ctx context.Context, req *agentspb.DeleteAgentRequest) (*agentspb.DeleteAgentResponse, error) {
	cReq := &connect.Request[agentspb.DeleteAgentRequest]{Msg: req}
	resp, err := g.base.DeleteAgent(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ImportAgentFromOpencode(ctx context.Context, req *agentspb.ImportAgentFromOpencodeRequest) (*agentspb.ImportAgentFromOpencodeResponse, error) {
	cReq := &connect.Request[agentspb.ImportAgentFromOpencodeRequest]{Msg: req}
	resp, err := g.base.ImportAgentFromOpencode(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ExportAgentToOpencode(ctx context.Context, req *agentspb.ExportAgentToOpencodeRequest) (*agentspb.ExportAgentToOpencodeResponse, error) {
	cReq := &connect.Request[agentspb.ExportAgentToOpencodeRequest]{Msg: req}
	resp, err := g.base.ExportAgentToOpencode(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateSession(ctx context.Context, req *agentspb.CreateSessionRequest) (*agentspb.CreateSessionResponse, error) {
	cReq := &connect.Request[agentspb.CreateSessionRequest]{Msg: req}
	resp, err := g.base.CreateSession(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSession(ctx context.Context, req *agentspb.GetSessionRequest) (*agentspb.GetSessionResponse, error) {
	cReq := &connect.Request[agentspb.GetSessionRequest]{Msg: req}
	resp, err := g.base.GetSession(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSessions(ctx context.Context, req *agentspb.ListSessionsRequest) (*agentspb.ListSessionsResponse, error) {
	cReq := &connect.Request[agentspb.ListSessionsRequest]{Msg: req}
	resp, err := g.base.ListSessions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RunTurn(ctx context.Context, req *agentspb.RunTurnRequest) (*agentspb.RunTurnResponse, error) {
	cReq := &connect.Request[agentspb.RunTurnRequest]{Msg: req}
	resp, err := g.base.RunTurn(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CancelSession(ctx context.Context, req *agentspb.CancelSessionRequest) (*agentspb.CancelSessionResponse, error) {
	cReq := &connect.Request[agentspb.CancelSessionRequest]{Msg: req}
	resp, err := g.base.CancelSession(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CompleteSession(ctx context.Context, req *agentspb.CompleteSessionRequest) (*agentspb.CompleteSessionResponse, error) {
	cReq := &connect.Request[agentspb.CompleteSessionRequest]{Msg: req}
	resp, err := g.base.CompleteSession(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// RunTurnStream wraps the Connect streaming handler for classic gRPC.
func (g *GrpcServer) RunTurnStream(req *agentspb.RunTurnStreamRequest, stream grpc.ServerStreamingServer[agentspb.AgentEvent]) error {
	cReq := &connect.Request[agentspb.RunTurnStreamRequest]{Msg: req}
	adapter := &grpcAgentEventStreamAdapter{stream: stream}
	return g.base.runTurnStreamInternal(stream.Context(), cReq, adapter)
}

// SteerSession wraps the Connect handler for classic gRPC.
func (g *GrpcServer) SteerSession(ctx context.Context, req *agentspb.SteerSessionRequest) (*agentspb.SteerSessionResponse, error) {
	cReq := &connect.Request[agentspb.SteerSessionRequest]{Msg: req}
	resp, err := g.base.SteerSession(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SubmitReview wraps the Connect handler for classic gRPC.
func (g *GrpcServer) SubmitReview(ctx context.Context, req *agentspb.SubmitReviewRequest) (*agentspb.SubmitReviewResponse, error) {
	cReq := &connect.Request[agentspb.SubmitReviewRequest]{Msg: req}
	resp, err := g.base.SubmitReview(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetReview wraps the Connect handler for classic gRPC.
func (g *GrpcServer) GetReview(ctx context.Context, req *agentspb.GetReviewRequest) (*agentspb.GetReviewResponse, error) {
	cReq := &connect.Request[agentspb.GetReviewRequest]{Msg: req}
	resp, err := g.base.GetReview(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ListReviews wraps the Connect handler for classic gRPC.
func (g *GrpcServer) ListReviews(ctx context.Context, req *agentspb.ListReviewsRequest) (*agentspb.ListReviewsResponse, error) {
	cReq := &connect.Request[agentspb.ListReviewsRequest]{Msg: req}
	resp, err := g.base.ListReviews(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Sandbox GrpcServer wrappers

func (g *GrpcServer) GetSandboxOverview(ctx context.Context, req *agentspb.GetSandboxOverviewRequest) (*agentspb.GetSandboxOverviewResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxOverviewRequest]{Msg: req}
	resp, err := g.base.GetSandboxOverview(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSandboxInstances(ctx context.Context, req *agentspb.ListSandboxInstancesRequest) (*agentspb.ListSandboxInstancesResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxInstancesRequest]{Msg: req}
	resp, err := g.base.ListSandboxInstances(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSandboxInstance(ctx context.Context, req *agentspb.GetSandboxInstanceRequest) (*agentspb.GetSandboxInstanceResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxInstanceRequest]{Msg: req}
	resp, err := g.base.GetSandboxInstance(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DestroySandbox(ctx context.Context, req *agentspb.DestroySandboxRequest) (*agentspb.DestroySandboxResponse, error) {
	cReq := &connect.Request[agentspb.DestroySandboxRequest]{Msg: req}
	resp, err := g.base.DestroySandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSandboxExecutions(ctx context.Context, req *agentspb.ListSandboxExecutionsRequest) (*agentspb.ListSandboxExecutionsResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxExecutionsRequest]{Msg: req}
	resp, err := g.base.ListSandboxExecutions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSandboxStats(ctx context.Context, req *agentspb.GetSandboxStatsRequest) (*agentspb.GetSandboxStatsResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxStatsRequest]{Msg: req}
	resp, err := g.base.GetSandboxStats(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Spawn Tree GrpcServer wrappers

func (g *GrpcServer) GetSpawnTree(ctx context.Context, req *agentspb.GetSpawnTreeRequest) (*agentspb.GetSpawnTreeResponse, error) {
	cReq := &connect.Request[agentspb.GetSpawnTreeRequest]{Msg: req}
	resp, err := g.base.GetSpawnTree(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSpawnNodes(ctx context.Context, req *agentspb.ListSpawnNodesRequest) (*agentspb.ListSpawnNodesResponse, error) {
	cReq := &connect.Request[agentspb.ListSpawnNodesRequest]{Msg: req}
	resp, err := g.base.ListSpawnNodes(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Sandbox + Memory GrpcServer wrappers (delegates to ConnectRPC methods on Server)

func (g *GrpcServer) CreateSandbox(ctx context.Context, req *agentspb.CreateSandboxRequest) (*agentspb.CreateSandboxResponse, error) {
	cReq := &connect.Request[agentspb.CreateSandboxRequest]{Msg: req}
	resp, err := g.base.CreateSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RecreateSandbox(ctx context.Context, req *agentspb.RecreateSandboxRequest) (*agentspb.CreateSandboxResponse, error) {
	cReq := &connect.Request[agentspb.RecreateSandboxRequest]{Msg: req}
	resp, err := g.base.RecreateSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSandboxTemplates(ctx context.Context, req *agentspb.ListSandboxTemplatesRequest) (*agentspb.ListSandboxTemplatesResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxTemplatesRequest]{Msg: req}
	resp, err := g.base.ListSandboxTemplates(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSandboxTemplate(ctx context.Context, req *agentspb.GetSandboxTemplateRequest) (*agentspb.GetSandboxTemplateResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxTemplateRequest]{Msg: req}
	resp, err := g.base.GetSandboxTemplate(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SetupMemory(ctx context.Context, req *agentspb.SetupMemoryRequest) (*agentspb.SetupMemoryResponse, error) {
	cReq := &connect.Request[agentspb.SetupMemoryRequest]{Msg: req}
	resp, err := g.base.SetupMemory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Sandbox Events GrpcServer wrappers

func (g *GrpcServer) ListSandboxEvents(ctx context.Context, req *agentspb.ListSandboxEventsRequest) (*agentspb.ListSandboxEventsResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxEventsRequest]{Msg: req}
	resp, err := g.base.ListSandboxEvents(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Port Exposure GrpcServer wrappers

func (g *GrpcServer) ExposePort(ctx context.Context, req *agentspb.ExposePortRequest) (*agentspb.ExposePortResponse, error) {
	cReq := &connect.Request[agentspb.ExposePortRequest]{Msg: req}
	resp, err := g.base.ExposePort(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UnexposePort(ctx context.Context, req *agentspb.UnexposePortRequest) (*agentspb.UnexposePortResponse, error) {
	cReq := &connect.Request[agentspb.UnexposePortRequest]{Msg: req}
	resp, err := g.base.UnexposePort(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListExposedPorts(ctx context.Context, req *agentspb.ListExposedPortsRequest) (*agentspb.ListExposedPortsResponse, error) {
	cReq := &connect.Request[agentspb.ListExposedPortsRequest]{Msg: req}
	resp, err := g.base.ListExposedPorts(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DetectListeningPorts(ctx context.Context, req *agentspb.DetectListeningPortsRequest) (*agentspb.DetectListeningPortsResponse, error) {
	cReq := &connect.Request[agentspb.DetectListeningPortsRequest]{Msg: req}
	resp, err := g.base.DetectListeningPorts(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Cron GrpcServer wrappers

func (g *GrpcServer) CreateCron(ctx context.Context, req *agentspb.CreateCronRequest) (*agentspb.CreateCronResponse, error) {
	cReq := &connect.Request[agentspb.CreateCronRequest]{Msg: req}
	resp, err := g.base.CreateCron(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateCron(ctx context.Context, req *agentspb.UpdateCronRequest) (*agentspb.UpdateCronResponse, error) {
	cReq := &connect.Request[agentspb.UpdateCronRequest]{Msg: req}
	resp, err := g.base.UpdateCron(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteCron(ctx context.Context, req *agentspb.DeleteCronRequest) (*agentspb.DeleteCronResponse, error) {
	cReq := &connect.Request[agentspb.DeleteCronRequest]{Msg: req}
	resp, err := g.base.DeleteCron(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListCrons(ctx context.Context, req *agentspb.ListCronsRequest) (*agentspb.ListCronsResponse, error) {
	cReq := &connect.Request[agentspb.ListCronsRequest]{Msg: req}
	resp, err := g.base.ListCrons(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Webhook GrpcServer wrappers

func (g *GrpcServer) CreateWebhook(ctx context.Context, req *agentspb.CreateWebhookRequest) (*agentspb.CreateWebhookResponse, error) {
	cReq := &connect.Request[agentspb.CreateWebhookRequest]{Msg: req}
	resp, err := g.base.CreateWebhook(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteWebhook(ctx context.Context, req *agentspb.DeleteWebhookRequest) (*agentspb.DeleteWebhookResponse, error) {
	cReq := &connect.Request[agentspb.DeleteWebhookRequest]{Msg: req}
	resp, err := g.base.DeleteWebhook(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListWebhooks(ctx context.Context, req *agentspb.ListWebhooksRequest) (*agentspb.ListWebhooksResponse, error) {
	cReq := &connect.Request[agentspb.ListWebhooksRequest]{Msg: req}
	resp, err := g.base.ListWebhooks(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Triggers GrpcServer wrapper

func (g *GrpcServer) ListTriggers(ctx context.Context, req *agentspb.ListTriggersRequest) (*agentspb.ListTriggersResponse, error) {
	cReq := &connect.Request[agentspb.ListTriggersRequest]{Msg: req}
	resp, err := g.base.ListTriggers(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// recordUsageMetrics records request metrics to the license monitor if available.
// Mirrors the gateway server's recordUsageMetrics for consistent token tracking.
// Returns a SpendLimitExceededError if the spend limit is exceeded with BLOCK action.
func (s *Server) recordUsageMetrics(inputTokens, outputTokens int64, estimatedCost, cacheSavings float64, cacheHit bool) error {
	if s.ctx == nil {
		return nil
	}

	totalTokens := inputTokens + outputTokens

	// Record tokens to trial manager if in trial mode
	le := enterprise.LicenseEnforcerFromContext(s.ctx)
	if le.IsInTrialMode() {
		if err := le.RecordTrialTokens(context.Background(), totalTokens); err != nil {
			logger.Warnf("agents: trial: failed to record tokens: %v", err)
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
			logger.Warn("agents: spend limit exceeded")
			return err
		}
	}
	return nil
}

// getProviderForModel returns the provider name for a given model using the
// request's tenant-scoped router.
func (s *Server) getProviderForModel(ctx context.Context, model string) string {
	if s.engine == nil {
		return "unknown"
	}
	_, router, err := s.engine.ProvidersForContext(ctx)
	if err != nil {
		return "unknown"
	}

	_, route, err := router.ResolveWithContext(ctx, model)
	if err != nil {
		return "unknown"
	}

	return route.ProviderName
}

// agentEventSender abstracts the Send method for both Connect and classic gRPC streams.
// The optional rawData parameter carries runtime Event.Data fields that cannot be
// expressed in the proto message (e.g., template lists, port URLs). SSE senders
// merge this into the JSON payload; gRPC senders ignore it.
type agentEventSender interface {
	Send(msg *agentspb.AgentEvent, rawData map[string]interface{}) error
}

// grpcAgentEventStreamAdapter adapts a classic gRPC server stream to the agentEventSender interface.
type grpcAgentEventStreamAdapter struct {
	stream grpc.ServerStreamingServer[agentspb.AgentEvent]
}

func (a *grpcAgentEventStreamAdapter) Send(msg *agentspb.AgentEvent, _ map[string]interface{}) error {
	return a.stream.Send(msg)
}

// connectAgentEventStreamAdapter adapts a Connect server stream to the agentEventSender interface.
type connectAgentEventStreamAdapter struct {
	stream *connect.ServerStream[agentspb.AgentEvent]
}

func (a *connectAgentEventStreamAdapter) Send(msg *agentspb.AgentEvent, _ map[string]interface{}) error {
	return a.stream.Send(msg)
}

// GitHub GrpcServer wrappers

func (g *GrpcServer) ListGitHubInstallations(ctx context.Context, req *agentspb.ListGitHubInstallationsRequest) (*agentspb.ListGitHubInstallationsResponse, error) {
	cReq := &connect.Request[agentspb.ListGitHubInstallationsRequest]{Msg: req}
	resp, err := g.base.ListGitHubInstallations(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RemoveGitHubInstallation(ctx context.Context, req *agentspb.RemoveGitHubInstallationRequest) (*agentspb.RemoveGitHubInstallationResponse, error) {
	cReq := &connect.Request[agentspb.RemoveGitHubInstallationRequest]{Msg: req}
	resp, err := g.base.RemoveGitHubInstallation(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) LinkGitHubInstallation(ctx context.Context, req *agentspb.LinkGitHubInstallationRequest) (*agentspb.LinkGitHubInstallationResponse, error) {
	cReq := &connect.Request[agentspb.LinkGitHubInstallationRequest]{Msg: req}
	resp, err := g.base.LinkGitHubInstallation(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListGitHubRepositories(ctx context.Context, req *agentspb.ListGitHubRepositoriesRequest) (*agentspb.ListGitHubRepositoriesResponse, error) {
	cReq := &connect.Request[agentspb.ListGitHubRepositoriesRequest]{Msg: req}
	resp, err := g.base.ListGitHubRepositories(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListGitHubBranches(ctx context.Context, req *agentspb.ListGitHubBranchesRequest) (*agentspb.ListGitHubBranchesResponse, error) {
	cReq := &connect.Request[agentspb.ListGitHubBranchesRequest]{Msg: req}
	resp, err := g.base.ListGitHubBranches(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Sandbox Lifecycle GrpcServer wrappers

func (g *GrpcServer) StopSandbox(ctx context.Context, req *agentspb.StopSandboxRequest) (*agentspb.StopSandboxResponse, error) {
	cReq := &connect.Request[agentspb.StopSandboxRequest]{Msg: req}
	resp, err := g.base.StopSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ReviveSandbox(ctx context.Context, req *agentspb.ReviveSandboxRequest) (*agentspb.ReviveSandboxResponse, error) {
	cReq := &connect.Request[agentspb.ReviveSandboxRequest]{Msg: req}
	resp, err := g.base.ReviveSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) TerminateSandbox(ctx context.Context, req *agentspb.TerminateSandboxRequest) (*agentspb.TerminateSandboxResponse, error) {
	cReq := &connect.Request[agentspb.TerminateSandboxRequest]{Msg: req}
	resp, err := g.base.TerminateSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SSH GrpcServer wrappers

func (g *GrpcServer) AddSSHKey(ctx context.Context, req *agentspb.AddSSHKeyRequest) (*agentspb.AddSSHKeyResponse, error) {
	cReq := &connect.Request[agentspb.AddSSHKeyRequest]{Msg: req}
	resp, err := g.base.AddSSHKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSSHKeys(ctx context.Context, req *agentspb.ListSSHKeysRequest) (*agentspb.ListSSHKeysResponse, error) {
	cReq := &connect.Request[agentspb.ListSSHKeysRequest]{Msg: req}
	resp, err := g.base.ListSSHKeys(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteSSHKey(ctx context.Context, req *agentspb.DeleteSSHKeyRequest) (*agentspb.DeleteSSHKeyResponse, error) {
	cReq := &connect.Request[agentspb.DeleteSSHKeyRequest]{Msg: req}
	resp, err := g.base.DeleteSSHKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GrantSandboxSSHAccess(ctx context.Context, req *agentspb.GrantSandboxSSHAccessRequest) (*agentspb.GrantSandboxSSHAccessResponse, error) {
	cReq := &connect.Request[agentspb.GrantSandboxSSHAccessRequest]{Msg: req}
	resp, err := g.base.GrantSandboxSSHAccess(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RevokeSandboxSSHAccess(ctx context.Context, req *agentspb.RevokeSandboxSSHAccessRequest) (*agentspb.RevokeSandboxSSHAccessResponse, error) {
	cReq := &connect.Request[agentspb.RevokeSandboxSSHAccessRequest]{Msg: req}
	resp, err := g.base.RevokeSandboxSSHAccess(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSandboxSSHInfo(ctx context.Context, req *agentspb.GetSandboxSSHInfoRequest) (*agentspb.GetSandboxSSHInfoResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxSSHInfoRequest]{Msg: req}
	resp, err := g.base.GetSandboxSSHInfo(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateSandboxSSHToken(ctx context.Context, req *agentspb.CreateSandboxSSHTokenRequest) (*agentspb.CreateSandboxSSHTokenResponse, error) {
	cReq := &connect.Request[agentspb.CreateSandboxSSHTokenRequest]{Msg: req}
	resp, err := g.base.CreateSandboxSSHToken(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSandboxSSHTokens(ctx context.Context, req *agentspb.ListSandboxSSHTokensRequest) (*agentspb.ListSandboxSSHTokensResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxSSHTokensRequest]{Msg: req}
	resp, err := g.base.ListSandboxSSHTokens(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RevokeSandboxSSHToken(ctx context.Context, req *agentspb.RevokeSandboxSSHTokenRequest) (*agentspb.RevokeSandboxSSHTokenResponse, error) {
	cReq := &connect.Request[agentspb.RevokeSandboxSSHTokenRequest]{Msg: req}
	resp, err := g.base.RevokeSandboxSSHToken(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetSandboxPreviewUrl(ctx context.Context, req *agentspb.GetSandboxPreviewUrlRequest) (*agentspb.GetSandboxPreviewUrlResponse, error) {
	cReq := &connect.Request[agentspb.GetSandboxPreviewUrlRequest]{Msg: req}
	resp, err := g.base.GetSandboxPreviewUrl(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ResizeSandbox(ctx context.Context, req *agentspb.ResizeSandboxRequest) (*agentspb.ResizeSandboxResponse, error) {
	cReq := &connect.Request[agentspb.ResizeSandboxRequest]{Msg: req}
	resp, err := g.base.ResizeSandbox(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListSandboxVolumes(ctx context.Context, req *agentspb.ListSandboxVolumesRequest) (*agentspb.ListSandboxVolumesResponse, error) {
	cReq := &connect.Request[agentspb.ListSandboxVolumesRequest]{Msg: req}
	resp, err := g.base.ListSandboxVolumes(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateSandboxVolume(ctx context.Context, req *agentspb.CreateSandboxVolumeRequest) (*agentspb.CreateSandboxVolumeResponse, error) {
	cReq := &connect.Request[agentspb.CreateSandboxVolumeRequest]{Msg: req}
	resp, err := g.base.CreateSandboxVolume(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteSandboxVolume(ctx context.Context, req *agentspb.DeleteSandboxVolumeRequest) (*agentspb.DeleteSandboxVolumeResponse, error) {
	cReq := &connect.Request[agentspb.DeleteSandboxVolumeRequest]{Msg: req}
	resp, err := g.base.DeleteSandboxVolume(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ContentSearch(ctx context.Context, req *agentspb.ContentSearchRequest) (*agentspb.ContentSearchResponse, error) {
	cReq := &connect.Request[agentspb.ContentSearchRequest]{Msg: req}
	resp, err := g.base.ContentSearch(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GlobalReplace(ctx context.Context, req *agentspb.GlobalReplaceRequest) (*agentspb.GlobalReplaceResponse, error) {
	cReq := &connect.Request[agentspb.GlobalReplaceRequest]{Msg: req}
	resp, err := g.base.GlobalReplace(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SandboxLSP(ctx context.Context, req *agentspb.SandboxLSPRequest) (*agentspb.SandboxLSPResponse, error) {
	cReq := &connect.Request[agentspb.SandboxLSPRequest]{Msg: req}
	resp, err := g.base.SandboxLSP(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SandboxLSPInfo(ctx context.Context, req *agentspb.SandboxLSPInfoRequest) (*agentspb.SandboxLSPInfoResponse, error) {
	cReq := &connect.Request[agentspb.SandboxLSPInfoRequest]{Msg: req}
	resp, err := g.base.SandboxLSPInfo(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SandboxMetricsHistory(ctx context.Context, req *agentspb.SandboxMetricsHistoryRequest) (*agentspb.SandboxMetricsHistoryResponse, error) {
	cReq := &connect.Request[agentspb.SandboxMetricsHistoryRequest]{Msg: req}
	resp, err := g.base.SandboxMetricsHistory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SandboxMetricsBatch(ctx context.Context, req *agentspb.SandboxMetricsBatchRequest) (*agentspb.SandboxMetricsBatchResponse, error) {
	cReq := &connect.Request[agentspb.SandboxMetricsBatchRequest]{Msg: req}
	resp, err := g.base.SandboxMetricsBatch(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ComputerUse(ctx context.Context, req *agentspb.ComputerUseRequest) (*agentspb.ComputerUseResponse, error) {
	cReq := &connect.Request[agentspb.ComputerUseRequest]{Msg: req}
	resp, err := g.base.ComputerUse(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ComputerUseInfo(ctx context.Context, req *agentspb.ComputerUseInfoRequest) (*agentspb.ComputerUseInfoResponse, error) {
	cReq := &connect.Request[agentspb.ComputerUseInfoRequest]{Msg: req}
	resp, err := g.base.ComputerUseInfo(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Agent Memory GrpcServer wrappers

func (g *GrpcServer) ListAgentMemories(ctx context.Context, req *agentspb.ListAgentMemoriesRequest) (*agentspb.ListAgentMemoriesResponse, error) {
	cReq := &connect.Request[agentspb.ListAgentMemoriesRequest]{Msg: req}
	resp, err := g.base.ListAgentMemories(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateAgentMemory(ctx context.Context, req *agentspb.CreateAgentMemoryRequest) (*agentspb.CreateAgentMemoryResponse, error) {
	cReq := &connect.Request[agentspb.CreateAgentMemoryRequest]{Msg: req}
	resp, err := g.base.CreateAgentMemory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateAgentMemory(ctx context.Context, req *agentspb.UpdateAgentMemoryRequest) (*agentspb.UpdateAgentMemoryResponse, error) {
	cReq := &connect.Request[agentspb.UpdateAgentMemoryRequest]{Msg: req}
	resp, err := g.base.UpdateAgentMemory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeactivateAgentMemory(ctx context.Context, req *agentspb.DeactivateAgentMemoryRequest) (*agentspb.DeactivateAgentMemoryResponse, error) {
	cReq := &connect.Request[agentspb.DeactivateAgentMemoryRequest]{Msg: req}
	resp, err := g.base.DeactivateAgentMemory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteAgentMemory(ctx context.Context, req *agentspb.DeleteAgentMemoryRequest) (*agentspb.DeleteAgentMemoryResponse, error) {
	cReq := &connect.Request[agentspb.DeleteAgentMemoryRequest]{Msg: req}
	resp, err := g.base.DeleteAgentMemory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Deployment GrpcServer wrappers

func (g *GrpcServer) DeployAgent(ctx context.Context, req *agentspb.DeployAgentRequest) (*agentspb.DeployAgentResponse, error) {
	cReq := &connect.Request[agentspb.DeployAgentRequest]{Msg: req}
	resp, err := g.base.DeployAgent(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListDeployments(ctx context.Context, req *agentspb.ListDeploymentsRequest) (*agentspb.ListDeploymentsResponse, error) {
	cReq := &connect.Request[agentspb.ListDeploymentsRequest]{Msg: req}
	resp, err := g.base.ListDeployments(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetDeployment(ctx context.Context, req *agentspb.GetDeploymentRequest) (*agentspb.GetDeploymentResponse, error) {
	cReq := &connect.Request[agentspb.GetDeploymentRequest]{Msg: req}
	resp, err := g.base.GetDeployment(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateDeployment(ctx context.Context, req *agentspb.UpdateDeploymentRequest) (*agentspb.UpdateDeploymentResponse, error) {
	cReq := &connect.Request[agentspb.UpdateDeploymentRequest]{Msg: req}
	resp, err := g.base.UpdateDeployment(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateDeploymentKey(ctx context.Context, req *agentspb.CreateDeploymentKeyRequest) (*agentspb.CreateDeploymentKeyResponse, error) {
	cReq := &connect.Request[agentspb.CreateDeploymentKeyRequest]{Msg: req}
	resp, err := g.base.CreateDeploymentKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListDeploymentKeys(ctx context.Context, req *agentspb.ListDeploymentKeysRequest) (*agentspb.ListDeploymentKeysResponse, error) {
	cReq := &connect.Request[agentspb.ListDeploymentKeysRequest]{Msg: req}
	resp, err := g.base.ListDeploymentKeys(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RevokeDeploymentKey(ctx context.Context, req *agentspb.RevokeDeploymentKeyRequest) (*agentspb.RevokeDeploymentKeyResponse, error) {
	cReq := &connect.Request[agentspb.RevokeDeploymentKeyRequest]{Msg: req}
	resp, err := g.base.RevokeDeploymentKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListDeploymentInvocations(ctx context.Context, req *agentspb.ListDeploymentInvocationsRequest) (*agentspb.ListDeploymentInvocationsResponse, error) {
	cReq := &connect.Request[agentspb.ListDeploymentInvocationsRequest]{Msg: req}
	resp, err := g.base.ListDeploymentInvocations(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Trigger GrpcServer wrappers

func (g *GrpcServer) CreateAgentTrigger(ctx context.Context, req *agentspb.CreateAgentTriggerRequest) (*agentspb.CreateAgentTriggerResponse, error) {
	cReq := &connect.Request[agentspb.CreateAgentTriggerRequest]{Msg: req}
	resp, err := g.base.CreateAgentTrigger(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListAgentTriggers(ctx context.Context, req *agentspb.ListAgentTriggersRequest) (*agentspb.ListAgentTriggersResponse, error) {
	cReq := &connect.Request[agentspb.ListAgentTriggersRequest]{Msg: req}
	resp, err := g.base.ListAgentTriggers(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetAgentTrigger(ctx context.Context, req *agentspb.GetAgentTriggerRequest) (*agentspb.GetAgentTriggerResponse, error) {
	cReq := &connect.Request[agentspb.GetAgentTriggerRequest]{Msg: req}
	resp, err := g.base.GetAgentTrigger(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateAgentTrigger(ctx context.Context, req *agentspb.UpdateAgentTriggerRequest) (*agentspb.UpdateAgentTriggerResponse, error) {
	cReq := &connect.Request[agentspb.UpdateAgentTriggerRequest]{Msg: req}
	resp, err := g.base.UpdateAgentTrigger(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteAgentTrigger(ctx context.Context, req *agentspb.DeleteAgentTriggerRequest) (*agentspb.DeleteAgentTriggerResponse, error) {
	cReq := &connect.Request[agentspb.DeleteAgentTriggerRequest]{Msg: req}
	resp, err := g.base.DeleteAgentTrigger(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) TestAgentTrigger(ctx context.Context, req *agentspb.TestAgentTriggerRequest) (*agentspb.TestAgentTriggerResponse, error) {
	cReq := &connect.Request[agentspb.TestAgentTriggerRequest]{Msg: req}
	resp, err := g.base.TestAgentTrigger(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListAgentTriggerExecutions(ctx context.Context, req *agentspb.ListAgentTriggerExecutionsRequest) (*agentspb.ListAgentTriggerExecutionsResponse, error) {
	cReq := &connect.Request[agentspb.ListAgentTriggerExecutionsRequest]{Msg: req}
	resp, err := g.base.ListAgentTriggerExecutions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ─── Trooper GrpcServer wrappers ──────────────────────────────────────────

func (g *GrpcServer) CreateTrooper(ctx context.Context, req *agentspb.CreateTrooperRequest) (*agentspb.CreateTrooperResponse, error) {
	resp, err := g.base.CreateTrooper(ctx, &connect.Request[agentspb.CreateTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetTrooper(ctx context.Context, req *agentspb.GetTrooperRequest) (*agentspb.GetTrooperResponse, error) {
	resp, err := g.base.GetTrooper(ctx, &connect.Request[agentspb.GetTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListTroopers(ctx context.Context, req *agentspb.ListTroopersRequest) (*agentspb.ListTroopersResponse, error) {
	resp, err := g.base.ListTroopers(ctx, &connect.Request[agentspb.ListTroopersRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateTrooper(ctx context.Context, req *agentspb.UpdateTrooperRequest) (*agentspb.UpdateTrooperResponse, error) {
	resp, err := g.base.UpdateTrooper(ctx, &connect.Request[agentspb.UpdateTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteTrooper(ctx context.Context, req *agentspb.DeleteTrooperRequest) (*agentspb.DeleteTrooperResponse, error) {
	resp, err := g.base.DeleteTrooper(ctx, &connect.Request[agentspb.DeleteTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ProvisionTrooper(ctx context.Context, req *agentspb.ProvisionTrooperRequest) (*agentspb.ProvisionTrooperResponse, error) {
	resp, err := g.base.ProvisionTrooper(ctx, &connect.Request[agentspb.ProvisionTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SleepTrooper(ctx context.Context, req *agentspb.SleepTrooperRequest) (*agentspb.SleepTrooperResponse, error) {
	resp, err := g.base.SleepTrooper(ctx, &connect.Request[agentspb.SleepTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) WakeTrooper(ctx context.Context, req *agentspb.WakeTrooperRequest) (*agentspb.WakeTrooperResponse, error) {
	resp, err := g.base.WakeTrooper(ctx, &connect.Request[agentspb.WakeTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateTrooperLink(ctx context.Context, req *agentspb.CreateTrooperLinkRequest) (*agentspb.CreateTrooperLinkResponse, error) {
	resp, err := g.base.CreateTrooperLink(ctx, &connect.Request[agentspb.CreateTrooperLinkRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListTrooperLinks(ctx context.Context, req *agentspb.ListTrooperLinksRequest) (*agentspb.ListTrooperLinksResponse, error) {
	resp, err := g.base.ListTrooperLinks(ctx, &connect.Request[agentspb.ListTrooperLinksRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteTrooperLink(ctx context.Context, req *agentspb.DeleteTrooperLinkRequest) (*agentspb.DeleteTrooperLinkResponse, error) {
	resp, err := g.base.DeleteTrooperLink(ctx, &connect.Request[agentspb.DeleteTrooperLinkRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) BindChannelToTrooper(ctx context.Context, req *agentspb.BindChannelToTrooperRequest) (*agentspb.BindChannelToTrooperResponse, error) {
	resp, err := g.base.BindChannelToTrooper(ctx, &connect.Request[agentspb.BindChannelToTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UnbindChannelFromTrooper(ctx context.Context, req *agentspb.UnbindChannelFromTrooperRequest) (*agentspb.UnbindChannelFromTrooperResponse, error) {
	resp, err := g.base.UnbindChannelFromTrooper(ctx, &connect.Request[agentspb.UnbindChannelFromTrooperRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListTrooperChannelBindings(ctx context.Context, req *agentspb.ListTrooperChannelBindingsRequest) (*agentspb.ListTrooperChannelBindingsResponse, error) {
	resp, err := g.base.ListTrooperChannelBindings(ctx, &connect.Request[agentspb.ListTrooperChannelBindingsRequest]{Msg: req})
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// resolveStorageContext attempts to build a StorageToolContext for the given
// tenant. Returns nil if the tenant has no storage configuration or if the
// database is unavailable.
func (s *Server) resolveStorageContext(ctx context.Context, tenantID, sessionID string) (*agenttools.StorageToolContext, error) {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionConnectionRead, tenantID); err != nil {
		return nil, err
	}
	if s.db == nil {
		return nil, nil
	}

	var cfg struct {
		ID            string `db:"id"`
		Bucket        string `db:"bucket"`
		Endpoint      string `db:"endpoint"`
		Region        string `db:"region"`
		Provider      string `db:"provider"`
		PathPrefix    string `db:"path_prefix"`
		CredentialRef string `db:"credential_ref"`
	}
	err := s.db.GetContext(ctx, &cfg,
		`SELECT id, bucket, endpoint, region, provider, path_prefix, COALESCE(credential_ref, '') AS credential_ref
		 FROM object_storage_configs
		 WHERE tenant_id = $1 AND enabled = true
		 ORDER BY is_default DESC
		 LIMIT 1`, tenantID)
	if err != nil {
		return nil, nil
	}
	if s.storageCredentialStore == nil {
		logger.WithFields("tenant_id", tenantID).Warn("agents: storage credential backend is unavailable")
		return nil, nil
	}
	credentials, reference, err := storagecredentials.ResolveConfigCredentials(
		ctx, s.storageCredentialStore, tenantID, cfg.ID, cfg.CredentialRef,
	)
	if err != nil {
		logger.WithFields("tenant_id", tenantID).Warn("agents: failed to resolve storage credentials")
		return nil, nil
	}
	cfg.CredentialRef = reference

	region := cfg.Region
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "r2" {
		region = "auto"
	}
	forcePathStyle := provider == "minio" || provider == "r2"
	wireChecksum := s3store.WireChecksumSHA256
	if provider == "r2" {
		wireChecksum = s3store.WireChecksumContentMD5
	}

	store, err := s3store.New(ctx, s3store.Config{
		Endpoint:             cfg.Endpoint,
		Region:               region,
		Bucket:               cfg.Bucket,
		AccessKeyID:          credentials.AccessKeyID,
		SecretAccessKey:      credentials.SecretAccessKey,
		PathPrefix:           cfg.PathPrefix,
		ForcePathStyle:       forcePathStyle,
		DisableNativeCopy:    provider == "minio" || provider == "r2",
		WireChecksum:         wireChecksum,
		EnforceManagedEgress: enterprise.ManagedGateway(),
	})
	if err != nil {
		logger.WithFields("error", err.Error(), "tenant_id", tenantID).Warn("agents: failed to create storage client for artifacts")
		return nil, nil
	}

	return &agenttools.StorageToolContext{
		Store:     store,
		Uploader:  storage.NewDirectUploader(store, storage.NewPostgresUploadLifecycle(s.db), cfg.Bucket),
		DB:        s.db,
		TenantID:  tenantID,
		SessionID: sessionID,
		Bucket:    cfg.Bucket,
		ConfigID:  cfg.ID,
	}, nil
}

// resolveRepoContext builds a RepoToolContext from the agent's sandbox git config.
// Returns nil if no GitHub repo is configured or if the GitHub App is unavailable.
func (s *Server) resolveRepoContext(sandboxConfig sandbox.SandboxConfig) *agenttools.RepoToolContext {
	if s.githubApp == nil {
		return nil
	}

	repoURL := strings.TrimSpace(sandboxConfig.GitRepoURL)
	if repoURL == "" || sandboxConfig.GitInstallationID <= 0 {
		return nil
	}

	owner, repo := parseOwnerRepo(repoURL)
	if owner == "" || repo == "" {
		return nil
	}

	return &agenttools.RepoToolContext{
		App:            s.githubApp,
		InstallationID: sandboxConfig.GitInstallationID,
		Owner:          owner,
		Repo:           repo,
		Branch:         strings.TrimSpace(sandboxConfig.GitBranch),
	}
}

// parseOwnerRepo extracts owner and repo from various GitHub URL formats.
func parseOwnerRepo(repoURL string) (string, string) {
	s := strings.TrimSpace(repoURL)
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	parts := strings.SplitN(s, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}
