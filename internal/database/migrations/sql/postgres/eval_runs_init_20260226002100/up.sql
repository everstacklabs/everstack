-- Create eval_runs table for evaluation run tracking
CREATE TABLE IF NOT EXISTS eval_runs (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,
    dataset_id VARCHAR(255) NOT NULL REFERENCES datasets(id),
    name VARCHAR(512) NOT NULL,
    description TEXT DEFAULT '',
    status VARCHAR(50) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    eval_target_type VARCHAR(50) NOT NULL DEFAULT '',
    eval_target_id VARCHAR(255) DEFAULT '',
    eval_config JSONB DEFAULT '{}',
    scorer_config_ids TEXT[] DEFAULT '{}',
    total_items INT NOT NULL DEFAULT 0,
    completed_items INT NOT NULL DEFAULT 0,
    failed_items INT NOT NULL DEFAULT 0,
    score_summary JSONB DEFAULT '{}',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_eval_runs_tenant_id ON eval_runs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_dataset_id ON eval_runs(dataset_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_status ON eval_runs(status);

-- Create eval_run_items table for per-item evaluation results
CREATE TABLE IF NOT EXISTS eval_run_items (
    id VARCHAR(255) PRIMARY KEY,
    eval_run_id VARCHAR(255) NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    dataset_item_id VARCHAR(255) NOT NULL REFERENCES dataset_items(id),
    tenant_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'skipped')),
    output JSONB,
    trace_id VARCHAR(255) DEFAULT '',
    latency_ms BIGINT DEFAULT 0,
    cost DOUBLE PRECISION DEFAULT 0,
    token_usage JSONB DEFAULT '{}',
    error TEXT DEFAULT '',
    scores JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_eval_run_items_eval_run_id ON eval_run_items(eval_run_id);
CREATE INDEX IF NOT EXISTS idx_eval_run_items_tenant_id ON eval_run_items(tenant_id);
CREATE INDEX IF NOT EXISTS idx_eval_run_items_status ON eval_run_items(status);
