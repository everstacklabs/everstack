package v1

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/modelmetrics"
	modelmetricspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/modelmetrics/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/modelmetrics/v1/modelmetricsconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	modelmetricspb.UnimplementedPublicModelMetricsServiceServer
	service *modelmetrics.Service
}

func NewServer(service *modelmetrics.Service) *Server {
	return &Server{service: service}
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return modelmetricsconnect.NewPublicModelMetricsServiceHandler(
		s,
		connect.WithInterceptors(interceptors...),
	)
}

func (s *Server) RegisterGateway(
	ctx context.Context,
	mux *runtime.ServeMux,
	endpoint string,
	opts []grpc.DialOption,
) error {
	return modelmetricspb.RegisterPublicModelMetricsServiceHandlerFromEndpoint(
		ctx,
		mux,
		endpoint,
		opts,
	)
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return modelmetricspb.File_everstack_modelmetrics_v1_model_metrics_service_proto
}

func (s *Server) AppName() string {
	return modelmetricsconnect.PublicModelMetricsServiceName
}

func (s *Server) MethodPrefix() string {
	return modelmetricsconnect.PublicModelMetricsServiceName
}

func (s *Server) GetReport(
	ctx context.Context,
	req *connect.Request[modelmetricspb.GetPublicModelMetricsReportRequest],
) (*connect.Response[modelmetricspb.GetPublicModelMetricsReportResponse], error) {
	query := modelmetrics.Query{
		Kind:     modelmetrics.Kind(req.Msg.GetKind()),
		Key:      req.Msg.GetKey(),
		Window:   modelmetrics.Window(req.Msg.GetWindow()),
		Interval: modelmetrics.Interval(req.Msg.GetInterval()),
	}
	if err := modelmetrics.ValidateQuery(query); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	report, err := s.service.Report(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(reportProto(report)), nil
}

func (s *Server) Compare(
	ctx context.Context,
	req *connect.Request[modelmetricspb.ComparePublicModelMetricsRequest],
) (*connect.Response[modelmetricspb.ComparePublicModelMetricsResponse], error) {
	query := modelmetrics.CompareQuery{
		Kind:     modelmetrics.Kind(req.Msg.GetKind()),
		Keys:     normalizeKeys(req.Msg.GetKeys()),
		Metric:   modelmetrics.Metric(req.Msg.GetMetric()),
		Window:   modelmetrics.Window(req.Msg.GetWindow()),
		Interval: modelmetrics.Interval(req.Msg.GetInterval()),
	}
	if err := modelmetrics.ValidateCompareQuery(query); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	comparison, err := s.service.Compare(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(comparisonProto(comparison)), nil
}

func (s *Server) GetProviderModelBreakdown(
	ctx context.Context,
	req *connect.Request[modelmetricspb.GetPublicProviderModelBreakdownRequest],
) (*connect.Response[modelmetricspb.GetPublicProviderModelBreakdownResponse], error) {
	query := modelmetrics.BreakdownQuery{
		Provider: req.Msg.GetProvider(),
		Metric:   modelmetrics.Metric(req.Msg.GetMetric()),
		Window:   modelmetrics.Window(req.Msg.GetWindow()),
		Interval: modelmetrics.Interval(req.Msg.GetInterval()),
		Limit:    req.Msg.GetLimit(),
	}
	if err := modelmetrics.ValidateBreakdownQuery(query); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	breakdown, err := s.service.ProviderModelBreakdown(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(breakdownProto(breakdown)), nil
}

func normalizeKeys(values []string) []string {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		for _, key := range strings.Split(value, ",") {
			if key = strings.TrimSpace(key); key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func reportProto(report modelmetrics.Report) *modelmetricspb.GetPublicModelMetricsReportResponse {
	points := make([]*modelmetricspb.PublicMetricPoint, 0, len(report.Points))
	for _, point := range report.Points {
		points = append(points, &modelmetricspb.PublicMetricPoint{
			Timestamp:  timestamp(point.Timestamp),
			Increment:  metricValuesProto(point.Increment),
			Cumulative: metricValuesProto(point.Cumulative),
		})
	}
	return &modelmetricspb.GetPublicModelMetricsReportResponse{
		SchemaVersion:   report.SchemaVersion,
		SemanticVersion: report.SemanticVersion,
		GeneratedAt:     timestamp(report.GeneratedAt),
		DataSince:       timestamp(report.DataSince),
		DataThrough:     timestamp(report.DataThrough),
		Status:          string(report.Status),
		Kind:            string(report.Kind),
		Key:             report.Key,
		Window:          string(report.Window),
		Interval:        string(report.Interval),
		Summary:         metricValuesProto(report.Summary),
		Points:          points,
		Coverage: &modelmetricspb.PublicMetricsCoverage{
			EligibleBuckets:     report.Coverage.EligibleBuckets,
			SuppressedBuckets:   report.Coverage.SuppressedBuckets,
			ContributingTenants: report.Coverage.ContributingTenants,
			SampleRequests:      report.Coverage.SampleRequests,
			MinimumTenants:      report.Coverage.MinimumTenants,
			MinimumRequests:     report.Coverage.MinimumRequests,
		},
	}
}

func comparisonProto(comparison modelmetrics.Comparison) *modelmetricspb.ComparePublicModelMetricsResponse {
	series := make([]*modelmetricspb.PublicMetricComparisonSeries, 0, len(comparison.Series))
	for _, item := range comparison.Series {
		points := make([]*modelmetricspb.PublicMetricComparisonPoint, 0, len(item.Points))
		for _, point := range item.Points {
			points = append(points, &modelmetricspb.PublicMetricComparisonPoint{
				Timestamp:  timestamp(point.Timestamp),
				Increment:  point.Increment,
				Cumulative: point.Cumulative,
			})
		}
		series = append(series, &modelmetricspb.PublicMetricComparisonSeries{
			Key:         item.Key,
			Status:      string(item.Status),
			Total:       item.Total,
			Points:      points,
			DataThrough: timestamp(item.DataThrough),
		})
	}
	return &modelmetricspb.ComparePublicModelMetricsResponse{
		SchemaVersion:   comparison.SchemaVersion,
		SemanticVersion: comparison.SemanticVersion,
		GeneratedAt:     timestamp(comparison.GeneratedAt),
		Kind:            string(comparison.Kind),
		Metric:          string(comparison.Metric),
		Window:          string(comparison.Window),
		Interval:        string(comparison.Interval),
		Series:          series,
	}
}

func breakdownProto(
	breakdown modelmetrics.Breakdown,
) *modelmetricspb.GetPublicProviderModelBreakdownResponse {
	series := make(
		[]*modelmetricspb.PublicMetricComparisonSeries,
		0,
		len(breakdown.Series),
	)
	for _, item := range breakdown.Series {
		points := make(
			[]*modelmetricspb.PublicMetricComparisonPoint,
			0,
			len(item.Points),
		)
		for _, point := range item.Points {
			points = append(points, &modelmetricspb.PublicMetricComparisonPoint{
				Timestamp:  timestamp(point.Timestamp),
				Increment:  point.Increment,
				Cumulative: point.Cumulative,
			})
		}
		series = append(series, &modelmetricspb.PublicMetricComparisonSeries{
			Key:         item.Key,
			Status:      string(item.Status),
			Total:       item.Total,
			Points:      points,
			DataThrough: timestamp(item.DataThrough),
		})
	}
	return &modelmetricspb.GetPublicProviderModelBreakdownResponse{
		SchemaVersion:   breakdown.SchemaVersion,
		SemanticVersion: breakdown.SemanticVersion,
		GeneratedAt:     timestamp(breakdown.GeneratedAt),
		DataSince:       timestamp(breakdown.DataSince),
		DataThrough:     timestamp(breakdown.DataThrough),
		Status:          string(breakdown.Status),
		Provider:        breakdown.Provider,
		Metric:          string(breakdown.Metric),
		Window:          string(breakdown.Window),
		Interval:        string(breakdown.Interval),
		Series:          series,
		Coverage: &modelmetricspb.PublicMetricsCoverage{
			EligibleBuckets:     breakdown.Coverage.EligibleBuckets,
			SuppressedBuckets:   breakdown.Coverage.SuppressedBuckets,
			ContributingTenants: breakdown.Coverage.ContributingTenants,
			SampleRequests:      breakdown.Coverage.SampleRequests,
			MinimumTenants:      breakdown.Coverage.MinimumTenants,
			MinimumRequests:     breakdown.Coverage.MinimumRequests,
		},
	}
}

func metricValuesProto(values modelmetrics.PublicMetrics) *modelmetricspb.PublicMetricValues {
	return &modelmetricspb.PublicMetricValues{
		Requests:                 values.Requests,
		Successes:                values.Successes,
		Errors:                   values.Errors,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		ReasoningTokens:          values.ReasoningTokens,
		NonReasoningOutputTokens: values.NonReasoningOutputTokens,
		CacheReadTokens:          values.CacheReadTokens,
		CacheWriteTokens:         values.CacheWriteTokens,
		TotalTokens:              values.TotalTokens,
		CostUsd:                  values.CostUSD,
		AvgLatencyMs:             values.AvgLatencyMS,
		AvgTtftMs:                values.AvgTTFTMS,
		AvgThroughputTps:         values.AvgThroughputTPS,
		SuccessRate:              values.SuccessRate,
	}
}

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}
