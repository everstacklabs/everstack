-- Restore the retention declared in otel_telemetry_init_20251016000000.
--
-- Note this does NOT restore the 3-day TTL the otel-collector exporter applies,
-- because that value was never intended -- it was the side effect of the
-- exporter winning the CREATE TABLE IF NOT EXISTS race. Down migrations revert
-- to the previously declared state, not to the accident.
--
-- Rolling back narrows retention, and ClickHouse materializes the new TTL on
-- the next merge, so rows outside the narrower window are deleted and are not
-- recoverable. Only run this if you mean it.

ALTER TABLE otel_traces MODIFY TTL toDateTime(Timestamp) + INTERVAL 7 DAY;

ALTER TABLE otel_logs MODIFY TTL toDateTime(Timestamp) + INTERVAL 30 DAY;
