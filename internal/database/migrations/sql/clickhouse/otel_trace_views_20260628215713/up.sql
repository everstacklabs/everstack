-- Saved views for the traces table: a named bundle of visible columns +
-- filters + sort, so a tenant can flip between "RAG view", "on-call errors",
-- etc. The config is an opaque JSON blob owned by the frontend; the backend is
-- a dumb CRUD store. Append-only with read-latest per (TenantId, ViewId);
-- delete inserts a tombstone (IsActive = 0). Mirrors otel_trace_custom_columns.
CREATE TABLE IF NOT EXISTS otel_trace_views (
    TenantId String CODEC(ZSTD(1)),
    ViewId String CODEC(ZSTD(1)),
    Name String CODEC(ZSTD(1)),
    ConfigJson String CODEC(ZSTD(1)),
    AuthorUserId String CODEC(ZSTD(1)),
    IsActive UInt8 DEFAULT 1 CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_trace_view_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
ORDER BY (TenantId, ViewId, toUnixTimestamp(UpdatedAt))
SETTINGS index_granularity = 8192;
