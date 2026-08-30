import { useEffect, useMemo } from 'react'
import { ui } from '@everstack/ui'
import {
  useSandboxStatsStream,
  useSandboxExecutions,
} from '@/hooks/deployments/use-sandbox'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'

const {
  EChart,
  brandTooltip,
  timeAxis,
  valueAxis,
  baseGrid,
  areaGradient,
  BRAND_PALETTE,
} = ui

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  return d.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

interface ChartDataPoint {
  ts: number
  cpu: number
  memoryPercent: number
  memoryUsage: number
  memoryLimit: number
}

export function MetricsTab() {
  const { instances, activeSessionId: sessionId } = useSandboxContext()
  const { stats, latestStats, isStreaming, start, stop } =
    useSandboxStatsStream(sessionId)

  // Auto-start streaming when sessionId changes
  useEffect(() => {
    if (sessionId && !isStreaming) {
      start()
    }
    return () => {
      stop()
    }
  }, [sessionId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Find the sandbox ID for the selected session to load executions
  const selectedInstance = instances.find((i) => i.sessionId === sessionId)
  const { data: executionsData } = useSandboxExecutions(
    selectedInstance?.id ?? '',
    { limit: 50 },
  )
  const executions = executionsData?.executions ?? []

  const chartData = useMemo<ChartDataPoint[]>(
    () =>
      stats.slice(-150).map((s) => ({
        ts: new Date(s.timestamp).getTime(),
        cpu: s.cpuPercent ?? 0,
        memoryPercent: s.memoryPercent ?? 0,
        memoryUsage: s.memoryUsage ?? 0,
        memoryLimit: s.memoryLimit ?? 0,
      })),
    [stats],
  )

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      {/* Session selector */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
        <SandboxSessionPicker />

        {isStreaming && (
          <span className="flex items-center gap-1 text-xs text-green-400 light:text-green-600">
            <span className="w-1.5 h-1.5 bg-green-500 rounded-full animate-pulse" />
            Live
          </span>
        )}
      </div>

      {!sessionId ? (
        <div className="flex-1 flex flex-col items-center justify-center pb-16">
          <div className="relative mb-6">
            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
              <Iconify.Icon
                icon="heroicons:chart-bar"
                className="size-8 text-brand-secondary-400"
              />
            </div>
          </div>
          <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
            No session selected
          </h3>
          <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
            Select a running sandbox session to view metrics.
          </p>
        </div>
      ) : !latestStats ? (
        <div className="flex-1">
          <Loader loaderText="Waiting for metrics..." />
        </div>
      ) : (
        <div className="p-4 space-y-6">
          {/* Summary Cards */}
          {latestStats && latestStats.cpuPercent != null && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <StatCard
                label="CPU Usage"
                value={`${(latestStats.cpuPercent ?? 0).toFixed(1)}%`}
              />
              <StatCard
                label="Memory"
                value={`${(latestStats.memoryPercent ?? 0).toFixed(1)}%`}
                subtitle={`${formatBytes(latestStats.memoryUsage ?? 0)} / ${formatBytes(latestStats.memoryLimit ?? 0)}`}
              />
              <StatCard label="PIDs" value={String(latestStats.pids ?? 0)} />
              <StatCard
                label="Network"
                value={`${formatBytes(latestStats.networkRxBytes ?? 0)} / ${formatBytes(latestStats.networkTxBytes ?? 0)}`}
                subtitle="RX / TX"
              />
            </div>
          )}

          {/* Charts */}
          {chartData.length > 0 && (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <MetricsChart
                title="CPU Usage"
                data={chartData}
                dataKey="cpu"
                unit="%"
                domain={[0, 100]}
                strokeColor={BRAND_PALETTE[0]}
                fillColor={BRAND_PALETTE[0]}
                currentValue={`${(latestStats?.cpuPercent ?? 0).toFixed(2)}%`}
                formatTooltipValue={(v) => `${v.toFixed(1)}%`}
              />
              <MetricsChart
                title="Memory Usage"
                data={chartData}
                dataKey="memoryPercent"
                unit="%"
                domain={[0, 100]}
                strokeColor="#3b82f6"
                fillColor="#3b82f6"
                currentValue={`${formatBytes(latestStats?.memoryUsage ?? 0)} / ${formatBytes(latestStats?.memoryLimit ?? 0)}`}
                formatTooltipValue={(_, point) =>
                  `${formatBytes(point.memoryUsage)} / ${formatBytes(point.memoryLimit)} (${point.memoryPercent.toFixed(1)}%)`
                }
              />
            </div>
          )}

          {/* Execution History */}
          {executions.length > 0 && (
            <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-4">
              <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-3">
                Execution History
              </h3>
              <div className="space-y-2">
                {executions.slice(0, 20).map((exec) => (
                  <div
                    key={exec.id}
                    className="flex items-center gap-3 text-sm"
                  >
                    <span
                      className={`w-2 h-2 rounded-full ${exec.exitCode === 0 ? 'bg-green-500' : 'bg-red-500'}`}
                    />
                    <span
                      className="font-mono text-white/70 light:text-black/70 truncate flex-1"
                      title={exec.command}
                    >
                      {exec.command.length > 80
                        ? exec.command.slice(0, 80) + '...'
                        : exec.command}
                    </span>
                    <span className="text-white/40 light:text-black/40 text-xs">
                      {exec.durationMs}ms
                    </span>
                    <span
                      className={`text-xs ${exec.exitCode === 0 ? 'text-green-400 light:text-green-600' : 'text-red-400 light:text-red-600'}`}
                    >
                      exit {exec.exitCode}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function StatCard({
  label,
  value,
  subtitle,
}: {
  label: string
  value: string
  subtitle?: string
}) {
  return (
    <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-3">
      <p className="text-xs text-white/50 light:text-black/50 mb-1">{label}</p>
      <p className="text-xl font-bold text-white light:text-brand-main-50">
        {value}
      </p>
      {subtitle && (
        <p className="text-[11px] text-white/30 light:text-black/30">
          {subtitle}
        </p>
      )}
    </div>
  )
}

function MetricsChart({
  title,
  data,
  dataKey,
  unit,
  domain,
  strokeColor,
  fillColor,
  currentValue,
  formatTooltipValue,
}: {
  title: string
  data: ChartDataPoint[]
  dataKey: keyof ChartDataPoint
  unit: string
  domain: [number, number]
  strokeColor: string
  fillColor: string
  currentValue?: string
  formatTooltipValue: (value: number, point: ChartDataPoint) => string
}) {
  const latest = data[data.length - 1]
  const currentNum = latest ? Number(latest[dataKey]) : 0
  const barPercent = unit === '%' ? currentNum : 0

  const option = useMemo(
    () => ({
      grid: baseGrid({ left: 0, right: 4, top: 4, bottom: 0 }),
      tooltip: brandTooltip({
        trigger: 'axis',
        headerFormatter: (v) => formatTime(Number(v)),
        // The full datum rides along as `param.data.raw` so memory can
        // render usage/limit/percent, not just the plotted value.
        valueFormatter: (val, _name, param) => {
          const datum = param?.data as { raw?: ChartDataPoint } | undefined
          return formatTooltipValue(
            val,
            (datum?.raw ?? datum) as ChartDataPoint,
          )
        },
      }),
      xAxis: timeAxis(() => '', { show: false }),
      yAxis: valueAxis((v) => String(v), {
        position: 'left',
        min: domain[0],
        max: domain[1],
        ...(unit === '%' ? { interval: 25 } : {}),
      }),
      series: [
        {
          name: title,
          type: 'line',
          smooth: true,
          symbol: 'none',
          animation: false,
          data: data.map((p) => ({
            value: [p.ts, Number(p[dataKey])],
            raw: p,
          })),
          lineStyle: { width: 1.5, color: strokeColor },
          itemStyle: { color: strokeColor },
          areaStyle: areaGradient(fillColor, 0.3, 0.02),
        },
      ],
    }),
    [
      data,
      dataKey,
      unit,
      domain,
      strokeColor,
      fillColor,
      title,
      formatTooltipValue,
    ],
  )

  return (
    <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-4">
      <h3 className="text-sm font-bold text-white light:text-brand-main-50 mb-1">
        {title}
      </h3>
      {currentValue && (
        <>
          <p className="text-xs text-white/50 light:text-black/50 mb-2">
            Used: {currentValue}
          </p>
          {barPercent > 0 && (
            <div className="w-full bg-brand-main-700 rounded-full h-1.5 mb-3">
              <div
                className="h-1.5 rounded-full transition-all"
                style={{
                  width: `${Math.min(barPercent, 100)}%`,
                  backgroundColor: strokeColor,
                }}
              />
            </div>
          )}
        </>
      )}
      <EChart option={option} height={160} />
      <p className="text-[10px] text-white/30 light:text-black/30 text-center mt-1">
        usage
      </p>
    </div>
  )
}
