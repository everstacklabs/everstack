-- Per-tenant OTLP configuration for sandbox telemetry export.
-- When configured, the sandbox metrics collector also exports to the
-- tenant's OTLP backend (e.g., New Relic, Grafana Cloud, Datadog).

CREATE TABLE IF NOT EXISTS sandbox_otlp_configs (
    tenant_id    VARCHAR(255) PRIMARY KEY,
    endpoint     TEXT         NOT NULL DEFAULT '',
    -- headers is a JSONB map of header name → value (for auth tokens).
    headers      JSONB        NOT NULL DEFAULT '{}',
    -- extra_labels is a JSONB map of additional resource attributes.
    extra_labels JSONB        NOT NULL DEFAULT '{}',
    enabled      BOOLEAN      NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
