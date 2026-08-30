import { createServerTransport } from '@/server'
import { createClientFor, create, timestampFromDate } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { TracesService } from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import type {
  CustomObservation,
  Span,
  SpanTreeNode,
  Trace,
  TraceAnnotation,
  TraceOverlay,
} from '@everstack/proto/everstack/traces/v1/traces_pb'
import type {
  ListTracesRequest,
  Score,
  RichTrace,
  TraceLogRecord,
} from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import {
  GetTraceByIDRequestSchema,
  GetTraceRequestSchema,
  GetTraceTreeRequestSchema,
  GetTraceLogsRequestSchema,
  GetTraceScoresRequestSchema,
  CreateScoreRequestSchema,
  DeleteScoreRequestSchema,
  ListRichTracesRequestSchema,
  GetTraceOverlayRequestSchema,
  UpdateTraceOverlayRequestSchema,
  CreateCustomObservationRequestSchema,
  ListCustomObservationsRequestSchema,
  CreateTraceAnnotationRequestSchema,
  ListTraceAnnotationsRequestSchema,
  ListCustomColumnsRequestSchema,
  UpsertCustomColumnRequestSchema,
  DeleteCustomColumnRequestSchema,
  CustomColumnDefSchema,
  ListTraceViewsRequestSchema,
  UpsertTraceViewRequestSchema,
  DeleteTraceViewRequestSchema,
  TraceViewSchema,
  ListSemanticMappingsRequestSchema,
  AddSemanticMappingRequestSchema,
  DeleteSemanticMappingRequestSchema,
  ListClassificationRulesRequestSchema,
  AddClassificationRuleRequestSchema,
  DeleteClassificationRuleRequestSchema,
} from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import type { CustomColumnDef, TraceView, SemanticMapping, ClassificationRule } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})
const tracesClient = createClientFor(TracesService)(transport)

// Stream traces from the server-streaming ListTraces RPC.
export async function* streamTraces(
  req: ListTracesRequest,
  opts?: { signal?: AbortSignal },
): AsyncGenerator<Trace, void, unknown> {
  const stream = tracesClient.listTraces(req, { signal: opts?.signal })
  for await (const trace of stream) {
    if (trace) {
      yield trace
    }
  }
}

export async function getTraceByID(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<Span[]> {
  const req = create(GetTraceByIDRequestSchema, { traceId })
  const response = await tracesClient.getTraceByID(req, {
    signal: opts?.signal,
  })
  return response.spans
}

export async function getTrace(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<Trace | null> {
  const req = create(GetTraceRequestSchema, { traceId })
  const response = await tracesClient.getTrace(req, { signal: opts?.signal })
  return response.trace || null
}

export async function getTraceTree(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<SpanTreeNode | null> {
  const req = create(GetTraceTreeRequestSchema, { traceId })
  const response = await tracesClient.getTraceTree(req, {
    signal: opts?.signal,
  })
  return response.root || null
}

export async function getTraceLogs(
  traceId: string,
  sessionId?: string,
  opts?: { signal?: AbortSignal },
): Promise<TraceLogRecord[]> {
  const req = create(GetTraceLogsRequestSchema, {
    traceId,
    sessionId: sessionId ?? '',
  })
  const response = await tracesClient.getTraceLogs(req, {
    signal: opts?.signal,
  })
  return response.records
}

export async function getTraceScores(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<Score[]> {
  const req = create(GetTraceScoresRequestSchema, { traceId })
  const response = await tracesClient.getTraceScores(req, {
    signal: opts?.signal,
  })
  return response.scores
}

export async function createScore(params: {
  traceId: string
  name: string
  source: string
  dataType: string
  numericValue?: number
  stringValue?: string
  booleanValue?: boolean
  comment?: string
}): Promise<Score> {
  const req = create(CreateScoreRequestSchema, {
    traceId: params.traceId,
    name: params.name,
    source: params.source,
    dataType: params.dataType,
    numericValue: params.numericValue,
    stringValue: params.stringValue,
    booleanValue: params.booleanValue,
    comment: params.comment,
  })
  const response = await tracesClient.createScore(req)
  return response.score!
}

export async function deleteScore(
  scoreId: string,
  traceId: string,
): Promise<void> {
  const req = create(DeleteScoreRequestSchema, { scoreId, traceId })
  await tracesClient.deleteScore(req)
}

export async function getTraceOverlay(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<TraceOverlay | null> {
  const req = create(GetTraceOverlayRequestSchema, { traceId })
  const response = await tracesClient.getTraceOverlay(req, {
    signal: opts?.signal,
  })
  return response.overlay || null
}

export async function updateTraceOverlay(params: {
  traceId: string
  displayName?: string
  inputOverride?: string
  outputOverride?: string
  metadata?: Record<string, string>
  tags?: string[]
  hiddenSpanIds?: string[]
}): Promise<TraceOverlay> {
  const req = create(UpdateTraceOverlayRequestSchema, {
    traceId: params.traceId,
    displayName: params.displayName,
    inputOverride: params.inputOverride,
    outputOverride: params.outputOverride,
    metadata: params.metadata ?? {},
    tags: params.tags ?? [],
    hiddenSpanIds: params.hiddenSpanIds ?? [],
  })
  const response = await tracesClient.updateTraceOverlay(req)
  return response.overlay!
}

export async function createCustomObservation(params: {
  traceId: string
  parentObservationId?: string
  name: string
  type?: string
  source?: string
  startTime?: Date
  endTime?: Date
  duration?: bigint
  level?: string
  statusMessage?: string
  model?: string
  inputData?: string
  outputData?: string
  inputMimeType?: string
  outputMimeType?: string
  inputTokens?: bigint
  outputTokens?: bigint
  totalTokens?: bigint
  inputCost?: number
  outputCost?: number
  totalCost?: number
  metadata?: Record<string, string>
  tags?: string[]
}): Promise<CustomObservation> {
  const req = create(CreateCustomObservationRequestSchema, {
    traceId: params.traceId,
    parentObservationId: params.parentObservationId,
    name: params.name,
    type: params.type ?? 'SPAN',
    source: params.source ?? 'USER',
    startTime: params.startTime
      ? timestampFromDate(params.startTime)
      : undefined,
    endTime: params.endTime ? timestampFromDate(params.endTime) : undefined,
    duration: params.duration,
    level: params.level,
    statusMessage: params.statusMessage,
    model: params.model,
    inputData: params.inputData,
    outputData: params.outputData,
    inputMimeType: params.inputMimeType,
    outputMimeType: params.outputMimeType,
    inputTokens: params.inputTokens,
    outputTokens: params.outputTokens,
    totalTokens: params.totalTokens,
    inputCost: params.inputCost,
    outputCost: params.outputCost,
    totalCost: params.totalCost,
    metadata: params.metadata ?? {},
    tags: params.tags ?? [],
  })
  const response = await tracesClient.createCustomObservation(req)
  return response.observation!
}

export async function listCustomObservations(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<CustomObservation[]> {
  const req = create(ListCustomObservationsRequestSchema, { traceId })
  const response = await tracesClient.listCustomObservations(req, {
    signal: opts?.signal,
  })
  return response.observations
}

export async function createTraceAnnotation(params: {
  traceId: string
  observationId?: string
  body: string
  metadata?: Record<string, string>
}): Promise<TraceAnnotation> {
  const req = create(CreateTraceAnnotationRequestSchema, {
    traceId: params.traceId,
    observationId: params.observationId,
    body: params.body,
    metadata: params.metadata ?? {},
  })
  const response = await tracesClient.createTraceAnnotation(req)
  return response.annotation!
}

export async function listTraceAnnotations(
  traceId: string,
  opts?: { signal?: AbortSignal },
): Promise<TraceAnnotation[]> {
  const req = create(ListTraceAnnotationsRequestSchema, { traceId })
  const response = await tracesClient.listTraceAnnotations(req, {
    signal: opts?.signal,
  })
  return response.annotations
}

export type ListRichTracesParams = {
  sessionId?: string
  userId?: string
  threadId?: string
  environment?: string
  tags?: string[]
  model?: string
  provider?: string
  limit?: number
  offset?: number
}

export async function listRichTraces(
  params: ListRichTracesParams = {},
  opts?: { signal?: AbortSignal },
): Promise<{ traces: RichTrace[]; totalCount: number }> {
  const req = create(ListRichTracesRequestSchema, {
    sessionId: params.sessionId,
    userId: params.userId,
    threadId: params.threadId,
    environment: params.environment,
    tags: params.tags ?? [],
    model: params.model,
    provider: params.provider,
    limit: params.limit ?? 50,
    offset: params.offset ?? 0,
  })
  const response = await tracesClient.listRichTraces(req, {
    signal: opts?.signal,
  })
  return { traces: response.traces, totalCount: response.totalCount }
}

// ---------------------------------------------------------------------------
// Custom columns (user-defined trace columns)
// ---------------------------------------------------------------------------

export type CustomColumnInput = {
  key: string
  label: string
  valueType: string
  source: string
  sourceRef: string
  position?: number
}

export async function listCustomColumns(opts?: {
  signal?: AbortSignal
}): Promise<CustomColumnDef[]> {
  const req = create(ListCustomColumnsRequestSchema, {})
  const response = await tracesClient.listCustomColumns(req, {
    signal: opts?.signal,
  })
  return response.columns
}

export async function upsertCustomColumn(
  input: CustomColumnInput,
): Promise<CustomColumnDef | undefined> {
  const req = create(UpsertCustomColumnRequestSchema, {
    column: create(CustomColumnDefSchema, {
      key: input.key,
      label: input.label,
      valueType: input.valueType,
      source: input.source,
      sourceRef: input.sourceRef,
      position: input.position ?? 0,
    }),
  })
  const response = await tracesClient.upsertCustomColumn(req)
  return response.column
}

export async function deleteCustomColumn(key: string): Promise<void> {
  const req = create(DeleteCustomColumnRequestSchema, { key })
  await tracesClient.deleteCustomColumn(req)
}

// ---------------------------------------------------------------------------
// Saved views
// ---------------------------------------------------------------------------

export type TraceViewInput = {
  id?: string
  name: string
  configJson: string
}

export async function listTraceViews(opts?: {
  signal?: AbortSignal
}): Promise<TraceView[]> {
  const req = create(ListTraceViewsRequestSchema, {})
  const response = await tracesClient.listTraceViews(req, {
    signal: opts?.signal,
  })
  return response.views
}

export async function upsertTraceView(
  input: TraceViewInput,
): Promise<TraceView | undefined> {
  const req = create(UpsertTraceViewRequestSchema, {
    view: create(TraceViewSchema, {
      id: input.id ?? '',
      name: input.name,
      configJson: input.configJson,
    }),
  })
  const response = await tracesClient.upsertTraceView(req)
  return response.view
}

export async function deleteTraceView(id: string): Promise<void> {
  const req = create(DeleteTraceViewRequestSchema, { id })
  await tracesClient.deleteTraceView(req)
}

// ---------------------------------------------------------------------------
// Semantic mappings
// ---------------------------------------------------------------------------

export async function listSemanticMappings(opts?: {
  signal?: AbortSignal
}): Promise<SemanticMapping[]> {
  const req = create(ListSemanticMappingsRequestSchema, {})
  const response = await tracesClient.listSemanticMappings(req, {
    signal: opts?.signal,
  })
  return response.mappings
}

export async function addSemanticMapping(
  field: string,
  attrKey: string,
): Promise<void> {
  const req = create(AddSemanticMappingRequestSchema, { field, attrKey })
  await tracesClient.addSemanticMapping(req)
}

export async function deleteSemanticMapping(
  field: string,
  attrKey: string,
): Promise<void> {
  const req = create(DeleteSemanticMappingRequestSchema, { field, attrKey })
  await tracesClient.deleteSemanticMapping(req)
}

// ---------------------------------------------------------------------------
// Classification rules
// ---------------------------------------------------------------------------

export async function listClassificationRules(opts?: {
  signal?: AbortSignal
}): Promise<ClassificationRule[]> {
  const req = create(ListClassificationRulesRequestSchema, {})
  const response = await tracesClient.listClassificationRules(req, {
    signal: opts?.signal,
  })
  return response.rules
}

export async function addClassificationRule(
  pattern: string,
  kind: string,
): Promise<void> {
  const req = create(AddClassificationRuleRequestSchema, { pattern, kind })
  await tracesClient.addClassificationRule(req)
}

export async function deleteClassificationRule(
  pattern: string,
  kind: string,
): Promise<void> {
  const req = create(DeleteClassificationRuleRequestSchema, { pattern, kind })
  await tracesClient.deleteClassificationRule(req)
}
