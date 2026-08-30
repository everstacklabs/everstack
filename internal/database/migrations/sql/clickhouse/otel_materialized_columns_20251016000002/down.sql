-- Rollback: Remove materialized columns and indexes

ALTER TABLE otel_logs
    DROP INDEX IF EXISTS idx_event;

ALTER TABLE otel_logs
    DROP COLUMN IF EXISTS event,
    DROP COLUMN IF EXISTS payload_json;

