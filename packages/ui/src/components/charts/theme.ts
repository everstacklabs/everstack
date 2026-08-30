/**
 * The Everstack ECharts themes.
 *
 * Canvas (which ECharts renders to) cannot read `var(--x)`, so both themes are
 * built from static colour tables that mirror the authored brand tokens in
 * `@everstack/tailwind-config/shared-styles.css` (dark defaults + the
 * `[data-theme="light"]` remap). `ensureTheme(mode)` registers both once and
 * returns the name for the requested mode; the `EChart` wrapper picks the mode
 * from the html[data-theme] attribute and re-registers nothing on toggle.
 *
 * This is the single source of truth for chart colour, axis, grid, tick and
 * tooltip styling. Call sites pass *data*, never styling. `AXIS`, `SEMANTIC`
 * and `BRAND_PALETTE` resolve against the *current* theme at read time, so
 * options built during render stay correct as long as the component re-renders
 * on theme change (see `useChartMode` in the EChart wrapper).
 */
import { echarts } from './core.js'

export type ChartMode = 'dark' | 'light'

export const EVERSTACK_THEME = 'everstack-dark'
export const EVERSTACK_THEME_LIGHT = 'everstack-light'

/** Current chart mode, read from the html[data-theme] attribute. */
export function chartMode(): ChartMode {
    if (typeof document !== 'undefined') {
        if (document.documentElement.getAttribute('data-theme') === 'light')
            return 'light'
    }
    return 'dark'
}

/**
 * Brand-leaning multi-series palette. Series carry meaning by label first;
 * colour is a secondary cue. Mirrors the muted, distinct hues used across the
 * trace-detail viz (`trace-viz.ts`) so nothing reads like a rainbow. Index 0
 * is the brand accent. The light set uses the 500/600-weight hues so series
 * hold contrast against a white canvas.
 */
const PALETTES: Record<ChartMode, readonly string[]> = {
    dark: [
        '#826cf5', // brand purple (accent)
        '#22d3ee', // cyan
        '#34d399', // emerald
        '#fbbf24', // amber
        '#fb7185', // rose
        '#a78bfa', // violet
        '#60a5fa', // blue
        '#e879f9', // fuchsia
    ],
    light: [
        '#6d55e8', // brand purple (accent)
        '#0891b2', // cyan
        '#059669', // emerald
        '#d97706', // amber
        '#e11d48', // rose
        '#7c3aed', // violet
        '#2563eb', // blue
        '#c026d3', // fuchsia
    ],
}

const SEMANTICS: Record<
    ChartMode,
    { success: string; error: string; warning: string; info: string; neutral: string }
> = {
    dark: {
        success: '#34d399',
        error: '#fb7185',
        warning: '#fbbf24',
        info: '#826cf5',
        neutral: 'rgba(255,255,255,0.45)',
    },
    light: {
        success: '#059669',
        error: '#e11d48',
        warning: '#d97706',
        info: '#6d55e8',
        neutral: 'rgba(0,0,0,0.45)',
    },
}

const FONTS = {
    fontFamily:
        'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
    monoFamily:
        'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
} as const

/** Low-opacity structural colours per mode, matched to the old recharts styling. */
const AXES: Record<
    ChartMode,
    { grid: string; axisLine: string; label: string; crosshair: string; title: string; legend: string; legendInactive: string }
> = {
    dark: {
        grid: 'rgba(255,255,255,0.06)',
        axisLine: 'rgba(255,255,255,0.1)',
        label: 'rgba(255,255,255,0.4)',
        crosshair: 'rgba(255,255,255,0.25)',
        title: 'rgba(255,255,255,0.85)',
        legend: 'rgba(255,255,255,0.7)',
        legendInactive: 'rgba(255,255,255,0.25)',
    },
    light: {
        grid: 'rgba(0,0,0,0.06)',
        axisLine: 'rgba(0,0,0,0.12)',
        label: 'rgba(0,0,0,0.45)',
        crosshair: 'rgba(0,0,0,0.3)',
        title: 'rgba(0,0,0,0.85)',
        legend: 'rgba(0,0,0,0.65)',
        legendInactive: 'rgba(0,0,0,0.25)',
    },
}

/** Tooltip panel colours, mirrored from the brand-main ramp per theme. */
const PANELS: Record<ChartMode, { bg: string; border: string; header: string; name: string; value: string; shadow: string }> = {
    dark: {
        bg: 'oklch(0.1823 0.0107 268.1014 / 0.96)', // brand-main-900/96
        border: 'oklch(0.254 0.0253 271.2464)', // brand-main-600
        header: 'rgba(255,255,255,0.5)',
        name: 'rgba(255,255,255,0.6)',
        value: '#fff',
        shadow: '0 8px 24px rgba(0,0,0,0.4)',
    },
    light: {
        bg: 'oklch(0.9945 0.0012 274 / 0.97)', // near-white panel
        border: 'oklch(0.8853 0.0087 269)', // light brand-main-700 (border gray)
        header: 'rgba(0,0,0,0.5)',
        name: 'rgba(0,0,0,0.6)',
        value: 'oklch(0.216 0.0132 274)',
        shadow: '0 8px 24px rgba(0,0,0,0.12)',
    },
}

/** @internal Tooltip colours for the current theme (read at hover time). */
export function panelColors() {
    return PANELS[chartMode()]
}

/** The multi-series palette for the current (or given) theme. */
export function brandPalette(mode: ChartMode = chartMode()): readonly string[] {
    return PALETTES[mode]
}

/** Semantic colours (error/success/etc) for the current (or given) theme. */
export function semantic(mode: ChartMode = chartMode()) {
    return SEMANTICS[mode]
}

/**
 * Live view of the current theme's palette. Reads resolve against
 * html[data-theme] at access time so existing `BRAND_PALETTE[0]` call sites
 * stay theme-correct without churn.
 */
export const BRAND_PALETTE: readonly string[] = new Proxy(
    PALETTES.dark as string[],
    {
        get(_target, prop) {
            const cur = PALETTES[chartMode()] as string[]
            const v = Reflect.get(cur, prop)
            return typeof v === 'function' ? v.bind(cur) : v
        },
    },
)

/** Semantic colours that mean something (error/success/etc) — live view. */
export const SEMANTIC: (typeof SEMANTICS)['dark'] = {
    get success() { return SEMANTICS[chartMode()].success },
    get error() { return SEMANTICS[chartMode()].error },
    get warning() { return SEMANTICS[chartMode()].warning },
    get info() { return SEMANTICS[chartMode()].info },
    get neutral() { return SEMANTICS[chartMode()].neutral },
}

/** Shared structural constants — live view of the current theme. */
export const AXIS = {
    get grid() { return AXES[chartMode()].grid },
    get axisLine() { return AXES[chartMode()].axisLine },
    get label() { return AXES[chartMode()].label },
    get crosshair() { return AXES[chartMode()].crosshair },
    fontFamily: FONTS.fontFamily,
    monoFamily: FONTS.monoFamily,
} as const

let registered = false

function buildTheme(mode: ChartMode): Record<string, unknown> {
    const axes = AXES[mode]
    const accent = mode === 'dark' ? PALETTES.dark[0] : PALETTES.light[0]

    const axisCommon = {
        axisLine: { show: true, lineStyle: { color: axes.axisLine } },
        axisTick: { show: false },
        axisLabel: {
            color: axes.label,
            fontSize: 11,
            fontFamily: FONTS.fontFamily,
        },
        splitLine: { show: true, lineStyle: { color: axes.grid, type: 'dashed' } },
    }

    return {
        color: [...PALETTES[mode]],
        backgroundColor: 'transparent',
        textStyle: { fontFamily: FONTS.fontFamily, color: axes.label },
        title: {
            textStyle: { color: axes.title, fontSize: 12 },
        },
        grid: {
            left: 8,
            right: 8,
            top: 16,
            bottom: 8,
            containLabel: true,
            borderColor: axes.grid,
        },
        categoryAxis: { ...axisCommon, splitLine: { show: false } },
        valueAxis: { ...axisCommon, axisLine: { show: false } },
        timeAxis: axisCommon,
        logAxis: axisCommon,
        legend: {
            textStyle: { color: axes.legend, fontSize: 11 },
            inactiveColor: axes.legendInactive,
            icon: 'circle',
            itemWidth: 8,
            itemHeight: 8,
        },
        line: {
            symbol: 'none',
            smooth: false,
            lineStyle: { width: 1.75 },
        },
        bar: {
            itemStyle: { borderRadius: [2, 2, 0, 0] },
        },
        scatter: { symbolSize: 7 },
        dataZoom: [
            {
                fillerColor:
                    mode === 'dark'
                        ? 'rgba(130,108,245,0.15)'
                        : 'rgba(109,85,232,0.12)',
                borderColor: 'transparent',
                handleStyle: { color: accent },
                moveHandleStyle: { color: accent },
                dataBackground: {
                    lineStyle: {
                        color: mode === 'dark' ? 'rgba(255,255,255,0.12)' : 'rgba(0,0,0,0.12)',
                    },
                    areaStyle: {
                        color: mode === 'dark' ? 'rgba(255,255,255,0.04)' : 'rgba(0,0,0,0.04)',
                    },
                },
                textStyle: { color: axes.label },
            },
        ],
    }
}

/**
 * Idempotently register both branded themes. Called by the EChart wrapper on
 * first mount. Returns the theme name for the requested (default: current)
 * mode. Safe to call repeatedly.
 */
export function ensureTheme(mode: ChartMode = chartMode()): string {
    if (!registered) {
        echarts.registerTheme(EVERSTACK_THEME, buildTheme('dark'))
        echarts.registerTheme(EVERSTACK_THEME_LIGHT, buildTheme('light'))
        registered = true
    }
    return mode === 'light' ? EVERSTACK_THEME_LIGHT : EVERSTACK_THEME
}
