CREATE TABLE IF NOT EXISTS openai_agent_session_leases (
    session_id text PRIMARY KEY REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    holder_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS openai_agent_session_leases_expiry_idx
    ON openai_agent_session_leases (expires_at);
