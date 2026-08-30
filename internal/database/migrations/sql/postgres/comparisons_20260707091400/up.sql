-- Materialized eval-run comparisons (design doc section 7a, PR 2b).
-- One row per (tenant, baseline, candidate, comparison-key config); recompute
-- is an explicit upsert. key_config_hash is '' in v1 (identity projection) and
-- exists so a future configurable comparison key can materialize side by side.
CREATE TABLE IF NOT EXISTS comparisons (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    baseline_run_id TEXT NOT NULL,
    candidate_run_id TEXT NOT NULL,
    key_config_hash TEXT NOT NULL DEFAULT '',
    match_mode TEXT NOT NULL,
    scorer_results JSONB NOT NULL DEFAULT '[]',
    overall_verdict TEXT NOT NULL,
    deltas JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, baseline_run_id, candidate_run_id, key_config_hash)
);

CREATE INDEX IF NOT EXISTS idx_comparisons_tenant_candidate ON comparisons(tenant_id, candidate_run_id);
