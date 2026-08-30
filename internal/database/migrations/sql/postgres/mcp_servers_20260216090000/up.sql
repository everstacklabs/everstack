-- MCP Server Registry
CREATE TABLE IF NOT EXISTS mcp_servers (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       VARCHAR(255) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    url             TEXT         NOT NULL DEFAULT '',
    transport_type  VARCHAR(50)  NOT NULL DEFAULT 'sse',
    auth_type       VARCHAR(50)  NOT NULL DEFAULT 'none',
    auth_config     JSONB        NOT NULL DEFAULT '{}',
    tool_filter     JSONB        NOT NULL DEFAULT '{}',
    headers         JSONB        NOT NULL DEFAULT '{}',
    stdio_config    JSONB        NOT NULL DEFAULT '{}',
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    health_status   VARCHAR(50)  NOT NULL DEFAULT 'unknown',
    health_last_check TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    UNIQUE(tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_tenant ON mcp_servers (tenant_id);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_tenant_enabled ON mcp_servers (tenant_id, enabled) WHERE deleted_at IS NULL;

-- Discovered tools for each MCP server
CREATE TABLE IF NOT EXISTS mcp_server_tools (
    id              BIGSERIAL PRIMARY KEY,
    server_id       BIGINT       NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    description     TEXT         NOT NULL DEFAULT '',
    input_schema    JSONB        NOT NULL DEFAULT '{}',
    annotations     JSONB        NOT NULL DEFAULT '{}',
    discovered_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(server_id, name)
);
CREATE INDEX IF NOT EXISTS idx_mcp_server_tools_server ON mcp_server_tools (server_id);

-- Tool call audit log
CREATE TABLE IF NOT EXISTS mcp_tool_calls (
    id          BIGSERIAL PRIMARY KEY,
    server_id   BIGINT       NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name   VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    status      VARCHAR(50)  NOT NULL DEFAULT 'success',
    input_args  JSONB        NOT NULL DEFAULT '{}',
    output      TEXT         NOT NULL DEFAULT '',
    latency_ms  BIGINT       NOT NULL DEFAULT 0,
    error       TEXT         NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_calls_server ON mcp_tool_calls (server_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_calls_tenant ON mcp_tool_calls (tenant_id, created_at DESC);
