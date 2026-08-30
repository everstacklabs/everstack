package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	functionspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1/functionsconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ functionspb.FunctionsServiceServer = (*GrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	ctx context.Context // Context containing CQRS system
}

type GrpcServer struct {
	functionspb.UnimplementedFunctionsServiceServer
	base *Server
}

func CreateServer() *Server {
	return &Server{}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

func CreateClassicServer() functionspb.FunctionsServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) functionspb.FunctionsServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return functionsconnect.NewFunctionsServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return functionspb.File_everstack_functions_v1_functions_service_proto
}

func (s *Server) AppName() string {
	return functionsconnect.FunctionsServiceName
}

func (s *Server) MethodPrefix() string {
	return functionsconnect.FunctionsServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return functionspb.RegisterFunctionsServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// GrpcServer wrapper methods for classic gRPC
func (g *GrpcServer) GetIsolationStatus(ctx context.Context, req *functionspb.GetIsolationStatusRequest) (*functionspb.GetIsolationStatusResponse, error) {
	cReq := &connect.Request[functionspb.GetIsolationStatusRequest]{Msg: req}
	resp, err := g.base.GetIsolationStatus(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CreateFunction(ctx context.Context, req *functionspb.CreateFunctionRequest) (*functionspb.CreateFunctionResponse, error) {
	cReq := &connect.Request[functionspb.CreateFunctionRequest]{Msg: req}
	resp, err := g.base.CreateFunction(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetFunction(ctx context.Context, req *functionspb.GetFunctionRequest) (*functionspb.GetFunctionResponse, error) {
	cReq := &connect.Request[functionspb.GetFunctionRequest]{Msg: req}
	resp, err := g.base.GetFunction(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetFunctionByName(ctx context.Context, req *functionspb.GetFunctionByNameRequest) (*functionspb.GetFunctionByNameResponse, error) {
	cReq := &connect.Request[functionspb.GetFunctionByNameRequest]{Msg: req}
	resp, err := g.base.GetFunctionByName(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListFunctions(ctx context.Context, req *functionspb.ListFunctionsRequest) (*functionspb.ListFunctionsResponse, error) {
	cReq := &connect.Request[functionspb.ListFunctionsRequest]{Msg: req}
	resp, err := g.base.ListFunctions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateFunction(ctx context.Context, req *functionspb.UpdateFunctionRequest) (*functionspb.UpdateFunctionResponse, error) {
	cReq := &connect.Request[functionspb.UpdateFunctionRequest]{Msg: req}
	resp, err := g.base.UpdateFunction(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteFunction(ctx context.Context, req *functionspb.DeleteFunctionRequest) (*functionspb.DeleteFunctionResponse, error) {
	cReq := &connect.Request[functionspb.DeleteFunctionRequest]{Msg: req}
	resp, err := g.base.DeleteFunction(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
