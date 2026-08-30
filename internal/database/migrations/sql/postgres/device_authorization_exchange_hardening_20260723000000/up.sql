ALTER TABLE device_authorization_sessions
    ADD COLUMN IF NOT EXISTS last_polled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS poll_interval_seconds INTEGER NOT NULL DEFAULT 5;
