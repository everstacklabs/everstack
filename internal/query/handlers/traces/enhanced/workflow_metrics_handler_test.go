package enhanced

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// otel_traces stores a span status as 'Ok' / 'Error' / 'Unset' when the
// OpenTelemetry collector exporter writes it, and as the protobuf enum name
// when the Everstack OTLP HTTP handler does. Production carries only the short
// spelling today (Error 32 / Ok 660 / Unset 8647). ClickHouse '=' is
// case-sensitive, so the rollup's original `t.StatusCode = 'OK'` and
// `t.StatusCode = 'ERROR'` matched no stored row at all: every workflow
// reported 0 completed steps, 0 failed steps and a 0 success rate.
func TestWorkflowMetricsSQLMatchesStoredStatusSpellings(t *testing.T) {
	sql := buildWorkflowMetricsSQL("WHERE t.SpanAttributes['tenant.id'] = ?")

	for _, unmatchable := range []string{
		"t.StatusCode = 'OK'",
		"t.StatusCode = 'ERROR'",
		"t.StatusCode = 'STATUS_CODE_OK'",
		"t.StatusCode = 'STATUS_CODE_ERROR'",
	} {
		if strings.Contains(sql, unmatchable) {
			t.Errorf("workflow metrics SQL still contains %q, which matches no stored status spelling", unmatchable)
		}
	}

	wantOK := otelstatus.IsOK(workflowStatusColumn)
	wantError := otelstatus.IsError(workflowStatusColumn)

	if !strings.Contains(sql, "countIf("+wantOK+") as completed_steps") {
		t.Errorf("completed_steps must count %s, got:\n%s", wantOK, sql)
	}
	if !strings.Contains(sql, "countIf("+wantError+") as failed_steps") {
		t.Errorf("failed_steps must count %s, got:\n%s", wantError, sql)
	}
}

// avgIf(1, cond) averages the constant 1 over only the rows that already
// satisfy cond, so it is 1 for any workflow with at least one matching row and
// NaN otherwise. Repairing the predicate without repairing this would have
// swapped a structural 0% success rate for a structural 100% one.
func TestWorkflowMetricsSuccessRateIsCompletedOverTotal(t *testing.T) {
	sql := buildWorkflowMetricsSQL("")

	if strings.Contains(sql, "avgIf(1,") {
		t.Errorf("success_rate must not be avgIf(1, ...): it can only ever yield 1 or NaN, got:\n%s", sql)
	}
	want := "avg(" + otelstatus.IsOK(workflowStatusColumn) + ") as success_rate"
	if !strings.Contains(sql, want) {
		t.Errorf("success_rate must be %q, got:\n%s", want, sql)
	}
}

func TestWorkflowMetricsSQLKeepsWhereClause(t *testing.T) {
	where := "WHERE wm.WorkflowId = ? AND t.Timestamp >= ?"
	sql := buildWorkflowMetricsSQL(where)
	if !strings.Contains(sql, where) {
		t.Fatalf("built SQL dropped the caller's WHERE clause %q:\n%s", where, sql)
	}
	if !strings.Contains(sql, "GROUP BY wm.WorkflowId, wm.WorkflowType, wm.WorkflowName") {
		t.Fatalf("built SQL lost its GROUP BY:\n%s", sql)
	}
}
