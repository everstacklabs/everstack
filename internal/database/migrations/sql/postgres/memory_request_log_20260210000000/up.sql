CREATE TABLE IF NOT EXISTS memory_request_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    collection_id VARCHAR(255),
    collection_name VARCHAR(255) NOT NULL DEFAULT '',
    operation VARCHAR(30) NOT NULL,
    caller_type VARCHAR(20),
    caller_id VARCHAR(255),
    trace_id VARCHAR(255),
    span_id VARCHAR(255),
    latency_ms INTEGER NOT NULL DEFAULT 0,
    result_count INTEGER,
    chunk_count INTEGER,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mrl_tenant_time ON memory_request_log (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mrl_collection ON memory_request_log (collection_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mrl_trace ON memory_request_log (trace_id) WHERE trace_id IS NOT NULL;
