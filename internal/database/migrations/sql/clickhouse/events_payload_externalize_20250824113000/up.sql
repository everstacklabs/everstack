-- Create blob table for large event payloads
CREATE TABLE IF NOT EXISTS event_blobs
(
    blob_id String,
    created_at DateTime DEFAULT now(),
    size_bytes UInt64,
    compression LowCardinality(String) DEFAULT 'zstd',
    content String CODEC(ZSTD(6))
)
ENGINE = MergeTree
ORDER BY (blob_id);

-- Add externalization metadata to events
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS payload_size_bytes UInt64 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payload_hash String DEFAULT '',
    ADD COLUMN IF NOT EXISTS blob_id Nullable(String) AFTER payload_hash
;

-- Apply compression on payload column to reduce storage footprint
ALTER TABLE events
    MODIFY COLUMN payload String CODEC(ZSTD(6));

-- Consider converting type/model to LowCardinality if present later

