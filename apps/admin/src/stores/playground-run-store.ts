import { create } from 'zustand'
import { useSearch } from '@tanstack/react-router'
import { useMemo } from 'react'
import {
    deltaText,
    streamChatCompletion,
    type PlaygroundMessage,
} from '@/server/playground'
import { streamWorkflowExecution } from '@/server/workflow-execution'
import { getActiveOrgId } from '@/lib/active-org'
import {
    usePlaygroundStore,
    type PlaygroundVariant,
    type GridRow,
    type TemplatingEngine,
} from '@/stores/playground-store'
import { scoreOutput, type ScoreMap } from '@/server/scoring'

export type VariantRunStatus = 'idle' | 'running' | 'done' | 'error' | 'aborted'

export type VariantRunState = {
    status: VariantRunStatus
    text: string
    error?: string
    traceId?: string
    /** Time to first token, ms. */
    ttftMs?: number
    /** Total wall-clock duration, ms. */
    durationMs?: number
}

const IDLE: VariantRunState = { status: 'idle', text: '' }

// ── Evaluation grid cell runs ──

export type CellStatus = 'idle' | 'queued' | 'running' | 'scoring' | 'done' | 'error' | 'aborted'

export type CellState = {
    status: CellStatus
    text: string
    error?: string
    traceId?: string
    ttftMs?: number
    durationMs?: number
    /** Scores from the synchronous ScoreOutput RPC, once the cell finishes. */
    scores?: ScoreMap
}

const IDLE_CELL: CellState = { status: 'idle', text: '' }

/** Stable key for a grid cell (one generation per row × variant). */
export const cellKey = (rowId: string, variantId: string) => `${rowId}:${variantId}`

/** Max concurrent generations when running the whole grid. */
const GRID_CONCURRENCY = 6

// AbortControllers are intentionally kept outside the reactive store: they are
// imperative handles, not render state. Keyed by variantId (single-run) or by
// cellKey (grid runs).
const controllers = new Map<string, AbortController>()

// Signals the whole grid run to stop. Aborting in-flight cell streams is not
// enough — the worker pool must also stop pulling queued jobs, so workers check
// this before taking the next job.
let gridAbort: AbortController | null = null

/**
 * Substitute row values into a task's prompt according to its templating
 * engine. Mustache and Jinja both render `{{var}}` (and `{{ var }}` with
 * surrounding spaces); `none` disables substitution. Supported variables:
 * `input`, `expected`. Full Jinja control-flow is not yet implemented — only
 * variable interpolation, which is identical across the two engines.
 */
function applyRowTemplate(text: string, row: GridRow, engine: TemplatingEngine): string {
    if (engine === 'none') return text
    const vars: Record<string, string> = { input: row.input ?? '', expected: row.expected ?? '' }
    return text.replace(/\{\{\s*(input|expected)\s*\}\}/g, (_, name: string) => vars[name] ?? '')
}

function hasExplicitMessages(variant: PlaygroundVariant): boolean {
    return variant.messages.some((m) => m.text.trim())
}

/** A task's messages for a given row (non-empty, with variables substituted). */
function rowMessages(row: GridRow, variant: PlaygroundVariant): PlaygroundMessage[] {
    const rendered = variant.messages
        .map((m) => ({ role: m.role, text: applyRowTemplate(m.text, row, variant.templating) }))
        .filter((m) => m.text.trim())
    if (rendered.length > 0) return rendered
    if (variant.type === 'workflow' && row.input.trim()) return [{ role: 'user', text: row.input }]
    return []
}

/**
 * Render a task's messages for a one-off sample input — the templated,
 * non-empty messages the live Test panel sends. Exported so the play-area
 * test surface reuses the exact substitution the grid uses.
 */
export function renderTaskMessages(
    variant: PlaygroundVariant,
    input: string,
    expected?: string,
): PlaygroundMessage[] {
    return rowMessages({ id: 'test', input, expected }, variant)
}

/** response_format for a task, or undefined for free text. */
function responseFormatFor(variant: PlaygroundVariant): Record<string, unknown> | undefined {
    return variant.outputFormat === 'json_object' ? { type: 'json_object' } : undefined
}

export function taskReadinessError(
    variant: PlaygroundVariant,
    scope: 'grid' | 'quick' = 'grid',
): string | null {
    if (variant.type === 'prompt') {
        if (!variant.model.trim()) return 'Pick a model'
        if (!hasExplicitMessages(variant)) return 'Add a prompt message'
        return null
    }
    if (variant.type === 'workflow') {
        if (!variant.workflow?.id) return 'Pick a workflow'
        if (scope === 'quick' && !hasExplicitMessages(variant)) {
            return 'Use the sample input or dataset grid to run this workflow'
        }
        return null
    }
    if (variant.type === 'remote') return 'Remote tasks need backend proxy support'
    return 'Scorer columns are not executable yet'
}

export function isTaskRunnable(variant: PlaygroundVariant): boolean {
    return taskReadinessError(variant, 'grid') === null
}

export function isQuickRunRunnable(variant: PlaygroundVariant): boolean {
    return taskReadinessError(variant, 'quick') === null
}

function abortError(): Error {
    const err = new Error('The operation was aborted.')
    err.name = 'AbortError'
    return err
}

export async function executePlaygroundTask({
    variant,
    messages,
    metadata,
    signal,
    onChunk,
    onTraceId,
}: {
    variant: PlaygroundVariant
    messages: PlaygroundMessage[]
    metadata: Record<string, string>
    signal: AbortSignal
    onChunk: (piece: string) => void
    onTraceId?: (traceId: string) => void
}): Promise<void> {
    if (signal.aborted) throw abortError()

    if (variant.type === 'prompt') {
        for await (const chunk of streamChatCompletion(
            {
                model: variant.model,
                messages,
                temperature: variant.temperature,
                maxTokens: variant.maxTokens,
                topP: variant.topP,
                responseFormat: responseFormatFor(variant),
                metadata,
            },
            { signal },
        )) {
            const piece = deltaText(chunk)
            const traceId = (chunk as { id?: string }).id
            if (traceId) onTraceId?.(traceId)
            if (piece) onChunk(piece)
        }
        return
    }

    if (variant.type === 'workflow') {
        const workflowId = variant.workflow?.id
        if (!workflowId) throw new Error('Pick a workflow first')

        const tenantId = getActiveOrgId()
        if (!tenantId) throw new Error('Organization is still loading')

        let emittedText = false
        for await (const event of streamWorkflowExecution(
            {
                workflowId,
                tenantId,
                messages: messages.map((m) => ({ role: m.role, content: m.text })),
                metadata,
            },
            { signal },
        )) {
            if (signal.aborted) throw abortError()
            if (event.type === 'chunk' && event.chunkContent) {
                emittedText = true
                onChunk(event.chunkContent)
            }
            if (event.type === 'done' && !emittedText && event.data?.response_content) {
                emittedText = true
                onChunk(event.data.response_content)
            }
            if (event.type === 'error' && event.error) {
                throw new Error(event.error)
            }
        }
        return
    }

    throw new Error(taskReadinessError(variant) ?? 'Unsupported task type')
}

/** A task's non-empty composer messages, untemplated (quick single run). */
function variantMessages(variant: PlaygroundVariant): PlaygroundMessage[] {
    return variant.messages
        .filter((m) => m.text.trim())
        .map((m) => ({ role: m.role, text: m.text }))
}

interface PlaygroundRunStore {
    runs: Record<string, VariantRunState>
    /** Grid cell runs, keyed by cellKey(rowId, variantId). */
    cells: Record<string, CellState>
    runVariant: (
        variant: PlaygroundVariant,
        metadata: Record<string, string>,
    ) => Promise<void>
    runAll: (metadata: Record<string, string>) => Promise<void>
    /** Run a single grid cell: generate, then score. */
    runCell: (
        row: GridRow,
        variant: PlaygroundVariant,
        metadata: Record<string, string>,
    ) => Promise<void>
    /** Run every cell (row × runnable task) with bounded concurrency. */
    runGrid: (metadata: Record<string, string>) => Promise<void>
    stop: (variantId: string) => void
    stopAll: () => void
    stopGrid: () => void
    clear: () => void
    clearGrid: () => void
}

/**
 * Drives one streaming chat completion per playground variant, all in
 * parallel, each independently abortable. Lives in a store (not a component
 * hook) so both the playground page and the topbar controls share one set of
 * runs.
 */
export const usePlaygroundRunStore = create<PlaygroundRunStore>((set, get) => ({
    runs: {},
    cells: {},

    runVariant: async (variant, metadata) => {
        if (!isQuickRunRunnable(variant)) return
        const messages = variantMessages(variant)
        if (!messages.length) return

        controllers.get(variant.id)?.abort()
        const ctrl = new AbortController()
        controllers.set(variant.id, ctrl)

        const startedAt = performance.now()
        let ttftMs: number | undefined
        set((s) => ({ runs: { ...s.runs, [variant.id]: { status: 'running', text: '' } } }))

        try {
            await executePlaygroundTask({
                variant,
                messages,
                metadata,
                signal: ctrl.signal,
                onTraceId: (traceId) => {
                    set((s) => {
                        const prev = s.runs[variant.id] ?? IDLE
                        return {
                            runs: {
                                ...s.runs,
                                [variant.id]: {
                                    ...prev,
                                    traceId: prev.traceId ?? traceId,
                                },
                            },
                        }
                    })
                },
                onChunk: (piece) => {
                    if (piece && ttftMs === undefined) {
                        ttftMs = Math.round(performance.now() - startedAt)
                    }
                    set((s) => {
                        const prev = s.runs[variant.id] ?? IDLE
                        return {
                            runs: {
                                ...s.runs,
                                [variant.id]: {
                                    ...prev,
                                    text: prev.text + piece,
                                    ttftMs,
                                },
                            },
                        }
                    })
                },
            })
            set((s) => ({
                runs: {
                    ...s.runs,
                    [variant.id]: {
                        ...(s.runs[variant.id] ?? IDLE),
                        status: 'done',
                        durationMs: Math.round(performance.now() - startedAt),
                    },
                },
            }))
        } catch (e) {
            const aborted = (e as { name?: string })?.name === 'AbortError'
            set((s) => ({
                runs: {
                    ...s.runs,
                    [variant.id]: {
                        ...(s.runs[variant.id] ?? IDLE),
                        status: aborted ? 'aborted' : 'error',
                        error: aborted ? undefined : ((e as Error)?.message ?? String(e)),
                        durationMs: Math.round(performance.now() - startedAt),
                    },
                },
            }))
        } finally {
            if (controllers.get(variant.id) === ctrl) {
                controllers.delete(variant.id)
            }
        }
    },

    runAll: async (metadata) => {
        const { variants } = usePlaygroundStore.getState()
        await Promise.allSettled(
            variants
                .filter(isQuickRunRunnable)
                .map((v) => get().runVariant(v, metadata)),
        )
    },

    runCell: async (row, variant, metadata) => {
        if (!isTaskRunnable(variant)) return
        const key = cellKey(row.id, variant.id)
        const messages = rowMessages(row, variant)
        if (!messages.length) return

        controllers.get(key)?.abort()
        const ctrl = new AbortController()
        controllers.set(key, ctrl)

        const { scorerConfigIds } = usePlaygroundStore.getState()
        const startedAt = performance.now()
        let ttftMs: number | undefined
        set((s) => ({ cells: { ...s.cells, [key]: { status: 'running', text: '' } } }))

        try {
            await executePlaygroundTask({
                variant,
                messages,
                metadata,
                signal: ctrl.signal,
                onTraceId: (traceId) => {
                    set((s) => {
                        const prev = s.cells[key] ?? IDLE_CELL
                        return {
                            cells: {
                                ...s.cells,
                                [key]: {
                                    ...prev,
                                    traceId: prev.traceId ?? traceId,
                                },
                            },
                        }
                    })
                },
                onChunk: (piece) => {
                    if (piece && ttftMs === undefined) {
                        ttftMs = Math.round(performance.now() - startedAt)
                    }
                    set((s) => {
                        const prev = s.cells[key] ?? IDLE_CELL
                        return {
                            cells: {
                                ...s.cells,
                                [key]: {
                                    ...prev,
                                    text: prev.text + piece,
                                    ttftMs,
                                },
                            },
                        }
                    })
                },
            })

            const durationMs = Math.round(performance.now() - startedAt)
            const output = get().cells[key]?.text ?? ''

            // Score the finished output. Failures here must not mask a
            // successful generation, so scoring errors are swallowed into an
            // empty score map (the cell still shows its output).
            let scores: ScoreMap = {}
            if (scorerConfigIds.length) {
                set((s) => ({
                    cells: {
                        ...s.cells,
                        [key]: { ...(s.cells[key] ?? IDLE_CELL), status: 'scoring', durationMs },
                    },
                }))
                try {
                    scores = await scoreOutput({
                        input: row.input,
                        output,
                        expectedOutput: row.expected,
                        scorerConfigIds,
                        signal: ctrl.signal,
                    })
                } catch {
                    scores = {}
                }
            }

            set((s) => ({
                cells: {
                    ...s.cells,
                    [key]: {
                        ...(s.cells[key] ?? IDLE_CELL),
                        status: 'done',
                        durationMs,
                        scores,
                    },
                },
            }))
        } catch (e) {
            const aborted = (e as { name?: string })?.name === 'AbortError'
            set((s) => ({
                cells: {
                    ...s.cells,
                    [key]: {
                        ...(s.cells[key] ?? IDLE_CELL),
                        status: aborted ? 'aborted' : 'error',
                        error: aborted ? undefined : ((e as Error)?.message ?? String(e)),
                        durationMs: Math.round(performance.now() - startedAt),
                    },
                },
            }))
        } finally {
            if (controllers.get(key) === ctrl) controllers.delete(key)
        }
    },

    runGrid: async (metadata) => {
        const { rows, variants } = usePlaygroundStore.getState()
        const runnableVariants = variants.filter(isTaskRunnable)
        if (!rows.length || !runnableVariants.length) return

        // Build the (row × variant) work list and mark every cell queued so the
        // grid reads as "pending" immediately.
        const jobs: Array<{ row: GridRow; variant: PlaygroundVariant }> = []
        const queued: Record<string, CellState> = {}
        for (const row of rows) {
            for (const variant of runnableVariants) {
                jobs.push({ row, variant })
                queued[cellKey(row.id, variant.id)] = { status: 'queued', text: '' }
            }
        }
        set((s) => ({ cells: { ...s.cells, ...queued } }))

        gridAbort = new AbortController()
        const signal = gridAbort.signal

        // Bounded-concurrency worker pool: a naive fan-out would open one
        // stream per cell (rows × variants), so cap in-flight work. Workers
        // check the grid signal so Stop halts pending jobs, not just live ones.
        let cursor = 0
        const worker = async () => {
            while (cursor < jobs.length) {
                if (signal.aborted) return
                const job = jobs[cursor++]
                await get().runCell(job.row, job.variant, metadata)
            }
        }
        await Promise.all(
            Array.from({ length: Math.min(GRID_CONCURRENCY, jobs.length) }, () => worker()),
        )
    },

    stop: (variantId) => {
        controllers.get(variantId)?.abort()
    },

    stopAll: () => {
        for (const ctrl of controllers.values()) ctrl.abort()
    },

    stopGrid: () => {
        gridAbort?.abort()
        for (const ctrl of controllers.values()) ctrl.abort()
        // In-flight cells transition to 'aborted' via their own catch; queued
        // cells never started, so reset them here or they'd read as stuck.
        set((s) => {
            const next = { ...s.cells }
            for (const [k, c] of Object.entries(next)) {
                if (c.status === 'queued') next[k] = { ...c, status: 'aborted' }
            }
            return { cells: next }
        })
    },

    clear: () => {
        get().stopAll()
        set({ runs: {} })
    },

    clearGrid: () => {
        get().stopGrid()
        set({ cells: {} })
    },
}))

/** True while any variant is mid-stream. */
export function useAnyRunning(): boolean {
    return usePlaygroundRunStore((s) =>
        Object.values(s.runs).some((r) => r.status === 'running'),
    )
}

/** True while any grid cell is queued, generating, or scoring. */
export function useAnyGridRunning(): boolean {
    return usePlaygroundRunStore((s) =>
        Object.values(s.cells).some(
            (c) => c.status === 'queued' || c.status === 'running' || c.status === 'scoring',
        ),
    )
}

const IDLE_CELL_REF: CellState = { status: 'idle', text: '' }

/** Subscribe to a single grid cell's run state. */
export function useCellState(rowId: string, variantId: string): CellState {
    return usePlaygroundRunStore((s) => s.cells[cellKey(rowId, variantId)] ?? IDLE_CELL_REF)
}

/**
 * Whether the quick-run controls are runnable: at least one task can execute
 * without a dataset/sample input and nothing is currently streaming.
 */
export function usePlaygroundCanRun(): boolean {
    const ready = usePlaygroundStore((s) => s.variants.some(isQuickRunRunnable))
    const anyRunning = useAnyRunning()
    return ready && !anyRunning
}

/**
 * Metadata tagged onto every playground completion so the resulting trace is
 * recognisable in /observability/traces and linkable back to its origin.
 * Reads the route search params, so it works from both the page and the topbar.
 */
export function usePlaygroundMetadata(): Record<string, string> {
    const search = useSearch({ strict: false }) as {
        fromTrace?: string
        fromSpan?: string
    }
    return useMemo(() => {
        const metadata: Record<string, string> = { 'everstack.source': 'playground' }
        if (search.fromTrace) metadata['compare_with'] = search.fromTrace
        if (search.fromSpan) metadata['parent_observation_id'] = search.fromSpan
        return metadata
    }, [search.fromTrace, search.fromSpan])
}
