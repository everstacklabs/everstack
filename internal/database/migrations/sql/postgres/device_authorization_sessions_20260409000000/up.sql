-- Device authorization sessions for CLI/desktop OAuth device flow (RFC 8628)
CREATE TABLE IF NOT EXISTS device_authorization_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code     TEXT NOT NULL UNIQUE,
    user_code       TEXT NOT NULL UNIQUE,
    client_id       TEXT NOT NULL,
    scope           TEXT NOT NULL DEFAULT 'cli:full',
    status          TEXT NOT NULL DEFAULT 'pending',
    user_id         UUID REFERENCES users(id),
    org_id          UUID REFERENCES organizations(id),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_device_auth_device_code ON device_authorization_sessions(device_code);
CREATE INDEX IF NOT EXISTS idx_device_auth_user_code ON device_authorization_sessions(user_code);
CREATE INDEX IF NOT EXISTS idx_device_auth_status ON device_authorization_sessions(status) WHERE status = 'pending';
