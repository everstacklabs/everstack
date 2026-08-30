package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	configgrpc "github.com/everstacklabs/everstack/internal/api/grpc/config"
	configsvc "github.com/everstacklabs/everstack/internal/config"
	"github.com/everstacklabs/everstack/internal/events"
	configpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/config/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/config/v1/configconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	service       *configsvc.Service
	runtimeConfig *RuntimeConfigServer
}

// CreateServer constructs a config service server using provided config service.
func CreateServer(svc *configsvc.Service) *Server { return &Server{service: svc} }

// CreateServerWithDB constructs a config service server with database support for runtime config.
func CreateServerWithDB(svc *configsvc.Service, db *sqlx.DB) *Server {
	return &Server{
		service:       svc,
		runtimeConfig: NewRuntimeConfigServer(db),
	}
}

// CreateServerWithSchemas constructs a config service server and loads schemas.
func CreateServerWithSchemas(schemaFiles map[string]string) (*Server, error) {
	svc := configsvc.NewService()
	if err := svc.LoadSchemasFromFiles(schemaFiles); err != nil {
		return nil, err
	}
	return &Server{service: svc}, nil
}

// CreateServerWithSchemasAndDB constructs a config service server with schemas and database support.
func CreateServerWithSchemasAndDB(schemaFiles map[string]string, db *sqlx.DB) (*Server, error) {
	svc := configsvc.NewService()
	if err := svc.LoadSchemasFromFiles(schemaFiles); err != nil {
		return nil, err
	}
	return &Server{
		service:       svc,
		runtimeConfig: NewRuntimeConfigServer(db),
	}, nil
}

// CreateServerWithDBAndEventBus constructs a config service server with database and event bus support.
// The event bus is shared with the RuntimeConfigService for hot-reload functionality.
func CreateServerWithDBAndEventBus(svc *configsvc.Service, db *sqlx.DB, eventBus events.Bus) *Server {
	return &Server{
		service:       svc,
		runtimeConfig: NewRuntimeConfigServerWithEventBus(db, eventBus),
	}
}

// CreateServerWithSchemasDBAndEventBus constructs a config service server with schemas, database, and event bus support.
func CreateServerWithSchemasDBAndEventBus(schemaFiles map[string]string, db *sqlx.DB, eventBus events.Bus) (*Server, error) {
	svc := configsvc.NewService()
	if err := svc.LoadSchemasFromFiles(schemaFiles); err != nil {
		return nil, err
	}
	return &Server{
		service:       svc,
		runtimeConfig: NewRuntimeConfigServerWithEventBus(db, eventBus),
	}, nil
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return configconnect.NewConfigServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return configpb.File_everstack_config_v1_config_service_proto
}

func (s *Server) AppName() string      { return configconnect.ConfigServiceName }
func (s *Server) MethodPrefix() string { return configconnect.ConfigServiceName }

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return configpb.RegisterConfigServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// Implement connect handlers by adapting to internal gRPC implementation shape.
// We reuse the logic from internal/api/grpc/config/service.go by invoking the
// underlying config service directly.

func (s *Server) ValidateYAML(ctx context.Context, req *connect.Request[configpb.ValidateYAMLRequest]) (*connect.Response[configpb.ValidationResult], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.ValidateYAML(ctx, &configpb.ValidateYAMLRequest{YamlConfig: req.Msg.GetYamlConfig()})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *Server) ValidateMap(ctx context.Context, req *connect.Request[configpb.ValidateMapRequest]) (*connect.Response[configpb.ValidationResult], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.ValidateMap(ctx, &configpb.ValidateMapRequest{Config: req.Msg.GetConfig()})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *Server) GetValidationStatus(ctx context.Context, req *connect.Request[configpb.GetValidationStatusRequest]) (*connect.Response[configpb.ValidationStatus], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.GetValidationStatus(ctx, &configpb.GetValidationStatusRequest{Section: req.Msg.GetSection()})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *Server) GetValidationHistory(ctx context.Context, req *connect.Request[configpb.GetValidationHistoryRequest]) (*connect.Response[configpb.ValidationHistory], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.GetValidationHistory(ctx, &configpb.GetValidationHistoryRequest{Limit: req.Msg.GetLimit(), Section: req.Msg.GetSection()})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *Server) GetSchema(ctx context.Context, req *connect.Request[configpb.GetSchemaRequest]) (*connect.Response[configpb.SchemaResponse], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.GetSchema(ctx, &configpb.GetSchemaRequest{Name: req.Msg.GetName()})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

func (s *Server) ListSchemas(ctx context.Context, req *connect.Request[configpb.ListSchemasRequest]) (*connect.Response[configpb.ListSchemasResponse], error) {
	grpcSrv := configgrpc.NewServer(s.service)
	res, err := grpcSrv.ListSchemas(ctx, &configpb.ListSchemasRequest{})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// ============================================================================
// Runtime Configuration Methods
// ============================================================================

// GetRuntimeConfig returns the full runtime configuration
func (s *Server) GetRuntimeConfig(ctx context.Context, req *connect.Request[configpb.GetRuntimeConfigRequest]) (*connect.Response[configpb.RuntimeConfig], error) {
	if s.runtimeConfig == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	return s.runtimeConfig.GetRuntimeConfig(ctx, req)
}

// UpdateRuntimeConfig updates the full runtime configuration
func (s *Server) UpdateRuntimeConfig(ctx context.Context, req *connect.Request[configpb.UpdateRuntimeConfigRequest]) (*connect.Response[configpb.RuntimeConfig], error) {
	if s.runtimeConfig == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	return s.runtimeConfig.UpdateRuntimeConfig(ctx, req)
}

// GetRuntimeConfigSection returns a specific section of the runtime configuration
func (s *Server) GetRuntimeConfigSection(ctx context.Context, req *connect.Request[configpb.GetRuntimeConfigSectionRequest]) (*connect.Response[configpb.ConfigSection], error) {
	if s.runtimeConfig == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	return s.runtimeConfig.GetRuntimeConfigSection(ctx, req)
}

// UpdateRuntimeConfigSection updates a specific section of the runtime configuration
func (s *Server) UpdateRuntimeConfigSection(ctx context.Context, req *connect.Request[configpb.UpdateRuntimeConfigSectionRequest]) (*connect.Response[configpb.ConfigSection], error) {
	if s.runtimeConfig == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	return s.runtimeConfig.UpdateRuntimeConfigSection(ctx, req)
}

// ResetRuntimeConfigSection resets a configuration section to its default values
func (s *Server) ResetRuntimeConfigSection(ctx context.Context, req *connect.Request[configpb.ResetRuntimeConfigSectionRequest]) (*connect.Response[configpb.ConfigSection], error) {
	if s.runtimeConfig == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, nil)
	}
	return s.runtimeConfig.ResetRuntimeConfigSection(ctx, req)
}
