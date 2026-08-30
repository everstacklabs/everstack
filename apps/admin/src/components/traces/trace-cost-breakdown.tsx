import { useMemo } from 'react'
import { ui } from '@everstack/ui'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr } from '@/utils/traces-common'
import { cn } from '@everstack/utils/functions/cn'
import { DollarSign } from 'lucide-react'
import { costBar, costBarTop, vizTrack } from './trace-viz'

const { Card, CardHeader, CardTitle, CardContent } = ui

/**
 * Cost attribution for a single trace, as a ranked list of the spans that
 * actually spent money. Replaces the old treemap: for the common case (one
 * generation span = 100%) a treemap is a meaningless coloured slab, and for
 * multi-span traces a ranked bar list is easier to scan than nested
 * rectangles. Cost is one magnitude metric, so bar length carries the signal
 * and colour stays a single restrained accent.
 */

interface Props {
    spans: Span[]
    onSelectSpan?: (spanId: string) => void
}

type CostRow = {
    spanId: string
    name: string
    group: string
    cost: number
}

function spanCost(span: Span): number {
    const total = Number(getAttr(span, 'llm.cost.total') || 0)
    if (total > 0) return total
    return Number(getAttr(span, 'cost.estimated_usd') || 0)
}

function groupKey(span: Span): string {
    return (
        getAttr(span, 'workflow.node') ||
        getAttr(span, 'node') ||
        getAttr(span, 'observation.type') ||
        span.spanName?.split('.')[0] ||
        'other'
    )
}

function shorten(name: string): string {
    if (!name) return ''
    return name.length > 44 ? name.slice(0, 42) + '…' : name
}

export function TraceCostBreakdown({ spans, onSelectSpan }: Props) {
    const { rows, total } = useMemo(() => {
        const list: CostRow[] = spans
            .map((s) => ({
                spanId: s.spanId,
                name: s.spanName,
                group: groupKey(s),
                cost: spanCost(s),
            }))
            .filter((r) => r.cost > 0)
            .sort((a, b) => b.cost - a.cost)
        return { rows: list, total: list.reduce((sum, r) => sum + r.cost, 0) }
    }, [spans])

    if (total === 0) {
        return (
            <Card className="border-brand-main-500 bg-brand-main-900/50">
                <CardHeader className="!pb-2">
                    <CardTitle className="text-brand-main-50 text-sm flex items-center gap-1.5 light:text-brand-main-50">
                        <DollarSign className="h-3.5 w-3.5 text-brand-main-300" />
                        Cost attribution
                    </CardTitle>
                </CardHeader>
                <CardContent className="!pt-0 flex items-center justify-center h-24 text-brand-main-50 text-xs light:text-black">
                    No cost recorded on this trace.
                </CardContent>
            </Card>
        )
    }

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/50">
            <CardHeader className="!pb-2 flex flex-row items-center justify-between">
                <CardTitle className="text-brand-main-50 text-sm flex items-center gap-1.5 light:text-brand-main-50">
                    <DollarSign className="h-3.5 w-3.5 text-brand-main-300" />
                    Cost attribution
                </CardTitle>
                <div className="text-xs text-brand-main-50 light:text-black">
                    Total <span className="text-brand-main-50 light:text-black">${total.toFixed(4)}</span>
                    <span className="ml-2 text-brand-main-50 light:text-black">
                        {rows.length} span{rows.length === 1 ? '' : 's'}
                    </span>
                </div>
            </CardHeader>
            <CardContent className="!pt-0 space-y-1.5">
                {rows.map((row, i) => {
                    const pct = total > 0 ? (row.cost / total) * 100 : 0
                    const interactive = Boolean(onSelectSpan)
                    return (
                        <button
                            key={row.spanId}
                            type="button"
                            disabled={!interactive}
                            onClick={() => onSelectSpan?.(row.spanId)}
                            className={cn(
                                'w-full text-left rounded-md px-2.5 py-2 transition-colors',
                                interactive
                                    ? 'hover:bg-brand-main-700/40 cursor-pointer'
                                    : 'cursor-default',
                            )}
                        >
                            <div className="flex items-baseline justify-between gap-3">
                                <div className="min-w-0 flex items-baseline gap-2">
                                    <span className="text-xs text-zinc-200 truncate">
                                        {shorten(row.name)}
                                    </span>
                                    <span className="text-[10px] text-brand-main-50 shrink-0 light:text-black">
                                        {row.group}
                                    </span>
                                </div>
                                <div className="shrink-0 flex items-baseline gap-2 ">
                                    <span className="text-xs text-zinc-300">
                                        ${row.cost.toFixed(4)}
                                    </span>
                                    <span className="text-[10px] text-brand-main-50 w-10 text-right light:text-black">
                                        {pct.toFixed(1)}%
                                    </span>
                                </div>
                            </div>
                            <div className={cn('mt-1.5 h-1.5 rounded-full overflow-hidden', vizTrack)}>
                                <div
                                    className={cn(
                                        'h-full rounded-full',
                                        i === 0 ? costBarTop : costBar,
                                    )}
                                    style={{ width: `${Math.max(pct, 1)}%` }}
                                />
                            </div>
                        </button>
                    )
                })}
            </CardContent>
        </Card>
    )
}
