import { useState, useMemo, useEffect, useCallback } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useMetricsDashboard,
  useMetricsTimeSeries,
  useTtftTimeSeries,
  useMetricsGroupedTimeSeries,
  useMetricsDimensionOptions,
  useMetricsBreakdown,
  useEnvironmentOptions,
  type MetricsFilterOptions,
} from '@/hooks/observability/use-metrics'
import { useProviderKeyNames } from '@/hooks/observability/use-provider-key-names'
import { useOutcomeDashboard } from '@/hooks/observability/use-outcomes'
import {
  MetricsBoard,
  BOARD_GRAPHS,
  type BoardGraphKey,
} from '@/components/observability/metrics/metrics-board'
import { MetricsToolbar } from '@/components/observability/metrics/metrics-toolbar'
import {
  MetricMultiChart,
  type NamedSeries,
} from '@/components/observability/metrics/metric-multi-chart'
import type { MetricPoint } from '@/components/observability/metrics/format'
import { ui } from '@everstack/ui'
import {
  Activity,
  Clock,
  DollarSign,
  AlertTriangle,
  Hash,
  Gauge,
  TrendingUp,
  Zap,
  Coins,
  Repeat,
  Trophy,
} from 'lucide-react'

type ProtoTimestamp = {
  seconds?: bigint | number | string
  nanos?: number
  toDate?: () => Date
}

const { Card, CardContent, Tabs, TabsList, TabsTrigger } = ui

const metricsSearchSchema = z.object({
  tab: z
    .enum(['overview', 'requests', 'performance', 'costs'])
    .optional()
    .default('overview'),
  range: z
    .enum([
      '15m',
      '6h',
      '12h',
      '24h',
      '3d',
      '7d',
      '14d',
      '30d',
      '90d',
      'custom',
    ])
    .optional()
    .default('24h'),
  from: z.string().optional(),
  to: z.string().optional(),
  env: z.string().optional(),
  model: z.string().optional(),
  provider: z.string().optional(),
  granularity: z.enum(['hour', '6hour', 'day']).optional(),
  compare: z.string().optional().default('false'),
})

export const Route = createFileRoute('/observability/metrics')({
  component: MetricsDashboardPage,
  validateSearch: metricsSearchSchema,
})

const GRAPH_STORAGE_KEY = 'metrics.board.graphs.v1'

function defaultVisibleGraphs(): Record<BoardGraphKey, boolean> {
  return BOARD_GRAPHS.reduce(
    (acc, graph) => {
      acc[graph.key] = true
      return acc
    },
    {} as Record<BoardGraphKey, boolean>,
  )
}

type ProtoBucket = {
  timestamp?: ProtoTimestamp
  value?: number | string | bigint | null
  label?: string
}

type ProtoSeries = {
  metricName?: string
  buckets?: ProtoBucket[]
}

function timestampToMillis(ts: ProtoTimestamp | undefined): number | null {
  if (!ts) return null
  if (typeof ts.toDate === 'function') {
    const ms = ts.toDate().getTime()
    return Number.isFinite(ms) ? ms : null
  }
  if (ts.seconds === undefined && ts.nanos === undefined) return null
  const seconds =
    typeof ts.seconds === 'bigint'
      ? Number(ts.seconds)
      : Number(ts.seconds ?? 0)
  const nanos = Number(ts.nanos ?? 0)
  const ms = seconds * 1000 + Math.floor(nanos / 1_000_000)
  return Number.isFinite(ms) ? ms : null
}

function bucketValue(value: ProtoBucket['value']): number {
  const n = Number(value ?? 0)
  return Number.isFinite(n) ? n : 0
}

function bucketsToPoints(buckets: ProtoBucket[] | undefined): MetricPoint[] {
  return (buckets ?? [])
    .map((bucket) => {
      const timestamp = timestampToMillis(bucket.timestamp)
      if (timestamp === null) return null
      return { timestamp, value: bucketValue(bucket.value) }
    })
    .filter((point): point is MetricPoint => point !== null)
    .sort((a, b) => a.timestamp - b.timestamp)
}

function getWindowMinutes(filters: MetricsFilterOptions): number {
  const start = filters.startTime ? new Date(filters.startTime).getTime() : NaN
  const end = filters.endTime ? new Date(filters.endTime).getTime() : NaN
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) {
    return 24 * 60
  }
  return Math.max((end - start) / 60_000, 1)
}

function getTimeFormat(filters: MetricsFilterOptions): string {
  if (filters.interval === 'day') return 'MMM D'
  const start = filters.startTime ? new Date(filters.startTime).getTime() : NaN
  const end = filters.endTime ? new Date(filters.endTime).getTime() : NaN
  if (Number.isFinite(start) && Number.isFinite(end)) {
    const windowMs = end - start
    return windowMs <= 24 * 60 * 60 * 1000 ? 'HH:mm' : 'MMM D HH:mm'
  }
  return 'MMM D HH:mm'
}

function formatNumber(n: number): string {
  if (!Number.isFinite(n)) return '0'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return n.toFixed(0)
}

// Convert proto time-series (metricName + buckets) into the normalized series
// shape consumed by MetricMultiChart.
function protoToNamedSeries(series: ProtoSeries[] | undefined): NamedSeries[] {
  return (series ?? []).map((s) => ({
    name: s.metricName || s.buckets?.find((b) => b.label)?.label || 'unknown',
    data: bucketsToPoints(s.buckets),
  }))
}

function formatCost(n: number): string {
  if (!Number.isFinite(n)) return '$0.00'
  if (n !== 0 && Math.abs(n) < 0.01) return `$${n.toFixed(4)}`
  return `$${n.toFixed(2)}`
}

function formatCostAxis(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '$0'
  if (Math.abs(n) < 0.01) return `$${n.toFixed(4)}`
  if (Math.abs(n) < 1) return `$${n.toFixed(2)}`
  if (Math.abs(n) >= 1000) return `$${(n / 1000).toFixed(1)}k`
  return `$${n.toFixed(0)}`
}

function formatLatency(ms: number | null | undefined): string {
  // Defensive guard: a NaN/Infinity backend value (e.g. quantile() on an
  // empty tenant window) would otherwise render "NaNms". Belt to the
  // server-side ifNotFinite suspenders.
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return 'N/A'
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`
  return `${ms.toFixed(0)}ms`
}

/** Derive input/output token time series from total_tokens using the dashboard ratio as fallback */
function deriveTokenSplit(
  findSeries: (name: string) => Array<{ timestamp: number; value: number }>,
  dashboard: any,
): {
  inputTokens: Array<{ timestamp: number; value: number }>
  outputTokens: Array<{ timestamp: number; value: number }>
} {
  const directInput = findSeries('input_tokens')
  const directOutput = findSeries('output_tokens')

  // If the backend returns dedicated input/output series, use them
  if (directInput.length > 0 || directOutput.length > 0) {
    return { inputTokens: directInput, outputTokens: directOutput }
  }

  // Fallback: split total_tokens using the dashboard's input/output ratio
  const totalTokensSeries = findSeries('total_tokens')
  const totalInput = Number(dashboard?.totalInputTokens ?? 0)
  const totalOutput = Number(dashboard?.totalOutputTokens ?? 0)
  const total = totalInput + totalOutput

  if (total === 0 || totalTokensSeries.length === 0) {
    return { inputTokens: [], outputTokens: [] }
  }

  const inputRatio = totalInput / total
  const outputRatio = totalOutput / total

  return {
    inputTokens: totalTokensSeries.map((d) => ({
      timestamp: d.timestamp,
      value: d.value * inputRatio,
    })),
    outputTokens: totalTokensSeries.map((d) => ({
      timestamp: d.timestamp,
      value: d.value * outputRatio,
    })),
  }
}

function deriveTokenTotals(
  findSeries: (name: string) => Array<{ timestamp: number; value: number }>,
  dashboard: any,
): { inputTokens: number; outputTokens: number; totalTokens: number } {
  const directInput = findSeries('input_tokens')
  const directOutput = findSeries('output_tokens')
  const dashboardInput = Number(dashboard?.totalInputTokens ?? 0)
  const dashboardOutput = Number(dashboard?.totalOutputTokens ?? 0)
  const dashboardTotal = Number(dashboard?.totalTokens ?? 0)

  if (directInput.length > 0 || directOutput.length > 0) {
    const inputTokens =
      directInput.length > 0
        ? directInput.reduce((sum, point) => sum + point.value, 0)
        : dashboardInput
    const outputTokens =
      directOutput.length > 0
        ? directOutput.reduce((sum, point) => sum + point.value, 0)
        : dashboardOutput
    return {
      inputTokens,
      outputTokens,
      totalTokens:
        dashboardTotal > 0 ? dashboardTotal : inputTokens + outputTokens,
    }
  }

  return {
    inputTokens: dashboardInput,
    outputTokens: dashboardOutput,
    totalTokens:
      dashboardTotal > 0 ? dashboardTotal : dashboardInput + dashboardOutput,
  }
}

function MetricsDashboardPage() {
  const gate = useFeatureGate(FeatureKey.ADVANCED_ANALYTICS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Advanced Analytics"
        description="Enhanced usage analytics and deep insights for your AI gateway."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <MetricsDashboardPageContent />
}

function MetricsDashboardPageContent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const timeRange = search.range
  const model = search.model ?? 'all'
  const provider = search.provider ?? 'all'
  const environment = search.env ?? 'all'
  const activeTab = search.tab
  const compare = search.compare === 'true'
  const [visibleGraphs, setVisibleGraphs] = useState<
    Record<BoardGraphKey, boolean>
  >(() => {
    if (typeof window === 'undefined') return defaultVisibleGraphs()
    try {
      return {
        ...defaultVisibleGraphs(),
        ...JSON.parse(window.localStorage.getItem(GRAPH_STORAGE_KEY) ?? '{}'),
      }
    } catch {
      return defaultVisibleGraphs()
    }
  })

  const updateSearch = (
    patch: Partial<z.infer<typeof metricsSearchSchema>>,
  ) => {
    navigate({
      search: (prev) => ({
        ...prev,
        ...patch,
      }),
      replace: true,
    })
  }

  const filters = useMemo(() => {
    const msMap: Record<string, number> = {
      '15m': 15 * 60 * 1000,
      '6h': 6 * 60 * 60 * 1000,
      '12h': 12 * 60 * 60 * 1000,
      '24h': 24 * 60 * 60 * 1000,
      '3d': 3 * 24 * 60 * 60 * 1000,
      '7d': 7 * 24 * 60 * 60 * 1000,
      '14d': 14 * 24 * 60 * 60 * 1000,
      '30d': 30 * 24 * 60 * 60 * 1000,
      '90d': 90 * 24 * 60 * 60 * 1000,
    }
    // For a custom range, derive the window length from from/to so the
    // granularity defaults sensibly; otherwise use the preset length.
    const rawCustomMs =
      search.from && search.to
        ? new Date(search.to).getTime() - new Date(search.from).getTime()
        : undefined
    const customMs =
      rawCustomMs !== undefined &&
      Number.isFinite(rawCustomMs) &&
      rawCustomMs > 0
        ? rawCustomMs
        : undefined
    const rangeMs = customMs ?? msMap[timeRange] ?? 24 * 60 * 60 * 1000
    const now = new Date()
    now.setSeconds(0, 0)
    const hasCustomRange = customMs !== undefined
    const startTime = hasCustomRange
      ? search.from
      : new Date(now.getTime() - rangeMs).toISOString()
    const endTime = hasCustomRange ? search.to : now.toISOString()
    const interval =
      search.granularity ??
      (rangeMs <= 24 * 60 * 60 * 1000
        ? 'hour'
        : rangeMs <= 7 * 24 * 60 * 60 * 1000
          ? '6hour'
          : 'day')

    return {
      startTime,
      endTime,
      model: model !== 'all' ? model : undefined,
      provider: provider !== 'all' ? provider : undefined,
      environment: environment !== 'all' ? environment : undefined,
      interval,
      compare,
    } satisfies MetricsFilterOptions
  }, [
    timeRange,
    search.from,
    search.to,
    search.granularity,
    model,
    provider,
    environment,
    compare,
  ])

  const {
    data: dashboardResp,
    isLoading,
    dataUpdatedAt: dashboardUpdatedAt,
  } = useMetricsDashboard(filters)
  const { data: outcomeResp } = useOutcomeDashboard({
    startTime: filters.startTime,
    endTime: filters.endTime,
  })
  const verdictRates = outcomeResp?.verdictRates
  const verdictSampleSize = verdictRates ? Number(verdictRates.sampleSize) : 0
  const winRate =
    verdictRates && verdictSampleSize > 0
      ? `${(verdictRates.winRate * 100).toFixed(1)}%`
      : '-'
  const { data: timeseriesResp, dataUpdatedAt: timeseriesUpdatedAt } =
    useMetricsTimeSeries(filters)
  const { data: ttftResp, dataUpdatedAt: ttftUpdatedAt } =
    useTtftTimeSeries(filters)
  const { data: topModelsByErrors } = useMetricsBreakdown(
    filters,
    'errors',
    'model',
    5,
  )
  const { data: topModelsByRequests } = useMetricsBreakdown(
    filters,
    'requests',
    'model',
    5,
  )
  const { data: topTracesByRequests } = useMetricsBreakdown(
    filters,
    'requests',
    'trace_name',
    5,
  )
  const { data: topSessionsByCost } = useMetricsBreakdown(
    filters,
    'cost',
    'session',
    5,
  )
  const { data: topUsersByCost } = useMetricsBreakdown(
    filters,
    'cost',
    'user',
    5,
  )
  const { data: topProviderKeysByCost } = useMetricsBreakdown(
    filters,
    'cost',
    'provider_api_key_id',
    5,
  )
  const providerKeyNames = useProviderKeyNames()

  const { data: modelOptionsResp } = useMetricsDimensionOptions(
    { ...filters, model: undefined },
    'model',
  )
  const { data: providerOptionsResp } = useMetricsDimensionOptions(
    { ...filters, provider: undefined },
    'provider',
  )
  const { data: environmentOptionsResp } = useEnvironmentOptions(filters)

  const dashboard = dashboardResp?.dashboard
  const series = timeseriesResp?.series ?? []

  const findSeries = useCallback(
    (name: string) =>
      series
        .filter((s) => s.metricName === name)
        .flatMap((s) => bucketsToPoints(s.buckets))
        .sort((a, b) => a.timestamp - b.timestamp),
    [series],
  )

  const modelOptions = useMemo(() => {
    const options = modelOptionsResp?.series?.map((s) => s.metricName) ?? []
    return options.filter((opt) => opt && opt !== 'unknown').sort()
  }, [modelOptionsResp])

  const providerOptions = useMemo(() => {
    const options = providerOptionsResp?.series?.map((s) => s.metricName) ?? []
    return options.filter((opt) => opt && opt !== 'unknown').sort()
  }, [providerOptionsResp])

  const environmentOptions = useMemo(() => {
    const options = environmentOptionsResp?.rows?.map((row) => row.key) ?? []
    return options.filter((opt) => opt && opt !== 'unknown').sort()
  }, [environmentOptionsResp])

  const lastUpdatedAt = Math.max(
    dashboardUpdatedAt,
    timeseriesUpdatedAt,
    ttftUpdatedAt,
  )

  // Auto-reset stale selections when the available options change
  // e.g. selecting a provider that doesn't serve the currently selected model
  useEffect(() => {
    if (
      model !== 'all' &&
      modelOptions.length > 0 &&
      !modelOptions.includes(model)
    ) {
      updateSearch({ model: undefined })
    }
  }, [model, modelOptions])

  useEffect(() => {
    if (
      provider !== 'all' &&
      providerOptions.length > 0 &&
      !providerOptions.includes(provider)
    ) {
      updateSearch({ provider: undefined })
    }
  }, [provider, providerOptions])

  useEffect(() => {
    if (
      environment !== 'all' &&
      environmentOptions.length > 0 &&
      !environmentOptions.includes(environment)
    ) {
      updateSearch({ env: undefined })
    }
  }, [environment, environmentOptions])

  const setGraphVisible = (key: BoardGraphKey, value: boolean) => {
    setVisibleGraphs((prev) => {
      const next = { ...prev, [key]: value }
      if (typeof window !== 'undefined') {
        window.localStorage.setItem(GRAPH_STORAGE_KEY, JSON.stringify(next))
      }
      return next
    })
  }

  const exportPayload = {
    filters,
    dashboard: dashboardResp?.dashboard,
    previousDashboard: dashboardResp?.previous,
    deltas: dashboardResp?.deltas,
    series: timeseriesResp?.series,
    previousSeries: timeseriesResp?.previousSeries,
    ttftSeries: ttftResp?.series,
    previousTtftSeries: ttftResp?.previousSeries,
    topModelsByErrors: topModelsByErrors?.rows,
    topModelsByRequests: topModelsByRequests?.rows,
    topTracesByRequests: topTracesByRequests?.rows,
  }

  return (
    <div className="flex flex-col h-full w-full overflow-hidden">
      <div className="shrink-0 border-b border-brand-main-700 bg-brand-main-900/50 px-3 py-1.5">
        <div className="flex items-center justify-between gap-3">
          <Tabs
            value={activeTab}
            onValueChange={(tab) =>
              updateSearch({
                tab: tab as 'overview' | 'requests' | 'performance' | 'costs',
              })
            }
            className="w-auto"
          >
            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
              <TabsTrigger
                className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                value="overview"
              >
                Overview
              </TabsTrigger>
              <TabsTrigger
                className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                value="requests"
              >
                Requests
              </TabsTrigger>
              <TabsTrigger
                className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                value="performance"
              >
                Performance
              </TabsTrigger>
              <TabsTrigger
                className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                value="costs"
              >
                Costs
              </TabsTrigger>
            </TabsList>
          </Tabs>

          <MetricsToolbar
            provider={provider}
            model={model}
            environment={environment}
            granularity={filters.interval ?? 'hour'}
            compare={compare}
            timeRange={timeRange}
            from={search.from}
            to={search.to}
            providerOptions={providerOptions}
            modelOptions={modelOptions}
            environmentOptions={environmentOptions}
            visibleGraphs={visibleGraphs}
            lastUpdatedAt={lastUpdatedAt}
            exportPayload={exportPayload}
            onProviderChange={(value) =>
              updateSearch({ provider: value === 'all' ? undefined : value })
            }
            onModelChange={(value) =>
              updateSearch({ model: value === 'all' ? undefined : value })
            }
            onEnvironmentChange={(value) =>
              updateSearch({ env: value === 'all' ? undefined : value })
            }
            onGranularityChange={(value) =>
              updateSearch({ granularity: value as 'hour' | '6hour' | 'day' })
            }
            onCompareChange={(value) =>
              updateSearch({ compare: value ? 'true' : 'false' })
            }
            onTimeRangeChange={(value) =>
              updateSearch({
                range: value as typeof timeRange,
                from: undefined,
                to: undefined,
              })
            }
            onCustomRangeChange={(range) =>
              updateSearch({
                range: 'custom',
                from: range.start.toISOString(),
                to: range.end.toISOString(),
              })
            }
            onGraphToggle={setGraphVisible}
          />
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        {activeTab === 'overview' && (
          <MetricsBoard
            dashboardResp={dashboardResp}
            timeseriesResp={timeseriesResp}
            ttftResp={ttftResp}
            topModelsByErrors={topModelsByErrors}
            topModelsByRequests={topModelsByRequests}
            topTracesByRequests={topTracesByRequests}
            topSessionsByCost={topSessionsByCost}
            topUsersByCost={topUsersByCost}
            topProviderKeysByCost={topProviderKeysByCost}
            providerKeyNames={providerKeyNames}
            filters={filters}
            compare={compare}
            visibleGraphs={visibleGraphs}
          />
        )}
        {activeTab === 'requests' && (
          <RequestsTab
            isLoading={isLoading}
            findSeries={findSeries}
            dashboard={dashboard}
            filters={filters}
          />
        )}
        {activeTab === 'performance' && (
          <PerformanceTab
            isLoading={isLoading}
            findSeries={findSeries}
            dashboard={dashboard}
            filters={filters}
            winRate={winRate}
            verdictSampleSize={verdictSampleSize}
          />
        )}
        {activeTab === 'costs' && (
          <CostsTab
            isLoading={isLoading}
            findSeries={findSeries}
            dashboard={dashboard}
            filters={filters}
          />
        )}
      </div>
    </div>
  )
}

function RequestsTab({
  isLoading,
  findSeries,
  dashboard,
  filters,
}: {
  isLoading: boolean
  findSeries: (name: string) => Array<{ timestamp: number; value: number }>
  dashboard?: any
  filters: MetricsFilterOptions
}) {
  const requestData = findSeries('request_count')
  const rangeMinutes = getWindowMinutes(filters)

  const { data: requestsByProvider } = useMetricsGroupedTimeSeries(
    filters,
    'request_count',
    'provider',
  )
  const errorRateData = findSeries('error_rate')

  const timeFmt = getTimeFormat(filters)

  return (
    <div className="p-3 space-y-3">
      <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
        <MetricCard
          icon={<Activity className="h-4 w-4" />}
          label="Total Requests"
          value={formatNumber(Number(dashboard?.totalRequests ?? 0))}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Gauge className="h-4 w-4" />}
          label="Requests/min"
          value={formatNumber(
            Number(dashboard?.totalRequests ?? 0) / rangeMinutes,
          )}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<TrendingUp className="h-4 w-4" />}
          label="Peak Requests"
          value={formatNumber(Math.max(...requestData.map((d) => d.value), 0))}
          isLoading={isLoading}
        />
      </div>

      <MetricMultiChart
        title="Request Volume"
        series={[{ name: 'Requests', data: requestData }]}
        timeFmt={timeFmt}
        yFormatter={(v) =>
          v >= 1000 ? `${(v / 1000).toFixed(1)}k` : String(Math.round(v))
        }
        tooltipFormatter={(v) => formatNumber(v)}
      />

      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        <MetricMultiChart
          title="Request Volume by Provider"
          series={protoToNamedSeries(requestsByProvider?.series)}
          timeFmt={timeFmt}
          yFormatter={(v) =>
            v >= 1000 ? `${(v / 1000).toFixed(1)}k` : String(Math.round(v))
          }
          tooltipFormatter={(v) => formatNumber(v)}
        />
        <MetricMultiChart
          title="Error Rate Over Time"
          series={[{ name: 'Error rate', data: errorRateData }]}
          timeFmt={timeFmt}
          yFormatter={(v) => `${(v * 100).toFixed(1)}%`}
          tooltipFormatter={(v) => `${(v * 100).toFixed(2)}%`}
          summary={`${((dashboard?.errorRate ?? 0) * 100).toFixed(1)}%`}
          summaryLabel="avg"
        />
      </div>
    </div>
  )
}

function PerformanceTab({
  isLoading,
  findSeries,
  dashboard,
  filters,
  winRate,
  verdictSampleSize,
}: {
  isLoading: boolean
  findSeries: (name: string) => Array<{ timestamp: number; value: number }>
  dashboard?: any
  filters: MetricsFilterOptions
  winRate: string
  verdictSampleSize: number
}) {
  const latencyData = findSeries('avg_latency_ms')
  const minLatency =
    latencyData.length > 0 ? Math.min(...latencyData.map((d) => d.value)) : 0
  const maxLatency =
    latencyData.length > 0 ? Math.max(...latencyData.map((d) => d.value)) : 0

  const { data: latencyByProvider } = useMetricsGroupedTimeSeries(
    filters,
    'avg_latency_ms',
    'provider',
  )
  const { data: latencyByModel } = useMetricsGroupedTimeSeries(
    filters,
    'avg_latency_ms',
    'model',
  )

  const timeFmt = getTimeFormat(filters)

  return (
    <div className="p-3 space-y-3">
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-2">
        <MetricCard
          icon={<Clock className="h-4 w-4" />}
          label="Avg Latency"
          value={formatLatency(dashboard?.avgLatencyMs ?? 0)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Gauge className="h-4 w-4" />}
          label="P50 Latency"
          value={formatLatency(dashboard?.p50LatencyMs ?? 0)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Gauge className="h-4 w-4" />}
          label="P95 Latency"
          value={formatLatency(dashboard?.p95LatencyMs ?? 0)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Gauge className="h-4 w-4" />}
          label="P99 Latency"
          value={formatLatency(dashboard?.p99LatencyMs ?? 0)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Zap className="h-4 w-4" />}
          label="Min Latency"
          value={formatLatency(minLatency)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<AlertTriangle className="h-4 w-4" />}
          label="Max Latency"
          value={formatLatency(maxLatency)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Repeat className="h-4 w-4" />}
          label="Agent Turns"
          value={formatNumber(Number(dashboard?.totalAgentTurns ?? 0))}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Trophy className="h-4 w-4" />}
          label={
            verdictSampleSize > 0
              ? `Win Rate (n=${verdictSampleSize})`
              : 'Win Rate'
          }
          value={winRate}
          isLoading={isLoading}
        />
      </div>

      <MetricMultiChart
        title="Latency Over Time"
        series={[{ name: 'Latency', data: latencyData }]}
        timeFmt={timeFmt}
        yFormatter={(v) => `${v.toFixed(0)}ms`}
        tooltipFormatter={(v) => formatLatency(v)}
      />

      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        <MetricMultiChart
          title="Latency by Provider"
          series={protoToNamedSeries(latencyByProvider?.series)}
          timeFmt={timeFmt}
          yFormatter={(v) => `${v.toFixed(0)}ms`}
          tooltipFormatter={(v) => formatLatency(v)}
        />
        <MetricMultiChart
          title="Latency by Model"
          series={protoToNamedSeries(latencyByModel?.series)}
          timeFmt={timeFmt}
          yFormatter={(v) => `${v.toFixed(0)}ms`}
          tooltipFormatter={(v) => formatLatency(v)}
        />
      </div>
    </div>
  )
}

function CostsTab({
  isLoading,
  findSeries,
  dashboard,
  filters,
}: {
  isLoading: boolean
  findSeries: (name: string) => Array<{ timestamp: number; value: number }>
  dashboard?: any
  filters: MetricsFilterOptions
}) {
  const costData = findSeries('total_cost')
  const tokenTotals = useMemo(
    () => deriveTokenTotals(findSeries, dashboard),
    [dashboard, findSeries],
  )
  const totalRequests = Number(dashboard?.totalRequests ?? 0)
  const costPerRequest =
    totalRequests > 0 ? (dashboard?.totalCost ?? 0) / totalRequests : 0

  const { data: costByProvider } = useMetricsGroupedTimeSeries(
    filters,
    'total_cost',
    'provider',
  )

  const { inputTokens: inputTokenData, outputTokens: outputTokenData } =
    deriveTokenSplit(findSeries, dashboard)

  const timeFmt = getTimeFormat(filters)

  return (
    <div className="p-3 space-y-3">
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-2">
        <MetricCard
          icon={<DollarSign className="h-4 w-4" />}
          label="Total Cost"
          value={formatCost(dashboard?.totalCost ?? 0)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Hash className="h-4 w-4" />}
          label="Total Tokens"
          value={formatNumber(tokenTotals.totalTokens)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Coins className="h-4 w-4" />}
          label="Cost/1K Tokens"
          value={`$${(tokenTotals.totalTokens > 0 ? (dashboard?.totalCost ?? 0) / (tokenTotals.totalTokens / 1000) : 0).toFixed(2)}`}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Hash className="h-4 w-4" />}
          label="Input Tokens"
          value={formatNumber(tokenTotals.inputTokens)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Hash className="h-4 w-4" />}
          label="Output Tokens"
          value={formatNumber(tokenTotals.outputTokens)}
          isLoading={isLoading}
        />
        <MetricCard
          icon={<Coins className="h-4 w-4" />}
          label="Cost/Request"
          value={`$${costPerRequest.toFixed(4)}`}
          isLoading={isLoading}
        />
      </div>

      <MetricMultiChart
        title="Cost Over Time"
        series={[{ name: 'Cost', data: costData }]}
        timeFmt={timeFmt}
        yFormatter={formatCostAxis}
        tooltipFormatter={(v) => formatCost(v)}
      />

      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        <MetricMultiChart
          title="Cost by Provider"
          series={protoToNamedSeries(costByProvider?.series)}
          timeFmt={timeFmt}
          yFormatter={formatCostAxis}
          tooltipFormatter={(v) => formatCost(v)}
        />
        <MetricMultiChart
          title="Token Usage Over Time"
          series={[
            { name: 'Input Tokens', data: inputTokenData },
            { name: 'Output Tokens', data: outputTokenData },
          ]}
          timeFmt={timeFmt}
          yFormatter={(v) =>
            v >= 1000 ? `${(v / 1000).toFixed(0)}k` : String(Math.round(v))
          }
          tooltipFormatter={(v) => formatNumber(v)}
        />
      </div>
    </div>
  )
}

function MetricCard({
  icon,
  label,
  value,
  isLoading,
}: {
  icon: React.ReactNode
  label: string
  value: string
  isLoading?: boolean
}) {
  return (
    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
      <CardContent className="">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            <div className="p-2.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
              <div className="text-brand-secondary-300">{icon}</div>
            </div>
            <div className="min-w-0">
              <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide truncate">
                {label}
              </div>
            </div>
          </div>
          {isLoading ? (
            <div className="h-4 w-10 bg-brand-main-800/60 rounded animate-pulse" />
          ) : (
            <div className="text-sm font-semibold text-white light:text-brand-main-50">
              {value}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
