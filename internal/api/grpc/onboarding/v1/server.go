package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	onboardingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/onboarding/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/onboarding/v1/onboardingpbconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Ensure Server satisfies the ConnectServer contract from
// internal/api/grpc/server (RegisterConnectServer + FileDescriptor). The
// service is Connect-only, so no grpc-gateway/REST plumbing is wired.
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

// Server implements the OnboardingService Connect/gRPC service. It persists a
// small, tenant-scoped slice of launch-center UI state to Postgres so it
// survives a cleared browser cache or a fresh device.
type Server struct {
	ctx context.Context
	db  *sqlx.DB
}

// CreateServer creates an onboarding server without a DB. Handlers return a
// clear "not configured" error until SetDB is called.
func CreateServer(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

// SetDB wires the PostgreSQL connection used to read and upsert state.
func (s *Server) SetDB(db *sqlx.DB) {
	s.db = db
}

// RegisterConnectServer registers the ConnectRPC handler.
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return onboardingpbconnect.NewOnboardingServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the proto file descriptor.
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return onboardingpb.File_everstack_onboarding_v1_onboarding_service_proto
}

// AppName returns the service name.
func (s *Server) AppName() string {
	return onboardingpbconnect.OnboardingServiceName
}

// MethodPrefix returns the method prefix.
func (s *Server) MethodPrefix() string {
	return onboardingpbconnect.OnboardingServiceName
}
