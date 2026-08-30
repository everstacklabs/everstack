-- Rollback: Remove materialized columns and restore nullable columns

-- Drop the materialized columns
ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS StepNumber;

ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS NodeName;

ALTER TABLE otel_traces 
  DROP COLUMN IF EXISTS ObservationType;

-- Restore as simple nullable columns (original state)
ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS StepNumber Nullable(UInt32) CODEC(ZSTD(1));

ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS NodeName Nullable(String) CODEC(ZSTD(1));

ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS ObservationType LowCardinality(Nullable(String)) CODEC(ZSTD(1));

-- Re-add indexes
ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_step_number StepNumber TYPE minmax GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_node_name NodeName TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_observation_type ObservationType TYPE bloom_filter(0.01) GRANULARITY 1;

