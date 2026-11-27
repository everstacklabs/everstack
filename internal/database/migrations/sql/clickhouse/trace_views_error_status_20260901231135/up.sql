-- Count trace errors under the stored OTel status spelling.
--
-- The three read-time views defined by trace_views_20251018000000 select
-- errors with
--
--   StatusCode = 'ERROR'
--
-- but otel_traces stores the OpenTelemetry status under the SHORT spelling the
-- collector exporter writes -- 'Ok' / 'Error' / 'Unset' -- or under the
-- protobuf enum name 'STATUS_CODE_ERROR' when the Everstack OTLP HTTP handler
-- (internal/api/http/otlp/handler.go) writes it. ClickHouse '=' is
-- case-sensitive, so the uppercase short form matches neither. PR #608 swept
-- for the enum name and therefore walked straight past these three sites.
--
-- trace_summary.ErrorCount is the one that reaches a user: TraceStatsHandler
-- (internal/query/handlers/traces/trace_query_handler.go) reads it as both
-- error_count and error_rate, so both have been exactly 0 since the view was
-- created. Verified read-only against production ClickHouse:
--
--   SELECT StatusCode, count() FROM everstack.otel_traces GROUP BY StatusCode
--     -> Error 32 / Ok 660 / Unset 8647
--
--   SELECT count(), sum(ErrorCount) FROM everstack.trace_summary
--     -> 9192 rows, 0 errors
--
--   SELECT sum(old_err), sum(new_err) FROM (
--     SELECT countIf(StatusCode = 'ERROR') AS old_err,
--            countIf(lower(StatusCode) IN ('error','status_code_error')) AS new_err
--       FROM everstack.otel_traces GROUP BY TraceId)
--     -> old 0, new 32
--
-- trace_errors and llm_operation_metrics are not read by any Go or TypeScript
-- code today. They are repaired in the same pass because they are plain views:
-- the predicate is evaluated at read time, so there is nothing to backfill and
-- nothing to get wrong later by wiring up a view that silently reports zero.
--
-- The predicate matches internal/telemetry/otelstatus.IsError, which is the
-- single place every Go reader of otel_traces now builds this comparison.
--
-- NOT touched here: the materialized views in
-- enhanced_observations_20251026000000 (trace_analytics_mv,
-- workflow_performance_mv, step_execution_mv) carry the same broken literals,
-- but they aggregate at INSERT time, so correcting them needs a backfill
-- decision rather than a redefinition, and nothing reads them.
--
-- span_hierarchy only projects StatusCode and is left exactly as it was.

DROP VIEW IF EXISTS trace_summary;

CREATE VIEW trace_summary AS
SELECT
    TraceId,
    min(Timestamp) as StartTime,
    max(Timestamp) as EndTime,
    max(Duration) as TotalDuration,
    countIf(lower(StatusCode) IN ('error', 'status_code_error')) as ErrorCount,
    SpanAttributes['model.requested'] as RequestedModel,
    SpanAttributes['model.served'] as ServedModel,
    SpanAttributes['llm.request.model'] as LLMModel,
    ResourceAttributes['tenant.id'] as TenantID,
    count() as SpanCount
FROM otel_traces
GROUP BY TraceId, RequestedModel, ServedModel, LLMModel, TenantID;

DROP VIEW IF EXISTS trace_errors;

CREATE VIEW trace_errors AS
SELECT
    TraceId,
    SpanId,
    SpanName,
    Timestamp,
    StatusCode,
    StatusMessage,
    SpanAttributes['error'] as Error,
    SpanAttributes['llm.provider'] as Provider,
    SpanAttributes['llm.model'] as Model,
    ResourceAttributes['tenant.id'] as TenantID
FROM otel_traces
WHERE lower(StatusCode) IN ('error', 'status_code_error')
ORDER BY Timestamp DESC;

DROP VIEW IF EXISTS llm_operation_metrics;

CREATE VIEW llm_operation_metrics AS
SELECT
    toDate(Timestamp) as Date,
    SpanAttributes['llm.provider'] as Provider,
    SpanAttributes['llm.model'] as Model,
    SpanAttributes['llm.operation'] as Operation,
    count() as RequestCount,
    sum(toInt64OrZero(SpanAttributes['llm.tokens.input'])) as TotalInputTokens,
    sum(toInt64OrZero(SpanAttributes['llm.tokens.output'])) as TotalOutputTokens,
    sum(toFloat64OrZero(SpanAttributes['llm.cost'])) as TotalCost,
    avg(Duration) as AvgDuration,
    countIf(lower(StatusCode) IN ('error', 'status_code_error')) as ErrorCount
FROM otel_traces
WHERE SpanName LIKE 'provider.%' OR SpanName = 'gateway.embeddings'
GROUP BY Date, Provider, Model, Operation
ORDER BY Date DESC, RequestCount DESC;
