import { useQuery } from '@tanstack/react-query'
import {
  getOutcomeDashboard,
  getOutcomeTimeSeries,
  type MetricsFilter,
} from '@/server/observability'

const OUTCOME_DASHBOARD_KEY = ['outcome-dashboard']
const OUTCOME_TIMESERIES_KEY = ['outcome-timeseries']

export type OutcomeFilterOptions = {
  startTime?: string
  endTime?: string
  agentId?: string
  interval?: string
  groupBy?: string[]
}

function toFilter(opts: OutcomeFilterOptions): MetricsFilter {
  return {
    startTime: opts.startTime ? new Date(opts.startTime) : undefined,
    endTime: opts.endTime ? new Date(opts.endTime) : undefined,
  }
}

export function useOutcomeDashboard(filters: OutcomeFilterOptions = {}) {
  return useQuery({
    queryKey: [...OUTCOME_DASHBOARD_KEY, filters],
    queryFn: () =>
      getOutcomeDashboard({
        filter: toFilter(filters),
        agentId: filters.agentId,
        groupBy: filters.groupBy,
      }),
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
}

export function useOutcomeTimeSeries(
  filters: OutcomeFilterOptions = {},
  scoreName: string,
  aggregation: string = 'avg',
) {
  return useQuery({
    queryKey: [...OUTCOME_TIMESERIES_KEY, filters, scoreName, aggregation],
    queryFn: () =>
      getOutcomeTimeSeries({
        filter: toFilter(filters),
        scoreName,
        aggregation,
        granularity: filters.interval ?? 'hour',
        agentId: filters.agentId,
      }),
    enabled: !!scoreName,
    refetchOnWindowFocus: false,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
}
