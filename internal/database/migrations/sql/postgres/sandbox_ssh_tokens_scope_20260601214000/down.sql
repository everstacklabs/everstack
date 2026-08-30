DROP INDEX IF EXISTS idx_sandbox_ssh_tokens_scope_active;

CREATE INDEX IF NOT EXISTS idx_sandbox_ssh_tokens_sandbox_active
    ON sandbox_ssh_tokens (sandbox_id, tenant_id, expires_at)
    WHERE revoked_at IS NULL;

-- organization_id and instance_id are part of the current base token schema.
-- Keep them on rollback so databases that started from the updated base
-- migration do not become incompatible with current runtime code.
