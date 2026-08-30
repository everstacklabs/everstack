-- Trace summary view for quick trace lookups
-- Aggregates span data to provide trace-level overview
CREATE VIEW IF NOT EXISTS trace_summary AS
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

-- Span hierarchy view (parent-child relationships)
-- Provides ordered view of spans for timeline and tree visualization
CREATE VIEW IF NOT EXISTS span_hierarchy AS
SELECT 
    TraceId,
    SpanId,
    ParentSpanId,
    SpanName,
    SpanKind,
    Timestamp,
    Duration,
    StatusCode,
    StatusMessage,
    SpanAttributes,
    ResourceAttributes
FROM otel_traces
ORDER BY TraceId, Timestamp;

-- Trace errors view for quick error lookup
CREATE VIEW IF NOT EXISTS trace_errors AS
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

-- LLM operation metrics view
-- Aggregates token usage and cost across traces
CREATE VIEW IF NOT EXISTS llm_operation_metrics AS
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



