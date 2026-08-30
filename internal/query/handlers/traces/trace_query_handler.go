package traces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

// TraceByIDHandler retrieves all spans for a specific trace
type TraceByIDHandler struct {
	conn clickhouse.Conn
}

func NewTraceByIDHandler(conn clickhouse.Conn) *TraceByIDHandler {
	return &TraceByIDHandler{
		conn: conn,
	}
}

func (h *TraceByIDHandler) QueryType() string {
	return "GetTraceByID"
}

func (h *TraceByIDHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	traceQuery, ok := q.(*GetTraceByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for TraceByIDHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — single-row trace lookup must be tenant-scoped
		// or it will return foreign rows by trace_id collision.
		return nil, sql.ErrNoRows
	}
	tenantClause, tenantArgs := tenantBridgeFilter(tenantID)
	tenantFilter := "AND " + tenantClause
	queryArgs := append([]interface{}{traceQuery.TraceID}, tenantArgs...)

	sqlQuery := fmt.Sprintf(`
		SELECT
			TraceId,
			SpanId,
			ParentSpanId,
			SpanName,
			SpanKind,
			Timestamp,
			toInt64(Duration) as Duration,
			StatusCode,
			StatusMessage,
			SpanAttributes,
			ResourceAttributes,
			Events.Timestamp as EventTimestamps,
			Events.Name as EventNames,
			Events.Attributes as EventAttributes
		FROM otel_traces
		WHERE TraceId = ? %s
		ORDER BY Timestamp ASC
	`, tenantFilter)

	rows, err := h.conn.Query(ctx, sqlQuery, queryArgs...)
	if err != nil {
		logger.WithFields(
			"trace_id", traceQuery.TraceID,
			"error", err.Error(),
		).Error("failed to query trace by ID")
		return nil, fmt.Errorf("failed to query trace: %w", err)
	}
	defer rows.Close()

	var spans []query.SpanReadModel
	for rows.Next() {
		var span query.SpanReadModel
		var eventTimestamps []time.Time
		var eventNames []string
		var eventAttributes []map[string]string
		if err := rows.Scan(
			&span.TraceID,
			&span.SpanID,
			&span.ParentSpanID,
			&span.SpanName,
			&span.SpanKind,
			&span.Timestamp,
			&span.Duration,
			&span.StatusCode,
			&span.StatusMessage,
			&span.SpanAttributes,
			&span.ResourceAttributes,
			&eventTimestamps,
			&eventNames,
			&eventAttributes,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan span row")
			return nil, fmt.Errorf("failed to scan span: %w", err)
		}

		// Parse span events (mirrors TraceTreeHandler / DetailedTraceHandler).
		// The detail panel reads selectedSpan via GetTraceByID, so without
		// these columns the Events tab always rendered "No events recorded"
		// even though events are stored and other handlers return them.
		for i := range eventNames {
			event := query.SpanEvent{Name: eventNames[i]}
			if i < len(eventTimestamps) {
				event.Timestamp = eventTimestamps[i]
			}
			if i < len(eventAttributes) {
				event.Attributes = eventAttributes[i]
			}
			span.Events = append(span.Events, event)
		}

		spans = append(spans, span)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating spans: %w", err)
	}

	if spans == nil {
		return nil, sql.ErrNoRows
	}

	return spans, nil
}

// ListTracesHandler searches traces with filtering and pagination
type ListTracesHandler struct {
	conn clickhouse.Conn
}

func NewListTracesHandler(conn clickhouse.Conn) *ListTracesHandler {
	return &ListTracesHandler{
		conn: conn,
	}
}

func (h *ListTracesHandler) QueryType() string {
	return "ListTraces"
}

func (h *ListTracesHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	listQuery, ok := q.(*ListTracesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for ListTracesHandler")
	}

	// Use tenant from context if not explicitly set in the query.
	// Fail closed when missing — a list query without tenant scoping
	// returned every tenant's traces, which is what surfaced in the
	// admin Logs page as "16 requests / 69.5K tokens" on a freshly
	// created tenant.
	tenantID := listQuery.TenantID
	if tenantID == "" {
		tenantID = database.TenantSchemaFromContext(ctx)
	}
	if tenantID == "" {
		return []query.TraceReadModel{}, nil
	}

	// Build dynamic WHERE clause — tenant predicate is mandatory.
	listTenantClause, listTenantArgs := tenantBridgeFilter(tenantID)
	whereConditions := []string{listTenantClause}
	args := append([]interface{}{}, listTenantArgs...)

	// Fold tenant semantic mappings into the coalesce for each typed field, so a
	// tenant's own attribute names populate the built-in columns. With no
	// mappings these are identical to the default coalesce fragments.
	modelFrag := modelSQL(listQuery.extraKeys("model")...)
	providerFrag := providerSQL(listQuery.extraKeys("provider")...)
	sessionFrag := sessionSQL(listQuery.extraKeys("session")...)
	userFrag := userSQL(listQuery.extraKeys("user")...)
	inputFrag := traceInputSQL(listQuery.extraKeys("input")...)
	outputFrag := traceOutputSQL(listQuery.extraKeys("output")...)
	costFrag := costSQL(listQuery.extraKeys("cost")...)
	inputTokensFrag := inputTokensSQL(listQuery.extraKeys("input_tokens")...)
	outputTokensFrag := outputTokensSQL(listQuery.extraKeys("output_tokens")...)
	totalTokensFrag := totalTokensSQL(listQuery.extraKeys("total_tokens")...)

	if !listQuery.StartTime.IsZero() {
		whereConditions = append(whereConditions, "Timestamp >= ?")
		args = append(args, listQuery.StartTime)
	}

	if !listQuery.EndTime.IsZero() {
		whereConditions = append(whereConditions, "Timestamp <= ?")
		args = append(args, listQuery.EndTime)
	}

	// Live tail: keep only traces touched since ActiveSince, but let each one
	// aggregate over the whole range above. See ListTracesQuery.ActiveSince for
	// why this is a TraceId subquery rather than a narrower StartTime.
	if !listQuery.ActiveSince.IsZero() {
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND Timestamp >= ?)", subTC))
		args = append(args, subTA...)
		args = append(args, listQuery.ActiveSince)
	}

	if listQuery.Model != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("(SpanAttributes['model.requested'] = ? OR SpanAttributes['model.served'] = ? OR %s = ?)", modelFrag))
		args = append(args, listQuery.Model, listQuery.Model, listQuery.Model)
	}

	if listQuery.Provider != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("%s = ?", providerFrag))
		args = append(args, listQuery.Provider)
	}

	if listQuery.CorrelationID != "" {
		// Use subquery to find traces where ANY span has the correlation_id
		// This is needed because correlation_id may only exist on specific spans (e.g., gateway span)
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND SpanAttributes['correlation_id'] = ?)", subTC))
		args = append(args, subTA...)
		args = append(args, listQuery.CorrelationID)
	}

	// Multi-dimension filters (P0.3)
	if listQuery.FilterUserID != "" {
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s = ?)", subTC, userFrag))
		args = append(args, subTA...)
		args = append(args, listQuery.FilterUserID)
	}

	if listQuery.FilterSessionID != "" {
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND %s = ?)", subTC, sessionFrag))
		args = append(args, subTA...)
		args = append(args, listQuery.FilterSessionID)
	}

	if listQuery.FilterThreadID != "" {
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND SpanAttributes['trace.thread_id'] = ?)", subTC))
		args = append(args, subTA...)
		args = append(args, listQuery.FilterThreadID)
	}

	// Arbitrary span-attribute predicates. The mapKeys / mapValues
	// bloom-filter skip indexes on otel_traces (idx_span_attr_key /
	// idx_span_attr_value, see otel_telemetry_init_20251016000000) prune
	// granules so this stays cheap even with several predicates.
	for _, pred := range listQuery.Metadata {
		k, v, ok := splitMetadataPredicate(pred)
		if !ok {
			continue
		}
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf("TraceId IN (SELECT DISTINCT TraceId FROM otel_traces WHERE %s AND SpanAttributes[?] = ?)", subTC))
		args = append(args, subTA...)
		args = append(args, k, v)
	}

	if listQuery.FullTextQuery != "" {
		// Full-text substring search across the IO payload columns.
		// LIKE %substring% on a tokenbf_v1-indexed Map value column lets
		// ClickHouse skip granules — confirmed against
		// `idx_trace_input_tokens` etc. in
		// trace_fulltext_indexes_20260511003437. Multi-semconv: cover the
		// Everstack-native and OpenInference IO attributes plus the LLM
		// request/response payload attributes.
		subTC, subTA := tenantBridgeFilter(tenantID)
		whereConditions = append(whereConditions, fmt.Sprintf(`TraceId IN (
			SELECT DISTINCT TraceId FROM otel_traces
			WHERE %s AND (
			    SpanAttributes['trace.input']           LIKE concat('%%', ?, '%%')
			   OR SpanAttributes['trace.output']          LIKE concat('%%', ?, '%%')
			   OR SpanAttributes['input.value']           LIKE concat('%%', ?, '%%')
			   OR SpanAttributes['output.value']          LIKE concat('%%', ?, '%%')
			   OR SpanAttributes['llm.request.messages']  LIKE concat('%%', ?, '%%')
			   OR SpanAttributes['llm.response.choices']  LIKE concat('%%', ?, '%%')
			)
		)`, subTC))
		args = append(args, subTA...)
		for i := 0; i < 6; i++ {
			args = append(args, listQuery.FullTextQuery)
		}
	}

	if listQuery.Environment != "" {
		whereConditions = append(whereConditions, "ResourceAttributes['deployment.environment'] = ?")
		args = append(args, listQuery.Environment)
	}

	if len(listQuery.Tags) > 0 {
		for _, tag := range listQuery.Tags {
			whereConditions = append(whereConditions, "SpanAttributes['trace.tags'] LIKE ?")
			args = append(args, "%"+tag+"%")
		}
	}

	if listQuery.StatusCode != "" {
		whereConditions = append(whereConditions, "StatusCode = ?")
		args = append(args, listQuery.StatusCode)
	}

	if len(listQuery.Clauses) > 0 {
		clauseConds, clauseArgs, err := compileClauses(listQuery.Clauses, tenantID)
		if err != nil {
			return nil, err
		}
		whereConditions = append(whereConditions, clauseConds...)
		args = append(args, clauseArgs...)
	}

	// Build HAVING clauses for post-aggregation filters (cost, duration)
	havingConditions := []string{}
	havingArgs := []interface{}{}

	if listQuery.MinCost != nil {
		havingConditions = append(havingConditions, "total_cost >= ?")
		havingArgs = append(havingArgs, *listQuery.MinCost)
	}
	if listQuery.MaxCost != nil {
		havingConditions = append(havingConditions, "total_cost <= ?")
		havingArgs = append(havingArgs, *listQuery.MaxCost)
	}
	if listQuery.MinDurationNs != nil {
		havingConditions = append(havingConditions, "total_duration >= ?")
		havingArgs = append(havingArgs, *listQuery.MinDurationNs)
	}
	if listQuery.MaxDurationNs != nil {
		havingConditions = append(havingConditions, "total_duration <= ?")
		havingArgs = append(havingArgs, *listQuery.MaxDurationNs)
	}

	havingClause := ""
	if len(havingConditions) > 0 {
		havingClause = "HAVING " + joinConditions(havingConditions)
	}

	// User-defined attribute columns: project each as a map entry so its value
	// surfaces from whichever span carries it. Keys are validated identifiers
	// (safe to inline as map keys); refs are bound as parameters, so there is no
	// injection surface. These params sit in the SELECT, ahead of the WHERE
	// params, so they are prepended to args below.
	customAttrProjection := ""
	var customAttrSelectArgs []interface{}
	if len(listQuery.CustomAttrColumns) > 0 {
		pairs := make([]string, 0, len(listQuery.CustomAttrColumns))
		for _, c := range listQuery.CustomAttrColumns {
			pairs = append(pairs, fmt.Sprintf("'%s', max(SpanAttributes[?])", c.Key))
			customAttrSelectArgs = append(customAttrSelectArgs, c.Ref)
		}
		customAttrProjection = ", map(" + strings.Join(pairs, ", ") + ") as custom_attr_columns"
	}

	// Tenant classification rules extend the built-in trace_kinds array. Pattern
	// is bound (?), Kind is validated + inlined. These SELECT params come before
	// the custom-attribute params (the kinds array is earlier in the SELECT).
	classificationElements := ""
	var classificationSelectArgs []interface{}
	for _, rule := range listQuery.ClassificationRules {
		classificationElements += fmt.Sprintf(", if(max(SpanName LIKE ?), '%s', '')", rule.Kind)
		classificationSelectArgs = append(classificationSelectArgs, rule.Pattern)
	}

	// Build final query from otel_traces directly with aggregation (includes rich trace fields)
	// Note: Rich fields (input/output/params) extracted only from root spans to minimize overhead
	// Note: toInt64() cast on Duration ensures compatibility with both Int64 and UInt64 schemas
	sqlQuery := fmt.Sprintf(`
		SELECT
			TraceId,
			min(Timestamp) as start_time,
			max(Timestamp) as end_time,
			-- Root span's Duration is the canonical trace duration, but an
			-- in-flight trace has no root yet (the root is emitted when the
			-- work finishes), and maxIf over an empty set is 0. Falling back to
			-- observed wall-clock keeps a live trace from rendering as 0s in
			-- the list while its detail view shows the real elapsed time.
			toInt64(if(
				countIf(ParentSpanId = '') > 0,
				maxIf(Duration, ParentSpanId = ''),
				max(toUnixTimestamp64Nano(Timestamp) + Duration) - min(toUnixTimestamp64Nano(Timestamp))
			)) as total_duration,
			countIf(`+otelstatus.IsError(otelstatus.Column)+`) as error_count,
			maxIf(StatusCode, ParentSpanId = '') as root_status,
			anyIf(SpanAttributes['model.requested'], SpanAttributes['model.requested'] != '') as requested_model,
			argMaxIf(coalesce(nullIf(SpanAttributes['model.served'], ''), nullIf(SpanAttributes['model.resolved'], ''), ''), Timestamp, SpanAttributes['model.served'] != '' OR SpanAttributes['model.resolved'] != '') as served_model,
			%s as llm_model,
			%s as provider,
			any(SpanAttributes['tenant.id']) as tenant_id,
			count(*) as span_count,
			argMinIf(%s, Timestamp, %s != '') as trace_input,
			argMaxIf(%s, Timestamp, %s != '') as trace_output,
			%s as user_id,
			%s as session_id,
			coalesce(nullIf(maxIf(SpanAttributes['trace.thread_id'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['trace.thread_id'], SpanAttributes['trace.thread_id'] != ''), ''), '') as thread_id,
			sum(%s) as total_cost,
			sum(toFloat64OrZero(SpanAttributes['cost.savings_usd'])) as total_savings,
			sum(toFloat64OrZero(SpanAttributes['carbon.saved_grams'])) as total_carbon_saved,
			coalesce(nullIf(maxIf(SpanAttributes['llm.request.model_parameters'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['llm.request.model_parameters'], SpanAttributes['llm.request.model_parameters'] != ''), ''), '') as model_parameters,
			sum(%s) as input_tokens,
			sum(%s) as output_tokens,
			sum(%s) as total_tokens,
			sum(coalesce(nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cache_read']), 0), nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cached']), 0), nullIf(toInt64OrZero(SpanAttributes['cache_read_tokens']), 0), 0)) as cached_tokens,
			sum(toInt64OrZero(SpanAttributes['llm.tokens.reasoning'])) as reasoning_tokens,
			coalesce(nullIf(maxIf(SpanAttributes['trace.metadata'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['trace.metadata'], SpanAttributes['trace.metadata'] != ''), ''), '') as metadata,
			arrayFilter(x -> x != '', [
				if(max(SpanName LIKE 'workflow.%%'), 'workflow', ''),
				if(max(SpanName LIKE 'agent.%%'), 'agent', ''),
				if(max(SpanName LIKE 'sandbox.%%'), 'sandbox', ''),
				if(max(SpanName LIKE 'browser.%%'), 'browser', ''),
				if(max(SpanName LIKE 'memory.%%') OR max(SpanName LIKE 'vector.%%'), 'memory', '')%s
			]) as trace_kinds,
			any(ServiceName) as service_name,
			any(ScopeName) as scope_name,
			maxIf(SpanAttributes['trace.name'], ParentSpanId = '') as trace_name_attr,
			anyIf(SpanName, ParentSpanId = '') as root_span_name%s
		FROM otel_traces
		WHERE %s
		GROUP BY TraceId
		%s
		ORDER BY start_time DESC
		LIMIT ?
		OFFSET ?`,
		latestSuccessfulGenExpr(modelFrag, providerFrag, modelFrag),
		latestSuccessfulGenExpr(providerFrag, providerFrag, modelFrag),
		inputFrag, inputFrag,
		outputFrag, outputFrag,
		rootPreferred(userFrag),
		rootPreferred(sessionFrag),
		costFrag,
		inputTokensFrag, outputTokensFrag, totalTokensFrag,
		classificationElements,
		customAttrProjection,
		joinConditions(whereConditions), havingClause)

	// SELECT-clause params precede the WHERE params in the SQL, so they go to the
	// front of the arg list, in SELECT order: classification patterns (in the
	// trace_kinds array) then custom-attribute refs (the trailing map column).
	if selectArgs := append(append([]interface{}{}, classificationSelectArgs...), customAttrSelectArgs...); len(selectArgs) > 0 {
		args = append(selectArgs, args...)
	}

	// Append HAVING args before LIMIT/OFFSET
	args = append(args, havingArgs...)

	limit := listQuery.Limit
	if limit == 0 {
		limit = 100
	}
	args = append(args, limit, listQuery.Offset)

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		// Context canceled is expected when client disconnects (e.g., historical mode or mode switch)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logger.WithFields("query_type", listQuery.QueryType()).Debug("query canceled by client (expected)")
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		logger.WithFields("error", err.Error()).Error("failed to list traces")
		return nil, fmt.Errorf("failed to list traces: %w", err)
	}
	defer rows.Close()

	var traces []query.TraceReadModel
	for rows.Next() {
		// Check for context cancellation during iteration
		if ctx.Err() != nil {
			logger.WithFields("query_type", listQuery.QueryType()).Debug("query canceled by client during iteration (expected)")
			return nil, ctx.Err()
		}

		var trace query.TraceReadModel
		scanTargets := []interface{}{
			&trace.TraceID,
			&trace.StartTime,
			&trace.EndTime,
			&trace.TotalDuration,
			&trace.ErrorCount,
			&trace.RootStatus,
			&trace.RequestedModel,
			&trace.ServedModel,
			&trace.LLMModel,
			&trace.Provider,
			&trace.TenantID,
			&trace.SpanCount,
			&trace.TraceInput,
			&trace.TraceOutput,
			&trace.UserID,
			&trace.SessionID,
			&trace.ThreadID,
			&trace.TotalCost,
			&trace.TotalSavings,
			&trace.TotalCarbonSaved,
			&trace.ModelParameters,
			&trace.InputTokens,
			&trace.OutputTokens,
			&trace.TotalTokens,
			&trace.CachedTokens,
			&trace.ReasoningTokens,
			&trace.Metadata,
			&trace.TraceKinds,
			&trace.ServiceName,
			&trace.ScopeName,
			&trace.TraceNameAttr,
			&trace.RootSpanName,
		}
		// The custom_attr_columns map column is present only when the query
		// projected it; scan it into the read model when so.
		if len(listQuery.CustomAttrColumns) > 0 {
			trace.CustomAttrValues = map[string]string{}
			scanTargets = append(scanTargets, &trace.CustomAttrValues)
		}
		if err := rows.Scan(scanTargets...); err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logger.WithFields("query_type", listQuery.QueryType()).Debug("query canceled by client during scan (expected)")
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, err
			}
			logger.WithFields("error", err.Error()).Error("failed to scan trace row")
			return nil, fmt.Errorf("failed to scan trace: %w", err)
		}
		// Fill in cost for token-only agent traces (Gemini CLI, Codex, GLM,
		// Kimi) that emit no cost attribute — priced from the model catalog.
		recomputeTraceCostIfMissing(&trace)
		traces = append(traces, trace)
	}

	if err := rows.Err(); err != nil {
		// Context canceled is expected when client disconnects
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logger.WithFields("query_type", listQuery.QueryType()).Debug("query canceled by client (expected during iteration)")
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		return nil, fmt.Errorf("error iterating traces: %w", err)
	}

	// Ensure we return a non-nil slice even if empty
	if traces == nil {
		traces = []query.TraceReadModel{}
	}

	return traces, nil
}

// GetTraceHandler retrieves a single aggregated Trace by ID
type GetTraceHandler struct {
	conn clickhouse.Conn
}

func NewGetTraceHandler(conn clickhouse.Conn) *GetTraceHandler {
	return &GetTraceHandler{
		conn: conn,
	}
}

func (h *GetTraceHandler) QueryType() string {
	return "GetTrace"
}

func (h *GetTraceHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	getTraceQuery, ok := q.(*GetTraceQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for GetTraceHandler")
	}

	// Tenant scope is mandatory. Without it, a single-trace lookup
	// could match a foreign tenant's row by trace_id.
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return nil, sql.ErrNoRows
	}

	// Use the same aggregation logic as ListTracesHandler but for a single trace
	// PREWHERE is used for better index utilization with bloom filter on TraceId
	// Note: toInt64() cast on Duration ensures compatibility with both Int64 and UInt64 schemas
	getTraceTenantClause, getTraceTenantArgs := tenantBridgeFilter(tenantID)
	sqlQuery := fmt.Sprintf(`
		SELECT
			TraceId,
			min(Timestamp) as start_time,
			max(Timestamp) as end_time,
			-- Root span's Duration is the canonical trace duration, but an
			-- in-flight trace has no root yet (the root is emitted when the
			-- work finishes), and maxIf over an empty set is 0. Falling back to
			-- observed wall-clock keeps a live trace from rendering as 0s in
			-- the list while its detail view shows the real elapsed time.
			toInt64(if(
				countIf(ParentSpanId = '') > 0,
				maxIf(Duration, ParentSpanId = ''),
				max(toUnixTimestamp64Nano(Timestamp) + Duration) - min(toUnixTimestamp64Nano(Timestamp))
			)) as total_duration,
			countIf(`+otelstatus.IsError(otelstatus.Column)+`) as error_count,
			maxIf(StatusCode, ParentSpanId = '') as root_status,
			anyIf(SpanAttributes['model.requested'], SpanAttributes['model.requested'] != '') as requested_model,
			argMaxIf(coalesce(nullIf(SpanAttributes['model.served'], ''), nullIf(SpanAttributes['model.resolved'], ''), ''), Timestamp, SpanAttributes['model.served'] != '' OR SpanAttributes['model.resolved'] != '') as served_model,
			%s as llm_model,
			%s as provider,
			anyIf(SpanAttributes['tenant.id'], SpanAttributes['tenant.id'] != '') as tenant_id,
			count(*) as span_count,
			argMinIf(%s, Timestamp, %s != '') as trace_input,
			argMaxIf(%s, Timestamp, %s != '') as trace_output,
			%s as user_id,
			%s as session_id,
			coalesce(nullIf(maxIf(SpanAttributes['trace.thread_id'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['trace.thread_id'], SpanAttributes['trace.thread_id'] != ''), ''), '') as thread_id,
			sum(%s) as total_cost,
			sum(toFloat64OrZero(SpanAttributes['cost.savings_usd'])) as total_savings,
			sum(toFloat64OrZero(SpanAttributes['carbon.saved_grams'])) as total_carbon_saved,
			coalesce(nullIf(maxIf(SpanAttributes['llm.request.model_parameters'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['llm.request.model_parameters'], SpanAttributes['llm.request.model_parameters'] != ''), ''), '') as model_parameters,
			sum(%s) as input_tokens,
			sum(%s) as output_tokens,
			sum(%s) as total_tokens,
			sum(coalesce(nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cache_read']), 0), nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cached']), 0), nullIf(toInt64OrZero(SpanAttributes['cache_read_tokens']), 0), 0)) as cached_tokens,
			sum(toInt64OrZero(SpanAttributes['llm.tokens.reasoning'])) as reasoning_tokens,
			coalesce(nullIf(maxIf(SpanAttributes['trace.metadata'], ParentSpanId = ''), ''), nullIf(maxIf(SpanAttributes['trace.metadata'], SpanAttributes['trace.metadata'] != ''), ''), '') as metadata,
			arrayFilter(x -> x != '', [
				if(max(SpanName LIKE 'workflow.%%'), 'workflow', ''),
				if(max(SpanName LIKE 'agent.%%'), 'agent', ''),
				if(max(SpanName LIKE 'sandbox.%%'), 'sandbox', ''),
				if(max(SpanName LIKE 'browser.%%'), 'browser', ''),
				if(max(SpanName LIKE 'memory.%%') OR max(SpanName LIKE 'vector.%%'), 'memory', '')
			]) as trace_kinds
		FROM otel_traces
		WHERE TraceId = ? AND %s
		GROUP BY TraceId
	`,
		latestSuccessfulGenExpr(modelSQL(), providerSQL(), modelSQL()),
		latestSuccessfulGenExpr(providerSQL(), providerSQL(), modelSQL()),
		traceInputSQL(), traceInputSQL(),
		traceOutputSQL(), traceOutputSQL(),
		rootPreferred(userSQL()),
		rootPreferred(sessionSQL()),
		costSQL(),
		inputTokensSQL(), outputTokensSQL(), totalTokensSQL(),
		getTraceTenantClause)

	rows, err := h.conn.Query(ctx, sqlQuery, append([]interface{}{getTraceQuery.TraceID}, getTraceTenantArgs...)...)
	if err != nil {
		logger.WithFields(
			"trace_id", getTraceQuery.TraceID,
			"error", err.Error(),
		).Error("failed to get trace")
		return nil, fmt.Errorf("failed to get trace: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	var trace query.TraceReadModel
	if err := rows.Scan(
		&trace.TraceID,
		&trace.StartTime,
		&trace.EndTime,
		&trace.TotalDuration,
		&trace.ErrorCount,
		&trace.RootStatus,
		&trace.RequestedModel,
		&trace.ServedModel,
		&trace.LLMModel,
		&trace.Provider,
		&trace.TenantID,
		&trace.SpanCount,
		&trace.TraceInput,
		&trace.TraceOutput,
		&trace.UserID,
		&trace.SessionID,
		&trace.ThreadID,
		&trace.TotalCost,
		&trace.TotalSavings,
		&trace.TotalCarbonSaved,
		&trace.ModelParameters,
		&trace.InputTokens,
		&trace.OutputTokens,
		&trace.TotalTokens,
		&trace.CachedTokens,
		&trace.ReasoningTokens,
		&trace.Metadata,
		&trace.TraceKinds,
	); err != nil {
		logger.WithFields("error", err.Error()).Error("failed to scan trace row")
		return nil, fmt.Errorf("failed to scan trace: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating trace: %w", err)
	}

	// Fill in cost for token-only agent traces priced from the model catalog.
	recomputeTraceCostIfMissing(&trace)
	return trace, nil
}

// TraceTreeHandler builds hierarchical tree structure for a trace
type TraceTreeHandler struct {
	conn clickhouse.Conn
}

func NewTraceTreeHandler(conn clickhouse.Conn) *TraceTreeHandler {
	return &TraceTreeHandler{
		conn: conn,
	}
}

func (h *TraceTreeHandler) QueryType() string {
	return "GetTraceTree"
}

func (h *TraceTreeHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	treeQuery, ok := q.(*GetTraceTreeQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for TraceTreeHandler")
	}

	// First, get all spans for the trace from otel_traces (includes events).
	// Tenant scope is mandatory — see GetTraceHandler for rationale.
	treeTenantID := database.TenantSchemaFromContext(ctx)
	if treeTenantID == "" {
		return nil, sql.ErrNoRows
	}
	treeTenantClause, treeTenantArgs := tenantBridgeFilter(treeTenantID)
	treeTenantFilter := "AND " + treeTenantClause
	treeArgs := append([]interface{}{treeQuery.TraceID}, treeTenantArgs...)

	sqlQuery := fmt.Sprintf(`
		SELECT
			TraceId,
			SpanId,
			ParentSpanId,
			SpanName,
			SpanKind,
			Timestamp,
			toInt64(Duration) as Duration,
			StatusCode,
			StatusMessage,
			SpanAttributes,
			ResourceAttributes,
			Events.Timestamp as EventTimestamps,
			Events.Name as EventNames,
			Events.Attributes as EventAttributes
		FROM otel_traces
		WHERE TraceId = ? %s
		ORDER BY Timestamp ASC
	`, treeTenantFilter)

	rows, err := h.conn.Query(ctx, sqlQuery, treeArgs...)
	if err != nil {
		logger.WithFields(
			"trace_id", treeQuery.TraceID,
			"error", err.Error(),
		).Error("failed to query trace tree")
		return nil, fmt.Errorf("failed to query trace tree: %w", err)
	}
	defer rows.Close()

	var spans []query.SpanReadModel
	spanMap := make(map[string]*query.SpanTreeNode)

	for rows.Next() {
		var span query.SpanReadModel
		var eventTimestamps []time.Time
		var eventNames []string
		var eventAttributes []map[string]string

		if err := rows.Scan(
			&span.TraceID,
			&span.SpanID,
			&span.ParentSpanID,
			&span.SpanName,
			&span.SpanKind,
			&span.Timestamp,
			&span.Duration,
			&span.StatusCode,
			&span.StatusMessage,
			&span.SpanAttributes,
			&span.ResourceAttributes,
			&eventTimestamps,
			&eventNames,
			&eventAttributes,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan span row")
			return nil, fmt.Errorf("failed to scan span: %w", err)
		}

		// Parse events
		for i := range eventNames {
			event := query.SpanEvent{
				Name: eventNames[i],
			}
			if i < len(eventTimestamps) {
				event.Timestamp = eventTimestamps[i]
			}
			if i < len(eventAttributes) {
				event.Attributes = eventAttributes[i]
			}
			span.Events = append(span.Events, event)
		}

		spans = append(spans, span)

		// Create tree node
		node := &query.SpanTreeNode{
			Span:     span,
			Children: []*query.SpanTreeNode{},
		}
		spanMap[span.SpanID] = node
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating spans: %w", err)
	}
	if len(spans) == 0 {
		return nil, sql.ErrNoRows
	}

	// Build tree structure
	var rootNodes []*query.SpanTreeNode
	parentBySpanID := make(map[string]string, len(spans))
	for _, span := range spans {
		parentBySpanID[span.SpanID] = span.ParentSpanID
	}
	for _, span := range spans {
		node := spanMap[span.SpanID]
		if span.ParentSpanID == "" {
			// Root span
			rootNodes = append(rootNodes, node)
		} else {
			// Child span - attach to parent
			if parent, exists := spanMap[span.ParentSpanID]; exists {
				if span.ParentSpanID == span.SpanID || traceTreeWouldCycle(span.SpanID, span.ParentSpanID, parentBySpanID) {
					logger.WithFields(
						"span_id", span.SpanID,
						"parent_span_id", span.ParentSpanID,
					).Warn("cyclic parent span relationship detected, treating as root")
					rootNodes = append(rootNodes, node)
					continue
				}
				parent.Children = append(parent.Children, node)
			} else {
				// Parent not found - treat as root
				logger.WithFields(
					"span_id", span.SpanID,
					"parent_span_id", span.ParentSpanID,
				).Warn("parent span not found, treating as root")
				rootNodes = append(rootNodes, node)
			}
		}
	}

	if len(rootNodes) == 0 {
		first := spans[0]
		logger.WithFields(
			"trace_id", treeQuery.TraceID,
			"span_id", first.SpanID,
		).Warn("trace tree has no explicit root, using earliest span as root")
		rootNodes = append(rootNodes, spanMap[first.SpanID])
	}

	// Return the first root (should only be one in a well-formed trace)
	return rootNodes[0], nil
}

func traceTreeWouldCycle(childID, parentID string, parentBySpanID map[string]string) bool {
	seen := map[string]struct{}{}
	for current := parentID; current != ""; current = parentBySpanID[current] {
		if current == childID {
			return true
		}
		if _, ok := seen[current]; ok {
			return true
		}
		seen[current] = struct{}{}
	}
	return false
}

// TraceStatsHandler retrieves aggregate trace statistics
type TraceStatsHandler struct {
	conn clickhouse.Conn
}

func NewTraceStatsHandler(conn clickhouse.Conn) *TraceStatsHandler {
	return &TraceStatsHandler{
		conn: conn,
	}
}

func (h *TraceStatsHandler) QueryType() string {
	return "GetTraceStats"
}

func (h *TraceStatsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	statsQuery, ok := q.(*GetTraceStatsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for TraceStatsHandler")
	}

	// Determine time grouping
	var timeGroup string
	switch statsQuery.GroupBy {
	case "hour":
		timeGroup = "toStartOfHour(StartTime)"
	case "day":
		timeGroup = "toStartOfDay(StartTime)"
	default:
		timeGroup = "toStartOfDay(StartTime)" // Default to day
	}

	// Use tenant from context if not explicitly set in the query.
	// Fail closed — stats query without tenant scoping aggregates
	// every tenant's traces.
	statsTenantID := statsQuery.TenantID
	if statsTenantID == "" {
		statsTenantID = database.TenantSchemaFromContext(ctx)
	}
	if statsTenantID == "" {
		return []query.TraceStatsReadModel{}, nil
	}

	// Build WHERE clause — tenant predicate is mandatory.
	whereConditions := []string{"TenantID = ?"}
	args := []interface{}{statsTenantID}

	if !statsQuery.StartTime.IsZero() {
		whereConditions = append(whereConditions, "StartTime >= ?")
		args = append(args, statsQuery.StartTime)
	}

	if !statsQuery.EndTime.IsZero() {
		whereConditions = append(whereConditions, "EndTime <= ?")
		args = append(args, statsQuery.EndTime)
	}

	sqlQuery := fmt.Sprintf(`
		SELECT 
			%s as period,
			TenantID,
			count(DISTINCT TraceId) as trace_count,
			avg(TotalDuration) as avg_duration,
			sum(ErrorCount) as error_count,
			sum(ErrorCount) / count(DISTINCT TraceId) as error_rate,
			sum(SpanCount) as total_spans,
			sum(SpanCount) / count(DISTINCT TraceId) as avg_spans_per_trace
		FROM trace_summary
		WHERE %s
		GROUP BY period, TenantID
		ORDER BY period DESC
		LIMIT ?
	`, timeGroup, joinConditions(whereConditions))

	limit := statsQuery.Limit
	if limit == 0 {
		limit = 100
	}
	args = append(args, limit)

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to query trace stats")
		return nil, fmt.Errorf("failed to query trace stats: %w", err)
	}
	defer rows.Close()

	var stats []query.TraceStatsReadModel
	for rows.Next() {
		var stat query.TraceStatsReadModel
		if err := rows.Scan(
			&stat.Period,
			&stat.TenantID,
			&stat.TraceCount,
			&stat.AvgDuration,
			&stat.ErrorCount,
			&stat.ErrorRate,
			&stat.TotalSpans,
			&stat.AvgSpansPerTrace,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan stats row")
			return nil, fmt.Errorf("failed to scan stats: %w", err)
		}
		stats = append(stats, stat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stats: %w", err)
	}

	return stats, nil
}

// Helper function to join WHERE conditions
func joinConditions(conditions []string) string {
	result := ""
	for i, cond := range conditions {
		if i > 0 {
			result += " AND "
		}
		result += cond
	}
	return result
}
