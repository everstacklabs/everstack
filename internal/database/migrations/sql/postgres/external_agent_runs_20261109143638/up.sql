ALTER TABLE a2a_remote_agents
    ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT 'a2a-legacy',
    ADD COLUMN IF NOT EXISTS routing_tenant TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS auth_ciphertext BYTEA,
    ADD COLUMN IF NOT EXISTS auth_key_id TEXT;
ALTER TABLE a2a_remote_agents ADD CONSTRAINT a2a_remote_protocol CHECK (protocol IN ('a2a-legacy', 'a2a-1.0'));
ALTER TABLE a2a_remote_agents ADD CONSTRAINT a2a_remote_secret CHECK (
    (auth_ciphertext IS NULL AND auth_key_id IS NULL) OR
    (auth_ciphertext IS NOT NULL AND auth_key_id IS NOT NULL AND auth_token IS NULL));

-- Remote deletion revokes access but must not erase run history. Deliberately
-- no FK to a2a_remote_agents; every network operation re-resolves the remote.
CREATE TABLE a2a_external_runs (
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    remote_id UUID NOT NULL,
    remote_name TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    protocol TEXT NOT NULL,
    routing_tenant TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('dispatching','submitted','working','input-required','auth-required','completed','failed','canceled','rejected','unknown')),
    remote_task_id TEXT NOT NULL DEFAULT '',
    remote_context_id TEXT NOT NULL DEFAULT '',
    output TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    cancel_requested_at TIMESTAMPTZ,
    lease_token TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX a2a_external_runs_tenant_created ON a2a_external_runs (tenant_id, created_at DESC);
