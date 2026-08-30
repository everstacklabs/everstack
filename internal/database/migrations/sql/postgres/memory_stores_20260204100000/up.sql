-- Memory collections (always created — backend-agnostic metadata)
CREATE TABLE IF NOT EXISTS memory_collections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    embedding_model VARCHAR(255) NOT NULL,
    embedding_dimension INTEGER NOT NULL,
    distance_metric VARCHAR(20) NOT NULL DEFAULT 'cosine',
    metadata JSONB DEFAULT '{}',
    document_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- Memory documents (always created — backend-agnostic)
CREATE TABLE IF NOT EXISTS memory_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES memory_collections(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    source VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Memory embeddings base table (pgvector embedding column added on-demand
-- via memory.EnsurePgVector() when the memory feature is enabled)
CREATE TABLE IF NOT EXISTS memory_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES memory_documents(id) ON DELETE CASCADE,
    collection_id UUID NOT NULL REFERENCES memory_collections(id) ON DELETE CASCADE,
    chunk_text TEXT NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes on base tables
CREATE INDEX IF NOT EXISTS idx_memory_collections_tenant ON memory_collections (tenant_id);
CREATE INDEX IF NOT EXISTS idx_memory_documents_collection ON memory_documents (collection_id);
CREATE INDEX IF NOT EXISTS idx_memory_documents_tenant ON memory_documents (tenant_id);
CREATE INDEX IF NOT EXISTS idx_memory_embeddings_collection ON memory_embeddings (collection_id);
CREATE INDEX IF NOT EXISTS idx_memory_embeddings_document ON memory_embeddings (document_id);
