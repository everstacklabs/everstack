DROP INDEX IF EXISTS idx_custom_models_tenant_id;

ALTER TABLE custom_models
    DROP CONSTRAINT IF EXISTS custom_models_tenant_provider_model_key;

ALTER TABLE custom_models
    ADD CONSTRAINT custom_models_provider_name_model_name_key
    UNIQUE (provider_name, model_name);

ALTER TABLE custom_models
    DROP COLUMN tenant_id;
