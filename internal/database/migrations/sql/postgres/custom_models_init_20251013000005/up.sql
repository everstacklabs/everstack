-- Create custom_models table for storing user-configured models from meta-providers

-- Create model_source enum type
CREATE TYPE model_source AS ENUM ('manual', 'discovered');

CREATE TABLE IF NOT EXISTS custom_models (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_name TEXT NOT NULL,
    model_name TEXT NOT NULL,
    display_name TEXT NOT NULL,
    model_metadata JSONB DEFAULT '{}'::jsonb,
    source model_source NOT NULL DEFAULT 'manual',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    -- Ensure unique model per provider
    UNIQUE(provider_name, model_name)
);

-- Create indexes for efficient lookups
CREATE INDEX IF NOT EXISTS idx_custom_models_provider_name 
    ON custom_models(provider_name);

CREATE INDEX IF NOT EXISTS idx_custom_models_is_active 
    ON custom_models(is_active);

CREATE INDEX IF NOT EXISTS idx_custom_models_provider_active 
    ON custom_models(provider_name, is_active);

-- Add foreign key constraint to provider_configurations
-- Constraint may already exist from previous migration attempt, skip if so
-- ALTER TABLE custom_models
--     ADD CONSTRAINT fk_custom_models_provider
--     FOREIGN KEY (provider_name)
--     REFERENCES provider_configurations(provider_name)
--     ON DELETE CASCADE;

-- Add comments
COMMENT ON TABLE custom_models IS 'Stores user-configured custom models from meta-providers (OpenRouter, HuggingFace, Ollama, etc.)';
COMMENT ON COLUMN custom_models.provider_name IS 'Name of the meta-provider this model belongs to';
COMMENT ON COLUMN custom_models.model_name IS 'Unique identifier for the model as used in API calls';
COMMENT ON COLUMN custom_models.display_name IS 'Human-readable name for the model';
COMMENT ON COLUMN custom_models.model_metadata IS 'JSON metadata: pricing, capabilities, max_tokens, context_length, etc.';
COMMENT ON COLUMN custom_models.source IS 'How the model was added: manual entry or discovered via API';
COMMENT ON COLUMN custom_models.is_active IS 'Whether the model is currently active and available for use';

