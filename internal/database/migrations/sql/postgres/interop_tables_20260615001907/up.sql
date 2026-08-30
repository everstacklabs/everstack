-- Interop (MCP server / A2A) admin control tables.
-- tenant_id is TEXT to match the agents domain (cloud tenant_id == instance_id,
-- which is not always a clean UUID).

-- Per-tenant overrides for which MCP-server tools are exposed. Absence of a row
-- means the tool uses its default (enabled). We only persist explicit changes.
CREATE TABLE IF NOT EXISTS mcp_tool_settings (
    tenant_id  TEXT NOT NULL,
    tool_name  TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, tool_name)
);

-- Which agents are published over A2A. Opt-in: absence of a row (or enabled=false)
-- means the agent is NOT discoverable/callable via A2A.
CREATE TABLE IF NOT EXISTS a2a_published_agents (
    tenant_id  TEXT NOT NULL,
    agent_id   TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, agent_id)
);

-- Saved external A2A agents that this tenant's agents can call by name via the
-- call_external_agent tool.
CREATE TABLE IF NOT EXISTS a2a_remote_agents (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    endpoint   TEXT NOT NULL,
    auth_token TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_a2a_remote_agents_tenant ON a2a_remote_agents(tenant_id);

-- Generic per-tenant interop capability flags (key -> enabled), e.g.
-- 'adk_runtime'. Opt-in: a missing row means disabled. For cloud these are
-- driven by the control plane / plan; for self-hosted by the admin UI.
CREATE TABLE IF NOT EXISTS interop_settings (
    tenant_id  TEXT NOT NULL,
    key        TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, key)
);
