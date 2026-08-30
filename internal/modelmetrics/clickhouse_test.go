package modelmetrics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildReportSQLUsesSafeDimensionAndMonthlyBucket(t *testing.T) {
	t.Parallel()

	query := Query{
		Kind:      KindModel,
		Key:       "qwen/qwen3.7-plus",
		Window:    Window6Months,
		Interval:  IntervalMonth,
		StartTime: time.Date(2026, time.January, 28, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
	}
	sql, args, err := buildReportSQL(query)
	if err != nil {
		t.Fatalf("buildReportSQL() error = %v", err)
	}
	for _, expected := range []string{
		"FROM model_metrics_hourly",
		"toStartOfMonth(period)",
		"canonical_model_id = ?",
		"startsWith(canonical_model_id, ?)",
		"match(canonical_model_id, '-[0-9]{4}-[0-9]{2}-[0-9]{2}$')",
		"uniqExact(tenant_id)",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("SQL does not contain %q:\n%s", expected, sql)
		}
	}
	if strings.Contains(sql, query.Key) {
		t.Fatalf("SQL interpolated the user key instead of binding it:\n%s", sql)
	}
	if got, want := len(args), 4; got != want {
		t.Fatalf("len(args) = %d, want %d (%#v)", got, want, args)
	}
	if args[2] != query.Key {
		t.Fatalf("key arg = %#v, want %q", args[2], query.Key)
	}
	if args[3] != query.Key+"-" {
		t.Fatalf("version prefix arg = %#v, want %q", args[3], query.Key+"-")
	}
}

func TestBuildReportSQLAllTimeOmitsLowerBound(t *testing.T) {
	t.Parallel()

	sql, args, err := buildReportSQL(Query{
		Kind:     KindProvider,
		Key:      "openai",
		Window:   WindowAll,
		Interval: IntervalDay,
		EndTime:  time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("buildReportSQL() error = %v", err)
	}
	if strings.Contains(sql, "period >= ?") {
		t.Fatalf("all-time SQL unexpectedly has a lower bound:\n%s", sql)
	}
	if strings.Contains(sql, "startsWith(") {
		t.Fatalf("provider SQL unexpectedly rolls up model versions:\n%s", sql)
	}
	if got, want := len(args), 2; got != want {
		t.Fatalf("len(args) = %d, want %d (%#v)", got, want, args)
	}
}

func TestBuildBreakdownSQLGroupsCanonicalModelVersionsWithBoundProvider(t *testing.T) {
	t.Parallel()

	query := BreakdownQuery{
		Provider:  "openai",
		Metric:    MetricTokens,
		Window:    Window30Days,
		Interval:  IntervalDay,
		Limit:     10,
		StartTime: time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC),
	}
	sql, args, err := buildBreakdownSQL(query)
	if err != nil {
		t.Fatalf("buildBreakdownSQL() error = %v", err)
	}
	for _, expected := range []string{
		"toStartOfDay(period)",
		"replaceRegexpOne(",
		"'-[0-9]{4}-[0-9]{2}-[0-9]{2}$'",
		"provider = ?",
		"GROUP BY bucket, series_key",
		"uniqExact(tenant_id)",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("SQL does not contain %q:\n%s", expected, sql)
		}
	}
	if strings.Contains(sql, query.Provider) {
		t.Fatalf("SQL interpolated the provider instead of binding it:\n%s", sql)
	}
	if got, want := len(args), 3; got != want {
		t.Fatalf("len(args) = %d, want %d (%#v)", got, want, args)
	}
	if args[2] != query.Provider {
		t.Fatalf("provider arg = %#v, want %q", args[2], query.Provider)
	}
}
