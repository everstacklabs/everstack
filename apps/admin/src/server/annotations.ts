import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { AnnotationService } from '@everstack/proto/everstack/annotations/v1/annotations_service_pb'
import type {
    CreateQueueRequest,
    CreateQueueResponse,
    GetQueueRequest,
    GetQueueResponse,
    ListQueuesRequest,
    ListQueuesResponse,
    UpdateQueueRequest,
    UpdateQueueResponse,
    DeleteQueueRequest,
    DeleteQueueResponse,
    AddItemToQueueRequest,
    AddItemToQueueResponse,
    GetNextItemRequest,
    GetNextItemResponse,
    SubmitAnnotationRequest,
    SubmitAnnotationResponse,
    SkipItemRequest,
    SkipItemResponse,
    ListQueueItemsRequest,
    ListQueueItemsResponse,
    GetQueueStatsRequest,
    GetQueueStatsResponse,
    AnnotationQueue,
    AnnotationQueueItem,
    QueueStats,
    PopulateFromTracesRequest,
    PopulateFromTracesResponse,
    AddItemsToQueueBatchRequest,
    AddItemsToQueueBatchResponse,
} from '@everstack/proto/everstack/annotations/v1/annotations_pb'
import {
    CreateQueueRequestSchema,
    GetQueueRequestSchema,
    ListQueuesRequestSchema,
    UpdateQueueRequestSchema,
    DeleteQueueRequestSchema,
    AddItemToQueueRequestSchema,
    GetNextItemRequestSchema,
    SubmitAnnotationRequestSchema,
    SkipItemRequestSchema,
    ListQueueItemsRequestSchema,
    GetQueueStatsRequestSchema,
    PopulateFromTracesRequestSchema,
    AddItemsToQueueBatchRequestSchema,
} from '@everstack/proto/everstack/annotations/v1/annotations_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const annotationClient = createClientFor(AnnotationService)(transport)

// ─── Queue CRUD ──────────────────────────────────────────────────────

export type CreateQueueParams = {
    tenantId: string
    name: string
    description?: string
    scoreConfigIds: string[]
}

export async function createQueue(params: CreateQueueParams): Promise<CreateQueueResponse> {
    const req: CreateQueueRequest = create(CreateQueueRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        description: params.description,
        scoreConfigIds: params.scoreConfigIds,
    })
    return annotationClient.createQueue(req)
}

export async function getQueue(id: string, tenantId: string): Promise<GetQueueResponse> {
    const req: GetQueueRequest = create(GetQueueRequestSchema, { tenantId, id })
    return annotationClient.getQueue(req)
}

export type ListQueuesParams = {
    tenantId?: string
    limit?: number
    offset?: number
}

export async function listQueues(params: ListQueuesParams = {}): Promise<ListQueuesResponse> {
    const req: ListQueuesRequest = create(ListQueuesRequestSchema, {
        tenantId: params.tenantId ?? '',
        limit: params.limit,
        offset: params.offset,
    })
    return annotationClient.listQueues(req)
}

export type UpdateQueueParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    scoreConfigIds?: string[]
}

export async function updateQueue(params: UpdateQueueParams): Promise<UpdateQueueResponse> {
    const req: UpdateQueueRequest = create(UpdateQueueRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        scoreConfigIds: params.scoreConfigIds,
    })
    return annotationClient.updateQueue(req)
}

export async function deleteQueue(id: string, tenantId: string): Promise<DeleteQueueResponse> {
    const req: DeleteQueueRequest = create(DeleteQueueRequestSchema, { tenantId, id })
    return annotationClient.deleteQueue(req)
}

// ─── Queue Items & Annotations ───────────────────────────────────────

export type AddItemToQueueParams = {
    tenantId: string
    queueId: string
    traceId: string
    observationId?: string
}

export async function addItemToQueue(params: AddItemToQueueParams): Promise<AddItemToQueueResponse> {
    const req: AddItemToQueueRequest = create(AddItemToQueueRequestSchema, {
        tenantId: params.tenantId,
        queueId: params.queueId,
        traceId: params.traceId,
        observationId: params.observationId,
    })
    return annotationClient.addItemToQueue(req)
}

export type GetNextItemParams = {
    tenantId: string
    queueId: string
    annotatorId?: string
}

export async function getNextItem(params: GetNextItemParams): Promise<GetNextItemResponse> {
    const req: GetNextItemRequest = create(GetNextItemRequestSchema, {
        tenantId: params.tenantId,
        queueId: params.queueId,
        annotatorId: params.annotatorId,
    })
    return annotationClient.getNextItem(req)
}

export type SubmitAnnotationParams = {
    tenantId: string
    itemId: string
    completedBy?: string
    scores: Record<string, number> | Array<{ scoreConfigId: string; scoreId: string }>
    comment?: string
    queueId?: string
}

export async function submitAnnotation(params: SubmitAnnotationParams): Promise<SubmitAnnotationResponse> {
    // Map Record<configId, value> to proto AnnotationScoreEntry[] if needed
    const scoreEntries = Array.isArray(params.scores)
        ? params.scores
        : Object.entries(params.scores).map(([scoreConfigId, val]) => ({
              scoreConfigId,
              scoreId: String(val),
          }))
    const req: SubmitAnnotationRequest = create(SubmitAnnotationRequestSchema, {
        tenantId: params.tenantId,
        itemId: params.itemId,
        completedBy: params.completedBy ?? '',
        scores: scoreEntries,
    } as any)
    return annotationClient.submitAnnotation(req)
}

export type SkipItemParams = {
    tenantId: string
    itemId: string
    skippedBy?: string
    queueId?: string
}

export async function skipItem(params: SkipItemParams): Promise<SkipItemResponse> {
    const req: SkipItemRequest = create(SkipItemRequestSchema, {
        tenantId: params.tenantId,
        itemId: params.itemId,
        skippedBy: params.skippedBy,
    })
    return annotationClient.skipItem(req)
}

export type ListQueueItemsParams = {
    tenantId?: string
    queueId: string
    status?: number
    limit?: number
    offset?: number
}

export async function listQueueItems(params: ListQueueItemsParams): Promise<ListQueueItemsResponse> {
    const req: ListQueueItemsRequest = create(ListQueueItemsRequestSchema, {
        tenantId: params.tenantId ?? '',
        queueId: params.queueId,
        status: params.status,
        limit: params.limit,
        offset: params.offset,
    })
    return annotationClient.listQueueItems(req)
}

export async function getQueueStats(queueId: string, tenantId: string): Promise<GetQueueStatsResponse> {
    const req: GetQueueStatsRequest = create(GetQueueStatsRequestSchema, { tenantId, queueId })
    return annotationClient.getQueueStats(req)
}

// ─── Batch Items ─────────────────────────────────────────────────────

export type AddItemsToQueueBatchParams = {
    tenantId: string
    queueId: string
    items: Array<{ traceId: string; observationId?: string; priority?: number }>
}

export async function addItemsToQueueBatch(params: AddItemsToQueueBatchParams): Promise<AddItemsToQueueBatchResponse> {
    const req: AddItemsToQueueBatchRequest = create(AddItemsToQueueBatchRequestSchema, {
        tenantId: params.tenantId,
        queueId: params.queueId,
        items: params.items,
    })
    return annotationClient.addItemsToQueueBatch(req)
}

// ─── Populate from Traces ────────────────────────────────────────────

export type PopulateFromTracesParams = {
    tenantId: string
    queueId: string
    traceFilter?: string
    maxItems?: number
}

export async function populateFromTraces(params: PopulateFromTracesParams): Promise<PopulateFromTracesResponse> {
    const req: PopulateFromTracesRequest = create(PopulateFromTracesRequestSchema, {
        tenantId: params.tenantId,
        queueId: params.queueId,
        traceFilter: params.traceFilter,
        maxItems: params.maxItems,
    })
    return annotationClient.populateFromTraces(req)
}

export type { AnnotationQueue, AnnotationQueueItem, QueueStats }
