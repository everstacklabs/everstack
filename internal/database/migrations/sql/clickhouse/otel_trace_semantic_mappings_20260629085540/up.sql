-- Tenant semantic mappings: alias a tenant's own span-attribute names into our
-- typed fields (model/provider/session/user/cost/tokens/input/output) so a
-- non-standard SDK's attributes populate the built-in columns without us
-- editing semconv.go. Append-only with read-latest per (TenantId, Field,
-- AttrKey); delete inserts a tombstone (IsActive = 0).
CREATE TABLE IF NOT EXISTS otel_trace_semantic_mappings (
    TenantId String CODEC(ZSTD(1)),
    Field LowCardinality(String) CODEC(ZSTD(1)),
    AttrKey String CODEC(ZSTD(1)),
    IsActive UInt8 DEFAULT 1 CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_sem_map_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (TenantId, Field, AttrKey, toUnixTimestamp(UpdatedAt))
SETTINGS index_granularity = 8192;
