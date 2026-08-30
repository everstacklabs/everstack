import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type PlaygroundRole = 'system' | 'user' | 'assistant'

export type ComposerMessage = {
    id: string
    role: PlaygroundRole
    text: string
}

/**
 * Templating engine used to substitute row/dataset variables (e.g. `{{input}}`)
 * into a task's prompt before generation. Mirrors the reference UI's
 * Mustache / Jinja / none toggle. On the web, Jinja is rendered via a
 * Mustache-compatible `{{var}}` substitution (full Jinja control-flow is a
 * later addition); `none` disables substitution entirely.
 */
export type TemplatingEngine = 'mustache' | 'jinja' | 'none'

/** Output format for a task: free text or a JSON object (response_format). */
export type OutputFormat = 'text' | 'json_object'

/**
 * A task is any typed row→output transform the grid can compare. `prompt` runs
 * a model on templated messages (fully wired); `workflow` runs a saved agent
 * workflow; `remote` calls a user-hosted endpoint; `scorer` grades another
 * task's output as its own column. Non-prompt types carry their own config and
 * ignore the prompt-only fields.
 */
export type TaskType = 'prompt' | 'workflow' | 'remote' | 'scorer'

/** Config for a `workflow` task — which saved workflow to run per row. */
export type WorkflowConfig = { id?: string; name?: string }
/** Config for a `remote` task — a user-hosted endpoint the row is POSTed to. */
export type RemoteConfig = { url?: string; auth?: string; inputPath?: string; outputPath?: string }
/** Config for a `scorer` task — grade a target task's output as a column. */
export type ScorerRef = { scorerConfigId?: string; targetTaskId?: string }

/**
 * A task column: its own prompt (messages), model, sampling params, and
 * templating engine. Columns compare different prompts and/or models against
 * the same dataset rows.
 */
export type PlaygroundVariant = {
    id: string
    /** Task type; defaults to `prompt`. Non-prompt types use their own config. */
    type: TaskType
    model: string
    temperature: number
    maxTokens?: number
    topP?: number
    messages: ComposerMessage[]
    templating: TemplatingEngine
    /** Output format; omitted/`text` = free text, `json_object` = JSON mode. */
    outputFormat?: OutputFormat
    // Per-type config (present only for the matching type):
    workflow?: WorkflowConfig
    remote?: RemoteConfig
    scorerRef?: ScorerRef
}

/**
 * A single row of the evaluation grid. `input` is substituted into each task's
 * prompt (via `{{input}}`) before generation; `expected` is the optional
 * reference output passed to scorers (e.g. ExactMatch). Rows are loaded from a
 * dataset or added manually.
 */
export type GridRow = {
    id: string
    /** Backing dataset item id when the row came from a saved dataset. */
    datasetItemId?: string
    input: string
    expected?: string
    /** JSON metadata string shown/editable in the row drawer. */
    metadata?: string
    sourceTraceId?: string
    sourceObservationId?: string
}

/** The subset of store state persisted as a playground doc's `config` blob. */
export type SerializablePlaygroundState = {
    variants: PlaygroundVariant[]
    datasetId?: string
    datasetName?: string
    rows: GridRow[]
    scorerConfigIds: string[]
    diffMode: boolean
}

const newId = () =>
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`

const defaultMessages = (): ComposerMessage[] => [
    { id: newId(), role: 'system', text: '' },
    { id: newId(), role: 'user', text: '' },
]

const defaultVariant = (): PlaygroundVariant => ({
    id: newId(),
    type: 'prompt',
    model: '',
    temperature: 0.7,
    messages: defaultMessages(),
    templating: 'mustache',
})

export const MAX_VARIANTS = 4

interface PlaygroundStore {
    variants: PlaygroundVariant[]

    // ── Saved playground document (server-persisted) ──
    /** Set when the current state is bound to a saved playground doc. */
    playgroundId?: string
    playgroundName?: string

    // ── Evaluation grid ──
    /** Currently attached dataset (rows were loaded from it), if any. */
    datasetId?: string
    datasetName?: string
    /** Grid rows: one generation per (row × variant). */
    rows: GridRow[]
    /** Score configs applied to every generated cell. */
    scorerConfigIds: string[]
    /** When on, comparison tasks render a read-only YAML diff vs the base task. */
    diffMode: boolean

    // ── Per-task prompt (messages) actions ──
    addMessage: (variantId: string) => void
    removeMessage: (variantId: string, id: string) => void
    setMessageText: (variantId: string, id: string, text: string) => void
    setMessageRole: (variantId: string, id: string, role: PlaygroundRole) => void

    addVariant: () => void
    /** Add a comparison task of the given type. */
    addTask: (type: TaskType) => void
    removeVariant: (id: string) => void
    updateVariant: (id: string, patch: Partial<Omit<PlaygroundVariant, 'id' | 'messages'>>) => void
    /** Merge into a task's per-type config (workflow / remote / scorerRef). */
    updateTaskConfig: (id: string, patch: Partial<Pick<PlaygroundVariant, 'workflow' | 'remote' | 'scorerRef'>>) => void
    setTemplating: (id: string, engine: TemplatingEngine) => void

    // ── Grid actions ──
    /** Replace grid rows with the given dataset's items. */
    loadDataset: (input: { id: string; name: string; rows: Array<Omit<GridRow, 'id'>> }) => void
    clearDataset: () => void
    addRow: () => void
    removeRow: (id: string) => void
    setRowInput: (id: string, input: string) => void
    setRowExpected: (id: string, expected: string) => void
    setRowMetadata: (id: string, metadata: string) => void
    setScorerConfigIds: (ids: string[]) => void
    setDiffMode: (on: boolean) => void

    // ── Saved-document actions ──
    /** Bind and hydrate the store from a saved playground doc's config. */
    hydrateFromConfig: (input: { id: string; name: string; config: Record<string, unknown> }) => void
    setPlaygroundName: (name: string) => void
    /** Detach from any saved doc (fresh scratch playground). */
    detachPlayground: () => void

    /**
     * Replace a task's prompt wholesale (URL prefill, prompt load, trace
     * re-run). Targets the given variant, or the base task (index 0) if none.
     */
    loadConversation: (input: {
        messages: Array<{ role: PlaygroundRole; text: string }>
        model?: string
        temperature?: number
        variantId?: string
    }) => void
    resetConversation: (variantId?: string) => void
}

/** Immutably map over a single variant's messages. */
function mapVariant(
    variants: PlaygroundVariant[],
    variantId: string,
    fn: (v: PlaygroundVariant) => PlaygroundVariant,
): PlaygroundVariant[] {
    return variants.map((v) => (v.id === variantId ? fn(v) : v))
}

export const usePlaygroundStore = create<PlaygroundStore>()(
    persist(
        (set) => ({
            variants: [defaultVariant()],

            datasetId: undefined,
            datasetName: undefined,
            rows: [],
            scorerConfigIds: [],
            diffMode: false,

            addMessage: (variantId) =>
                set((s) => ({
                    variants: mapVariant(s.variants, variantId, (v) => {
                        // Alternate role from the last non-system message so a
                        // conversation reads user / assistant / user out of the box.
                        const last = [...v.messages].reverse().find((m) => m.role !== 'system')
                        const role: PlaygroundRole = last?.role === 'user' ? 'assistant' : 'user'
                        return { ...v, messages: [...v.messages, { id: newId(), role, text: '' }] }
                    }),
                })),
            removeMessage: (variantId, id) =>
                set((s) => ({
                    variants: mapVariant(s.variants, variantId, (v) => ({
                        ...v,
                        messages: v.messages.length > 1 ? v.messages.filter((m) => m.id !== id) : v.messages,
                    })),
                })),
            setMessageText: (variantId, id, text) =>
                set((s) => ({
                    variants: mapVariant(s.variants, variantId, (v) => ({
                        ...v,
                        messages: v.messages.map((m) => (m.id === id ? { ...m, text } : m)),
                    })),
                })),
            setMessageRole: (variantId, id, role) =>
                set((s) => ({
                    variants: mapVariant(s.variants, variantId, (v) => ({
                        ...v,
                        messages: v.messages.map((m) => (m.id === id ? { ...m, role } : m)),
                    })),
                })),

            addVariant: () =>
                set((s) => {
                    if (s.variants.length >= MAX_VARIANTS) return s
                    // Seed the new column from the last one so comparing starts
                    // from an identical prompt/params (fresh message ids).
                    const seed = s.variants[s.variants.length - 1] ?? defaultVariant()
                    return {
                        variants: [
                            ...s.variants,
                            {
                                ...seed,
                                id: newId(),
                                model: '',
                                messages: seed.messages.map((m) => ({ ...m, id: newId() })),
                            },
                        ],
                    }
                }),
            addTask: (type) =>
                set((s) => {
                    if (s.variants.length >= MAX_VARIANTS) return s
                    const base = defaultVariant()
                    const task: PlaygroundVariant = { ...base, type }
                    if (type === 'workflow') task.workflow = {}
                    else if (type === 'remote') task.remote = { inputPath: '{{input}}' }
                    else if (type === 'scorer') task.scorerRef = {}
                    return { variants: [...s.variants, task] }
                }),
            removeVariant: (id) =>
                set((s) => ({
                    variants: s.variants.length > 1 ? s.variants.filter((v) => v.id !== id) : s.variants,
                })),
            updateVariant: (id, patch) =>
                set((s) => ({ variants: mapVariant(s.variants, id, (v) => ({ ...v, ...patch })) })),
            updateTaskConfig: (id, patch) =>
                set((s) => ({
                    variants: mapVariant(s.variants, id, (v) => ({
                        ...v,
                        ...(patch.workflow ? { workflow: { ...v.workflow, ...patch.workflow } } : {}),
                        ...(patch.remote ? { remote: { ...v.remote, ...patch.remote } } : {}),
                        ...(patch.scorerRef ? { scorerRef: { ...v.scorerRef, ...patch.scorerRef } } : {}),
                    })),
                })),
            setTemplating: (id, engine) =>
                set((s) => ({ variants: mapVariant(s.variants, id, (v) => ({ ...v, templating: engine })) })),

            loadDataset: ({ id, name, rows }) =>
                set(() => ({
                    datasetId: id,
                    datasetName: name,
                    rows: rows.map((r) => ({
                        id: newId(),
                        datasetItemId: r.datasetItemId,
                        input: r.input,
                        expected: r.expected,
                        metadata: r.metadata,
                        sourceTraceId: r.sourceTraceId,
                        sourceObservationId: r.sourceObservationId,
                    })),
                })),
            clearDataset: () =>
                set(() => ({ datasetId: undefined, datasetName: undefined, rows: [] })),
            addRow: () =>
                set((s) => ({ rows: [...s.rows, { id: newId(), input: '' }] })),
            removeRow: (id) =>
                set((s) => ({ rows: s.rows.filter((r) => r.id !== id) })),
            setRowInput: (id, input) =>
                set((s) => ({
                    rows: s.rows.map((r) => (r.id === id ? { ...r, input } : r)),
                })),
            setRowExpected: (id, expected) =>
                set((s) => ({
                    rows: s.rows.map((r) => (r.id === id ? { ...r, expected } : r)),
                })),
            setRowMetadata: (id, metadata) =>
                set((s) => ({
                    rows: s.rows.map((r) => (r.id === id ? { ...r, metadata } : r)),
                })),
            setScorerConfigIds: (ids) => set(() => ({ scorerConfigIds: ids })),
            setDiffMode: (on) => set(() => ({ diffMode: on })),

            hydrateFromConfig: ({ id, name, config }) =>
                set(() => {
                    const c = (config ?? {}) as Partial<SerializablePlaygroundState>
                    const variants =
                        Array.isArray(c.variants) && c.variants.length ? c.variants : [defaultVariant()]
                    return {
                        playgroundId: id,
                        playgroundName: name,
                        variants: variants.map((v) => ({
                            ...defaultVariant(),
                            ...v,
                            messages:
                                Array.isArray(v.messages) && v.messages.length
                                    ? v.messages
                                    : defaultMessages(),
                        })),
                        datasetId: c.datasetId,
                        datasetName: c.datasetName,
                        rows: Array.isArray(c.rows) ? c.rows : [],
                        scorerConfigIds: Array.isArray(c.scorerConfigIds) ? c.scorerConfigIds : [],
                        diffMode: Boolean(c.diffMode),
                    }
                }),
            setPlaygroundName: (name) => set(() => ({ playgroundName: name })),
            detachPlayground: () => set(() => ({ playgroundId: undefined, playgroundName: undefined })),

            loadConversation: ({ messages, model, temperature, variantId }) =>
                set((s) => {
                    const targetId = variantId ?? s.variants[0]?.id
                    return {
                        variants: mapVariant(s.variants, targetId ?? '', (v) => ({
                            ...v,
                            messages: messages.length
                                ? messages.map((m) => ({ id: newId(), ...m }))
                                : defaultMessages(),
                            ...(model !== undefined ? { model } : {}),
                            ...(temperature !== undefined ? { temperature } : {}),
                        })),
                    }
                }),
            resetConversation: (variantId) =>
                set((s) => {
                    const targetId = variantId ?? s.variants[0]?.id
                    return {
                        variants: mapVariant(s.variants, targetId ?? '', (v) => ({
                            ...v,
                            messages: defaultMessages(),
                        })),
                    }
                }),
        }),
        {
            name: 'playground-composer',
            version: 3,
            migrate: (persisted: unknown, version: number) => {
                let state = (persisted ?? {}) as Record<string, unknown>
                // v1 -> v2: a single shared `messages` array + params-only
                // variants become per-task messages (old prompt copied to each).
                if (version < 2) {
                    const shared = Array.isArray(state.messages)
                        ? (state.messages as ComposerMessage[])
                        : defaultMessages()
                    const oldVariants = Array.isArray(state.variants)
                        ? (state.variants as Array<Partial<PlaygroundVariant>>)
                        : [defaultVariant()]
                    state = {
                        ...state,
                        messages: undefined,
                        variants: oldVariants.map((v) => ({
                            id: v.id ?? newId(),
                            model: v.model ?? '',
                            temperature: v.temperature ?? 0.7,
                            maxTokens: v.maxTokens,
                            topP: v.topP,
                            messages: shared.map((m) => ({ ...m, id: newId() })),
                            templating: 'mustache' as TemplatingEngine,
                        })),
                    }
                }
                // v2 -> v3: every task gains a `type` (default prompt).
                if (Array.isArray(state.variants)) {
                    state = {
                        ...state,
                        variants: (state.variants as Array<Partial<PlaygroundVariant>>).map((v) => ({
                            type: 'prompt' as TaskType,
                            ...v,
                        })),
                    }
                }
                return state
            },
        },
    ),
)

/**
 * Snapshot the current store state as a playground doc's `config` blob (the
 * durable subset: tasks, dataset, rows, scorers, diff flag). Used when saving
 * or autosaving to the server.
 */
export function serializePlaygroundConfig(): SerializablePlaygroundState {
    const s = usePlaygroundStore.getState()
    return {
        variants: s.variants,
        datasetId: s.datasetId,
        datasetName: s.datasetName,
        rows: s.rows,
        scorerConfigIds: s.scorerConfigIds,
        diffMode: s.diffMode,
    }
}
