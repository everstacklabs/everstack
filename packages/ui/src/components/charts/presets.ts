/**
 * Small option builders so call sites stay terse and consistent. These return
 * partial ECharts option fragments tuned for the branded theme; spread them in
 * and override per chart as needed. Styling defaults come from the theme — these
 * only set structural defaults (axis type, padding, gradients).
 */
import { echarts } from './core.js'
import type { BucketTick } from './buckets.js'

/** Compact grid that leaves room for right-aligned value axes. */
export function baseGrid(over: Record<string, unknown> = {}) {
    return { left: 8, right: 12, top: 16, bottom: 8, containLabel: true, ...over }
}

/** Time x-axis (epoch ms values). `formatter` renders the tick labels. */
export function timeAxis(
    formatter: (value: number) => string,
    over: Record<string, unknown> = {},
) {
    return {
        type: 'time',
        boundaryGap: false,
        axisLabel: { hideOverlap: true, formatter: (v: number) => formatter(v) },
        ...over,
    }
}

/** Category x-axis from pre-formatted labels. */
export function categoryAxis(
    data: (string | number)[],
    over: Record<string, unknown> = {},
) {
    return { type: 'category', data, boundaryGap: true, ...over }
}

/** Value y-axis, right-aligned by default to match the dashboards. */
export function valueAxis(
    formatter?: (value: number) => string,
    over: Record<string, unknown> = {},
) {
    return {
        type: 'value',
        position: 'right',
        ...(formatter ? { axisLabel: { formatter: (v: number) => formatter(v) } } : {}),
        ...over,
    }
}

/**
 * x-axis for fixed-width time buckets (see `bucketTicks`). A value axis over
 * epoch ms, pinned to [first start, last start + width], with ticks and
 * labels exactly where `ticks` says, so they sit on bucket starts instead of
 * wherever ECharts' auto interval lands. Plot bars at `start + width / 2`.
 */
export function bucketAxis(
    starts: readonly number[],
    bucketMs: number,
    ticks: readonly BucketTick[],
    over: Record<string, unknown> = {},
) {
    const first = starts[0] ?? 0
    const last = starts[starts.length - 1] ?? first
    const labels = new Map(ticks.map((t) => [t.value, t.label]))
    const values = ticks.map((t) => t.value)
    return {
        type: 'value',
        min: first,
        max: last + bucketMs,
        splitLine: { show: false },
        axisTick: { show: true, customValues: values, length: 3 },
        axisLabel: {
            customValues: values,
            hideOverlap: true,
            formatter: (v: number) => labels.get(v) ?? '',
        },
        ...over,
    }
}

/**
 * A compact sparkline in two layers: a faint dashed bridge across every value
 * (`connectNulls: true`) so the trend reads as one line, and the solid line
 * on top with gaps where a bucket has no value (`connectNulls: false`). A
 * no-data stretch is therefore a faint dashed span, never a zero, and a lone
 * value sits on the bridge instead of floating. No symbols except the hover
 * dot. Pair with `brandTooltip` (set `nullText` to say what a gap means). x
 * values are category keys, typically bucket starts, which the tooltip
 * receives as the axis value.
 */
export function sparkline(
    values: readonly (number | null)[],
    opts: { name: string; color: string; categories?: readonly (string | number)[]; min?: number },
) {
    const { name, color, min = 0 } = opts
    const series: Record<string, unknown>[] = [
        {
            id: SPARKLINE_BRIDGE,
            type: 'line',
            data: [...values],
            connectNulls: true,
            symbol: 'none',
            silent: true,
            tooltip: { show: false },
            lineStyle: { width: 1, type: [2, 3], color: withAlpha(color, 0.4) },
            emphasis: { disabled: true },
            animation: false,
            z: 1,
        },
        {
            name,
            type: 'line',
            data: [...values],
            connectNulls: false,
            symbol: 'circle',
            symbolSize: 5,
            showSymbol: false,
            lineStyle: { width: 1.5, color },
            itemStyle: { color },
            emphasis: { scale: false, lineStyle: { width: 1.5 } },
            animation: false,
            z: 2,
        },
    ]
    return {
        grid: { left: 3, right: 3, top: 4, bottom: 3, containLabel: false },
        xAxis: {
            type: 'category',
            data: opts.categories ? [...opts.categories] : values.map((_, i) => i),
            boundaryGap: false,
            show: false,
        },
        yAxis: { type: 'value', show: false, min },
        series,
    }
}

/**
 * Tooltip `position` for charts too small to hold their tooltip (sparklines):
 * centred on the pointer, below the chart so it never covers the card's
 * title and value above it (above only when there is no room below), and
 * kept inside the viewport. `confine` would squeeze it into the chart itself.
 * Use with `brandTooltip`, which appends the tooltip to the body.
 */
export function viewportTooltipPosition(getContainer: () => Element | null, gap = 8) {
    return (
        point: number[],
        _params: unknown,
        _dom: unknown,
        _rect: unknown,
        size: { contentSize: number[] },
    ): number[] => {
        const [w = 0, h = 0] = size.contentSize
        let x = (point[0] ?? 0) - w / 2
        const rect = getContainer()?.getBoundingClientRect()
        let y = (rect?.height ?? 0) + gap
        if (rect && typeof window !== 'undefined') {
            x = Math.max(gap - rect.left, Math.min(window.innerWidth - gap - w - rect.left, x))
            if (rect.top + y + h > window.innerHeight - gap) y = -h - gap
        }
        return [x, y]
    }
}

/** Series id of the dashed bridge `sparkline` draws; tooltips skip it. */
export const SPARKLINE_BRIDGE = 'sparkline-bridge'

/**
 * Vertical fade for area series (bright at the line, transparent at the base) —
 * the gradient the recharts areas used. Pass the series colour.
 */
export function areaGradient(
    color: string,
    topOpacity = 0.35,
    bottomOpacity = 0,
): { color: unknown } {
    return {
        color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: withAlpha(color, topOpacity) },
            { offset: 1, color: withAlpha(color, bottomOpacity) },
        ]),
    }
}

/**
 * Wheel/drag pan-zoom on the x-axis, emitting `datazoom` events the EChart
 * wrapper forwards to `onZoom`. For box-select-to-zoom, charts wire the `brush`
 * component directly (see traces-chart).
 */
export function insideZoom(over: Record<string, unknown> = {}) {
    return { type: 'inside', xAxisIndex: 0, filterMode: 'none', ...over }
}

/** Apply an alpha to a hex (`#rgb`/`#rrggbb`) or pass through other formats. */
export function withAlpha(color: string, alpha: number): string {
    const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color)
    if (!m?.[1]) return color
    let hex = m[1]
    if (hex.length === 3)
        hex = hex
            .split('')
            .map((c) => c + c)
            .join('')
    const r = parseInt(hex.slice(0, 2), 16)
    const g = parseInt(hex.slice(2, 4), 16)
    const b = parseInt(hex.slice(4, 6), 16)
    return `rgba(${r},${g},${b},${alpha})`
}
