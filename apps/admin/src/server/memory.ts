import { getApiBaseUrl } from '@/lib/api-url'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const rpcBase = `${baseUrl}${connectBase}/everstack.memory.v1.MemoryService`

// ─── Types ──────────────────────────────────────────────────────────

export interface MemoryCollection {
    id: string
    tenantId: string
    name: string
    description: string
    embeddingModel: string
    embeddingDimension: number
    distanceMetric: string
    documentCount: number
    metadata: Record<string, string>
    createdAt: string
    updatedAt: string
}

export interface MemorySearchResult {
    documentId: string
    chunkText: string
    chunkIndex: number
    score: number
    metadata: Record<string, string>
}

export interface AnalyticsBucket {
    timestamp: string
    queryCount: number
    storeCount: number
    deleteCount: number
    errorCount: number
    avgLatencyMs: number
}

export interface MemoryAnalytics {
    buckets: AnalyticsBucket[]
    totalRequests: number
    totalErrors: number
    avgLatencyMs: number
    topCollections: { collectionName: string; requestCount: number }[]
}

// ─── ConnectRPC Transport ───────────────────────────────────────────

function friendlyMemoryError(status: number, raw: string): string {
    if (raw.includes('connection refused') || raw.includes('Unavailable'))
        return 'Memory service is currently unavailable. The vector database may be starting up or unreachable.'
    if (status === 401 || status === 403) return 'You do not have permission to access the memory service.'
    if (status === 404) return 'Memory service endpoint not found. Please check your deployment configuration.'
    return 'An unexpected error occurred with the memory service. Please try again later.'
}

export class MemoryServiceError extends Error {
    status: number
    detail: string
    constructor(status: number, detail: string, friendly: string) {
        super(friendly)
        this.name = 'MemoryServiceError'
        this.status = status
        this.detail = detail
    }
}

async function connectRPC<TReq, TResp>(method: string, body: TReq): Promise<TResp> {
    const res = await fetch(`${rpcBase}/${method}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(body),
    })
    if (!res.ok) {
        const text = await res.text()
        throw new MemoryServiceError(res.status, text, friendlyMemoryError(res.status, text))
    }
    return res.json()
}

// ─── API Functions ──────────────────────────────────────────────────

export async function listCollections(tenantId: string): Promise<MemoryCollection[]> {
    try {
        const resp = await connectRPC<{ tenantId: string }, { collections?: MemoryCollection[] }>(
            'ListCollections',
            { tenantId }
        )
        return resp.collections ?? []
    } catch (err) {
        // 503 = memory backend not configured on this instance. Treat as
        // "feature off" rather than surfacing a console error.
        if (err instanceof MemoryServiceError && err.status === 503) return []
        throw err
    }
}

export async function getCollection(tenantId: string, name: string): Promise<MemoryCollection> {
    const resp = await connectRPC<
        { tenantId: string; name: string },
        { collection?: MemoryCollection }
    >('GetCollection', { tenantId, name })
    if (!resp.collection) throw new Error('Collection not found')
    return resp.collection
}

export async function createCollection(params: {
    tenantId: string
    name: string
    description?: string
    embeddingModel?: string
    embeddingDimension?: number
    distanceMetric?: string
}): Promise<MemoryCollection> {
    const resp = await connectRPC<
        {
            tenantId: string
            name: string
            description?: string
            embeddingModel?: string
            embeddingDimension?: number
            distanceMetric?: string
        },
        { collection?: MemoryCollection }
    >('CreateCollection', {
        tenantId: params.tenantId,
        name: params.name,
        description: params.description ?? '',
        embeddingModel: params.embeddingModel ?? '',
        embeddingDimension: params.embeddingDimension ?? 0,
        distanceMetric: params.distanceMetric ?? 'cosine',
    })
    if (!resp.collection) throw new Error('Failed to create collection')
    return resp.collection
}

export async function deleteCollection(tenantId: string, name: string): Promise<void> {
    await connectRPC<{ tenantId: string; name: string }, { success: boolean }>(
        'DeleteCollection',
        { tenantId, name }
    )
}

export async function addDocuments(params: {
    tenantId: string
    collectionName: string
    content: string
    source?: string
    chunkSize?: number
}): Promise<{ documentIds: string[]; chunksCreated: number }> {
    return connectRPC<
        {
            tenantId: string
            collectionName: string
            documents: { content: string; source?: string }[]
            chunkSize?: number
        },
        { documentIds: string[]; chunksCreated: number }
    >('AddDocuments', {
        tenantId: params.tenantId,
        collectionName: params.collectionName,
        documents: [
            {
                content: params.content,
                source: params.source ?? '',
            },
        ],
        chunkSize: params.chunkSize ?? 512,
    })
}

export async function queryCollection(params: {
    tenantId: string
    collectionName: string
    query: string
    topK?: number
    minScore?: number
}): Promise<MemorySearchResult[]> {
    const resp = await connectRPC<
        {
            tenantId: string
            collectionName: string
            query: string
            topK?: number
            minScore?: number
        },
        { results?: MemorySearchResult[] }
    >('QueryCollection', {
        tenantId: params.tenantId,
        collectionName: params.collectionName,
        query: params.query,
        topK: params.topK ?? 5,
        minScore: params.minScore ?? 0,
    })
    return resp.results ?? []
}

export async function getMemoryAnalytics(
    tenantId: string,
    from: string,
    to: string
): Promise<MemoryAnalytics> {
    const resp = await connectRPC<
        { tenantId: string; from: string; to: string },
        {
            buckets?: AnalyticsBucket[]
            totalRequests?: number
            totalErrors?: number
            avgLatencyMs?: number
            topCollections?: { collectionName: string; requestCount: number }[]
        }
    >('GetMemoryAnalytics', { tenantId, from, to })
    return {
        buckets: resp.buckets ?? [],
        totalRequests: resp.totalRequests ?? 0,
        totalErrors: resp.totalErrors ?? 0,
        avgLatencyMs: resp.avgLatencyMs ?? 0,
        topCollections: resp.topCollections ?? [],
    }
}
