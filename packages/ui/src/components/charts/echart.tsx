/**
 * `EChart` — the one React entry point for charts in Everstack.
 *
 * Wraps the tree-shaken `echarts-for-react/lib/core` binding, registers the
 * branded `everstack-dark` theme, and adds the platform conveniences every
 * dashboard needs: autosizing, empty + loading states, synced-hover groups,
 * a typed event surface, a drag-to-zoom convenience callback, and PNG export.
 *
 * Call sites build a plain ECharts `option` (data only — styling comes from the
 * theme + `brandTooltip`) and hand it here. They never import echarts directly.
 */
import * as React from 'react'
import ReactEChartsCore from 'echarts-for-react/lib/core.js'
import { useTheme } from 'next-themes'
import { cn } from '../../lib/utils.js'
import { echarts } from './core.js'
import { chartMode, ensureTheme, type ChartMode } from './theme.js'

/**
 * The current chart mode, reactive to theme toggles. Include this in the deps
 * of any option `useMemo` that bakes in `AXIS`/`SEMANTIC`/`BRAND_PALETTE`
 * colours so the option rebuilds when the theme flips.
 */
export function useChartMode(): ChartMode {
    const { resolvedTheme } = useTheme()
    // resolvedTheme is undefined until next-themes mounts; the html attribute
    // is already set pre-paint by the index.html bootstrap script.
    return resolvedTheme === 'light' || resolvedTheme === 'dark'
        ? resolvedTheme
        : chartMode()
}

export type EChartsInstance = ReturnType<typeof echarts.init>

export interface ZoomRange {
    /** Inclusive start value of the selected x range (axis units, e.g. epoch ms). */
    startValue: number
    /** Inclusive end value of the selected x range. */
    endValue: number
}

/**
 * ECharts leaves event-callback parameters untyped at its public boundary
 * because the payload differs per event and per series type. Narrow it to the
 * fields handlers commonly read, with an index signature for the rest, so
 * consumers do not have to reach for `any`.
 */
export type EChartsEventParams = {
    value?: number | string | (number | string)[]
    data?: number | string | (number | string)[] | Record<string, unknown>
    seriesName?: string
    name?: string
    dataIndex?: number
    [key: string]: unknown
}

export interface EChartProps {
    /** Plain ECharts option. Data only — theme supplies styling. */
    option: Record<string, unknown>
    /** Height in px or any CSS length. Width always fills the parent. */
    height?: number | string
    className?: string
    /** Show the empty placeholder instead of the chart. */
    empty?: boolean
    emptyText?: string
    /** Show ECharts' built-in loading spinner over the chart. */
    loading?: boolean
    /**
     * Synced-hover group. All charts sharing a group string get connected
     * crosshairs + tooltips (replaces recharts' `syncId`).
     */
    group?: string
    /** Replace the option wholesale instead of merging (use for streaming resets). */
    notMerge?: boolean
    /** Raw ECharts events, e.g. `{ click: (p) => ... }`. */
    onEvents?: Record<string, (params: EChartsEventParams, instance: EChartsInstance) => void>
    /**
     * Convenience for drag-to-zoom. Fires with the selected x range whenever a
     * `dataZoom` brush settles. Requires a `dataZoom` entry in `option` (use
     * `selectZoom()` from presets for the brush-select variant).
     */
    onZoom?: (range: ZoomRange) => void
    /** Imperative access to the underlying instance (for export, etc). */
    instanceRef?: React.MutableRefObject<EChartsInstance | null>
    /**
     * Called once with the instance when the chart is ready. Use for low-level
     * wiring such as zrender drag handlers (see traces-chart drag-to-zoom).
     */
    onReady?: (instance: EChartsInstance) => void
}

/** Trigger a PNG download of a chart instance. */
export function exportChartPng(
    instance: EChartsInstance | null,
    filename = 'chart.png',
): void {
    if (!instance) return
    const url = instance.getDataURL({
        type: 'png',
        pixelRatio: 2,
        backgroundColor: 'transparent',
    })
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
}

export function EChart({
    option,
    height = 200,
    className,
    empty = false,
    emptyText = 'No data',
    loading = false,
    group,
    notMerge = false,
    onEvents,
    onZoom,
    instanceRef,
    onReady,
}: EChartProps) {
    const mode = useChartMode()
    const theme = React.useMemo(() => ensureTheme(mode), [mode])
    const localRef = React.useRef<EChartsInstance | null>(null)

    // `unknown` here sidesteps a private-property clash between echarts/core's
    // instance type and echarts-for-react's bundled echarts types; both expose
    // the same public surface we use (group, getOption, getDataURL).
    const handleReady = React.useCallback(
        (raw: unknown) => {
            const instance = raw as EChartsInstance
            localRef.current = instance
            if (instanceRef) instanceRef.current = instance
            if (group) {
                instance.group = group
                echarts.connect(group)
            }
            onReady?.(instance)
        },
        [group, instanceRef, onReady],
    )

    // Map a settled dataZoom into the friendlier ZoomRange callback.
    const mergedEvents = React.useMemo(() => {
        const ev = { ...(onEvents ?? {}) }
        if (onZoom) {
            ev.datazoom = (_params: EChartsEventParams, instance: EChartsInstance) => {
                const opt = instance.getOption() as {
                    dataZoom?: { startValue?: unknown; endValue?: unknown }[]
                }
                const dz = opt?.dataZoom?.[0]
                if (!dz) return
                const { startValue, endValue } = dz
                if (typeof startValue === 'number' && typeof endValue === 'number') {
                    onZoom({ startValue, endValue })
                }
            }
        }
        return ev
    }, [onEvents, onZoom])

    const style = React.useMemo(
        () => ({ height: typeof height === 'number' ? `${height}px` : height, width: '100%' }),
        [height],
    )

    if (empty) {
        return (
            <div
                className={cn(
                    'flex items-center justify-center text-xs text-brand-main-200/60',
                    className,
                )}
                style={style}
            >
                {emptyText}
            </div>
        )
    }

    return (
        <div className={cn('w-full', className)} style={style}>
            <ReactEChartsCore
                echarts={echarts}
                option={option}
                theme={theme}
                notMerge={notMerge}
                lazyUpdate
                showLoading={loading}
                onChartReady={handleReady}
                onEvents={mergedEvents}
                style={{ height: '100%', width: '100%' }}
            />
        </div>
    )
}
