type ProtoTimestamp = {
  seconds?: bigint | number
  nanos?: number
  toDate?: () => Date
}

export type MetricPoint = {
  timestamp: number
  value: number
}

export function tsToISO(ts: ProtoTimestamp | undefined): string {
  if (!ts) return ''
  if (typeof ts.toDate === 'function') return ts.toDate().toISOString()
  const seconds =
    typeof ts.seconds === 'bigint'
      ? Number(ts.seconds)
      : Number(ts.seconds ?? 0)
  return new Date(seconds * 1000).toISOString()
}

export function seriesToPoints(
  series:
    | Array<{
        metricName?: string
        buckets?: Array<{ timestamp?: ProtoTimestamp; value?: number }>
      }>
    | undefined,
  metricName: string,
): MetricPoint[] {
  return (
    series
      ?.find((s) => s.metricName === metricName)
      ?.buckets?.map((b) => ({
        timestamp: new Date(tsToISO(b.timestamp)).getTime(),
        value: b.value ?? 0,
      }))
      .sort((a, b) => a.timestamp - b.timestamp) ?? []
  )
}

export function formatCompactNumber(n: number): string {
  if (!Number.isFinite(n)) return '0'
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (Math.abs(n) >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return n.toFixed(0)
}

export function formatUsd(n: number): string {
  if (!Number.isFinite(n)) return '$0.00'
  return `$${n.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

export function formatLatency(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return '0ms'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  return `${ms.toFixed(0)}ms`
}

export function formatPercentRatio(n: number): string {
  if (!Number.isFinite(n)) return '0.00%'
  return `${(n * 100).toFixed(2)}%`
}

export function formatDelta(delta: number | undefined): string {
  if (delta === undefined || !Number.isFinite(delta) || delta === 0)
    return '0.0%'
  const sign = delta > 0 ? '+' : ''
  return `${sign}${(delta * 100).toFixed(1)}%`
}

/**
 * Board series colors, defined once and sourced from brand-system tokens.
 * brand-secondary covers the primary/brand series; semantic rose/amber/emerald
 * carry error/warning/cost meaning. All map to CSS variables that are emitted
 * elsewhere in the bundle, so they resolve at runtime.
 */
// Concrete colors (not CSS vars): recharts sets the SVG stroke attribute,
// which does not resolve var(). Brand purple primary + semantic hues.
export const BOARD_COLORS = {
  requests: '#826cf5',
  errors: '#fb7185',
  errorRate: '#fbbf24',
  cost: '#34d399',
  ttftP50: '#38bdf8',
  ttftP95: '#a78bfa',
} as const

/**
 * Light-theme counterparts (600-weight hues) so series keep contrast on a
 * white canvas. Mirrors the light entries in @everstack/ui's chart theme.
 */
const BOARD_COLORS_LIGHT: Record<keyof typeof BOARD_COLORS, string> = {
  requests: '#6d55e8',
  errors: '#e11d48',
  errorRate: '#d97706',
  cost: '#059669',
  ttftP50: '#0284c7',
  ttftP95: '#7c3aed',
}

/** Board series colors for the given chart mode. */
export function boardColors(
  mode: 'dark' | 'light',
): Record<keyof typeof BOARD_COLORS, string> {
  return mode === 'light' ? BOARD_COLORS_LIGHT : BOARD_COLORS
}

export function windowMsFromGranularity(granularity: string): number {
  if (granularity === 'day') return 24 * 60 * 60 * 1000
  if (granularity === '6hour') return 6 * 60 * 60 * 1000
  return 60 * 60 * 1000
}

export function shiftPreviousSeries(
  points: MetricPoint[],
  currentStart?: string,
  previousStart?: string,
): Array<MetricPoint & { originalTimestamp: number }> {
  if (!currentStart || !previousStart) {
    return points.map((p) => ({ ...p, originalTimestamp: p.timestamp }))
  }
  const offset =
    new Date(currentStart).getTime() - new Date(previousStart).getTime()
  return points.map((p) => ({
    timestamp: p.timestamp + offset,
    value: p.value,
    originalTimestamp: p.timestamp,
  }))
}
