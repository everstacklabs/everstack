ALTER TABLE device_authorization_sessions
    DROP COLUMN IF EXISTS poll_interval_seconds,
    DROP COLUMN IF EXISTS last_polled_at;
