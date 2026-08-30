-- OAuth 2.1 state storage for MCP server authorization flows
CREATE TABLE IF NOT EXISTS mcp_oauth_states (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       VARCHAR(255) NOT NULL,
    server_id       BIGINT       NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    state           VARCHAR(255) NOT NULL UNIQUE,
    code_verifier   TEXT         NOT NULL,
    redirect_uri    TEXT         NOT NULL,
    token_endpoint  TEXT         NOT NULL,
    client_id       TEXT         NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ  NOT NULL DEFAULT (NOW() + INTERVAL '10 minutes')
);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_states_state ON mcp_oauth_states (state);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_states_expires ON mcp_oauth_states (expires_at);
