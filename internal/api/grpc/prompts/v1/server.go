package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	promptspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/prompts/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/prompts/v1/promptsconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*PromptServer)(nil)

// PromptServer handles Prompt + PromptVersion RPCs (Connect)
type PromptServer struct {
	ctx                 context.Context
	db                  *sqlx.DB
	serviceInterceptors []connect.Interceptor
}

func (s *PromptServer) SetDB(db *sqlx.DB) { s.db = db }

// --- Constructors ---

func CreatePromptServer() *PromptServer {
	return &PromptServer{}
}

func CreatePromptServerWithContext(ctx context.Context) *PromptServer {
	return &PromptServer{ctx: ctx}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *PromptServer) WithInterceptors(interceptors ...connect.Interceptor) *PromptServer {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// --- Connect plumbing ---

func (s *PromptServer) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return promptsconnect.NewPromptServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *PromptServer) FileDescriptor() protoreflect.FileDescriptor {
	return promptspb.File_everstack_prompts_v1_prompts_service_proto
}

func (s *PromptServer) AppName() string {
	return promptsconnect.PromptServiceName
}

func (s *PromptServer) MethodPrefix() string {
	return promptsconnect.PromptServiceName
}

func (s *PromptServer) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return promptspb.RegisterPromptServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}
