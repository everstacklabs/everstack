-- Lightweight time-series storage for per-sandbox resource metrics.
-- Collected every 30s by the MetricsCollector goroutine. Retained for 2h
-- (240 rows per sandbox). Older rows are pruned by the collector itself
-- to avoid unbounded growth without needing a separate cron.

CREATE TABLE IF NOT EXISTS sandbox_metrics_history (
    id           BIGSERIAL   PRIMARY KEY,
    sandbox_id   VARCHAR(255) NOT NULL,
    tenant_id    VARCHAR(255) NOT NULL,
    cpu_percent  DOUBLE PRECISION NOT NULL DEFAULT 0,
    memory_usage BIGINT NOT NULL DEFAULT 0,  -- bytes used
    memory_limit BIGINT NOT NULL DEFAULT 0,  -- bytes available
    disk_used_mb BIGINT NOT NULL DEFAULT 0,
    collected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Fast lookup by sandbox + time for the history endpoint.
CREATE INDEX IF NOT EXISTS idx_sandbox_metrics_history_lookup
    ON sandbox_metrics_history (sandbox_id, collected_at DESC);

-- Tenant-level index for the admin overview.
CREATE INDEX IF NOT EXISTS idx_sandbox_metrics_history_tenant
    ON sandbox_metrics_history (tenant_id, collected_at DESC);
