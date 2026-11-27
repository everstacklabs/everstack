package modelmetrics

import (
	"context"
	"strings"
	"testing"
	"time"
)

type stubRepository struct {
	report    RawReport
	breakdown RawBreakdown
	err       error
}

func (s stubRepository) LoadReport(context.Context, Query) (RawReport, error) {
	return s.report, s.err
}

func (s stubRepository) LoadBreakdown(
	context.Context,
	BreakdownQuery,
) (RawBreakdown, error) {
	return s.breakdown, s.err
}

func TestServiceEnforcesImmutablePrivacyFloors(t *testing.T) {
	t.Parallel()

	service := NewService(stubRepository{}, Config{
		MinimumTenants:  1,
		MinimumRequests: 1,
	})

	if service.config.MinimumTenants != MinimumPublicTenants {
		t.Fatalf(
			"minimum tenants = %d, want privacy floor %d",
			service.config.MinimumTenants,
			MinimumPublicTenants,
		)
	}
	if service.config.MinimumRequests != MinimumPublicRequests {
		t.Fatalf(
			"minimum requests = %d, want privacy floor %d",
			service.config.MinimumRequests,
			MinimumPublicRequests,
		)
	}
}

func TestServiceAllowsThresholdsDuringExplicitTestingWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{report: RawReport{
		Buckets: []Bucket{{
			Period:      now.Add(-time.Hour),
			TenantCount: 1,
			Metrics: Metrics{
				Requests:    1,
				InputTokens: 42,
			},
		}},
	}}, Config{
		MinimumTenants:         1,
		MinimumRequests:        1,
		TestingThresholdsUntil: now.Add(time.Hour),
		Now:                    func() time.Time { return now },
	})

	report, err := service.Report(context.Background(), Query{
		Kind:     KindModel,
		Key:      "openai/gpt-test",
		Window:   Window7Days,
		Interval: IntervalHour,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	if report.Status != StatusAvailable {
		t.Fatalf("status = %q, want %q", report.Status, StatusAvailable)
	}
	if got := len(report.Points); got != 1 {
		t.Fatalf("len(points) = %d, want 1", got)
	}
	if report.Summary.Requests != 1 || report.Summary.InputTokens != 42 {
		t.Fatalf("summary = %#v, want one visible test request", report.Summary)
	}
	if report.Coverage.MinimumTenants != 1 ||
		report.Coverage.MinimumRequests != 1 {
		t.Fatalf("coverage = %#v, want explicit 1/1 testing thresholds", report.Coverage)
	}
}

func TestServiceRestoresPrivacyFloorsAfterTestingWindow(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	now := startedAt
	service := NewService(stubRepository{report: RawReport{
		Buckets: []Bucket{{
			Period:      startedAt.Add(-time.Hour),
			TenantCount: 1,
			Metrics:     Metrics{Requests: 1},
		}},
	}}, Config{
		MinimumTenants:         1,
		MinimumRequests:        1,
		TestingThresholdsUntil: startedAt.Add(time.Hour),
		Now:                    func() time.Time { return now },
	})
	now = startedAt.Add(2 * time.Hour)

	report, err := service.Report(context.Background(), Query{
		Kind:     KindModel,
		Key:      "openai/gpt-test",
		Window:   Window7Days,
		Interval: IntervalHour,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if report.Status != StatusInsufficientData {
		t.Fatalf("status = %q, want %q", report.Status, StatusInsufficientData)
	}
	if report.Coverage.MinimumTenants != MinimumPublicTenants ||
		report.Coverage.MinimumRequests != MinimumPublicRequests {
		t.Fatalf("coverage = %#v, want restored public floors", report.Coverage)
	}
}

func TestServiceRejectsTestingWindowsLongerThanFortyEightHours(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{}, Config{
		MinimumTenants:         1,
		MinimumRequests:        1,
		TestingThresholdsUntil: now.Add(MaximumTestingThresholdWindow + time.Second),
		Now:                    func() time.Time { return now },
	})

	if !service.config.TestingThresholdsUntil.IsZero() {
		t.Fatalf(
			"testing threshold expiry = %v, want rejected window",
			service.config.TestingThresholdsUntil,
		)
	}
	if service.config.MinimumTenants != MinimumPublicTenants ||
		service.config.MinimumRequests != MinimumPublicRequests {
		t.Fatalf("thresholds = %#v, want public privacy floors", service.config)
	}
}

func TestServiceRequiresExplicitThresholdsForTestingWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{}, Config{
		TestingThresholdsUntil: now.Add(time.Hour),
		Now:                    func() time.Time { return now },
	})

	if service.config.MinimumTenants != MinimumPublicTenants ||
		service.config.MinimumRequests != MinimumPublicRequests {
		t.Fatalf(
			"thresholds = %d/%d, want explicit public defaults %d/%d",
			service.config.MinimumTenants,
			service.config.MinimumRequests,
			MinimumPublicTenants,
			MinimumPublicRequests,
		)
	}
}

func TestServiceBuildsPrivacySafeCumulativeSeries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	repo := stubRepository{report: RawReport{
		DataSince: now.Add(-72 * time.Hour),
		Buckets: []Bucket{
			{
				Period:      dayStart.AddDate(0, 0, -3),
				TenantCount: 5,
				Metrics: Metrics{
					Requests:             100,
					Errors:               4,
					InputTokens:          1_000,
					OutputTokens:         600,
					ReasoningTokens:      200,
					CacheReadTokens:      100,
					CacheWriteTokens:     40,
					CostUSD:              1.25,
					LatencyTotalMS:       25_000,
					LatencySamples:       100,
					TTFTTotalMS:          4_000,
					TTFTSamples:          80,
					StreamOutputTokens:   500,
					GenerationDurationMS: 10_000,
				},
			},
			{
				// This bucket has plenty of traffic but too few tenants. It must
				// not be recoverable through either the series or cumulative
				// differences.
				Period:      dayStart.AddDate(0, 0, -2),
				TenantCount: 4,
				Metrics: Metrics{
					Requests:        500,
					InputTokens:     50_000,
					OutputTokens:    25_000,
					ReasoningTokens: 10_000,
				},
			},
			{
				Period:      dayStart.AddDate(0, 0, -1),
				TenantCount: 7,
				Metrics: Metrics{
					Requests:             200,
					Errors:               10,
					InputTokens:          2_000,
					OutputTokens:         1_000,
					ReasoningTokens:      1_200, // Provider anomaly: clamp non-reasoning to zero.
					CacheReadTokens:      300,
					CacheWriteTokens:     50,
					CostUSD:              2.75,
					LatencyTotalMS:       70_000,
					LatencySamples:       200,
					TTFTTotalMS:          9_000,
					TTFTSamples:          180,
					StreamOutputTokens:   900,
					GenerationDurationMS: 30_000,
				},
			},
		},
	}}

	service := NewService(repo, Config{
		MinimumTenants:  5,
		MinimumRequests: 100,
		Now:             func() time.Time { return now },
	})

	report, err := service.Report(context.Background(), Query{
		Kind:     KindModel,
		Key:      "openai/gpt-5.6",
		Window:   Window30Days,
		Interval: IntervalDay,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	if report.Status != StatusAvailable {
		t.Fatalf("status = %q, want %q", report.Status, StatusAvailable)
	}
	if got := len(report.Points); got != 2 {
		t.Fatalf("len(points) = %d, want 2", got)
	}
	if got := report.Points[1].Cumulative.Requests; got != 300 {
		t.Fatalf("cumulative requests = %d, want 300", got)
	}
	if got := report.Summary.InputTokens; got != 3_000 {
		t.Fatalf("summary input tokens = %d, want 3000", got)
	}
	if got := report.Summary.NonReasoningOutputTokens; got != 400 {
		t.Fatalf("non-reasoning output tokens = %d, want 400", got)
	}
	if got := report.Summary.Successes; got != 286 {
		t.Fatalf("successes = %d, want 286", got)
	}
	if got := report.Summary.AvgLatencyMS; got != 316.6666666666667 {
		t.Fatalf("average latency = %v, want weighted average", got)
	}
	// 260 TTFT samples across 300 requests clears both the absolute floor and
	// the coverage floor, so the figure is published.
	if report.Summary.AvgThroughputTPS == nil {
		t.Fatal("average throughput suppressed for a well-sampled bucket")
	}
	if got := *report.Summary.AvgThroughputTPS; got != 35 {
		t.Fatalf("average throughput = %v, want 35", got)
	}
	if got := report.Coverage.SuppressedBuckets; got != 1 {
		t.Fatalf("suppressed buckets = %d, want 1", got)
	}
	if report.DataThrough != dayStart {
		t.Fatalf("data through = %v, want end of latest eligible bucket", report.DataThrough)
	}
}

func TestServiceReturnsInsufficientDataWithoutLeakingSuppressedTotals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{report: RawReport{
		Buckets: []Bucket{{
			Period:      now.Add(-time.Hour),
			TenantCount: 2,
			Metrics: Metrics{
				Requests:    10_000,
				InputTokens: 5_000_000,
			},
		}},
	}}, Config{
		MinimumTenants:  5,
		MinimumRequests: 100,
		Now:             func() time.Time { return now },
	})

	report, err := service.Report(context.Background(), Query{
		Kind:     KindProvider,
		Key:      "openai",
		Window:   Window7Days,
		Interval: IntervalDay,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}

	if report.Status != StatusInsufficientData {
		t.Fatalf("status = %q, want %q", report.Status, StatusInsufficientData)
	}
	if report.Points == nil || len(report.Points) != 0 {
		t.Fatalf("points = %#v, want a non-nil empty slice", report.Points)
	}
	if report.Summary.Requests != 0 || report.Summary.InputTokens != 0 {
		t.Fatalf("suppressed totals leaked in summary: %#v", report.Summary)
	}
}

func TestProviderModelBreakdownRanksSeriesAndAggregatesOthersAfterPrivacyFiltering(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	eligible := func(
		period time.Time,
		key string,
		inputTokens uint64,
	) BreakdownBucket {
		return BreakdownBucket{
			Period:      period,
			Key:         key,
			TenantCount: MinimumPublicTenants,
			Metrics: Metrics{
				Requests:    MinimumPublicRequests,
				InputTokens: inputTokens,
			},
		}
	}
	service := NewService(stubRepository{breakdown: RawBreakdown{
		Buckets: []BreakdownBucket{
			eligible(dayStart.AddDate(0, 0, -2), "openai/gpt-a", 1_000),
			eligible(dayStart.AddDate(0, 0, -1), "openai/gpt-a", 500),
			eligible(dayStart.AddDate(0, 0, -2), "openai/gpt-b", 700),
			eligible(dayStart.AddDate(0, 0, -1), "openai/gpt-b", 200),
			eligible(dayStart.AddDate(0, 0, -1), "openai/gpt-c", 700),
			eligible(dayStart.AddDate(0, 0, -1), "openai/gpt-d", 300),
			{
				Period:      dayStart.AddDate(0, 0, -1),
				Key:         "openai/private-model",
				TenantCount: MinimumPublicTenants - 1,
				Metrics: Metrics{
					Requests:    10_000,
					InputTokens: 9_000_000,
				},
			},
		},
	}}, Config{Now: func() time.Time { return now }})

	result, err := service.ProviderModelBreakdown(
		context.Background(),
		BreakdownQuery{
			Provider: "openai",
			Metric:   MetricTokens,
			Window:   Window30Days,
			Interval: IntervalDay,
			Limit:    3,
		},
	)
	if err != nil {
		t.Fatalf("ProviderModelBreakdown() error = %v", err)
	}

	if result.Status != StatusAvailable {
		t.Fatalf("status = %q, want %q", result.Status, StatusAvailable)
	}
	if got := len(result.Series); got != 3 {
		t.Fatalf("len(series) = %d, want 3", got)
	}
	for index, want := range []struct {
		key   string
		total float64
	}{
		{key: "openai/gpt-a", total: 1_500},
		{key: "others", total: 1_000},
		{key: "openai/gpt-b", total: 900},
	} {
		if got := result.Series[index]; got.Key != want.key || got.Total != want.total {
			t.Fatalf(
				"series[%d] = %#v, want key %q total %v",
				index,
				got,
				want.key,
				want.total,
			)
		}
	}
	if got := result.Series[0].Points[1].Cumulative; got != 1_500 {
		t.Fatalf("gpt-a cumulative tokens = %v, want 1500", got)
	}
	if result.Coverage.SuppressedBuckets != 1 {
		t.Fatalf(
			"suppressed buckets = %d, want 1",
			result.Coverage.SuppressedBuckets,
		)
	}
	for _, series := range result.Series {
		if series.Key == "openai/private-model" || series.Total >= 9_000_000 {
			t.Fatalf("privacy-suppressed series leaked: %#v", series)
		}
	}
}

func TestServiceMarksOldReportsStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{report: RawReport{
		Buckets: []Bucket{{
			Period:      dayStart.AddDate(0, 0, -4),
			TenantCount: 8,
			Metrics:     Metrics{Requests: 200},
		}},
	}}, Config{
		MinimumTenants:  5,
		MinimumRequests: 100,
		Now:             func() time.Time { return now },
	})

	report, err := service.Report(context.Background(), Query{
		Kind:     KindPublisher,
		Key:      "qwen",
		Window:   Window30Days,
		Interval: IntervalDay,
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if report.Status != StatusStale {
		t.Fatalf("status = %q, want %q", report.Status, StatusStale)
	}
}

func TestValidateCompareQueryRejectsMoreThanFourKeys(t *testing.T) {
	t.Parallel()

	err := ValidateCompareQuery(CompareQuery{
		Kind:     KindModel,
		Keys:     []string{"a/1", "b/2", "c/3", "d/4", "e/5"},
		Metric:   MetricTokens,
		Window:   Window30Days,
		Interval: IntervalDay,
	})
	if err == nil {
		t.Fatal("ValidateCompareQuery() error = nil, want max-four validation error")
	}
}

func TestResolveQuerySupportsMonthlyUsageWindows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	query, err := resolveQuery(Query{
		Kind:     KindModel,
		Key:      "qwen/qwen3.7-plus",
		Window:   Window6Months,
		Interval: IntervalMonth,
	}, now)
	if err != nil {
		t.Fatalf("resolveQuery() error = %v", err)
	}
	if want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC); query.StartTime != want {
		t.Fatalf("start time = %v, want %v", query.StartTime, want)
	}
	if want := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC); query.EndTime != want {
		t.Fatalf("end time = %v, want %v", query.EndTime, want)
	}
}

func TestResolveQueryExcludesOpenBuckets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 37, 45, 0, time.UTC)
	tests := []struct {
		name     string
		interval Interval
		wantEnd  time.Time
	}{
		{
			name:     "hour",
			interval: IntervalHour,
			wantEnd:  time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "day",
			interval: IntervalDay,
			wantEnd:  time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "month",
			interval: IntervalMonth,
			wantEnd:  time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, err := resolveQuery(Query{
				Kind:     KindModel,
				Key:      "openai/gpt-5.6",
				Window:   Window30Days,
				Interval: test.interval,
			}, now)
			if err != nil {
				t.Fatalf("resolveQuery() error = %v", err)
			}
			if query.EndTime != test.wantEnd {
				t.Fatalf("end time = %v, want %v", query.EndTime, test.wantEnd)
			}
		})
	}
}

func TestValidateQueryRejectsUnboundedOrControlCharacterKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		strings.Repeat("a", 257),
		"openai/gpt-5\nsecondary-query",
	} {
		err := ValidateQuery(Query{
			Kind:     KindModel,
			Key:      key,
			Window:   Window30Days,
			Interval: IntervalDay,
		})
		if err == nil {
			t.Fatalf("ValidateQuery() accepted unsafe key %q", key)
		}
	}
}

func TestResolveQueryAllowsAllTimeMonthlyReports(t *testing.T) {
	t.Parallel()

	_, err := resolveQuery(Query{
		Kind:     KindProvider,
		Key:      "openai",
		Window:   WindowAll,
		Interval: IntervalMonth,
	}, time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolveQuery() all-time monthly error = %v", err)
	}
}

// TestDerivePublicMetricsSuppressesThroughputFromASampleOfOne reproduces the
// exact production shape that made this fix necessary: openai/gpt-5.4-mini
// published an avg_throughput_tps of exactly 200 -- 4 streamed output tokens
// over a 20 ms generation window, one streamed request out of 510 in the
// bucket. The arithmetic was right and the number was meaningless.
func TestDerivePublicMetricsSuppressesThroughputFromASampleOfOne(t *testing.T) {
	t.Parallel()

	public := derivePublicMetrics(Metrics{
		Requests:             510,
		Errors:               10,
		LatencyTotalMS:       510_000,
		LatencySamples:       510,
		TTFTTotalMS:          1_263,
		TTFTSamples:          1,
		StreamOutputTokens:   4,
		GenerationDurationMS: 20,
	})

	if public.AvgThroughputTPS != nil {
		t.Fatalf(
			"avg_throughput_tps = %v from a sample of 1 over 510 requests, want suppressed",
			*public.AvgThroughputTPS,
		)
	}
	if public.AvgTTFTMS != nil {
		t.Fatalf(
			"avg_ttft_ms = %v from a sample of 1 over 510 requests, want suppressed",
			*public.AvgTTFTMS,
		)
	}
	// Everything not derived from the streaming sample must survive.
	if public.Requests != 510 || public.AvgLatencyMS != 1_000 {
		t.Fatalf(
			"suppression leaked into non-streaming metrics: requests=%d avg_latency_ms=%v",
			public.Requests, public.AvgLatencyMS,
		)
	}
}

func TestDerivePublicMetricsSuppressesBelowStreamCoverageOnHighVolume(t *testing.T) {
	t.Parallel()

	// Clears MinimumStreamSamples outright, but 25 streams out of 100k
	// requests is a rounding-error slice of the traffic the figure is
	// labelled with.
	public := derivePublicMetrics(Metrics{
		Requests:             100_000,
		TTFTTotalMS:          25_000,
		TTFTSamples:          25,
		StreamOutputTokens:   2_500,
		GenerationDurationMS: 25_000,
	})

	if public.AvgTTFTMS != nil || public.AvgThroughputTPS != nil {
		t.Fatalf(
			"streaming averages published at %.4f%% coverage, want suppressed",
			100*float64(25)/float64(100_000),
		)
	}
}

func TestDerivePublicMetricsKeepsAGenuineZeroDistinctFromSuppression(t *testing.T) {
	t.Parallel()

	// A first chunk that arrives inside the same millisecond is a real 0 ms
	// TTFT, and attributes.SetLLMStreamMetrics records it on purpose. It must
	// reach the wire as 0, not as "no data".
	public := derivePublicMetrics(Metrics{
		Requests:             100,
		TTFTTotalMS:          0,
		TTFTSamples:          100,
		StreamOutputTokens:   0,
		GenerationDurationMS: 5_000,
	})

	if public.AvgTTFTMS == nil {
		t.Fatal("a genuine 0 ms TTFT over 100 samples must be published, not suppressed")
	}
	if got := *public.AvgTTFTMS; got != 0 {
		t.Fatalf("avg_ttft_ms = %v, want 0", got)
	}
	if public.AvgThroughputTPS == nil {
		t.Fatal("a genuine zero throughput over 100 samples must be published")
	}
	if got := *public.AvgThroughputTPS; got != 0 {
		t.Fatalf("avg_throughput_tps = %v, want 0", got)
	}
}

func TestDerivePublicMetricsSuppressesThroughputWithoutAGenerationWindow(t *testing.T) {
	t.Parallel()

	// Enough TTFT samples to publish a TTFT, but nothing ever timed a
	// generation window, so there is no throughput to report.
	public := derivePublicMetrics(Metrics{
		Requests:             100,
		TTFTTotalMS:          0,
		TTFTSamples:          100,
		StreamOutputTokens:   400,
		GenerationDurationMS: 0,
	})

	if public.AvgTTFTMS == nil {
		t.Fatal("avg_ttft_ms suppressed despite 100 samples")
	}
	if public.AvgThroughputTPS != nil {
		t.Fatalf(
			"avg_throughput_tps = %v with a zero generation window, want suppressed",
			*public.AvgThroughputTPS,
		)
	}
}

// TestCompareReportsInsufficientDataForASuppressedStreamingMetric pins the
// wire behaviour of the comparison endpoint: ComparisonPoint carries a plain
// double, so a suppressed metric would chart as a real zero. The series status
// carries the distinction instead, and the compare view already renders
// "insufficient_data" per series.
func TestCompareReportsInsufficientDataForASuppressedStreamingMetric(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	service := NewService(stubRepository{report: RawReport{
		DataSince: now.Add(-48 * time.Hour),
		Buckets: []Bucket{{
			Period:      dayStart.AddDate(0, 0, -1),
			TenantCount: 6,
			Metrics: Metrics{
				Requests:             510,
				LatencyTotalMS:       510_000,
				LatencySamples:       510,
				TTFTTotalMS:          1_263,
				TTFTSamples:          1,
				StreamOutputTokens:   4,
				GenerationDurationMS: 20,
			},
		}},
	}}, Config{
		MinimumTenants:  5,
		MinimumRequests: 100,
		Now:             func() time.Time { return now },
	})

	comparison, err := service.Compare(context.Background(), CompareQuery{
		Kind:     KindModel,
		Keys:     []string{"openai/gpt-5.4-mini", "openai/gpt-5.5"},
		Metric:   MetricThroughput,
		Window:   Window30Days,
		Interval: IntervalDay,
	})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	for _, series := range comparison.Series {
		if series.Status != StatusInsufficientData {
			t.Fatalf(
				"series %q status = %q, want %q for a suppressed throughput",
				series.Key, series.Status, StatusInsufficientData,
			)
		}
		if series.Total != 0 {
			t.Fatalf("series %q total = %v, want 0", series.Key, series.Total)
		}
	}

	// The same buckets compared on a non-streaming metric stay available.
	requestComparison, err := service.Compare(context.Background(), CompareQuery{
		Kind:     KindModel,
		Keys:     []string{"openai/gpt-5.4-mini", "openai/gpt-5.5"},
		Metric:   MetricRequests,
		Window:   Window30Days,
		Interval: IntervalDay,
	})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	for _, series := range requestComparison.Series {
		if series.Status == StatusInsufficientData {
			t.Fatalf("series %q wrongly suppressed for a non-streaming metric", series.Key)
		}
	}
}
