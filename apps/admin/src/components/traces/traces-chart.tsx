import { ui } from '@everstack/ui'
import type { Trace } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { useCallback, useMemo, useRef, useState } from 'react'
import dayjs from 'dayjs'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

const { EChart, brandTooltip, valueAxis, baseGrid, SEMANTIC } = ui

interface TracesChartProps {
  traces: Trace[]
  from: string
  to: string
  activeChart?: 'total' | 'success' | 'error'
  onChartChange?: (chart: 'total' | 'success' | 'error') => void
  totals?: { total: number; success: number; error: number }
  onZoom?: (from: string, to: string) => void
  isLiveMode?: boolean
}

function getTimestampMs(trace: Trace): number {
  if (!trace.startTime) return 0
  const seconds =
    typeof trace.startTime.seconds === 'bigint'
      ? safeBigIntToNumber(trace.startTime.seconds)
      : Number(trace.startTime.seconds || 0)
  const nanos =
    typeof trace.startTime.nanos === 'bigint'
      ? safeBigIntToNumber(trace.startTime.nanos)
      : Number(trace.startTime.nanos || 0)
  return seconds * 1000 + Math.floor(nanos / 1_000_000)
}

function groupTracesByTimeInterval(
  traces: Trace[],
  from: string,
  to: string,
  isLiveMode?: boolean,
): {
  data: Array<{
    date: string
    timestamp: number
    total: number
    success: number
    error: number
  }>
  intervalMs: number
} {
  const fromDate = new Date(from)
  let toDate = new Date(to)

  if (isLiveMode && traces.length > 0) {
    const maxTimestamp = Math.max(...traces.map(getTimestampMs))
    if (maxTimestamp > toDate.getTime()) {
      toDate = new Date(maxTimestamp)
    }
  }

  const duration = toDate.getTime() - fromDate.getTime()

  let intervalMs: number
  let dateFormat: string

  if (duration <= 3600000) {
    intervalMs = 60000
    dateFormat = 'HH:mm'
  } else if (duration <= 86400000) {
    intervalMs = 300000
    dateFormat = 'HH:mm'
  } else if (duration <= 604800000) {
    intervalMs = 3600000
    dateFormat = 'MMM D HH:mm'
  } else if (duration <= 2592000000) {
    intervalMs = 21600000
    dateFormat = 'MMM D'
  } else {
    intervalMs = 86400000
    dateFormat = 'MMM D'
  }

  const buckets = new Map<
    number,
    { total: number; success: number; error: number }
  >()
  const startTime = Math.floor(fromDate.getTime() / intervalMs) * intervalMs

  for (
    let current = startTime;
    current <= toDate.getTime();
    current += intervalMs
  ) {
    buckets.set(current, { total: 0, success: 0, error: 0 })
  }

  traces.forEach((trace) => {
    const timestampMs = getTimestampMs(trace)
    if (timestampMs < fromDate.getTime() || timestampMs > toDate.getTime())
      return

    const bucketTime = Math.floor(timestampMs / intervalMs) * intervalMs
    const bucket = buckets.get(bucketTime)
    if (!bucket) return

    bucket.total++
    const hasError = trace.status?.toUpperCase() === 'ERROR'
    hasError ? bucket.error++ : bucket.success++
  })

  return {
    data: Array.from(buckets.entries())
      .map(([timestamp, { total, success, error }]) => ({
        timestamp,
        date: dayjs(timestamp).format(dateFormat),
        total,
        success,
        error,
      }))
      .sort((a, b) => a.timestamp - b.timestamp),
    intervalMs,
  }
}

export function TracesChart({
  traces,
  from,
  to,
  activeChart = 'total',
  onZoom,
  isLiveMode,
}: TracesChartProps) {
  const { data: chartData, intervalMs } = useMemo(
    () => groupTracesByTimeInterval(traces, from, to, isLiveMode),
    [traces, from, to, isLiveMode],
  )

  const errorPoints = useMemo(
    () => chartData.filter((d) => d.error > 0),
    [chartData],
  )
  const hasErrorPoints = errorPoints.length > 0

  const getTickFormat = useMemo(() => {
    const duration = new Date(to).getTime() - new Date(from).getTime()
    if (duration <= 86400000) return 'HH:mm'
    if (duration <= 604800000) return 'MMM D HH:mm'
    return 'MMM D'
  }, [from, to])

  // Pin the axis bounds and force a fixed tick interval so ticks stay evenly
  // spread. A bare `type: 'time'` axis auto-places ticks at "nice" boundaries
  // and drops overlapping labels, which reads as an uneven spread.
  const xAxisBounds = useMemo(() => {
    if (chartData.length === 0) return {}
    const min = chartData[0].timestamp
    const last = chartData[chartData.length - 1].timestamp
    const max = Math.max(last, new Date(to).getTime())
    const range = max - min
    if (range <= 0) return { min, max }
    const tickCount = range <= 960000 ? 15 : 12
    return { min, max, interval: range / tickCount }
  }, [chartData, to])

  // Drag-to-zoom is reimplemented over zrender (ECharts has no recharts-style
  // ReferenceArea). We track the dragged x-range in refs (read by the once-bound
  // zr handlers) and mirror it into state purely to draw the selection markArea.
  const [refLeft, setRefLeft] = useState<number | null>(null)
  const [refRight, setRefRight] = useState<number | null>(null)
  const dragStartRef = useRef<number | null>(null)
  const dragStartPxRef = useRef<number | null>(null)
  const dragLastRef = useRef<number | null>(null)
  const isDraggingRef = useRef(false)
  // Keep live config out of the once-bound handlers' stale closure.
  const cfgRef = useRef({ onZoom, intervalMs })
  cfgRef.current = { onZoom, intervalMs }

  const option = useMemo(() => {
    const successBar = {
      name: 'Success',
      type: 'bar',
      barWidth: 8,
      data: chartData.map((d) => [d.timestamp, d.success]),
      itemStyle: { color: SEMANTIC.success, borderRadius: [2, 2, 0, 0] },
      cursor: 'pointer',
    }
    const errorScatter = {
      name: 'Errors',
      type: 'scatter',
      symbolSize: 8,
      data: errorPoints.map((d) => [d.timestamp, d.error]),
      itemStyle: { color: SEMANTIC.error },
      cursor: 'pointer',
    }

    const series: Record<string, unknown>[] = []
    if (activeChart === 'total') {
      series.push(successBar)
      if (hasErrorPoints) series.push(errorScatter)
    } else if (activeChart === 'success') {
      series.push(successBar)
    } else if (hasErrorPoints) {
      series.push(errorScatter)
    }

    // Selection overlay while dragging.
    if (refLeft !== null && refRight !== null && series[0]) {
      series[0] = {
        ...series[0],
        markArea: {
          silent: true,
          itemStyle: { color: 'rgba(130,108,245,0.2)' },
          data: [
            [
              { xAxis: Math.min(refLeft, refRight) },
              { xAxis: Math.max(refLeft, refRight) },
            ],
          ],
        },
      }
    }

    return {
      grid: baseGrid({ left: 5, right: 5, top: 5, bottom: 5 }),
      tooltip: brandTooltip({
        headerFormatter: (v) => dayjs(Number(v)).format('MMM D, YYYY HH:mm'),
      }),
      // A `type: 'value'` axis (raw epoch ms) honors `interval` exactly, so
      // ticks land at min + k*interval — evenly spread in pixels. `type: 'time'`
      // ignores a custom interval and snaps to calendar boundaries, which drifts
      // uneven at the pinned max edge.
      xAxis: {
        type: 'value',
        axisLabel: {
          hideOverlap: true,
          formatter: (v: number) => dayjs(v).format(getTickFormat),
        },
        ...xAxisBounds,
      },
      yAxis: valueAxis(undefined, { position: 'left', min: 0 }),
      series: series.length > 0 ? series : [{ type: 'bar', data: [] }],
    }
  }, [chartData, errorPoints, hasErrorPoints, activeChart, refLeft, refRight, getTickFormat, xAxisBounds])

  // Bind low-level drag handlers once the instance exists.
  const handleReady = useCallback((instance: any) => {
    const zr = instance.getZr()
    const tsAt = (offsetX: number): number | null => {
      const v = instance.convertFromPixel({ xAxisIndex: 0 }, offsetX)
      return typeof v === 'number' && Number.isFinite(v) ? v : null
    }

    // Movement under this many pixels is treated as a click, not a drag —
    // real clicks carry a few px of jitter that would otherwise convert to
    // minutes on a multi-day axis and hijack the click-to-drill path.
    const DRAG_PX_THRESHOLD = 4

    zr.on('mousedown', (e: any) => {
      const ts = tsAt(e.offsetX)
      if (ts === null) return
      dragStartRef.current = ts
      dragStartPxRef.current = e.offsetX
      dragLastRef.current = ts
      isDraggingRef.current = false
      setRefLeft(ts)
      setRefRight(null)
    })

    zr.on('mousemove', (e: any) => {
      if (dragStartRef.current === null || dragStartPxRef.current === null) return
      const ts = tsAt(e.offsetX)
      if (ts === null) return
      dragLastRef.current = ts
      if (Math.abs(e.offsetX - dragStartPxRef.current) > DRAG_PX_THRESHOLD) {
        isDraggingRef.current = true
        setRefRight(ts)
      }
    })

    // A release resolves to either a drag-zoom (moved past the threshold) or a
    // click that drills into the single bucket under the pointer. Both are
    // handled here on zrender rather than via ECharts' series `click`, which
    // only fires on the 8px bar graphic and misses clicks next to it.
    const finish = (allowClick: boolean) => {
      const { onZoom: zoom, intervalMs: step } = cfgRef.current
      const dragged = isDraggingRef.current
      const l0 = dragStartRef.current
      const r0 = dragLastRef.current

      dragStartRef.current = null
      dragStartPxRef.current = null
      dragLastRef.current = null
      isDraggingRef.current = false
      setRefLeft(null)
      setRefRight(null)

      if (!zoom) return
      if (dragged) {
        if (l0 !== null && r0 !== null) {
          const [left, right] = [l0, r0].sort((a, b) => a - b)
          if (left !== right) {
            zoom(
              new Date(left).toISOString(),
              new Date(right + step).toISOString(),
            )
          }
        }
      } else if (allowClick && l0 !== null) {
        // Snap the pointer time to its bucket window [start, start + step).
        const bucketStart = Math.floor(l0 / step) * step
        zoom(
          new Date(bucketStart).toISOString(),
          new Date(bucketStart + step).toISOString(),
        )
      }
    }

    zr.on('mouseup', () => finish(true))
    zr.on('globalout', () => finish(false))
  }, [])

  return (
    <div className="w-full bg-brand-main-950 select-none [&_canvas]:cursor-crosshair">
      <div className="py-4 px-2">
        <EChart
          option={option}
          height={150}
          notMerge
          onReady={handleReady}
        />
      </div>
    </div>
  )
}
