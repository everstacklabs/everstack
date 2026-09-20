CREATE TABLE IF NOT EXISTS openai_agent_connections (
    id text PRIMARY KEY,
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    name text NOT NULL,
    provider text NOT NULL CHECK (provider = 'openai_agents'),
    openai_project_id text NOT NULL DEFAULT '',
    openai_organization_id text NOT NULL DEFAULT '',
    openai_principal_id text NOT NULL DEFAULT '',
    allowed_models jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'pending',
    last_error text,
    credential_ref text NOT NULL,
    executor_credential_ref text,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS openai_agent_connections_owner_idx
    ON openai_agent_connections (organization_id, instance_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS openai_agent_sessions (
    id text PRIMARY KEY,
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    provider text NOT NULL CHECK (provider = 'openai_agents'),
    connection_id text NOT NULL REFERENCES openai_agent_connections(id),
    conversation_id text,
    upstream_session_id text,
    upstream_environment_id text,
    sandbox_id text,
    model text NOT NULL,
    environment_type text NOT NULL CHECK (environment_type IN ('none','self_hosted')),
    workspace_directory text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'creating',
    readiness text NOT NULL DEFAULT 'pending',
    cleanup_status text NOT NULL DEFAULT 'active',
    last_error text,
    idempotency_key text NOT NULL,
	request_fingerprint text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, instance_id, idempotency_key),
    UNIQUE (organization_id, instance_id, upstream_session_id)
);
CREATE INDEX IF NOT EXISTS openai_agent_sessions_owner_idx
    ON openai_agent_sessions (organization_id, instance_id, created_at DESC);

CREATE TABLE IF NOT EXISTS openai_agent_events (
    cursor bigserial PRIMARY KEY,
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    session_id text NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    event_id text NOT NULL,
    event_type text NOT NULL,
    event_version text NOT NULL DEFAULT '',
    upstream_item_id text,
    payload jsonb NOT NULL,
    tool_failed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, event_id)
);
CREATE INDEX IF NOT EXISTS openai_agent_events_recovery_idx
    ON openai_agent_events (organization_id, instance_id, session_id, cursor);

CREATE TABLE IF NOT EXISTS openai_agent_input_intents (
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    session_id text NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
	request_fingerprint text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, instance_id, session_id, idempotency_key)
);
