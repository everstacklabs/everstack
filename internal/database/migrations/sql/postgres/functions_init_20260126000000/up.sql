-- Create functions table for serverless function definitions
CREATE TABLE IF NOT EXISTS functions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,

    -- Execution mode: 'webhook', 'proxy', 'isolated'
    mode VARCHAR(50) NOT NULL CHECK (mode IN ('webhook', 'proxy', 'isolated')),

    -- Common parameters schema (JSON Schema format)
    parameters JSONB NOT NULL DEFAULT '{}',

    -- Webhook mode configuration
    webhook_url TEXT,
    webhook_method VARCHAR(10) DEFAULT 'POST',
    webhook_headers JSONB DEFAULT '{}',
    webhook_timeout_ms INTEGER DEFAULT 30000,

    -- Proxy mode configuration
    proxy_base_url TEXT,
    proxy_path TEXT,
    proxy_method VARCHAR(10) DEFAULT 'GET',
    proxy_query_mapping JSONB DEFAULT '{}',
    proxy_header_mapping JSONB DEFAULT '{}',
    proxy_body_mapping JSONB DEFAULT '{}',
    proxy_response_mapping JSONB DEFAULT '{}',

    -- Isolated mode (Phase 2 - schema only)
    runtime VARCHAR(50),
    code TEXT,
    packages TEXT[],

    -- Resource limits
    timeout_ms INTEGER NOT NULL DEFAULT 30000,
    memory_mb INTEGER NOT NULL DEFAULT 512,
    max_retries INTEGER NOT NULL DEFAULT 0,

    -- State
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Unique constraint per tenant
    CONSTRAINT uq_functions_tenant_name UNIQUE(tenant_id, name)
);

-- Indexes for efficient lookups
CREATE INDEX IF NOT EXISTS idx_functions_tenant_id ON functions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_functions_tenant_name ON functions(tenant_id, name);
CREATE INDEX IF NOT EXISTS idx_functions_mode ON functions(mode);
CREATE INDEX IF NOT EXISTS idx_functions_enabled ON functions(enabled);

-- Function execution audit log
CREATE TABLE IF NOT EXISTS function_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    function_id UUID NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    request_id VARCHAR(255) NOT NULL,
    tenant_id UUID NOT NULL,

    -- Execution details
    mode VARCHAR(50) NOT NULL,
    tool_call_id VARCHAR(255),

    -- Timing
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    duration_ms INTEGER,

    -- Result
    success BOOLEAN NOT NULL DEFAULT FALSE,
    error_type VARCHAR(255),
    error_message TEXT,

    -- Input/output (truncated for storage)
    input_preview TEXT,
    output_preview TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_fn_exec_function_id ON function_executions(function_id);
CREATE INDEX IF NOT EXISTS idx_fn_exec_tenant_id ON function_executions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_fn_exec_request_id ON function_executions(request_id);
CREATE INDEX IF NOT EXISTS idx_fn_exec_created_at ON function_executions(created_at DESC);
