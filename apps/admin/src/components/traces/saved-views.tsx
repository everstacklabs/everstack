import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import {
    Bookmark,
    BookmarkPlus,
    Check,
    Copy,
    Link as LinkIcon,
    Trash2,
} from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import {
    listTraceViews,
    upsertTraceView,
    deleteTraceView,
} from '@/server/traces'
import type { TraceView } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

const { Button, Popover, PopoverContent, PopoverTrigger, Tooltip, TooltipProvider } = ui

/**
 * Saved-views UI for the traces page. Backed by the tenant-scoped
 * otel_trace_views store (ListTraceViews / UpsertTraceView / DeleteTraceView),
 * so views sync across devices and teammates. The view config is an opaque
 * JSON blob: { path, search, createdAt }.
 */

export const SAVED_VIEWS_QUERY_KEY = ['trace-saved-views'] as const

const EMPTY_VIEWS: TraceView[] = []

export type SavedView = {
    id: string
    name: string
    /** Path the view belongs to — currently always /observability/traces. */
    path: string
    /** Search params as a flat record, saved exactly as Tanstack Router
     * serialises them so restoration is lossless. */
    search: Record<string, unknown>
    createdAt: number
}

type StoredConfig = {
    path: string
    search: Record<string, unknown>
    createdAt?: number
}

function toSavedView(v: TraceView): SavedView | null {
    try {
        const cfg = JSON.parse(v.configJson) as StoredConfig
        if (!cfg || typeof cfg.path !== 'string' || !cfg.search) return null
        return {
            id: v.id,
            name: v.name,
            path: cfg.path,
            search: cfg.search,
            createdAt: cfg.createdAt ?? 0,
        }
    } catch {
        return null
    }
}

interface SavedViewsProps {
    /** Current path the view belongs to. */
    path: string
    /** Current search params snapshot. */
    search: Record<string, unknown>
    /** Apply a saved view's search params back to the URL. */
    onApply: (search: Record<string, unknown>) => void
    className?: string
}

export function SavedViews({ path, search, onApply, className }: SavedViewsProps) {
    const queryClient = useQueryClient()
    const [pendingName, setPendingName] = useState('')
    const [copied, setCopied] = useState(false)

    const { data: rawViews = EMPTY_VIEWS } = useQuery({
        queryKey: SAVED_VIEWS_QUERY_KEY,
        queryFn: () => listTraceViews(),
        staleTime: 60_000,
    })

    const ownViews = useMemo(
        () =>
            (rawViews.map(toSavedView).filter(Boolean) as SavedView[]).filter(
                (v) => v.path === path,
            ),
        [rawViews, path],
    )

    const invalidate = () =>
        queryClient.invalidateQueries({ queryKey: SAVED_VIEWS_QUERY_KEY })

    const upsert = useMutation({
        mutationFn: (name: string) =>
            upsertTraceView({
                name,
                configJson: JSON.stringify({ path, search, createdAt: Date.now() }),
            }),
        onSuccess: () => {
            invalidate()
            setPendingName('')
        },
    })

    const remove = useMutation({
        mutationFn: (id: string) => deleteTraceView(id),
        onSuccess: invalidate,
    })

    const save = () => {
        const name = pendingName.trim()
        if (!name) return
        upsert.mutate(name)
    }

    const copyLink = async () => {
        if (typeof window === 'undefined') return
        try {
            await navigator.clipboard.writeText(window.location.href)
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
        } catch {
            // ignore — clipboard may be unavailable in non-secure contexts
        }
    }

    return (
        <TooltipProvider>
            <div className={cn('flex items-center gap-1', className)}>
                <Tooltip content="Copy a shareable link to this view">
                    <Button
                        variant="outline"
                        className="text-brand-main-50 h-8 w-8 hover:text-brand-main-50 light:text-black light:hover:text-brand-main-50"
                        onClick={copyLink}
                    >
                        {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <LinkIcon className="h-3.5 w-3.5" />}
                    </Button>
                </Tooltip>

                <Popover>
                    <Tooltip content="Saved views">
                        <PopoverTrigger asChild>
                            <Button variant="outline" className="text-brand-main-50 h-8 w-8 hover:text-brand-main-50 light:text-black light:hover:text-brand-main-50">
                                <Bookmark className="h-3.5 w-3.5" />
                            </Button>
                        </PopoverTrigger>
                    </Tooltip>
                    <PopoverContent
                        side="bottom"
                        align="end"
                        className="w-72 p-2 bg-brand-main-900 border-brand-main-500"
                    >
                        <div className="flex items-center gap-2 mb-2 pb-2 border-b border-brand-main-700">
                            <BookmarkPlus className="h-3.5 w-3.5 text-brand-main-200" />
                            <span className="text-xs font-medium text-brand-main-50 light:text-brand-main-50">Save current view</span>
                        </div>
                        <div className="flex items-center gap-1">
                            <input
                                type="text"
                                placeholder="View name"
                                value={pendingName}
                                onChange={(e) => setPendingName(e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter') save()
                                }}
                                className="flex-1 bg-brand-main-700/60 text-xs text-zinc-200 rounded px-2 py-1 border border-brand-main-500 focus:border-brand-secondary-500 outline-none"
                            />
                            <Button
                                size="sm"
                                className="h-6 text-[11px] px-2"
                                disabled={!pendingName.trim() || upsert.isPending}
                                onClick={save}
                            >
                                Save
                            </Button>
                        </div>

                        <div className="mt-3 pt-2 border-t border-brand-main-700">
                            <div className="text-[10px] text-brand-main-50 uppercase tracking-wide mb-1.5 light:text-black">
                                Saved ({ownViews.length})
                            </div>
                            {ownViews.length === 0 ? (
                                <div className="text-xs text-brand-main-50 py-2 text-center light:text-black">No saved views yet.</div>
                            ) : (
                                <div className="space-y-0.5 max-h-60 overflow-auto">
                                    {ownViews
                                        .slice()
                                        .sort((a, b) => b.createdAt - a.createdAt)
                                        .map((v) => (
                                            <div
                                                key={v.id}
                                                className="group/view flex items-center justify-between gap-1 px-1.5 py-1 rounded hover:bg-brand-main-700/40"
                                            >
                                                <button
                                                    type="button"
                                                    onClick={() => onApply(v.search)}
                                                    className="flex-1 min-w-0 text-left text-xs text-brand-main-50 hover:text-brand-main-50 truncate light:text-black light:hover:text-brand-main-50"
                                                    title={v.name}
                                                >
                                                    {v.name}
                                                </button>
                                                <Tooltip content="Copy this view's link">
                                                    <button
                                                        type="button"
                                                        onClick={(e) => {
                                                            e.stopPropagation()
                                                            const usp = new URLSearchParams()
                                                            for (const [k, val] of Object.entries(v.search)) {
                                                                if (val === undefined || val === null) continue
                                                                usp.set(k, String(val))
                                                            }
                                                            const url = `${window.location.origin}${v.path}?${usp.toString()}`
                                                            navigator.clipboard.writeText(url).catch(() => { })
                                                        }}
                                                        className="opacity-0 group-hover/view:opacity-100 transition-opacity text-brand-main-50 hover:text-brand-main-50 light:text-black light:hover:text-brand-main-50"
                                                    >
                                                        <Copy className="h-3 w-3" />
                                                    </button>
                                                </Tooltip>
                                                <Tooltip content="Delete">
                                                    <button
                                                        type="button"
                                                        disabled={remove.isPending}
                                                        onClick={(e) => {
                                                            e.stopPropagation()
                                                            remove.mutate(v.id)
                                                        }}
                                                        className="opacity-0 group-hover/view:opacity-100 transition-opacity text-brand-main-50 hover:text-rose-400 light:text-black"
                                                    >
                                                        <Trash2 className="h-3 w-3" />
                                                    </button>
                                                </Tooltip>
                                            </div>
                                        ))}
                                </div>
                            )}
                        </div>
                        <div className="mt-2 pt-2 border-t border-brand-main-700 text-[10px] text-brand-main-50 leading-snug light:text-black">
                            Saved to your workspace and shared across devices.
                        </div>
                    </PopoverContent>
                </Popover>
            </div>
        </TooltipProvider>
    )
}
