-- Tenant classification rules: extend the built-in trace-kind classifier with
-- a tenant's own span-name patterns, e.g. "retriever.% -> retrieval", so the
-- Type column shows kinds that matter to them. Append-only read-latest per
-- (TenantId, Pattern, Kind); delete inserts a tombstone (IsActive = 0).
CREATE TABLE IF NOT EXISTS otel_trace_classification_rules (
    TenantId String CODEC(ZSTD(1)),
    Pattern String CODEC(ZSTD(1)),
    Kind String CODEC(ZSTD(1)),
    IsActive UInt8 DEFAULT 1 CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_class_rule_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (TenantId, Pattern, Kind, toUnixTimestamp(UpdatedAt))
SETTINGS index_granularity = 8192;
