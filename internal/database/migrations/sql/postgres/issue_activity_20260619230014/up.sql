CREATE TABLE IF NOT EXISTS issue_activity (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT        NOT NULL,
    fingerprint  TEXT        NOT NULL,
    actor        TEXT        NOT NULL DEFAULT 'system',
    action       TEXT        NOT NULL,
    from_status  TEXT        NOT NULL DEFAULT '',
    to_status    TEXT        NOT NULL DEFAULT '',
    note         TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_issue_activity_tenant_fp
    ON issue_activity (tenant_id, fingerprint, created_at DESC);
