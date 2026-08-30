import { useEffect, useMemo, useRef, useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { GitCompare, Loader2, ChevronRight, ArrowRight, Flag } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useCompareEvalRuns, useEvalRuns, useListComparisonRows } from '@/hooks/evaluations/use-evals'
import type { EvalRun } from '@/server/evals'
import {
    ComparisonGrade,
    type ComparisonRow,
    type ComparisonScorerResult,
    type ComparisonVerdict,
    type ScorerCellDelta,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'
import { statusBadge, statusTint, vizTrack } from '@/components/traces/trace-viz'
import { evaluationPanelClass } from '@/components/evaluations/evaluation-form'
import { diffText } from '@/utils/text-diff'

const { Badge, Button, Switch, Label } = ui

const compareSearchSchema = z.object({
    // Comma-separated run ids, e.g. ?runs=idA,idB (baseline first).
    runs: z.string().optional(),
})

export const Route = createFileRoute('/evaluations/runs_/compare')({
    component: EvalRunsCompare,
    validateSearch: compareSearchSchema,
})

// ─── Shared style + motion constants (design doc §6.1) ───────────────

/**
 * First-paint entrance: fade always; the small translate rides behind
 * `motion-safe:` so reduced motion keeps the opacity change and drops the
 * transform. Decorative only, never blocks interaction.
 */
const enterClass =
    'animate-in fade-in-0 motion-safe:slide-in-from-bottom-2 fill-mode-both duration-300 ease-out-strong'

function enterDelay(index: number) {
    return { animationDelay: `${Math.min(index, 10) * 40}ms` }
}

const mutedText = 'text-white/45 light:text-black/45'
const faintText = 'text-white/30 light:text-black/30'
const bodyText = 'text-white/80 light:text-black/80'

const neutralChip = 'text-white/45 light:text-black/45 bg-brand-main-600/20 border-brand-main-600'

const ROWS_PAGE = 50

// ─── Verdict pill (clip-path duplicate-layer color morph) ────────────

const GRADE_LABEL: Record<ComparisonGrade, string> = {
    [ComparisonGrade.UNSPECIFIED]: 'Pending',
    [ComparisonGrade.IMPROVEMENT]: 'Improvement',
    [ComparisonGrade.REGRESSION]: 'Regression',
    [ComparisonGrade.TRADEOFF]: 'Tradeoff',
    [ComparisonGrade.TIE]: 'Tie',
    [ComparisonGrade.INSUFFICIENT_DATA]: 'Insufficient data',
}

const GRADE_TONE: Record<ComparisonGrade, string> = {
    [ComparisonGrade.UNSPECIFIED]: neutralChip,
    [ComparisonGrade.IMPROVEMENT]: statusBadge('success'),
    [ComparisonGrade.REGRESSION]: statusBadge('error'),
    [ComparisonGrade.TRADEOFF]: statusBadge('warning'),
    [ComparisonGrade.TIE]: statusBadge('neutral'),
    [ComparisonGrade.INSUFFICIENT_DATA]: neutralChip,
}

const GRADE_DOT: Record<ComparisonGrade, string> = {
    [ComparisonGrade.UNSPECIFIED]: 'bg-zinc-500/60',
    [ComparisonGrade.IMPROVEMENT]: statusTint.success.dot,
    [ComparisonGrade.REGRESSION]: statusTint.error.dot,
    [ComparisonGrade.TRADEOFF]: statusTint.warning.dot,
    [ComparisonGrade.TIE]: statusTint.neutral.dot,
    [ComparisonGrade.INSUFFICIENT_DATA]: 'bg-zinc-500/60',
}

/**
 * The signature verdict pill. Grade changes morph via the duplicate-layer +
 * clip-path technique (§6.1): the new tone renders as a stacked layer clipped
 * to zero width, then the clip-path transitions open left-to-right, so both
 * colors are always fully saturated during the swap (no muddy color fade).
 * Reduced motion drops the reveal and swaps instantly (color still changes).
 */
function VerdictPill({ grade }: { grade: ComparisonGrade }) {
    const [settled, setSettled] = useState(grade)
    const [overlay, setOverlay] = useState<ComparisonGrade | null>(null)
    const [revealed, setRevealed] = useState(false)
    const settledRef = useRef(settled)
    settledRef.current = settled

    useEffect(() => {
        if (grade === settledRef.current) return
        setOverlay(grade)
        setRevealed(false)
        // Double-rAF so the overlay paints fully clipped before the
        // transition retargets clip-path (otherwise it pops, no morph).
        let raf2 = 0
        const raf1 = requestAnimationFrame(() => {
            raf2 = requestAnimationFrame(() => setRevealed(true))
        })
        const t = setTimeout(() => {
            setSettled(grade)
            setOverlay(null)
            setRevealed(false)
        }, 340)
        return () => {
            cancelAnimationFrame(raf1)
            cancelAnimationFrame(raf2)
            clearTimeout(t)
        }
    }, [grade])

    const layer =
        'col-start-1 row-start-1 inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 py-0.5 text-[11px] font-medium'
    return (
        <span className="inline-grid align-middle">
            <span className={cn(layer, GRADE_TONE[settled])}>
                <span className={cn('size-1.5 rounded-full', GRADE_DOT[settled])} />
                {GRADE_LABEL[settled]}
            </span>
            {overlay !== null && (
                <span
                    aria-hidden
                    className={cn(
                        layer,
                        GRADE_TONE[overlay],
                        '[transition-property:clip-path] duration-300 ease-out-strong motion-reduce:transition-none',
                    )}
                    style={{
                        clipPath: revealed ? 'inset(-1px -1px -1px -1px)' : 'inset(-1px 100% -1px -1px)',
                    }}
                >
                    <span className={cn('size-1.5 rounded-full', GRADE_DOT[overlay])} />
                    {GRADE_LABEL[overlay]}
                </span>
            )}
        </span>
    )
}

// ─── Route shell ─────────────────────────────────────────────────────

function EvalRunsCompare() {
    const gate = useFeatureGate(FeatureKey.EVALUATIONS)
    const search = Route.useSearch()

    const runIds = useMemo(
        () =>
            (search.runs ?? '')
                .split(',')
                .map((s) => s.trim())
                .filter(Boolean),
        [search.runs],
    )

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Evaluation Runs"
                description="Run evaluations with scheduled runs, regression detection, and scoring."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            {runIds.length < 2 ? <EmptyState /> : <CompareView key={runIds.join(',')} runIds={runIds} />}
        </div>
    )
}

function EmptyState() {
    return (
        <div className={cn('flex-1 flex flex-col items-center justify-center gap-2', mutedText)}>
            <GitCompare className="size-8 opacity-30" />
            <div className="text-sm">Select two eval runs to diff</div>
            <Link
                to="/evaluations/runs"
                className="text-xs text-brand-secondary-300 hover:text-brand-secondary-200"
            >
                Back to eval runs
            </Link>
        </div>
    )
}

// ─── Compare view: pair selection + the diff surface ─────────────────

function CompareView({ runIds }: { runIds: string[] }) {
    // The typed comparison is strictly pairwise: baseline = first id,
    // candidate = second. With more ids in the param the user picks two.
    const [pair, setPair] = useState<string[]>([runIds[0], runIds[1]])
    const ready = pair.length === 2

    return (
        <div className="flex-1 min-h-0 overflow-auto p-3 space-y-3 scrollbar-macos">
            {runIds.length > 2 && <PairSelector runIds={runIds} pair={pair} onChange={setPair} />}
            {ready ? (
                <ComparePanel key={pair.join(',')} baselineId={pair[0]} candidateId={pair[1]} />
            ) : (
                <div className={cn('py-10 text-center text-xs', mutedText)}>
                    Pick two runs above to diff them.
                </div>
            )}
        </div>
    )
}

/** Compact banner shown when the URL carries more than two run ids. */
function PairSelector({
    runIds,
    pair,
    onChange,
}: {
    runIds: string[]
    pair: string[]
    onChange: (pair: string[]) => void
}) {
    const { data: allRuns } = useEvalRuns()
    const nameOf = (id: string) => allRuns?.find((r) => r.id === id)?.name || `${id.slice(0, 8)}…`

    const toggle = (id: string) => {
        if (pair.includes(id)) onChange(pair.filter((p) => p !== id))
        else if (pair.length < 2) onChange([...pair, id])
        else onChange([pair[0], id])
    }

    return (
        <div
            className={cn(
                'rounded border border-amber-500/25 bg-amber-500/8 px-3 py-2 space-y-1.5',
                enterClass,
            )}
        >
            <div className="text-xs text-amber-300/90 light:text-amber-700/90">
                Select two runs to diff. The typed comparison is pairwise: first pick is the
                baseline, second is the candidate.
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
                {runIds.map((id) => {
                    const idx = pair.indexOf(id)
                    const selected = idx >= 0
                    return (
                        <button
                            key={id}
                            type="button"
                            onClick={() => toggle(id)}
                            className={cn(
                                'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px]',
                                'transition-colors duration-150 ease-out-strong active:scale-[0.98] motion-reduce:active:scale-100',
                                selected
                                    ? 'border-brand-secondary-500/60 bg-brand-secondary-500/15 text-brand-secondary-200 light:text-brand-secondary-700'
                                    : cn('border-brand-main-600 hover:text-white/85 light:hover:text-black/85', mutedText),
                            )}
                        >
                            <span className="max-w-48 truncate">{nameOf(id)}</span>
                            {selected && (
                                <span className={cn('text-[9px] uppercase tracking-wide', faintText)}>
                                    {idx === 0 ? 'baseline' : 'candidate'}
                                </span>
                            )}
                        </button>
                    )
                })}
            </div>
        </div>
    )
}

// ─── The two-run comparison panel ────────────────────────────────────

const TERMINAL_STATUSES = new Set(['completed', 'failed', 'cancelled', 'canceled', 'error'])

function isTerminal(status?: string): boolean {
    return TERMINAL_STATUSES.has((status ?? '').toLowerCase())
}

function ComparePanel({ baselineId, candidateId }: { baselineId: string; candidateId: string }) {
    // persist=true so the engine materializes the comparison and returns a
    // comparisonId for the per-row grid. The hook falls back to a verdict-only
    // preview (empty comparisonId) while either run is still in progress.
    const { data, isLoading, error } = useCompareEvalRuns([baselineId, candidateId], {
        persist: true,
    })

    if (isLoading) {
        return (
            <div className={cn('flex items-center justify-center py-16 text-xs', mutedText)}>
                <Loader2 className="size-4 animate-spin mr-2" /> Comparing runs…
            </div>
        )
    }

    if (error) {
        return (
            <div className="rounded border border-red-500/20 bg-red-500/10 p-3 text-xs text-red-400 light:text-red-600">
                Comparison failed: {error.message}
            </div>
        )
    }

    const runs = data?.evalRuns ?? []
    const baseline = runs.find((r) => r.id === baselineId) ?? runs[0]
    const candidate = runs.find((r) => r.id === candidateId) ?? runs[1]
    if (!data || !baseline || !candidate) {
        return (
            <div className={cn('py-10 text-center text-xs', mutedText)}>
                Could not load both of the selected runs.
            </div>
        )
    }

    const comparisonId = data.comparisonId ?? ''
    const bothTerminal = isTerminal(baseline.status) && isTerminal(candidate.status)

    return (
        <div className="space-y-3">
            <div className={enterClass} style={enterDelay(0)}>
                <CompareHeader
                    baseline={baseline}
                    candidate={candidate}
                    overall={data.overall}
                    matchMode={data.matchMode}
                />
            </div>
            {!comparisonId && (
                <div
                    className={cn(
                        'rounded border border-amber-500/25 bg-amber-500/8 px-3 py-2 text-xs text-amber-300/90 light:text-amber-700/90',
                        enterClass,
                    )}
                    style={enterDelay(1)}
                >
                    {bothTerminal
                        ? 'Per-row diffs are unavailable for this comparison.'
                        : 'Runs still in progress. Per-row diffs are available once both complete.'}
                </div>
            )}
            <div className={enterClass} style={enterDelay(2)}>
                <ScorerPanel results={data.scorerResults ?? []} />
            </div>
            {comparisonId && (
                <div className={enterClass} style={enterDelay(3)}>
                    <ComparisonRowsGrid key={comparisonId} comparisonId={comparisonId} />
                </div>
            )}
        </div>
    )
}

// ─── Header: runs, match mode, verdict ───────────────────────────────

function RunLabel({ run, role }: { run: EvalRun; role: 'baseline' | 'candidate' }) {
    const r = run as EvalRun & { isBaseline?: boolean }
    return (
        <div className="flex min-w-0 items-center gap-1.5">
            <span className={cn('text-[9px] uppercase tracking-wide shrink-0', faintText)}>{role}</span>
            <span className="truncate text-sm font-medium text-white light:text-brand-main-50">
                {run.name || run.id}
            </span>
            {r.isBaseline && (
                <Badge
                    variant="outline"
                    className="text-[10px] text-amber-400 light:text-amber-700 border-amber-500/30 inline-flex items-center gap-0.5 shrink-0"
                >
                    <Flag className="h-2.5 w-2.5" /> Baseline
                </Badge>
            )}
            <Badge
                variant="outline"
                className={cn('text-[10px] border-brand-main-500 shrink-0', mutedText)}
            >
                {run.status || 'unknown'}
            </Badge>
        </div>
    )
}

function fmtSignedNum(v: number, fmt: (n: number) => string): string {
    const sign = v > 0 ? '+' : v < 0 ? '-' : ''
    return `${sign}${fmt(Math.abs(v))}`
}

function CompareHeader({
    baseline,
    candidate,
    overall,
    matchMode,
}: {
    baseline: EvalRun
    candidate: EvalRun
    overall?: ComparisonVerdict
    matchMode?: string
}) {
    const grade = overall?.grade ?? ComparisonGrade.UNSPECIFIED
    const coverage = overall && Number.isFinite(overall.coverage) ? overall.coverage : undefined
    const stats: string[] = []
    if (overall) {
        if (Number.isFinite(overall.latencyDelta) && overall.latencyDelta !== 0)
            stats.push(`latency ${fmtSignedNum(overall.latencyDelta, (n) => `${Math.round(n)} ms`)}`)
        if (Number.isFinite(overall.costDelta) && overall.costDelta !== 0)
            stats.push(`cost ${fmtSignedNum(overall.costDelta, (n) => `$${n.toFixed(5)}`)}/item`)
        if (Number.isFinite(overall.errorRateDelta) && overall.errorRateDelta !== 0)
            stats.push(`errors ${fmtSignedNum(overall.errorRateDelta * 100, (n) => `${n.toFixed(1)}pp`)}`)
    }

    return (
        <div className={cn(evaluationPanelClass, 'px-3 py-2.5 space-y-1.5')}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <div className="flex min-w-0 flex-1 items-center gap-2">
                    <RunLabel run={baseline} role="baseline" />
                    <ArrowRight className={cn('size-3.5 shrink-0', faintText)} />
                    <RunLabel run={candidate} role="candidate" />
                </div>
                <div className="flex items-center gap-2 shrink-0">
                    {matchMode && (
                        <Badge
                            variant="outline"
                            className={cn('text-[10px] font-mono border-brand-main-500', mutedText)}
                            title={
                                matchMode === 'hash'
                                    ? 'Rows paired by canonical input hash (works across dataset versions)'
                                    : 'Rows paired by dataset item id'
                            }
                        >
                            match: {matchMode}
                        </Badge>
                    )}
                    <VerdictPill grade={grade} />
                </div>
            </div>
            {(overall?.rationale || coverage !== undefined || stats.length > 0) && (
                <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs', mutedText)}>
                    {overall?.rationale && (
                        <span className={bodyText}>{overall.rationale}</span>
                    )}
                    {coverage !== undefined && (
                        <span className="font-mono">{(coverage * 100).toFixed(0)}% of items paired</span>
                    )}
                    {stats.map((s) => (
                        <span key={s} className="font-mono">
                            {s}
                        </span>
                    ))}
                </div>
            )}
        </div>
    )
}

// ─── Per-scorer panel with CI bars ───────────────────────────────────

const SCORER_VERDICT_STYLE: Record<string, { cls: string; label: string }> = {
    improvement: { cls: statusTint.success.text, label: 'improvement' },
    regression: { cls: statusTint.error.text, label: 'regression' },
    inconclusive: { cls: 'text-amber-400/80 light:text-amber-700/80', label: 'not significant' },
    insufficient: { cls: faintText, label: 'n too low' },
}

function fmtMean(v: number): string {
    return Number.isFinite(v) ? v.toFixed(3) : '–'
}

/**
 * Tiny horizontal bar visualizing the 95% CI [ciLow, ciHigh] relative to zero
 * (center tick). Bar turns green when the whole CI clears zero upward, red
 * when it clears downward, neutral when it spans zero. Dot marks the mean.
 */
function CIBar({ s }: { s: ComparisonScorerResult }) {
    if (s.verdict === 'insufficient' || !Number.isFinite(s.ciLow) || !Number.isFinite(s.ciHigh)) {
        return <div className={cn('relative h-1.5 w-24 rounded-full', vizTrack)} />
    }
    const span = Math.max(Math.abs(s.ciLow), Math.abs(s.ciHigh), Math.abs(s.meanDiff), 1e-9) * 1.15
    const pct = (v: number) => ((v + span) / (2 * span)) * 100
    const lo = pct(Math.min(s.ciLow, s.ciHigh))
    const hi = pct(Math.max(s.ciLow, s.ciHigh))
    const fill =
        s.ciLow > 0 ? statusTint.success.bar : s.ciHigh < 0 ? statusTint.error.bar : 'bg-zinc-500/50'
    return (
        <div
            className={cn('relative h-1.5 w-24 rounded-full', vizTrack)}
            title={`95% CI [${s.ciLow.toFixed(3)}, ${s.ciHigh.toFixed(3)}]`}
        >
            <div
                className={cn(
                    'absolute inset-y-0 rounded-full transition-colors duration-200 ease-out-strong',
                    fill,
                )}
                style={{ left: `${lo}%`, width: `${Math.max(hi - lo, 2)}%` }}
            />
            <div className="absolute inset-y-0 left-1/2 w-px bg-white/30 light:bg-black/30" />
            <div
                className="absolute top-1/2 size-[5px] -translate-y-1/2 rounded-full bg-white/85 light:bg-black/70"
                style={{ left: `calc(${pct(s.meanDiff)}% - 2.5px)` }}
            />
        </div>
    )
}

function ScorerPanel({ results }: { results: ComparisonScorerResult[] }) {
    return (
        <div className={cn(evaluationPanelClass, 'overflow-hidden')}>
            <div className="flex items-center gap-2 px-3 pt-2.5 pb-1.5">
                <GitCompare className="h-3.5 w-3.5 text-brand-secondary-400" />
                <span className="text-sm text-white light:text-brand-main-50">Scorers</span>
                <span className={cn('text-[10px]', faintText)}>
                    paired bootstrap, 95% CI. Green/red only when the CI excludes zero.
                </span>
            </div>
            {results.length === 0 ? (
                <div className={cn('px-3 pb-4 pt-2 text-center text-xs', faintText)}>
                    No scorer results for this comparison yet.
                </div>
            ) : (
                <div className="overflow-x-auto px-3 pb-2">
                    <table className="w-full text-xs border-collapse">
                        <thead>
                            <tr className={cn('border-b border-brand-main-700/60', mutedText)}>
                                <th className="text-left font-normal py-1.5 pr-3">Scorer</th>
                                <th className="text-right font-normal py-1.5 px-3">Baseline → candidate</th>
                                <th className="text-right font-normal py-1.5 px-3">Δ</th>
                                <th className="text-left font-normal py-1.5 px-3">95% CI vs 0</th>
                                <th className="text-right font-normal py-1.5 px-3">p</th>
                                <th className="text-right font-normal py-1.5 pl-3">Verdict</th>
                            </tr>
                        </thead>
                        <tbody>
                            {results.map((s) => {
                                const style = SCORER_VERDICT_STYLE[s.verdict] ?? SCORER_VERDICT_STYLE.insufficient
                                const deltaCls =
                                    s.meanDiff > 1e-9
                                        ? statusTint.success.text
                                        : s.meanDiff < -1e-9
                                          ? statusTint.error.text
                                          : mutedText
                                return (
                                    <tr key={s.name} className="border-b border-brand-main-700/30 last:border-0">
                                        <td className={cn('py-1.5 pr-3', bodyText)}>{s.name}</td>
                                        <td className={cn('py-1.5 px-3 text-right font-mono', bodyText)}>
                                            {fmtMean(s.baselineMean)}
                                            <span className={cn('mx-1', faintText)}>→</span>
                                            {fmtMean(s.candidateMean)}
                                        </td>
                                        <td
                                            className={cn(
                                                'py-1.5 px-3 text-right font-mono transition-colors duration-200 ease-out-strong',
                                                deltaCls,
                                            )}
                                        >
                                            {Number.isFinite(s.meanDiff)
                                                ? fmtSignedNum(s.meanDiff, (n) => n.toFixed(3))
                                                : '–'}
                                        </td>
                                        <td className="py-1.5 px-3">
                                            <CIBar s={s} />
                                        </td>
                                        <td className={cn('py-1.5 px-3 text-right font-mono', mutedText)}>
                                            {s.verdict === 'insufficient' || !Number.isFinite(s.pValue)
                                                ? '–'
                                                : s.pValue < 0.001
                                                  ? '<0.001'
                                                  : s.pValue.toFixed(3)}
                                        </td>
                                        <td className={cn('py-1.5 pl-3 text-right', style.cls)}>
                                            {style.label}
                                            <span className={cn('ml-1.5 font-mono', faintText)}>n={s.n}</span>
                                        </td>
                                    </tr>
                                )
                            })}
                        </tbody>
                    </table>
                </div>
            )}
        </div>
    )
}

// ─── Row-matched diff grid ───────────────────────────────────────────

const gridCols =
    'grid-cols-[14px_minmax(0,1fr)_minmax(0,1.25fr)_minmax(0,1.25fr)_minmax(0,0.9fr)]'

function ComparisonRowsGrid({ comparisonId }: { comparisonId: string }) {
    const [onlyRegressions, setOnlyRegressions] = useState(false)
    const [offset, setOffset] = useState(0)
    const [pages, setPages] = useState<Record<number, ComparisonRow[]>>({})
    const [total, setTotal] = useState<number | null>(null)
    const [expanded, setExpanded] = useState<Set<string>>(new Set())
    // First-paint stagger only (§6.1): rows mounted while this is true animate;
    // rows added by pagination or the regression filter never do.
    const staggerRef = useRef(true)

    const query = useListComparisonRows(comparisonId, {
        onlyRegressions,
        limit: ROWS_PAGE,
        offset,
    })

    useEffect(() => {
        const data = query.data
        if (!data) return
        setPages((prev) => ({ ...prev, [offset]: data.rows }))
        setTotal(data.total)
    }, [query.data, offset])

    const rows = useMemo(
        () =>
            Object.keys(pages)
                .map(Number)
                .sort((a, b) => a - b)
                .flatMap((k) => pages[k]),
        [pages],
    )

    useEffect(() => {
        if (rows.length > 0) staggerRef.current = false
    }, [rows.length > 0])

    const toggleFilter = (v: boolean) => {
        setOnlyRegressions(v)
        setOffset(0)
        setPages({})
        setTotal(null)
        setExpanded(new Set())
    }

    const toggleRow = (key: string) => {
        setExpanded((prev) => {
            const next = new Set(prev)
            if (next.has(key)) next.delete(key)
            else next.add(key)
            return next
        })
    }

    const hasMore = total !== null && rows.length < total
    const initialLoading = query.isLoading && rows.length === 0

    return (
        <div className={cn(evaluationPanelClass, 'overflow-hidden')}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 pt-2.5 pb-1.5">
                <span className="text-sm text-white light:text-brand-main-50">Row diffs</span>
                {total !== null && (
                    <span className={cn('text-[10px] font-mono', faintText)}>
                        {rows.length} of {total}
                    </span>
                )}
                <div className="ml-auto flex items-center gap-2">
                    <Label
                        htmlFor="only-regressions"
                        className={cn('text-[11px] font-normal cursor-pointer', mutedText)}
                    >
                        Regressions only
                    </Label>
                    <Switch
                        id="only-regressions"
                        checked={onlyRegressions}
                        onCheckedChange={toggleFilter}
                    />
                </div>
            </div>

            <div
                className={cn(
                    'grid items-center gap-x-3 border-b border-brand-main-700/60 px-3 py-1.5 text-[10px] uppercase tracking-wide',
                    gridCols,
                    faintText,
                )}
            >
                <span />
                <span>Input</span>
                <span>Baseline output</span>
                <span>Candidate output</span>
                <span>Score deltas</span>
            </div>

            {initialLoading ? (
                <div className={cn('flex items-center justify-center py-10 text-xs', mutedText)}>
                    <Loader2 className="size-4 animate-spin mr-2" /> Loading rows…
                </div>
            ) : query.error && rows.length === 0 ? (
                <div className="px-3 py-6 text-center text-xs text-red-400 light:text-red-600">
                    Could not load comparison rows: {query.error.message}
                </div>
            ) : rows.length === 0 ? (
                <div className={cn('px-3 py-8 text-center text-xs', faintText)}>
                    {onlyRegressions
                        ? 'No regressed rows in this comparison.'
                        : 'No matched rows. The runs may not share any inputs.'}
                </div>
            ) : (
                <ul>
                    {rows.map((row, i) => {
                        const key = `${row.inputHash}:${i}`
                        return (
                            <DiffGridRow
                                key={key}
                                row={row}
                                index={i}
                                expanded={expanded.has(key)}
                                onToggle={() => toggleRow(key)}
                                staggerRef={staggerRef}
                            />
                        )
                    })}
                </ul>
            )}

            {hasMore && (
                <div className="flex justify-center border-t border-brand-main-700/30 px-3 py-2">
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={query.isFetching}
                        onClick={() => setOffset(rows.length)}
                        className="h-7 text-xs transition-transform duration-150 ease-out-strong active:scale-[0.98] motion-reduce:active:scale-100"
                    >
                        {query.isFetching ? (
                            <>
                                <Loader2 className="size-3 animate-spin mr-1.5" /> Loading…
                            </>
                        ) : (
                            `Load ${Math.min(ROWS_PAGE, (total ?? 0) - rows.length)} more`
                        )}
                    </Button>
                </div>
            )}
        </div>
    )
}

function DeltaChip({ d }: { d: ScorerCellDelta }) {
    const up = d.delta > 1e-9
    const down = d.delta < -1e-9
    const cls = up ? statusBadge('success') : down ? statusBadge('error') : neutralChip
    return (
        <span
            title={`${d.name}: ${fmtMean(d.baseline)} → ${fmtMean(d.candidate)}`}
            className={cn(
                'inline-flex max-w-full items-center gap-1 rounded border px-1.5 py-px font-mono text-[10px]',
                'transition-colors duration-200 ease-out-strong',
                cls,
            )}
        >
            <span className="truncate">{d.name}</span>
            <span className="shrink-0">
                {Number.isFinite(d.delta) ? fmtSignedNum(d.delta, (n) => n.toFixed(2)) : '–'}
            </span>
        </span>
    )
}

function DiffGridRow({
    row,
    index,
    expanded,
    onToggle,
    staggerRef,
}: {
    row: ComparisonRow
    index: number
    expanded: boolean
    onToggle: () => void
    staggerRef: React.RefObject<boolean>
}) {
    // Captured once at mount: rows in the first-paint batch stagger in, rows
    // mounted later (pagination, filter changes) appear instantly.
    const animate = useRef(staggerRef.current).current
    const [everExpanded, setEverExpanded] = useState(false)
    useEffect(() => {
        if (expanded) setEverExpanded(true)
    }, [expanded])

    return (
        <li
            className={cn(
                'border-b border-brand-main-700/30 last:border-0 border-l-2',
                'transition-colors duration-200 ease-out-strong',
                row.regression ? 'border-l-rose-500/60 bg-rose-500/5' : 'border-l-transparent',
                animate && enterClass,
            )}
            style={animate ? enterDelay(index) : undefined}
        >
            <button
                type="button"
                onClick={onToggle}
                aria-expanded={expanded}
                className={cn(
                    'grid w-full items-start gap-x-3 px-3 py-2 text-left text-xs',
                    gridCols,
                    'transition-colors duration-150 ease-out-strong hover:bg-white/[0.03] light:hover:bg-black/[0.03]',
                )}
            >
                <ChevronRight
                    className={cn(
                        'mt-0.5 size-3 transition-transform duration-150 ease-out-strong motion-reduce:transition-none',
                        expanded && 'rotate-90',
                        faintText,
                    )}
                />
                <span className={cn('line-clamp-2 break-words font-mono text-[11px]', mutedText)}>
                    {row.inputPreview || '(empty input)'}
                </span>
                <span className={cn('line-clamp-2 break-words font-mono text-[11px]', bodyText)}>
                    {row.baselineOutput || <span className={faintText}>(no output)</span>}
                </span>
                <span className={cn('line-clamp-2 break-words font-mono text-[11px]', bodyText)}>
                    {row.candidateOutput || <span className={faintText}>(no output)</span>}
                </span>
                <span className="flex min-w-0 flex-wrap gap-1">
                    {row.scorerDeltas.map((d) => (
                        <DeltaChip key={d.name} d={d} />
                    ))}
                </span>
            </button>

            {/* Fast (<200ms) height/opacity reveal; no other motion (§6.1). */}
            <div
                className={cn(
                    'grid transition-[grid-template-rows] duration-200 ease-out-strong motion-reduce:transition-none',
                    expanded ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]',
                )}
            >
                <div className="overflow-hidden">
                    {everExpanded && (
                        <div
                            className={cn(
                                'mx-3 mb-2 rounded border border-brand-main-700/50 bg-brand-main-950/60 light:border-black/10 light:bg-black/[0.02] px-3 py-2',
                                'transition-opacity duration-150 ease-out-strong',
                                expanded ? 'opacity-100' : 'opacity-0',
                            )}
                        >
                            <div className={cn('mb-1.5 flex items-center gap-2 text-[10px] uppercase tracking-wide', faintText)}>
                                Output diff
                                <span className="normal-case tracking-normal">
                                    <span className={statusTint.error.text}>baseline removals</span>
                                    {' / '}
                                    <span className={statusTint.success.text}>candidate additions</span>
                                </span>
                            </div>
                            <OutputDiff a={row.baselineOutput} b={row.candidateOutput} />
                        </div>
                    )}
                </div>
            </div>
        </li>
    )
}

/**
 * Char-level field diff of the full baseline vs candidate output. No length
 * cap: this is the explicit Braintrust-beat, the diff util degrades in
 * granularity (never truncates) as outputs grow.
 */
function OutputDiff({ a, b }: { a: string; b: string }) {
    const segments = useMemo(() => diffText(a, b), [a, b])
    if (segments.length === 0) {
        return <div className={cn('text-xs', faintText)}>Both outputs are empty.</div>
    }
    return (
        <pre className={cn('whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed', bodyText)}>
            {segments.map((seg, i) =>
                seg.type === 'equal' ? (
                    <span key={i}>{seg.text}</span>
                ) : seg.type === 'add' ? (
                    <ins
                        key={i}
                        className="rounded-[2px] bg-emerald-500/15 text-emerald-200 no-underline light:bg-emerald-500/15 light:text-emerald-800"
                    >
                        {seg.text}
                    </ins>
                ) : (
                    <del
                        key={i}
                        className="rounded-[2px] bg-rose-500/15 text-rose-300 line-through decoration-rose-400/50 light:bg-rose-500/15 light:text-rose-700"
                    >
                        {seg.text}
                    </del>
                ),
            )}
        </pre>
    )
}
