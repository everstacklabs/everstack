import { create } from 'zustand'
import { persist } from 'zustand/middleware'

// The time window a query was run over. Presets carry only `range`; custom
// windows also carry absolute `from`/`to`. A query without its range is
// meaningless ("failed" over 15m vs 7d are different searches).
export type QueryContext = {
    range?: string
    from?: string
    to?: string
}

export type SavedQuery = {
    id: string
    name: string
    esql: string
    createdAt: number
} & QueryContext

export type QueryHistoryEntry = {
    esql: string
    at: number
} & QueryContext

const HISTORY_LIMIT = 50

interface EsqlQueriesStore {
    saved: SavedQuery[]
    history: QueryHistoryEntry[]

    saveQuery: (name: string, esql: string, ctx?: QueryContext) => void
    renameSaved: (id: string, name: string) => void
    removeSaved: (id: string) => void
    pushHistory: (esql: string, ctx?: QueryContext) => void
    removeHistory: (at: number) => void
    clearHistory: () => void
}

function newId(): string {
    if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
    return `q_${Date.now().toString(36)}_${Math.floor(Math.random() * 1e6).toString(36)}`
}

export const useEsqlQueriesStore = create<EsqlQueriesStore>()(
    persist(
        (set) => ({
            saved: [],
            history: [],

            saveQuery: (name, esql, ctx) => {
                const trimmed = esql.trim()
                if (!trimmed) return
                set((s) => ({
                    saved: [
                        { id: newId(), name: name.trim() || trimmed, esql: trimmed, createdAt: Date.now(), ...ctx },
                        ...s.saved,
                    ],
                }))
            },

            renameSaved: (id, name) =>
                set((s) => ({
                    saved: s.saved.map((q) => (q.id === id ? { ...q, name: name.trim() || q.name } : q)),
                })),

            removeSaved: (id) => set((s) => ({ saved: s.saved.filter((q) => q.id !== id) })),

            pushHistory: (esql, ctx) => {
                const trimmed = esql.trim()
                if (!trimmed) return
                set((s) => {
                    // Drop any earlier identical entry, prepend the new one (with its
                    // latest range), cap the list.
                    const rest = s.history.filter((h) => h.esql !== trimmed)
                    return { history: [{ esql: trimmed, at: Date.now(), ...ctx }, ...rest].slice(0, HISTORY_LIMIT) }
                })
            },

            removeHistory: (at) => set((s) => ({ history: s.history.filter((h) => h.at !== at) })),

            clearHistory: () => set({ history: [] }),
        }),
        { name: 'esql-queries' },
    ),
)
