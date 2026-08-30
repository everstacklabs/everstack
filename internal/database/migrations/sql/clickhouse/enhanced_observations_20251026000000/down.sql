-- Rollback Enhanced Observability System

-- Drop views first (they depend on tables)
DROP VIEW IF EXISTS performance_breakdown_view;
DROP VIEW IF EXISTS enhanced_observations_view;

-- Drop materialized views
DROP VIEW IF EXISTS step_execution_mv;
DROP VIEW IF EXISTS workflow_performance_mv;
DROP VIEW IF EXISTS trace_analytics_mv;

-- Drop new tables
DROP TABLE IF EXISTS otel_workflow_metadata;
DROP TABLE IF EXISTS otel_resource_metrics;
DROP TABLE IF EXISTS otel_performance_metrics;
DROP TABLE IF EXISTS otel_observation_io;

-- Remove indexes from otel_traces (ClickHouse doesn't support DROP INDEX directly)
-- Note: Indexes will be removed when columns are dropped

-- Remove enhanced columns from otel_traces
ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS ObservationType;

ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS NodeName;

ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS StepNumber;

