-- sampling_eval_rules — continuously sample prod traces matching a filter
-- and run scorers against them. Output rows land in otel_trace_scores via
-- the existing score_recorder so the trace detail UI / dashboards / CI gate
-- all see the resulting scores with no extra wiring.
--
-- Direct attack on Braintrust / Langfuse: this is the "online eval" loop
-- that lets a team set up a scorer once and have it keep grading production
-- without manual eval-run kicks.

CREATE TABLE IF NOT EXISTS sampling_eval_rules (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,

    name VARCHAR(512) NOT NULL,
    description TEXT DEFAULT '',

    -- Filter is a JSONB doc with the same shape as ListRichTracesRequest
    -- (subset): { environment, tags[], user_id, session_id, thread_id,
    -- model, provider, metadata[] }. Empty doc = match all.
    filter_predicate JSONB DEFAULT '{}',

    -- 0.0..1.0 — fraction of matching traces to score. The runner uses
    -- this with a tenant-stable hash so the same trace is consistently
    -- in or out across re-runs.
    sample_rate DOUBLE PRECISION NOT NULL DEFAULT 1.0
        CHECK (sample_rate >= 0 AND sample_rate <= 1),

    -- Score config IDs to apply to every sampled trace.
    scorer_config_ids TEXT[] DEFAULT '{}',

    -- How far back the runner looks each tick — both as a backstop (won't
    -- re-score the same trace twice in this window) and as the lower
    -- bound for the catch-up scan.
    lookback_seconds INT NOT NULL DEFAULT 300,

    -- Polling interval. 0 = never auto-run; rule still works via
    -- RunSamplingRuleNow.
    interval_seconds INT NOT NULL DEFAULT 60,

    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at TIMESTAMPTZ,
    last_run_trace_count INT NOT NULL DEFAULT 0,
    last_run_error TEXT DEFAULT '',
    last_processed_trace_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sampling_eval_rules_tenant_id ON sampling_eval_rules(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sampling_eval_rules_enabled ON sampling_eval_rules(enabled) WHERE enabled = TRUE;

CREATE OR REPLACE FUNCTION sampling_eval_rules_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sampling_eval_rules_updated_at ON sampling_eval_rules;
CREATE TRIGGER trg_sampling_eval_rules_updated_at
    BEFORE UPDATE ON sampling_eval_rules
    FOR EACH ROW
    EXECUTE FUNCTION sampling_eval_rules_set_updated_at();
