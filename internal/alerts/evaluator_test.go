package alerts

import (
	"testing"
)

func TestScoreMetricSelectExpr(t *testing.T) {
	tests := []struct {
		metric     string
		wantSelect string
		wantName   string
		wantErr    bool
	}{
		{
			metric:     "score.avg.tool_quality.success_rate",
			wantSelect: "avg(NumericValue)",
			wantName:   "tool_quality.success_rate",
		},
		{
			metric:     "score.rate_true.task_completion.finished",
			wantSelect: "countIf(BooleanValue = 1) / greatest(count(), 1)",
			wantName:   "task_completion.finished",
		},
		{
			metric:     "score.rate_false.policy.compliant",
			wantSelect: "countIf(BooleanValue = 0) / greatest(count(), 1)",
			wantName:   "policy.compliant",
		},
		{
			metric:     "score.count.loop_health.looping",
			wantSelect: "toFloat64(count())",
			wantName:   "loop_health.looping",
		},
		{
			metric:     "score.min.task_completion.efficiency",
			wantSelect: "min(NumericValue)",
			wantName:   "task_completion.efficiency",
		},
		{
			metric:     "score.max.sandbox_hygiene.exit_code_rate",
			wantSelect: "max(NumericValue)",
			wantName:   "sandbox_hygiene.exit_code_rate",
		},
		// Error cases
		{
			metric:  "score.invalid_agg.foo",
			wantErr: true,
		},
		{
			metric:  "score.",
			wantErr: true,
		},
		{
			metric:  "score.avg.",
			wantErr: true,
		},
		{
			metric:  "score.avg",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			selectExpr, scoreName, err := scoreMetricSelectExpr(tt.metric)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for metric %q, got nil", tt.metric)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if selectExpr != tt.wantSelect {
				t.Errorf("selectExpr = %q, want %q", selectExpr, tt.wantSelect)
			}
			if scoreName != tt.wantName {
				t.Errorf("scoreName = %q, want %q", scoreName, tt.wantName)
			}
		})
	}
}

func TestMetricSelectExpr(t *testing.T) {
	// Verify all gateway metrics resolve without error
	gatewayMetrics := []string{
		"error_rate", "error_count",
		"avg_latency_ms", "p50_latency_ms", "p95_latency_ms", "p99_latency_ms", "max_latency_ms",
		"request_count", "requests_per_minute",
		"total_cost", "avg_cost_per_request", "cost_savings",
		"total_tokens", "input_tokens", "output_tokens", "avg_tokens_per_request",
		"cache_hit_rate", "carbon_emissions",
		"hourly_cost", "token_count",
	}

	for _, m := range gatewayMetrics {
		t.Run(m, func(t *testing.T) {
			expr, err := metricSelectExprRaw(m)
			if err != nil {
				t.Fatalf("unexpected error for metric %q: %v", m, err)
			}
			if expr == "" {
				t.Fatalf("empty expression for metric %q", m)
			}
		})
	}

	// Unsupported metric
	_, err := metricSelectExprRaw("nonexistent_metric")
	if err == nil {
		t.Fatal("expected error for unsupported metric")
	}
}

func TestIsEventDrivenMetric(t *testing.T) {
	eventDriven := []string{"eval.regression", "eval.anything"}
	for _, m := range eventDriven {
		if !isEventDrivenMetric(m) {
			t.Errorf("expected %q to be event-driven", m)
		}
	}

	polled := []string{"error_rate", "score.avg.task_completion.efficiency", "p95_latency_ms", "evaluation"}
	for _, m := range polled {
		if isEventDrivenMetric(m) {
			t.Errorf("expected %q to be polled, not event-driven", m)
		}
	}
}

func TestIsBreached(t *testing.T) {
	tests := []struct {
		op        string
		value     float64
		threshold float64
		want      bool
	}{
		{">", 10, 5, true},
		{">", 5, 10, false},
		{"<", 3, 5, true},
		{"<", 5, 3, false},
		{">=", 5, 5, true},
		{">=", 4, 5, false},
		{"<=", 5, 5, true},
		{"<=", 6, 5, false},
		{"==", 5, 5, false}, // unsupported operator
	}

	for _, tt := range tests {
		got := isBreached(tt.op, tt.value, tt.threshold)
		if got != tt.want {
			t.Errorf("isBreached(%q, %v, %v) = %v, want %v", tt.op, tt.value, tt.threshold, got, tt.want)
		}
	}
}

func TestAllBuiltinRules(t *testing.T) {
	rules := AllBuiltinRules()

	// Should include gateway, outcome, and regression rules
	want := len(builtinGatewayAlerts) + len(builtinOutcomeAlerts) + len(builtinRegressionAlerts)
	if len(rules) != want {
		t.Errorf("AllBuiltinRules() returned %d rules, want %d",
			len(rules), want)
	}

	// Verify all keys are unique
	seen := make(map[string]bool)
	for _, r := range rules {
		if seen[r.Key] {
			t.Errorf("duplicate builtin key: %s", r.Key)
		}
		seen[r.Key] = true
	}

	// Verify score-based metrics exist in outcome alerts
	scoreCount := 0
	for _, r := range rules {
		if len(r.Metric) > 6 && r.Metric[:6] == "score." {
			scoreCount++
		}
	}
	if scoreCount != len(builtinOutcomeAlerts) {
		t.Errorf("expected %d score-based rules, got %d", len(builtinOutcomeAlerts), scoreCount)
	}
}
