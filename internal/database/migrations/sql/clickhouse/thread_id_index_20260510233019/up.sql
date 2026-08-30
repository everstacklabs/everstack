-- Add bloom_filter skip index on SpanAttributes['trace.thread_id'].
-- A thread is the conversational continuation within or across sessions —
-- distinct from session_id (which is a product-level grouping). Filters by
-- thread_id are written as TraceId IN (SELECT … WHERE SpanAttributes['trace.thread_id'] = ?)
-- on the WHERE side, so a skip index on the attribute pays off the same way
-- idx_session_id does.
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_thread_id (SpanAttributes['trace.thread_id']) TYPE bloom_filter(0.01) GRANULARITY 1;
