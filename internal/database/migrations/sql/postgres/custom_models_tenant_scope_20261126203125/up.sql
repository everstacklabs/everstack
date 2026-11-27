ALTER TABLE custom_models
    ADD COLUMN tenant_id TEXT NOT NULL DEFAULT '';

ALTER TABLE custom_models
    DROP CONSTRAINT IF EXISTS custom_models_provider_name_model_name_key;

ALTER TABLE custom_models
    ADD CONSTRAINT custom_models_tenant_provider_model_key
    UNIQUE (tenant_id, provider_name, model_name);

CREATE INDEX idx_custom_models_tenant_id
    ON custom_models (tenant_id);
