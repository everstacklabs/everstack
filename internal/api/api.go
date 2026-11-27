package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/cmd/build"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/api/common"
	grpcserver "github.com/everstacklabs/everstack/internal/api/grpc/server"
	"github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
	http_util "github.com/everstacklabs/everstack/internal/api/http"
	"github.com/everstacklabs/everstack/internal/api/http/anthropic"
	"github.com/everstacklabs/everstack/internal/api/http/handlers"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	http_mw "github.com/everstacklabs/everstack/internal/api/http/middleware/interceptors"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/api/service/registry"
	"github.com/everstacklabs/everstack/internal/cqrs"
	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	"github.com/everstacklabs/everstack/internal/enterprise"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/mferrors"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

type API struct {
	port            uint16
	externalDomain  string
	health          healthCheck
	dependencies    []DependencyCheck
	router          *mux.Router
	hostHeaders     []string
	connectServices map[string][]string
	grpcGateway     http.Handler
	grpcGatewayMux  *runtime.ServeMux
	grpcEndpoint    string
	grpcDialOpts    []grpc.DialOption
	grpcDirectConn  grpc.ClientConnInterface
	serviceRegistry *registry.Registry
	// baseCtx carries process-level context (with CQRS system) for injecting into requests
	baseCtx context.Context
}

func (a *API) ListGrpcServices() []string {
	services := make([]string, len(a.connectServices))
	i := 0
	for prefix := range a.connectServices {
		services[i] = strings.Trim(prefix, "/")
		i++
	}
	sort.Strings(services)
	return services
}

func (a *API) ListGrpcMethods() []string {
	methods := make([]string, 0)
	for service, methodList := range a.connectServices {
		for _, method := range methodList {
			methods = append(methods, service+method)
		}
	}
	sort.Strings(methods)
	return methods
}

type healthCheck interface {
	Health(ctx context.Context) error
}

// DependencyCheck describes an external service exposed through dependency
// health. Required dependencies participate in readiness. Optional dependency
// failures report a degraded state without removing the pod from service.
type DependencyCheck struct {
	Name     string
	Required bool
	Check    func(context.Context) error
}

func NewAPI(
	ctx context.Context,
	port uint16,
	externalDomain string,
	hostHeaders []string,
	tlsConfig *tls.Config,
	router *mux.Router,
) (_ *API, err error) {
	api := &API{
		port:            port,
		externalDomain:  externalDomain,
		hostHeaders:     hostHeaders,
		router:          router,
		connectServices: make(map[string][]string),
		baseCtx:         ctx,
	}

	api.RegisterHandlerOnPrefix("/debug", api.healthHandler())

	// Initialize a grpc-gateway mux and mount it under / if available.
	if gw, gwErr := grpcserver.NewGRPCJSONGateway(ctx, externalDomain, port, tlsConfig); gwErr == nil {
		api.grpcGateway = gw
		// Capture mux and endpoint from server package for per-service registration
		api.grpcGatewayMux = grpcserver.GatewayMux()
		api.grpcEndpoint = grpcserver.GatewayEndpoint()
		api.grpcDialOpts = grpcserver.GatewayDialOpts()
		api.grpcDirectConn = grpcserver.GatewayDirectConn()
		// Mount the gateway under /v1; streaming is controlled by request body and config only.
		cfg := apiConfigFromCtx(ctx)
		// SpendLimitStatus sits directly on the grpc-gateway mux, inside the
		// OpenAI and SSE wrappers, so the Cloud spend limit refusal reaches
		// every /v1 and /openai/v1 caller as 402, not the 429 its gRPC code
		// maps to (OpenAI SDKs retry 429; the limit resets at 00:00 UTC).
		handler := middleware.TraceContextHTTPHandler(httpmw.SpendLimitStatus(gw))

		// Apply OpenAI compatibility transformation FIRST before grpc-gateway processes the request
		// This converts OpenAI format (content as string) to proto format (content as array of ContentPart)
		handler = withOpenAICompatTransform(handler)

		// Check if SSE is enabled - prefer RuntimeConfigService for hot-reload support
		var runtimeConfigSvc *rtconfig.Service
		if svcAny := ctx.Value(contextkeys.RuntimeConfigService); svcAny != nil {
			runtimeConfigSvc, _ = svcAny.(*rtconfig.Service)
		}

		// Get initial values from static config (fallback)
		defaultStream := false
		enableSSE := false
		if feat, _ := ctx.Value(contextkeys.FeaturesConfig).(*validator.FeaturesConfig); feat != nil {
			defaultStream = feat.Gateway.EnableStreaming
			enableSSE = feat.Gateway.EnableSSE
		}

		// If RuntimeConfigService is available, SSE is always enabled and streaming is checked dynamically.
		// Boot-time SSE check uses the bootstrap context's tenant (empty
		// for self-hosted; hosted multi-tenant gateways enable SSE for the
		// "default" tenant at boot, then per-request streaming checks
		// resolve the real tenant from request context below).
		useRuntimeConfig := runtimeConfigSvc != nil
		if useRuntimeConfig {
			enableSSE = runtimeConfigSvc.IsSSEEnabled(contextkeys.ExtractTenantID(ctx))
		}

		// Apply API key validation middleware (require API keys)
		// For self-hosted: use DB-backed session validation so session cookies work behind reverse proxies
		if db, ok := ctx.Value(contextkeys.PrimaryDB).(*sqlx.DB); ok && db != nil {
			interceptor := httpmw.NewAPIKeyInterceptorWithSessionDB(false, db)
			handler = interceptor.WithAPIKeyValidation(handler)
		} else {
			handler = httpmw.WithAPIKeyValidation(handler, false) // false = require API key
		}
		// Apply license enforcement for REST gateway routes (no-op in CE builds).
		handler = enterprise.LicenseEnforcerFromContext(ctx).WithLicenseEnforcement(handler)

		// Apply spend limit enforcement for metered paths (AI gateway endpoints).
		// Uses local cached state for zero-latency checks — no network calls.
		{
			monitor := enterprise.LicenseMonitorFromContext(ctx)
			policy := apilic.NewDefaultPolicy()
			base := handler
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if policy.ShouldMeterRequest(r.URL.Path) {
					if blocked, reason := monitor.IsSpendBlocked(); blocked {
						logger.Warnf("api: request blocked - spend limit exceeded: %s", reason)
						writeSpendLimitError(w, reason)
						return
					}
				} else if policy.ShouldShadowMeterRequest(r.URL.Path) {
					// G0 shadow: observe billable AI paths outside the
					// metered allowlist. Log-only; no gating change.
					logger.Warnf("api: shadow observation - billable AI path outside metered prefixes: %s", r.URL.Path)
				}
				base.ServeHTTP(w, r)
			})
		}

		// Inject fast-path engine from startup ctx into each HTTP request context
		if engine := fastpath.GetGlobalEngine(); engine != nil && engine.IsEnabled() {
			base := handler
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// copy engine into request context
				r = r.WithContext(fastpath.WithEngine(r.Context(), engine))
				base.ServeHTTP(w, r)
			})
		}

		// Inject CQRS system from startup ctx into each HTTP request context so middleware can access it (must be outermost)
		if sys, err := cqrs.GetSystemFromContext(ctx); err == nil && sys != nil {
			base := handler
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// copy system into request context
				r = r.WithContext(cqrs.WithSystem(r.Context(), sys))
				base.ServeHTTP(w, r)
			})
		}

		if enableSSE {
			inner := httpmw.WrapWithSSENegotiationWith(handler, func(r *http.Request) string {
				fmt := "proto"
				if cfg != nil {
					if df := strings.ToLower(cfg.SSE.DefaultFormat); df != "" {
						fmt = df
					}
					for _, rt := range cfg.SSE.Routes {
						if strings.HasPrefix(r.URL.Path, rt.Path) {
							fmt = strings.ToLower(rt.Format)
							break
						}
					}
				}
				if h := r.Header.Get("X-SSE-Format"); h != "" {
					fmt = strings.ToLower(h)
				}
				return fmt
			})
			// Use dynamic streaming config when RuntimeConfigService is available.
			// Tenant resolves to the bootstrap context's tenant (empty for
			// self-hosted, "default" for hosted) — per-request tenant
			// streaming is a follow-up; the closure signature here doesn't
			// receive the request, so we'd need a middleware refactor.
			if useRuntimeConfig {
				bootTenant := contextkeys.ExtractTenantID(ctx)
				handler = httpmw.WithSSEAcceptForStreamingDynamic(inner, httpmw.StreamingConfig{
					IsEnabled: func() bool {
						return runtimeConfigSvc.IsStreamingEnabled(bootTenant)
					},
					IsDefault: func() bool {
						// Never default to streaming - user must explicitly request it
						// This allows curl | jq to work without specifying "stream": false
						return false
					},
				})
			} else {
				handler = httpmw.WithSSEAcceptForStreamingDefault(inner, defaultStream)
			}
		} else {
			logger.Warn("SSE disabled, not wrapping /v1 handler with SSE middleware")
		}

		// Add rate limit headers middleware to forward provider rate limit info to clients
		handler = httpmw.RateLimitHeadersMiddleware(handler)

		// Per-tenant request rate limiting (RPM/burst from runtime_config).
		// Sits outside the upstream-header passthrough above so a 429 from
		// our limiter still emits our own X-RateLimit-* headers (the
		// upstream wrapper would otherwise overwrite them on success).
		// Pass-through if RuntimeConfigService isn't wired.
		if runtimeConfigSvc != nil {
			handler = httpmw.NewRateLimiter(runtimeConfigSvc).Wrap(handler)
		}

		// Ensure the JSON shim targets the wrapped/validated gateway handler
		api.grpcGateway = handler

		// /v1 is the documented OpenAI-compatible base URL: an OpenAI SDK
		// sending its key as Bearer gets OpenAI-shaped answers on the OpenAI
		// verbs, native x-evs-api-key callers keep proto-JSON. See
		// withV1OpenAICompat.
		api.mountMessages(handler)
		api.RegisterHandlerPrefixes(grpcserver.StripClientGatewayIdentity(withV1OpenAICompat(handler)), "/v1")
		// /openai/v1 is the older alias of the same OpenAI-compatible surface,
		// still served for clients configured against it. Reuses the same
		// authed + metered /v1 chain; see withOpenAIRoundTrip.
		api.RegisterHandlerPrefixes(grpcserver.StripClientGatewayIdentity(withOpenAIRoundTrip(handler)), "/openai/v1")
	} else {
		logger.WithError(gwErr).Warn("grpc-gateway initialization failed; continuing without REST gateway")
	}
	// Serve OpenAPI JSONs under /openapi
	api.RegisterHandlerOnPrefix("/openapi", http.StripPrefix("/openapi", http.FileServer(http.Dir("openapi"))))
	return api, nil
}

// mountMessages registers exact paths before the /v1 prefix. The translator
// invokes the same OpenAI wire adapter and authenticated chain once, in process.
func (a *API) mountMessages(inner http.Handler) {
	// Same edge rule as /v1: a client-supplied copy of the forwarded gateway
	// identity never reaches the authenticated chain.
	handler := grpcserver.StripClientGatewayIdentity(anthropic.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveOpenAICompat(w, r, inner) })))
	a.router.Handle("/v1/messages", handler)
	a.router.Handle("/v1/messages/count_tokens", handler)
}

// apiConfigFromCtx retrieves the validated gateway config from context if stored by the caller.
// Fallback: return nil and selector will use defaults.
func apiConfigFromCtx(ctx context.Context) *validator.GatewayConfig {
	// In this codebase, NewAPI is invoked with fully-parsed config elsewhere.
	// If needed, thread it through context.WithValue at the call site and fetch here.
	cfg, _ := ctx.Value(contextkeys.GatewayConfig).(*validator.GatewayConfig)
	return cfg
}

func (a *API) serverReflection() {
	reflector := grpcreflect.NewStaticReflector(a.ListGrpcServices()...)
	v1Pattern, v1Handler := grpcreflect.NewHandlerV1(reflector)
	a.RegisterHandlerPrefixes(v1Handler, v1Pattern)
	v1aPattern, v1aHandler := grpcreflect.NewHandlerV1Alpha(reflector)
	a.RegisterHandlerPrefixes(v1aHandler, v1aPattern)
}

// EnableServerReflection enables server reflection for ConnectRPC services
func (a *API) EnableServerReflection() {
	a.serverReflection()
}

func (a *API) RegisterService(ctx context.Context, srv grpcserver.Server) error {
	// Right now we're only supporting connectrpc services and not grpc gateway. If we do decide to go with grpc gateway, we need to add the grpc gateway server to the api and register it here.
	a.registerConnectServer(srv.(grpcserver.ConnectServer))

	// If the service also exposes grpc-gateway handlers and the mux exists, register them now.
	// A service registered here replaces any handlers already registered for
	// the same routes (grpc-gateway matches the latest registration first), so
	// it must use the same transport NewGRPCJSONGateway chose for it.
	if a.grpcGatewayMux != nil {
		if direct, ok := any(srv).(grpcserver.GatewayClientRegistrable); ok && a.grpcDirectConn != nil {
			if err := direct.RegisterGatewayClient(ctx, a.grpcGatewayMux, a.grpcDirectConn); err != nil {
				return err
			}
		} else if registrable, ok := any(srv).(grpcserver.GatewayRegistrable); ok {
			if err := registrable.RegisterGateway(ctx, a.grpcGatewayMux, a.grpcEndpoint, a.grpcDialOpts); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *API) registerConnectServer(service grpcserver.ConnectServer) {
	interceptors := []connect.Interceptor{middleware.NewTraceContextInterceptor()}
	// Always inject CQRS system first so downstream interceptors (API key) can use it
	if sys, err := cqrs.GetSystemFromContext(a.baseCtx); err == nil && sys != nil {
		interceptors = append(interceptors, middleware.NewSystemContextInjector(sys))
	}
	// Inject RuntimeConfigService for hot-reload configuration support
	if svcAny := a.baseCtx.Value(contextkeys.RuntimeConfigService); svcAny != nil {
		if runtimeSvc, ok := svcAny.(*rtconfig.Service); ok && runtimeSvc != nil {
			interceptors = append(interceptors, middleware.NewRuntimeConfigInjector(runtimeSvc))
		}
	}
	if a.serviceRegistry != nil {
		interceptors = append(interceptors, a.serviceRegistry.BuildInterceptors(service)...)
	} else {
		interceptors = append(interceptors,
			middleware.NewAPIKeyInterceptor(false), // require API key; adds correlation id and headers per Connect docs
			middleware.CallDurationHandler(),
			middleware.ActivityInterceptor(),
			middleware.LLMActivityInterceptor(),
			middleware.ValidationHandler(),
		)
	}
	// Skip compression for payloads below this size. ChatCompletion is a
	// server-streaming RPC that emits one envelope per token, and those
	// envelopes are 159-228 bytes. gzip's header alone is around 20 bytes, so
	// compressing them cannot pay for itself, and connect builds a fresh
	// compressor per envelope: a 64-token completion did 67 of them.
	//
	// Under load this was the single largest CPU consumer in the process --
	// compress/flate.(*compressor).reset at 16% flat, with the window-zeroing
	// memclr beneath it at another 5%. connect defaults this threshold to 0,
	// meaning everything compresses however small.
	//
	// Large payloads still compress, so an external client sending a big
	// request body or receiving a big unary response is unaffected.
	const compressMinBytes = 1024

	handlerOpts := []connect.HandlerOption{
		connect.WithInterceptors(interceptors...),
		connect.WithCompressMinBytes(compressMinBytes),
	}
	prefix, handler := service.RegisterConnectServer(handlerOpts...)
	methods := service.FileDescriptor().Services().Get(0).Methods()
	methodNames := make([]string, methods.Len())
	for i := 0; i < methods.Len(); i++ {
		methodNames[i] = string(methods.Get(i).Name())
	}
	a.connectServices[prefix] = methodNames
	// Add JSON compatibility first: if clients POST application/json to Connect RPC paths,
	// forward them to the grpc-gateway REST endpoints.
	var finalHandler http.Handler = handler
	if a.grpcGateway != nil {
		finalHandler = jsonCompatForConnectPaths(handler, a.grpcGateway)
	}

	// Per-tenant CORS: layered inside the static CORS so preflight (no
	// auth, can't resolve tenant) still uses the gateway-wide policy
	// while real requests get tenant overrides applied via header
	// rewrite. No-op when rtconfig service isn't wired.
	innerHandler := finalHandler
	if svcAny := a.baseCtx.Value(contextkeys.RuntimeConfigService); svcAny != nil {
		if svc, ok := svcAny.(*rtconfig.Service); ok && svc != nil {
			innerHandler = httpmw.NewRuntimeCORS(svc).Wrap(finalHandler)
		}
	}

	// Wrap the handler with response wrapper to add correlation IDs
	wrappedHandler := middleware.NewResponseWrapper(http_mw.CORSInterceptor(innerHandler))

	a.RegisterHandlerPrefixes(grpcserver.StripClientGatewayIdentity(wrappedHandler), prefix)
}

// SetRegistry allows wiring a service registry for dynamic interceptor selection.
func (a *API) SetRegistry(r *registry.Registry) { a.serviceRegistry = r }

func (a *API) GetRegistry() *registry.Registry { return a.serviceRegistry }

// HandleFunc allows registering a [http.HandlerFunc] on an exact
// path, instead of prefix like RegisterHandlerOnPrefix.
func (a *API) HandleFunc(path string, f http.HandlerFunc) {
	a.router.HandleFunc(path, f)
}

// RegisterHandlerOnPrefix registers a http handler on a path prefix
// the prefix will not be passed to the actual handler
func (a *API) RegisterHandlerOnPrefix(prefix string, handler http.Handler) {
	prefix = strings.TrimSuffix(prefix, "/")
	subRouter := a.router.PathPrefix(prefix).Name(prefix).Subrouter()
	subRouter.PathPrefix("").Handler(http.StripPrefix(prefix, handler))
}

// RegisterHandlerPrefixes registers a http handler on a multiple path prefixes
// the prefix will remain when calling the actual handler
func (a *API) RegisterHandlerPrefixes(handler http.Handler, prefixes ...string) {
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		subRouter := a.router.PathPrefix(prefix).Name(prefix).Subrouter()
		subRouter.PathPrefix("").Handler(handler)
	}
}

// AddExternalServicePrefixes registers external Connect service prefixes so they
// appear in server reflection, even if the handlers are reverse-proxied.
// Provide prefixes like "/pkg.svc.v1.ServiceName/".
func (a *API) AddExternalServicePrefixes(prefixes ...string) {
	for _, p := range prefixes {
		if strings.TrimSpace(p) == "" {
			continue
		}
		// Normalize to have leading and trailing slashes
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		if !strings.HasSuffix(p, "/") {
			p = p + "/"
		}
		if _, exists := a.connectServices[p]; !exists {
			a.connectServices[p] = []string{}
		}
	}
}

// GRPCGateway exposes the mounted grpc-gateway handler for wrapping.
func (a *API) GRPCGateway() http.Handler { return a.grpcGateway }

// MountGRPCGatewayPrefixes makes grpc-gateway routes outside the default /v1
// namespace reachable through the main HTTP router. The registered gateway
// keeps the full request path, matching google.api.http route annotations.
func (a *API) MountGRPCGatewayPrefixes(prefixes ...string) {
	if a.grpcGateway == nil {
		return
	}
	a.RegisterHandlerPrefixes(a.grpcGateway, prefixes...)
}

// SetHealth sets the health checker used by /debug/ready and /debug/validate.
func (a *API) SetHealth(h healthCheck) { a.health = h }

// SetDependencies sets the required and optional external dependency checks.
func (a *API) SetDependencies(checks []DependencyCheck) { a.dependencies = checks }

func (a *API) healthHandler() http.Handler {
	// Resolve health dynamically because StartAPI wires checks after NewAPI has
	// registered this handler.
	checks := []ValidationFunction{
		func(ctx context.Context) error {
			if a.health == nil {
				return nil
			}
			if err := a.health.Health(ctx); err != nil {
				return mferrors.ThrowInternal(err, "API-DBConn-5003", "DB CONNECTION ERROR")
			}
			return nil
		},
		func(ctx context.Context) error {
			for _, dependency := range a.dependencies {
				if !dependency.Required || dependency.Check == nil {
					continue
				}
				if err := dependency.Check(ctx); err != nil {
					return mferrors.ThrowInternal(err, "API-Dependency-5004", "REQUIRED DEPENDENCY UNAVAILABLE")
				}
			}
			return nil
		},
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", handleHealth)
	handler.HandleFunc("/version", handleVersion)
	handler.HandleFunc("/ready", handleReadiness(checks))
	handler.HandleFunc("/validate", handleValidate(checks))
	handler.HandleFunc("/health/dependencies", a.handleDependencyHealth)
	// handler.Handle("/metrics", metricsExporter())

	// Rate limit and provider health state is process-wide: on a shared
	// gateway it mixes every tenant's key ids, quotas and raw upstream error
	// text. These are operator views, so they need a signed-in organization
	// owner or admin. The liveness and readiness endpoints above stay open.
	handler.HandleFunc("/ratelimit/status", requireOrgAdmin(handlers.RateLimitStatusHandler))
	handler.HandleFunc("/ratelimit/subscribe", requireOrgAdmin(handlers.RateLimitSubscribeHandler))
	handler.HandleFunc("/health/providers", requireOrgAdmin(handlers.ProviderHealthHandler))
	handler.HandleFunc("/health/providers/", requireOrgAdmin(handlers.ProviderHealthByNameHandler))

	return handler
}

type dependencyStatus struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
}

func (a *API) handleDependencyHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	httpStatus := http.StatusOK
	dependencies := make([]dependencyStatus, 0, len(a.dependencies))
	for _, dependency := range a.dependencies {
		dependencyState := "ok"
		var err error
		if dependency.Check == nil {
			err = errors.New("dependency check is not configured")
		} else {
			err = dependency.Check(r.Context())
		}
		if err != nil {
			dependencyState = "unavailable"
			logger.WithFields("dependency", dependency.Name, "required", dependency.Required).WithError(err).Warn("dependency health check failed")
			if dependency.Required {
				status = "unavailable"
				httpStatus = http.StatusServiceUnavailable
			} else if status == "ok" {
				status = "degraded"
			}
		}
		dependencies = append(dependencies, dependencyStatus{
			Name:     dependency.Name,
			Required: dependency.Required,
			Status:   dependencyState,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":       status,
		"dependencies": dependencies,
	}); err != nil {
		logger.WithError(err).Error("failed to write dependency health response")
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	_, err := w.Write([]byte("ok"))
	logger.WithFields(
		// "traceID", tracing.TraceIDFromCtx(r.Context())
		"traceID", "1234",
	).OnError(err).Error("error writing ok for health")
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	http_util.MarshalJSON(w, map[string]string{
		"version": build.Version(),
		"commit":  build.Commit(),
		"date":    build.Date().Format("2006-01-02T15:04:05Z"),
	}, nil, http.StatusOK)
}

func handleReadiness(checks []ValidationFunction) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		errs := validate(r.Context(), checks)
		if len(errs) == 0 {
			http_util.MarshalJSON(w, "ok", nil, http.StatusOK)
			return
		}
		http_util.MarshalJSON(w, nil, errs[0], http.StatusPreconditionFailed)
	}
}

func handleValidate(checks []ValidationFunction) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		errs := validate(r.Context(), checks)
		if len(errs) == 0 {
			http_util.MarshalJSON(w, "ok", nil, http.StatusOK)
			return
		}
		http_util.MarshalJSON(w, errs, nil, http.StatusOK)
	}
}

// jsonCompatForConnectPaths dispatches application/json requests sent to
// ConnectRPC service paths to the grpc-gateway REST endpoints for
// compatibility with simple curl/clients.
func jsonCompatForConnectPaths(next http.Handler, gw http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if strings.HasPrefix(ct, "application/json") {
			var mapped string
			switch r.URL.Path {
			case gatewayconnect.GatewayServiceChatCompletionProcedure:
				mapped = "/v1/chat/completions"
				var reject string
				if r, reject = transformChatCompletionRequestChecked(r); reject != "" {
					writeInvalidArgument(w, reject)
					return
				}
			case gatewayconnect.GatewayServiceEmbeddingsProcedure:
				mapped = "/v1/embeddings"
				r = transformEmbeddingsRequest(r)
			}
			if mapped != "" {
				r2 := r.Clone(r.Context())
				r2.URL.Path = mapped
				gw.ServeHTTP(w, r2)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withOpenAICompatTransform wraps a handler with OpenAI compatibility transformation.
// It transforms chat completion and embeddings requests from OpenAI format to proto format.
func withOpenAICompatTransform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only transform POST requests with JSON content to chat/embeddings endpoints
		if r.Method == http.MethodPost && strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			switch r.URL.Path {
			case "/v1/chat/completions":
				var reject string
				if r, reject = transformChatCompletionRequestChecked(r); reject != "" {
					writeInvalidArgument(w, reject)
					return
				}
			case "/v1/embeddings":
				r = transformEmbeddingsRequest(r)
			case "/v1/responses":
				r = transformResponsesRequest(r)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// openAIStreamDefaultKey marks a chat request that arrived on the
// OpenAI-compatible surface without a `stream` field.
type openAIStreamDefaultKey struct{}

// withOpenAIStreamDefault records that the request omitted `stream` on the
// OpenAI-compatible surface, where that means a non-streaming call.
func withOpenAIStreamDefault(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAIStreamDefaultKey{}, true)
}

// chatRequestOffersTools reports whether a decoded chat request gives the
// model tools to call. Anything other than an absent, null or empty `tools`
// counts, so a malformed value leaves the request's routing as it was.
func chatRequestOffersTools(body map[string]interface{}) bool {
	switch tools := body["tools"].(type) {
	case nil:
		return false
	case []interface{}:
		return len(tools) > 0
	default:
		return true
	}
}

// openAIStreamDefault reports whether transformChatCompletionRequest should
// write the OpenAI default (`stream: false`) into the request it forwards.
func openAIStreamDefault(ctx context.Context) bool {
	v, _ := ctx.Value(openAIStreamDefaultKey{}).(bool)
	return v
}

// bufferingResponseWriter captures a downstream response so it can be rewritten
// before being sent to the client (used by the /openai/v1 round-trip surface).
type bufferingResponseWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (w *bufferingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *bufferingResponseWriter) WriteHeader(s int) { w.status = s }

// Flush is a no-op: everything is sent at once after conversion. The
// grpc-gateway refuses to forward a server stream (CreateResponse) to a
// writer that cannot flush ("unexpected type of web server", 500), so a
// unary OpenAI Responses call needs one.
func (w *bufferingResponseWriter) Flush() {}
func (w *bufferingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(b)
}

// serveOpenAIModels answers a model listing in the OpenAI `{"object":"list",
// "data":[...]}` shape by delegating to the gateway's own /v1/gateway/models
// handler and reshaping its response.
//
// The listing is the tenant's enabled models, which is what OpenAI semantics
// require: `client.models.list()` must return what the caller can actually
// invoke. It is deliberately not the upstream model catalog
// (catalog.everstack.ai, the signed metadata bundle instances sync from):
// serving that here would offer thousands of models the tenant has not enabled
// and fail on the first call to any of them.
//
// r must already have its path rewritten for the inner handler.
func serveOpenAIModels(w http.ResponseWriter, r *http.Request, next http.Handler) {
	r.URL.Path = "/v1/gateway/models"
	rec := &bufferingResponseWriter{}
	next.ServeHTTP(rec, r)
	body, status := rec.buf.Bytes(), rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusOK {
		if conv, ok := gatewayModelsToOpenAI(body); ok {
			body = conv
		}
	} else if conv, mapped, ok := protoErrorToOpenAI(body, status); ok {
		body, status = conv, mapped
	}
	writeOpenAIBuffered(w, rec.Header(), status, body)
}

// openAIBearerAPIKey returns the API key an OpenAI-style client carries in
// `Authorization: Bearer <key>`, or "" when the header is absent or carries
// anything other than a key this platform issued.
//
// It is an allowlist on the issued-key prefixes (apikeylib.HasIssuedKeyPrefix)
// rather than "any bearer": Authorization is also how a device-auth JWT and a
// session token arrive, and those must keep falling through to the API-key
// interceptor's rejection instead of being probed against the key store as if
// they were keys.
func openAIBearerAPIKey(h http.Header) string {
	a := strings.TrimSpace(h.Get(common.Authorization))
	if !strings.HasPrefix(a, "Bearer ") {
		return ""
	}
	token := strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	if !apikeylib.HasIssuedKeyPrefix(token) {
		return ""
	}
	return token
}

// hasBearerAuth reports whether the request carries a non-empty
// Authorization: Bearer credential of any kind.
func hasBearerAuth(h http.Header) bool {
	a := strings.TrimSpace(h.Get(common.Authorization))
	return strings.HasPrefix(a, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(a, "Bearer ")) != ""
}

// isV1OpenAIPath reports whether a /v1 request is one of the OpenAI verbs an
// OpenAI SDK sends when its base URL is https://<instance>/v1. Only these take
// a Bearer key on /v1: the rest of the prefix (OTLP receivers, deploy invoke,
// native REST) has its own auth contract and is left alone.
func isV1OpenAIPath(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/v1/chat/completions" && r.Method == http.MethodPost,
		p == "/v1/embeddings" && r.Method == http.MethodPost,
		p == "/v1/models" && r.Method == http.MethodGet,
		p == "/v1/responses", strings.HasPrefix(p, "/v1/responses/"):
		return true
	case r.Method == http.MethodPost && (p == "/v1/images/generations" ||
		p == "/v1/moderations" ||
		p == "/v1/rerank"):
		// JSON-bodied inference routes. Their answers pass through
		// serveOpenAICompat unreshaped (errors still become the OpenAI
		// envelope). Image edits and variations (multipart in OpenAI) and the
		// audio routes (multipart uploads, binary speech) are not listed: the
		// native routes cannot serve an OpenAI SDK's request or response.
		return true
	}
	return false
}

// withV1OpenAICompat makes the documented base URL, https://<instance>/v1,
// work with an unmodified OpenAI SDK.
//
// An SDK can only send its key as `Authorization: Bearer`, and it parses the
// OpenAI response envelope. /v1 rejected the first and answered in proto-JSON
// (grpc-gateway `{"result":...}` frames), so the SDK failed at both ends. When
// an OpenAI verb on /v1 arrives with an issued key as its bearer, this serves
// it exactly as /openai/v1 does: the key moves onto the api-key header, the
// Authorization header is dropped (the grpc-gateway forwards it as metadata,
// and the Connect interceptor still refuses any bearer), and the response is
// reshaped by serveOpenAICompat.
//
// The key is not trusted here. It goes through the unchanged API-key
// interceptor (hash, bloom filter, revocation, deleted-instance check), so
// this widens how a credential is carried, never what it grants. An explicit
// x-evs-api-key (or a legacy alias) wins over the bearer, which is then
// dropped unread. A bearer that is not an issued key (device-auth JWT,
// session token) and comes without an explicit header is passed through
// untouched and still gets the interceptor's 401.
//
// Native callers that send x-evs-api-key without a bearer keep the proto-JSON
// response they have always had: the reshaping keys off the bearer, which /v1
// rejected outright until now, so no working caller changes behaviour.
//
// GET /v1/models is served regardless of credential shape: the grpc-gateway
// has no route for it, so it used to 404 even for an authenticated caller.
func withV1OpenAICompat(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isV1OpenAIPath(r) {
			explicit := common.GetHTTPHeader(r.Header, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey) != ""
			key := openAIBearerAPIKey(r.Header)
			// An OpenAI SDK always sends a bearer. With an explicit api-key
			// header that bearer is the SDK's placeholder (createOpenAIConfig
			// sends apiKey "everstack" plus x-evs-api-key) and is dropped
			// unread; the header is the credential either way.
			if key != "" || (explicit && hasBearerAuth(r.Header)) {
				r2 := r.Clone(r.Context())
				if !explicit {
					r2.Header.Set(common.EverstackApiKey, key)
				}
				r2.Header.Del(common.Authorization)
				serveOpenAICompat(w, r2, next)
				return
			}
		}
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			serveOpenAIModels(w, r.Clone(r.Context()), next)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withOpenAIRoundTrip exposes a fully OpenAI-wire-compatible surface under
// /openai/v1/*, kept as an alias of the OpenAI verbs on /v1 for clients
// configured before /v1 accepted a Bearer key. It translates any Bearer onto
// the api-key header, rewrites the path to /v1/* so the request flows through
// the same authed + metered chain, and reshapes the response (see
// serveOpenAICompat).
func withOpenAIRoundTrip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OpenAI clients authenticate with Authorization: Bearer <key>; the
		// gateway expects the api-key header and rejects Bearer. Map it across
		// (the key is still validated downstream). Only when no api-key header
		// (canonical or legacy) is already set; emit the canonical x-evs-api-key.
		if common.GetHTTPHeader(r.Header, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey) == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				if key := strings.TrimSpace(strings.TrimPrefix(a, "Bearer ")); key != "" {
					r.Header.Set(common.EverstackApiKey, key)
				}
			}
		}
		r.Header.Del("Authorization")

		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/openai")
		serveOpenAICompat(w, r2, next)
	})
}

// serveOpenAICompat runs an OpenAI-shaped request through the native /v1 chain
// and converts the proto-JSON answer back to OpenAI JSON. r2 is owned by this
// call, already carries its credential on the api-key header, and has its
// path set to the native /v1 route.
//
// Covered routes:
//   - POST /v1/chat/completions: unary and SSE streaming. Streaming rides the
//     /v1 SSE middleware with X-SSE-Format: openai; when the instance has SSE
//     disabled the response degrades to a single chat.completion JSON body
//     (see sseSniffWriter).
//   - GET  /v1/models: rewritten to /v1/gateway/models and reshaped into the
//     OpenAI model list.
//   - POST /v1/embeddings: stream-envelope unwrap (the inner frame is already
//     OpenAI-shaped).
//   - POST /v1/responses: unary and SSE streaming. Streaming rides the /v1
//     SSE middleware with X-SSE-Format: openai-responses, which reframes the
//     CreateResponse server stream into OpenAI Responses events
//     (response.created ... response.completed) as each frame arrives. The
//     unary answer, and a stream on an instance with SSE disabled, is the
//     final response object.
//
// Error bodies on all buffered paths are rewritten into the OpenAI error
// envelope with a gRPC-code-derived HTTP status.
func serveOpenAICompat(w http.ResponseWriter, r2 *http.Request, next http.Handler) {
	inner := r2.URL.Path

	// GET /v1/models -> /v1/gateway/models, reshaped.
	if inner == "/v1/models" && r2.Method == http.MethodGet {
		serveOpenAIModels(w, r2, next)
		return
	}

	isChat := inner == "/v1/chat/completions"
	isEmbeddings := inner == "/v1/embeddings"
	isResponses := inner == "/v1/responses" && r2.Method == http.MethodPost

	// Read the body once to learn whether the caller wants a stream.
	streamWanted := false
	if (isChat || isResponses) && r2.Body != nil {
		if b, err := io.ReadAll(r2.Body); err == nil {
			_ = r2.Body.Close()
			var m map[string]interface{}
			if json.Unmarshal(b, &m) == nil {
				if s, ok := m["stream"].(bool); ok && s {
					streamWanted = true
				}
				// OpenAI defines an absent (or null) `stream` as false. The
				// native handler instead falls back to the instance's
				// streaming default, which turns this buffered request into a
				// streamed upstream call that is then re-aggregated frame by
				// frame. Pin the OpenAI meaning so the upstream call is unary.
				//
				// Not for a request that offers tools. The handler's unary
				// branch runs the server-side tool loop on any tool call the
				// model makes, including calls to tools the client declared
				// and means to run itself; routing those requests there would
				// change what they get back and how many upstream calls they
				// cost. They keep the instance default until that loop only
				// claims tools the gateway owns.
				if v, present := m["stream"]; isChat && (!present || v == nil) && !chatRequestOffersTools(m) {
					r2 = r2.WithContext(withOpenAIStreamDefault(r2.Context()))
				}
			}
			r2.Body = io.NopCloser(bytes.NewReader(b))
			r2.ContentLength = int64(len(b))
		}
	}

	if streamWanted {
		// Let the /v1 SSE middleware do the streaming; it reframes the
		// grpc-gateway delta frames into OpenAI chat.completion.chunk
		// events when the format is "openai" and closes with [DONE].
		// No Accept override: the middleware engages on stream:true +
		// this header and sets Accept downstream itself, so when SSE is
		// disabled the chain stays on plain NDJSON and the sniff writer
		// degrades to a single JSON body.
		r2.Header.Set("X-SSE-Format", "openai")
		sniff := &sseSniffWriter{w: w}
		if isResponses {
			// Same path for the Responses API, in its own event format
			// (the chat chunk format cannot carry a response object).
			r2.Header.Set("X-SSE-Format", httpmw.SSEFormatOpenAIResponses)
			sniff.convert = protoResponseStreamToOpenAI
			sniff.convertErr = protoResponseStreamErrorToOpenAI
		}
		next.ServeHTTP(sniff, r2)
		sniff.finishBuffered()
		return
	}
	if isResponses {
		// A unary request must come back as NDJSON frames to be reshaped,
		// whatever SSE format the instance defaults to.
		r2.Header.Set("X-SSE-Format", "proto")
	}

	rec := &bufferingResponseWriter{}
	next.ServeHTTP(rec, r2)

	body := rec.buf.Bytes()
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusOK {
		switch {
		case isChat:
			if conv, ok := protoChatCompletionToOpenAI(body); ok {
				body = conv
			} else if conv, mapped, ok := protoErrorToOpenAI(body, status); ok {
				// Some error paths report 200 with an error body.
				body, status = conv, mapped
			}
		case isResponses:
			if conv, ok := protoResponseStreamToOpenAI(body); ok {
				body = conv
			} else if conv, mapped, ok := protoResponseStreamErrorToOpenAI(body, status); ok {
				body, status = conv, mapped
			}
		case isEmbeddings:
			if conv, ok := protoEmbeddingsToOpenAI(body); ok {
				body = conv
			} else if conv, mapped, ok := protoErrorToOpenAI(body, status); ok {
				body, status = conv, mapped
			}
		}
	} else if conv, mapped, ok := protoErrorToOpenAI(body, status); ok {
		body, status = conv, mapped
	}
	writeOpenAIBuffered(w, rec.Header(), status, body)
}

// writeOpenAIBuffered sends a converted buffered response, dropping the stale
// Content-Length from the inner response.
func writeOpenAIBuffered(w http.ResponseWriter, hdr http.Header, status int, body []byte) {
	for k, vs := range hdr {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// protoChatCompletionToOpenAI converts the gateway's proto-JSON chat completion
// response into the standard OpenAI shape. Returns ok=false (and the caller keeps
// the original body) when the input does not match the expected proto shape.
func protoChatCompletionToOpenAI(body []byte) ([]byte, bool) {
	// grpc-gateway renders the (server-streaming) ChatCompletion RPC as a sequence
	// of newline-delimited {"result":{...}} frames where each frame carries a token
	// DELTA (the final frame is an empty terminator). Accumulate the deltas per
	// choice index to reconstruct the full message.
	type frameChoice struct {
		Message struct {
			ToolCalls []map[string]interface{} `json:"tool_calls"`
			Content   []struct {
				Text              string `json:"text"`
				ProviderJSON      string `json:"provider_json"`
				ProviderJSONCamel string `json:"providerJson"`
			} `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
		Index        int    `json:"index"`
	}
	type frame struct {
		ID      string          `json:"id"`
		Created json.RawMessage `json:"created"`
		Model   string          `json:"model"`
		Choices []frameChoice   `json:"choices"`
		Usage   json.RawMessage `json:"usage"`
	}

	texts := map[int]*strings.Builder{}
	toolCalls := map[int][]map[string]interface{}{}
	providerContent := map[int][]interface{}{}
	finish := map[int]string{}
	var id, model string
	var created, usage json.RawMessage
	any := false

	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(line, &top) != nil {
			continue
		}
		raw := line
		if res, ok := top["result"]; ok {
			raw = res
		}
		var f frame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		any = true
		if f.ID != "" {
			id = f.ID
		}
		if f.Model != "" {
			model = f.Model
		}
		if len(f.Created) > 0 {
			created = f.Created
		}
		if len(f.Usage) > 0 {
			usage = f.Usage
		}
		for _, c := range f.Choices {
			i := c.Index
			for _, call := range c.Message.ToolCalls {
				call["type"] = "function"
				f, _ := call["function"].(map[string]interface{})
				if f == nil {
					f = map[string]interface{}{}
					call["function"] = f
				}
				if _, ok := f["arguments"]; !ok {
					f["arguments"] = ""
				}
				toolCalls[i] = append(toolCalls[i], call)
			}
			if texts[i] == nil {
				texts[i] = &strings.Builder{}
			}
			for _, part := range c.Message.Content {
				texts[i].WriteString(part.Text)
				raw := part.ProviderJSON
				if raw == "" {
					raw = part.ProviderJSONCamel
				}
				if native, ok := decodeProviderJSON(raw); ok {
					providerContent[i] = append(providerContent[i], native)
				}
			}
			if c.FinishReason != "" {
				finish[i] = c.FinishReason
			}
		}
	}
	if !any || len(texts) == 0 {
		return nil, false
	}

	maxIdx := -1
	for i := range texts {
		if i > maxIdx {
			maxIdx = i
		}
	}
	choices := make([]map[string]interface{}, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		if texts[i] == nil {
			continue
		}
		fr := finish[i]
		if fr == "" {
			fr = "stop"
		}
		message := map[string]interface{}{
			"role":    "assistant",
			"content": texts[i].String(),
		}
		if len(toolCalls[i]) > 0 {
			message["tool_calls"] = toolCalls[i]
			if texts[i].Len() == 0 {
				message["content"] = nil
			}
			if fr == "stop" || fr == "function_call" {
				fr = "tool_calls"
			}
		}
		if len(providerContent[i]) > 0 {
			// Everstack extension: clients replay this opaque provider-native
			// content on the next assistant message as `provider_content`.
			message["provider_content"] = providerContent[i]
		}
		choices = append(choices, map[string]interface{}{
			"index":         i,
			"message":       message,
			"finish_reason": fr,
		})
	}
	out := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion",
		"created": parseCreatedUnix(created),
		"model":   model,
		"choices": choices,
	}
	if len(usage) > 0 {
		var u interface{}
		if json.Unmarshal(usage, &u) == nil {
			out["usage"] = u
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

func decodeProviderJSON(raw string) (interface{}, bool) {
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, false
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, false
	}
	return value, true
}

func providerContentToProtoParts(value interface{}) []map[string]interface{} {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}

	parts := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		var raw []byte
		if serialized, ok := item.(string); ok && json.Valid([]byte(serialized)) {
			raw = []byte(serialized)
		} else {
			encoded, err := json.Marshal(item)
			if err != nil {
				continue
			}
			raw = encoded
		}

		var metadata map[string]interface{}
		_ = json.Unmarshal(raw, &metadata)
		partType, _ := metadata["type"].(string)
		if partType == "" {
			partType = "provider"
		}
		part := map[string]interface{}{
			"type":          partType,
			"provider_json": string(raw),
		}
		if partType == "text" {
			if text, ok := metadata["text"].(string); ok {
				part["text"] = text
			}
		}
		parts = append(parts, part)
	}
	return parts
}

// parseCreatedUnix coerces the proto "created" field (which may be a JSON string
// or number) into a unix-seconds integer, defaulting to 0.
func parseCreatedUnix(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

// transformChatCompletionRequest transforms an OpenAI-compatible chat completion request
// to the proto format expected by grpc-gateway. The main transformation is converting
// messages.content from a simple string to an array of ContentPart objects.
func transformChatCompletionRequest(r *http.Request) *http.Request {
	r, _ = transformChatCompletionRequestChecked(r)
	return r
}

// writeInvalidArgument answers in the grpc-gateway error shape (gRPC code 3,
// HTTP 400), which serveOpenAICompat reshapes into the OpenAI error envelope.
func writeInvalidArgument(w http.ResponseWriter, message string) {
	body, _ := json.Marshal(map[string]interface{}{"code": 3, "message": message, "details": []interface{}{}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(body)
}

// chatRequestRejection reports why a decoded OpenAI chat request cannot be
// served faithfully, or "" when it can. The gateway returns exactly one
// choice per request (no adapter forwards n), so n > 1 is refused instead of
// silently answering with a single choice.
func chatRequestRejection(reqData map[string]interface{}) string {
	if n, ok := reqData["n"].(float64); ok && n > 1 {
		return "n > 1 is not supported: the gateway returns one choice per request; send separate requests instead"
	}
	return ""
}

// transformChatCompletionRequestChecked is transformChatCompletionRequest
// plus a rejection message ("" when the request is acceptable). On a
// rejection the request body is restored unchanged.
func transformChatCompletionRequestChecked(r *http.Request) (*http.Request, string) {
	if r.Body == nil {
		return r, ""
	}

	// Read the body
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		// Restore original body and return
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return r, ""
	}

	// Parse JSON
	var reqData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqData); err != nil {
		// Not valid JSON, restore and return
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return r, ""
	}
	if reject := chatRequestRejection(reqData); reject != "" {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return r, reject
	}

	modified := false
	// n of 1 is the only count the gateway serves, and newer OpenAI models
	// reject an explicit n even when it is 1, so it is dropped here.
	if _, ok := reqData["n"]; ok {
		delete(reqData, "n")
		modified = true
	}

	// Transform messages: convert string content to array of ContentPart objects
	if messages, ok := reqData["messages"].([]interface{}); ok {
		for i, msg := range messages {
			msgMap, ok := msg.(map[string]interface{})
			if !ok {
				continue
			}

			// Transform role from OpenAI format to proto enum format
			if role, ok := msgMap["role"].(string); ok {
				protoRole := mapRoleToProtoEnum(role)
				if protoRole != role {
					msgMap["role"] = protoRole
					modified = true
				}
			}

			// `provider_content` is an Everstack extension for replaying opaque
			// provider-native reasoning chunks. When present it is authoritative
			// because it already contains the normalized text content as native
			// provider chunks.
			providerReplayApplied := false
			if providerContent, exists := msgMap["provider_content"]; exists {
				delete(msgMap, "provider_content")
				modified = true
				if parts := providerContentToProtoParts(providerContent); len(parts) > 0 {
					msgMap["content"] = parts
					providerReplayApplied = true
				}
			}

			// Transform content: if it's a string, convert to ContentPart array.
			if content, ok := msgMap["content"].(string); ok && !providerReplayApplied {
				// Convert string content to array of ContentPart
				msgMap["content"] = []map[string]interface{}{
					{
						"type": "text",
						"text": content,
					},
				}
				modified = true
			} else if contentArr, ok := msgMap["content"].([]interface{}); ok && !providerReplayApplied {
				// Content is already an array, ensure each part has the right format
				for j, part := range contentArr {
					if partMap, ok := part.(map[string]interface{}); ok {
						if image, ok := partMap["image_url"].(map[string]interface{}); ok {
							partMap["image_url"] = image["url"]
							if detail, ok := image["detail"].(string); ok && detail != "" {
								partMap["image_detail"] = detail
							}
							modified = true
						}
						// If it's a simple text part with just "text" key, normalize it
						if _, hasType := partMap["type"]; !hasType {
							if text, hasText := partMap["text"].(string); hasText {
								partMap["type"] = "text"
								partMap["text"] = text
								modified = true
							}
						}
						contentArr[j] = partMap
					}
				}
				msgMap["content"] = contentArr
			}

			messages[i] = msgMap
		}
		reqData["messages"] = messages
	}

	// Transform tool_choice: if it's a simple string, keep it in the "mode" field
	if toolChoice, ok := reqData["tool_choice"].(string); ok {
		reqData["tool_choice"] = map[string]interface{}{
			"mode": toolChoice,
		}
		modified = true
	}

	if tc, ok := reqData["tool_choice"].(map[string]interface{}); ok {
		if tc["type"] == "function" {
			reqData["tool_choice"] = map[string]interface{}{"specific_tool": tc}
			modified = true
		}
	}
	if user, ok := reqData["user"].(string); ok {
		metadata, _ := reqData["metadata"].(map[string]interface{})
		if metadata == nil {
			metadata = map[string]interface{}{}
		}
		metadata["user"] = user
		reqData["metadata"] = metadata
		delete(reqData, "user")
		modified = true
	}
	// The protobuf request groups sampling controls under "sampling", while the
	// OpenAI-compatible surface keeps them at the top level. Move every
	// supported field by key presence rather than value so explicit zeroes such
	// as temperature=0 and top_p=0 survive the transform.
	sampling, _ := reqData["sampling"].(map[string]interface{})
	for _, key := range []string{
		"temperature",
		"top_p",
		"max_tokens",
		"max_completion_tokens",
		"stop",
		"frequency_penalty",
		"presence_penalty",
		"reasoning_effort",
		"reasoning_budget_tokens",
		"reasoning_enabled",
		"top_k",
		"seed",
		"verbosity",
		"logprobs",
		"top_logprobs",
		"logit_bias",
	} {
		value, exists := reqData[key]
		if !exists {
			continue
		}
		if sampling == nil {
			sampling = make(map[string]interface{})
		}
		if key == "stop" {
			if scalar, ok := value.(string); ok {
				value = []string{scalar}
			}
		}
		sampling[key] = value
		delete(reqData, key)
		modified = true
	}
	if sampling != nil {
		reqData["sampling"] = sampling
	}

	// On the OpenAI-compatible surface an absent `stream` means false. Make
	// that explicit: the proto field is optional and the handler would
	// otherwise apply the instance streaming default to the upstream call.
	if openAIStreamDefault(r.Context()) {
		if v, present := reqData["stream"]; !present || v == nil {
			reqData["stream"] = false
			modified = true
		}
	}

	if modified {
		// Re-serialize
		newBody, err := json.Marshal(reqData)
		if err == nil {
			bodyBytes = newBody
		}
	}

	// Restore body with possibly modified content
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))
	return r, ""
}

// mapRoleToProtoEnum converts OpenAI role strings to proto enum format
// transformResponsesRequest accepts the OpenAI Responses request shapes that
// proto-JSON cannot express, so an unmodified OpenAI SDK can call
// POST /v1/responses:
//
//   - input as a string: one user message;
//   - an input message without a type (the SDKs' "easy" message
//     {"role": "user", "content": "..."}): type "message";
//   - an OpenAI role name (user, assistant, system, developer; developer is a
//     system message): its proto enum name;
//   - content as a string: one text part;
//   - input_text and output_text parts: text parts.
//
// Anything already in proto shape, and every other input item, is left as
// it is. instructions is a plain string field and needs nothing.
func transformResponsesRequest(r *http.Request) *http.Request {
	if r.Body == nil {
		return r
	}
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	restore := func(b []byte) *http.Request {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
		return r
	}
	if err != nil {
		return restore(bodyBytes)
	}
	var reqData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqData); err != nil {
		return restore(bodyBytes)
	}
	modified := false
	switch input := reqData["input"].(type) {
	case string:
		reqData["input"] = []interface{}{map[string]interface{}{
			"type":    "message",
			"role":    "ROLE_USER",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": input}},
		}}
		modified = true
	case []interface{}:
		for _, raw := range input {
			if item, ok := raw.(map[string]interface{}); ok && normalizeResponsesInputItem(item) {
				modified = true
			}
		}
	}
	if !modified {
		return restore(bodyBytes)
	}
	out, err := json.Marshal(reqData)
	if err != nil {
		return restore(bodyBytes)
	}
	return restore(out)
}

// normalizeResponsesInputItem rewrites one OpenAI input message in place
// into the proto shape and reports whether it changed anything.
func normalizeResponsesInputItem(item map[string]interface{}) bool {
	typ, hasType := item["type"].(string)
	role, hasRole := item["role"].(string)
	if !hasType && !hasRole {
		return false
	}
	if hasType && typ != "message" {
		return false
	}
	changed := false
	if !hasType {
		item["type"] = "message"
		changed = true
	}
	if hasRole {
		mapped := mapRoleToProtoEnum(role)
		if strings.EqualFold(role, "developer") {
			mapped = "ROLE_SYSTEM"
		}
		if mapped != role {
			item["role"] = mapped
			changed = true
		}
	}
	switch content := item["content"].(type) {
	case string:
		item["content"] = []interface{}{map[string]interface{}{"type": "text", "text": content}}
		changed = true
	case []interface{}:
		for i, raw := range content {
			part, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			switch part["type"] {
			case "input_text", "output_text":
				text, _ := part["text"].(string)
				content[i] = map[string]interface{}{"type": "text", "text": text}
				changed = true
			}
		}
	}
	return changed
}

func mapRoleToProtoEnum(role string) string {
	switch strings.ToLower(role) {
	case "system", "developer":
		// developer is OpenAI's successor to system ("with o1 models and
		// newer, developer messages replace the previous system messages"),
		// so it carries system-level priority to every provider.
		return "ROLE_SYSTEM"
	case "user":
		return "ROLE_USER"
	case "assistant":
		return "ROLE_ASSISTANT"
	case "function":
		return "ROLE_FUNCTION"
	case "tool":
		return "ROLE_TOOL"
	default:
		return role
	}
}

// transformEmbeddingsRequest transforms an embeddings request to extract text from messages
// if the input field is empty. This provides consistency with the ChatCompletion API format.
// Also extracts model from messages if not at root level.
func transformEmbeddingsRequest(r *http.Request) *http.Request {
	if r.Body == nil {
		return r
	}

	// Read the body
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		// Restore original body and return
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return r
	}

	// Parse JSON to check for messages
	var reqData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqData); err != nil {
		// Not valid JSON, restore and return
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return r
	}

	// Check if input is empty/missing and messages exist
	input, hasInput := reqData["input"].(string)
	messages, hasMessages := reqData["messages"].([]interface{})
	rootModel, hasRootModel := reqData["model"].(string)

	modified := false

	if hasMessages && len(messages) > 0 {
		// Extract text from messages (concatenate all user message content)
		var textParts []string
		var extractedModel string

		for _, msg := range messages {
			msgMap, ok := msg.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract model from message if not at root level
			if !hasRootModel || rootModel == "" {
				if msgModel, ok := msgMap["model"].(string); ok && msgModel != "" {
					extractedModel = msgModel
				}
			}

			// Check role - prefer user messages but accept any
			role, _ := msgMap["role"].(string)
			// Accept ROLE_USER, user, ROLE_ASSISTANT, assistant, etc.
			isUserMessage := strings.Contains(strings.ToLower(role), "user")

			// Extract content
			content := msgMap["content"]
			switch c := content.(type) {
			case string:
				// Simple string content
				if isUserMessage || len(textParts) == 0 {
					textParts = append(textParts, c)
				}
			case []interface{}:
				// Array of content parts (OpenAI format)
				for _, part := range c {
					partMap, ok := part.(map[string]interface{})
					if !ok {
						continue
					}
					// Check for text type
					partType, _ := partMap["type"].(string)
					if partType == "text" {
						if text, ok := partMap["text"].(string); ok && text != "" {
							if isUserMessage || len(textParts) == 0 {
								textParts = append(textParts, text)
							}
						}
					}
				}
			}
		}

		// Set model if extracted from messages
		if extractedModel != "" && (!hasRootModel || rootModel == "") {
			reqData["model"] = extractedModel
			modified = true
		}

		// Set input if we extracted text and input was empty
		if len(textParts) > 0 && (!hasInput || input == "") {
			reqData["input"] = strings.Join(textParts, " ")
			modified = true
		}

		// Remove messages field (not needed by proto)
		if modified {
			delete(reqData, "messages")
			delete(reqData, "stream") // Also remove stream field if present
		}
	}

	if modified {
		// Re-serialize
		newBody, err := json.Marshal(reqData)
		if err == nil {
			bodyBytes = newBody
		}
	}

	// Restore body with possibly modified content
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))
	return r
}

type ValidationFunction func(ctx context.Context) error

func validate(ctx context.Context, validations []ValidationFunction) []error {
	errs := make([]error, 0)
	for _, validation := range validations {
		if err := validation(ctx); err != nil {
			// logger.WithFields("traceID", tracing.TraceIDFromCtx(ctx)).WithError(err).Error("validation failed")
			errs = append(errs, err)
		}
	}
	return errs
}

// writeSpendLimitError writes a 402 Payment Required response for spend limit exceeded
func writeSpendLimitError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "spend_limit_exceeded",
			"message": "Spend limit exceeded",
			"details": reason,
			"code":    402,
		},
	})
}

// func metricsExporter() http.Handler {
// 	exporter := metrics.GetExporter()
// 	if exporter == nil {
// 		return http.NotFoundHandler()
// 	}
// 	return exporter
// }

// requireOrgAdmin admits only a verified organization owner or admin.
func requireOrgAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !contextkeys.IsVerifiedOrgAdmin(r.Context()) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}`))
			return
		}
		next(w, r)
	}
}
