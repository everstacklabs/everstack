ALTER TABLE sandbox_ssh_tokens
    ADD COLUMN IF NOT EXISTS organization_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS instance_id VARCHAR(255);

UPDATE sandbox_ssh_tokens
   SET organization_id = COALESCE(NULLIF(organization_id, ''), tenant_id),
       instance_id = COALESCE(NULLIF(instance_id, ''), tenant_id)
 WHERE organization_id IS NULL
    OR organization_id = ''
    OR instance_id IS NULL
    OR instance_id = '';

ALTER TABLE sandbox_ssh_tokens
    ALTER COLUMN organization_id SET NOT NULL,
    ALTER COLUMN instance_id SET NOT NULL;

DROP INDEX IF EXISTS idx_sandbox_ssh_tokens_sandbox_active;

CREATE INDEX IF NOT EXISTS idx_sandbox_ssh_tokens_scope_active
    ON sandbox_ssh_tokens (organization_id, tenant_id, instance_id, sandbox_id, expires_at)
    WHERE revoked_at IS NULL;
