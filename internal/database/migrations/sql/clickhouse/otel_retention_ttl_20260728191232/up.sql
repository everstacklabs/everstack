-- Authoritative retention for the OTLP tables.
--
-- Why this exists: otel_traces and otel_logs are created by whichever component
-- reaches ClickHouse first, and every creator uses CREATE TABLE IF NOT EXISTS.
-- In any deployment that runs an otel-collector, the collector's ClickHouse
-- exporter wins that race and stamps the table with the exporter's own `ttl:`
-- setting (72h in our collector config). From that moment the CREATE in
-- otel_telemetry_init_20251016000000 is a permanent no-op, so the retention
-- declared there (7d traces / 30d logs) has never taken effect on any
-- environment with a collector -- both tables silently ran at 3 days and
-- deleted everything older. The migration was recorded as applied while doing
-- nothing, which is why this went unnoticed.
--
-- Tell-tale that the exporter, not us, owns the schema: the live otel_logs TTL
-- keys off `TimestampTime`, an exporter-only column, and the exporter-only
-- helper table otel_traces_trace_id_ts exists alongside it.
--
-- ALTER ... MODIFY TTL is unconditional and idempotent, so it overrides
-- whichever creator won and stays correct on every re-run. Retention is stated
-- once, here, and this file is the single source of truth for it.
--
-- Keep `ttl:` in the otel-collector config >= these values. It does not change
-- an existing table, but it decides the starting retention of a brand-new
-- environment for the window before this migration runs.
--
-- toDateTime(Timestamp) is used for both tables rather than the exporter's
-- `TimestampTime`, because `Timestamp` is the one column present in every
-- version of both schemas (exporter-created and migration-created). The two
-- expressions evaluate identically -- TimestampTime is DEFAULT
-- toDateTime(Timestamp).

ALTER TABLE otel_traces MODIFY TTL toDateTime(Timestamp) + INTERVAL 30 DAY;

ALTER TABLE otel_logs MODIFY TTL toDateTime(Timestamp) + INTERVAL 30 DAY;
