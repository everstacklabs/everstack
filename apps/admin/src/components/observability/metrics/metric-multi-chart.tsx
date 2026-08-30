import { useMemo, useState } from 'react'
import dayjs from 'dayjs'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import type { MetricPoint } from './format'

const {
  Card,
  CardContent,
  EChart,
  brandTooltip,
  timeAxis,
  valueAxis,
  baseGrid,
  brandPalette,
  useChartMode,
} = ui

export type NamedSeries = {
  name: string
  data: MetricPoint[]
  color?: string
  dashed?: boolean
}

export function MetricMultiChart({
  title,
  series,
  timeFmt,
  height = 240,
  yFormatter = (v: number) => String(v),
  tooltipFormatter = (v: number) => String(v),
  summary,
  summaryLabel,
  syncId = 'metrics',
}: {
  title: string
  series: NamedSeries[]
  timeFmt: string
  height?: number
  yFormatter?: (v: number) => string
  tooltipFormatter?: (v: number) => string
  summary?: string
  summaryLabel?: string
  syncId?: string
}) {
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const showLegend = series.length > 1
  const mode = useChartMode()
  // Theme-aware multi-series palette (brand primary + distinct hues).
  const palette = brandPalette(mode)
  const colorOf = (s: NamedSeries, i: number) =>
    s.color ?? palette[i % palette.length]

  const visibleSeries = useMemo(
    () =>
      series
        .map((s, index) => ({ s, index }))
        .filter(({ s }) => !hidden.has(s.name)),
    [series, hidden],
  )
  const isEmpty = visibleSeries.every(({ s }) => s.data.length === 0)

  const toggle = (name: string) =>
    setHidden((prev) => {
      const next = new Set(prev)
      next.has(name) ? next.delete(name) : next.add(name)
      return next
    })

  const option = useMemo(() => {
    return {
      grid: baseGrid({ left: 0, right: 8, top: 8, bottom: 0 }),
      tooltip: brandTooltip({
        headerFormatter: (v) => dayjs(Number(v)).format('MMM D, YYYY HH:mm'),
        valueFormatter: (val) => tooltipFormatter(val),
      }),
      xAxis: timeAxis((v) => dayjs(v).format(timeFmt)),
      yAxis: valueAxis(yFormatter, { min: 0 }),
      series: visibleSeries.map(({ s, index }) => ({
        name: s.name,
        type: 'line',
        symbol: 'none',
        connectNulls: true,
        data: s.data
          .filter(
            (p) => Number.isFinite(p.timestamp) && Number.isFinite(p.value),
          )
          .map((p) => [p.timestamp, p.value]),
        lineStyle: {
          width: 1.75,
          color: colorOf(s, index),
          type: s.dashed ? 'dashed' : 'solid',
        },
        itemStyle: { color: colorOf(s, index) },
      })),
    }
  }, [visibleSeries, timeFmt, yFormatter, tooltipFormatter, mode])

  return (
    <Card className="border-brand-main-700 bg-brand-main-950/80 rounded">
      <CardContent className="px-3">
        <div className="mb-2 flex items-center justify-between gap-3">
          <span className="text-[11px] uppercase text-white/45 light:text-black/45">{title}</span>
          {summary !== undefined && (
            <span className="text-xs text-white/70 light:text-black/70">
              <span className="font-mono text-white light:text-brand-main-50">{summary}</span>
              {summaryLabel ? (
                <span className="ml-1 text-white/40 light:text-black/40">{summaryLabel}</span>
              ) : null}
            </span>
          )}
        </div>

        <EChart
          option={option}
          height={height}
          group={syncId}
          notMerge
          empty={isEmpty}
        />

        {showLegend && (
          <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">
            {series.map((s, i) => {
              const off = hidden.has(s.name)
              return (
                <button
                  key={s.name}
                  type="button"
                  onClick={() => toggle(s.name)}
                  className={cn(
                    'flex items-center gap-1.5 text-[11px] transition-opacity',
                    off ? 'opacity-35' : 'opacity-90',
                  )}
                >
                  <span
                    className="inline-block size-2 rounded-full"
                    style={{ background: colorOf(s, i) }}
                  />
                  <span className="font-mono text-white/70 light:text-black/70">{s.name}</span>
                </button>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
