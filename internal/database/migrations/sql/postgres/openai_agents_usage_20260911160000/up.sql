CREATE TABLE IF NOT EXISTS openai_agent_turn_usage (
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    session_id text NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    upstream_session_id text NOT NULL,
    upstream_turn_id text NOT NULL,
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    total_tokens bigint CHECK (total_tokens >= 0),
    event_cursor bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, upstream_turn_id)
);
CREATE INDEX IF NOT EXISTS openai_agent_turn_usage_owner_idx
    ON openai_agent_turn_usage (organization_id, instance_id, session_id);

CREATE TABLE IF NOT EXISTS openai_agent_usage_events (
    organization_id text NOT NULL,
    instance_id text NOT NULL,
    session_id text NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    event_id text NOT NULL,
    upstream_turn_id text NOT NULL,
    event_cursor bigint NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (session_id, event_id)
);
CREATE INDEX IF NOT EXISTS openai_agent_usage_events_owner_idx
    ON openai_agent_usage_events (organization_id, instance_id, session_id, event_cursor);
