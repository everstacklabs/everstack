-- Request and token counters per billable organization per UTC hour,
-- incremented by every gateway replica at the provider dispatch seam for
-- every served attempt, platform and BYOK alike: these feed the usage
-- display, never a charge. Increments are atomic upserts, so any number of
-- replicas share one row per (organization, hour). A single elected reporter
-- sums the hours inside the organization's current billing period and sends
-- that period-cumulative total to billing's usage snapshot endpoint, which
-- stores it replace-on-write in billing.usage_totals. Period assignment
-- happens at report time, so a period rollover cannot strand counts in a
-- row keyed to the old period. Platform-level, not tenant-scoped:
-- organization_id is the billable organization, so this table is
-- deliberately not in the RLS tenant-isolation list.
CREATE TABLE IF NOT EXISTS everstack.inference_usage_counters (
    organization_id TEXT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    requests BIGINT NOT NULL DEFAULT 0 CHECK (requests >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, bucket_start)
);

CREATE INDEX IF NOT EXISTS idx_inference_usage_counters_bucket
    ON everstack.inference_usage_counters (bucket_start);
