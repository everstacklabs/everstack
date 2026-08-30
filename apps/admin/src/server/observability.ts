import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ObservabilityService } from '@everstack/proto/everstack/traces/v1/observability_service_pb'
import type {
  GetMetricsDashboardRequest,
  GetMetricsDashboardResponse,
  GetMetricsTimeSeriesRequest,
  GetMetricsTimeSeriesResponse,
  ListTraceSessionsRequest,
  ListTraceSessionsResponse,
  GetTraceSessionRequest,
  GetTraceSessionResponse,
  ListTraceUsersRequest,
  ListTraceUsersResponse,
  GetTraceUserRequest,
  GetTraceUserResponse,
  GetMetricsBreakdownResponse,
  MetricsDashboard,
  MetricsTimeSeries,
  TraceSession,
  TraceUser,
} from '@everstack/proto/everstack/traces/v1/observability_pb'
import {
  GetMetricsDashboardRequestSchema,
  GetMetricsTimeSeriesRequestSchema,
  GetMetricsBreakdownRequestSchema,
  ListTraceSessionsRequestSchema,
  GetTraceSessionRequestSchema,
  ListTraceUsersRequestSchema,
  GetTraceUserRequestSchema,
  GetOutcomeDashboardRequestSchema,
  GetOutcomeTimeSeriesRequestSchema,
} from '@everstack/proto/everstack/traces/v1/observability_pb'
function timestampFromDate(date: Date) {
  const ms = date.getTime()
  return {
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1_000_000,
  }
}

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})
const observabilityClient = createClientFor(ObservabilityService)(transport)

// ─── Metrics Dashboard ──────────────────────────────────────────────

export type MetricsFilter = {
  startTime?: Date
  endTime?: Date
  models?: string[]
  providers?: string[]
  environments?: string[]
}

export async function getMetricsDashboard(
  filter: MetricsFilter,
  compare = false,
): Promise<GetMetricsDashboardResponse> {
  const req: GetMetricsDashboardRequest = create(
    GetMetricsDashboardRequestSchema,
    {
      filter: {
        startTime: filter.startTime
          ? timestampFromDate(filter.startTime)
          : undefined,
        endTime: filter.endTime ? timestampFromDate(filter.endTime) : undefined,
        models: filter.models ?? [],
        providers: filter.providers ?? [],
        environments: filter.environments ?? [],
      },
      compare,
    },
  )
  return observabilityClient.getMetricsDashboard(req)
}

// ─── Metrics Time Series ────────────────────────────────────────────

export type TimeSeriesParams = {
  filter: MetricsFilter
  metric?: string
  groupBy?: string
  granularity?: string
  compare?: boolean
}

export async function getMetricsTimeSeries(
  params: TimeSeriesParams,
): Promise<GetMetricsTimeSeriesResponse> {
  const req: GetMetricsTimeSeriesRequest = create(
    GetMetricsTimeSeriesRequestSchema,
    {
      filter: {
        startTime: params.filter.startTime
          ? timestampFromDate(params.filter.startTime)
          : undefined,
        endTime: params.filter.endTime
          ? timestampFromDate(params.filter.endTime)
          : undefined,
        models: params.filter.models ?? [],
        providers: params.filter.providers ?? [],
        environments: params.filter.environments ?? [],
      },
      metric: params.metric ?? 'request_count',
      groupBy: params.groupBy ?? '',
      granularity: params.granularity ?? 'hour',
      compare: params.compare ?? false,
    },
  )
  return observabilityClient.getMetricsTimeSeries(req)
}

// ─── Metrics Breakdown ─────────────────────────────────────────────

export type MetricsBreakdownParams = {
  filter: MetricsFilter
  metric: string
  groupBy: string
  limit?: number
  compare?: boolean
}

export async function getMetricsBreakdown(
  params: MetricsBreakdownParams,
): Promise<GetMetricsBreakdownResponse> {
  const req = create(GetMetricsBreakdownRequestSchema, {
    filter: {
      startTime: params.filter.startTime
        ? timestampFromDate(params.filter.startTime)
        : undefined,
      endTime: params.filter.endTime
        ? timestampFromDate(params.filter.endTime)
        : undefined,
      models: params.filter.models ?? [],
      providers: params.filter.providers ?? [],
      environments: params.filter.environments ?? [],
    },
    metric: params.metric,
    groupBy: params.groupBy,
    limit: params.limit ?? 5,
    compare: params.compare ?? false,
  })
  return observabilityClient.getMetricsBreakdown(req)
}

// ─── Sessions ───────────────────────────────────────────────────────

export type ListSessionsParams = {
  userId?: string
  search?: string
  limit?: number
  offset?: number
  orderBy?: string
  startTime?: Date
  endTime?: Date
}

export async function listTraceSessions(
  params: ListSessionsParams = {},
): Promise<ListTraceSessionsResponse> {
  const req: ListTraceSessionsRequest = create(ListTraceSessionsRequestSchema, {
    filter: {
      startTime: params.startTime
        ? timestampFromDate(params.startTime)
        : undefined,
      endTime: params.endTime ? timestampFromDate(params.endTime) : undefined,
    },
    userId: params.userId,
    search: params.search,
    limit: params.limit ?? 50,
    offset: params.offset ?? 0,
    orderBy: params.orderBy ?? '',
  })
  return observabilityClient.listTraceSessions(req)
}

export async function getTraceSession(
  sessionId: string,
): Promise<GetTraceSessionResponse> {
  const req: GetTraceSessionRequest = create(GetTraceSessionRequestSchema, {
    sessionId,
  })
  return observabilityClient.getTraceSession(req)
}

// ─── Users ──────────────────────────────────────────────────────────

export type ListUsersParams = {
  search?: string
  limit?: number
  offset?: number
  orderBy?: string
  startTime?: Date
  endTime?: Date
}

export async function listTraceUsers(
  params: ListUsersParams = {},
): Promise<ListTraceUsersResponse> {
  const req: ListTraceUsersRequest = create(ListTraceUsersRequestSchema, {
    filter: {
      startTime: params.startTime
        ? timestampFromDate(params.startTime)
        : undefined,
      endTime: params.endTime ? timestampFromDate(params.endTime) : undefined,
    },
    search: params.search,
    limit: params.limit ?? 50,
    offset: params.offset ?? 0,
    orderBy: params.orderBy ?? '',
  })
  return observabilityClient.listTraceUsers(req)
}

export async function getTraceUser(
  userId: string,
): Promise<GetTraceUserResponse> {
  const req: GetTraceUserRequest = create(GetTraceUserRequestSchema, { userId })
  return observabilityClient.getTraceUser(req)
}

// ─── Outcome Dashboard ───────────────────────────────────────────────

export type OutcomeDashboardParams = {
  filter: MetricsFilter
  agentId?: string
  groupBy?: string[]
}

export async function getOutcomeDashboard(params: OutcomeDashboardParams) {
  const req = create(GetOutcomeDashboardRequestSchema, {
    filter: {
      startTime: params.filter.startTime
        ? timestampFromDate(params.filter.startTime)
        : undefined,
      endTime: params.filter.endTime
        ? timestampFromDate(params.filter.endTime)
        : undefined,
    },
    agentId: params.agentId ?? '',
    groupBy: params.groupBy ?? [],
  })
  return observabilityClient.getOutcomeDashboard(req)
}

export type OutcomeTimeSeriesParams = {
  filter: MetricsFilter
  scoreName: string
  aggregation?: string
  granularity?: string
  agentId?: string
}

export async function getOutcomeTimeSeries(params: OutcomeTimeSeriesParams) {
  const req = create(GetOutcomeTimeSeriesRequestSchema, {
    filter: {
      startTime: params.filter.startTime
        ? timestampFromDate(params.filter.startTime)
        : undefined,
      endTime: params.filter.endTime
        ? timestampFromDate(params.filter.endTime)
        : undefined,
    },
    scoreName: params.scoreName,
    aggregation: params.aggregation ?? 'avg',
    granularity: params.granularity ?? 'hour',
    agentId: params.agentId ?? '',
  })
  return observabilityClient.getOutcomeTimeSeries(req)
}

export type { MetricsDashboard, MetricsTimeSeries, TraceSession, TraceUser }
