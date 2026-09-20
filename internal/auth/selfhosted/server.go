package selfhosted

import (
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	grpcserver "github.com/everstacklabs/everstack/internal/api/grpc/server"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/auth/oauthserver"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/transport"
	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/authz"
	"github.com/everstacklabs/everstack/pkg/authz/authzhttp"
	authpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1/authconnect"
	orgpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1/orgconnect"
	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Config holds the external-facing configuration for the self-hosted auth server.
type Config struct {
	SessionSecret     string
	SessionCookieName string
	SessionMaxAge     int
	SessionSecure     bool
	SessionHTTPOnly   bool
	SessionSameSite   string
	ExternalURL       string
	DeviceTokens      *deviceauth.TokenManager

	// EmailSender delivers magic-link and invitation mail. Nil in builds with
	// no email provider configured, in which case those flows fall back to
	// logging the token. Injected rather than imported: see service.EmailSender.
	EmailSender service.EmailSender
}

// Server implements grpcserver.ConnectServer for self-hosted auth.
type Server struct {
	handler           authconnect.AuthServiceHandler
	selfHostedHandler *transport.SelfHostedAuthHandler
	orgServer         *OrgServer
	seatLimit         int
	limiter           *authratelimit.AuthRateLimiter
}

// OrgServer implements grpcserver.ConnectServer for the OrganizationService.
type OrgServer struct {
	handler orgconnect.OrganizationServiceHandler
}

var _ grpcserver.ConnectServer = (*OrgServer)(nil)

func (s *OrgServer) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	return orgconnect.NewOrganizationServiceHandler(s.handler, opts...)
}

func (s *OrgServer) FileDescriptor() protoreflect.FileDescriptor {
	return orgpb.File_everstack_org_v1_org_service_proto
}

func (s *OrgServer) AppName() string      { return orgconnect.OrganizationServiceName }
func (s *OrgServer) MethodPrefix() string { return orgconnect.OrganizationServiceName }

// Ensure Server implements the ConnectServer interface at compile time.
var _ grpcserver.ConnectServer = (*Server)(nil)

// CreateServer constructs a self-hosted auth server.
func CreateServer(db *sqlx.DB, cfg *Config, seatLimit int) (*Server, error) {
	if db == nil {
		return nil, nil // Auth not configured
	}
	if cfg == nil {
		cfg = &Config{}
	}

	// Convert to internal config
	internalCfg := &InternalConfig{
		Session: SessionConfig{
			Secret:     cfg.SessionSecret,
			CookieName: cfg.SessionCookieName,
			MaxAge:     time.Duration(cfg.SessionMaxAge) * time.Second,
			Secure:     cfg.SessionSecure,
			HTTPOnly:   cfg.SessionHTTPOnly,
			SameSite:   cfg.SessionSameSite,
		},
	}

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	orgRepo := repository.NewOrganizationRepository(db)
	credentialsRepo := repository.NewCredentialsRepository(db)
	invitationRepo := repository.NewInvitationRepository(db)
	magicLinkRepo := repository.NewMagicLinkRepository(db)
	authConfigRepo := repository.NewAuthConfigRepository(db)
	deviceAuthRepo := deviceauth.NewRepository(db)

	// Create self-hosted service
	selfHostedSvc := service.NewSelfHostedAuthService(
		internalCfg, userRepo, sessionRepo, credentialsRepo, invitationRepo, magicLinkRepo, orgRepo, authConfigRepo,
	).WithEmail(cfg.EmailSender, cfg.ExternalURL)

	// Build the frontend PDP against the same relation tuples used by the
	// gateway PEP and resource recorder.
	var batchCheck *authzhttp.Handler
	if v := os.Getenv("EVS_AUTHZ_ENABLED"); v == "true" || v == "1" {
		store := authz.NewPostgresStore(db, "relation_tuples")
		engine := authz.NewEngine(
			authz.NewBridgeStore(store),
			authz.EverstackSchema().WithResourceTypes(authz.DefaultResourceTypes()...),
		)
		batchCheck = authzhttp.New(
			engine,
			contextkeys.GetUserID,
			contextkeys.GetTenantID,
			contextkeys.GetUserRole,
		)
	}

	// Create organization service
	orgSvc := service.NewOrganizationService(orgRepo, authConfigRepo)
	deviceTokens := cfg.DeviceTokens
	if deviceTokens == nil {
		var tokenErr error
		deviceTokens, tokenErr = deviceauth.NewTokenManager([]byte(cfg.SessionSecret), 90*24*time.Hour)
		if tokenErr != nil {
			logger.WithError(tokenErr).Warn("auth: device authorization disabled because token signing is not configured securely")
		}
	}
	selfHostedHandler := transport.NewSelfHostedAuthHandler(
		internalCfg,
		selfHostedSvc,
		seatLimit,
		batchCheck,
		transport.DeviceAuthorizationDependencies{
			Store:         deviceAuthRepo,
			Organizations: orgSvc,
			Tokens:        deviceTokens,
			ExternalURL:   cfg.ExternalURL,
		},
	)
	selfHostedHandler.ConfigureOAuth(oauthserver.NewPostgresStore(db), deviceTokens)
	orgHandler := transport.NewOrgHandler(internalCfg, selfHostedSvc.AuthService, orgSvc)

	logger.Info("auth: server created in SELF-HOSTED mode")

	return &Server{
		handler:           selfHostedHandler,
		selfHostedHandler: selfHostedHandler,
		orgServer:         &OrgServer{handler: orgHandler},
		seatLimit:         seatLimit,
		// Shared with the HTTP routes on purpose: one budget per caller
		// across both surfaces, not one each.
		limiter: selfHostedHandler.Limiter(),
	}, nil
}

// OrgService returns the OrganizationService ConnectServer for separate registration.
func (s *Server) OrgService() *OrgServer {
	return s.orgServer
}

// RegisterConnectServer returns the path and handler for Connect RPC.
func (s *Server) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	path, handler := authconnect.NewAuthServiceHandler(s.handler, opts...)
	// The Connect surface carries the same credential operations as the HTTP
	// routes, and on instance hosts it is the only way to reach some of them:
	// RequestMagicLink has no HTTP route, so limiting the routes alone would
	// leave the endpoint that sends mail wide open. The gateway's own
	// tenant-aware limiter cannot cover this, because a request arriving here
	// has no tenant yet.
	if s.limiter != nil {
		handler = s.limiter.WrapSensitive(handler)
	}
	return path, handler
}

// FileDescriptor returns the protobuf file descriptor.
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return authpb.File_everstack_auth_v1_auth_service_proto
}

// AppName returns the service name.
func (s *Server) AppName() string { return authconnect.AuthServiceName }

// MethodPrefix returns the service prefix for routing.
func (s *Server) MethodPrefix() string { return authconnect.AuthServiceName }

// RegisterHTTPRoutes registers HTTP endpoints for login/register on the given router.
func (s *Server) RegisterHTTPRoutes(router *mux.Router) {
	if s.selfHostedHandler != nil {
		s.selfHostedHandler.RegisterHTTPRoutes(router)
		logger.Info("auth: HTTP routes registered for self-hosted mode")
	}
}
