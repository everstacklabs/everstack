import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { EvalService } from '@everstack/proto/everstack/datasets/v1/datasets_service_pb'
import type {
    CreateEvalRunRequest,
    CreateEvalRunResponse,
    GetEvalRunRequest,
    GetEvalRunResponse,
    ListEvalRunsRequest,
    ListEvalRunsResponse,
    CancelEvalRunRequest,
    CancelEvalRunResponse,
    DeleteEvalRunRequest,
    DeleteEvalRunResponse,
    RetryEvalRunRequest,
    RetryEvalRunResponse,
    GetEvalRunItemsRequest,
    GetEvalRunItemsResponse,
    GetEvalRunSummaryRequest,
    GetEvalRunSummaryResponse,
    CompareEvalRunsRequest,
    CompareEvalRunsResponse,
    ListComparisonRowsRequest,
    ListComparisonRowsResponse,
    SetBaselineRequest,
    SetBaselineResponse,
    CreateEvalScheduleRequest,
    CreateEvalScheduleResponse,
    GetEvalScheduleRequest,
    GetEvalScheduleResponse,
    ListEvalSchedulesRequest,
    ListEvalSchedulesResponse,
    UpdateEvalScheduleRequest,
    UpdateEvalScheduleResponse,
    DeleteEvalScheduleRequest,
    DeleteEvalScheduleResponse,
    CreateSamplingEvalRuleRequest,
    CreateSamplingEvalRuleResponse,
    ListSamplingEvalRulesRequest,
    ListSamplingEvalRulesResponse,
    UpdateSamplingEvalRuleRequest,
    UpdateSamplingEvalRuleResponse,
    DeleteSamplingEvalRuleRequest,
    DeleteSamplingEvalRuleResponse,
    RunSamplingEvalRuleNowRequest,
    RunSamplingEvalRuleNowResponse,
    EvalRun,
    EvalRunItem,
    EvalSchedule,
    SamplingEvalRule,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'
import {
    CreateEvalRunRequestSchema,
    GetEvalRunRequestSchema,
    ListEvalRunsRequestSchema,
    CancelEvalRunRequestSchema,
    DeleteEvalRunRequestSchema,
    RetryEvalRunRequestSchema,
    GetEvalRunItemsRequestSchema,
    GetEvalRunSummaryRequestSchema,
    CompareEvalRunsRequestSchema,
    ListComparisonRowsRequestSchema,
    SetBaselineRequestSchema,
    CreateEvalScheduleRequestSchema,
    GetEvalScheduleRequestSchema,
    ListEvalSchedulesRequestSchema,
    UpdateEvalScheduleRequestSchema,
    DeleteEvalScheduleRequestSchema,
    CreateSamplingEvalRuleRequestSchema,
    ListSamplingEvalRulesRequestSchema,
    UpdateSamplingEvalRuleRequestSchema,
    DeleteSamplingEvalRuleRequestSchema,
    RunSamplingEvalRuleNowRequestSchema,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'

type JsonObject = { [k: string]: JsonValue }
type JsonValue = number | string | boolean | null | JsonObject | JsonValue[]

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
const evalClient = createClientFor(EvalService)(transport)

// ─── Eval Run CRUD ───────────────────────────────────────────────────

export type CreateEvalRunParams = {
    tenantId: string
    name: string
    datasetId: string
    description?: string
    evalTargetType: string
    evalTargetId?: string
    evalConfig?: JsonObject
    scorerConfigIds: string[]
}

export async function createEvalRun(params: CreateEvalRunParams): Promise<CreateEvalRunResponse> {
    const req: CreateEvalRunRequest = create(CreateEvalRunRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        datasetId: params.datasetId,
        description: params.description,
        evalTargetType: params.evalTargetType,
        evalTargetId: params.evalTargetId,
        evalConfig: params.evalConfig,
        scorerConfigIds: params.scorerConfigIds,
    })
    return evalClient.createEvalRun(req)
}

export async function getEvalRun(id: string, tenantId: string): Promise<GetEvalRunResponse> {
    const req: GetEvalRunRequest = create(GetEvalRunRequestSchema, { tenantId, id })
    return evalClient.getEvalRun(req)
}

export type ListEvalRunsParams = {
    tenantId?: string
    datasetId?: string
    status?: string
    limit?: number
    offset?: number
}

export async function listEvalRuns(params: ListEvalRunsParams = {}): Promise<ListEvalRunsResponse> {
    const req: ListEvalRunsRequest = create(ListEvalRunsRequestSchema, {
        tenantId: params.tenantId ?? '',
        datasetId: params.datasetId,
        status: params.status,
        limit: params.limit,
        offset: params.offset,
    })
    return evalClient.listEvalRuns(req)
}

export async function cancelEvalRun(id: string, tenantId: string): Promise<CancelEvalRunResponse> {
    const req: CancelEvalRunRequest = create(CancelEvalRunRequestSchema, { tenantId, id })
    return evalClient.cancelEvalRun(req)
}

export async function deleteEvalRun(id: string, tenantId: string): Promise<DeleteEvalRunResponse> {
    const req: DeleteEvalRunRequest = create(DeleteEvalRunRequestSchema, { tenantId, id })
    return evalClient.deleteEvalRun(req)
}

export async function retryEvalRun(id: string, tenantId: string, retryAll: boolean): Promise<RetryEvalRunResponse> {
    const req: RetryEvalRunRequest = create(RetryEvalRunRequestSchema, { tenantId, id, retryAll })
    return evalClient.retryEvalRun(req)
}

// ─── Eval Run Items & Summary ────────────────────────────────────────

export type GetEvalRunItemsParams = {
    tenantId?: string
    runId: string
    limit?: number
    offset?: number
}

export async function getEvalRunItems(params: GetEvalRunItemsParams): Promise<GetEvalRunItemsResponse> {
    const req: GetEvalRunItemsRequest = create(GetEvalRunItemsRequestSchema, {
        tenantId: params.tenantId ?? '',
        evalRunId: params.runId,
        limit: params.limit,
        offset: params.offset,
    })
    return evalClient.getEvalRunItems(req)
}

export async function getEvalRunSummary(runId: string, tenantId: string): Promise<GetEvalRunSummaryResponse> {
    const req: GetEvalRunSummaryRequest = create(GetEvalRunSummaryRequestSchema, { tenantId, id: runId })
    return evalClient.getEvalRunSummary(req)
}

export type CompareEvalRunsParams = {
    tenantId?: string
    runIds: string[]
    /**
     * When true, the engine materializes the comparison (both runs must be
     * terminal) and the response carries `comparisonId` for ListComparisonRows.
     */
    persist?: boolean
}

export async function compareEvalRuns(params: CompareEvalRunsParams): Promise<CompareEvalRunsResponse> {
    const req: CompareEvalRunsRequest = create(CompareEvalRunsRequestSchema, {
        tenantId: params.tenantId ?? '',
        evalRunIds: params.runIds,
        persist: params.persist ?? false,
    })
    return evalClient.compareEvalRuns(req)
}

export type ListComparisonRowsParams = {
    tenantId: string
    comparisonId: string
    limit?: number
    offset?: number
    onlyRegressions?: boolean
}

export async function listComparisonRows(params: ListComparisonRowsParams): Promise<ListComparisonRowsResponse> {
    const req: ListComparisonRowsRequest = create(ListComparisonRowsRequestSchema, {
        tenantId: params.tenantId,
        comparisonId: params.comparisonId,
        limit: params.limit ?? 50,
        offset: params.offset ?? 0,
        onlyRegressions: params.onlyRegressions ?? false,
    })
    return evalClient.listComparisonRows(req)
}

// ─── Baseline ────────────────────────────────────────────────────────

export async function setBaseline(evalRunId: string, tenantId: string): Promise<SetBaselineResponse> {
    const req: SetBaselineRequest = create(SetBaselineRequestSchema, { tenantId, evalRunId })
    return evalClient.setBaseline(req)
}

// ─── Eval Schedules ──────────────────────────────────────────────────

export type CreateEvalScheduleParams = {
    tenantId: string
    name: string
    datasetId: string
    description?: string
    evalTargetType: string
    evalTargetId?: string
    evalConfig?: JsonObject
    scorerConfigIds: string[]
    cronExpression: string
    timezone?: string
}

export async function createEvalSchedule(params: CreateEvalScheduleParams): Promise<CreateEvalScheduleResponse> {
    const req: CreateEvalScheduleRequest = create(CreateEvalScheduleRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        datasetId: params.datasetId,
        description: params.description,
        evalTargetType: params.evalTargetType,
        evalTargetId: params.evalTargetId,
        evalConfig: params.evalConfig,
        scorerConfigIds: params.scorerConfigIds,
        cronExpression: params.cronExpression,
        timezone: params.timezone,
    })
    return evalClient.createEvalSchedule(req)
}

export async function getEvalSchedule(id: string, tenantId: string): Promise<GetEvalScheduleResponse> {
    const req: GetEvalScheduleRequest = create(GetEvalScheduleRequestSchema, { tenantId, id })
    return evalClient.getEvalSchedule(req)
}

export type ListEvalSchedulesParams = {
    tenantId?: string
    datasetId?: string
    limit?: number
    offset?: number
}

export async function listEvalSchedules(params: ListEvalSchedulesParams = {}): Promise<ListEvalSchedulesResponse> {
    const req: ListEvalSchedulesRequest = create(ListEvalSchedulesRequestSchema, {
        tenantId: params.tenantId ?? '',
        datasetId: params.datasetId,
        limit: params.limit,
        offset: params.offset,
    })
    return evalClient.listEvalSchedules(req)
}

export type UpdateEvalScheduleParams = {
    id: string
    tenantId: string
    name?: string
    description?: string
    cronExpression?: string
    timezone?: string
    enabled?: boolean
    scorerConfigIds?: string[]
}

export async function updateEvalSchedule(params: UpdateEvalScheduleParams): Promise<UpdateEvalScheduleResponse> {
    const req: UpdateEvalScheduleRequest = create(UpdateEvalScheduleRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        cronExpression: params.cronExpression,
        timezone: params.timezone,
        enabled: params.enabled,
        scorerConfigIds: params.scorerConfigIds,
    })
    return evalClient.updateEvalSchedule(req)
}

export async function deleteEvalSchedule(id: string, tenantId: string): Promise<DeleteEvalScheduleResponse> {
    const req: DeleteEvalScheduleRequest = create(DeleteEvalScheduleRequestSchema, { tenantId, id })
    return evalClient.deleteEvalSchedule(req)
}

// ─── Sampling / Online Eval Rules ────────────────────────────────────

export type CreateSamplingEvalRuleParams = {
    tenantId: string
    name: string
    description?: string
    filterPredicate?: JsonObject
    sampleRate?: number
    scorerConfigIds: string[]
    lookbackSeconds?: number
    intervalSeconds?: number
    enabled?: boolean
}

export async function createSamplingEvalRule(
    params: CreateSamplingEvalRuleParams,
): Promise<CreateSamplingEvalRuleResponse> {
    const req: CreateSamplingEvalRuleRequest = create(CreateSamplingEvalRuleRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        description: params.description,
        filterPredicate: params.filterPredicate,
        sampleRate: params.sampleRate,
        scorerConfigIds: params.scorerConfigIds,
        lookbackSeconds: params.lookbackSeconds,
        intervalSeconds: params.intervalSeconds,
        enabled: params.enabled,
    })
    return evalClient.createSamplingEvalRule(req)
}

export type ListSamplingEvalRulesParams = {
    tenantId?: string
    enabledOnly?: boolean
    limit?: number
    offset?: number
}

export async function listSamplingEvalRules(
    params: ListSamplingEvalRulesParams = {},
): Promise<ListSamplingEvalRulesResponse> {
    const req: ListSamplingEvalRulesRequest = create(ListSamplingEvalRulesRequestSchema, {
        tenantId: params.tenantId ?? '',
        enabledOnly: params.enabledOnly,
        limit: params.limit,
        offset: params.offset,
    })
    return evalClient.listSamplingEvalRules(req)
}

export type UpdateSamplingEvalRuleParams = {
    id: string
    tenantId: string
    name?: string
    description?: string
    filterPredicate?: JsonObject
    sampleRate?: number
    scorerConfigIds?: string[]
    lookbackSeconds?: number
    intervalSeconds?: number
    enabled?: boolean
}

export async function updateSamplingEvalRule(
    params: UpdateSamplingEvalRuleParams,
): Promise<UpdateSamplingEvalRuleResponse> {
    const req: UpdateSamplingEvalRuleRequest = create(UpdateSamplingEvalRuleRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        filterPredicate: params.filterPredicate,
        sampleRate: params.sampleRate,
        scorerConfigIds: params.scorerConfigIds ?? [],
        lookbackSeconds: params.lookbackSeconds,
        intervalSeconds: params.intervalSeconds,
        enabled: params.enabled,
    })
    return evalClient.updateSamplingEvalRule(req)
}

export async function deleteSamplingEvalRule(
    id: string,
    tenantId: string,
): Promise<DeleteSamplingEvalRuleResponse> {
    const req: DeleteSamplingEvalRuleRequest = create(DeleteSamplingEvalRuleRequestSchema, { tenantId, id })
    return evalClient.deleteSamplingEvalRule(req)
}

export async function runSamplingEvalRuleNow(
    id: string,
    tenantId: string,
): Promise<RunSamplingEvalRuleNowResponse> {
    const req: RunSamplingEvalRuleNowRequest = create(RunSamplingEvalRuleNowRequestSchema, { tenantId, id })
    return evalClient.runSamplingEvalRuleNow(req)
}

export type { EvalRun, EvalRunItem, EvalSchedule, SamplingEvalRule }
