ALTER TABLE events
    DROP COLUMN IF EXISTS blob_id,
    DROP COLUMN IF EXISTS payload_hash,
    DROP COLUMN IF EXISTS payload_size_bytes;

DROP TABLE IF EXISTS event_blobs;