import { useMemo } from 'react'
import dayjs from 'dayjs'
import { Info } from 'lucide-react'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import { formatDelta, type MetricPoint, shiftPreviousSeries } from './format'

const {
  Card,
  CardContent,
  Badge,
  Tooltip: InfoTooltip,
  EChart,
  brandTooltip,
  timeAxis,
  valueAxis,
  baseGrid,
  useChartMode,
} = ui

export function KpiChartCard({
  title,
  info,
  value,
  delta,
  data,
  previousData = [],
  color,
  syncId = 'metrics',
  height = 168,
  large = false,
  timeFmt,
  yFormatter,
  tooltipFormatter,
  currentStart,
  previousStart,
  onPointClick,
}: {
  title: string
  info?: string
  value: string
  delta?: number
  data: MetricPoint[]
  previousData?: MetricPoint[]
  color: string
  syncId?: string
  height?: number
  large?: boolean
  timeFmt: string
  yFormatter: (value: number) => string
  tooltipFormatter: (value: number) => string
  currentStart?: string
  previousStart?: string
  onPointClick?: (timestamp: number) => void
}) {
  const shiftedPrevious = shiftPreviousSeries(
    previousData,
    currentStart,
    previousStart,
  )
  const isUp = (delta ?? 0) > 0
  const isDown = (delta ?? 0) < 0
  const mode = useChartMode()

  const option = useMemo(() => {
    const series: Record<string, unknown>[] = []
    if (shiftedPrevious.length > 0) {
      series.push({
        name: 'Previous',
        type: 'line',
        data: shiftedPrevious.map((p) => [p.timestamp, p.value]),
        symbol: 'none',
        lineStyle: {
          width: 1.5,
          type: 'dashed',
          color:
            mode === 'light' ? 'rgba(0,0,0,0.35)' : 'rgba(255,255,255,0.38)',
        },
        z: 1,
        animation: false,
      })
    }
    series.push({
      name: 'Current',
      type: 'line',
      data: data.map((p) => [p.timestamp, p.value]),
      symbol: 'none',
      lineStyle: { width: large ? 2 : 1.75, color },
      itemStyle: { color },
      emphasis: { focus: 'none', itemStyle: { borderWidth: 0 } },
      z: 2,
    })
    return {
      grid: baseGrid({ left: 0, right: 8, top: 8, bottom: 0 }),
      tooltip: brandTooltip({
        headerFormatter: (v) => dayjs(Number(v)).format('MMM D, YYYY HH:mm'),
        valueFormatter: (val) => tooltipFormatter(val),
      }),
      xAxis: timeAxis((v) => dayjs(v).format(timeFmt)),
      yAxis: valueAxis(yFormatter, { min: 0 }),
      series,
    }
  }, [data, shiftedPrevious, color, large, timeFmt, yFormatter, tooltipFormatter, mode])

  return (
    <Card className="border-brand-main-700 bg-brand-main-950/80 rounded">
      <CardContent className="px-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-1">
              <span className="text-[11px] uppercase text-white/45 light:text-black/45">{title}</span>
              {info && (
                <InfoTooltip
                  content={
                    <span className="block w-max max-w-[240px] px-2 py-1 text-left text-xs text-white light:text-brand-main-50">
                      {info}
                    </span>
                  }
                >
                  <Info className="size-3 shrink-0 text-white/30 transition-colors hover:text-white/60 light:text-black/30 light:hover:text-black/60" />
                </InfoTooltip>
              )}
            </div>
            <div
              className={cn(
                'mt-1 font-mono font-semibold text-white tracking-normal light:text-brand-main-50',
                large ? 'text-3xl' : 'text-xl',
              )}
            >
              {value}
            </div>
          </div>
          {delta !== undefined && (
            <Badge
              variant="outline"
              className={cn(
                'rounded border px-1.5 py-0 text-[10px] font-mono',
                isUp &&
                  'border-emerald-500/30 bg-emerald-500/10 text-emerald-400 light:text-emerald-600',
                isDown && 'border-rose-500/30 bg-rose-500/10 text-rose-400 light:text-rose-600',
                !isUp && !isDown && 'border-white/10 bg-white/5 text-white/45 light:border-black/10 light:bg-black/5 light:text-black/45',
              )}
            >
              {formatDelta(delta)}
            </Badge>
          )}
        </div>

        <div className="mt-2">
          <EChart
            option={option}
            height={height}
            group={syncId}
            notMerge
            empty={data.length === 0}
            onEvents={
              onPointClick
                ? {
                    click: (params: any) => {
                      const ts = Array.isArray(params?.value)
                        ? Number(params.value[0])
                        : Number(params?.data?.[0])
                      if (Number.isFinite(ts)) onPointClick(ts)
                    },
                  }
                : undefined
            }
          />
        </div>
      </CardContent>
    </Card>
  )
}
