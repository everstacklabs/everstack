-- ClickHouse projections on otel_traces.
--
-- The base table is ORDER BY (ServiceName, SpanName, toUnixTimestamp(Timestamp), TraceId)
-- which is great for "all traces of service X named Y" but bad for the read paths
-- we actually run hot:
--
--   - ListTraces / dashboards filter by tenant.id + time range and order by time DESC.
--   - Percentile latency query (metrics_dashboard) filters by tenant.id + time range.
--   - Session-scoped queries filter by session.id (via subquery on trace.session_id).
--
-- A projection re-stores rows in a different sort order so the right granules
-- can be skipped. Storage roughly doubles (TTL still bounds it; we drop with
-- the rest of the part).
--
-- Direct attack on Langfuse's scale antipattern — their v3
-- ReplacingMergeTree scans billions of rows for "latest version" and times
-- out on multi-day dashboards. Projections give us the sorted view we need
-- without changing the ingest schema.
--
-- Materialize=true populates the projection from existing data on the next
-- merge cycle so the rollout doesn't need a backfill job.

-- 1. Tenant + time projection. Powers tenant-scoped list and dashboard.
ALTER TABLE otel_traces
    ADD PROJECTION IF NOT EXISTS proj_tenant_time (
        SELECT *
        ORDER BY (
            ResourceAttributes['tenant.id'],
            toUnixTimestamp(Timestamp)
        )
    );

ALTER TABLE otel_traces MATERIALIZE PROJECTION proj_tenant_time;

-- 2. Trace lookup projection. Lookups by TraceId (GetTrace, GetTraceByID,
-- GetTraceTree) currently hit the idx_trace_id bloom; this projection
-- sorts by TraceId so we don't even need to consult the bloom for hot
-- traces and adjacent spans sit in the same granule.
ALTER TABLE otel_traces
    ADD PROJECTION IF NOT EXISTS proj_trace_id (
        SELECT *
        ORDER BY (TraceId, ParentSpanId, toUnixTimestamp(Timestamp))
    );

ALTER TABLE otel_traces MATERIALIZE PROJECTION proj_trace_id;
