package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1/datasetsconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ datasetspb.DatasetServiceServer = (*DatasetGrpcServer)(nil)
var _ datasetspb.EvalServiceServer = (*EvalGrpcServer)(nil)

// Ensure Server implements ConnectServer contract from internal/api/grpc/server
var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*DatasetServer)(nil)

var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*EvalServer)(nil)

// DatasetServer handles Dataset + DatasetItem + ScoreConfig RPCs (Connect)
type DatasetServer struct {
	ctx                 context.Context
	db                  *sqlx.DB
	serviceInterceptors []connect.Interceptor
}

// EvalServer handles EvalRun RPCs (Connect)
type EvalServer struct {
	ctx                 context.Context
	db                  *sqlx.DB
	serviceInterceptors []connect.Interceptor
	// samplingRunner is optional; set via SetSamplingRunner from start_api
	// once the eval_runner.Runner is constructed. Required for
	// RunSamplingEvalRuleNow to actually execute (without it, the call
	// returns a configuration error).
	samplingRunner *eval_runner.Runner
}

// SetSamplingRunner wires the eval runner so RunSamplingEvalRuleNow can
// execute. Called from start_api once the runner is built.
func (s *EvalServer) SetSamplingRunner(r *eval_runner.Runner) {
	s.samplingRunner = r
}

func (s *DatasetServer) SetDB(db *sqlx.DB) { s.db = db }
func (s *EvalServer) SetDB(db *sqlx.DB)    { s.db = db }

// DatasetGrpcServer wraps DatasetServer for classic gRPC
type DatasetGrpcServer struct {
	datasetspb.UnimplementedDatasetServiceServer
	base *DatasetServer
}

// EvalGrpcServer wraps EvalServer for classic gRPC
type EvalGrpcServer struct {
	datasetspb.UnimplementedEvalServiceServer
	base *EvalServer
}

// --- Constructors ---

func CreateDatasetServer() *DatasetServer {
	return &DatasetServer{}
}

func CreateDatasetServerWithContext(ctx context.Context) *DatasetServer {
	return &DatasetServer{ctx: ctx}
}

func CreateEvalServer() *EvalServer {
	return &EvalServer{}
}

func CreateEvalServerWithContext(ctx context.Context) *EvalServer {
	return &EvalServer{ctx: ctx}
}

func CreateClassicDatasetServer() datasetspb.DatasetServiceServer {
	return &DatasetGrpcServer{base: CreateDatasetServer()}
}

func CreateClassicDatasetServerWithContext(ctx context.Context) datasetspb.DatasetServiceServer {
	return &DatasetGrpcServer{base: CreateDatasetServerWithContext(ctx)}
}

func CreateClassicEvalServer() datasetspb.EvalServiceServer {
	return &EvalGrpcServer{base: CreateEvalServer()}
}

func CreateClassicEvalServerWithContext(ctx context.Context) datasetspb.EvalServiceServer {
	return &EvalGrpcServer{base: CreateEvalServerWithContext(ctx)}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *DatasetServer) WithInterceptors(interceptors ...connect.Interceptor) *DatasetServer {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *EvalServer) WithInterceptors(interceptors ...connect.Interceptor) *EvalServer {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// --- DatasetServer Connect plumbing ---

func (s *DatasetServer) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return datasetsconnect.NewDatasetServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *DatasetServer) FileDescriptor() protoreflect.FileDescriptor {
	return datasetspb.File_everstack_datasets_v1_datasets_service_proto
}

func (s *DatasetServer) AppName() string {
	return datasetsconnect.DatasetServiceName
}

func (s *DatasetServer) MethodPrefix() string {
	return datasetsconnect.DatasetServiceName
}

func (s *DatasetServer) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return datasetspb.RegisterDatasetServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// --- EvalServer Connect plumbing ---

func (s *EvalServer) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return datasetsconnect.NewEvalServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *EvalServer) FileDescriptor() protoreflect.FileDescriptor {
	return datasetspb.File_everstack_datasets_v1_datasets_service_proto
}

func (s *EvalServer) AppName() string {
	return datasetsconnect.EvalServiceName
}

func (s *EvalServer) MethodPrefix() string {
	return datasetsconnect.EvalServiceName
}

func (s *EvalServer) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return datasetspb.RegisterEvalServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// --- DatasetGrpcServer wrapper methods ---

func (g *DatasetGrpcServer) CreateDataset(ctx context.Context, req *datasetspb.CreateDatasetRequest) (*datasetspb.CreateDatasetResponse, error) {
	cReq := &connect.Request[datasetspb.CreateDatasetRequest]{Msg: req}
	resp, err := g.base.CreateDataset(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GetDataset(ctx context.Context, req *datasetspb.GetDatasetRequest) (*datasetspb.GetDatasetResponse, error) {
	cReq := &connect.Request[datasetspb.GetDatasetRequest]{Msg: req}
	resp, err := g.base.GetDataset(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) ListDatasets(ctx context.Context, req *datasetspb.ListDatasetsRequest) (*datasetspb.ListDatasetsResponse, error) {
	cReq := &connect.Request[datasetspb.ListDatasetsRequest]{Msg: req}
	resp, err := g.base.ListDatasets(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) UpdateDataset(ctx context.Context, req *datasetspb.UpdateDatasetRequest) (*datasetspb.UpdateDatasetResponse, error) {
	cReq := &connect.Request[datasetspb.UpdateDatasetRequest]{Msg: req}
	resp, err := g.base.UpdateDataset(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) DeleteDataset(ctx context.Context, req *datasetspb.DeleteDatasetRequest) (*datasetspb.DeleteDatasetResponse, error) {
	cReq := &connect.Request[datasetspb.DeleteDatasetRequest]{Msg: req}
	resp, err := g.base.DeleteDataset(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) CreateDatasetVersion(ctx context.Context, req *datasetspb.CreateDatasetVersionRequest) (*datasetspb.CreateDatasetVersionResponse, error) {
	cReq := &connect.Request[datasetspb.CreateDatasetVersionRequest]{Msg: req}
	resp, err := g.base.CreateDatasetVersion(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) ListDatasetVersions(ctx context.Context, req *datasetspb.ListDatasetVersionsRequest) (*datasetspb.ListDatasetVersionsResponse, error) {
	cReq := &connect.Request[datasetspb.ListDatasetVersionsRequest]{Msg: req}
	resp, err := g.base.ListDatasetVersions(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GetDatasetVersion(ctx context.Context, req *datasetspb.GetDatasetVersionRequest) (*datasetspb.GetDatasetVersionResponse, error) {
	cReq := &connect.Request[datasetspb.GetDatasetVersionRequest]{Msg: req}
	resp, err := g.base.GetDatasetVersion(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) CreateDatasetItem(ctx context.Context, req *datasetspb.CreateDatasetItemRequest) (*datasetspb.CreateDatasetItemResponse, error) {
	cReq := &connect.Request[datasetspb.CreateDatasetItemRequest]{Msg: req}
	resp, err := g.base.CreateDatasetItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) CreateDatasetItemBatch(ctx context.Context, req *datasetspb.CreateDatasetItemBatchRequest) (*datasetspb.CreateDatasetItemBatchResponse, error) {
	cReq := &connect.Request[datasetspb.CreateDatasetItemBatchRequest]{Msg: req}
	resp, err := g.base.CreateDatasetItemBatch(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GenerateDatasetItems(ctx context.Context, req *datasetspb.GenerateDatasetItemsRequest) (*datasetspb.GenerateDatasetItemsResponse, error) {
	cReq := &connect.Request[datasetspb.GenerateDatasetItemsRequest]{Msg: req}
	resp, err := g.base.GenerateDatasetItems(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GenerateRedTeamDataset(ctx context.Context, req *datasetspb.GenerateRedTeamDatasetRequest) (*datasetspb.GenerateRedTeamDatasetResponse, error) {
	cReq := &connect.Request[datasetspb.GenerateRedTeamDatasetRequest]{Msg: req}
	resp, err := g.base.GenerateRedTeamDataset(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GetDatasetItem(ctx context.Context, req *datasetspb.GetDatasetItemRequest) (*datasetspb.GetDatasetItemResponse, error) {
	cReq := &connect.Request[datasetspb.GetDatasetItemRequest]{Msg: req}
	resp, err := g.base.GetDatasetItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) ListDatasetItems(ctx context.Context, req *datasetspb.ListDatasetItemsRequest) (*datasetspb.ListDatasetItemsResponse, error) {
	cReq := &connect.Request[datasetspb.ListDatasetItemsRequest]{Msg: req}
	resp, err := g.base.ListDatasetItems(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) UpdateDatasetItem(ctx context.Context, req *datasetspb.UpdateDatasetItemRequest) (*datasetspb.UpdateDatasetItemResponse, error) {
	cReq := &connect.Request[datasetspb.UpdateDatasetItemRequest]{Msg: req}
	resp, err := g.base.UpdateDatasetItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) DeleteDatasetItem(ctx context.Context, req *datasetspb.DeleteDatasetItemRequest) (*datasetspb.DeleteDatasetItemResponse, error) {
	cReq := &connect.Request[datasetspb.DeleteDatasetItemRequest]{Msg: req}
	resp, err := g.base.DeleteDatasetItem(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) CreateScoreConfig(ctx context.Context, req *datasetspb.CreateScoreConfigRequest) (*datasetspb.CreateScoreConfigResponse, error) {
	cReq := &connect.Request[datasetspb.CreateScoreConfigRequest]{Msg: req}
	resp, err := g.base.CreateScoreConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) GetScoreConfig(ctx context.Context, req *datasetspb.GetScoreConfigRequest) (*datasetspb.GetScoreConfigResponse, error) {
	cReq := &connect.Request[datasetspb.GetScoreConfigRequest]{Msg: req}
	resp, err := g.base.GetScoreConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) ListScoreConfigs(ctx context.Context, req *datasetspb.ListScoreConfigsRequest) (*datasetspb.ListScoreConfigsResponse, error) {
	cReq := &connect.Request[datasetspb.ListScoreConfigsRequest]{Msg: req}
	resp, err := g.base.ListScoreConfigs(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) UpdateScoreConfig(ctx context.Context, req *datasetspb.UpdateScoreConfigRequest) (*datasetspb.UpdateScoreConfigResponse, error) {
	cReq := &connect.Request[datasetspb.UpdateScoreConfigRequest]{Msg: req}
	resp, err := g.base.UpdateScoreConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) DeleteScoreConfig(ctx context.Context, req *datasetspb.DeleteScoreConfigRequest) (*datasetspb.DeleteScoreConfigResponse, error) {
	cReq := &connect.Request[datasetspb.DeleteScoreConfigRequest]{Msg: req}
	resp, err := g.base.DeleteScoreConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *DatasetGrpcServer) ListBuiltinMetrics(ctx context.Context, req *datasetspb.ListBuiltinMetricsRequest) (*datasetspb.ListBuiltinMetricsResponse, error) {
	cReq := &connect.Request[datasetspb.ListBuiltinMetricsRequest]{Msg: req}
	resp, err := g.base.ListBuiltinMetrics(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// --- EvalGrpcServer wrapper methods ---

func (g *EvalGrpcServer) CreateEvalRun(ctx context.Context, req *datasetspb.CreateEvalRunRequest) (*datasetspb.CreateEvalRunResponse, error) {
	cReq := &connect.Request[datasetspb.CreateEvalRunRequest]{Msg: req}
	resp, err := g.base.CreateEvalRun(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) GetEvalRun(ctx context.Context, req *datasetspb.GetEvalRunRequest) (*datasetspb.GetEvalRunResponse, error) {
	cReq := &connect.Request[datasetspb.GetEvalRunRequest]{Msg: req}
	resp, err := g.base.GetEvalRun(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) ListEvalRuns(ctx context.Context, req *datasetspb.ListEvalRunsRequest) (*datasetspb.ListEvalRunsResponse, error) {
	cReq := &connect.Request[datasetspb.ListEvalRunsRequest]{Msg: req}
	resp, err := g.base.ListEvalRuns(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) CancelEvalRun(ctx context.Context, req *datasetspb.CancelEvalRunRequest) (*datasetspb.CancelEvalRunResponse, error) {
	cReq := &connect.Request[datasetspb.CancelEvalRunRequest]{Msg: req}
	resp, err := g.base.CancelEvalRun(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) DeleteEvalRun(ctx context.Context, req *datasetspb.DeleteEvalRunRequest) (*datasetspb.DeleteEvalRunResponse, error) {
	cReq := &connect.Request[datasetspb.DeleteEvalRunRequest]{Msg: req}
	resp, err := g.base.DeleteEvalRun(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) RetryEvalRun(ctx context.Context, req *datasetspb.RetryEvalRunRequest) (*datasetspb.RetryEvalRunResponse, error) {
	cReq := &connect.Request[datasetspb.RetryEvalRunRequest]{Msg: req}
	resp, err := g.base.RetryEvalRun(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) GetEvalRunItems(ctx context.Context, req *datasetspb.GetEvalRunItemsRequest) (*datasetspb.GetEvalRunItemsResponse, error) {
	cReq := &connect.Request[datasetspb.GetEvalRunItemsRequest]{Msg: req}
	resp, err := g.base.GetEvalRunItems(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) GetEvalRunSummary(ctx context.Context, req *datasetspb.GetEvalRunSummaryRequest) (*datasetspb.GetEvalRunSummaryResponse, error) {
	cReq := &connect.Request[datasetspb.GetEvalRunSummaryRequest]{Msg: req}
	resp, err := g.base.GetEvalRunSummary(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) CompareEvalRuns(ctx context.Context, req *datasetspb.CompareEvalRunsRequest) (*datasetspb.CompareEvalRunsResponse, error) {
	cReq := &connect.Request[datasetspb.CompareEvalRunsRequest]{Msg: req}
	resp, err := g.base.CompareEvalRuns(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) ListComparisonRows(ctx context.Context, req *datasetspb.ListComparisonRowsRequest) (*datasetspb.ListComparisonRowsResponse, error) {
	cReq := &connect.Request[datasetspb.ListComparisonRowsRequest]{Msg: req}
	resp, err := g.base.ListComparisonRows(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) SetBaseline(ctx context.Context, req *datasetspb.SetBaselineRequest) (*datasetspb.SetBaselineResponse, error) {
	cReq := &connect.Request[datasetspb.SetBaselineRequest]{Msg: req}
	resp, err := g.base.SetBaseline(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) CreateEvalSchedule(ctx context.Context, req *datasetspb.CreateEvalScheduleRequest) (*datasetspb.CreateEvalScheduleResponse, error) {
	cReq := &connect.Request[datasetspb.CreateEvalScheduleRequest]{Msg: req}
	resp, err := g.base.CreateEvalSchedule(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) GetEvalSchedule(ctx context.Context, req *datasetspb.GetEvalScheduleRequest) (*datasetspb.GetEvalScheduleResponse, error) {
	cReq := &connect.Request[datasetspb.GetEvalScheduleRequest]{Msg: req}
	resp, err := g.base.GetEvalSchedule(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) ListEvalSchedules(ctx context.Context, req *datasetspb.ListEvalSchedulesRequest) (*datasetspb.ListEvalSchedulesResponse, error) {
	cReq := &connect.Request[datasetspb.ListEvalSchedulesRequest]{Msg: req}
	resp, err := g.base.ListEvalSchedules(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) UpdateEvalSchedule(ctx context.Context, req *datasetspb.UpdateEvalScheduleRequest) (*datasetspb.UpdateEvalScheduleResponse, error) {
	cReq := &connect.Request[datasetspb.UpdateEvalScheduleRequest]{Msg: req}
	resp, err := g.base.UpdateEvalSchedule(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) DeleteEvalSchedule(ctx context.Context, req *datasetspb.DeleteEvalScheduleRequest) (*datasetspb.DeleteEvalScheduleResponse, error) {
	cReq := &connect.Request[datasetspb.DeleteEvalScheduleRequest]{Msg: req}
	resp, err := g.base.DeleteEvalSchedule(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *EvalGrpcServer) ScoreOutput(ctx context.Context, req *datasetspb.ScoreOutputRequest) (*datasetspb.ScoreOutputResponse, error) {
	cReq := &connect.Request[datasetspb.ScoreOutputRequest]{Msg: req}
	resp, err := g.base.ScoreOutput(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// === PlaygroundServer ===

var _ datasetspb.PlaygroundServiceServer = (*PlaygroundGrpcServer)(nil)

var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*PlaygroundServer)(nil)

// PlaygroundServer handles Playground document CRUD RPCs (Connect), backed by
// direct SQL against s.db.
type PlaygroundServer struct {
	ctx                 context.Context
	db                  *sqlx.DB
	serviceInterceptors []connect.Interceptor
}

func (s *PlaygroundServer) SetDB(db *sqlx.DB) { s.db = db }

// PlaygroundGrpcServer wraps PlaygroundServer for classic gRPC
type PlaygroundGrpcServer struct {
	datasetspb.UnimplementedPlaygroundServiceServer
	base *PlaygroundServer
}

// --- Constructors ---

func CreatePlaygroundServer() *PlaygroundServer {
	return &PlaygroundServer{}
}

func CreatePlaygroundServerWithContext(ctx context.Context) *PlaygroundServer {
	return &PlaygroundServer{ctx: ctx}
}

func CreateClassicPlaygroundServer() datasetspb.PlaygroundServiceServer {
	return &PlaygroundGrpcServer{base: CreatePlaygroundServer()}
}

func CreateClassicPlaygroundServerWithContext(ctx context.Context) datasetspb.PlaygroundServiceServer {
	return &PlaygroundGrpcServer{base: CreatePlaygroundServerWithContext(ctx)}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *PlaygroundServer) WithInterceptors(interceptors ...connect.Interceptor) *PlaygroundServer {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// --- PlaygroundServer Connect plumbing ---

func (s *PlaygroundServer) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return datasetsconnect.NewPlaygroundServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *PlaygroundServer) FileDescriptor() protoreflect.FileDescriptor {
	return datasetspb.File_everstack_datasets_v1_datasets_service_proto
}

func (s *PlaygroundServer) AppName() string {
	return datasetsconnect.PlaygroundServiceName
}

func (s *PlaygroundServer) MethodPrefix() string {
	return datasetsconnect.PlaygroundServiceName
}

func (s *PlaygroundServer) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return datasetspb.RegisterPlaygroundServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// --- PlaygroundGrpcServer wrapper methods ---

func (g *PlaygroundGrpcServer) CreatePlayground(ctx context.Context, req *datasetspb.CreatePlaygroundRequest) (*datasetspb.CreatePlaygroundResponse, error) {
	cReq := &connect.Request[datasetspb.CreatePlaygroundRequest]{Msg: req}
	resp, err := g.base.CreatePlayground(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *PlaygroundGrpcServer) GetPlayground(ctx context.Context, req *datasetspb.GetPlaygroundRequest) (*datasetspb.GetPlaygroundResponse, error) {
	cReq := &connect.Request[datasetspb.GetPlaygroundRequest]{Msg: req}
	resp, err := g.base.GetPlayground(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *PlaygroundGrpcServer) ListPlaygrounds(ctx context.Context, req *datasetspb.ListPlaygroundsRequest) (*datasetspb.ListPlaygroundsResponse, error) {
	cReq := &connect.Request[datasetspb.ListPlaygroundsRequest]{Msg: req}
	resp, err := g.base.ListPlaygrounds(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *PlaygroundGrpcServer) UpdatePlayground(ctx context.Context, req *datasetspb.UpdatePlaygroundRequest) (*datasetspb.UpdatePlaygroundResponse, error) {
	cReq := &connect.Request[datasetspb.UpdatePlaygroundRequest]{Msg: req}
	resp, err := g.base.UpdatePlayground(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *PlaygroundGrpcServer) DeletePlayground(ctx context.Context, req *datasetspb.DeletePlaygroundRequest) (*datasetspb.DeletePlaygroundResponse, error) {
	cReq := &connect.Request[datasetspb.DeletePlaygroundRequest]{Msg: req}
	resp, err := g.base.DeletePlayground(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
