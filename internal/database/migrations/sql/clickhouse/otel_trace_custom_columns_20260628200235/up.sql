-- User-defined custom trace columns. A tenant registers columns (label + type +
-- source) so the traces table shows the dimensions THEY care about instead of
-- only our fixed set. Append-only with read-latest per (TenantId, ColKey):
-- an edit inserts a new row, a delete inserts a tombstone (IsActive = 0). This
-- mirrors otel_trace_overlays so it reuses the same ClickHouse handle/wiring.
CREATE TABLE IF NOT EXISTS otel_trace_custom_columns (
    TenantId String CODEC(ZSTD(1)),
    ColKey String CODEC(ZSTD(1)),
    Label String CODEC(ZSTD(1)),
    ValueType LowCardinality(String) CODEC(ZSTD(1)),
    Source LowCardinality(String) CODEC(ZSTD(1)),
    SourceRef String CODEC(ZSTD(1)),
    Position Int32 CODEC(ZSTD(1)),
    IsActive UInt8 DEFAULT 1 CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_custom_col_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (TenantId, ColKey, toUnixTimestamp(UpdatedAt))
SETTINGS index_granularity = 8192;
