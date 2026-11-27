/**
 * Branded tooltip for ECharts.
 *
 * Reproduces the custom recharts tooltip used across the dashboards: a rounded
 * `brand-main-600` border over a `brand-main-900/95` panel, a muted time/header
 * row, then one row per series with a colour swatch, the series name, and a
 * right-aligned mono value. ECharts tooltips render to a DOM node inside the
 * chart container, so we build the markup with inline styles to stay faithful
 * regardless of which app consumes the UI package. Panel colours resolve at
 * hover time via `panelColors()` so the tooltip tracks theme toggles even when
 * the surrounding option object is memoized.
 */
import { AXIS, panelColors } from './theme.js'
import { SPARKLINE_BRIDGE } from './presets.js'

/**
 * The subset of an ECharts tooltip callback parameter this module reads.
 * ECharts leaves the callback argument untyped at its public boundary because
 * the shape varies per series type, so narrow it to the fields we touch.
 */
export interface EChartsTooltipParam {
    value?: number | string | (number | string)[]
    seriesName?: string
    color?: string
    name?: string
    axisValue?: number | string
    axisValueLabel?: string
    data?: unknown
    seriesId?: string
    seriesType?: string
}

/** One series datum, normalised across `axis` and `item` triggers. */
interface TipRow {
    name: string
    color: string
    value: number
    /** The datum was null/absent (a gap), not a number. */
    missing: boolean
}

export interface BrandTooltipOptions {
    /** `axis` (shared crosshair, multi-series) or `item` (single point). */
    trigger?: 'axis' | 'item'
    /** Format the header (x value). Receives the raw axis value. */
    headerFormatter?: (axisValue: number | string) => string
    /**
     * Format each row value. Receives the numeric value, the series name, and
     * the raw ECharts param (use `param.data` to reach a richer datum object).
     */
    valueFormatter?: (value: number, name: string, param: EChartsTooltipParam) => string
    /** Hide rows whose value is null/0 (e.g. empty buckets). */
    hideZero?: boolean
    /**
     * Text for a row whose datum is null (a gap, e.g. a bucket with no
     * requests), instead of formatting it as 0. Such rows are kept even with
     * `hideZero`.
     */
    nullText?: string
    /**
     * Shown under the header when every row was hidden (e.g. an empty bucket
     * with `hideZero`), instead of showing no tooltip at all.
     */
    emptyText?: string
}

function esc(s: string): string {
    return String(s).replace(
        /[&<>"]/g,
        (c) =>
            ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c] ?? c,
    )
}

function rawValue(p: EChartsTooltipParam): unknown {
    return Array.isArray(p?.value) ? p.value[p.value.length - 1] : p?.value
}

function isMissing(v: unknown): boolean {
    return v === null || v === undefined || v === '-' || (typeof v === 'number' && Number.isNaN(v))
}

function rowValue(p: EChartsTooltipParam): number {
    const v = rawValue(p)
    return typeof v === 'number' ? v : Number(v ?? 0)
}

/**
 * Build a branded `tooltip` option block. Spread the result into your chart
 * option's `tooltip`. Override any field afterwards if a chart needs it.
 */
export function brandTooltip(opts: BrandTooltipOptions = {}): Record<string, unknown> {
    const {
        trigger = 'axis',
        headerFormatter,
        valueFormatter = (v) => String(v),
        hideZero = false,
        nullText,
        emptyText,
    } = opts

    return {
        trigger,
        appendToBody: true,
        backgroundColor: 'transparent',
        borderWidth: 0,
        padding: 0,
        extraCssText: 'box-shadow:none;border:0;',
        axisPointer: {
            type: trigger === 'axis' ? 'line' : 'none',
            lineStyle: { color: AXIS.crosshair, width: 1 },
        },
        formatter: (params: EChartsTooltipParam | EChartsTooltipParam[]) => {
            const list: EChartsTooltipParam[] = (Array.isArray(params) ? params : [params]).filter(
                (p) => p.seriesId !== SPARKLINE_BRIDGE,
            )
            if (list.length === 0) return ''

            const panel = panelColors()

            const headerRaw = list[0]?.axisValueLabel ?? list[0]?.axisValue ?? list[0]?.name
            const header = headerFormatter
                ? headerFormatter(list[0]?.axisValue ?? headerRaw ?? '')
                : esc(String(headerRaw ?? ''))

            const rows: (TipRow & { param: EChartsTooltipParam })[] = list
                .map((p) => ({
                    name: p.seriesName ?? '',
                    color: p.color ?? AXIS.label,
                    value: rowValue(p),
                    // Without nullText a gap reads as 0, as it always has.
                    missing: nullText !== undefined && isMissing(rawValue(p)),
                    param: p,
                }))
                .filter((r) => r.missing || !hideZero || r.value !== 0)

            const frame = (inner: string) => `
        <div style="border-radius:8px;border:1px solid ${panel.border};background:${panel.bg};backdrop-filter:blur(4px);padding:10px 12px;box-shadow:${panel.shadow}">
          ${header ? `<div style="font-size:11px;color:${panel.header};margin-bottom:6px">${header}</div>` : ''}
          ${inner}
        </div>`

            if (rows.length === 0) {
                if (emptyText === undefined) return ''
                return frame(
                    `<div style="font-size:12px;line-height:1.4;color:${panel.name}">${esc(emptyText)}</div>`,
                )
            }

            const body = rows
                .map(
                    (r) => `
            <div style="display:flex;align-items:center;justify-content:space-between;gap:16px;font-size:12px;line-height:1.4">
              <span style="display:flex;align-items:center;gap:6px;color:${panel.name}">
                <span style="display:inline-block;width:8px;height:8px;border-radius:9999px;background:${r.color}"></span>
                ${esc(r.name)}
              </span>
              <span style="font-family:${AXIS.monoFamily};color:${panel.value}">${esc(r.missing ? (nullText ?? '') : valueFormatter(r.value, r.name, r.param))}</span>
            </div>`,
                )
                .join('')

            return frame(`<div style="display:flex;flex-direction:column;gap:2px">${body}</div>`)
        },
    }
}
