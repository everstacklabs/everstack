package traces

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// verdictGroupByExpr maps a canonical group_by dimension to a ClickHouse
// expression evaluated against `trace_details_view tdv`. Returning a string
// keeps the SQL composable; the column key is the dimension name itself.
func verdictGroupByExpr(dim string) (string, bool) {
	switch dim {
	case "model":
		// Prefer Model (llm.model), fall back to RequestedModel for early-stage spans.
		return "if(tdv.Model != '', tdv.Model, tdv.RequestedModel)", true
	case "provider":
		return "tdv.Provider", true
	case "prompt_template_id":
		return "tdv.SpanAttributes['prompt.template_id']", true
	case "prompt_version":
		return "tdv.SpanAttributes['prompt.version']", true
	case "tool_name":
		return "tdv.SpanAttributes['tool.name']", true
	}
	return "", false
}

// OutcomeDashboardHandler queries aggregated outcome KPIs from otel_trace_scores.
type OutcomeDashboardHandler struct {
	conn clickhouse.Conn
}

func NewOutcomeDashboardHandler(conn clickhouse.Conn) *OutcomeDashboardHandler {
	return &OutcomeDashboardHandler{conn: conn}
}

func (h *OutcomeDashboardHandler) QueryType() string {
	return "GetOutcomeDashboard"
}

func (h *OutcomeDashboardHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	oq, ok := q.(*OutcomeDashboardQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for OutcomeDashboardHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return OutcomeDashboardResult{}, nil
	}

	whereConditions := []string{"Timestamp >= ?", "Timestamp <= ?", "Source = 'EVAL'", "Environment = ?"}
	args := []interface{}{oq.StartTime, oq.EndTime, tenantID}

	if oq.AgentID != "" {
		whereConditions = append(whereConditions, "ObservationId = ?")
		args = append(args, oq.AgentID)
	}

	where := joinConditions(whereConditions)

	// ── KPI aggregates ──────────────────────────────────────────────
	kpiSQL := fmt.Sprintf(`
		SELECT
			countIf(Name = 'task_completion.finished' AND BooleanValue = 1)
				/ greatest(countIf(Name = 'task_completion.finished'), 1) as task_completion_rate,
			ifNotFinite(avgIf(NumericValue, Name = 'tool_quality.success_rate'), 0)   as tool_success_rate,
			countIf(Name = 'policy.compliant' AND BooleanValue = 1)
				/ greatest(countIf(Name = 'policy.compliant'), 1)          as policy_compliance_rate,
			1 - (countIf(Name = 'loop_health.looping' AND BooleanValue = 1)
				/ greatest(countIf(Name = 'loop_health.looping'), 1))      as loop_health_rate,
			ifNotFinite(avgIf(NumericValue, Name = 'task_completion.efficiency'), 0)  as iteration_efficiency,
			ifNotFinite(avgIf(NumericValue, Name = 'sandbox_hygiene.exit_code_rate'), 0) as sandbox_success_rate,
			uniqExact(TraceId)                                                     as total_evaluations,
			uniqExactIf(JSONExtractString(Metadata, 'session_id'),
				JSONExtractString(Metadata, 'session_id') != '')           as unique_sessions
		FROM otel_trace_scores
		WHERE %s
	`, where)

	rows, err := h.conn.Query(ctx, kpiSQL, args...)
	if err != nil {
		logger.WithError(err).Error("outcome dashboard: KPI query failed")
		return nil, fmt.Errorf("outcome dashboard KPI query: %w", err)
	}
	defer rows.Close()

	result := OutcomeDashboardResult{}
	if rows.Next() {
		if err := rows.Scan(
			&result.TaskCompletionRate,
			&result.ToolSuccessRate,
			&result.PolicyComplianceRate,
			&result.LoopHealthRate,
			&result.IterationEfficiency,
			&result.SandboxSuccessRate,
			&result.TotalEvaluations,
			&result.UniqueSessions,
		); err != nil {
			logger.WithError(err).Error("outcome dashboard: KPI scan failed")
			return nil, fmt.Errorf("outcome dashboard KPI scan: %w", err)
		}
	}
	rows.Close()

	// ── Per-score breakdown ─────────────────────────────────────────
	scoreSQL := fmt.Sprintf(`
		SELECT
			Name                                                              as score_name,
			DataType                                                          as data_type,
			count()                                                           as cnt,
			ifNotFinite(avg(NumericValue), 0)                                 as mean,
			ifNotFinite(min(NumericValue), 0)                                 as mn,
			ifNotFinite(max(NumericValue), 0)                                 as mx,
			ifNotFinite(quantile(0.50)(NumericValue), 0)                      as p50,
			ifNotFinite(quantile(0.95)(NumericValue), 0)                      as p95,
			countIf(BooleanValue = 1) / greatest(count(), 1)                  as pass_rate
		FROM otel_trace_scores
		WHERE %s
		GROUP BY Name, DataType
		ORDER BY score_name ASC
	`, where)

	scoreRows, err := h.conn.Query(ctx, scoreSQL, args...)
	if err != nil {
		logger.WithError(err).Error("outcome dashboard: scores query failed")
		return nil, fmt.Errorf("outcome dashboard scores query: %w", err)
	}
	defer scoreRows.Close()

	for scoreRows.Next() {
		var s OutcomeScoreSummary
		if err := scoreRows.Scan(
			&s.ScoreName, &s.DataType, &s.Count,
			&s.Mean, &s.Min, &s.Max, &s.P50, &s.P95, &s.PassRate,
		); err != nil {
			logger.WithError(err).Warn("outcome dashboard: score row scan failed")
			continue
		}
		result.Scores = append(result.Scores, s)
	}

	if result.Scores == nil {
		result.Scores = []OutcomeScoreSummary{}
	}

	// ── Overall fix_attempt_verdict rates ───────────────────────────
	// Verdicts are categorical strings, so we look at StringValue.
	// Use a separate WHERE block (drop Source = 'EVAL' filter) so that
	// API/ANNOTATION verdicts also count — verdicts are typically labeled
	// by humans or test harnesses, not the autoscorer.
	verdictWhere, verdictArgs := h.buildVerdictWhere(oq.StartTime, oq.EndTime, tenantID, oq.AgentID)
	overallSQL := fmt.Sprintf(`
		SELECT
			countIf(StringValue = '%s')      as wins,
			countIf(StringValue = '%s')      as fails,
			countIf(StringValue = '%s')      as draws,
			countIf(StringValue = '%s')      as no_changes,
			count()                          as total
		FROM otel_trace_scores
		WHERE %s
	`, scores.VerdictWin, scores.VerdictFail, scores.VerdictDraw, scores.VerdictNoChange, verdictWhere)

	overallRows, err := h.conn.Query(ctx, overallSQL, verdictArgs...)
	if err != nil {
		logger.WithError(err).Warn("outcome dashboard: verdict overall query failed")
	} else {
		defer overallRows.Close()
		if overallRows.Next() {
			var wins, fails, draws, noChanges, total uint64
			if scanErr := overallRows.Scan(&wins, &fails, &draws, &noChanges, &total); scanErr == nil {
				result.VerdictRates = computeVerdictRates(wins, fails, draws, noChanges, total)
			}
		}
		overallRows.Close()
	}

	// ── Per-dimension verdict breakdowns ────────────────────────────
	for _, dim := range oq.GroupBy {
		expr, ok := verdictGroupByExpr(dim)
		if !ok {
			continue
		}
		breakdown, err := h.queryVerdictBreakdown(ctx, dim, expr, oq.StartTime, oq.EndTime, tenantID, oq.AgentID)
		if err != nil {
			logger.WithFields("dimension", dim).WithError(err).Warn("outcome dashboard: verdict breakdown query failed")
			continue
		}
		result.VerdictBreakdowns = append(result.VerdictBreakdowns, breakdown)
	}

	return result, nil
}

// buildVerdictWhere builds the WHERE clause + args for fix_attempt_verdict
// queries. Scoped to tenant via the Environment column the rest of this file
// already uses for tenant isolation, plus optional agent scope.
func (h *OutcomeDashboardHandler) buildVerdictWhere(startTime, endTime interface{}, tenantID, agentID string) (string, []interface{}) {
	conds := []string{
		"Timestamp >= ?",
		"Timestamp <= ?",
		"Environment = ?",
		"Name = '" + scores.FixAttemptVerdictName + "'",
		"DataType = 'CATEGORICAL'",
	}
	args := []interface{}{startTime, endTime, tenantID}
	if agentID != "" {
		conds = append(conds, "ObservationId = ?")
		args = append(args, agentID)
	}
	return joinConditions(conds), args
}

func computeVerdictRates(wins, fails, draws, noChanges, total uint64) VerdictRates {
	if total == 0 {
		return VerdictRates{}
	}
	t := float64(total)
	return VerdictRates{
		WinRate:      float64(wins) / t,
		FailRate:     float64(fails) / t,
		DrawRate:     float64(draws) / t,
		NoChangeRate: float64(noChanges) / t,
		SampleSize:   total,
	}
}

// queryVerdictBreakdown joins otel_trace_scores → trace_details_view on TraceId
// to slice verdicts by the requested dimension. Empty group keys are skipped
// so we don't surface "model=" rows from spans missing the attribute.
func (h *OutcomeDashboardHandler) queryVerdictBreakdown(
	ctx context.Context,
	dim, expr string,
	startTime, endTime interface{},
	tenantID, agentID string,
) (VerdictBreakdown, error) {
	conds := []string{
		"s.Timestamp >= ?",
		"s.Timestamp <= ?",
		"s.Environment = ?",
		"s.Name = '" + scores.FixAttemptVerdictName + "'",
		"s.DataType = 'CATEGORICAL'",
		fmt.Sprintf("%s != ''", expr),
	}
	args := []interface{}{startTime, endTime, tenantID}
	if agentID != "" {
		conds = append(conds, "s.ObservationId = ?")
		args = append(args, agentID)
	}

	sql := fmt.Sprintf(`
		SELECT
			%[1]s                                  as group_key,
			countIf(s.StringValue = '%[2]s')        as wins,
			countIf(s.StringValue = '%[3]s')        as fails,
			countIf(s.StringValue = '%[4]s')        as draws,
			countIf(s.StringValue = '%[5]s')        as no_changes,
			count()                                 as total
		FROM otel_trace_scores AS s
		ANY LEFT JOIN trace_details_view AS tdv ON tdv.TraceId = s.TraceId
		WHERE %[6]s
		GROUP BY group_key
		ORDER BY total DESC
		LIMIT 50
	`, expr,
		scores.VerdictWin, scores.VerdictFail, scores.VerdictDraw, scores.VerdictNoChange,
		joinConditions(conds))

	rows, err := h.conn.Query(ctx, sql, args...)
	if err != nil {
		return VerdictBreakdown{}, fmt.Errorf("verdict breakdown %s: %w", dim, err)
	}
	defer rows.Close()

	breakdown := VerdictBreakdown{Dimension: dim}
	for rows.Next() {
		var groupKey string
		var wins, fails, draws, noChanges, total uint64
		if err := rows.Scan(&groupKey, &wins, &fails, &draws, &noChanges, &total); err != nil {
			logger.WithFields("dimension", dim).WithError(err).Warn("verdict breakdown row scan failed")
			continue
		}
		breakdown.Entries = append(breakdown.Entries, VerdictBreakdownEntry{
			GroupKey: groupKey,
			Rates:    computeVerdictRates(wins, fails, draws, noChanges, total),
		})
	}
	return breakdown, nil
}
