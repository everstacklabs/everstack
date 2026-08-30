-- Per-tenant shadow session table for the cloud→tenant launch flow.
--
-- Cloud sessions remain the source of truth for authentication: the tenant
-- cookie still holds the cloud session token and tenant middleware validates
-- against everstack.sessions on every request. This table is a *shadow* row
-- written when a cloud session first lands on this tenant via the
-- /auth/cloud-callback handler, plus a throttled last_seen_at touch on
-- subsequent validated requests.
--
-- Purpose: per-tenant queryable session history without round-tripping to
-- the cloud DB. "Show me logins on this tenant in the last 30 days" is a
-- single-table query here.
--
-- id mirrors the cloud session id so a given cloud session has at most one
-- shadow row per tenant; the (id, cloud_session_id) split keeps the API
-- explicit even though they're equal in practice.
CREATE TABLE IF NOT EXISTS cloud_relay_sessions (
    id                UUID        PRIMARY KEY,
    cloud_session_id  UUID        NOT NULL,
    cloud_user_id     UUID        NOT NULL,
    organization_id   UUID        NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at          TIMESTAMPTZ,
    ip_address        TEXT,
    user_agent        TEXT,
    launch_code_id    BYTEA
);

CREATE INDEX IF NOT EXISTS idx_cloud_relay_sessions_user
    ON cloud_relay_sessions (cloud_user_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_cloud_relay_sessions_active
    ON cloud_relay_sessions (last_seen_at DESC)
    WHERE ended_at IS NULL;
