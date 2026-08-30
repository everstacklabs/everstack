package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
	"github.com/google/uuid"
)

const (
	evaluationInterval = 60 * time.Second
	renotifyInterval   = 5 * time.Minute
)

// Evaluator runs a background loop that evaluates alert rules against
// ClickHouse metrics and fires/resolves alerts accordingly.
type Evaluator struct {
	store    AlertStore
	chConn   clickhouse.Conn
	notifier *NotificationRouter
}

// NewEvaluator creates a new alert Evaluator.
func NewEvaluator(store AlertStore, chConn clickhouse.Conn, notifier *NotificationRouter) *Evaluator {
	return &Evaluator{
		store:    store,
		chConn:   chConn,
		notifier: notifier,
	}
}

// Start begins the evaluation loop. It blocks until the context is cancelled.
func (e *Evaluator) Start(ctx context.Context) error {
	logger.Info("alerts: evaluator started")
	ticker := time.NewTicker(evaluationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("alerts: evaluator stopped")
			return nil
		case <-ticker.C:
			e.evaluateAll(ctx)
		}
	}
}

// TestNotificationTarget sends a test message through a notification target.
func (e *Evaluator) TestNotificationTarget(ctx context.Context, target *NotificationTargetRecord) error {
	return e.notifier.SendTestNotification(ctx, target)
}

func (e *Evaluator) evaluateAll(ctx context.Context) {
	rules, err := e.store.ListAllEnabledRules(ctx)
	if err != nil {
		logger.WithError(err).Warn("alerts: failed to list enabled rules")
		return
	}

	for _, rule := range rules {
		if err := e.evaluateRule(ctx, rule); err != nil {
			logger.WithFields("rule", rule.Name).WithError(err).Warn("alerts: evaluation failed")
		}
	}
}

func (e *Evaluator) evaluateRule(ctx context.Context, rule *AlertRuleRecord) error {
	// Event-driven rules are fired by an external producer (e.g. the eval
	// runner's regression notifier), not by this ClickHouse-polling loop.
	// They must stay enabled in the store so that producer can look them up
	// by builtin key, but there is no metric for the evaluator to query —
	// skip them here instead of failing with "unsupported metric".
	if isEventDrivenMetric(rule.Metric) {
		return nil
	}

	metricValue, err := e.queryMetric(ctx, rule)
	if err != nil {
		return fmt.Errorf("query metric %s: %w", rule.Metric, err)
	}

	breached := isBreached(rule.Operator, metricValue, rule.Threshold)
	activeEvent, err := e.store.GetActiveEventForRule(ctx, rule.ID)
	if err != nil {
		return fmt.Errorf("get active event: %w", err)
	}

	if breached {
		if activeEvent == nil {
			// New firing — create event and notify
			delivered := e.notifier.NotifyFiring(ctx, rule, metricValue)
			event := &AlertEventRecord{
				ID:                uuid.New().String(),
				TenantID:          rule.TenantID,
				AlertRuleID:       rule.ID,
				Status:            "firing",
				MetricValue:       metricValue,
				Threshold:         rule.Threshold,
				FiredAt:           time.Now(),
				LastNotifiedAt:    sql.NullTime{Time: time.Now(), Valid: delivered > 0},
				NotificationCount: int32(delivered),
			}
			if err := e.store.CreateAlertEvent(ctx, event); err != nil {
				return fmt.Errorf("create event: %w", err)
			}
		} else if activeEvent.Status == "firing" {
			// Already firing — re-notify if interval elapsed
			if !activeEvent.LastNotifiedAt.Valid || time.Since(activeEvent.LastNotifiedAt.Time) >= renotifyInterval {
				delivered := e.notifier.NotifyFiring(ctx, rule, metricValue)
				activeEvent.MetricValue = metricValue
				if delivered > 0 {
					activeEvent.LastNotifiedAt = sql.NullTime{Time: time.Now(), Valid: true}
					activeEvent.NotificationCount += int32(delivered)
				}
				if err := e.store.UpdateAlertEvent(ctx, activeEvent); err != nil {
					return fmt.Errorf("update event: %w", err)
				}
			}
		}
		// If acknowledged, don't re-notify
	} else if activeEvent != nil {
		// Condition OK but active event exists — auto-resolve
		activeEvent.Status = "resolved"
		activeEvent.ResolvedAt = sql.NullTime{Time: time.Now(), Valid: true}
		if err := e.store.UpdateAlertEvent(ctx, activeEvent); err != nil {
			return fmt.Errorf("resolve event: %w", err)
		}
		e.notifier.NotifyResolved(ctx, rule)
	}

	return nil
}

// isEventDrivenMetric reports whether a rule's metric is fired by an external
// producer rather than evaluated by the polling loop. The "eval." namespace is
// owned by the eval runner (see internal/services/eval_runner/regression_notifier.go),
// which pushes events directly; the evaluator has no ClickHouse query for it.
func isEventDrivenMetric(metric string) bool {
	return strings.HasPrefix(metric, "eval.")
}

func (e *Evaluator) queryMetric(ctx context.Context, rule *AlertRuleRecord) (float64, error) {
	if e.chConn == nil {
		return 0, fmt.Errorf("clickhouse connection not available")
	}

	// Fail closed on missing tenant. Each downstream query path gated
	// the WHERE clause on `rule.TenantID != ""`, which silently dropped
	// the tenant filter when empty — the rule then evaluated against
	// every tenant's traffic and could fire on someone else's data.
	// AlertRuleRecord.TenantID is required by the schema; if it's empty
	// here, the rule was created malformed and we refuse to evaluate it.
	if strings.TrimSpace(rule.TenantID) == "" {
		return 0, fmt.Errorf("alert rule %s has empty tenant_id; refusing to evaluate cross-tenant", rule.ID)
	}

	// Route score-based metrics to otel_trace_scores table
	if strings.HasPrefix(rule.Metric, "score.") {
		return e.queryScoreMetric(ctx, rule)
	}

	// Try pre-aggregated table first; fall back to raw traces for metrics
	// not available in the hourly table (percentiles, cache, carbon, etc.)
	val, err := e.queryTraceMetric(ctx, rule)
	if err != nil && strings.Contains(err.Error(), "unsupported metric for hourly table") {
		return e.queryTraceMetricRaw(ctx, rule)
	}
	return val, err
}

// queryTraceMetric queries metrics from trace_metrics_hourly (pre-aggregated).
func (e *Evaluator) queryTraceMetric(ctx context.Context, rule *AlertRuleRecord) (float64, error) {
	window := time.Duration(rule.DurationSeconds) * time.Second
	now := time.Now()
	start := now.Add(-window)

	// queryMetric guarantees non-empty tenant; bake it into the
	// initial conditions unconditionally so it can never be dropped.
	whereConditions := []string{"period >= toStartOfHour(?)", "period <= ?", "tenant_id = ?"}
	args := []interface{}{start, now, rule.TenantID}

	// Apply dimension filters from JSONB (model, provider, environment, etc.)
	if len(rule.Filters) > 0 {
		var filters map[string]interface{}
		if json.Unmarshal(rule.Filters, &filters) == nil {
			if v, ok := filters["model"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "model = ?")
				args = append(args, v)
			}
			if v, ok := filters["provider"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "provider = ?")
				args = append(args, v)
			}
			if v, ok := filters["environment"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "environment = ?")
				args = append(args, v)
			}
		}
	}

	selectExpr, err := metricsHourlySelectExpr(rule.Metric)
	if err != nil {
		return 0, err
	}

	where := ""
	for i, cond := range whereConditions {
		if i > 0 {
			where += " AND "
		}
		where += cond
	}

	sqlQuery := fmt.Sprintf("SELECT %s FROM trace_metrics_hourly WHERE %s", selectExpr, where)

	rows, err := e.chConn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return 0, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var value float64
	if rows.Next() {
		if err := rows.Scan(&value); err != nil {
			return 0, fmt.Errorf("scan metric: %w", err)
		}
	}

	return value, nil
}

// metricsHourlySelectExpr returns the ClickHouse SELECT expression for a given
// metric key when querying from trace_metrics_hourly.
func metricsHourlySelectExpr(metric string) (string, error) {
	switch metric {
	case "error_rate":
		return "if(sum(request_count) > 0, sum(error_count) / sum(request_count), 0)", nil
	case "error_count":
		return "toFloat64(sum(error_count))", nil
	case "avg_latency_ms":
		return "if(sum(request_count) > 0, sum(sum_duration_ns) / sum(request_count) / 1e6, 0)", nil
	case "request_count":
		return "toFloat64(sum(request_count))", nil
	case "requests_per_minute":
		return "toFloat64(sum(request_count))", nil // approximate: total count in window
	case "total_cost", "hourly_cost":
		return "sum(total_cost)", nil
	case "avg_cost_per_request":
		return "if(sum(request_count) > 0, sum(total_cost) / sum(request_count), 0)", nil
	case "total_tokens", "token_count":
		return "toFloat64(sum(total_input_tokens) + sum(total_output_tokens))", nil
	case "input_tokens":
		return "toFloat64(sum(total_input_tokens))", nil
	case "output_tokens":
		return "toFloat64(sum(total_output_tokens))", nil
	default:
		return "", fmt.Errorf("unsupported metric for hourly table: %s", metric)
	}
}

// queryScoreMetric queries outcome metrics from the otel_trace_scores table.
// Score metrics use the format "score.<aggregation>.<score_name>" where:
//   - aggregation: avg, rate_true, rate_false, count
//   - score_name: the Name field in otel_trace_scores (e.g., "task_completion.finished")
func (e *Evaluator) queryScoreMetric(ctx context.Context, rule *AlertRuleRecord) (float64, error) {
	selectExpr, scoreName, err := scoreMetricSelectExpr(rule.Metric)
	if err != nil {
		return 0, err
	}

	window := time.Duration(rule.DurationSeconds) * time.Second
	now := time.Now()
	start := now.Add(-window)

	// queryMetric guarantees non-empty tenant. Tenant lives in the
	// Environment column for otel_trace_scores (set by the recorder).
	whereConditions := []string{"Timestamp >= ?", "Timestamp <= ?", "Name = ?", "Source = 'EVAL'", "Environment = ?"}
	args := []interface{}{start, now, scoreName, rule.TenantID}

	where := ""
	for i, cond := range whereConditions {
		if i > 0 {
			where += " AND "
		}
		where += cond
	}

	sqlQuery := fmt.Sprintf("SELECT %s FROM otel_trace_scores WHERE %s", selectExpr, where)

	rows, err := e.chConn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return 0, fmt.Errorf("clickhouse score query: %w", err)
	}
	defer rows.Close()

	var value float64
	if rows.Next() {
		if err := rows.Scan(&value); err != nil {
			return 0, fmt.Errorf("scan score metric: %w", err)
		}
	}

	return value, nil
}

// scoreMetricSelectExpr parses a score metric key and returns the ClickHouse
// SELECT expression and the score name to filter on.
//
// Format: score.<aggregation>.<score_name>
//
// Aggregations:
//   - avg:        average of NumericValue (for numeric scores)
//   - rate_true:  percentage of BooleanValue = 1 (for boolean scores)
//   - rate_false: percentage of BooleanValue = 0 (for boolean scores, e.g., failure rate)
//   - count:      total number of score records
//   - min:        minimum NumericValue
//   - max:        maximum NumericValue
func scoreMetricSelectExpr(metric string) (selectExpr string, scoreName string, err error) {
	// Strip "score." prefix
	rest := strings.TrimPrefix(metric, "score.")
	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		return "", "", fmt.Errorf("invalid score metric format %q, expected score.<agg>.<name>", metric)
	}

	agg := rest[:dotIdx]
	scoreName = rest[dotIdx+1:]

	if scoreName == "" {
		return "", "", fmt.Errorf("empty score name in metric %q", metric)
	}

	switch agg {
	case "avg":
		selectExpr = "avg(NumericValue)"
	case "rate_true":
		// Ratio of boolean=true scores: count(BooleanValue=1) / count(*)
		selectExpr = "countIf(BooleanValue = 1) / greatest(count(), 1)"
	case "rate_false":
		// Ratio of boolean=false scores: count(BooleanValue=0) / count(*)
		selectExpr = "countIf(BooleanValue = 0) / greatest(count(), 1)"
	case "count":
		selectExpr = "toFloat64(count())"
	case "min":
		selectExpr = "min(NumericValue)"
	case "max":
		selectExpr = "max(NumericValue)"
	default:
		return "", "", fmt.Errorf("unsupported score aggregation %q in metric %q", agg, metric)
	}

	return selectExpr, scoreName, nil
}

// queryTraceMetricRaw queries metrics directly from otel_traces for metrics not
// available in the pre-aggregated hourly table (percentiles, cache, carbon, etc.).
func (e *Evaluator) queryTraceMetricRaw(ctx context.Context, rule *AlertRuleRecord) (float64, error) {
	window := time.Duration(rule.DurationSeconds) * time.Second
	now := time.Now()
	start := now.Add(-window)

	// queryMetric guarantees non-empty tenant; bake it into the
	// initial conditions unconditionally.
	//
	// Latency/percentile alerts are user-facing — narrow the source set
	// to chat-completion + embeddings so a long-running agent tool
	// loop doesn't push p95 over a customer's threshold and trigger a
	// false page. Agent-turn percentiles get their own metric path
	// when we add it.
	// Tenant predicate uses the bridge form so historical spans emitted
	// before internal/telemetry/tenant_span_processor.go was wired in
	// (which only carry tenant.id on the resource) still match. The
	// empty-string guard on SpanAttributes prevents cross-tenant matches
	// when the gateway's resource happens to be a different tenant —
	// see internal/query/handlers/traces/tenant_filter.go for rationale.
	whereConditions := []string{
		"Timestamp >= ?", "Timestamp <= ?",
		"SpanName IN ('gateway.chat.completion', 'gateway.embeddings')",
		"(SpanAttributes['tenant.id'] = ? OR (SpanAttributes['tenant.id'] = '' AND ResourceAttributes['tenant.id'] = ?))",
	}
	args := []interface{}{start, now, rule.TenantID, rule.TenantID}

	if len(rule.Filters) > 0 {
		var filters map[string]interface{}
		if json.Unmarshal(rule.Filters, &filters) == nil {
			if v, ok := filters["model"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "SpanAttributes['llm.request.model'] = ?")
				args = append(args, v)
			}
			if v, ok := filters["provider"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "SpanAttributes['provider'] = ?")
				args = append(args, v)
			}
			if v, ok := filters["environment"].(string); ok && v != "" {
				whereConditions = append(whereConditions, "ResourceAttributes['deployment.environment'] = ?")
				args = append(args, v)
			}
		}
	}

	selectExpr, err := metricSelectExprRaw(rule.Metric)
	if err != nil {
		return 0, err
	}

	where := ""
	for i, cond := range whereConditions {
		if i > 0 {
			where += " AND "
		}
		where += cond
	}

	sqlQuery := fmt.Sprintf("SELECT %s FROM otel_traces WHERE %s", selectExpr, where)

	rows, err := e.chConn.Query(ctx, sqlQuery, args...)
	if err != nil {
		return 0, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var value float64
	if rows.Next() {
		if err := rows.Scan(&value); err != nil {
			return 0, fmt.Errorf("scan metric: %w", err)
		}
	}
	return value, nil
}

// metricSelectExprRaw returns the ClickHouse SELECT expression for metrics
// that require raw otel_traces data (percentiles, cache, carbon, etc.).
func metricSelectExprRaw(metric string) (string, error) {
	switch metric {
	// ─── Error metrics ───────────────────────────────────────────────
	case "error_rate":
		return "countIf(" + otelstatus.IsError(otelstatus.Column) + ") / greatest(count(), 1)", nil
	case "error_count":
		return "toFloat64(countIf(" + otelstatus.IsError(otelstatus.Column) + "))", nil

	// ─── Latency metrics ─────────────────────────────────────────────
	case "avg_latency_ms":
		return "avg(toInt64(Duration)) / 1e6", nil
	case "p50_latency_ms":
		return "quantile(0.5)(toInt64(Duration)) / 1e6", nil
	case "p95_latency_ms":
		return "quantile(0.95)(toInt64(Duration)) / 1e6", nil
	case "p99_latency_ms":
		return "quantile(0.99)(toInt64(Duration)) / 1e6", nil
	case "max_latency_ms":
		return "max(toInt64(Duration)) / 1e6", nil

	// ─── Throughput metrics ──────────────────────────────────────────
	case "request_count":
		return "toFloat64(count())", nil
	case "requests_per_minute":
		return "toFloat64(count()) / greatest(dateDiff('minute', min(Timestamp), max(Timestamp)), 1)", nil

	// ─── Cost metrics ────────────────────────────────────────────────
	case "total_cost":
		return "sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total'])))", nil
	case "avg_cost_per_request":
		return "sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total']))) / greatest(count(), 1)", nil
	case "cost_savings":
		return "sum(toFloat64OrZero(SpanAttributes['cost.savings_usd']))", nil

	// ─── Token metrics ───────────────────────────────────────────────
	case "total_tokens":
		return "sum(toInt64OrZero(SpanAttributes['llm.tokens.total']))", nil
	case "input_tokens":
		return "sum(toInt64OrZero(SpanAttributes['llm.tokens.input']))", nil
	case "output_tokens":
		return "sum(toInt64OrZero(SpanAttributes['llm.tokens.output']))", nil
	case "avg_tokens_per_request":
		return "sum(toInt64OrZero(SpanAttributes['llm.tokens.total'])) / greatest(count(), 1)", nil

	// ─── Cache metrics ───────────────────────────────────────────────
	case "cache_hit_rate":
		return "countIf(SpanAttributes['cache.hit'] = 'true') / greatest(count(), 1)", nil

	// ─── Carbon metrics ──────────────────────────────────────────────
	case "carbon_emissions":
		return "sum(toFloat64OrZero(SpanAttributes['carbon.saved_grams']))", nil

	// Legacy aliases
	case "hourly_cost":
		return "sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total'])))", nil
	case "token_count":
		return "sum(toInt64OrZero(SpanAttributes['llm.tokens.total']))", nil

	default:
		return "", fmt.Errorf("unsupported metric: %s", metric)
	}
}

func isBreached(operator string, value, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	default:
		return false
	}
}
