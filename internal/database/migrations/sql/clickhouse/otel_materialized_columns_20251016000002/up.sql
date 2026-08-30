-- Add materialized columns for fast querying of event and payload
-- These columns are auto-populated from LogAttributes map with zero overhead
-- They provide fast indexed access without duplicating storage (MATERIALIZED = computed on read)

ALTER TABLE otel_logs
    ADD COLUMN IF NOT EXISTS event String MATERIALIZED LogAttributes['event'] CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS payload_json String MATERIALIZED LogAttributes['payload'] CODEC(ZSTD(1));

-- Add bloom filter index on event column for fast filtering
-- This enables queries like: WHERE event = 'provider.request.issued'
ALTER TABLE otel_logs
    ADD INDEX IF NOT EXISTS idx_event event TYPE bloom_filter(0.01) GRANULARITY 1;

