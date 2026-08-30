package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/browserpool"
	workflowspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/workflows/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/workflows/v1/workflowsconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ workflowspb.WorkflowsServiceServer = (*GrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	ctx      context.Context       // Context containing CQRS system
	registry *gw.Registry          // Provider registry from gateway
	router   *gw.Router            // Model router from gateway
	toolLoop *toolloop.LoopManager // Tool loop for function execution
	db       *sqlx.DB              // Direct DB access for draft saves and version snapshots

	// Memory backend (optional, set via SetMemoryBackend)
	memoryStore    memory.VectorStore
	memoryEmbedder memory.EmbedderInterface
	memoryModel    string
	memoryDim      int

	// Voice clone profile repository (optional, set via SetVoiceCloneRepo)
	voiceCloneRepo voice_clone.Repository

	// Storage server for audio persistence (optional, set via SetStorageServer)
	storageServer *storagesvc.Server

	// Agent workflow runtime dependencies. These are injected after sandbox
	// startup because the workflow server is constructed earlier in start_api.
	sandboxManager *sandbox.SandboxManager
	browserPool    *browserpool.Pool
}

type GrpcServer struct {
	workflowspb.UnimplementedWorkflowsServiceServer
	base *Server
}

func CreateServer() *Server {
	return &Server{}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

func CreateServerWithDeps(ctx context.Context, registry *gw.Registry, router *gw.Router, tl *toolloop.LoopManager, db *sqlx.DB) *Server {
	return &Server{ctx: ctx, registry: registry, router: router, toolLoop: tl, db: db}
}

// SetMemoryBackend configures the optional vector memory backend for memory workflow nodes.
func (s *Server) SetMemoryBackend(store memory.VectorStore, embedder memory.EmbedderInterface, model string, dim int) {
	s.memoryStore = store
	s.memoryEmbedder = embedder
	s.memoryModel = model
	s.memoryDim = dim
}

// SetVoiceCloneRepo configures the voice clone profile repository for TTS/VoiceClone workflow nodes.
func (s *Server) SetVoiceCloneRepo(repo voice_clone.Repository) {
	s.voiceCloneRepo = repo
}

// SetStorageServer configures the storage server for audio persistence in TTS/VoiceClone workflow nodes.
func (s *Server) SetStorageServer(ss *storagesvc.Server) {
	s.storageServer = ss
}

// SetSandboxManager enables sandbox-backed synthetic tools for agent nodes.
func (s *Server) SetSandboxManager(manager *sandbox.SandboxManager) {
	s.sandboxManager = manager
}

// SetBrowserPool enables tenant-isolated hosted browsers for agent nodes.
func (s *Server) SetBrowserPool(pool *browserpool.Pool) {
	s.browserPool = pool
}

func CreateClassicServer() workflowspb.WorkflowsServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) workflowspb.WorkflowsServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return workflowsconnect.NewWorkflowsServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return workflowspb.File_everstack_workflows_v1_workflows_service_proto
}

func (s *Server) AppName() string {
	return workflowsconnect.WorkflowsServiceName
}

func (s *Server) MethodPrefix() string {
	return workflowsconnect.WorkflowsServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return workflowspb.RegisterWorkflowsServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// GrpcServer wrapper methods for classic gRPC
func (g *GrpcServer) CreateWorkflow(ctx context.Context, req *workflowspb.CreateWorkflowRequest) (*workflowspb.CreateWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.CreateWorkflowRequest]{Msg: req}
	resp, err := g.base.CreateWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetWorkflow(ctx context.Context, req *workflowspb.GetWorkflowRequest) (*workflowspb.GetWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.GetWorkflowRequest]{Msg: req}
	resp, err := g.base.GetWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListWorkflows(ctx context.Context, req *workflowspb.ListWorkflowsRequest) (*workflowspb.ListWorkflowsResponse, error) {
	cReq := &connect.Request[workflowspb.ListWorkflowsRequest]{Msg: req}
	resp, err := g.base.ListWorkflows(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateWorkflow(ctx context.Context, req *workflowspb.UpdateWorkflowRequest) (*workflowspb.UpdateWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.UpdateWorkflowRequest]{Msg: req}
	resp, err := g.base.UpdateWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteWorkflow(ctx context.Context, req *workflowspb.DeleteWorkflowRequest) (*workflowspb.DeleteWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.DeleteWorkflowRequest]{Msg: req}
	resp, err := g.base.DeleteWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetWorkflowVersionHistory(ctx context.Context, req *workflowspb.GetWorkflowVersionHistoryRequest) (*workflowspb.GetWorkflowVersionHistoryResponse, error) {
	cReq := &connect.Request[workflowspb.GetWorkflowVersionHistoryRequest]{Msg: req}
	resp, err := g.base.GetWorkflowVersionHistory(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetWorkflowAtVersion(ctx context.Context, req *workflowspb.GetWorkflowAtVersionRequest) (*workflowspb.GetWorkflowAtVersionResponse, error) {
	cReq := &connect.Request[workflowspb.GetWorkflowAtVersionRequest]{Msg: req}
	resp, err := g.base.GetWorkflowAtVersion(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ExecuteWorkflow(req *workflowspb.ExecuteWorkflowRequest, stream workflowspb.WorkflowsService_ExecuteWorkflowServer) error {
	cReq := &connect.Request[workflowspb.ExecuteWorkflowRequest]{Msg: req}
	adapter := &grpcStreamSenderAdapter{stream: stream}
	return g.base.executeWorkflowInternal(stream.Context(), cReq, adapter, "manual")
}

func (g *GrpcServer) SaveWorkflowDraft(ctx context.Context, req *workflowspb.SaveWorkflowDraftRequest) (*workflowspb.SaveWorkflowDraftResponse, error) {
	cReq := &connect.Request[workflowspb.SaveWorkflowDraftRequest]{Msg: req}
	resp, err := g.base.SaveWorkflowDraft(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) PublishWorkflow(ctx context.Context, req *workflowspb.PublishWorkflowRequest) (*workflowspb.PublishWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.PublishWorkflowRequest]{Msg: req}
	resp, err := g.base.PublishWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UnpublishWorkflow(ctx context.Context, req *workflowspb.UnpublishWorkflowRequest) (*workflowspb.UnpublishWorkflowResponse, error) {
	cReq := &connect.Request[workflowspb.UnpublishWorkflowRequest]{Msg: req}
	resp, err := g.base.UnpublishWorkflow(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListWorkflowExecutions(ctx context.Context, req *workflowspb.ListWorkflowExecutionsRequest) (*workflowspb.ListWorkflowExecutionsResponse, error) {
	cReq := &connect.Request[workflowspb.ListWorkflowExecutionsRequest]{Msg: req}
	resp, err := g.base.ListWorkflowExecutions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetWorkflowExecution(ctx context.Context, req *workflowspb.GetWorkflowExecutionRequest) (*workflowspb.GetWorkflowExecutionResponse, error) {
	cReq := &connect.Request[workflowspb.GetWorkflowExecutionRequest]{Msg: req}
	resp, err := g.base.GetWorkflowExecution(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ReplayWorkflowExecution(req *workflowspb.ReplayWorkflowExecutionRequest, stream workflowspb.WorkflowsService_ReplayWorkflowExecutionServer) error {
	cReq := &connect.Request[workflowspb.ReplayWorkflowExecutionRequest]{Msg: req}
	adapter := &grpcStreamSenderAdapter{stream: stream}
	return g.base.replayWorkflowExecutionInternal(stream.Context(), cReq, adapter)
}

// grpcStreamSenderAdapter adapts a classic gRPC server stream to the streamSender interface.
type grpcStreamSenderAdapter struct {
	stream interface {
		Send(*workflowspb.ExecuteWorkflowEvent) error
	}
}

func (a *grpcStreamSenderAdapter) Send(msg *workflowspb.ExecuteWorkflowEvent) error {
	return a.stream.Send(msg)
}
