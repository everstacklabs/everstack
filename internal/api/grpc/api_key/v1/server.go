package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	api_keypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1/api_keyconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ api_keypb.ApiKeyServiceServer = (*GrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	ctx context.Context // Context containing CQRS system
}

type GrpcServer struct {
	api_keypb.UnimplementedApiKeyServiceServer
	base *Server
}

func CreateServer() *Server {
	return &Server{}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

func CreateClassicServer() api_keypb.ApiKeyServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) api_keypb.ApiKeyServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return api_keyconnect.NewApiKeyServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return api_keypb.File_everstack_api_key_v1_api_key_service_proto
}

func (s *Server) AppName() string {
	return api_keyconnect.ApiKeyServiceName
}
func (s *Server) MethodPrefix() string {
	return api_keyconnect.ApiKeyServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return api_keypb.RegisterApiKeyServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

func (g *GrpcServer) CreateApiKey(ctx context.Context, req *api_keypb.CreateApiKeyRequest) (*api_keypb.CreateApiKeyResponse, error) {
	cReq := &connect.Request[api_keypb.CreateApiKeyRequest]{Msg: req}
	resp, err := g.base.CreateApiKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteApiKey(ctx context.Context, req *api_keypb.DeleteApiKeyRequest) (*api_keypb.DeleteApiKeyResponse, error) {
	cReq := &connect.Request[api_keypb.DeleteApiKeyRequest]{Msg: req}
	resp, err := g.base.DeleteApiKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListApiKeys(ctx context.Context, req *api_keypb.ListApiKeysRequest) (*api_keypb.ListApiKeysResponse, error) {
	cReq := &connect.Request[api_keypb.ListApiKeysRequest]{Msg: req}
	resp, err := g.base.ListApiKeys(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) RegenerateApiKey(ctx context.Context, req *api_keypb.RegenerateApiKeyRequest) (*api_keypb.RegenerateApiKeyResponse, error) {
	cReq := &connect.Request[api_keypb.RegenerateApiKeyRequest]{Msg: req}
	resp, err := g.base.RegenerateApiKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateApiKey(ctx context.Context, req *api_keypb.UpdateApiKeyRequest) (*api_keypb.UpdateApiKeyResponse, error) {
	cReq := &connect.Request[api_keypb.UpdateApiKeyRequest]{Msg: req}
	resp, err := g.base.UpdateApiKey(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
