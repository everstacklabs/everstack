CREATE TABLE IF NOT EXISTS dataset_versions (
    id TEXT PRIMARY KEY,
    dataset_id TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL,
    version_number INT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    item_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT '',

    CONSTRAINT uq_dataset_versions_dataset_version UNIQUE(dataset_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_dataset_versions_tenant_dataset
    ON dataset_versions(tenant_id, dataset_id);

CREATE TABLE IF NOT EXISTS dataset_version_items (
    id TEXT PRIMARY KEY,
    dataset_version_id TEXT NOT NULL REFERENCES dataset_versions(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL,
    source_dataset_item_id TEXT,
    input JSONB NOT NULL,
    expected_output JSONB,
    metadata JSONB DEFAULT '{}',
    source_trace_id TEXT DEFAULT '',
    source_observation_id TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dataset_version_items_version
    ON dataset_version_items(dataset_version_id);

ALTER TABLE eval_runs
    ADD COLUMN IF NOT EXISTS dataset_version_id TEXT;
