-- Add catalog sync fields to provider_configurations table
ALTER TABLE provider_configurations
ADD COLUMN IF NOT EXISTS catalog_status TEXT DEFAULT '',
ADD COLUMN IF NOT EXISTS is_from_catalog BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS catalog_synced_at TIMESTAMPTZ,
ADD COLUMN IF NOT EXISTS deprecated_at TIMESTAMPTZ;

-- Create index on catalog_status for faster filtering
CREATE INDEX IF NOT EXISTS idx_provider_configurations_catalog_status 
    ON provider_configurations(catalog_status);

-- Create index on is_from_catalog for faster filtering
CREATE INDEX IF NOT EXISTS idx_provider_configurations_is_from_catalog 
    ON provider_configurations(is_from_catalog);

-- Add comments to new columns
COMMENT ON COLUMN provider_configurations.catalog_status IS 'Status of provider in catalog: available, configured, active, deprecated';
COMMENT ON COLUMN provider_configurations.is_from_catalog IS 'True if provider was synced from catalog';
COMMENT ON COLUMN provider_configurations.catalog_synced_at IS 'Timestamp of last catalog sync';
COMMENT ON COLUMN provider_configurations.deprecated_at IS 'Timestamp when provider was deprecated';

-- Create provider_model_status table
CREATE TABLE IF NOT EXISTS provider_model_status (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_name TEXT NOT NULL,
    model_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'available', -- available, configured, active, deprecated
    freshness TEXT NOT NULL DEFAULT 'stable', -- new, stable
    marked_new_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_name, model_name)
);

-- Create index on provider_name for faster lookups
CREATE INDEX IF NOT EXISTS idx_provider_model_status_provider_name 
    ON provider_model_status(provider_name);

-- Create index on status for faster filtering
CREATE INDEX IF NOT EXISTS idx_provider_model_status_status 
    ON provider_model_status(status);

-- Create index on freshness for faster filtering (for "new" badge)
CREATE INDEX IF NOT EXISTS idx_provider_model_status_freshness 
    ON provider_model_status(freshness);

-- Create composite index for provider and model lookups
CREATE INDEX IF NOT EXISTS idx_provider_model_status_provider_model 
    ON provider_model_status(provider_name, model_name);

-- Add comments to table
COMMENT ON TABLE provider_model_status IS 'Tracks status and freshness of models from catalog';
COMMENT ON COLUMN provider_model_status.status IS 'Status of model: available, configured, active, deprecated';
COMMENT ON COLUMN provider_model_status.freshness IS 'Freshness indicator: new (< 8 weeks), stable (>= 8 weeks)';
COMMENT ON COLUMN provider_model_status.marked_new_at IS 'Timestamp when model was first marked as new';
COMMENT ON COLUMN provider_model_status.deprecated_at IS 'Timestamp when model was deprecated';

