/**
 * Memory store type definitions
 */

// ── Collection types ────────────────────────────────────────────────

export interface MemoryCollection {
    id: string;
    tenant_id: string;
    name: string;
    description: string;
    embedding_model: string;
    embedding_dimension: number;
    distance_metric: string;
    document_count: number;
    metadata?: Record<string, string>;
    created_at: string;
    updated_at: string;
}

export interface CreateCollectionParams {
    name: string;
    description?: string;
    embedding_model?: string;
    embedding_dimension?: number;
    distance_metric?: string;
    metadata?: Record<string, string>;
}

export interface CreateCollectionResponse {
    collection: MemoryCollection;
}

export interface ListCollectionsResponse {
    collections: MemoryCollection[];
}

export interface DeleteCollectionResponse {
    success: boolean;
}

// ── Document types ──────────────────────────────────────────────────

export interface DocumentInput {
    content: string;
    metadata?: Record<string, string>;
    source?: string;
}

export interface AddDocumentsParams {
    documents: DocumentInput[];
    chunk_size?: number;
}

export interface AddDocumentsResponse {
    document_ids: string[];
    chunks_created: number;
}

export interface DeleteDocumentResponse {
    success: boolean;
}

// ── Query types ─────────────────────────────────────────────────────

export interface QueryParams {
    query: string;
    top_k?: number;
    min_score?: number;
    metadata_filter?: Record<string, string>;
}

export interface SearchResult {
    document_id: string;
    chunk_text: string;
    chunk_index: number;
    score: number;
    metadata?: Record<string, string>;
}

export interface QueryResponse {
    results: SearchResult[];
}

// ── Analytics types ─────────────────────────────────────────────────

export interface AnalyticsBucket {
    timestamp: string;
    query_count: number;
    store_count: number;
    delete_count: number;
    error_count: number;
    avg_latency_ms: number;
}

export interface CollectionStat {
    collection_name: string;
    request_count: number;
}

export interface AnalyticsSummary {
    buckets: AnalyticsBucket[];
    total_requests: number;
    total_errors: number;
    avg_latency_ms: number;
    top_collections: CollectionStat[];
}
