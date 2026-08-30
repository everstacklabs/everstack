CREATE TABLE IF NOT EXISTS browser_usage_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    pod_name TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    last_heartbeat_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_seconds BIGINT,
    billable_seconds BIGINT,
    cost_micros BIGINT,
    end_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (duration_seconds IS NULL OR duration_seconds >= 0),
    CHECK (billable_seconds IS NULL OR billable_seconds >= duration_seconds),
    CHECK (cost_micros IS NULL OR cost_micros >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_browser_usage_windows_open_session
    ON browser_usage_windows (session_id)
    WHERE ended_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_browser_usage_windows_tenant_started
    ON browser_usage_windows (tenant_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_browser_usage_windows_open_heartbeat
    ON browser_usage_windows (last_heartbeat_at)
    WHERE ended_at IS NULL;

COMMENT ON TABLE browser_usage_windows IS
    'Immutable hosted-browser lease windows. Only bound sessions are billable; idle pool pods never create rows.';

COMMENT ON COLUMN browser_usage_windows.cost_micros IS
    'Finalized USD cost multiplied by 1,000,000. Computed from the canonical browser runtime contract.';
