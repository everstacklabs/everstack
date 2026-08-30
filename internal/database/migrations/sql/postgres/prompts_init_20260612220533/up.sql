-- Prompt library: named prompts with immutable versions and deployment labels.

CREATE TABLE IF NOT EXISTS prompts (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT uq_prompts_tenant_name UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_prompts_tenant_id ON prompts(tenant_id);

CREATE TABLE IF NOT EXISTS prompt_versions (
    id VARCHAR(255) PRIMARY KEY,
    prompt_id VARCHAR(255) NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    tenant_id VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]',
    config JSONB NOT NULL DEFAULT '{}',
    labels JSONB NOT NULL DEFAULT '[]',
    commit_message TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_prompt_versions_prompt_version UNIQUE (prompt_id, version)
);

CREATE INDEX IF NOT EXISTS idx_prompt_versions_tenant_id ON prompt_versions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_prompt_versions_prompt_id ON prompt_versions(prompt_id);
