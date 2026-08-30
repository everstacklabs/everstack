-- User-defined custom columns for the logs table. A tenant surfaces a
-- LogAttributes field as a column so the logs list shows what THEY care about.
-- Append-only read-latest per (TenantId, ColKey); delete inserts a tombstone.
-- Mirrors otel_trace_custom_columns.
CREATE TABLE IF NOT EXISTS otel_log_custom_columns (
    TenantId String CODEC(ZSTD(1)),
    ColKey String CODEC(ZSTD(1)),
    Label String CODEC(ZSTD(1)),
    AttrKey String CODEC(ZSTD(1)),
    Position Int32 CODEC(ZSTD(1)),
    IsActive UInt8 DEFAULT 1 CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_log_custom_col_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (TenantId, ColKey, toUnixTimestamp(UpdatedAt))
SETTINGS index_granularity = 8192;
