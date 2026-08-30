/**
 * Small option builders so call sites stay terse and consistent. These return
 * partial ECharts option fragments tuned for the branded theme; spread them in
 * and override per chart as needed. Styling defaults come from the theme — these
 * only set structural defaults (axis type, padding, gradients).
 */
import { echarts } from './core.js'

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
