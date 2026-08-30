-- issue_states holds the mutable triage overlay for error-tracking Issues.
-- Issues themselves are derived at query time by aggregating error spans in the
-- trace store; this table only persists the human decisions (resolve / ignore /
-- assign / snooze) keyed by the issue fingerprint, scoped per tenant.
CREATE TABLE IF NOT EXISTS issue_states (
    tenant_id    TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'unresolved',
    assignee     TEXT,
    snooze_until TIMESTAMPTZ,
    resolved_at  TIMESTAMPTZ,
    signature    TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_issue_states_tenant_status ON issue_states(tenant_id, status);
