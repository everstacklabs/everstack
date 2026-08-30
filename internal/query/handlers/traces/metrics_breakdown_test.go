package traces

import (
	"strings"
	"testing"
	"time"
)

func TestBreakdownOtelDimension(t *testing.T) {
	cases := []struct {
		groupBy   string
		wantOK    bool
		wantInExp string // substring the returned expression must contain
	}{
		{"trace_name", true, "SpanName"},
		{"session", true, "trace.session_id"},
		{"user", true, "trace.user_id"},
		{"model", false, ""},
		{"provider", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.groupBy, func(t *testing.T) {
			expr, ok := breakdownOtelDimension(tc.groupBy)
			if ok != tc.wantOK {
				t.Fatalf("breakdownOtelDimension(%q) ok = %v, want %v", tc.groupBy, ok, tc.wantOK)
			}
			if tc.wantOK && !strings.Contains(expr, tc.wantInExp) {
				t.Fatalf("breakdownOtelDimension(%q) = %q, want it to contain %q", tc.groupBy, expr, tc.wantInExp)
			}
			if !tc.wantOK && expr != "" {
				t.Fatalf("breakdownOtelDimension(%q) = %q, want empty for non-otel dimension", tc.groupBy, expr)
			}
		})
	}
}

func TestMetricsBreakdownQuery_ValidateGroupBy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	base := func(groupBy string) *MetricsBreakdownQuery {
		return &MetricsBreakdownQuery{
			StartTime: now.Add(-time.Hour),
			EndTime:   now,
			Metric:    "cost",
			GroupBy:   groupBy,
			Limit:     5,
		}
	}
	for _, groupBy := range []string{"model", "provider", "environment", "trace_name", "session", "user"} {
		if err := base(groupBy).Validate(); err != nil {
			t.Fatalf("group_by %q should be valid, got %v", groupBy, err)
		}
	}
	if err := base("prompt_version").Validate(); err == nil {
		t.Fatal("group_by prompt_version should be rejected (no prompt-version span attribute yet)")
	}
}
