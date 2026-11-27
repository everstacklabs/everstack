package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1/mcpconnect"
	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
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
func (s *Server) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	return mcpconnect.NewMcpServiceHandler(s, opts...)
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

// stdioRefusedError is the single client-facing refusal for every stdio
// write or start on a gateway that serves more than one tenant: Register,
// Update and the lazy start path all return exactly this code and message,
// and so does any refusal surfacing from the mcp package below (its
// ErrStdioNotAllowed text is the message). The wording is about the
// deployment and carries no ids.
func stdioRefusedError() error {
	return connect.NewError(connect.CodeInvalidArgument, mcp.ErrStdioNotAllowed)
}

// stdioAllowed reports whether this gateway may run stdio MCP servers. A
// stdio server is a child process of the gateway with the gateway's
// privileges, so it is only acceptable where the gateway serves exactly one
// tenant. Two independent signals are consulted and either one refuses:
// the mcp package's process-wide switch (set by the embedder in managed
// mode, and the gate the registry and transport constructors enforce) and
// the SharedGatewayMode marker on the server context (the same marker
// gateway/v1 uses for instance scoping).
func (s *Server) stdioAllowed() bool {
	if !mcp.StdioAllowed() {
		return false
	}
	if s.ctx != nil {
		if shared, _ := s.ctx.Value(contextkeys.SharedGatewayMode).(bool); shared {
			return false
		}
	}
	return true
}

// checkStdioWrite is the write-path guard shared by Register and Update.
// It refuses when the request either selects the stdio transport or carries
// a stdio config (a config on a non-stdio row would still be executed the
// moment the transport is flipped), and only where stdio is disallowed.
func (s *Server) checkStdioWrite(stdioTransport, hasStdioConfig bool) error {
	if !stdioTransport && !hasStdioConfig {
		return nil
	}
	if s.stdioAllowed() {
		return nil
	}
	return stdioRefusedError()
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
