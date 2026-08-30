-- Fix Enhanced Observability Columns to Extract from SpanAttributes
-- The OTEL collector stores our custom attributes in the SpanAttributes map
-- We need to extract them using DEFAULT expressions

-- First, drop indexes if they exist (must be done before modifying columns)
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_step_number;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_node_name;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_observation_type;

-- Modify existing columns to add DEFAULT expressions
-- If columns don't exist, this will fail silently and we'll add them below

-- For StepNumber: Try to modify if exists, otherwise add
ALTER TABLE otel_traces 
  MODIFY COLUMN IF EXISTS StepNumber Nullable(UInt32) 
    DEFAULT if(
      mapContains(SpanAttributes, 'span.step'),
      CAST(SpanAttributes['span.step'] AS Nullable(UInt32)),
      NULL
    ) CODEC(ZSTD(1));

-- Add if it doesn't exist (will be skipped if MODIFY succeeded)
ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS StepNumber Nullable(UInt32) 
    DEFAULT if(
      mapContains(SpanAttributes, 'span.step'),
      CAST(SpanAttributes['span.step'] AS Nullable(UInt32)),
      NULL
    ) CODEC(ZSTD(1));

-- For NodeName: Try to modify if exists, otherwise add
ALTER TABLE otel_traces 
  MODIFY COLUMN IF EXISTS NodeName Nullable(String) 
    DEFAULT if(
      mapContains(SpanAttributes, 'span.node'),
      SpanAttributes['span.node'],
      NULL
    ) CODEC(ZSTD(1));

ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS NodeName Nullable(String) 
    DEFAULT if(
      mapContains(SpanAttributes, 'span.node'),
      SpanAttributes['span.node'],
      NULL
    ) CODEC(ZSTD(1));

-- For ObservationType: Try to modify if exists, otherwise add
ALTER TABLE otel_traces 
  MODIFY COLUMN IF EXISTS ObservationType LowCardinality(Nullable(String)) 
    DEFAULT if(
      mapContains(SpanAttributes, 'observation.type'),
      SpanAttributes['observation.type'],
      NULL
    ) CODEC(ZSTD(1));

ALTER TABLE otel_traces 
  ADD COLUMN IF NOT EXISTS ObservationType LowCardinality(Nullable(String)) 
    DEFAULT if(
      mapContains(SpanAttributes, 'observation.type'),
      SpanAttributes['observation.type'],
      NULL
    ) CODEC(ZSTD(1));

-- Re-add indexes for enhanced columns
ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_step_number StepNumber TYPE minmax GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_node_name NodeName TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE otel_traces 
  ADD INDEX IF NOT EXISTS idx_observation_type ObservationType TYPE bloom_filter(0.01) GRANULARITY 1;

-- Note: For existing data, you may want to run:
-- ALTER TABLE otel_traces MATERIALIZE COLUMN StepNumber;
-- ALTER TABLE otel_traces MATERIALIZE COLUMN NodeName;
-- ALTER TABLE otel_traces MATERIALIZE COLUMN ObservationType;
-- But this can be expensive on large tables, so we'll let new data populate naturally

