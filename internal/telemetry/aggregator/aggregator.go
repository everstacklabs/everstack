// Package aggregator provides background jobs that pre-aggregate trace data
// from otel_traces into materialized summary tables (trace_sessions, trace_users, trace_metrics_hourly).
package aggregator

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// sessionIDExpr / userIDExpr coalesce the session/user id across the
// Everstack-native key, OTel GenAI, and OpenInference, so spans ingested via any
// SDK group into the same session/user in the Sessions / Users views. Keep the
// key lists in sync with internal/query/handlers/traces/semconv.go
// (sessionAttrs / userAttrs), the canonical source used on the read path.
const (
	sessionIDExpr = `coalesce(nullIf(SpanAttributes['trace.session_id'], ''), nullIf(SpanAttributes['gen_ai.conversation.id'], ''), nullIf(SpanAttributes['session.id'], ''), nullIf(SpanAttributes['session_id'], ''), nullIf(SpanAttributes['agent.session.id'], ''))`
	userIDExpr    = `coalesce(nullIf(SpanAttributes['trace.user_id'], ''), nullIf(SpanAttributes['user.id'], ''), nullIf(SpanAttributes['user_id'], ''))`
)

// Aggregator runs periodic aggregation jobs against ClickHouse
type Aggregator struct {
	conn     clickhouse.Conn
	interval time.Duration
	stopCh   chan struct{}
}

// New creates an Aggregator with the given ClickHouse native connection
func New(conn clickhouse.Conn) *Aggregator {
	return &Aggregator{
		conn:     conn,
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
}

// NewWithInterval creates an Aggregator with a custom interval (useful for testing)
func NewWithInterval(conn clickhouse.Conn, interval time.Duration) *Aggregator {
	return &Aggregator{
		conn:     conn,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the aggregation loop in a background goroutine.
// It runs immediately on startup, then at the configured interval.
func (a *Aggregator) Start(ctx context.Context) {
	go a.run(ctx)
	logger.WithFields("interval", a.interval.String()).Info("observability aggregator started")
}

// Stop signals the aggregation loop to exit
func (a *Aggregator) Stop() {
	close(a.stopCh)
	logger.Info("observability aggregator stopped")
}

func (a *Aggregator) run(ctx context.Context) {
	// Run immediately on startup
	a.runAll(ctx)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.runAll(ctx)
		}
	}
}

func (a *Aggregator) runAll(ctx context.Context) {
	start := time.Now()

	if err := a.RunSessionAggregation(ctx); err != nil {
		logger.WithFields("error", err.Error()).Error("session aggregation failed")
	}

	if err := a.RunUserAggregation(ctx); err != nil {
		logger.WithFields("error", err.Error()).Error("user aggregation failed")
	}

	// NOTE: RunMetricsBackfill is NOT called here. The materialized view
	// (mv_trace_metrics) handles all new data automatically on INSERT.
	// Calling the backfill repeatedly would cause SummingMergeTree to
	// multiply values (it sums, it does NOT deduplicate).
	// Use RunMetricsBackfill only as a one-time migration for historical data.

	logger.WithFields("elapsed_ms", time.Since(start).Milliseconds()).Debug("aggregation cycle completed")
}

// RunSessionAggregation queries otel_traces grouped by session ID and upserts into trace_sessions.
// It looks at traces from the last 24 hours to capture recent session activity.
func (a *Aggregator) RunSessionAggregation(ctx context.Context) error {
	// We aggregate traces from the last 24 hours to keep sessions fresh.
	// ReplacingMergeTree with updated_at handles deduplication.
	lookback := time.Now().Add(-24 * time.Hour)

	sqlQuery := fmt.Sprintf(`
		INSERT INTO trace_sessions
			(tenant_id, session_id, user_id, first_trace_at, last_trace_at,
			 trace_count, total_duration_ns, total_input_tokens, total_output_tokens,
			 total_cost, error_count, models, tags, environment, kinds, updated_at)
		SELECT
			-- Bridge: prefer the per-span tenant.id (canonical), fall back to
			-- the resource-level one for spans emitted before the
			-- tenantSpanProcessor was wired in. See
			-- internal/query/handlers/traces/tenant_filter.go for rationale.
			coalesce(
				nullIf(anyIf(SpanAttributes['tenant.id'], SpanAttributes['tenant.id'] != ''), ''),
				anyIf(ResourceAttributes['tenant.id'], ResourceAttributes['tenant.id'] != '')
			) as tenant_id,
			session_id,
			anyIf(%s, %s != '') as user_id,
			min(Timestamp) as first_trace_at,
			max(Timestamp) as last_trace_at,
			toUInt32(count(DISTINCT TraceId)) as trace_count,
			toUInt64(sum(toInt64(Duration))) as total_duration_ns,
			toUInt64(sum(toInt64OrZero(SpanAttributes['llm.tokens.input']))) as total_input_tokens,
			toUInt64(sum(toInt64OrZero(SpanAttributes['llm.tokens.output']))) as total_output_tokens,
			sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total']))) as total_cost,
			toUInt32(countIf(`+otelstatus.IsError(otelstatus.Column)+`)) as error_count,
			groupUniqArray(SpanAttributes['model.requested']) as models,
			[] as tags,
			anyIf(ResourceAttributes['deployment.environment'], ResourceAttributes['deployment.environment'] != '') as environment,
			-- Per-session execution kinds, derived from each trace's root span so
			-- the Sessions view shows agent/workflow/sandbox/llm/combos. Mirrors
			-- the traces list trace_kinds. Root-only (ParentSpanId = '') already,
			-- so each row is one trace's root.
			arrayFilter(x -> x != '', groupUniqArray(multiIf(
				SpanName LIKE 'workflow.%%', 'workflow',
				SpanName LIKE 'agent.%%', 'agent',
				SpanName LIKE 'sandbox.%%', 'sandbox',
				SpanName LIKE 'browser.%%', 'browser',
				SpanName LIKE 'memory.%%', 'memory',
				SpanName LIKE 'vector.%%', 'memory',
				SpanName LIKE 'harness.%%', 'harness',
				SpanName IN ('gateway.chat.completion', 'gateway.embeddings'), 'llm',
				SpanName LIKE 'provider.%%', 'llm',
				''
			))) as kinds,
			now64(3) as updated_at
		FROM otel_traces
		WHERE
			ParentSpanId = ''
			AND %s != ''
			AND Timestamp >= ?
		GROUP BY %s as session_id
		HAVING session_id != ''
	`, userIDExpr, userIDExpr, sessionIDExpr, sessionIDExpr)

	if err := a.conn.Exec(ctx, sqlQuery, lookback); err != nil {
		return fmt.Errorf("session aggregation INSERT failed: %w", err)
	}

	return nil
}

// RunMetricsBackfill backfills trace_metrics_hourly from raw otel_traces.
//
// Mirrors the current mv_trace_metrics definition
// (metrics_split_kinds_20260510010630/up.sql): request_count and
// sum_duration_ns count gateway.chat.completion + gateway.embeddings only;
// agent.turn.% feeds agent_turn_count + sum_agent_turn_duration_ns; tokens
// and cost come from provider.% spans. Keep these in sync with the MV — if
// they drift, backfilled hours will diverge from live ingest.
//
// tenant_id uses the bridge form to recover spans emitted before
// internal/telemetry/tenant_span_processor.go was wired in (which only
// carry tenant.id on the resource). The empty-string guard on the
// SpanAttribute is mandatory: without it, a span deliberately stamped
// for tenant A would also count toward tenant B in shared-mode
// deployments where the resource happens to be B. See
// internal/query/handlers/traces/tenant_filter.go for full rationale.
//
// Safe to re-run: SummingMergeTree merges duplicate keys.
//
// NOT called from runAll — only invoke as a one-time op when historical
// metrics need recovery. Repeated automatic runs would multiply existing
// values (sum on every backfill).
func (a *Aggregator) RunMetricsBackfill(ctx context.Context) error {
	lookback := time.Now().Add(-24 * time.Hour)

	sqlQuery := `
		INSERT INTO trace_metrics_hourly
		SELECT
			coalesce(
				nullIf(SpanAttributes['tenant.id'], ''),
				ResourceAttributes['tenant.id']
			) as tenant_id,
			toStartOfHour(Timestamp) as period,
			coalesce(
				nullIf(SpanAttributes['model.served'], ''),
				nullIf(SpanAttributes['llm.response.model'], ''),
				nullIf(SpanAttributes['model.requested'], ''),
				nullIf(SpanAttributes['llm.request.model'], ''),
				''
			) as model,
			if(SpanAttributes['provider'] != '' AND SpanAttributes['provider'] != 'unknown',
			   SpanAttributes['provider'], '') as provider,
			ResourceAttributes['deployment.environment'] as environment,

			-- Gateway request count: only chat-completion + embeddings root spans.
			toUInt64(if(
				SpanName IN ('gateway.chat.completion', 'gateway.embeddings'),
				1, 0
			)) as request_count,

			-- Gateway error count: same set, gated on an error span status.
			toUInt64(if(
				SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
				AND ` + otelstatus.IsError(otelstatus.Column) + `,
				1, 0
			)) as error_count,

			-- Tokens: provider spans only.
			toUInt64(if(SpanName LIKE 'provider.%',
				toInt64OrZero(SpanAttributes['llm.tokens.input']), 0
			)) as total_input_tokens,

			toUInt64(if(SpanName LIKE 'provider.%',
				toInt64OrZero(SpanAttributes['llm.tokens.output']), 0
			)) as total_output_tokens,

			-- Cost: provider spans only.
			if(SpanName LIKE 'provider.%',
				greatest(
					toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
					toFloat64OrZero(SpanAttributes['llm.cost.total'])
				), 0
			) as total_cost,

			-- Gateway latency: chat-completion + embeddings only.
			toUInt64(if(
				SpanName IN ('gateway.chat.completion', 'gateway.embeddings'),
				toInt64(Duration), 0
			)) as sum_duration_ns,

			-- Agent turn count: one row per agent loop turn.
			toUInt64(if(SpanName LIKE 'agent.turn.%', 1, 0)) as agent_turn_count,

			-- Agent turn latency: separate column to keep gateway latency clean.
			toUInt64(if(SpanName LIKE 'agent.turn.%', toInt64(Duration), 0)) as sum_agent_turn_duration_ns

		FROM otel_traces
		WHERE Timestamp >= ?
		  AND (SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
			   OR SpanName LIKE 'agent.turn.%'
			   OR SpanName LIKE 'provider.%')
	`

	if err := a.conn.Exec(ctx, sqlQuery, lookback); err != nil {
		return fmt.Errorf("metrics backfill INSERT failed: %w", err)
	}

	return nil
}

// RunUserAggregation queries otel_traces grouped by user ID and upserts into trace_users.
// It looks at traces from the last 24 hours to capture recent user activity.
func (a *Aggregator) RunUserAggregation(ctx context.Context) error {
	lookback := time.Now().Add(-24 * time.Hour)

	sqlQuery := fmt.Sprintf(`
		INSERT INTO trace_users
		SELECT
			-- Bridge: see RunSessionAggregation comment.
			coalesce(
				nullIf(anyIf(SpanAttributes['tenant.id'], SpanAttributes['tenant.id'] != ''), ''),
				anyIf(ResourceAttributes['tenant.id'], ResourceAttributes['tenant.id'] != '')
			) as tenant_id,
			user_id,
			min(Timestamp) as first_seen,
			max(Timestamp) as last_seen,
			toUInt32(uniq(%s)) as session_count,
			toUInt32(count(DISTINCT TraceId)) as trace_count,
			toUInt64(sum(toInt64OrZero(SpanAttributes['llm.tokens.total']))) as total_tokens,
			sum(greatest(toFloat64OrZero(SpanAttributes['cost.estimated_usd']), toFloat64OrZero(SpanAttributes['llm.cost.total']))) as total_cost,
			countIf(`+otelstatus.IsError(otelstatus.Column)+`) / greatest(count(), 1) as error_rate,
			toUInt64(avg(toInt64(Duration))) as avg_latency_ns,
			now64(3) as updated_at
		FROM otel_traces
		WHERE
			ParentSpanId = ''
			AND %s != ''
			AND Timestamp >= ?
		GROUP BY %s as user_id
		HAVING user_id != ''
	`, sessionIDExpr, userIDExpr, userIDExpr)

	if err := a.conn.Exec(ctx, sqlQuery, lookback); err != nil {
		return fmt.Errorf("user aggregation INSERT failed: %w", err)
	}

	return nil
}
