import { useMemo } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import type {
  GetMetricsBreakdownResponse,
  GetMetricsDashboardResponse,
  GetMetricsTimeSeriesResponse,
} from '@everstack/proto/everstack/traces/v1/observability_pb'
import { KpiChartCard } from './kpi-chart-card'
import { TopNCard } from './top-n-card'
import {
  boardColors,
  formatCompactNumber,
  formatLatency,
  formatPercentRatio,
  formatUsd,
  seriesToPoints,
  windowMsFromGranularity,
} from './format'

const { TooltipProvider, useChartMode } = ui

// One-line explanation per panel, shown via an info tooltip on the title.
const BOARD_INFO: Record<BoardGraphKey, string> = {
  requests:
    'Total gateway requests (chat and embeddings calls) in the selected window.',
  errors: 'Requests that returned an error status in the window.',
  errorRate:
    'Share of requests that errored, as errors divided by total requests.',
  cost: 'Estimated total spend across all model calls in the window.',
  modelsByErrors: 'Models ranked by number of errored requests.',
  modelsByRequests: 'Models ranked by number of requests.',
  tracesByRequests: 'Root span names (traces) ranked by request volume.',
  sessionsByCost: 'Sessions ranked by estimated spend in the window.',
  usersByCost: 'Users ranked by estimated spend in the window.',
  providerKeysByCost:
    'Upstream provider API keys ranked by estimated spend in the window.',
  ttftP50: 'Median time to first token for streaming responses.',
  ttftP95: '95th percentile time to first token for streaming responses.',
}

export type MetricsBoardFilters = {
  startTime?: string
  endTime?: string
  environment?: string
  interval?: string
}

export type BoardGraphKey =
  | 'requests'
  | 'errors'
  | 'errorRate'
  | 'cost'
  | 'modelsByErrors'
  | 'modelsByRequests'
  | 'tracesByRequests'
  | 'sessionsByCost'
  | 'usersByCost'
  | 'providerKeysByCost'
  | 'ttftP50'
  | 'ttftP95'

export const BOARD_GRAPHS: Array<{ key: BoardGraphKey; label: string }> = [
  { key: 'requests', label: 'Requests' },
  { key: 'errors', label: 'Errors' },
  { key: 'errorRate', label: 'Error rate' },
  { key: 'cost', label: 'Cost' },
  { key: 'modelsByErrors', label: 'Models by errors' },
  { key: 'modelsByRequests', label: 'Models by requests' },
  { key: 'tracesByRequests', label: 'Traces by requests' },
  { key: 'sessionsByCost', label: 'Sessions by cost' },
  { key: 'usersByCost', label: 'Users by cost' },
  { key: 'providerKeysByCost', label: 'Provider keys by cost' },
  { key: 'ttftP50', label: 'TTFT p50' },
  { key: 'ttftP95', label: 'TTFT p95' },
]

export function MetricsBoard({
  dashboardResp,
  timeseriesResp,
  ttftResp,
  topModelsByErrors,
  topModelsByRequests,
  topTracesByRequests,
  topSessionsByCost,
  topUsersByCost,
  topProviderKeysByCost,
  providerKeyNames,
  filters,
  compare,
  visibleGraphs,
}: {
  dashboardResp?: GetMetricsDashboardResponse
  timeseriesResp?: GetMetricsTimeSeriesResponse
  ttftResp?: GetMetricsTimeSeriesResponse
  topModelsByErrors?: GetMetricsBreakdownResponse
  topModelsByRequests?: GetMetricsBreakdownResponse
  topTracesByRequests?: GetMetricsBreakdownResponse
  topSessionsByCost?: GetMetricsBreakdownResponse
  topUsersByCost?: GetMetricsBreakdownResponse
  topProviderKeysByCost?: GetMetricsBreakdownResponse
  // Maps upstream provider-key id -> { name, provider } for display.
  providerKeyNames?: Record<string, { name: string; provider: string }>
  filters: MetricsBoardFilters
  compare: boolean
  visibleGraphs: Record<BoardGraphKey, boolean>
}) {
  const navigate = useNavigate()
  const mode = useChartMode()
  const colors = boardColors(mode)
  const dashboard = dashboardResp?.dashboard
  const deltas = dashboardResp?.deltas
  const timeFmt = filters.interval === 'day' ? 'MMM D' : 'MMM D HH:mm'
  const previousStart = useMemo(() => {
    if (!filters.startTime || !filters.endTime) return undefined
    const start = new Date(filters.startTime).getTime()
    const end = new Date(filters.endTime).getTime()
    return new Date(start - (end - start)).toISOString()
  }, [filters.startTime, filters.endTime])

  const series = timeseriesResp?.series ?? []
  const previousSeries = timeseriesResp?.previousSeries ?? []
  const ttftSeries = ttftResp?.series ?? []
  const previousTtftSeries = ttftResp?.previousSeries ?? []

  const requestData = seriesToPoints(series, 'request_count')
  const previousRequestData = seriesToPoints(previousSeries, 'request_count')
  const errorData = seriesToPoints(series, 'error_count')
  const previousErrorData = seriesToPoints(previousSeries, 'error_count')
  const errorRateData = seriesToPoints(series, 'error_rate')
  const previousErrorRateData = seriesToPoints(previousSeries, 'error_rate')
  const costData = seriesToPoints(series, 'total_cost')
  const previousCostData = seriesToPoints(previousSeries, 'total_cost')
  const ttftP50Data = seriesToPoints(ttftSeries, 'ttft_p50')
  const previousTtftP50Data = seriesToPoints(previousTtftSeries, 'ttft_p50')
  const ttftP95Data = seriesToPoints(ttftSeries, 'ttft_p95')
  const previousTtftP95Data = seriesToPoints(previousTtftSeries, 'ttft_p95')

  const openTraces = (params: Record<string, string | undefined>) => {
    navigate({
      to: '/observability/traces',
      search: {
        live: 'false',
        range: 'custom',
        from: filters.startTime,
        to: filters.endTime,
        environment: filters.environment,
        ...params,
      },
    })
  }

  const openErrorBucket = (timestamp: number) => {
    const bucketMs = windowMsFromGranularity(filters.interval ?? 'hour')
    openTraces({
      statusCode: 'ERROR',
      from: new Date(timestamp).toISOString(),
      to: new Date(timestamp + bucketMs).toISOString(),
    })
  }

  return (
    <TooltipProvider>
      <div className="p-3 space-y-3">
        {visibleGraphs.requests && (
          <KpiChartCard
            title="Requests"
            info={BOARD_INFO.requests}
            value={formatCompactNumber(Number(dashboard?.totalRequests ?? 0))}
            delta={compare ? deltas?.requests : undefined}
            data={requestData}
            previousData={previousRequestData}
            color={colors.requests}
            large
            height={252}
            timeFmt={timeFmt}
            currentStart={filters.startTime}
            previousStart={previousStart}
            yFormatter={formatCompactNumber}
            tooltipFormatter={formatCompactNumber}
          />
        )}

        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {visibleGraphs.errors && (
            <KpiChartCard
              title="Errors"
              info={BOARD_INFO.errors}
              value={formatCompactNumber(Number(dashboard?.totalErrors ?? 0))}
              delta={compare ? deltas?.errors : undefined}
              data={errorData}
              previousData={previousErrorData}
              color={colors.errors}
              timeFmt={timeFmt}
              currentStart={filters.startTime}
              previousStart={previousStart}
              yFormatter={formatCompactNumber}
              tooltipFormatter={formatCompactNumber}
              onPointClick={openErrorBucket}
            />
          )}
          {visibleGraphs.errorRate && (
            <KpiChartCard
              title="Error rate"
              info={BOARD_INFO.errorRate}
              value={formatPercentRatio(dashboard?.errorRate ?? 0)}
              delta={compare ? deltas?.errorRate : undefined}
              data={errorRateData}
              previousData={previousErrorRateData}
              color={colors.errorRate}
              timeFmt={timeFmt}
              currentStart={filters.startTime}
              previousStart={previousStart}
              yFormatter={formatPercentRatio}
              tooltipFormatter={formatPercentRatio}
              onPointClick={openErrorBucket}
            />
          )}
          {visibleGraphs.cost && (
            <KpiChartCard
              title="Cost"
              info={BOARD_INFO.cost}
              value={formatUsd(dashboard?.totalCost ?? 0)}
              delta={compare ? deltas?.cost : undefined}
              data={costData}
              previousData={previousCostData}
              color={colors.cost}
              timeFmt={timeFmt}
              currentStart={filters.startTime}
              previousStart={previousStart}
              yFormatter={formatUsd}
              tooltipFormatter={formatUsd}
            />
          )}
          {visibleGraphs.modelsByErrors && (
            <TopNCard
              title="Top models by errors"
              info={BOARD_INFO.modelsByErrors}
              rows={topModelsByErrors?.rows ?? []}
              totalGroups={topModelsByErrors?.totalGroups}
              compare={compare}
              valueFormatter={formatCompactNumber}
              onRowClick={(key) =>
                openTraces({ query: key, statusCode: 'ERROR' })
              }
            />
          )}
          {visibleGraphs.modelsByRequests && (
            <TopNCard
              title="Top models by requests"
              info={BOARD_INFO.modelsByRequests}
              rows={topModelsByRequests?.rows ?? []}
              totalGroups={topModelsByRequests?.totalGroups}
              compare={compare}
              valueFormatter={formatCompactNumber}
              onRowClick={(key) => openTraces({ query: key })}
            />
          )}
          {visibleGraphs.tracesByRequests && (
            <TopNCard
              title="Top traces by requests"
              info={BOARD_INFO.tracesByRequests}
              rows={topTracesByRequests?.rows ?? []}
              totalGroups={topTracesByRequests?.totalGroups}
              compare={compare}
              valueFormatter={formatCompactNumber}
              onRowClick={(key) => openTraces({ query: key })}
            />
          )}
          {visibleGraphs.sessionsByCost && (
            <TopNCard
              title="Top sessions by cost"
              info={BOARD_INFO.sessionsByCost}
              rows={topSessionsByCost?.rows ?? []}
              totalGroups={topSessionsByCost?.totalGroups}
              compare={compare}
              valueFormatter={formatUsd}
              onRowClick={(key) => openTraces({ sessionId: key })}
            />
          )}
          {visibleGraphs.usersByCost && (
            <TopNCard
              title="Top users by cost"
              info={BOARD_INFO.usersByCost}
              rows={topUsersByCost?.rows ?? []}
              totalGroups={topUsersByCost?.totalGroups}
              compare={compare}
              valueFormatter={formatUsd}
              onRowClick={(key) => openTraces({ userId: key })}
            />
          )}
          {visibleGraphs.providerKeysByCost && (
            <TopNCard
              title="Top provider keys by cost"
              info={BOARD_INFO.providerKeysByCost}
              rows={topProviderKeysByCost?.rows ?? []}
              totalGroups={topProviderKeysByCost?.totalGroups}
              compare={compare}
              valueFormatter={formatUsd}
              labelFormatter={(key) => providerKeyNames?.[key]?.name ?? key}
              onRowClick={(key) =>
                navigate({
                  to: '/observability/traces',
                  search: {
                    live: 'false',
                    range: 'custom',
                    from: filters.startTime,
                    to: filters.endTime,
                    environment: filters.environment,
                    metadata: `provider.api_key_id=${key}`,
                  },
                })
              }
            />
          )}
          {visibleGraphs.ttftP50 && (
            <KpiChartCard
              title="TTFT p50"
              info={BOARD_INFO.ttftP50}
              value={formatLatency(dashboard?.ttftP50Ms ?? 0)}
              delta={compare ? deltas?.ttftP50 : undefined}
              data={ttftP50Data}
              previousData={previousTtftP50Data}
              color={colors.ttftP50}
              timeFmt={timeFmt}
              currentStart={filters.startTime}
              previousStart={previousStart}
              yFormatter={formatLatency}
              tooltipFormatter={formatLatency}
            />
          )}
          {visibleGraphs.ttftP95 && (
            <KpiChartCard
              title="TTFT p95"
              info={BOARD_INFO.ttftP95}
              value={formatLatency(dashboard?.ttftP95Ms ?? 0)}
              delta={compare ? deltas?.ttftP95 : undefined}
              data={ttftP95Data}
              previousData={previousTtftP95Data}
              color={colors.ttftP95}
              timeFmt={timeFmt}
              currentStart={filters.startTime}
              previousStart={previousStart}
              yFormatter={formatLatency}
              tooltipFormatter={formatLatency}
            />
          )}
        </div>
      </div>
    </TooltipProvider>
  )
}
