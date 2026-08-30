package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	annotationspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/annotations/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/annotations/v1/annotationsconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ annotationspb.AnnotationServiceServer = (*GrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	ctx                 context.Context // Context containing CQRS system
	db                  *sqlx.DB
	serviceInterceptors []connect.Interceptor
}

func (s *Server) SetDB(db *sqlx.DB) { s.db = db }

type GrpcServer struct {
	annotationspb.UnimplementedAnnotationServiceServer
	base *Server
}

func CreateServer() *Server {
	return &Server{}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

func CreateClassicServer() annotationspb.AnnotationServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) annotationspb.AnnotationServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// Connect server plumbing
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return annotationsconnect.NewAnnotationServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return annotationspb.File_everstack_annotations_v1_annotations_service_proto
}

func (s *Server) AppName() string {
	return annotationsconnect.AnnotationServiceName
}

func (s *Server) MethodPrefix() string {
	return annotationsconnect.AnnotationServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return annotationspb.RegisterAnnotationServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// GrpcServer wrapper methods for classic gRPC

func (g *GrpcServer) CreateQueue(ctx context.Context, req *annotationspb.CreateQueueRequest) (*annotationspb.CreateQueueResponse, error) {
	cReq := &connect.Request[annotationspb.CreateQueueRequest]{Msg: req}
	resp, err := g.base.CreateQueue(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetQueue(ctx context.Context, req *annotationspb.GetQueueRequest) (*annotationspb.GetQueueResponse, error) {
	cReq := &connect.Request[annotationspb.GetQueueRequest]{Msg: req}
	resp, err := g.base.GetQueue(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListQueues(ctx context.Context, req *annotationspb.ListQueuesRequest) (*annotationspb.ListQueuesResponse, error) {
	cReq := &connect.Request[annotationspb.ListQueuesRequest]{Msg: req}
	resp, err := g.base.ListQueues(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateQueue(ctx context.Context, req *annotationspb.UpdateQueueRequest) (*annotationspb.UpdateQueueResponse, error) {
	cReq := &connect.Request[annotationspb.UpdateQueueRequest]{Msg: req}
	resp, err := g.base.UpdateQueue(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteQueue(ctx context.Context, req *annotationspb.DeleteQueueRequest) (*annotationspb.DeleteQueueResponse, error) {
	cReq := &connect.Request[annotationspb.DeleteQueueRequest]{Msg: req}
	resp, err := g.base.DeleteQueue(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) AddItemToQueue(ctx context.Context, req *annotationspb.AddItemToQueueRequest) (*annotationspb.AddItemToQueueResponse, error) {
	cReq := &connect.Request[annotationspb.AddItemToQueueRequest]{Msg: req}
	resp, err := g.base.AddItemToQueue(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) AddItemsToQueueBatch(ctx context.Context, req *annotationspb.AddItemsToQueueBatchRequest) (*annotationspb.AddItemsToQueueBatchResponse, error) {
	cReq := &connect.Request[annotationspb.AddItemsToQueueBatchRequest]{Msg: req}
	resp, err := g.base.AddItemsToQueueBatch(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetNextItem(ctx context.Context, req *annotationspb.GetNextItemRequest) (*annotationspb.GetNextItemResponse, error) {
	cReq := &connect.Request[annotationspb.GetNextItemRequest]{Msg: req}
	resp, err := g.base.GetNextItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SubmitAnnotation(ctx context.Context, req *annotationspb.SubmitAnnotationRequest) (*annotationspb.SubmitAnnotationResponse, error) {
	cReq := &connect.Request[annotationspb.SubmitAnnotationRequest]{Msg: req}
	resp, err := g.base.SubmitAnnotation(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) SkipItem(ctx context.Context, req *annotationspb.SkipItemRequest) (*annotationspb.SkipItemResponse, error) {
	cReq := &connect.Request[annotationspb.SkipItemRequest]{Msg: req}
	resp, err := g.base.SkipItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListQueueItems(ctx context.Context, req *annotationspb.ListQueueItemsRequest) (*annotationspb.ListQueueItemsResponse, error) {
	cReq := &connect.Request[annotationspb.ListQueueItemsRequest]{Msg: req}
	resp, err := g.base.ListQueueItems(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetQueueStats(ctx context.Context, req *annotationspb.GetQueueStatsRequest) (*annotationspb.GetQueueStatsResponse, error) {
	cReq := &connect.Request[annotationspb.GetQueueStatsRequest]{Msg: req}
	resp, err := g.base.GetQueueStats(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) PopulateFromTraces(ctx context.Context, req *annotationspb.PopulateFromTracesRequest) (*annotationspb.PopulateFromTracesResponse, error) {
	cReq := &connect.Request[annotationspb.PopulateFromTracesRequest]{Msg: req}
	resp, err := g.base.PopulateFromTraces(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
