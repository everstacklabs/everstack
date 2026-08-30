-- Revert the partial unique indexes back to full UNIQUE constraints.
-- Operators must ensure no archived rows share a (tenant_id, name) with
-- an active row before running this rollback, or the ALTER will fail
-- with a uniqueness violation.

DROP INDEX IF EXISTS uq_score_configs_tenant_name_active;
ALTER TABLE score_configs
    ADD CONSTRAINT uq_score_configs_tenant_name UNIQUE(tenant_id, name);

DROP INDEX IF EXISTS uq_datasets_tenant_name_active;
ALTER TABLE datasets
    ADD CONSTRAINT uq_datasets_tenant_name UNIQUE(tenant_id, name);
