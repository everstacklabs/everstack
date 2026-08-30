import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import {
  getMetricsDashboard,
  getMetricsTimeSeries,
  getMetricsBreakdown,
  type MetricsFilter,
} from '@/server/observability'
import type {
  GetMetricsBreakdownResponse,
  GetMetricsDashboardResponse,
  GetMetricsTimeSeriesResponse,
} from '@everstack/proto/everstack/traces/v1/observability_pb'

const METRICS_DASHBOARD_KEY = ['metrics-dashboard']
const METRICS_TIMESERIES_KEY = ['metrics-timeseries']
const METRICS_BREAKDOWN_KEY = ['metrics-breakdown']

export type MetricsFilterOptions = {
  startTime?: string
  endTime?: string
  model?: string
  provider?: string
  environment?: string
  interval?: string
  compare?: boolean
}

function toFilter(opts: MetricsFilterOptions): MetricsFilter {
  return {
    startTime: opts.startTime ? new Date(opts.startTime) : undefined,
    endTime: opts.endTime ? new Date(opts.endTime) : undefined,
    models: opts.model ? [opts.model] : undefined,
    providers: opts.provider ? [opts.provider] : undefined,
    environments: opts.environment ? [opts.environment] : undefined,
  }
}

export function useMetricsDashboard(
  filters: MetricsFilterOptions = {},
): UseQueryResult<GetMetricsDashboardResponse, Error> {
  return useQuery({
    queryKey: [...METRICS_DASHBOARD_KEY, filters],
    queryFn: () =>
      getMetricsDashboard(toFilter(filters), filters.compare ?? false),
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: filters.compare ? 120_000 : 60_000,
  })
}

const ALL_METRICS = [
  'request_count',
  'error_count',
  'avg_latency_ms',
  'total_cost',
  'total_tokens',
  'error_rate',
  'input_tokens',
  'output_tokens',
] as const

export function useMetricsTimeSeries(
  filters: MetricsFilterOptions = {},
): UseQueryResult<GetMetricsTimeSeriesResponse, Error> {
  return useQuery({
    queryKey: [...METRICS_TIMESERIES_KEY, filters],
    queryFn: async () => {
      const filter = toFilter(filters)
      const granularity = filters.interval ?? 'hour'
      const results = await Promise.allSettled(
        ALL_METRICS.map((metric) =>
          getMetricsTimeSeries({
            filter,
            granularity,
            metric,
            compare: filters.compare ?? false,
          }),
        ),
      )
      // Merge all fulfilled series into a single response (skip failed metrics)
      const series = results
        .filter(
          (r): r is PromiseFulfilledResult<GetMetricsTimeSeriesResponse> =>
            r.status === 'fulfilled',
        )
        .flatMap((r) => r.value.series ?? [])
      const previousSeries = results
        .filter(
          (r): r is PromiseFulfilledResult<GetMetricsTimeSeriesResponse> =>
            r.status === 'fulfilled',
        )
        .flatMap((r) => r.value.previousSeries ?? [])
      return { series, previousSeries } as GetMetricsTimeSeriesResponse
    },
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: filters.compare ? 120_000 : 60_000,
  })
}

export function useMetricsGroupedTimeSeries(
  filters: MetricsFilterOptions = {},
  metric: string,
  groupBy: 'model' | 'provider',
): UseQueryResult<GetMetricsTimeSeriesResponse, Error> {
  return useQuery({
    queryKey: [...METRICS_TIMESERIES_KEY, 'grouped', metric, groupBy, filters],
    queryFn: () =>
      getMetricsTimeSeries({
        filter: toFilter(filters),
        granularity: filters.interval ?? 'hour',
        metric,
        groupBy,
        compare: filters.compare ?? false,
      }),
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: filters.compare ? 120_000 : 60_000,
  })
}

export function useTtftTimeSeries(
  filters: MetricsFilterOptions = {},
): UseQueryResult<GetMetricsTimeSeriesResponse, Error> {
  return useQuery({
    queryKey: [...METRICS_TIMESERIES_KEY, 'ttft', filters],
    queryFn: async () => {
      const filter = toFilter(filters)
      const granularity = filters.interval ?? 'hour'
      const results = await Promise.allSettled(
        (['ttft_p50', 'ttft_p95'] as const).map((metric) =>
          getMetricsTimeSeries({
            filter,
            granularity,
            metric,
            compare: filters.compare ?? false,
          }),
        ),
      )
      const series = results
        .filter(
          (r): r is PromiseFulfilledResult<GetMetricsTimeSeriesResponse> =>
            r.status === 'fulfilled',
        )
        .flatMap((r) => r.value.series ?? [])
      const previousSeries = results
        .filter(
          (r): r is PromiseFulfilledResult<GetMetricsTimeSeriesResponse> =>
            r.status === 'fulfilled',
        )
        .flatMap((r) => r.value.previousSeries ?? [])
      return { series, previousSeries } as GetMetricsTimeSeriesResponse
    },
    refetchOnWindowFocus: false,
    staleTime: 60_000,
    refetchInterval: filters.compare ? 120_000 : 60_000,
  })
}

export function useMetricsBreakdown(
  filters: MetricsFilterOptions = {},
  metric: 'requests' | 'errors' | 'cost' | 'tokens',
  groupBy:
    | 'model'
    | 'provider'
    | 'environment'
    | 'trace_name'
    | 'session'
    | 'user'
    | 'provider_api_key_id',
  limit = 5,
): UseQueryResult<GetMetricsBreakdownResponse, Error> {
  return useQuery({
    queryKey: [...METRICS_BREAKDOWN_KEY, metric, groupBy, limit, filters],
    queryFn: () =>
      getMetricsBreakdown({
        filter: toFilter(filters),
        metric,
        groupBy,
        limit,
        compare: filters.compare ?? false,
      }),
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: filters.compare ? 120_000 : 60_000,
  })
}

export function useEnvironmentOptions(
  filters: MetricsFilterOptions = {},
): UseQueryResult<GetMetricsBreakdownResponse, Error> {
  return useQuery({
    queryKey: [
      ...METRICS_BREAKDOWN_KEY,
      'environment-options',
      filters.startTime,
      filters.endTime,
      filters.model,
      filters.provider,
    ],
    queryFn: () =>
      getMetricsBreakdown({
        filter: toFilter({ ...filters, environment: undefined }),
        metric: 'requests',
        groupBy: 'environment',
        limit: 50,
        compare: false,
      }),
    refetchOnWindowFocus: false,
    staleTime: 300_000,
    refetchInterval: 300_000,
  })
}

export function useMetricsDimensionOptions(
  filters: MetricsFilterOptions = {},
  groupBy: 'model' | 'provider',
): UseQueryResult<GetMetricsTimeSeriesResponse, Error> {
  return useQuery({
    queryKey: [
      ...METRICS_TIMESERIES_KEY,
      'dimension-options',
      groupBy,
      filters,
    ],
    queryFn: () =>
      getMetricsTimeSeries({
        filter: toFilter(filters),
        granularity: filters.interval ?? 'hour',
        groupBy,
        metric: 'request_count',
      }),
    refetchOnWindowFocus: false,
    staleTime: 300_000,
    refetchInterval: 300_000,
  })
}
