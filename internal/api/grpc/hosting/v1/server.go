// Package v1 implements everstack.hosting.v1.SitesService, the control
// plane for evs.run instant static hosting (docs/design/evs-run-hosting.md).
//
// The publish, claim, sign-in, and report routes are bypassed in the outer
// auth policy. Their handlers enforce their own public-surface rules and rate
// limits; anonymous publishing remains disabled unless Config.AllowAnonymous
// is explicitly enabled. Anonymous requests carry no tenant in context and
// MUST take the explicit anonymous branch; no handler may fall through to an
// unscoped tenant query.
package v1

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/everstacklabs/everstack/internal/hosting"
	"github.com/everstacklabs/everstack/internal/hosting/moderation"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storage"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1/hostingconnect"
)

var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

// Config carries the deployment-specific settings for the hosting service.
type Config struct {
	// Bucket is the platform sites bucket (e.g. "everstack-sites").
	Bucket string
	// BaseDomain is the serving domain, e.g. "evs.run".
	BaseDomain string
	// ClaimBaseURL is the page that finishes a claim, e.g. "https://evs.run/claim".
	ClaimBaseURL string
	// AllowAnonymous enables zero-credential PublishSite calls. It defaults to
	// false so public exposure is an explicit operational decision.
	AllowAnonymous bool
	// ProxyToken authenticates the evs.run apex Worker when it forwards the
	// original client IP in X-EVS-Client-IP. Without it, forwarding headers
	// supplied directly by callers are never trusted as Cloudflare identity.
	ProxyToken string
}

type Server struct {
	hostingconnect.UnimplementedSitesServiceHandler

	ctx    context.Context
	db     *sqlx.DB
	store  storage.ObjectStore
	cfg    Config
	purger hosting.Purger

	// Per-IP limiters for the anonymous surface. Enforced in-handler so
	// every transport (Connect, gRPC, REST gateway) is covered. The global
	// limiters backstop them against client-IP header rotation.
	publishLimiter *hosting.IPLimiter
	codeLimiter    *hosting.IPLimiter
	globalPublish  *hosting.GlobalLimiter
	globalCode     *hosting.GlobalLimiter
	reportLimiter  *hosting.IPLimiter
	globalReport   *hosting.GlobalLimiter
	reporter       *moderation.Reporter
	quotaResolver  hosting.QuotaResolver
	edgeConfigured bool

	// issueKey mints a permanent API key for a claimed org; wired from
	// the api_key command stack at startup (see auth.go).
	issueKey KeyIssuer
	// sendCode delivers a one-time claim/sign-in code by email.
	sendCode CodeSender
	// provisionOwner finds or creates the user+org for a verified email.
	provisionOwner OwnerProvisioner
}

// KeyIssuer mints a permanent API key for an org and returns the raw key.
type KeyIssuer func(ctx context.Context, orgID, name string) (string, error)

// CodeSender delivers a one-time code to an email address.
type CodeSender func(ctx context.Context, email, code string) error

// OwnerProvisioner resolves a verified email to (userID, orgID), creating
// both when they do not exist yet.
type OwnerProvisioner func(ctx context.Context, email string) (userID, orgID string, err error)

func CreateServerWithDeps(ctx context.Context, db *sqlx.DB, store storage.ObjectStore, cfg Config) *Server {
	if cfg.BaseDomain == "" {
		cfg.BaseDomain = "evs.run"
	}
	if cfg.ClaimBaseURL == "" {
		cfg.ClaimBaseURL = "https://" + cfg.BaseDomain + "/claim"
	}
	server := &Server{
		ctx:            ctx,
		db:             db,
		store:          store,
		cfg:            cfg,
		purger:         hosting.NoopPurger{},
		publishLimiter: hosting.NewIPLimiter(10, 10),      // ~10 publishes/min/IP
		codeLimiter:    hosting.NewIPLimiter(3, 3),        // ~3 code requests/min/IP
		globalPublish:  hosting.NewGlobalLimiter(300, 60), // all anonymous publish traffic combined
		globalCode:     hosting.NewGlobalLimiter(60, 20),  // all code emails combined
		reportLimiter:  hosting.NewIPLimiter(0.1, 3),      // roughly 6 reports/hour/IP
		globalReport:   hosting.NewGlobalLimiter(10, 30),  // roughly 600 reports/hour globally
	}
	if db != nil {
		moderationStore := moderation.NewPostgresStore(db)
		server.reporter = moderation.NewReporter(moderationStore)
	}
	return server
}

// SetPurger wires an edge-cache purger (Cloudflare in cloud deployments).
func (s *Server) SetPurger(p hosting.Purger) {
	if p != nil {
		s.purger = p
	}
}

// SetReporter replaces the report intake implementation. Production uses the
// Postgres adapter; tests use an in-memory adapter at the same seam.
func (s *Server) SetReporter(reporter *moderation.Reporter) { s.reporter = reporter }

func (s *Server) SetEdgeEnforcementConfigured(configured bool) {
	s.edgeConfigured = configured
}

// SetQuotaResolver enables plan-level site-count and retained-storage
// enforcement for authenticated tenants. Anonymous publishes keep their
// separate TTL, size, and rate-limit policy.
func (s *Server) SetQuotaResolver(resolver hosting.QuotaResolver) {
	s.quotaResolver = resolver
}

// SetKeyIssuer wires API-key minting for the claim flow.
func (s *Server) SetKeyIssuer(f KeyIssuer) { s.issueKey = f }

// SetCodeSender wires email delivery for one-time codes.
func (s *Server) SetCodeSender(f CodeSender) { s.sendCode = f }

// SetOwnerProvisioner wires user+org find-or-create for the claim flow.
func (s *Server) SetOwnerProvisioner(f OwnerProvisioner) { s.provisionOwner = f }

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return hostingconnect.NewSitesServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return hostingpb.File_everstack_hosting_v1_hosting_service_proto
}

func (s *Server) AppName() string {
	return hostingconnect.SitesServiceName
}

func (s *Server) MethodPrefix() string {
	return hostingconnect.SitesServiceName
}

func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return hostingpb.RegisterSitesServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// tenantID returns the tenant of an AUTHENTICATED caller, or "" when the
// request is anonymous. It deliberately requires the authenticated marker
// (set only by credential validation) rather than trusting a bare tenant id
// in context: on standalone self-hosted gateways the LocalTenantInterceptor
// injects a default tenant id AFTER our anonymous auth bypass, so keying off
// GetTenantID alone would let an unauthenticated caller act as the local
// tenant (republish its slugs, skip anonymous limits). Request-body tenant
// ids are never trusted either (2026-05-06 incident).
func (s *Server) tenantID(ctx context.Context) string {
	if !contextkeys.IsTenantAuthenticated(ctx) {
		return ""
	}
	return contextkeys.GetTenantID(ctx)
}

// clientIP extracts the caller IP for rate limiting. Only the apex Worker can
// supply the original address, authenticated with a deployment secret. Direct
// callers cannot rotate buckets by spoofing CF-Connecting-IP. Otherwise use
// the last X-Forwarded-For hop appended by our ingress, then the raw peer.
func clientIP[T any](s *Server, req *connect.Request[T]) string {
	proxyToken := strings.TrimSpace(s.cfg.ProxyToken)
	presentedToken := strings.TrimSpace(req.Header().Get("X-EVS-Proxy-Token"))
	if proxyToken != "" && len(presentedToken) == len(proxyToken) &&
		subtle.ConstantTimeCompare([]byte(presentedToken), []byte(proxyToken)) == 1 {
		if forwarded := canonicalClientIP(req.Header().Get("X-EVS-Client-IP")); forwarded != "" {
			return forwarded
		}
	}
	peer := canonicalClientIP(req.Peer().Addr)
	peerIP := net.ParseIP(peer)
	if peerIP != nil && (peerIP.IsPrivate() || peerIP.IsLoopback()) {
		xff := req.Header().Get("X-Forwarded-For")
		parts := strings.Split(xff, ",")
		if forwarded := canonicalClientIP(parts[len(parts)-1]); forwarded != "" {
			return forwarded
		}
	}
	return peer
}

func canonicalClientIP(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if zone := strings.LastIndex(value, "%"); zone != -1 {
		value = value[:zone]
	}
	if parsed := net.ParseIP(value); parsed != nil {
		return parsed.String()
	}
	return ""
}

func (s *Server) siteURL(slug string) string {
	return "https://" + slug + "." + s.cfg.BaseDomain
}
