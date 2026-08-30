/**
 * Everstack branded ECharts core. Apps import from here, never from `echarts`.
 *
 *   import { EChart, brandTooltip, timeAxis, BRAND_PALETTE } from '@everstack/ui'
 */
export { EChart, exportChartPng, useChartMode } from './echart.js'
export type { EChartProps, EChartsInstance, ZoomRange } from './echart.js'
export { brandTooltip } from './tooltip.js'
export type { BrandTooltipOptions } from './tooltip.js'
export {
    BRAND_PALETTE,
    SEMANTIC,
    AXIS,
    EVERSTACK_THEME,
    EVERSTACK_THEME_LIGHT,
    brandPalette,
    semantic,
    chartMode,
    ensureTheme,
} from './theme.js'
export type { ChartMode } from './theme.js'
export {
    baseGrid,
    timeAxis,
    categoryAxis,
    valueAxis,
    areaGradient,
    insideZoom,
    withAlpha,
} from './presets.js'
export { echarts } from './core.js'
