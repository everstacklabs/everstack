import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import {
    Activity,
    AlertTriangle,
    BarChart3,
    ChevronLeft,
    ChevronRight,
    Clock3,
    Database,
    FileText,
    GitCompare,
    LayoutPanelTop,
    ListTree,
    ListFilter,
    MessageSquare,
    Play,
    Plus,
    Square,
    Table2,
    ThumbsDown,
    ThumbsUp,
    Trash2,
    X,
} from 'lucide-react'
import { useDatasets, useDatasetItems } from '@/hooks/evaluations/use-datasets'
import {
    usePlaygroundStore,
    type GridRow,
    type PlaygroundVariant,
} from '@/stores/playground-store'
import {
    cellKey,
    useAnyGridRunning,
    useCellState,
    usePlaygroundMetadata,
    usePlaygroundRunStore,
    type CellState,
} from '@/stores/playground-run-store'
import { getTraceByID } from '@/server/traces'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { formatDuration as formatTraceDuration } from '@/utils/trace-formatters'
import { categoryTimelineColors, categoryLabels } from '@/utils/span-display-helpers'
import { getSpanDisplayConfig } from '@/utils/span-title-name-map'
import { timelineStatusColors } from '@/components/traces/trace-viz'
import { ScorerPicker } from './scorer-picker'

const {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} = ui

type ResultView = 'summary' | 'grid' | 'failures' | 'diff'
type RowDrawerLayout = 'trace' | 'timeline'

type RunGridProps = {
    focus?: 'dataset' | 'compare'
}

const IDLE_CELL_VIEW: CellState = { status: 'idle', text: '' }

const VIEWS: Array<{ id: ResultView; label: string; icon: typeof BarChart3 }> = [
    { id: 'summary', label: 'Summary', icon: BarChart3 },
    { id: 'grid', label: 'Grid', icon: Table2 },
    { id: 'failures', label: 'Failures', icon: ListFilter },
    { id: 'diff', label: 'Diff', icon: GitCompare },
]

const ROW_LAYOUTS: Array<{ id: RowDrawerLayout; label: string; icon: typeof ListTree }> = [
    { id: 'trace', label: 'Trace layout', icon: ListTree },
    { id: 'timeline', label: 'Timeline layout', icon: LayoutPanelTop },
]

/** Best-effort human-readable text for a dataset item's Struct field. */
function structToText(v: unknown): string {
    if (v === null || v === undefined) return ''
    if (typeof v === 'string') return v
    if (typeof v === 'number' || typeof v === 'boolean') return String(v)
    if (typeof v === 'object') {
        const obj = v as Record<string, unknown>
        for (const key of ['input', 'value', 'text', 'output', 'expected', 'expected_output']) {
            if (key in obj) return structToText(obj[key])
        }
        const keys = Object.keys(obj)
        if (keys.length === 1) return structToText(obj[keys[0]])
        return JSON.stringify(obj)
    }
    return String(v)
}

function taskLabel(index: number) {
    return String.fromCharCode(65 + index)
}

function scoreEntries(scores: CellState['scores']) {
    if (!scores) return []
    return Object.entries(scores).filter(
        ([key]) => !key.endsWith('_reason') && !key.endsWith('_error'),
    )
}

function scoreFailed(value: unknown) {
    return value === false || value === 0 || value === '0'
}

function scorePassed(value: unknown) {
    return value === true || value === 1 || value === '1'
}

function isFailure(cell: CellState) {
    if (cell.status === 'error') return true
    return scoreEntries(cell.scores).some(([, value]) => scoreFailed(value))
}

function hasPositiveScore(cell: CellState) {
    return scoreEntries(cell.scores).some(([, value]) => scorePassed(value))
}

function formatDuration(ms?: number) {
    if (ms === undefined) return 'Pending'
    if (ms < 1000) return `${ms} ms`
    return `${(ms / 1000).toFixed(2)} s`
}

export function RunGrid({ focus = 'dataset' }: RunGridProps = {}) {
    const rows = usePlaygroundStore((s) => s.rows)
    const variants = usePlaygroundStore((s) => s.variants)
    const datasetName = usePlaygroundStore((s) => s.datasetName)
    const addRow = usePlaygroundStore((s) => s.addRow)

    const cells = usePlaygroundRunStore((s) => s.cells)
    const runGrid = usePlaygroundRunStore((s) => s.runGrid)
    const stopGrid = usePlaygroundRunStore((s) => s.stopGrid)
    const clearGrid = usePlaygroundRunStore((s) => s.clearGrid)
    const metadata = usePlaygroundMetadata()
    const anyRunning = useAnyGridRunning()

    const [view, setView] = useState<ResultView>(focus === 'compare' ? 'diff' : 'summary')
    const [selectedRowId, setSelectedRowId] = useState<string | null>(null)
    const [rowLayout, setRowLayout] = useState<RowDrawerLayout>('trace')
    useEffect(() => {
        setView(focus === 'compare' ? 'diff' : 'summary')
    }, [focus])

    useEffect(() => {
        if (selectedRowId && !rows.some((row) => row.id === selectedRowId)) {
            setSelectedRowId(null)
        }
    }, [rows, selectedRowId])

    const runnableVariants = variants.filter((variant) => variant.model.trim())
    const canRun = rows.length > 0 && runnableVariants.length > 0 && !anyRunning

    const entries = useMemo(
        () =>
            rows.flatMap((row, rowIndex) =>
                variants.map((variant, variantIndex) => ({
                    row,
                    rowIndex,
                    variant,
                    variantIndex,
                    cell: cells[cellKey(row.id, variant.id)] ?? IDLE_CELL_VIEW,
                })),
            ),
        [cells, rows, variants],
    )

    const activeEntries = entries.filter((entry) => entry.variant.model.trim())
    const doneEntries = activeEntries.filter((entry) => entry.cell.status === 'done')
    const failureEntries = activeEntries.filter((entry) => isFailure(entry.cell))
    const inFlightEntries = activeEntries.filter((entry) =>
        ['queued', 'running', 'scoring'].includes(entry.cell.status),
    )
    const latencyEntries = doneEntries.filter((entry) => entry.cell.durationMs !== undefined)
    const avgLatency =
        latencyEntries.length === 0
            ? undefined
            : Math.round(
                  latencyEntries.reduce((sum, entry) => sum + (entry.cell.durationMs ?? 0), 0) /
                      latencyEntries.length,
              )
    const selectedRowIndex = selectedRowId
        ? rows.findIndex((row) => row.id === selectedRowId)
        : -1
    const selectedRow = selectedRowIndex >= 0 ? rows[selectedRowIndex] : undefined

    return (
        <section className="relative flex h-full min-h-0 flex-col overflow-hidden">
            <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-brand-main-800 bg-brand-main-900/20 px-4 py-2">
                <div className="flex items-center gap-3">
                    {VIEWS.map(({ id, label, icon: Icon }) => (
                        <button
                            key={id}
                            type="button"
                            onClick={() => setView(id)}
                            className={`inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs transition-colors ${
                                view === id
                                    ? 'bg-brand-secondary-500/10 text-brand-secondary-200 ring-1 ring-brand-secondary-500/25'
                                    : 'text-white/45 hover:bg-brand-main-800/70 hover:text-white light:text-black/45 light:hover:text-brand-main-50'
                            }`}
                        >
                            <Icon className="h-3.5 w-3.5" />
                            {label}
                        </button>
                    ))}
                </div>

                <div className="ml-auto flex flex-wrap items-center gap-3">
                    <DatasetMenu />
                    <ScorerPicker />
                    <span className="text-xs text-white/35 light:text-black/35">
                        {datasetName ?? 'No dataset'} · {rows.length} rows
                    </span>
                    <button
                        type="button"
                        onClick={addRow}
                        className="inline-flex items-center gap-1 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/65 light:hover:text-brand-main-50"
                    >
                        <Plus className="h-3 w-3" />
                        Row
                    </button>
                    {rows.length > 0 && (
                        <button
                            type="button"
                            onClick={clearGrid}
                            className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-white/35 transition-colors hover:bg-brand-main-800/70 hover:text-white/70 light:text-black/35 light:hover:text-black/70"
                            title="Clear all cell results"
                        >
                            <Trash2 className="h-3 w-3" />
                            Clear
                        </button>
                    )}
                    {anyRunning ? (
                        <button
                            type="button"
                            onClick={stopGrid}
                            className="inline-flex items-center gap-1 rounded border border-rose-400/50 bg-rose-500/10 px-2 py-1 text-xs text-rose-300 transition-colors hover:text-rose-200"
                        >
                            <Square className="h-3 w-3" />
                            Stop
                        </button>
                    ) : (
                        <button
                            type="button"
                            onClick={() => void runGrid(metadata)}
                            disabled={!canRun}
                            className="inline-flex items-center gap-1 rounded border border-brand-secondary-500/50 bg-brand-secondary-500/10 px-2 py-1 text-xs text-brand-secondary-200 transition-colors hover:text-brand-secondary-100 disabled:border-brand-main-800 disabled:bg-transparent disabled:text-white/25 light:disabled:text-black/25"
                        >
                            <Play className="h-3 w-3" />
                            Run dataset
                        </button>
                    )}
                </div>
            </div>

            {rows.length === 0 ? (
                <EmptyDataset />
            ) : view === 'summary' ? (
                <SummaryView
                    rows={rows}
                    variants={variants}
                    activeEntries={activeEntries}
                    doneCount={doneEntries.length}
                    failureEntries={failureEntries}
                    inFlightCount={inFlightEntries.length}
                    avgLatency={avgLatency}
                    onOpenRow={setSelectedRowId}
                />
            ) : view === 'grid' ? (
                <GridView rows={rows} variants={variants} onOpenRow={setSelectedRowId} />
            ) : view === 'failures' ? (
                <FailuresView failureEntries={failureEntries} onOpenRow={setSelectedRowId} />
            ) : (
                <DiffView rows={rows} variants={variants} cells={cells} onOpenRow={setSelectedRowId} />
            )}

            {selectedRow && (
                <RowDetailDrawer
                    row={selectedRow}
                    rowIndex={selectedRowIndex}
                    rowCount={rows.length}
                    variants={variants}
                    cells={cells}
                    layout={rowLayout}
                    onLayoutChange={setRowLayout}
                    onClose={() => setSelectedRowId(null)}
                    onPrevious={() => {
                        const previous = rows[Math.max(0, selectedRowIndex - 1)]
                        if (previous) setSelectedRowId(previous.id)
                    }}
                    onNext={() => {
                        const next = rows[Math.min(rows.length - 1, selectedRowIndex + 1)]
                        if (next) setSelectedRowId(next.id)
                    }}
                />
            )}
        </section>
    )
}

function DatasetMenu() {
    const datasetName = usePlaygroundStore((s) => s.datasetName)
    const loadDataset = usePlaygroundStore((s) => s.loadDataset)
    const clearDataset = usePlaygroundStore((s) => s.clearDataset)
    const clearGrid = usePlaygroundRunStore((s) => s.clearGrid)

    const { data: datasets } = useDatasets()
    const [pendingId, setPendingId] = useState<string | null>(null)
    const { data: items } = useDatasetItems(pendingId ?? '')
    const loadedRef = useRef<string | null>(null)

    useEffect(() => {
        if (!pendingId || !items) return
        if (loadedRef.current === pendingId) return
        loadedRef.current = pendingId
        const dataset = datasets?.find((candidate) => candidate.id === pendingId)
        loadDataset({
            id: pendingId,
            name: dataset?.name ?? 'Dataset',
            rows: items.map((item) => ({
                datasetItemId: item.id,
                input: structToText(item.input),
                expected: item.expectedOutput ? structToText(item.expectedOutput) : undefined,
                metadata: item.metadata ? JSON.stringify(item.metadata, null, 2) : undefined,
                sourceTraceId: item.sourceTraceId,
                sourceObservationId: item.sourceObservationId,
            })),
        })
        setPendingId(null)
    }, [datasets, items, loadDataset, pendingId])

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/65 light:hover:text-brand-main-50"
                >
                    <Database className="h-3.5 w-3.5" />
                    {datasetName ?? 'Dataset'}
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-60">
                {(datasets ?? []).length === 0 ? (
                    <div className="px-2 py-2 text-xs text-white/40 light:text-black/40">No datasets yet</div>
                ) : (
                    (datasets ?? []).map((dataset) => (
                        <DropdownMenuItem key={dataset.id} onSelect={() => setPendingId(dataset.id)}>
                            {dataset.name}
                        </DropdownMenuItem>
                    ))
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem
                    onSelect={() => {
                        clearDataset()
                        clearGrid()
                    }}
                >
                    <X className="mr-1.5 h-3 w-3" /> Clear dataset
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    )
}

function EmptyDataset() {
    return (
        <div className="flex min-h-0 flex-1 items-center justify-center p-8 text-center">
            <div className="max-w-sm">
                <Database className="mx-auto mb-3 h-7 w-7 text-white/20 light:text-black/20" />
                <div className="text-sm font-medium text-white/75 light:text-brand-main-50">
                    No dataset rows attached
                </div>
                <div className="mt-1 text-xs leading-relaxed text-white/35 light:text-black/35">
                    Attach a dataset or add a row to evaluate tasks across inputs.
                </div>
            </div>
        </div>
    )
}

function SummaryView({
    rows,
    variants,
    activeEntries,
    doneCount,
    failureEntries,
    inFlightCount,
    avgLatency,
    onOpenRow,
}: {
    rows: GridRow[]
    variants: PlaygroundVariant[]
    activeEntries: Array<{
        row: GridRow
        rowIndex: number
        variant: PlaygroundVariant
        variantIndex: number
        cell: CellState
    }>
    doneCount: number
    failureEntries: Array<{
        row: GridRow
        rowIndex: number
        variant: PlaygroundVariant
        variantIndex: number
        cell: CellState
    }>
    inFlightCount: number
    avgLatency?: number
    onOpenRow: (rowId: string) => void
}) {
    const total = activeEntries.length
    const passCount = activeEntries.filter((entry) => {
        const scoreCount = scoreEntries(entry.cell.scores).length
        return entry.cell.status === 'done' && scoreCount > 0 && !isFailure(entry.cell)
    }).length

    return (
        <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_320px] overflow-hidden">
            <div className="min-w-0 overflow-auto">
                <div className="grid grid-cols-4 border-b border-brand-main-800">
                    <MetricCell label="Completed" value={`${doneCount}/${total || 0}`} />
                    <MetricCell label="Running" value={String(inFlightCount)} />
                    <MetricCell label="Passes" value={String(passCount)} />
                    <MetricCell label="Avg latency" value={formatDuration(avgLatency)} />
                </div>
                <table className="w-full border-collapse text-sm">
                    <thead className="sticky top-0 z-10 bg-brand-main-950">
                        <tr className="border-b border-brand-main-800 text-left text-xs text-white/45 light:text-black/45">
                            <th className="px-4 py-2 font-medium">Task</th>
                            <th className="px-4 py-2 font-medium">Status</th>
                            <th className="px-4 py-2 font-medium">Pass rate</th>
                            <th className="px-4 py-2 font-medium">Latency</th>
                            <th className="px-4 py-2 font-medium">Trace coverage</th>
                        </tr>
                    </thead>
                    <tbody>
                        {variants.map((variant, index) => {
                            const taskEntries = rows.map((row) => {
                                const entry = activeEntries.find(
                                    (candidate) =>
                                        candidate.row.id === row.id &&
                                        candidate.variant.id === variant.id,
                                )
                                return entry?.cell ?? IDLE_CELL_VIEW
                            })
                            const done = taskEntries.filter((cell) => cell.status === 'done')
                            const failed = taskEntries.filter(isFailure)
                            const scored = done.filter((cell) => scoreEntries(cell.scores).length > 0)
                            const passed = scored.filter((cell) => !isFailure(cell))
                            const latencyCells = done.filter((cell) => cell.durationMs !== undefined)
                            const latency =
                                latencyCells.length === 0
                                    ? undefined
                                    : Math.round(
                                          latencyCells.reduce(
                                              (sum, cell) => sum + (cell.durationMs ?? 0),
                                              0,
                                          ) / latencyCells.length,
                                      )
                            const traced = taskEntries.filter((cell) => cell.traceId)

                            return (
                                <tr key={variant.id} className="border-b border-brand-main-800/70">
                                    <td className="px-4 py-3">
                                        <div className="flex items-center gap-2">
                                            <span className="text-xs font-semibold text-brand-secondary-300">
                                                {taskLabel(index)}
                                            </span>
                                            <div className="min-w-0">
                                                <div className="truncate text-sm text-white/85 light:text-brand-main-50">
                                                    {variant.model || variant.type}
                                                </div>
                                                <div className="truncate text-xs text-white/35 light:text-black/35">
                                                    {variant.type === 'prompt' ? 'Prompt task' : variant.type}
                                                </div>
                                            </div>
                                        </div>
                                    </td>
                                    <td className="px-4 py-3 text-xs text-white/60 light:text-black/60">
                                        {done.length}/{rows.length} done
                                        {failed.length > 0 && (
                                            <span className="ml-2 text-rose-300">{failed.length} fail</span>
                                        )}
                                    </td>
                                    <td className="px-4 py-3 text-xs text-white/60 light:text-black/60">
                                        {scored.length
                                            ? `${Math.round((passed.length / scored.length) * 100)}%`
                                            : 'No scores'}
                                    </td>
                                    <td className="px-4 py-3 text-xs text-white/60 light:text-black/60">
                                        {formatDuration(latency)}
                                    </td>
                                    <td className="px-4 py-3 text-xs text-white/60 light:text-black/60">
                                        {traced.length}/{rows.length}
                                    </td>
                                </tr>
                            )
                        })}
                    </tbody>
                </table>
            </div>

            <aside className="min-h-0 overflow-auto border-l border-brand-main-800">
                <div className="border-b border-brand-main-800 px-4 py-2 text-xs font-medium text-white/60 light:text-black/60">
                    Failure stream
                </div>
                {failureEntries.length === 0 ? (
                    <div className="px-4 py-5 text-xs leading-relaxed text-white/35 light:text-black/35">
                        No failing cells recorded for this run.
                    </div>
                ) : (
                    <div className="divide-y divide-brand-main-800/70">
                        {failureEntries.slice(0, 12).map((entry) => (
                            <FailureItem
                                key={`${entry.row.id}:${entry.variant.id}`}
                                entry={entry}
                                onOpenRow={onOpenRow}
                            />
                        ))}
                    </div>
                )}
            </aside>
        </div>
    )
}

function MetricCell({ label, value }: { label: string; value: string }) {
    return (
        <div className="border-r border-brand-main-800 px-4 py-3 last:border-r-0">
            <div className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
                {label}
            </div>
            <div className="mt-1 text-lg font-semibold text-white/85 light:text-brand-main-50">
                {value}
            </div>
        </div>
    )
}

function GridView({
    rows,
    variants,
    onOpenRow,
}: {
    rows: GridRow[]
    variants: PlaygroundVariant[]
    onOpenRow: (rowId: string) => void
}) {
    const removeRow = usePlaygroundStore((s) => s.removeRow)
    const setRowInput = usePlaygroundStore((s) => s.setRowInput)

    return (
        <div className="min-h-0 flex-1 overflow-auto">
            <table className="w-full min-w-[760px] border-collapse text-sm">
                <thead className="sticky top-0 z-10 bg-brand-main-950">
                    <tr className="border-b border-brand-main-800 text-left text-xs text-white/45 light:text-black/45">
                        <th className="w-[300px] px-4 py-2 font-medium">Input</th>
                        {variants.map((variant, index) => (
                            <th
                                key={variant.id}
                                className="min-w-[240px] border-l border-brand-main-800 px-4 py-2 font-medium"
                            >
                                <span className="text-brand-secondary-300">{taskLabel(index)}</span>
                                <span className="ml-2 text-white/65 light:text-black/65">
                                    {variant.model || variant.type}
                                </span>
                            </th>
                        ))}
                    </tr>
                </thead>
                <tbody>
                    {rows.map((row, rowIndex) => (
                        <tr
                            key={row.id}
                            onClick={() => onOpenRow(row.id)}
                            className="group cursor-pointer border-b border-brand-main-800/70 align-top hover:bg-brand-main-900/35"
                        >
                            <td className="w-[300px] px-4 py-3">
                                <div className="flex items-start gap-2">
                                    <span className="pt-1 text-xs tabular-nums text-white/25 light:text-black/25">
                                        {rowIndex + 1}
                                    </span>
                                    <textarea
                                        value={row.input}
                                        onChange={(event) => setRowInput(row.id, event.target.value)}
                                        onClick={(event) => event.stopPropagation()}
                                        rows={2}
                                        placeholder="Row input…"
                                        className="min-h-[2.5rem] flex-1 resize-y bg-transparent text-xs leading-relaxed text-white/85 outline-none placeholder:text-white/25 light:text-black/85 light:placeholder:text-black/25"
                                    />
                                    <button
                                        type="button"
                                        onClick={(event) => {
                                            event.stopPropagation()
                                            removeRow(row.id)
                                        }}
                                        className="pt-1 text-white/0 transition-colors group-hover:text-white/35 hover:!text-rose-300 light:text-black/0 light:group-hover:text-black/35"
                                        aria-label="Remove row"
                                    >
                                        <X className="h-3 w-3" />
                                    </button>
                                </div>
                            </td>
                            {variants.map((variant) => (
                                <td
                                    key={variant.id}
                                    className="min-w-[240px] border-l border-brand-main-800 px-4 py-3"
                                >
                                    <CellView rowId={row.id} variant={variant} />
                                </td>
                            ))}
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    )
}

function FailuresView({
    failureEntries,
    onOpenRow,
}: {
    failureEntries: Array<{
        row: GridRow
        rowIndex: number
        variant: PlaygroundVariant
        variantIndex: number
        cell: CellState
    }>
    onOpenRow: (rowId: string) => void
}) {
    if (failureEntries.length === 0) {
        return (
            <div className="flex min-h-0 flex-1 items-center justify-center p-8 text-center">
                <div>
                    <AlertTriangle className="mx-auto mb-3 h-7 w-7 text-white/20 light:text-black/20" />
                    <div className="text-sm font-medium text-white/75 light:text-brand-main-50">
                        No failures in the current results
                    </div>
                </div>
            </div>
        )
    }

    return (
        <div className="min-h-0 flex-1 overflow-auto">
            <div className="divide-y divide-brand-main-800/70">
                {failureEntries.map((entry) => (
                    <FailureItem
                        key={`${entry.row.id}:${entry.variant.id}`}
                        entry={entry}
                        wide
                        onOpenRow={onOpenRow}
                    />
                ))}
            </div>
        </div>
    )
}

function FailureItem({
    entry,
    wide = false,
    onOpenRow,
}: {
    entry: {
        row: GridRow
        rowIndex: number
        variant: PlaygroundVariant
        variantIndex: number
        cell: CellState
    }
    wide?: boolean
    onOpenRow?: (rowId: string) => void
}) {
    const scoreText = scoreEntries(entry.cell.scores)
        .map(([name, value]) => `${name}: ${String(value)}`)
        .join(' · ')

    return (
        <button
            type="button"
            onClick={() => onOpenRow?.(entry.row.id)}
            className={`block w-full text-left transition-colors hover:bg-brand-main-900/35 ${
                wide ? 'px-4 py-3' : 'px-4 py-3'
            }`}
        >
            <div className="flex items-center gap-2 text-xs">
                <span className="font-semibold text-brand-secondary-300">
                    {taskLabel(entry.variantIndex)}
                </span>
                <span className="text-white/65 light:text-black/65">Row {entry.rowIndex + 1}</span>
                <span className="ml-auto text-rose-300">
                    {entry.cell.status === 'error' ? 'Error' : 'Failed score'}
                </span>
            </div>
            <div className="mt-2 line-clamp-2 text-xs leading-relaxed text-white/50 light:text-black/50">
                {entry.cell.error || scoreText || entry.cell.text || entry.row.input}
            </div>
            {entry.cell.traceId && (
                <Link
                    to="/observability/traces"
                    search={(params: Record<string, unknown>) => ({ ...params, trace: entry.cell.traceId })}
                    onClick={(event) => event.stopPropagation()}
                    className="mt-2 inline-flex text-xs text-brand-secondary-300 hover:text-brand-secondary-200"
                >
                    View trace
                </Link>
            )}
        </button>
    )
}

function DiffView({
    rows,
    variants,
    cells,
    onOpenRow,
}: {
    rows: GridRow[]
    variants: PlaygroundVariant[]
    cells: Record<string, CellState>
    onOpenRow: (rowId: string) => void
}) {
    const base = variants[0]
    const compare = variants[1]

    if (!base || !compare) {
        return (
            <div className="flex min-h-0 flex-1 items-center justify-center p-8 text-center">
                <div className="max-w-sm">
                    <GitCompare className="mx-auto mb-3 h-7 w-7 text-white/20 light:text-black/20" />
                    <div className="text-sm font-medium text-white/75 light:text-brand-main-50">
                        Add a second task to compare outputs
                    </div>
                </div>
            </div>
        )
    }

    return (
        <div className="min-h-0 flex-1 overflow-auto">
            <table className="w-full min-w-[760px] border-collapse text-sm">
                <thead className="sticky top-0 z-10 bg-brand-main-950">
                    <tr className="border-b border-brand-main-800 text-left text-xs text-white/45 light:text-black/45">
                        <th className="w-[240px] px-4 py-2 font-medium">Input</th>
                        <th className="border-l border-brand-main-800 px-4 py-2 font-medium">
                            {taskLabel(0)} · {base.model || base.type}
                        </th>
                        <th className="border-l border-brand-main-800 px-4 py-2 font-medium">
                            {taskLabel(1)} · {compare.model || compare.type}
                        </th>
                    </tr>
                </thead>
                <tbody>
                    {rows.map((row, rowIndex) => {
                        const baseCell = cells[cellKey(row.id, base.id)] ?? IDLE_CELL_VIEW
                        const compareCell = cells[cellKey(row.id, compare.id)] ?? IDLE_CELL_VIEW
                        return (
                            <tr
                                key={row.id}
                                onClick={() => onOpenRow(row.id)}
                                className="cursor-pointer border-b border-brand-main-800/70 align-top hover:bg-brand-main-900/35"
                            >
                                <td className="w-[240px] px-4 py-3">
                                    <div className="text-xs tabular-nums text-white/25 light:text-black/25">
                                        Row {rowIndex + 1}
                                    </div>
                                    <div className="mt-1 text-xs leading-relaxed text-white/65 light:text-black/65">
                                        {row.input || 'Empty input'}
                                    </div>
                                </td>
                                <td className="border-l border-brand-main-800 px-4 py-3">
                                    <DiffOutput cell={baseCell} />
                                </td>
                                <td className="border-l border-brand-main-800 px-4 py-3">
                                    <DiffOutput cell={compareCell} />
                                </td>
                            </tr>
                        )
                    })}
                </tbody>
            </table>
        </div>
    )
}

function DiffOutput({ cell }: { cell: CellState }) {
    if (cell.status === 'idle') {
        return <span className="text-xs text-white/25 light:text-black/25">Not run</span>
    }
    if (cell.status === 'queued') {
        return <span className="text-xs text-white/35 light:text-black/35">Queued</span>
    }
    if (cell.status === 'running' || cell.status === 'scoring') {
        return <span className="text-xs text-brand-secondary-300">Running</span>
    }
    if (cell.status === 'error') {
        return <span className="text-xs text-rose-300">{cell.error ?? 'Error'}</span>
    }
    if (cell.status === 'aborted') {
        return <span className="text-xs text-white/35 light:text-black/35">Stopped</span>
    }

    return (
        <div className="space-y-2">
            <div className="whitespace-pre-wrap text-xs leading-relaxed text-white/85 light:text-black/85">
                {cell.text || 'Empty output'}
            </div>
            <ScoreMarks scores={cell.scores} />
        </div>
    )
}

function RowDetailDrawer({
    row,
    rowIndex,
    rowCount,
    variants,
    cells,
    layout,
    onLayoutChange,
    onClose,
    onPrevious,
    onNext,
}: {
    row: GridRow
    rowIndex: number
    rowCount: number
    variants: PlaygroundVariant[]
    cells: Record<string, CellState>
    layout: RowDrawerLayout
    onLayoutChange: (layout: RowDrawerLayout) => void
    onClose: () => void
    onPrevious: () => void
    onNext: () => void
}) {
    const runCell = usePlaygroundRunStore((s) => s.runCell)
    const metadata = usePlaygroundMetadata()
    const rowCells = variants.map((variant, variantIndex) => ({
        variant,
        variantIndex,
        cell: cells[cellKey(row.id, variant.id)] ?? IDLE_CELL_VIEW,
    }))
    const running = rowCells.some(({ cell }) =>
        ['queued', 'running', 'scoring'].includes(cell.status),
    )

    const runRow = async () => {
        await Promise.allSettled(
            variants
                .filter((variant) => variant.model.trim())
                .map((variant) => runCell(row, variant, metadata)),
        )
    }

    return (
        <div className="fixed inset-x-3 bottom-3 top-12 z-50 flex flex-col overflow-hidden rounded-md border border-brand-main-700 bg-brand-main-950 shadow-[0_24px_80px_rgba(0,0,0,0.55)]">
            <div className="flex h-11 shrink-0 items-center gap-2 border-b border-brand-main-800 bg-brand-main-900/40 px-3">
                <button
                    type="button"
                    onClick={onPrevious}
                    disabled={rowIndex <= 0}
                    className="p-1 text-white/45 transition-colors hover:text-white disabled:text-white/15 light:text-black/45 light:hover:text-brand-main-50 light:disabled:text-black/15"
                    aria-label="Previous row"
                >
                    <ChevronLeft className="h-4 w-4" />
                </button>
                <button
                    type="button"
                    onClick={onNext}
                    disabled={rowIndex >= rowCount - 1}
                    className="p-1 text-white/45 transition-colors hover:text-white disabled:text-white/15 light:text-black/45 light:hover:text-brand-main-50 light:disabled:text-black/15"
                    aria-label="Next row"
                >
                    <ChevronRight className="h-4 w-4" />
                </button>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">
                    Row {rowIndex + 1} of {rowCount}
                </div>
                <LayoutMenu layout={layout} onLayoutChange={onLayoutChange} />
                <div className="ml-auto flex items-center gap-3">
                    <button
                        type="button"
                        onClick={() => void runRow()}
                        disabled={running}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-secondary-500/50 bg-brand-secondary-500/10 px-2 py-1 text-xs text-brand-secondary-200 transition-colors hover:text-brand-secondary-100 disabled:border-brand-main-800 disabled:bg-transparent disabled:text-white/25"
                    >
                        <Play className="h-3.5 w-3.5" />
                        {running ? 'Running' : 'Run row'}
                    </button>
                    <button
                        type="button"
                        onClick={onClose}
                        className="p-1 text-white/45 transition-colors hover:text-white light:text-black/45 light:hover:text-brand-main-50"
                        aria-label="Close row detail"
                    >
                        <X className="h-4 w-4" />
                    </button>
                </div>
            </div>

            <div className="grid min-h-0 flex-1 grid-cols-[320px_minmax(0,1fr)]">
                <RowEditor row={row} rowIndex={rowIndex} />
                <div className="min-w-0 overflow-x-auto overflow-y-hidden">
                    <div
                        className={
                            layout === 'trace'
                                ? 'grid h-full min-w-max grid-cols-[280px_repeat(var(--task-count),minmax(340px,1fr))]'
                                : 'grid h-full min-w-max grid-cols-[repeat(var(--task-count),minmax(360px,1fr))]'
                        }
                        style={{ '--task-count': variants.length } as React.CSSProperties}
                    >
                        {layout === 'trace' && <TraceRail rowCells={rowCells} />}
                        {rowCells.map(({ variant, variantIndex, cell }) => (
                            <TaskResultPane
                                key={variant.id}
                                row={row}
                                variant={variant}
                                variantIndex={variantIndex}
                                cell={cell}
                                layout={layout}
                            />
                        ))}
                    </div>
                </div>
            </div>
        </div>
    )
}

function LayoutMenu({
    layout,
    onLayoutChange,
}: {
    layout: RowDrawerLayout
    onLayoutChange: (layout: RowDrawerLayout) => void
}) {
    const active = ROW_LAYOUTS.find((candidate) => candidate.id === layout) ?? ROW_LAYOUTS[0]
    const Icon = active.icon

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    className="ml-2 inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/60 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/60 light:hover:text-brand-main-50"
                >
                    <Icon className="h-3.5 w-3.5" />
                    {active.label}
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-44">
                {ROW_LAYOUTS.map(({ id, label, icon: MenuIcon }) => (
                    <DropdownMenuItem key={id} onSelect={() => onLayoutChange(id)}>
                        <MenuIcon className="mr-2 h-3.5 w-3.5" />
                        {label}
                    </DropdownMenuItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled>Thread</DropdownMenuItem>
                <DropdownMenuItem disabled>Views</DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    )
}

function RowEditor({ row, rowIndex }: { row: GridRow; rowIndex: number }) {
    const setRowInput = usePlaygroundStore((s) => s.setRowInput)
    const setRowExpected = usePlaygroundStore((s) => s.setRowExpected)
    const setRowMetadata = usePlaygroundStore((s) => s.setRowMetadata)

    return (
        <aside className="min-h-0 overflow-auto border-r border-brand-main-800">
            <div className="border-b border-brand-main-800 px-3 py-3">
                <div className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
                    Edit dataset row
                </div>
            </div>
            <div className="space-y-4 px-3 py-3">
                <DatasetTextArea
                    label="Input"
                    value={row.input}
                    rows={3}
                    onChange={(value) => setRowInput(row.id, value)}
                />
                <DatasetTextArea
                    label="Expected"
                    value={row.expected ?? ''}
                    rows={3}
                    onChange={(value) => setRowExpected(row.id, value)}
                />
                <DatasetTextArea
                    label="Metadata"
                    value={row.metadata ?? ''}
                    rows={4}
                    placeholder="null"
                    onChange={(value) => setRowMetadata(row.id, value)}
                />
                <section className="space-y-2">
                    <DrawerSectionTitle icon={Activity}>Activity</DrawerSectionTitle>
                    <div className="border-l border-brand-main-700 pl-3 text-xs leading-relaxed text-white/40 light:text-black/40">
                        Row {rowIndex + 1} opened in playground.
                        {row.datasetItemId && (
                            <div className="mt-1 truncate">Dataset item {row.datasetItemId}</div>
                        )}
                        {row.sourceTraceId && (
                            <Link
                                to="/observability/traces"
                                search={(params: Record<string, unknown>) => ({
                                    ...params,
                                    trace: row.sourceTraceId,
                                })}
                                className="mt-1 block truncate text-brand-secondary-300 hover:text-brand-secondary-200"
                            >
                                Source trace {row.sourceTraceId.slice(0, 12)}
                            </Link>
                        )}
                    </div>
                </section>
            </div>
        </aside>
    )
}

function DatasetTextArea({
    label,
    value,
    rows,
    placeholder,
    onChange,
}: {
    label: string
    value: string
    rows: number
    placeholder?: string
    onChange: (value: string) => void
}) {
    return (
        <label className="block">
            <div className="mb-1.5 text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
                {label}
            </div>
            <textarea
                value={value}
                rows={rows}
                placeholder={placeholder}
                onChange={(event) => onChange(event.target.value)}
                className="w-full resize-y rounded border border-brand-main-800 bg-brand-main-900/25 px-2 py-2 text-xs leading-relaxed text-white/80 outline-none placeholder:text-white/25 focus:border-brand-secondary-500/60 light:text-black/80 light:placeholder:text-black/25"
            />
        </label>
    )
}

function TraceRail({
    rowCells,
}: {
    rowCells: Array<{
        variant: PlaygroundVariant
        variantIndex: number
        cell: CellState
    }>
}) {
    const totalTraces = rowCells.filter(({ cell }) => cell.traceId).length

    return (
        <aside className="min-h-0 overflow-auto border-r border-brand-main-800 px-3 py-3">
            <div className="mb-3 flex items-center justify-between gap-2">
                <DrawerSectionTitle icon={ListTree}>Traces</DrawerSectionTitle>
                <span className="text-[10px] text-white/30 light:text-black/30">
                    {totalTraces} traces
                </span>
            </div>
            <div className="space-y-2.5">
                {rowCells.map(({ variant, variantIndex, cell }) => (
                    <TraceRailItem
                        key={variant.id}
                        variant={variant}
                        variantIndex={variantIndex}
                        cell={cell}
                    />
                ))}
            </div>
        </aside>
    )
}

function TraceRailItem({
    variant,
    variantIndex,
    cell,
}: {
    variant: PlaygroundVariant
    variantIndex: number
    cell: CellState
}) {
    const traceQuery = useQuery({
        queryKey: ['trace-spans', cell.traceId],
        queryFn: () => getTraceByID(cell.traceId!),
        enabled: !!cell.traceId,
        staleTime: 30_000,
    })
    const spans = traceQuery.data ?? []
    const rootSpan =
        spans.find((span) => !span.parentSpanId || !spans.some((parent) => parent.spanId === span.parentSpanId)) ??
        spans[0]
    const rootConfig = rootSpan ? getSpanDisplayConfig(rootSpan) : undefined

    return (
        <div className="rounded border border-brand-main-800 bg-brand-main-900/25 p-2">
            <div className="flex items-start gap-2">
                <span className={`mt-1 h-2 w-2 shrink-0 rounded-full ${taskDotClass(variantIndex)}`} />
                <div className="min-w-0 flex-1">
                    <div className="truncate text-xs font-medium text-white/75 light:text-brand-main-50">
                        {taskLabel(variantIndex)} · {rootConfig?.title ?? (variant.model || variant.type)}
                    </div>
                    <div className="mt-0.5 truncate text-[10px] text-white/35 light:text-black/35">
                        {cell.traceId
                            ? `${traceQuery.isLoading ? 'Loading' : spans.length} spans · ${cell.traceId.slice(0, 12)}`
                            : cell.status === 'idle'
                              ? 'No trace captured'
                              : cell.status}
                    </div>
                </div>
                {cell.traceId && (
                    <Link
                        to="/observability/traces"
                        search={(params: Record<string, unknown>) => ({ ...params, trace: cell.traceId })}
                        className="shrink-0 text-[10px] text-brand-secondary-300 hover:text-brand-secondary-200"
                    >
                        Open
                    </Link>
                )}
            </div>
            {traceQuery.isLoading ? (
                <div className="mt-2 h-8 rounded bg-brand-main-950/50" />
            ) : spans.length > 0 ? (
                <MiniTraceWaterfall spans={spans} />
            ) : (
                <div className="mt-2 text-[10px] leading-relaxed text-white/30 light:text-black/30">
                    {cell.traceId ? 'Trace returned no spans yet.' : 'Run this row to collect a trace.'}
                </div>
            )}
        </div>
    )
}

function TaskResultPane({
    row,
    variant,
    variantIndex,
    cell,
    layout,
}: {
    row: GridRow
    variant: PlaygroundVariant
    variantIndex: number
    cell: CellState
    layout: RowDrawerLayout
}) {
    const spansQuery = useQuery({
        queryKey: ['trace-spans', cell.traceId],
        queryFn: () => getTraceByID(cell.traceId!),
        enabled: !!cell.traceId,
        staleTime: 30_000,
    })
    const spans = spansQuery.data ?? []

    return (
        <section className="min-h-0 overflow-auto border-r border-brand-main-800 px-3 py-3 last:border-r-0">
            <div className="mb-3 flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <span className={`h-2 w-2 rounded-full ${taskDotClass(variantIndex)}`} />
                        <span className="truncate text-sm font-semibold text-white light:text-brand-main-50">
                            {variant.model || variant.type}
                        </span>
                    </div>
                    <div className="mt-0.5 text-[10px] text-white/35 light:text-black/35">
                        {variantIndex === 0 ? 'Base' : 'Comparison'}
                    </div>
                </div>
                <button
                    type="button"
                    className="p-1 text-white/35 transition-colors hover:text-brand-secondary-300"
                    title="Run this task for the selected row"
                >
                    <Play className="h-3.5 w-3.5" />
                </button>
            </div>

            <CellMetricLine cell={cell} spans={spans} />

            {layout === 'timeline' ? (
                <TimelinePanel
                    cell={cell}
                    spans={spans}
                    spansLoading={spansQuery.isLoading}
                />
            ) : (
                <TracePanel row={row} cell={cell} spans={spans} spansLoading={spansQuery.isLoading} />
            )}
        </section>
    )
}

function TracePanel({
    row,
    cell,
    spans,
    spansLoading,
}: {
    row: GridRow
    cell: CellState
    spans: Span[]
    spansLoading: boolean
}) {
    return (
        <div className="space-y-4">
            <section className="space-y-2">
                <div className="flex items-center justify-between gap-2 border-b border-brand-main-800 pb-1">
                    <DrawerSectionTitle icon={MessageSquare}>eval</DrawerSectionTitle>
                    {cell.traceId && (
                        <Link
                            to="/observability/traces"
                            search={(params: Record<string, unknown>) => ({
                                ...params,
                                trace: cell.traceId,
                            })}
                            className="text-[10px] text-brand-secondary-300 hover:text-brand-secondary-200"
                        >
                            Open trace
                        </Link>
                    )}
                </div>
                <div className="flex items-center gap-3 text-[10px] text-white/35 light:text-black/35">
                    <span>Messages</span>
                    <span>Details</span>
                    <span>Raw</span>
                </div>
            </section>

            <section className="space-y-2">
                <DrawerSectionTitle icon={ThumbsUp}>Annotations</DrawerSectionTitle>
                <div className="text-xs text-white/45 light:text-black/45">Rate this span&apos;s output quality</div>
                <div className="flex items-center gap-2">
                    <button type="button" className="text-white/45 hover:text-emerald-300">
                        <ThumbsUp className="h-3.5 w-3.5" />
                    </button>
                    <button type="button" className="text-white/45 hover:text-rose-300">
                        <ThumbsDown className="h-3.5 w-3.5" />
                    </button>
                </div>
            </section>

            <section className="space-y-2">
                <DrawerSectionTitle icon={FileText}>Input</DrawerSectionTitle>
                <div className="whitespace-pre-wrap text-xs leading-relaxed text-white/60 light:text-black/60">
                    {row.input || 'Empty input'}
                </div>
            </section>

            <section className="space-y-2">
                <DrawerSectionTitle icon={MessageSquare}>Output</DrawerSectionTitle>
                <OutputBlock cell={cell} />
            </section>

            <section className="space-y-2">
                <DrawerSectionTitle icon={ListTree}>Trace waterfall</DrawerSectionTitle>
                {spansLoading ? (
                    <div className="h-28 rounded border border-brand-main-800 bg-brand-main-900/20" />
                ) : spans.length > 0 ? (
                    <TraceWaterfall spans={spans} compact />
                ) : (
                    <div className="text-xs text-white/35 light:text-black/35">
                        {cell.traceId ? 'No spans returned for this trace yet.' : 'Run this cell to capture a trace.'}
                    </div>
                )}
            </section>
        </div>
    )
}

function TimelinePanel({
    cell,
    spans,
    spansLoading,
}: {
    cell: CellState
    spans: Span[]
    spansLoading: boolean
}) {
    return (
        <div className="space-y-3">
            <div className="border-b border-brand-main-800 pb-2">
                <DrawerSectionTitle icon={Clock3}>Trace timeline</DrawerSectionTitle>
                <div className="mt-1 text-[10px] text-white/35 light:text-black/35">
                    {cell.traceId ? `Trace ${cell.traceId.slice(0, 12)}` : 'No trace captured'}
                </div>
            </div>
            {spansLoading ? (
                <div className="h-36 rounded border border-brand-main-800 bg-brand-main-900/20" />
            ) : spans.length > 0 ? (
                <TraceWaterfall spans={spans} />
            ) : (
                <div className="rounded border border-brand-main-800 bg-brand-main-900/20 px-3 py-4 text-xs leading-relaxed text-white/35 light:text-black/35">
                    {cell.traceId ? 'No spans returned for this trace yet.' : 'Run this cell to capture trace spans.'}
                </div>
            )}
        </div>
    )
}

type WaterfallRow = {
    span: Span
    depth: number
    startNs: bigint
    durationNs: bigint
    endNs: bigint
}

function TraceWaterfall({ spans, compact = false }: { spans: Span[]; compact?: boolean }) {
    const rows = useMemo(() => buildWaterfallRows(spans), [spans])
    if (rows.length === 0) {
        return (
            <div className="rounded border border-brand-main-800 bg-brand-main-900/20 px-3 py-4 text-xs text-white/35 light:text-black/35">
                No trace spans available.
            </div>
        )
    }

    const startNs = rows.reduce((min, row) => (row.startNs < min ? row.startNs : min), rows[0].startNs)
    const endNs = rows.reduce((max, row) => (row.endNs > max ? row.endNs : max), rows[0].endNs)
    const totalNs = endNs > startNs ? endNs - startNs : BigInt(1)
    const visibleRows = compact ? rows.slice(0, 8) : rows

    return (
        <div className="overflow-hidden rounded border border-brand-main-800 bg-brand-main-900/20">
            <div className="grid grid-cols-[minmax(115px,40%)_minmax(120px,1fr)_54px] border-b border-brand-main-800 bg-brand-main-950/30 px-2 py-1.5 text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
                <span>Span</span>
                <span>Waterfall</span>
                <span className="text-right">Time</span>
            </div>
            <div className="divide-y divide-brand-main-800/70">
                {visibleRows.map((row) => (
                    <WaterfallSpanRow
                        key={row.span.spanId}
                        row={row}
                        traceStartNs={startNs}
                        traceTotalNs={totalNs}
                        compact={compact}
                    />
                ))}
            </div>
            {compact && rows.length > visibleRows.length && (
                <div className="border-t border-brand-main-800 px-2 py-1.5 text-[10px] text-white/30 light:text-black/30">
                    +{rows.length - visibleRows.length} more spans in this trace
                </div>
            )}
        </div>
    )
}

function MiniTraceWaterfall({ spans }: { spans: Span[] }) {
    const rows = useMemo(() => buildWaterfallRows(spans).slice(0, 5), [spans])
    if (!rows.length) return null
    const startNs = rows.reduce((min, row) => (row.startNs < min ? row.startNs : min), rows[0].startNs)
    const endNs = rows.reduce((max, row) => (row.endNs > max ? row.endNs : max), rows[0].endNs)
    const totalNs = endNs > startNs ? endNs - startNs : BigInt(1)

    return (
        <div className="mt-2 space-y-1">
            {rows.map((row) => {
                const left = waterfallPercent(row.startNs - startNs, totalNs)
                const width = Math.min(100 - left, Math.max(2, waterfallPercent(row.durationNs, totalNs)))
                const config = getSpanDisplayConfig(row.span)
                const colors = spanWaterfallColors(row.span, config.category)

                return (
                    <div key={row.span.spanId} className="grid grid-cols-[1fr_42px] items-center gap-2">
                        <div className="relative h-2 overflow-hidden rounded bg-brand-main-950/60">
                            <div
                                className="absolute inset-y-0 rounded"
                                style={{
                                    left: `${left}%`,
                                    width: `${width}%`,
                                    backgroundColor: colors.fill,
                                }}
                            />
                        </div>
                        <span className="truncate text-[10px] text-white/30 light:text-black/30">
                            {formatTraceDuration(row.durationNs)}
                        </span>
                    </div>
                )
            })}
        </div>
    )
}

function WaterfallSpanRow({
    row,
    traceStartNs,
    traceTotalNs,
    compact,
}: {
    row: WaterfallRow
    traceStartNs: bigint
    traceTotalNs: bigint
    compact: boolean
}) {
    const config = getSpanDisplayConfig(row.span)
    const colors = spanWaterfallColors(row.span, config.category)
    const left = waterfallPercent(row.startNs - traceStartNs, traceTotalNs)
    const width = Math.min(
        100 - left,
        Math.max(compact ? 2.5 : 1.2, waterfallPercent(row.durationNs, traceTotalNs)),
    )
    const errored = (row.span.statusCode ?? '').toUpperCase() === 'ERROR'

    return (
        <div className="grid grid-cols-[minmax(115px,40%)_minmax(120px,1fr)_54px] items-center gap-2 px-2 py-1.5 text-xs">
            <div
                className="flex min-w-0 items-center gap-1.5"
                style={{ paddingLeft: Math.min(row.depth * 12, 48) }}
                title={config.subtitle ? `${config.title} · ${config.subtitle}` : config.title}
            >
                <span
                    className="h-2 w-2 shrink-0 rounded-full"
                    style={{ backgroundColor: colors.fill }}
                />
                <span className="min-w-0 flex-1 truncate text-white/65 light:text-black/65">
                    {config.title}
                </span>
            </div>
            <div className="relative h-5 overflow-hidden rounded bg-brand-main-950/55">
                {[0, 25, 50, 75, 100].map((mark) => (
                    <span
                        key={mark}
                        className="absolute inset-y-0 border-l border-white/5 light:border-black/5"
                        style={{ left: `${mark}%` }}
                    />
                ))}
                <div
                    className="absolute inset-y-1 rounded"
                    style={{
                        left: `${left}%`,
                        width: `${width}%`,
                        backgroundColor: colors.fill,
                        border: `1px solid ${colors.border}`,
                    }}
                    title={`${categoryLabels[config.category]} · ${formatTraceDuration(row.durationNs)}`}
                />
            </div>
            <span className={errored ? 'text-right text-[10px] text-rose-300' : 'text-right text-[10px] text-white/35 light:text-black/35'}>
                {formatTraceDuration(row.durationNs)}
            </span>
        </div>
    )
}

function buildWaterfallRows(spans: Span[]): WaterfallRow[] {
    if (spans.length === 0) return []

    const startMap = new Map<string, bigint>()
    const durationMap = new Map<string, bigint>()
    const validStarts: bigint[] = []

    for (const span of spans) {
        const start = spanTimestampNs(span)
        if (start !== null) validStarts.push(start)
    }

    const fallbackStart = validStarts.length > 0
        ? validStarts.reduce((min, start) => (start < min ? start : min))
        : BigInt(0)

    for (const span of spans) {
        startMap.set(span.spanId, spanTimestampNs(span) ?? fallbackStart)
        durationMap.set(span.spanId, spanDurationNs(span))
    }

    const byId = new Map(spans.map((span) => [span.spanId, span]))
    const children = new Map<string, Span[]>()
    const roots: Span[] = []

    for (const span of spans) {
        if (span.parentSpanId && byId.has(span.parentSpanId)) {
            const list = children.get(span.parentSpanId) ?? []
            list.push(span)
            children.set(span.parentSpanId, list)
        } else {
            roots.push(span)
        }
    }

    const byStart = (a: Span, b: Span) =>
        bigIntCompare(startMap.get(a.spanId) ?? fallbackStart, startMap.get(b.spanId) ?? fallbackStart)
    roots.sort(byStart)
    for (const list of children.values()) list.sort(byStart)

    const rows: WaterfallRow[] = []
    const seen = new Set<string>()
    const visit = (span: Span, depth: number) => {
        if (seen.has(span.spanId)) return
        seen.add(span.spanId)
        const startNs = startMap.get(span.spanId) ?? fallbackStart
        const durationNs = durationMap.get(span.spanId) ?? BigInt(0)
        rows.push({
            span,
            depth,
            startNs,
            durationNs,
            endNs: startNs + durationNs,
        })
        for (const child of children.get(span.spanId) ?? []) {
            visit(child, depth + 1)
        }
    }

    for (const root of roots) visit(root, 0)
    for (const span of spans) visit(span, 0)

    return rows
}

function spanTimestampNs(span: Span): bigint | null {
    if (!span.timestamp) return null
    const seconds = typeof span.timestamp.seconds === 'bigint'
        ? span.timestamp.seconds
        : BigInt(span.timestamp.seconds || 0)
    const nanos = typeof span.timestamp.nanos === 'bigint'
        ? span.timestamp.nanos
        : BigInt(span.timestamp.nanos || 0)
    const value = seconds * BigInt(1_000_000_000) + nanos
    return value > BigInt(0) ? value : null
}

function spanDurationNs(span: Span): bigint {
    const duration = span.duration
    if (duration === undefined) return BigInt(0)
    if (typeof duration === 'bigint') return duration > BigInt(0) ? duration : BigInt(0)
    if (!Number.isFinite(duration)) return BigInt(0)
    return BigInt(Math.max(0, Math.round(duration)))
}

function bigIntCompare(a: bigint, b: bigint): number {
    if (a < b) return -1
    if (a > b) return 1
    return 0
}

function waterfallPercent(value: bigint, total: bigint): number {
    if (total <= BigInt(0)) return 0
    const raw = (safeBigIntNumber(value) / safeBigIntNumber(total)) * 100
    if (!Number.isFinite(raw)) return 0
    return Math.max(0, Math.min(100, raw))
}

function safeBigIntNumber(value: bigint): number {
    const max = BigInt(Number.MAX_SAFE_INTEGER)
    const min = BigInt(Number.MIN_SAFE_INTEGER)
    if (value > max) return Number.MAX_SAFE_INTEGER
    if (value < min) return Number.MIN_SAFE_INTEGER
    return Number(value)
}

function spanWaterfallColors(
    span: Span,
    category: keyof typeof categoryTimelineColors,
): { fill: string; border: string } {
    return (span.statusCode ?? '').toUpperCase() === 'ERROR'
        ? timelineStatusColors.ERROR
        : categoryTimelineColors[category]
}

function OutputBlock({ cell }: { cell: CellState }) {
    if (cell.status === 'idle') {
        return <div className="text-xs text-white/35 light:text-black/35">No output yet.</div>
    }
    if (cell.status === 'queued' || cell.status === 'running' || cell.status === 'scoring') {
        return <div className="text-xs text-brand-secondary-300">Running</div>
    }
    if (cell.status === 'error') {
        return <div className="text-xs text-rose-300">{cell.error ?? 'Error'}</div>
    }
    if (cell.status === 'aborted') {
        return <div className="text-xs text-white/35 light:text-black/35">Stopped</div>
    }
    return (
        <div className="whitespace-pre-wrap text-xs leading-relaxed text-white/80 light:text-black/80">
            {cell.text || 'Empty output'}
        </div>
    )
}

function CellMetricLine({ cell, spans }: { cell: CellState; spans: Span[] }) {
    return (
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-brand-main-800 pb-2 text-[10px] text-white/35 light:text-black/35">
            <span>{cell.traceId ? 'Trace captured' : 'No trace'}</span>
            <span>{formatDuration(cell.durationMs)}</span>
            <span>{spans.length || (cell.status === 'done' ? 2 : 0)} spans</span>
            {scoreEntries(cell.scores).map(([name, value]) => (
                <span key={name} className={scoreFailed(value) ? 'text-rose-300' : 'text-emerald-300'}>
                    {name}: {String(value)}
                </span>
            ))}
        </div>
    )
}

function DrawerSectionTitle({
    icon: Icon,
    children,
}: {
    icon: typeof Activity
    children: React.ReactNode
}) {
    return (
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
            <Icon className="h-3 w-3" />
            {children}
        </div>
    )
}

function taskDotClass(index: number): string {
    const colors = [
        'bg-brand-secondary-400',
        'bg-violet-400',
        'bg-blue-400',
        'bg-orange-400',
        'bg-lime-400',
        'bg-fuchsia-400',
    ]
    return colors[index % colors.length]
}

/** Renders one grid cell's live run + scores. */
function CellView({ rowId, variant }: { rowId: string; variant: PlaygroundVariant }) {
    const cell = useCellState(rowId, variant.id)

    if (!variant.model.trim()) {
        return <span className="text-xs text-white/25 light:text-black/25">No model</span>
    }

    if (cell.status === 'idle' || cell.status === 'queued') {
        return (
            <span className="text-xs text-white/25 light:text-black/25">
                {cell.status === 'queued' ? 'Queued' : 'Not run'}
            </span>
        )
    }

    if (cell.status === 'error') {
        return <span className="text-xs text-rose-300">{cell.error ?? 'Error'}</span>
    }

    if (cell.status === 'aborted') {
        return <span className="text-xs text-white/35 light:text-black/35">Stopped</span>
    }

    return (
        <div className="flex flex-col gap-2">
            <div className="whitespace-pre-wrap text-xs leading-relaxed text-white/85 light:text-black/85">
                {cell.text}
                {cell.status === 'running' && (
                    <span className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-brand-secondary-400 align-text-bottom" />
                )}
            </div>
            {cell.status === 'scoring' && (
                <span className="text-[10px] text-white/35 light:text-black/35">Scoring</span>
            )}
            {cell.status === 'done' && <ScoreMarks scores={cell.scores} />}
            {(cell.status === 'done' || cell.status === 'scoring') && cell.traceId && (
                <Link
                    to="/observability/traces"
                    search={(params: Record<string, unknown>) => ({ ...params, trace: cell.traceId })}
                    className="text-[10px] text-white/35 hover:text-brand-secondary-300 light:text-black/35"
                >
                    View trace
                </Link>
            )}
        </div>
    )
}

function ScoreMarks({ scores }: { scores: CellState['scores'] }) {
    const entries = scoreEntries(scores)
    if (entries.length === 0) return null

    return (
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-[10px]">
            {entries.map(([name, value]) => {
                const pass = scorePassed(value)
                const fail = scoreFailed(value)
                const label = typeof value === 'number' ? value.toFixed(2) : String(value)
                const reason = scores?.[`${name}_reason`]
                return (
                    <span
                        key={name}
                        title={typeof reason === 'string' ? reason : undefined}
                        className={
                            pass
                                ? 'text-emerald-300'
                                : fail
                                  ? 'text-rose-300'
                                  : hasPositiveScore({ status: 'done', text: '', scores })
                                    ? 'text-brand-secondary-200'
                                    : 'text-white/45 light:text-black/45'
                        }
                    >
                        {name}: {label}
                    </span>
                )
            })}
        </div>
    )
}
