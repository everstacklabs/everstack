package v1

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/modelmetrics"
	modelmetricspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/modelmetrics/v1"
)

type reportRepository struct {
	raw       modelmetrics.RawReport
	breakdown modelmetrics.RawBreakdown
}

func (r reportRepository) LoadReport(context.Context, modelmetrics.Query) (modelmetrics.RawReport, error) {
	return r.raw, nil
}

func (r reportRepository) LoadBreakdown(
	context.Context,
	modelmetrics.BreakdownQuery,
) (modelmetrics.RawBreakdown, error) {
	return r.breakdown, nil
}

func TestGetReportMapsPublicContract(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	server := NewServer(modelmetrics.NewService(reportRepository{
		raw: modelmetrics.RawReport{Buckets: []modelmetrics.Bucket{{
			Period:      now.Add(-time.Hour),
			TenantCount: modelmetrics.MinimumPublicTenants,
			Metrics: modelmetrics.Metrics{
				Requests:        modelmetrics.MinimumPublicRequests,
				InputTokens:     2_000,
				OutputTokens:    500,
				ReasoningTokens: 100,
			},
		}}},
	}, modelmetrics.Config{
		MinimumTenants:  modelmetrics.MinimumPublicTenants,
		MinimumRequests: modelmetrics.MinimumPublicRequests,
		Now:             func() time.Time { return now },
	}))

	response, err := server.GetReport(context.Background(), connect.NewRequest(
		&modelmetricspb.GetPublicModelMetricsReportRequest{
			Kind:     "model",
			Key:      "qwen/qwen3.7-plus",
			Window:   "30d",
			Interval: "day",
		},
	))
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if response.Msg.GetStatus() != "available" {
		t.Fatalf("status = %q, want available", response.Msg.GetStatus())
	}
	if got := response.Msg.GetSummary().GetTotalTokens(); got != 2_500 {
		t.Fatalf("summary total tokens = %d, want 2500", got)
	}
	if got := len(response.Msg.GetPoints()); got != 1 {
		t.Fatalf("len(points) = %d, want 1", got)
	}
	if got := response.Msg.GetPoints()[0].GetCumulative().GetReasoningTokens(); got != 100 {
		t.Fatalf("cumulative reasoning tokens = %d, want 100", got)
	}
}

func TestGetReportMapsTimeBoundTestingThresholds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	server := NewServer(modelmetrics.NewService(reportRepository{
		raw: modelmetrics.RawReport{Buckets: []modelmetrics.Bucket{{
			Period:      now.Add(-time.Hour),
			TenantCount: 1,
			Metrics: modelmetrics.Metrics{
				Requests:    1,
				InputTokens: 42,
			},
		}}},
	}, modelmetrics.Config{
		MinimumTenants:         1,
		MinimumRequests:        1,
		TestingThresholdsUntil: now.Add(time.Hour),
		Now:                    func() time.Time { return now },
	}))

	response, err := server.GetReport(context.Background(), connect.NewRequest(
		&modelmetricspb.GetPublicModelMetricsReportRequest{
			Kind:     "model",
			Key:      "openai/gpt-test",
			Window:   "7d",
			Interval: "hour",
		},
	))
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if response.Msg.GetStatus() != "available" {
		t.Fatalf("status = %q, want available", response.Msg.GetStatus())
	}
	if got := response.Msg.GetSummary().GetRequests(); got != 1 {
		t.Fatalf("summary requests = %d, want 1", got)
	}
	if got := response.Msg.GetCoverage().GetMinimumTenants(); got != 1 {
		t.Fatalf("coverage minimum tenants = %d, want 1", got)
	}
	if got := response.Msg.GetCoverage().GetMinimumRequests(); got != 1 {
		t.Fatalf("coverage minimum requests = %d, want 1", got)
	}
}

func TestGetProviderModelBreakdownMapsRankedSeries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	server := NewServer(modelmetrics.NewService(reportRepository{
		breakdown: modelmetrics.RawBreakdown{
			Buckets: []modelmetrics.BreakdownBucket{
				{
					Period:      now.Add(-24 * time.Hour),
					Key:         "openai/gpt-5.5",
					TenantCount: modelmetrics.MinimumPublicTenants,
					Metrics: modelmetrics.Metrics{
						Requests:     modelmetrics.MinimumPublicRequests,
						InputTokens:  2_000,
						OutputTokens: 500,
					},
				},
			},
		},
	}, modelmetrics.Config{Now: func() time.Time { return now }}))

	response, err := server.GetProviderModelBreakdown(
		context.Background(),
		connect.NewRequest(
			&modelmetricspb.GetPublicProviderModelBreakdownRequest{
				Provider: "openai",
				Metric:   "tokens",
				Window:   "30d",
				Interval: "day",
				Limit:    10,
			},
		),
	)
	if err != nil {
		t.Fatalf("GetProviderModelBreakdown() error = %v", err)
	}
	if response.Msg.GetStatus() != "available" {
		t.Fatalf("status = %q, want available", response.Msg.GetStatus())
	}
	if got := len(response.Msg.GetSeries()); got != 1 {
		t.Fatalf("len(series) = %d, want 1", got)
	}
	if got := response.Msg.GetSeries()[0].GetKey(); got != "openai/gpt-5.5" {
		t.Fatalf("series key = %q, want openai/gpt-5.5", got)
	}
	if got := response.Msg.GetSeries()[0].GetTotal(); got != 2_500 {
		t.Fatalf("series total = %v, want 2500", got)
	}
}

func TestGetReportRejectsInvalidFilters(t *testing.T) {
	t.Parallel()

	server := NewServer(modelmetrics.NewService(reportRepository{}, modelmetrics.Config{}))
	_, err := server.GetReport(context.Background(), connect.NewRequest(
		&modelmetricspb.GetPublicModelMetricsReportRequest{
			Kind:     "tenant",
			Key:      "secret",
			Window:   "30d",
			Interval: "day",
		},
	))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("error code = %v, want invalid_argument (err=%v)", connect.CodeOf(err), err)
	}
}
