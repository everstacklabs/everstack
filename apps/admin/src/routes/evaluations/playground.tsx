import { createFileRoute, Link } from '@tanstack/react-router'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import {
    BookOpen,
    Braces,
    CheckCircle2,
    Database,
    GitCompare,
    Globe,
    MessageSquare,
    Play,
    Save,
    SlidersHorizontal,
    Sparkles,
    Square,
    Table2,
    Triangle,
    Workflow,
    X,
} from 'lucide-react'
import { listGatewayModels } from '@/server/gateway'
import {
    MAX_VARIANTS,
    serializePlaygroundConfig,
    usePlaygroundStore,
    type OutputFormat,
    type PlaygroundVariant,
    type TaskType,
    type TemplatingEngine,
} from '@/stores/playground-store'
import { usePlayground, useUpdatePlayground } from '@/hooks/evaluations/use-playgrounds'
import {
    isQuickRunRunnable,
    useAnyRunning,
    usePlaygroundCanRun,
    usePlaygroundMetadata,
    usePlaygroundRunStore,
    taskReadinessError,
    type VariantRunState,
} from '@/stores/playground-run-store'
import { AddTaskMenu } from '@/components/playground/add-task-menu'
import { MessageComposer } from '@/components/playground/message-composer'
import { PromptDiff } from '@/components/playground/prompt-diff'
import { RunGrid } from '@/components/playground/run-grid'
import { ScorerPicker } from '@/components/playground/scorer-picker'
import { TaskTestPanel } from '@/components/playground/task-test-panel'
import { TaskTypeEditor } from '@/components/playground/task-type-editor'
import { ExperimentsButton } from '@/components/playground/experiments-button'
import { parseMessagesAttribute } from '@/components/playground/prefill'
import { LoadPromptDialog, SavePromptDialog } from '@/components/playground/prompt-dialogs'
import {
    AddToDatasetDialog,
    type AddToDatasetPayload,
} from '@/components/evaluations/add-to-dataset-dialog'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

type WorkbenchMode = 'try' | 'dataset' | 'compare'

const playgroundSearchSchema = z.object({
    /** Bind the editor to a saved playground document. */
    id: z.string().optional(),
    model: z.string().optional(),
    system: z.string().optional(),
    user: z.string().optional(),
    temperature: z.string().optional(),
    fromTrace: z.string().optional(),
    fromSpan: z.string().optional(),
})

export const Route = createFileRoute('/evaluations/playground')({
    component: EvaluationsPlaygroundPage,
    validateSearch: playgroundSearchSchema,
})

const MODE_META: Array<{ id: WorkbenchMode; label: string; icon: typeof Sparkles }> = [
    { id: 'try', label: 'Try', icon: Sparkles },
    { id: 'dataset', label: 'Dataset', icon: Table2 },
    { id: 'compare', label: 'Compare', icon: GitCompare },
]

const TASK_META: Record<TaskType, { label: string; icon: typeof MessageSquare; color: string }> = {
    prompt: { label: 'Prompt', icon: MessageSquare, color: 'text-brand-secondary-300' },
    workflow: { label: 'Workflow', icon: Workflow, color: 'text-amber-300' },
    remote: { label: 'Remote eval', icon: Globe, color: 'text-sky-300' },
    scorer: { label: 'Scorer', icon: Triangle, color: 'text-emerald-300' },
}

function EvaluationsPlaygroundPage() {
    const search = Route.useSearch()

    const variants = usePlaygroundStore((s) => s.variants)
    const rows = usePlaygroundStore((s) => s.rows)
    const scorerConfigIds = usePlaygroundStore((s) => s.scorerConfigIds)
    const removeVariant = usePlaygroundStore((s) => s.removeVariant)
    const updateVariant = usePlaygroundStore((s) => s.updateVariant)
    const loadConversation = usePlaygroundStore((s) => s.loadConversation)
    const diffMode = usePlaygroundStore((s) => s.diffMode)
    const setDiffMode = usePlaygroundStore((s) => s.setDiffMode)
    const playgroundId = usePlaygroundStore((s) => s.playgroundId)
    const playgroundName = usePlaygroundStore((s) => s.playgroundName)
    const hydrateFromConfig = usePlaygroundStore((s) => s.hydrateFromConfig)
    const setPlaygroundName = usePlaygroundStore((s) => s.setPlaygroundName)

    // Bind to a saved doc when ?id= is present: fetch it and hydrate once per id.
    const { data: doc } = usePlayground(search.id ?? '')
    const updatePlaygroundMutation = useUpdatePlayground()
    const loadedDocRef = useRef<string | null>(null)
    useEffect(() => {
        if (!search.id || !doc || doc.id !== search.id) return
        if (loadedDocRef.current === doc.id) return
        loadedDocRef.current = doc.id
        hydrateFromConfig({
            id: doc.id,
            name: doc.name,
            config: (doc.config ?? {}) as Record<string, unknown>,
        })
    }, [search.id, doc, hydrateFromConfig])

    // Autosave: debounce store changes back to the bound doc.
    useEffect(() => {
        if (!playgroundId) return
        let timer: ReturnType<typeof setTimeout> | undefined
        const schedule = () => {
            clearTimeout(timer)
            timer = setTimeout(() => {
                updatePlaygroundMutation.mutate({
                    id: playgroundId,
                    name: playgroundName,
                    config: serializePlaygroundConfig() as Record<string, unknown>,
                })
            }, 900)
        }
        const unsub = usePlaygroundStore.subscribe(schedule)
        return () => {
            clearTimeout(timer)
            unsub()
        }
        // Re-subscribe when the bound doc changes; mutation ref is stable enough.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [playgroundId, playgroundName])

    const runs = usePlaygroundRunStore((s) => s.runs)
    const runVariant = usePlaygroundRunStore((s) => s.runVariant)
    const runAll = usePlaygroundRunStore((s) => s.runAll)
    const stop = usePlaygroundRunStore((s) => s.stop)
    const stopAll = usePlaygroundRunStore((s) => s.stopAll)
    const clearRuns = usePlaygroundRunStore((s) => s.clear)

    const metadata = usePlaygroundMetadata()
    const canRun = usePlaygroundCanRun()
    const anyRunning = useAnyRunning()

    const [mode, setMode] = useState<WorkbenchMode>('try')
    const [selectedVariantId, setSelectedVariantId] = useState<string | null>(null)
    const [datasetPayload, setDatasetPayload] = useState<AddToDatasetPayload | null>(null)
    const [savePromptVariant, setSavePromptVariant] = useState<PlaygroundVariant | null>(null)
    const [loadPromptOpen, setLoadPromptOpen] = useState(false)

    useEffect(() => {
        if (!variants.length) return
        if (!selectedVariantId || !variants.some((variant) => variant.id === selectedVariantId)) {
            setSelectedVariantId(variants[0].id)
        }
    }, [selectedVariantId, variants])

    // Hydrate composer from URL params once (trace re-run / shared links).
    // After mount the store is the source of truth.
    const hydratedRef = useRef(false)
    useEffect(() => {
        if (hydratedRef.current) return
        hydratedRef.current = true
        const hasPrefill =
            search.model || search.system || search.user || search.temperature
        if (!hasPrefill) return
        const prefill = search.user ? parseMessagesAttribute(search.user) : []
        loadConversation({
            messages: [
                ...(search.system ? [{ role: 'system' as const, text: search.system }] : []),
                ...prefill.filter((message) => message.role !== 'system' || !search.system),
            ],
            model: search.model,
            temperature: search.temperature ? Number(search.temperature) : undefined,
        })
    }, [search, loadConversation])

    const { data: providerModels } = useQuery({
        queryKey: ['gateway-models'],
        queryFn: listGatewayModels,
        staleTime: 60_000,
    })
    const modelOptions = (providerModels ?? []).flatMap((provider) =>
        provider.models.map((model) => `@${provider.provider}/${model}`),
    )

    // Cmd/Ctrl+Enter runs every variant. Controls live in the topbar, but the
    // shortcut stays bound to the page where the composer has focus.
    const canRunRef = useRef(canRun)
    canRunRef.current = canRun
    useEffect(() => {
        const onKeyDown = (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault()
                if (canRunRef.current) void runAll(metadata)
            }
        }
        window.addEventListener('keydown', onKeyDown)
        return () => window.removeEventListener('keydown', onKeyDown)
    }, [runAll, metadata])

    const activeVariant = variants.find((variant) => variant.id === selectedVariantId) ?? variants[0]
    const activeIndex = Math.max(
        0,
        variants.findIndex((variant) => variant.id === activeVariant?.id),
    )
    const activeRun = activeVariant ? runs[activeVariant.id] : undefined
    const activeQuickCanRun = activeVariant ? isQuickRunRunnable(activeVariant) : false

    const variantTraceIds = variants
        .map((variant) => runs[variant.id]?.traceId)
        .filter((id): id is string => Boolean(id))

    const addActiveOutputToDataset = () => {
        if (!activeVariant) return
        const run = runs[activeVariant.id]
        if (!run?.text) return
        const runMessages = activeVariant.messages.filter((message) => message.text.trim())
        setDatasetPayload({
            input: {
                messages: runMessages.map((message) => ({
                    role: message.role,
                    content: message.text,
                })),
            },
            expectedOutput: { output: run.text },
            metadata: {
                source: 'playground',
                model: activeVariant.model,
            },
            sourceTraceId: run.traceId,
        })
    }

    return (
        <div className="flex h-full w-full flex-col gap-3 overflow-hidden bg-brand-main-950 p-3">
            <CommandHeader
                mode={mode}
                setMode={setMode}
                playgroundId={playgroundId}
                playgroundName={playgroundName}
                setPlaygroundName={setPlaygroundName}
                saving={updatePlaygroundMutation.isPending}
                variants={variants}
                rowsCount={rows.length}
                scorerCount={scorerConfigIds.length}
                activeVariant={activeVariant}
                activeRun={activeRun}
                canRun={canRun}
                activeCanRun={activeQuickCanRun}
                anyRunning={anyRunning}
                onLoadPrompt={() => setLoadPromptOpen(true)}
                onSavePrompt={() => activeVariant && setSavePromptVariant(activeVariant)}
                onRunActive={() => activeVariant && void runVariant(activeVariant, metadata)}
                onRunAll={() => void runAll(metadata)}
                onStopActive={() => activeVariant && stop(activeVariant.id)}
                onStopAll={stopAll}
                onClear={clearRuns}
            />

            {search.fromTrace && (
                <TraceBanner
                    fromTrace={search.fromTrace}
                    compareTraceId={variantTraceIds[0]}
                />
            )}

            <div className="grid min-h-0 flex-1 grid-rows-[minmax(320px,52%)_minmax(0,1fr)] overflow-hidden rounded-md border border-brand-main-800 bg-brand-main-950/70">
                <div className="grid min-h-0 grid-cols-[minmax(190px,230px)_minmax(360px,1fr)_minmax(280px,330px)] border-b border-brand-main-800">
                    <TaskRail
                        variants={variants}
                        selectedVariantId={activeVariant?.id}
                        onSelect={setSelectedVariantId}
                        onRemove={(id) => {
                            removeVariant(id)
                            if (selectedVariantId === id) {
                                const next = variants.find((variant) => variant.id !== id)
                                setSelectedVariantId(next?.id ?? null)
                            }
                        }}
                    />
                    {activeVariant && (
                        <PromptStudio
                            activeVariant={activeVariant}
                            activeIndex={activeIndex}
                            baseVariant={variants[0]}
                            run={activeRun}
                            canRun={activeQuickCanRun}
                            diffMode={diffMode}
                            setDiffMode={setDiffMode}
                            onSavePrompt={() => setSavePromptVariant(activeVariant)}
                            onRun={() => void runVariant(activeVariant, metadata)}
                            onStop={() => stop(activeVariant.id)}
                        />
                    )}
                    {activeVariant && (
                        <SettingsRail
                            variant={activeVariant}
                            modelOptions={modelOptions}
                            rowsCount={rows.length}
                            scorerCount={scorerConfigIds.length}
                            onChange={(patch) => updateVariant(activeVariant.id, patch)}
                        />
                    )}
                </div>

                <div className="min-h-0 bg-brand-main-950/55">
                    {mode === 'try' && activeVariant ? (
                        <TryConsole
                            variant={activeVariant}
                            run={activeRun}
                            onAddToDataset={addActiveOutputToDataset}
                        />
                    ) : (
                        <RunGrid focus={mode === 'compare' ? 'compare' : 'dataset'} />
                    )}
                </div>
            </div>

            {variantTraceIds.length >= 2 && mode === 'compare' && (
                <div className="shrink-0 rounded-md border border-brand-main-700 bg-brand-main-900/50 px-4 py-2 text-xs text-white/50 light:text-black/50">
                    <Link
                        to="/observability/traces/compare"
                        search={{ a: variantTraceIds[0], b: variantTraceIds[1] }}
                        className="inline-flex items-center gap-1.5 text-brand-secondary-300 hover:text-brand-secondary-200"
                    >
                        <GitCompare className="h-3.5 w-3.5" />
                        Compare traces from the first two completed tasks
                    </Link>
                </div>
            )}

            <AddToDatasetDialog
                open={datasetPayload !== null}
                onOpenChange={(open) => !open && setDatasetPayload(null)}
                payload={datasetPayload}
                sourceLabel="playground run"
            />

            <LoadPromptDialog open={loadPromptOpen} onOpenChange={setLoadPromptOpen} />

            <SavePromptDialog
                open={savePromptVariant !== null}
                onOpenChange={(open) => !open && setSavePromptVariant(null)}
                configVariant={savePromptVariant ?? undefined}
            />
        </div>
    )
}

function CommandHeader({
    mode,
    setMode,
    playgroundId,
    playgroundName,
    setPlaygroundName,
    saving,
    variants,
    rowsCount,
    scorerCount,
    activeVariant,
    activeRun,
    canRun,
    activeCanRun,
    anyRunning,
    onLoadPrompt,
    onSavePrompt,
    onRunActive,
    onRunAll,
    onStopActive,
    onStopAll,
    onClear,
}: {
    mode: WorkbenchMode
    setMode: (mode: WorkbenchMode) => void
    playgroundId?: string
    playgroundName?: string
    setPlaygroundName: (name: string) => void
    saving: boolean
    variants: PlaygroundVariant[]
    rowsCount: number
    scorerCount: number
    activeVariant?: PlaygroundVariant
    activeRun?: VariantRunState
    canRun: boolean
    activeCanRun: boolean
    anyRunning: boolean
    onLoadPrompt: () => void
    onSavePrompt: () => void
    onRunActive: () => void
    onRunAll: () => void
    onStopActive: () => void
    onStopAll: () => void
    onClear: () => void
}) {
    const activeRunning = activeRun?.status === 'running'

    return (
        <header className="shrink-0 rounded-md border border-brand-main-700 bg-brand-main-900/60 px-3 py-2 shadow-[0_10px_32px_rgba(0,0,0,0.18)]">
            <div className="flex flex-wrap items-center gap-4">
                <div className="min-w-[210px]">
                    {playgroundId ? (
                        <input
                            value={playgroundName ?? ''}
                            onChange={(event) => setPlaygroundName(event.target.value)}
                            placeholder="Untitled playground"
                            className="w-full bg-transparent text-base font-semibold text-white outline-none placeholder:text-white/25 light:text-brand-main-50 light:placeholder:text-black/25"
                        />
                    ) : (
                        <h1 className="text-base font-semibold text-white light:text-brand-main-50">
                            Playground
                        </h1>
                    )}
                    <div className="mt-0.5 flex items-center gap-2 text-[11px] text-white/35 light:text-black/35">
                        <CheckCircle2 className="h-3 w-3" />
                        <span>{saving ? 'Saving' : playgroundId ? 'Saved' : 'Scratch'}</span>
                    </div>
                </div>

                <div className="flex items-center gap-4">
                    {MODE_META.map(({ id, label, icon: Icon }) => (
                        <button
                            key={id}
                            type="button"
                            onClick={() => setMode(id)}
                            className={`inline-flex items-center gap-1.5 rounded px-2 py-1 text-sm transition-colors ${
                                mode === id
                                    ? 'bg-brand-secondary-500/10 text-brand-secondary-200 ring-1 ring-brand-secondary-500/25'
                                    : 'text-white/45 hover:bg-brand-main-800/70 hover:text-white light:text-black/45 light:hover:text-brand-main-50'
                            }`}
                        >
                            <Icon className="h-3.5 w-3.5" />
                            {label}
                        </button>
                    ))}
                </div>

                <div className="hidden items-center gap-4 text-xs text-white/40 light:text-black/40 xl:flex">
                    <span>{variants.length} tasks</span>
                    <span>{rowsCount} rows</span>
                    <span>{scorerCount} scorers</span>
                </div>

                <div className="ml-auto flex flex-wrap items-center gap-3">
                    <button
                        type="button"
                        onClick={onLoadPrompt}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/65 light:hover:text-brand-main-50"
                    >
                        <BookOpen className="h-3.5 w-3.5" />
                        Load
                    </button>
                    <button
                        type="button"
                        onClick={onSavePrompt}
                        disabled={!activeVariant}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white disabled:text-white/25 light:text-black/65 light:hover:text-brand-main-50 light:disabled:text-black/25"
                    >
                        <Save className="h-3.5 w-3.5" />
                        Save prompt
                    </button>
                    <ExperimentsButton />
                    {anyRunning ? (
                        <button
                            type="button"
                            onClick={onStopAll}
                            className="inline-flex items-center gap-1.5 rounded border border-rose-400/50 bg-rose-500/10 px-2 py-1 text-xs text-rose-300 transition-colors hover:text-rose-200"
                        >
                            <Square className="h-3.5 w-3.5" />
                            Stop all
                        </button>
                    ) : (
                        <button
                            type="button"
                            onClick={onRunAll}
                            disabled={!canRun}
                            className="inline-flex items-center gap-1.5 rounded border border-brand-secondary-500/50 bg-brand-secondary-500/10 px-2 py-1 text-xs text-brand-secondary-200 transition-colors hover:text-brand-secondary-100 disabled:border-brand-main-800 disabled:bg-transparent disabled:text-white/25 light:disabled:text-black/25"
                        >
                            <Play className="h-3.5 w-3.5" />
                            Run all
                        </button>
                    )}
                    {activeRunning ? (
                        <button
                            type="button"
                            onClick={onStopActive}
                            className="inline-flex items-center gap-1.5 rounded border border-rose-400/50 bg-rose-500/10 px-2 py-1 text-xs text-rose-300 transition-colors hover:text-rose-200"
                        >
                            <Square className="h-3.5 w-3.5" />
                            Stop task
                        </button>
                    ) : (
                            <button
                                type="button"
                                onClick={onRunActive}
                                disabled={!activeCanRun}
                                className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white disabled:text-white/25 light:text-black/65 light:hover:text-brand-main-50 light:disabled:text-black/25"
                                title={activeVariant ? (taskReadinessError(activeVariant, 'quick') ?? undefined) : undefined}
                            >
                            <Play className="h-3.5 w-3.5" />
                            Run task
                        </button>
                    )}
                    <button
                        type="button"
                        onClick={onClear}
                        className="rounded px-2 py-1 text-xs text-white/35 transition-colors hover:bg-brand-main-800/70 hover:text-white/70 light:text-black/35 light:hover:text-black/70"
                    >
                        Clear
                    </button>
                </div>
            </div>
        </header>
    )
}

function TraceBanner({
    fromTrace,
    compareTraceId,
}: {
    fromTrace: string
    compareTraceId?: string
}) {
    return (
        <div className="flex shrink-0 items-center justify-between gap-3 rounded-md border border-brand-secondary-500/25 bg-brand-secondary-500/5 px-4 py-2 text-xs text-white/55 light:text-black/55">
            <span>Re-running from trace {fromTrace.slice(0, 12)}...</span>
            <Link
                to="/observability/traces/compare"
                search={{
                    a: fromTrace,
                    ...(compareTraceId ? { b: compareTraceId } : {}),
                }}
                className="inline-flex items-center gap-1.5 text-brand-secondary-300 hover:text-brand-secondary-200"
            >
                <GitCompare className="h-3.5 w-3.5" />
                Compare with original
            </Link>
        </div>
    )
}

function TaskRail({
    variants,
    selectedVariantId,
    onSelect,
    onRemove,
}: {
    variants: PlaygroundVariant[]
    selectedVariantId?: string
    onSelect: (id: string) => void
    onRemove: (id: string) => void
}) {
    return (
        <aside className="flex min-h-0 flex-col border-r border-brand-main-800 bg-brand-main-900/20">
            <div className="border-b border-brand-main-800 px-3 py-2">
                <div className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
                    Tasks
                </div>
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
                {variants.map((variant, index) => {
                    const meta = TASK_META[variant.type]
                    const Icon = meta.icon
                    const active = variant.id === selectedVariantId
                    return (
                        <button
                            key={variant.id}
                            type="button"
                            onClick={() => onSelect(variant.id)}
                            className={`group mx-2 mt-2 flex w-[calc(100%-1rem)] items-start gap-2 rounded border border-l-2 px-2.5 py-2.5 text-left transition-colors ${
                                active
                                    ? 'border-brand-secondary-500/35 border-l-brand-secondary-400 bg-brand-secondary-500/10'
                                    : 'border-brand-main-800 border-l-transparent bg-brand-main-950/20 hover:border-l-brand-main-600 hover:bg-brand-main-800/40'
                            }`}
                        >
                            <span
                                className={`mt-0.5 text-xs font-semibold ${
                                    active ? 'text-brand-secondary-300' : 'text-white/35 light:text-black/35'
                                }`}
                            >
                                {taskLabel(index)}
                            </span>
                            <Icon className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${meta.color}`} />
                            <span className="min-w-0 flex-1">
                                <span
                                    className={`block truncate text-sm ${
                                        active
                                            ? 'text-white light:text-brand-main-50'
                                            : 'text-white/70 light:text-black/70'
                                    }`}
                                >
                                    {taskTitle(variant)}
                                </span>
                                <span className="mt-0.5 block truncate text-xs text-white/35 light:text-black/35">
                                    {variant.type === 'prompt'
                                        ? `${variant.messages.filter((message) => message.text.trim()).length} messages`
                                        : variant.type === 'workflow'
                                          ? variant.workflow?.id
                                              ? 'Backend workflow'
                                              : 'Pick a workflow'
                                        : meta.label}
                                </span>
                            </span>
                            {variants.length > 1 && (
                                <span
                                    role="button"
                                    tabIndex={0}
                                    onClick={(event) => {
                                        event.stopPropagation()
                                        onRemove(variant.id)
                                    }}
                                    onKeyDown={(event) => {
                                        if (event.key === 'Enter' || event.key === ' ') {
                                            event.preventDefault()
                                            event.stopPropagation()
                                            onRemove(variant.id)
                                        }
                                    }}
                                    className="mt-0.5 text-white/0 transition-colors group-hover:text-white/35 hover:!text-rose-300 light:text-black/0 light:group-hover:text-black/35"
                                    aria-label="Remove task"
                                >
                                    <X className="h-3.5 w-3.5" />
                                </span>
                            )}
                        </button>
                    )
                })}
            </div>
            {variants.length < MAX_VARIANTS && <AddTaskMenu />}
        </aside>
    )
}

function PromptStudio({
    activeVariant,
    activeIndex,
    baseVariant,
    run,
    canRun,
    diffMode,
    setDiffMode,
    onSavePrompt,
    onRun,
    onStop,
}: {
    activeVariant: PlaygroundVariant
    activeIndex: number
    baseVariant?: PlaygroundVariant
    run?: VariantRunState
    canRun: boolean
    diffMode: boolean
    setDiffMode: (on: boolean) => void
    onSavePrompt: () => void
    onRun: () => void
    onStop: () => void
}) {
    const meta = TASK_META[activeVariant.type]
    const Icon = meta.icon
    const variables = useMemo(() => extractVariables(activeVariant), [activeVariant])
    const streaming = run?.status === 'running'
    const canDiff = Boolean(baseVariant && activeVariant.id !== baseVariant.id)

    return (
        <section className="flex min-h-0 min-w-0 flex-col border-r border-brand-main-800">
            <div className="flex shrink-0 items-start justify-between gap-3 border-b border-brand-main-800 bg-brand-main-900/30 px-4 py-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <span className="text-xs font-semibold text-brand-secondary-300">
                            {taskLabel(activeIndex)}
                        </span>
                        <Icon className={`h-3.5 w-3.5 ${meta.color}`} />
                        <h2 className="truncate text-sm font-semibold text-white light:text-brand-main-50">
                            {meta.label}
                        </h2>
                    </div>
                    <VariableStrip variables={variables} />
                </div>
                <div className="flex shrink-0 items-center gap-3">
                    {canDiff && (
                        <button
                            type="button"
                            onClick={() => setDiffMode(!diffMode)}
                            className={`inline-flex items-center gap-1.5 rounded border px-2 py-1 text-xs transition-colors ${
                                diffMode
                                    ? 'border-brand-secondary-500/40 bg-brand-secondary-500/10 text-brand-secondary-200'
                                    : 'border-brand-main-700 bg-brand-main-950/20 text-white/55 hover:border-brand-secondary-500/60 hover:text-white light:text-black/55 light:hover:text-brand-main-50'
                            }`}
                        >
                            <GitCompare className="h-3.5 w-3.5" />
                            Diff
                        </button>
                    )}
                    <button
                        type="button"
                        onClick={onSavePrompt}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/55 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/55 light:hover:text-brand-main-50"
                    >
                        <Save className="h-3.5 w-3.5" />
                        Save
                    </button>
                    {streaming ? (
                        <button
                            type="button"
                            onClick={onStop}
                            className="inline-flex items-center gap-1.5 rounded border border-rose-400/50 bg-rose-500/10 px-2 py-1 text-xs text-rose-300 transition-colors hover:text-rose-200"
                        >
                            <Square className="h-3.5 w-3.5" />
                            Stop
                        </button>
                    ) : (
                        <button
                            type="button"
                            onClick={onRun}
                            disabled={!canRun}
                            className="inline-flex items-center gap-1.5 rounded border border-brand-secondary-500/50 bg-brand-secondary-500/10 px-2 py-1 text-xs text-brand-secondary-200 transition-colors hover:text-brand-secondary-100 disabled:border-brand-main-800 disabled:bg-transparent disabled:text-white/25 light:disabled:text-black/25"
                            title={taskReadinessError(activeVariant, 'quick') ?? undefined}
                        >
                            <Play className="h-3.5 w-3.5" />
                            Run
                        </button>
                    )}
                </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto px-4 py-2">
                {activeVariant.type !== 'prompt' ? (
                    <TaskTypeEditor variant={activeVariant} />
                ) : diffMode && canDiff && baseVariant ? (
                    <div className="h-full min-h-[260px]">
                        <PromptDiff base={baseVariant} variant={activeVariant} />
                    </div>
                ) : (
                    <MessageComposer variantId={activeVariant.id} />
                )}
            </div>
        </section>
    )
}

function VariableStrip({ variables }: { variables: string[] }) {
    return (
        <div className="mt-1 flex min-h-5 flex-wrap items-center gap-2 text-xs">
            <span className="inline-flex items-center gap-1 text-white/35 light:text-black/35">
                <Braces className="h-3 w-3" />
                Variables
            </span>
            {variables.length === 0 ? (
                <span className="text-white/25 light:text-black/25">none detected</span>
            ) : (
                variables.map((variable) => (
                    <span
                        key={variable}
                        className={
                            variable === 'input' || variable === 'expected'
                                ? 'text-brand-secondary-300'
                                : 'text-amber-300'
                        }
                    >
                        {'{{'}
                        {variable}
                        {'}}'}
                    </span>
                ))
            )}
        </div>
    )
}

function SettingsRail({
    variant,
    modelOptions,
    rowsCount,
    scorerCount,
    onChange,
}: {
    variant: PlaygroundVariant
    modelOptions: string[]
    rowsCount: number
    scorerCount: number
    onChange: (patch: Partial<Omit<PlaygroundVariant, 'id' | 'messages'>>) => void
}) {
    const variables = useMemo(() => extractVariables(variant), [variant])
    const unsupportedVariables = variables.filter(
        (variable) => variable !== 'input' && variable !== 'expected',
    )

    return (
        <aside className="flex min-h-0 flex-col overflow-auto">
            <div className="border-b border-brand-main-800 bg-brand-main-900/30 px-4 py-3">
                <div className="flex items-center gap-2 text-sm font-semibold text-white light:text-brand-main-50">
                    <SlidersHorizontal className="h-3.5 w-3.5 text-brand-secondary-300" />
                    Run settings
                </div>
            </div>

            <div className="space-y-5 px-4 py-4">
                {variant.type === 'prompt' ? (
                    <>
                <section className="space-y-2">
                    <SectionLabel>Model</SectionLabel>
                    {modelOptions.length > 0 ? (
                        <Select
                            value={variant.model}
                            onValueChange={(model) => onChange({ model })}
                        >
                            <SelectTrigger className="h-8 bg-transparent border-brand-main-700 text-xs">
                                <SelectValue placeholder="Pick a model" />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                {modelOptions.map((model) => (
                                    <SelectItem
                                        key={model}
                                        value={model}
                                        className="text-xs text-white/80 light:text-black/80"
                                    >
                                        {model}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    ) : (
                        <input
                            type="text"
                            value={variant.model}
                            onChange={(event) => onChange({ model: event.target.value })}
                            placeholder="@openai/gpt-4o-mini"
                            className="w-full rounded border border-brand-main-800 bg-brand-main-900/25 px-2 py-1.5 text-sm text-white/85 outline-none placeholder:text-white/25 focus:border-brand-secondary-500/60 light:text-black/85 light:placeholder:text-black/25"
                        />
                    )}
                </section>

                <section className="space-y-4">
                    <SectionLabel>Sampling</SectionLabel>
                    <SliderField
                        label="Temperature"
                        value={variant.temperature}
                        min={0}
                        max={2}
                        step={0.05}
                        display={variant.temperature.toFixed(2)}
                        onChange={(value) => onChange({ temperature: value })}
                    />
                    <SliderField
                        label="Top P"
                        value={variant.topP ?? 1}
                        min={0}
                        max={1}
                        step={0.01}
                        display={variant.topP !== undefined ? variant.topP.toFixed(2) : 'default'}
                        onChange={(value) => onChange({ topP: value })}
                    />
                    <label className="block">
                        <span className="flex items-center justify-between text-xs text-white/45 light:text-black/45">
                            Max tokens
                            <span>{variant.maxTokens ?? 'default'}</span>
                        </span>
                        <input
                            type="number"
                            min={1}
                            value={variant.maxTokens ?? ''}
                            placeholder="default"
                            onChange={(event) =>
                                onChange({
                                    maxTokens: event.target.value
                                        ? Math.max(1, Number(event.target.value))
                                        : undefined,
                                })
                            }
                            className="mt-1 w-full rounded border border-brand-main-800 bg-brand-main-900/25 px-2 py-1.5 text-sm text-white/85 outline-none placeholder:text-white/25 focus:border-brand-secondary-500/60 light:text-black/85 light:placeholder:text-black/25"
                        />
                    </label>
                </section>

                <section className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <SectionLabel>Output</SectionLabel>
                        <Select
                            value={variant.outputFormat ?? 'text'}
                            onValueChange={(format) => onChange({ outputFormat: format as OutputFormat })}
                        >
                            <SelectTrigger className="h-8 bg-transparent border-brand-main-700 text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                <SelectItem value="text" className="text-xs text-white/80 light:text-black/80">
                                    Text
                                </SelectItem>
                                <SelectItem
                                    value="json_object"
                                    className="text-xs text-white/80 light:text-black/80"
                                >
                                    JSON
                                </SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-2">
                        <SectionLabel>Template</SectionLabel>
                        <Select
                            value={variant.templating}
                            onValueChange={(templating) =>
                                onChange({ templating: templating as TemplatingEngine })
                            }
                        >
                            <SelectTrigger className="h-8 bg-transparent border-brand-main-700 text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                {(['mustache', 'jinja', 'none'] as TemplatingEngine[]).map((templating) => (
                                    <SelectItem
                                        key={templating}
                                        value={templating}
                                        className="text-xs text-white/80 light:text-black/80"
                                    >
                                        {templating === 'none' ? 'None' : templating}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                </section>
                    </>
                ) : (
                    <section className="space-y-2">
                        <SectionLabel>Backend task</SectionLabel>
                        <div className="rounded border border-brand-main-800 bg-brand-main-900/25 px-3 py-2 text-xs leading-relaxed text-white/50 light:text-black/50">
                            {taskReadinessError(variant) ?? 'Ready to run from the sample input or dataset grid.'}
                        </div>
                    </section>
                )}

                <section className="space-y-2">
                    <SectionLabel>Evaluation</SectionLabel>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                        <div className="border-l border-brand-main-700 pl-3">
                            <div className="text-white/35 light:text-black/35">Rows</div>
                            <div className="mt-1 text-white/80 light:text-brand-main-50">{rowsCount}</div>
                        </div>
                        <div className="border-l border-brand-main-700 pl-3">
                            <div className="text-white/35 light:text-black/35">Scorers</div>
                            <div className="mt-1 text-white/80 light:text-brand-main-50">{scorerCount}</div>
                        </div>
                    </div>
                    <ScorerPicker />
                </section>

                <section className="space-y-2">
                    <SectionLabel>Variable health</SectionLabel>
                    {unsupportedVariables.length === 0 ? (
                        <p className="text-xs leading-relaxed text-white/40 light:text-black/40">
                            Dataset variables are ready for input and expected fields.
                        </p>
                    ) : (
                        <p className="text-xs leading-relaxed text-amber-300/85">
                            Unsupported fields: {unsupportedVariables.join(', ')}
                        </p>
                    )}
                </section>
            </div>
        </aside>
    )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
    return (
        <div className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
            {children}
        </div>
    )
}

function SliderField({
    label,
    value,
    min,
    max,
    step,
    display,
    onChange,
}: {
    label: string
    value: number
    min: number
    max: number
    step: number
    display: string
    onChange: (value: number) => void
}) {
    return (
        <label className="block">
            <span className="flex items-center justify-between text-xs text-white/45 light:text-black/45">
                {label}
                <span>{display}</span>
            </span>
            <input
                type="range"
                min={min}
                max={max}
                step={step}
                value={value}
                onChange={(event) => onChange(Number(event.target.value))}
                className="mt-1 w-full accent-brand-secondary-500"
            />
        </label>
    )
}

function TryConsole({
    variant,
    run,
    onAddToDataset,
}: {
    variant: PlaygroundVariant
    run?: VariantRunState
    onAddToDataset: () => void
}) {
    const streaming = run?.status === 'running'

    return (
        <section className="grid h-full min-h-0 grid-cols-[minmax(0,1fr)_minmax(280px,34%)] overflow-hidden">
            <div className="min-h-0 overflow-auto border-r border-brand-main-800 px-4 py-3">
                <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-white light:text-brand-main-50">
                    <Sparkles className="h-3.5 w-3.5 text-brand-secondary-300" />
                    Sample run
                </div>
                <TaskTestPanel variant={variant} />
            </div>
            <aside className="min-h-0 overflow-auto px-4 py-3">
                <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-white light:text-brand-main-50">
                    <Database className="h-3.5 w-3.5 text-brand-secondary-300" />
                    Task output
                </div>
                {run?.status === 'error' ? (
                    <div className="text-sm leading-relaxed text-rose-300">{run.error}</div>
                ) : run?.text ? (
                    <div className="space-y-3">
                        <div className="whitespace-pre-wrap text-sm leading-relaxed text-white/85 light:text-black/85">
                            {run.text}
                            {streaming && (
                                <span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-brand-secondary-400 align-text-bottom" />
                            )}
                        </div>
                        <div className="flex flex-wrap items-center gap-3 text-xs text-white/35 light:text-black/35">
                            {run.ttftMs !== undefined && <span>{run.ttftMs} ms first token</span>}
                            {run.durationMs !== undefined && (
                                <span>{(run.durationMs / 1000).toFixed(2)} s total</span>
                            )}
                            {run.traceId && (
                                <Link
                                    to="/observability/traces"
                                    search={(params: Record<string, unknown>) => ({
                                        ...params,
                                        trace: run.traceId,
                                    })}
                                    className="text-brand-secondary-300 hover:text-brand-secondary-200"
                                >
                                    View trace
                                </Link>
                            )}
                        </div>
                        {!streaming && (
                            <button
                                type="button"
                                onClick={onAddToDataset}
                                className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/55 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/55 light:hover:text-brand-main-50"
                            >
                                <Database className="h-3.5 w-3.5" />
                                Add output to dataset
                            </button>
                        )}
                    </div>
                ) : streaming ? (
                    <div className="text-sm text-white/35 light:text-black/35">Waiting for first token</div>
                ) : (
                    <div className="text-sm leading-relaxed text-white/35 light:text-black/35">
                        Run the selected task to keep a raw completion alongside the sample console.
                    </div>
                )}
            </aside>
        </section>
    )
}

function extractVariables(variant: PlaygroundVariant): string[] {
    const found = new Set<string>()
    for (const message of variant.messages) {
        for (const match of message.text.matchAll(/\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}/g)) {
            found.add(match[1])
        }
    }
    return Array.from(found)
}

function taskLabel(index: number) {
    return String.fromCharCode(65 + index)
}

function taskTitle(variant: PlaygroundVariant) {
    if (variant.type === 'prompt') return variant.model || TASK_META.prompt.label
    if (variant.type === 'workflow') return variant.workflow?.name || TASK_META.workflow.label
    if (variant.type === 'remote') return variant.remote?.url || TASK_META.remote.label
    return TASK_META.scorer.label
}
