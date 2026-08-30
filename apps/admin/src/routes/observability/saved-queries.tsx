import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { BookmarkPlus, Check, Pencil, Play, Trash2, X } from 'lucide-react'
import dayjs from 'dayjs'
import { ui } from '@everstack/ui'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { cn } from '@/lib/utils'
import { TIME_RANGE_LABELS } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import {
    useEsqlQueriesStore,
    type QueryContext,
    type QueryHistoryEntry,
    type SavedQuery,
} from '@/stores/esql-queries-store'
import { describeEsql, esqlToSearchParams, parseEsql } from '@/utils/esql'

const { Tabs, TabsList, TabsTrigger, TabsContent, Button } = ui

export const Route = createFileRoute('/observability/saved-queries')({
    component: SavedQueriesPage,
    validateSearch: z.object({
        tab: z.enum(['saved', 'history']).optional(),
        filter: z.string().optional(),
    }),
})

const TAB_TRIGGER_CLASS =
    'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function explain(esql: string): string {
    const parsed = parseEsql(esql)
    return parsed.ok ? describeEsql(parsed.query) : 'Invalid query'
}

function rangeLabel(rec: QueryContext): string {
    if (rec.range === 'custom' && rec.from && rec.to) {
        return `${dayjs(rec.from).format('MMM D, HH:mm')} – ${dayjs(rec.to).format('MMM D, HH:mm')}`
    }
    return rec.range ? (TIME_RANGE_LABELS[rec.range as TimeRangePreset] ?? rec.range) : '—'
}

function SavedQueriesPage() {
    const navigate = Route.useNavigate()
    const search = Route.useSearch()
    const tab = search.tab ?? 'saved'
    const needle = (search.filter ?? '').trim().toLowerCase()

    const saved = useEsqlQueriesStore((s) => s.saved)
    const history = useEsqlQueriesStore((s) => s.history)
    const saveQuery = useEsqlQueriesStore((s) => s.saveQuery)
    const removeSaved = useEsqlQueriesStore((s) => s.removeSaved)
    const renameSaved = useEsqlQueriesStore((s) => s.renameSaved)
    const removeHistory = useEsqlQueriesStore((s) => s.removeHistory)
    const clearHistory = useEsqlQueriesStore((s) => s.clearHistory)

    const [editingId, setEditingId] = useState<string | null>(null)
    const [nameDraft, setNameDraft] = useState('')
    const [savingAt, setSavingAt] = useState<number | null>(null)
    const [saveName, setSaveName] = useState('')

    const run = (rec: { esql: string } & QueryContext) => {
        const { params } = esqlToSearchParams(rec.esql)
        navigate({
            to: '/observability/traces',
            // Restore the filter AND its time window; a search pauses live tailing.
            search: {
                ...params,
                q: rec.esql,
                range: rec.range,
                from: rec.from,
                to: rec.to,
                live: 'false',
            } as never,
        })
    }

    const savedRows = useMemo(
        () =>
            needle
                ? saved.filter((q) => q.name.toLowerCase().includes(needle) || q.esql.toLowerCase().includes(needle))
                : saved,
        [saved, needle],
    )
    const historyRows = useMemo(
        () => (needle ? history.filter((h) => h.esql.toLowerCase().includes(needle)) : history),
        [history, needle],
    )

    const savedColumns = useMemo<ColumnConfig<SavedQuery>[]>(
        () => [
            {
                id: 'name',
                header: 'Name',
                width: 240,
                minWidth: 140,
                render: (q) =>
                    editingId === q.id ? (
                        <input
                            value={nameDraft}
                            autoFocus
                            onChange={(e) => setNameDraft(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                    renameSaved(q.id, nameDraft)
                                    setEditingId(null)
                                }
                                if (e.key === 'Escape') setEditingId(null)
                            }}
                            onBlur={() => {
                                renameSaved(q.id, nameDraft)
                                setEditingId(null)
                            }}
                            className="w-full rounded border border-brand-secondary-500/50 bg-brand-main-950 px-2 py-0.5 text-xs text-white outline-none"
                        />
                    ) : (
                        <span className="truncate text-xs font-medium text-white/90 light:text-black/85">{q.name}</span>
                    ),
            },
            {
                id: 'query',
                header: 'Query (ESQL)',
                width: 360,
                minWidth: 200,
                render: (q) => (
                    <code className="block truncate font-mono text-xs text-brand-secondary-100 light:text-brand-secondary-800">{q.esql}</code>
                ),
            },
            {
                id: 'matches',
                header: 'Matches',
                width: 260,
                minWidth: 160,
                render: (q) => <span className="block truncate text-xs text-brand-main-100/60">{explain(q.esql)}</span>,
            },
            {
                id: 'range',
                header: 'Range',
                width: 160,
                minWidth: 110,
                render: (q) => <span className="truncate text-xs text-brand-main-100/70">{rangeLabel(q)}</span>,
            },
            {
                id: 'saved',
                header: 'Saved',
                width: 130,
                minWidth: 100,
                render: (q) => <span className="text-xs text-brand-main-100/70">{dayjs(q.createdAt).format('MMM D, YYYY')}</span>,
            },
            {
                id: 'actions',
                header: '',
                width: 150,
                minWidth: 150,
                render: (q) => (
                    <RowActions>
                        <IconBtn
                            label="Rename"
                            onClick={() => {
                                setNameDraft(q.name)
                                setEditingId(q.id)
                            }}
                        >
                            <Pencil className="size-3.5" />
                        </IconBtn>
                        <IconBtn label="Delete" danger onClick={() => removeSaved(q.id)}>
                            <Trash2 className="size-3.5" />
                        </IconBtn>
                        <RunButton onClick={() => run(q)} />
                    </RowActions>
                ),
            },
        ],
        [editingId, nameDraft, renameSaved, removeSaved, run],
    )

    const historyColumns = useMemo<ColumnConfig<QueryHistoryEntry>[]>(
        () => [
            {
                id: 'query',
                header: 'Query (ESQL)',
                width: 380,
                minWidth: 200,
                render: (h) => (
                    <code className="block truncate font-mono text-xs text-brand-secondary-100 light:text-brand-secondary-800">{h.esql}</code>
                ),
            },
            {
                id: 'matches',
                header: 'Matches',
                width: 260,
                minWidth: 160,
                render: (h) => <span className="block truncate text-xs text-brand-main-100/60">{explain(h.esql)}</span>,
            },
            {
                id: 'range',
                header: 'Range',
                width: 160,
                minWidth: 110,
                render: (h) => <span className="truncate text-xs text-brand-main-100/70">{rangeLabel(h)}</span>,
            },
            {
                id: 'ran',
                header: 'Ran',
                width: 130,
                minWidth: 100,
                render: (h) => <span className="text-xs text-brand-main-100/70">{dayjs(h.at).format('MMM D, HH:mm')}</span>,
            },
            {
                id: 'actions',
                header: '',
                width: 210,
                minWidth: 210,
                render: (h) =>
                    savingAt === h.at ? (
                        <div className="flex items-center justify-end gap-1">
                            <input
                                value={saveName}
                                autoFocus
                                placeholder="Name…"
                                onChange={(e) => setSaveName(e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter' && saveName.trim()) {
                                        saveQuery(saveName, h.esql, { range: h.range, from: h.from, to: h.to })
                                        setSavingAt(null)
                                        setSaveName('')
                                    }
                                    if (e.key === 'Escape') setSavingAt(null)
                                }}
                                className="w-40 rounded border border-brand-secondary-500/50 bg-brand-main-950 px-2 py-0.5 text-xs text-white outline-none"
                            />
                            <IconBtn
                                label="Confirm save"
                                onClick={() => {
                                    if (saveName.trim()) {
                                        saveQuery(saveName, h.esql, { range: h.range, from: h.from, to: h.to })
                                        setSavingAt(null)
                                        setSaveName('')
                                    }
                                }}
                            >
                                <Check className="size-3.5" />
                            </IconBtn>
                            <IconBtn label="Cancel" onClick={() => setSavingAt(null)}>
                                <X className="size-3.5" />
                            </IconBtn>
                        </div>
                    ) : (
                        <RowActions>
                            <IconBtn
                                label="Save this query"
                                onClick={() => {
                                    setSaveName('')
                                    setSavingAt(h.at)
                                }}
                            >
                                <BookmarkPlus className="size-3.5" />
                            </IconBtn>
                            <IconBtn label="Remove from history" danger onClick={() => removeHistory(h.at)}>
                                <Trash2 className="size-3.5" />
                            </IconBtn>
                            <RunButton onClick={() => run(h)} />
                        </RowActions>
                    ),
            },
        ],
        [savingAt, saveName, saveQuery, removeHistory, run],
    )

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <Tabs
                value={tab}
                onValueChange={(v) =>
                    navigate({ search: (prev) => ({ ...prev, tab: v as 'saved' | 'history' }), replace: true })
                }
                className="flex min-h-0 w-full flex-1 flex-col"
            >
                <div className="flex shrink-0 items-center gap-3 border-b border-brand-main-700/40 px-4 py-2.5">
                    <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1 light:border-brand-main-700 light:bg-white">
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="saved">
                            Saved
                            <Count n={saved.length} />
                        </TabsTrigger>
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="history">
                            History
                            <Count n={history.length} />
                        </TabsTrigger>
                    </TabsList>
                    {tab === 'history' && history.length > 0 && (
                        <Button
                            variant="outline"
                            onClick={clearHistory}
                            className="ml-auto text-white/60 hover:text-rose-300"
                        >
                            Clear history
                        </Button>
                    )}
                </div>

                <TabsContent value="saved" className="min-h-0 flex-1 overflow-hidden">
                    <ResponsiveTable
                        columns={savedColumns}
                        data={savedRows}
                        rowKey={(q) => q.id}
                        minTableWidth="100%"
                        emptyMessage={
                            <EmptyState
                                title={saved.length === 0 ? 'No saved queries yet' : 'No matches'}
                                body={saved.length === 0 ? 'Save a query from the History tab to pin it here.' : 'No saved queries match your filter.'}
                            />
                        }
                    />
                </TabsContent>
                <TabsContent value="history" className="min-h-0 flex-1 overflow-hidden">
                    <ResponsiveTable
                        columns={historyColumns}
                        data={historyRows}
                        rowKey={(h) => String(h.at)}
                        minTableWidth="100%"
                        emptyMessage={
                            <EmptyState
                                title={history.length === 0 ? 'No recent searches' : 'No matches'}
                                body={history.length === 0 ? 'Filters you run in Traces show up here so you can pick them back up.' : 'No history entries match your filter.'}
                            />
                        }
                    />
                </TabsContent>
            </Tabs>
        </div>
    )
}

function Count({ n }: { n: number }) {
    return <span className="rounded bg-brand-main-900 px-1.5 py-0.5 font-mono text-[10px] text-white/50">{n}</span>
}

function EmptyState({ title, body }: { title: string; body: string }) {
    return (
        <div className="flex flex-col items-center justify-center px-4 py-10 text-center">
            <div className="text-sm font-medium text-white/70 light:text-black/70">{title}</div>
            <div className="mt-1 max-w-sm text-xs text-white/40 light:text-black/45">{body}</div>
        </div>
    )
}

function RowActions({ children }: { children: React.ReactNode }) {
    return <div className="flex items-center justify-end gap-1">{children}</div>
}

function RunButton({ onClick }: { onClick: () => void }) {
    return (
        <Button onClick={onClick}>
            <Play className="size-3" />
            Run
        </Button>
    )
}

function IconBtn({ label, onClick, danger, children }: { label: string; onClick: () => void; danger?: boolean; children: React.ReactNode }) {
    return (
        <Button
            variant="ghost"
            aria-label={label}
            title={label}
            onClick={onClick}
            className={cn('text-white/50', danger && 'hover:text-rose-300')}
        >
            {children}
        </Button>
    )
}
