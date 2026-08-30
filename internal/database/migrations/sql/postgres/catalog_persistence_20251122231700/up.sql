-- Create catalog_providers table for persisting provider configurations
CREATE TABLE IF NOT EXISTS catalog_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) UNIQUE NOT NULL,
    display_name VARCHAR(255),
    base_url TEXT,
    api_version VARCHAR(50),
    config JSONB,  -- Full provider config (auth, rate_limits, capabilities, etc.)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create catalog_models table for persisting model definitions
CREATE TABLE IF NOT EXISTS catalog_models (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_name VARCHAR(255) NOT NULL,
    model_name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255),
    max_tokens INTEGER,
    max_completion_tokens INTEGER,
    input_cost_per_1k DECIMAL(12, 8),
    output_cost_per_1k DECIMAL(12, 8),
    capabilities JSONB,  -- Array of capabilities: ["chat", "function_calling", "vision"]
    status VARCHAR(50),  -- stable, beta, deprecated
    config JSONB,  -- Full model config for additional fields
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_name, model_name)
);

-- Create catalog_metadata table for tracking catalog versions and sync status
CREATE TABLE IF NOT EXISTS catalog_metadata (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,  -- 'filesystem', 'github', 'embedded'
    synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    checksum VARCHAR(64),  -- SHA256 of combined catalog for validation
    models_count INTEGER DEFAULT 0,
    providers_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create indexes for catalog_providers
CREATE INDEX IF NOT EXISTS idx_catalog_providers_name 
    ON catalog_providers(name);

-- Create indexes for catalog_models
CREATE INDEX IF NOT EXISTS idx_catalog_models_provider_name 
    ON catalog_models(provider_name);

CREATE INDEX IF NOT EXISTS idx_catalog_models_provider_model 
    ON catalog_models(provider_name, model_name);

CREATE INDEX IF NOT EXISTS idx_catalog_models_status 
    ON catalog_models(status);

-- Create indexes for catalog_metadata
CREATE INDEX IF NOT EXISTS idx_catalog_metadata_version 
    ON catalog_metadata(version);

CREATE INDEX IF NOT EXISTS idx_catalog_metadata_synced_at 
    ON catalog_metadata(synced_at DESC);

-- Add comments for documentation
COMMENT ON TABLE catalog_providers IS 'Persisted provider catalog from model-catalog repository';
COMMENT ON TABLE catalog_models IS 'Persisted model catalog from model-catalog repository';
COMMENT ON TABLE catalog_metadata IS 'Tracks catalog version and sync metadata';

COMMENT ON COLUMN catalog_providers.config IS 'Full provider configuration including auth, rate_limits, capabilities, model_families, defaults, and error_mapping';
COMMENT ON COLUMN catalog_models.config IS 'Additional model configuration fields not in dedicated columns';
COMMENT ON COLUMN catalog_models.capabilities IS 'Array of model capabilities: chat, function_calling, vision, embeddings, etc.';
COMMENT ON COLUMN catalog_metadata.source IS 'Source of catalog data: filesystem (dev), github (production), or embedded (binary)';
COMMENT ON COLUMN catalog_metadata.checksum IS 'SHA256 hash of catalog data for integrity verification';
