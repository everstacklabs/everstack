-- Rollback Rich Tracing Extensions

-- Drop materialized view
DROP VIEW IF EXISTS trace_details_view;

-- Drop scores table
DROP TABLE IF EXISTS otel_trace_scores;

-- Remove added indexes from base traces table
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_user_id;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_session_id;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_model;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_observation_type;


