-- Immutable, revision-scoped source projects for Agent Runtime.
CREATE TABLE IF NOT EXISTS agent_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent_definitions(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    digest CHAR(64) NOT NULL,
    manifest JSONB NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_agent_revisions_number UNIQUE (agent_id, revision_number),
    CONSTRAINT uq_agent_revisions_digest UNIQUE (agent_id, digest),
    CONSTRAINT ck_agent_revisions_digest CHECK (digest ~ '^[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS idx_agent_revisions_tenant_agent
    ON agent_revisions(tenant_id, agent_id, revision_number DESC);

CREATE TABLE IF NOT EXISTS agent_revision_files (
    tenant_id UUID NOT NULL,
    revision_id UUID NOT NULL REFERENCES agent_revisions(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    mode INTEGER NOT NULL DEFAULT 420,
    size_bytes BIGINT NOT NULL,
    content BYTEA NOT NULL,
    PRIMARY KEY (revision_id, path),
    CONSTRAINT ck_agent_revision_files_path CHECK (
        path <> '' AND
        path !~ '(^|/)\.\.(/|$)' AND
        path !~ '^/'
    ),
    CONSTRAINT ck_agent_revision_files_sha256 CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_agent_revision_files_size CHECK (size_bytes >= 0)
);

CREATE INDEX IF NOT EXISTS idx_agent_revision_files_tenant_revision
    ON agent_revision_files(tenant_id, revision_id);

ALTER TABLE agent_definitions
    ADD COLUMN IF NOT EXISTS active_revision_id UUID;

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_agent_definitions_active_revision'
    ) THEN
        ALTER TABLE agent_definitions
            ADD CONSTRAINT fk_agent_definitions_active_revision
            FOREIGN KEY (active_revision_id) REFERENCES agent_revisions(id) ON DELETE SET NULL;
    END IF;
END
$do$;

ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS revision_id UUID;

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_agent_sessions_revision'
    ) THEN
        ALTER TABLE agent_sessions
            ADD CONSTRAINT fk_agent_sessions_revision
            FOREIGN KEY (revision_id) REFERENCES agent_revisions(id) ON DELETE SET NULL;
    END IF;
END
$do$;

CREATE INDEX IF NOT EXISTS idx_agent_sessions_revision_id
    ON agent_sessions(revision_id) WHERE revision_id IS NOT NULL;

-- Install dormant tenant policies when the shared helper is available. RLS is
-- intentionally not enabled here, matching the staged gateway rollout.
DO $do$
BEGIN
    IF to_regprocedure('everstack.tenant_matches(text)') IS NULL THEN
        RETURN;
    END IF;
    DROP POLICY IF EXISTS tenant_isolation ON agent_revisions;
    CREATE POLICY tenant_isolation ON agent_revisions FOR ALL
        USING (everstack.tenant_matches(tenant_id::text))
        WITH CHECK (everstack.tenant_matches(tenant_id::text));

    DROP POLICY IF EXISTS tenant_isolation ON agent_revision_files;
    CREATE POLICY tenant_isolation ON agent_revision_files FOR ALL
        USING (everstack.tenant_matches(tenant_id::text))
        WITH CHECK (everstack.tenant_matches(tenant_id::text));
END
$do$;
