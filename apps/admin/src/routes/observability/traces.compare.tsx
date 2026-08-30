import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { ArrowLeftRight, ArrowRight, ArrowLeft, ArrowDown, Loader2, GitCompare } from 'lucide-react'
import { getTrace, getTraceByID, getTraceScores } from '@/server/traces'
import { formatCost, formatDuration, formatTokens, safeBigIntToNumber } from '@/utils/trace-formatters'
import { getAttr } from '@/utils/traces-common'
import { diffWords, diffStats, type DiffOp } from '@/lib/diff'
import { cn } from '@everstack/utils/functions/cn'

const { Card, CardContent, CardHeader, CardTitle, Badge, Button } = ui

const compareSearchSchema = z.object({
    a: z.string().optional(),
    b: z.string().optional(),
})

export const Route = createFileRoute('/observability/traces/compare')({
    component: TraceCompare,
    validateSearch: compareSearchSchema,
})

function TraceCompare() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()

    const traceAId = search.a ?? ''
    const traceBId = search.b ?? ''

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            <div className="shrink-0 px-4 py-2 border-b border-brand-main-700 flex items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                    <GitCompare className="h-4 w-4 text-brand-secondary-400" />
                    <span className="text-sm font-medium text-white light:text-brand-main-50">Compare traces</span>
                </div>
                <div className="flex items-center gap-2 text-xs">
                    <input
                        type="text"
                        placeholder="Trace A id"
                        value={traceAId}
                        onChange={(e) => navigate({ search: (p) => ({ ...p, a: e.target.value || undefined }), replace: true })}
                        className="w-64 bg-brand-main-700/60 text-zinc-200 light:text-zinc-800 rounded px-2 py-1 border border-brand-main-500 focus:border-brand-secondary-500 outline-none font-mono"
                    />
                    <Button
                        variant="ghost"
                        size="icon"
                        title="Swap"
                        onClick={() =>
                            navigate({
                                search: () => ({ a: traceBId || undefined, b: traceAId || undefined }),
                                replace: true,
                            })
                        }
                    >
                        <ArrowLeftRight className="h-3.5 w-3.5 text-white/60 light:text-black/60" />
                    </Button>
                    <input
                        type="text"
                        placeholder="Trace B id"
                        value={traceBId}
                        onChange={(e) => navigate({ search: (p) => ({ ...p, b: e.target.value || undefined }), replace: true })}
                        className="w-64 bg-brand-main-700/60 text-zinc-200 light:text-zinc-800 rounded px-2 py-1 border border-brand-main-500 focus:border-brand-secondary-500 outline-none font-mono"
                    />
                </div>
            </div>

            {(!traceAId || !traceBId) ? (
                <div className="flex-1 flex flex-col items-center justify-center text-white/40 light:text-black/40 gap-2">
                    <GitCompare className="size-8 opacity-30" />
                    <div className="text-sm">Enter two trace IDs to compare</div>
                    <div className="text-xs text-white/30 light:text-black/30">Output diff · score deltas · latency/cost/token deltas</div>
                </div>
            ) : (
                <ComparePanels traceAId={traceAId} traceBId={traceBId} />
            )}
        </div>
    )
}

function ComparePanels({ traceAId, traceBId }: { traceAId: string; traceBId: string }) {
    return (
        <div className="flex-1 min-h-0 grid grid-cols-2 gap-3 p-3 overflow-auto">
            <TracePane label="A" traceId={traceAId} accent="text-brand-secondary-300" />
            <TracePane label="B" traceId={traceBId} accent="text-amber-300 light:text-amber-700" />
            <div className="col-span-2">
                <DeltasBar traceAId={traceAId} traceBId={traceBId} />
            </div>
            <div className="col-span-2">
                <OutputDiff traceAId={traceAId} traceBId={traceBId} />
            </div>
        </div>
    )
}

function useTraceBundle(traceId: string) {
    const trace = useQuery({
        queryKey: ['compare-trace', traceId],
        queryFn: () => getTrace(traceId),
        enabled: !!traceId,
    })
    const spans = useQuery({
        queryKey: ['compare-spans', traceId],
        queryFn: () => getTraceByID(traceId),
        enabled: !!traceId,
    })
    const scores = useQuery({
        queryKey: ['compare-scores', traceId],
        queryFn: () => getTraceScores(traceId),
        enabled: !!traceId,
    })
    return { trace: trace.data ?? null, spans: spans.data ?? [], scores: scores.data ?? [], isLoading: trace.isLoading || spans.isLoading }
}

function TracePane({ label, traceId, accent }: { label: string; traceId: string; accent: string }) {
    const { trace, spans, scores, isLoading } = useTraceBundle(traceId)

    if (isLoading) {
        return (
            <Card className="border-brand-main-500 bg-brand-main-900/40">
                <CardContent className="p-6 flex items-center justify-center text-white/40 light:text-black/40 text-xs">
                    <Loader2 className="size-4 animate-spin mr-2" /> Loading trace {label}…
                </CardContent>
            </Card>
        )
    }
    if (!trace) {
        return (
            <Card className="border-brand-main-500 bg-brand-main-900/40">
                <CardContent className="p-6 text-white/40 light:text-black/40 text-xs">Trace {label} not found</CardContent>
            </Card>
        )
    }

    const rootSpan = spans.find((s) => !s.parentSpanId || !spans.find((p) => p.spanId === s.parentSpanId)) ?? spans[0]
    const model = rootSpan ? getAttr(rootSpan, 'llm.model') || trace.llmModel : trace.llmModel
    const provider = rootSpan ? getAttr(rootSpan, 'llm.provider') || trace.provider : trace.provider

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-2 flex flex-row items-center justify-between">
                <CardTitle className={cn('text-sm font-mono', accent)}>
                    {label}: {trace.traceId.slice(0, 12)}…
                </CardTitle>
                <Badge variant="outline" className="text-[10px] text-white/60 light:text-black/60 border-brand-main-500">
                    {trace.status}
                </Badge>
            </CardHeader>
            <CardContent className="space-y-1.5 text-xs">
                <Row k="Model" v={model || '—'} />
                <Row k="Provider" v={provider || '—'} />
                <Row k="Spans" v={String(trace.spanCount)} />
                <Row k="Duration" v={formatDuration(Number(trace.totalDuration))} />
                <Row k="Cost" v={formatCost(trace.totalCost)} />
                {trace.tokenBreakdown && (
                    <Row k="Tokens" v={formatTokens(Number(trace.tokenBreakdown.totalTokens))} />
                )}
                {scores.length > 0 && (
                    <div className="pt-1.5 border-t border-brand-main-700/60">
                        <div className="text-[10px] text-white/40 light:text-black/40 mb-1">Scores</div>
                        <div className="flex flex-wrap gap-1">
                            {scores.map((s) => (
                                <Badge
                                    key={s.id}
                                    variant="outline"
                                    className="text-[10px] text-white/70 light:text-black/70 border-brand-main-500"
                                >
                                    {s.name}: {scoreDisplay(s)}
                                </Badge>
                            ))}
                        </div>
                    </div>
                )}
            </CardContent>
        </Card>
    )
}

function Row({ k, v }: { k: string; v: string }) {
    return (
        <div className="flex items-center justify-between">
            <span className="text-white/40 light:text-black/40">{k}</span>
            <span className="text-white/90 light:text-black/90 font-mono">{v}</span>
        </div>
    )
}

function scoreDisplay(s: { dataType: string; numericValue?: number; stringValue?: string; booleanValue?: boolean }): string {
    if (s.dataType === 'NUMERIC' && s.numericValue !== undefined) return s.numericValue.toFixed(2)
    if (s.dataType === 'CATEGORICAL' && s.stringValue) return s.stringValue
    if (s.dataType === 'BOOLEAN' && s.booleanValue !== undefined) return s.booleanValue ? '✓' : '✗'
    return '—'
}

function DeltasBar({ traceAId, traceBId }: { traceAId: string; traceBId: string }) {
    const a = useTraceBundle(traceAId)
    const b = useTraceBundle(traceBId)
    if (!a.trace || !b.trace) return null

    const dDur = Number(b.trace.totalDuration) - Number(a.trace.totalDuration)
    const dCost = b.trace.totalCost - a.trace.totalCost
    const dTokens =
        Number(b.trace.tokenBreakdown?.totalTokens ?? 0) - Number(a.trace.tokenBreakdown?.totalTokens ?? 0)

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-1.5">
                <CardTitle className="text-white light:text-brand-main-50 text-sm">Deltas (B − A)</CardTitle>
            </CardHeader>
            <CardContent className="grid grid-cols-3 gap-3 text-xs">
                <Delta label="Duration" value={formatDuration(Math.abs(dDur))} sign={dDur} />
                <Delta label="Cost" value={formatCost(Math.abs(dCost))} sign={dCost} />
                <Delta label="Tokens" value={formatTokens(Math.abs(dTokens))} sign={dTokens} />
            </CardContent>
        </Card>
    )
}

function Delta({ label, value, sign }: { label: string; value: string; sign: number }) {
    const arrow = sign === 0 ? null : sign > 0 ? <ArrowRight className="size-3.5" /> : <ArrowLeft className="size-3.5" />
    const color = sign === 0 ? 'text-white/50 light:text-black/50' : sign > 0 ? 'text-amber-400 light:text-amber-700' : 'text-emerald-400 light:text-emerald-600'
    return (
        <div className="flex items-center justify-between rounded border border-brand-main-500/60 px-2 py-1 bg-brand-main-700/30">
            <span className="text-white/50 light:text-black/50">{label}</span>
            <span className={cn('font-mono flex items-center gap-1', color)}>
                {arrow}
                {sign === 0 ? '—' : (sign > 0 ? '+' : '−') + value}
            </span>
        </div>
    )
}

function OutputDiff({ traceAId, traceBId }: { traceAId: string; traceBId: string }) {
    const a = useTraceBundle(traceAId)
    const b = useTraceBundle(traceBId)
    const [side, setSide] = useState<'unified' | 'split'>('unified')

    const aOutput = useMemo(() => extractOutput(a.spans), [a.spans])
    const bOutput = useMemo(() => extractOutput(b.spans), [b.spans])

    const ops = useMemo(() => diffWords(aOutput, bOutput), [aOutput, bOutput])
    const stats = useMemo(() => diffStats(ops), [ops])

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-1.5 flex flex-row items-center justify-between">
                <CardTitle className="text-white light:text-brand-main-50 text-sm flex items-center gap-2">
                    Output diff
                    <span className="text-[10px] text-emerald-400 light:text-emerald-600 font-mono">+{stats.adds}</span>
                    <span className="text-[10px] text-rose-400 light:text-rose-600 font-mono">−{stats.deletes}</span>
                </CardTitle>
                <div className="flex items-center bg-brand-main-700 rounded p-0.5 text-[11px]">
                    {(['unified', 'split'] as const).map((m) => (
                        <button
                            key={m}
                            type="button"
                            onClick={() => setSide(m)}
                            className={cn(
                                'px-2 py-0.5 rounded transition-colors',
                                side === m ? 'bg-brand-main-500 text-white light:text-brand-main-50' : 'text-zinc-400 light:text-zinc-600 hover:text-zinc-200 light:hover:text-zinc-800'
                            )}
                        >
                            {m}
                        </button>
                    ))}
                </div>
            </CardHeader>
            <CardContent className="!pt-0 text-xs">
                {!aOutput && !bOutput ? (
                    <div className="text-white/30 light:text-black/30 text-xs py-4 text-center">
                        <ArrowDown className="size-4 mx-auto opacity-30 mb-1" />
                        No outputs to diff on either trace.
                    </div>
                ) : side === 'unified' ? (
                    <UnifiedDiff ops={ops} />
                ) : (
                    <SplitDiff aOutput={aOutput} bOutput={bOutput} ops={ops} />
                )}
            </CardContent>
        </Card>
    )
}

function UnifiedDiff({ ops }: { ops: DiffOp[] }) {
    return (
        <pre className="font-mono text-[11px] whitespace-pre-wrap leading-relaxed bg-brand-main-950/60 rounded p-2 border border-brand-main-700/60 max-h-[480px] overflow-auto">
            {ops.map((op, i) => (
                <span
                    key={i}
                    className={
                        op.type === 'insert'
                            ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600 rounded px-px'
                            : op.type === 'delete'
                              ? 'bg-rose-500/15 text-rose-300 light:text-rose-600 line-through rounded px-px'
                              : 'text-zinc-300 light:text-zinc-700'
                    }
                >
                    {op.value}
                </span>
            ))}
        </pre>
    )
}

function SplitDiff({ aOutput, bOutput, ops }: { aOutput: string; bOutput: string; ops: DiffOp[] }) {
    return (
        <div className="grid grid-cols-2 gap-2">
            <pre className="font-mono text-[11px] whitespace-pre-wrap leading-relaxed bg-brand-main-950/60 rounded p-2 border border-brand-main-700/60 max-h-[480px] overflow-auto">
                {ops.map((op, i) =>
                    op.type === 'insert' ? null : (
                        <span
                            key={`a${i}`}
                            className={op.type === 'delete' ? 'bg-rose-500/15 text-rose-300 light:text-rose-600 rounded px-px' : 'text-zinc-300 light:text-zinc-700'}
                        >
                            {op.value}
                        </span>
                    ),
                )}
                {!aOutput && <span className="text-white/30 light:text-black/30">(empty)</span>}
            </pre>
            <pre className="font-mono text-[11px] whitespace-pre-wrap leading-relaxed bg-brand-main-950/60 rounded p-2 border border-brand-main-700/60 max-h-[480px] overflow-auto">
                {ops.map((op, i) =>
                    op.type === 'delete' ? null : (
                        <span
                            key={`b${i}`}
                            className={op.type === 'insert' ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600 rounded px-px' : 'text-zinc-300 light:text-zinc-700'}
                        >
                            {op.value}
                        </span>
                    ),
                )}
                {!bOutput && <span className="text-white/30 light:text-black/30">(empty)</span>}
            </pre>
        </div>
    )
}

function extractOutput(spans: Array<any>): string {
    if (!spans?.length) return ''
    // Find the root span — trace.output usually lives on it.
    const rootSpan =
        spans.find((s) => !s.parentSpanId || !spans.find((p) => p.spanId === s.parentSpanId)) ?? spans[0]
    if (!rootSpan) return ''
    const traceOutput = getAttr(rootSpan, 'trace.output')
    if (traceOutput) return traceOutput
    // Fall back to the response choices of the primary generation span.
    const gen = spans.find(
        (s) => s.spanName?.startsWith('provider.') || getAttr(s, 'observation.type') === 'GENERATION',
    )
    if (gen) {
        const choices = getAttr(gen, 'llm.response.choices')
        if (choices) return choices
        const output = getAttr(gen, 'trace.output') || getAttr(gen, 'output')
        if (output) return output
    }
    return ''
}

// safeBigIntToNumber is intentionally referenced to silence unused-import
// in dev TS configs; remove once we surface duration as a bigint anywhere.
export { safeBigIntToNumber as _unused_safeBigIntToNumber }
