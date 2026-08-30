package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1/mcpconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ mcppb.McpServiceServer = (*GrpcServer)(nil)

// Server implements the McpService Connect/gRPC service.
type Server struct {
	ctx      context.Context
	db       *sqlx.DB
	registry *mcp.Registry
	health   *mcp.HealthChecker
}

// GrpcServer wraps Server for classic gRPC compatibility.
type GrpcServer struct {
	mcppb.UnimplementedMcpServiceServer
	base *Server
}

// CreateServer creates a new MCP gateway server.
func CreateServer(ctx context.Context, db *sqlx.DB, registry *mcp.Registry, health *mcp.HealthChecker) *Server {
	return &Server{
		ctx:      ctx,
		db:       db,
		registry: registry,
		health:   health,
	}
}

// RegisterConnectServer returns the Connect handler path and handler.
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return mcpconnect.NewMcpServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the proto file descriptor for reflection.
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return mcppb.File_everstack_mcp_v1_mcp_service_proto
}

// AppName returns the service name.
func (s *Server) AppName() string {
	return mcpconnect.McpServiceName
}

// MethodPrefix returns the Connect method prefix.
func (s *Server) MethodPrefix() string {
	return mcpconnect.McpServiceName
}

// RegisterGateway registers REST endpoints on the grpc-gateway mux.
func (s *Server) RegisterGateway(_ context.Context, mux *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	return mcppb.RegisterMcpServiceHandlerServer(context.Background(), mux, &GrpcServer{base: s})
}

// resolveTenantID returns the tenant id set by the auth middleware. The
// requestTenantID parameter is intentionally ignored — accepting it would
// let any caller read another tenant's MCP servers, which is the
// cross-tenant leak class this rewrite closes. The argument stays on the
// signature so callers compile but is never consulted. The previous
// "first organization in the database" fallback has been removed because in
// any multi-tenant deployment it returned a stranger's id whenever the auth
// context was empty for any reason.
func (s *Server) resolveTenantID(ctx context.Context, _ string) (string, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid, nil
	}
	if tid := contextkeys.ExtractTenantID(ctx); tid != "" {
		return tid, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// cqrsSystem extracts the CQRS system from the provided context.
func (s *Server) cqrsSystem(ctx context.Context) *cqrs.System {
	if sys, err := cqrs.GetSystemFromContext(ctx); err == nil && sys != nil {
		return sys
	}
	if s.ctx != nil {
		if sys, err := cqrs.GetSystemFromContext(s.ctx); err == nil && sys != nil {
			return sys
		}
	}
	return nil
}

// publishEvent is a helper that creates a database.Event and publishes it
// to the CQRS event bus. The payload map is JSON-encoded as the event payload.
func (s *Server) publishEvent(ctx context.Context, eventType string, payload map[string]interface{}) {
	sys := s.cqrsSystem(ctx)
	if sys == nil || sys.EventBus == nil {
		return
	}
	data, _ := json.Marshal(payload)
	event := database.NewEvent(uuid.New().String(), eventType, "mcp", data, time.Now().UnixMilli())
	_ = sys.EventBus.Publish(ctx, event)
}
