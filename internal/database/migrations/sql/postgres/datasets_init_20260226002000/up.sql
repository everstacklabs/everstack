-- Create datasets table for evaluation dataset definitions
CREATE TABLE IF NOT EXISTS datasets (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT DEFAULT '',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT uq_datasets_tenant_name UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_datasets_tenant_id ON datasets(tenant_id);

-- Create dataset_items table for individual test cases within a dataset
CREATE TABLE IF NOT EXISTS dataset_items (
    id VARCHAR(255) PRIMARY KEY,
    dataset_id VARCHAR(255) NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    tenant_id VARCHAR(255) NOT NULL,
    input JSONB NOT NULL,
    expected_output JSONB,
    metadata JSONB DEFAULT '{}',
    source_trace_id VARCHAR(255) DEFAULT '',
    source_observation_id VARCHAR(255) DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dataset_items_dataset_id ON dataset_items(dataset_id);
CREATE INDEX IF NOT EXISTS idx_dataset_items_tenant_id ON dataset_items(tenant_id);
CREATE INDEX IF NOT EXISTS idx_dataset_items_status ON dataset_items(status);

-- Create score_configs table for scoring/grading configuration
CREATE TABLE IF NOT EXISTS score_configs (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    data_type VARCHAR(50) NOT NULL CHECK (data_type IN ('NUMERIC', 'CATEGORICAL', 'BOOLEAN')),
    description TEXT DEFAULT '',
    min_value DOUBLE PRECISION,
    max_value DOUBLE PRECISION,
    categories JSONB,
    eval_prompt TEXT DEFAULT '',
    eval_model VARCHAR(255) DEFAULT '',
    is_archived BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_score_configs_tenant_name UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_score_configs_tenant_id ON score_configs(tenant_id);
