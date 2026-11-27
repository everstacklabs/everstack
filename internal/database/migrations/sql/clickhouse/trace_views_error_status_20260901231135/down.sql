-- Restore the trace views as trace_views_20251018000000 defined them, i.e.
-- with the StatusCode = 'ERROR' comparison that matches no stored span status.

DROP VIEW IF EXISTS trace_summary;

CREATE VIEW trace_summary AS
SELECT
    TraceId,
    min(Timestamp) as StartTime,
    max(Timestamp) as EndTime,
    max(Duration) as TotalDuration,
    countIf(StatusCode = 'ERROR') as ErrorCount,
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
WHERE StatusCode = 'ERROR'
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
    countIf(StatusCode = 'ERROR') as ErrorCount
FROM otel_traces
WHERE SpanName LIKE 'provider.%' OR SpanName = 'gateway.embeddings'
GROUP BY Date, Provider, Model, Operation
ORDER BY Date DESC, RequestCount DESC;
