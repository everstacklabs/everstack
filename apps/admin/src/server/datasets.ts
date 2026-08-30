import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { DatasetService } from '@everstack/proto/everstack/datasets/v1/datasets_service_pb'
import type {
    CreateDatasetRequest,
    CreateDatasetResponse,
    GetDatasetRequest,
    GetDatasetResponse,
    ListDatasetsRequest,
    ListDatasetsResponse,
    UpdateDatasetRequest,
    UpdateDatasetResponse,
    DeleteDatasetRequest,
    DeleteDatasetResponse,
    CreateDatasetItemRequest,
    CreateDatasetItemResponse,
    CreateDatasetItemBatchRequest,
    CreateDatasetItemBatchResponse,
    ListDatasetItemsRequest,
    ListDatasetItemsResponse,
    UpdateDatasetItemRequest,
    UpdateDatasetItemResponse,
    DeleteDatasetItemRequest,
    DeleteDatasetItemResponse,
    CreateScoreConfigRequest,
    CreateScoreConfigResponse,
    ListScoreConfigsRequest,
    ListScoreConfigsResponse,
    UpdateScoreConfigRequest,
    UpdateScoreConfigResponse,
    DeleteScoreConfigRequest,
    DeleteScoreConfigResponse,
    ListBuiltinMetricsResponse,
    BuiltinMetric,
    Dataset,
    DatasetItem,
    ScoreConfig,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'
import {
    CreateDatasetRequestSchema,
    GetDatasetRequestSchema,
    ListDatasetsRequestSchema,
    UpdateDatasetRequestSchema,
    DeleteDatasetRequestSchema,
    CreateDatasetItemRequestSchema,
    CreateDatasetItemBatchRequestSchema,
    ListDatasetItemsRequestSchema,
    UpdateDatasetItemRequestSchema,
    DeleteDatasetItemRequestSchema,
    CreateScoreConfigRequestSchema,
    ListScoreConfigsRequestSchema,
    UpdateScoreConfigRequestSchema,
    DeleteScoreConfigRequestSchema,
    ListBuiltinMetricsRequestSchema,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'

export type JsonObject = { [k: string]: JsonValue }
export type JsonValue = number | string | boolean | null | JsonObject | JsonValue[]
export type ScoreConfigMessageInput = { role: string; content: string }
export type ScoreConfigModelParamsInput = {
    temperature?: number
    topP?: number
    maxTokens?: number
    stop?: string[]
    toolChoice?: string
}
export type ScoreConfigChoiceScoreInput = { choice: string; score: number }

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
const datasetClient = createClientFor(DatasetService)(transport)

// ─── Dataset CRUD ────────────────────────────────────────────────────

export type CreateDatasetParams = {
    tenantId: string
    name: string
    description?: string
    metadata?: JsonObject
}

export async function createDataset(params: CreateDatasetParams): Promise<CreateDatasetResponse> {
    const req: CreateDatasetRequest = create(CreateDatasetRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        description: params.description,
        metadata: params.metadata,
    })
    return datasetClient.createDataset(req)
}

export async function getDataset(id: string, tenantId: string): Promise<GetDatasetResponse> {
    const req: GetDatasetRequest = create(GetDatasetRequestSchema, { tenantId, id })
    return datasetClient.getDataset(req)
}

export type ListDatasetsParams = {
    tenantId?: string
    limit?: number
    offset?: number
}

export async function listDatasets(params: ListDatasetsParams = {}): Promise<ListDatasetsResponse> {
    const req: ListDatasetsRequest = create(ListDatasetsRequestSchema, {
        tenantId: params.tenantId ?? '',
        limit: params.limit,
        offset: params.offset,
    })
    return datasetClient.listDatasets(req)
}

export type UpdateDatasetParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    metadata?: JsonObject
}

export async function updateDataset(params: UpdateDatasetParams): Promise<UpdateDatasetResponse> {
    const req: UpdateDatasetRequest = create(UpdateDatasetRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        metadata: params.metadata,
    })
    return datasetClient.updateDataset(req)
}

export async function deleteDataset(id: string, tenantId: string): Promise<DeleteDatasetResponse> {
    const req: DeleteDatasetRequest = create(DeleteDatasetRequestSchema, { tenantId, id })
    return datasetClient.deleteDataset(req)
}

// ─── Dataset Items ───────────────────────────────────────────────────

export type CreateDatasetItemParams = {
    tenantId: string
    datasetId: string
    input: JsonObject
    expectedOutput?: JsonObject
    metadata?: JsonObject
    sourceTraceId?: string
    sourceObservationId?: string
}

export async function createDatasetItem(params: CreateDatasetItemParams): Promise<CreateDatasetItemResponse> {
    const req: CreateDatasetItemRequest = create(CreateDatasetItemRequestSchema, {
        tenantId: params.tenantId,
        datasetId: params.datasetId,
        input: params.input,
        expectedOutput: params.expectedOutput,
        metadata: params.metadata,
        sourceTraceId: params.sourceTraceId,
        sourceObservationId: params.sourceObservationId,
    })
    return datasetClient.createDatasetItem(req)
}

export type CreateDatasetItemBatchParams = {
    tenantId: string
    datasetId: string
    items: Array<{
        input: JsonObject
        expectedOutput?: JsonObject
        metadata?: JsonObject
        sourceTraceId?: string
        sourceObservationId?: string
    }>
}

export async function createDatasetItemBatch(params: CreateDatasetItemBatchParams): Promise<CreateDatasetItemBatchResponse> {
    const req: CreateDatasetItemBatchRequest = create(CreateDatasetItemBatchRequestSchema, {
        tenantId: params.tenantId,
        datasetId: params.datasetId,
        items: params.items,
    })
    return datasetClient.createDatasetItemBatch(req)
}

export type ListDatasetItemsParams = {
    tenantId?: string
    datasetId: string
    limit?: number
    offset?: number
}

export async function listDatasetItems(params: ListDatasetItemsParams): Promise<ListDatasetItemsResponse> {
    const req: ListDatasetItemsRequest = create(ListDatasetItemsRequestSchema, {
        tenantId: params.tenantId ?? '',
        datasetId: params.datasetId,
        limit: params.limit,
        offset: params.offset,
    })
    return datasetClient.listDatasetItems(req)
}

export type UpdateDatasetItemParams = {
    tenantId: string
    id: string
    input?: JsonObject
    expectedOutput?: JsonObject
    metadata?: JsonObject
}

export async function updateDatasetItem(params: UpdateDatasetItemParams): Promise<UpdateDatasetItemResponse> {
    const req: UpdateDatasetItemRequest = create(UpdateDatasetItemRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        input: params.input,
        expectedOutput: params.expectedOutput,
        metadata: params.metadata,
    })
    return datasetClient.updateDatasetItem(req)
}

export async function deleteDatasetItem(id: string, tenantId: string): Promise<DeleteDatasetItemResponse> {
    const req: DeleteDatasetItemRequest = create(DeleteDatasetItemRequestSchema, { tenantId, id })
    return datasetClient.deleteDatasetItem(req)
}

// ─── Score Configs ───────────────────────────────────────────────────

export type CreateScoreConfigParams = {
    tenantId: string
    name: string
    dataType: string
    description?: string
    minValue?: number
    maxValue?: number
    categories?: string[]
    evalPrompt?: string
    evalModel?: string
    scorerCode?: string
    scorerLanguage?: string
    useSandbox?: boolean
    slug?: string
    scorerType?: string
    outputType?: string
    messages?: ScoreConfigMessageInput[]
    modelParams?: ScoreConfigModelParamsInput
    choiceScores?: ScoreConfigChoiceScoreInput[]
    useCot?: boolean
    passThreshold?: number
    /** JSON DAG scorer definition (see eval_runner/dag_scorer.go), UTF-8 encoded. */
    dagDefinition?: Uint8Array
}

export async function createScoreConfig(params: CreateScoreConfigParams): Promise<CreateScoreConfigResponse> {
    const categoriesObj: JsonObject | undefined = params.categories
        ? Object.fromEntries(params.categories.map((c, i) => [String(i), c]))
        : undefined

    const req: CreateScoreConfigRequest = create(CreateScoreConfigRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        dataType: params.dataType,
        description: params.description,
        minValue: params.minValue,
        maxValue: params.maxValue,
        categories: categoriesObj,
        evalPrompt: params.evalPrompt,
        evalModel: params.evalModel,
        scorerCode: params.scorerCode,
        scorerLanguage: params.scorerLanguage,
        useSandbox: params.useSandbox,
        slug: params.slug,
        scorerType: params.scorerType,
        outputType: params.outputType,
        messages: params.messages,
        modelParams: params.modelParams,
        choiceScores: params.choiceScores,
        useCot: params.useCot,
        passThreshold: params.passThreshold,
        dagDefinition: params.dagDefinition,
    })
    return datasetClient.createScoreConfig(req)
}

export type ListScoreConfigsParams = {
    tenantId?: string
    limit?: number
    offset?: number
}

export async function listScoreConfigs(params: ListScoreConfigsParams = {}): Promise<ListScoreConfigsResponse> {
    const req: ListScoreConfigsRequest = create(ListScoreConfigsRequestSchema, {
        tenantId: params.tenantId ?? '',
        limit: params.limit,
        offset: params.offset,
    })
    return datasetClient.listScoreConfigs(req)
}

export type UpdateScoreConfigParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    minValue?: number
    maxValue?: number
    categories?: string[]
    evalPrompt?: string
    scorerCode?: string
    scorerLanguage?: string
    useSandbox?: boolean
    dataType?: string
    slug?: string
    scorerType?: string
    outputType?: string
    messages?: ScoreConfigMessageInput[]
    modelParams?: ScoreConfigModelParamsInput
    choiceScores?: ScoreConfigChoiceScoreInput[]
    useCot?: boolean
    passThreshold?: number
    /** JSON DAG scorer definition (see eval_runner/dag_scorer.go), UTF-8 encoded. */
    dagDefinition?: Uint8Array
}

export async function updateScoreConfig(params: UpdateScoreConfigParams): Promise<UpdateScoreConfigResponse> {
    const categoriesObj: JsonObject | undefined = params.categories
        ? Object.fromEntries(params.categories.map((c, i) => [String(i), c]))
        : undefined
    const req: UpdateScoreConfigRequest = create(UpdateScoreConfigRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        minValue: params.minValue,
        maxValue: params.maxValue,
        categories: categoriesObj,
        evalPrompt: params.evalPrompt,
        scorerCode: params.scorerCode,
        scorerLanguage: params.scorerLanguage,
        useSandbox: params.useSandbox,
        dataType: params.dataType,
        slug: params.slug,
        scorerType: params.scorerType,
        outputType: params.outputType,
        messages: params.messages,
        modelParams: params.modelParams,
        choiceScores: params.choiceScores,
        useCot: params.useCot,
        passThreshold: params.passThreshold,
        dagDefinition: params.dagDefinition,
    })
    return datasetClient.updateScoreConfig(req)
}

export async function deleteScoreConfig(id: string, tenantId: string): Promise<DeleteScoreConfigResponse> {
    const req: DeleteScoreConfigRequest = create(DeleteScoreConfigRequestSchema, { tenantId, id })
    return datasetClient.deleteScoreConfig(req)
}

// ─── Built-in Metrics ────────────────────────────────────────────────

export async function listBuiltinMetrics(): Promise<ListBuiltinMetricsResponse> {
    const req = create(ListBuiltinMetricsRequestSchema, {})
    return datasetClient.listBuiltinMetrics(req)
}

export type { Dataset, DatasetItem, ScoreConfig, BuiltinMetric }
