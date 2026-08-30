package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	health "github.com/everstacklabs/everstack/pkg/grpc/everstack/health/v1"
	healthconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/health/v1/healthconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ healthconnect.HealthServiceHandler = (*Server)(nil)

type Server struct {
	// ListActionFunctions func() []string
	ListGRPCMethods  func() []string
	ListGRPCServices func() []string
}

func CreateServer(
	// listActionFunctions func() []string,
	listGRPCMethods func() []string,
	listGRPCServices func() []string,
) *Server {
	return &Server{
		// ListActionFunctions: listActionFunctions,
		ListGRPCMethods:  listGRPCMethods,
		ListGRPCServices: listGRPCServices,
	}
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return healthconnect.NewHealthServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return health.File_everstack_health_v1_health_service_proto
}

func (s *Server) AppName() string {
	return health.HealthService_ServiceDesc.ServiceName
}

func (s *Server) MethodPrefix() string {
	return health.HealthService_ServiceDesc.ServiceName
}

// RegisterGateway allows the Health service to self-register its grpc-gateway handlers.
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return health.RegisterHealthServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}
