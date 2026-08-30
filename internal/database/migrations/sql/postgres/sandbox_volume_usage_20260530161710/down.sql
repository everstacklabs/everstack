ALTER TABLE sandbox_volumes
    DROP COLUMN IF EXISTS used_bytes,
    DROP COLUMN IF EXISTS usage_measured_at;
