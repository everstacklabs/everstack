package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	tracespb "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1"
	tracesconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1/tracesconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Server implements the ObservabilityService gRPC server using ConnectRPC.
// The service proto lives in traces/v1 since observability is an extension of the traces domain.
type Server struct {
	ctx context.Context
}

// CreateServerWithContext creates a new observability gRPC server with context
func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

// RegisterConnectServer registers the Connect RPC handler
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return tracesconnect.NewObservabilityServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the file descriptor for this service
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return tracespb.File_everstack_traces_v1_observability_service_proto
}

// AppName returns the service name
func (s *Server) AppName() string {
	return tracesconnect.ObservabilityServiceName
}

// MethodPrefix returns the method prefix
func (s *Server) MethodPrefix() string {
	return tracesconnect.ObservabilityServiceName
}

// RegisterGateway registers the grpc-gateway REST handler
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return tracespb.RegisterObservabilityServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}
