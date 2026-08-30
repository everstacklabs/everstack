import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import type { Trace } from '@everstack/proto/everstack/traces/v1/traces_pb'
import type { CustomColumnDef } from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import { Iconify, Loader2, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { formatRelativeTime, formatTimestamp } from '@everstack/utils/functions/datetime'
import { lazy, Suspense, startTransition, useState, useRef, useEffect, useMemo } from 'react'
import type { ReactNode } from 'react'
import { Check, AlertCircle, GitCompare, GitBranch, Layers, Zap, User, Hash, Database, Brain, X } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { Route } from '@/routes/observability/traces'
import { TIME_RANGE_LABELS } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import { ESQL_MANAGED_KEYS } from '@/utils/esql'
import { NavigationButtons } from '../common/navigation-buttons'
import { useKeyboardKeys } from '@/hooks/use-keyboard-key'
import { useQuery } from '@tanstack/react-query'
import { getTrace, listCustomColumns } from '@/server/traces'
import { CUSTOM_COLUMNS_QUERY_KEY } from './custom-column-manager'
import { TraceSheetSkeleton } from './trace-sheet-skeleton'
import {
    formatDuration,
    formatCost,
    formatTokensCompact,
    truncateText,
    safeJsonParse,
    safeBigIntToNumber,
} from '@/utils/trace-formatters'
import { cn } from '@everstack/utils/functions/cn'
import { ProviderDisplay } from '../providers/provider-icon'
import { getTraceNameBadge } from '@/utils/span-display-helpers'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { inferProviderFromModel } from '@/utils/infer-provider-from-model'

const { Badge, Tooltip, TooltipProvider } = ui
const TraceSheet = lazy(() => import('./trace-sheet').then((module) => ({ default: module.TraceSheet })))

const RANGE_ORDER: TimeRangePreset[] = ['15m', '6h', '12h', '24h', '3d', '7d', '14d', '30d', '90d']

/**
 * Empty state shown when active filters match nothing in the current window.
 * Offers to widen the time range in one click, since the match may simply be
 * outside the range (the common "I searched an id that exists elsewhere" case).
 */
function SearchEmptyState({
    currentRange,
    onWiden,
}: {
    currentRange: string
    onWiden: (range: TimeRangePreset) => void
}) {
    const curIdx = RANGE_ORDER.indexOf(currentRange as TimeRangePreset)
    const wider = (['30d', '90d'] as TimeRangePreset[]).filter(
        (r) => RANGE_ORDER.indexOf(r) > curIdx,
    )
    const rangeLabel = TIME_RANGE_LABELS[currentRange as TimeRangePreset] ?? 'this range'
    return (
        <div className='flex flex-col items-center justify-center space-y-3 text-center px-4'>
            <Iconify.Icon icon='tabler:search-off' className='size-10 text-brand-main-50 light:text-black' />
            <div className='space-y-1'>
                <div className='text-brand-main-50 font-semibold light:text-black'>No matches in {rangeLabel.replace('Last ', 'the last ')}</div>
                <div className='text-brand-main-50 text-sm max-w-md light:text-black'>
                    Your filters match no traces in this window. The trace you want may be further back.
                </div>
            </div>
            {wider.length > 0 && (
                <div className='flex items-center gap-2 pt-1'>
                    {wider.map((r) => (
                        <button
                            key={r}
                            type='button'
                            onClick={() => onWiden(r)}
                            className='rounded border border-brand-main-600 bg-brand-main-800/60 px-3 py-1.5 text-sm text-brand-main-50 transition-colors hover:border-brand-secondary-500/40 hover:text-brand-main-50 light:bg-black/5 light:text-black'
                        >
                            Search {(TIME_RANGE_LABELS[r] ?? r).replace('Last ', '')}
                        </button>
                    ))}
                </div>
            )}
        </div>
    )
}

// Stable empty reference so the useQuery fallback does not create a new array
// each render (would churn downstream memoization). See the Zustand-selector rule.
const EMPTY_CUSTOM_COLUMNS: CustomColumnDef[] = []

// Reserved custom-column keys the backend injects for the Trace Name column:
// a coding-agent client key (for the logo) and the display name. The
// "__everstack_" prefix keeps them out of the generic custom-column renderer.
const RESERVED_CLIENT_KEY = '__everstack_client'
const RESERVED_TRACE_NAME_KEY = '__everstack_trace_name'
const TEXT_PREVIEW_CACHE_LIMIT = 800
const inputPreviewCache = new Map<string, string>()
const outputPreviewCache = new Map<string, string>()
const timestampObjectCache = new WeakMap<object, string>()
const timestampPrimitiveCache = new Map<string, string>()
const TRACE_SHEET_WORKSPACE_DELAY_MS = 120

function useTraceSheetWorkspaceReady(open: boolean, traceId?: string): boolean {
    const [ready, setReady] = useState(false)
    const wasOpenRef = useRef(false)

    useEffect(() => {
        if (!open || !traceId) {
            wasOpenRef.current = false
            setReady(false)
            return
        }

        if (wasOpenRef.current) {
            setReady(true)
            return
        }

        wasOpenRef.current = true
        setReady(false)

        if (typeof window === 'undefined') {
            setReady(true)
            return
        }

        let timeoutId = 0
        const frameId = window.requestAnimationFrame(() => {
            timeoutId = window.setTimeout(() => setReady(true), TRACE_SHEET_WORKSPACE_DELAY_MS)
        })

        return () => {
            window.cancelAnimationFrame(frameId)
            if (timeoutId) window.clearTimeout(timeoutId)
        }
    }, [open, traceId])

    return ready
}

function TraceSheetOpeningState({ label = 'Opening trace' }: { label?: string }) {
    return <TraceSheetSkeleton label={label} />
}

function TraceSidePanel({
    open,
    onClose,
    header,
    children,
}: {
    open: boolean
    onClose: () => void
    header: ReactNode
    children: ReactNode
}) {
    const [mounted, setMounted] = useState(open)

    useEffect(() => {
        if (open) {
            setMounted(true)
            return
        }

        const timeout = window.setTimeout(() => setMounted(false), 100)
        return () => window.clearTimeout(timeout)
    }, [open])

    useEffect(() => {
        if (!open) return
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') onClose()
        }
        window.addEventListener('keydown', handleKeyDown)
        return () => window.removeEventListener('keydown', handleKeyDown)
    }, [onClose, open])

    if (!mounted) return null

    return (
        <div
            data-slot="trace-details-overlay"
            className={cn(
                'fixed inset-0 z-50',
                open ? 'pointer-events-auto' : 'pointer-events-none',
            )}
        >
            <button
                type="button"
                aria-label="Close trace details"
                className={cn(
                    'absolute inset-0 bg-black/25 transition-opacity duration-75',
                    open ? 'opacity-100' : 'opacity-0',
                )}
                onClick={onClose}
            />
            <aside
                data-slot="trace-details-panel"
                role="dialog"
                aria-modal="false"
                aria-label="Trace details"
                className={cn(
                    'absolute inset-y-0 right-0 flex w-[82vw] flex-col overflow-hidden border-l border-brand-main-600 bg-brand-main-700 text-brand-main-50 antialiased shadow-[-24px_0_50px_-32px_rgba(0,0,0,0.95)] transition-transform duration-100 ease-out light:border-border light:bg-background light:text-foreground',
                    open ? 'translate-x-0 [will-change:auto]' : 'translate-x-full will-change-transform',
                )}
                onClick={(event) => event.stopPropagation()}
            >
                <TooltipProvider>
                    <div data-slot="trace-details-panel-header" className="flex min-h-10 items-center justify-between border-b border-brand-main-500 px-3 light:border-border">
                        <div className="flex min-w-0 flex-1">{header}</div>
                        <button
                            data-slot="trace-details-close"
                            type="button"
                            aria-label="Close trace details"
                            className="z-10 rounded-sm p-1 opacity-70 transition-opacity hover:bg-white/10 hover:opacity-100 focus:outline-none focus:ring-0 light:text-muted-foreground light:hover:bg-black/10 light:hover:text-foreground"
                            onClick={onClose}
                        >
                            <X className="size-4" />
                        </button>
                    </div>
                    {children}
                </TooltipProvider>
            </aside>
        </div>
    )
}

// Map a coding-agent client to a provider name so we reuse the provider logos:
// Claude mark for Claude Code, OpenAI for Codex, Gemini for Gemini CLI, Zhipu /
// Moonshot for GLM / Kimi, and the Cursor / GitHub Copilot brand marks.
const CLIENT_TO_PROVIDER: Record<string, string> = {
    'claude-code': 'claude',
    codex: 'openai',
    'gemini-cli': 'google',
    glm: 'zhipu',
    kimi: 'moonshot',
    cursor: 'cursor',
    'github-copilot': 'github-copilot',
}

// formatCustomColumnValue renders a resolved custom-column string per its
// declared type. Values arrive as strings in trace.customColumns.
function formatCustomColumnValue(raw: string | undefined, valueType: string): string {
    if (raw === undefined || raw === '') return '-'
    if (valueType === 'number') {
        const n = Number(raw)
        return Number.isFinite(n) ? n.toLocaleString() : raw
    }
    if (valueType === 'date') {
        const d = new Date(raw)
        return Number.isNaN(d.getTime()) ? raw : d.toLocaleString()
    }
    return raw
}

function rememberTextPreview(cache: Map<string, string>, key: string, compute: () => string): string {
    const cached = cache.get(key)
    if (cached !== undefined) return cached
    const value = compute()
    cache.set(key, value)
    if (cache.size > TEXT_PREVIEW_CACHE_LIMIT) {
        const oldest = cache.keys().next().value
        if (oldest !== undefined) cache.delete(oldest)
    }
    return value
}

function rememberTimestampFormat(value: unknown): string {
    if (!value) return '-'
    if (typeof value === 'object') {
        const cached = timestampObjectCache.get(value)
        if (cached !== undefined) return cached
        const formatted = formatTimestamp(value)
        timestampObjectCache.set(value, formatted)
        return formatted
    }

    const key = String(value)
    const cached = timestampPrimitiveCache.get(key)
    if (cached !== undefined) return cached
    const formatted = formatTimestamp(value)
    timestampPrimitiveCache.set(key, formatted)
    if (timestampPrimitiveCache.size > TEXT_PREVIEW_CACHE_LIMIT) {
        const oldest = timestampPrimitiveCache.keys().next().value
        if (oldest !== undefined) timestampPrimitiveCache.delete(oldest)
    }
    return formatted
}

// Get method from trace input (Chat, Embeddings, Completions)
function getMethod(trace: Trace): string {
    // Check if it's an embedding based on output format
    if (trace.traceOutput) {
        const output = safeJsonParse<any>(trace.traceOutput, null)
        if (output && (output.dimension || output.magnitude || output.hash)) {
            return 'Embeddings'
        }
        if (typeof trace.traceOutput === 'string' && trace.traceOutput.includes('dim')) {
            return 'Embeddings'
        }
    }

    // Default to Chat for most LLM requests
    return 'Chat'
}

// Extract first message from input JSON
function getInputPreview(traceInput: string): string {
    if (!traceInput) return 'N/A'

    return rememberTextPreview(inputPreviewCache, traceInput, () => {
        const messages = safeJsonParse<any[]>(traceInput, [])
        if (Array.isArray(messages) && messages.length > 0) {
            const firstMsg = messages[0]
            let content = firstMsg.content || ''

            // Handle content being an object or array
            if (typeof content === 'object' && content !== null) {
                if (Array.isArray(content)) {
                    // Handle array of content blocks
                    content = content.map(block => block.text || block.content || JSON.stringify(block)).join(' ')
                } else {
                    // Handle single content object
                    content = content.text || content.content || JSON.stringify(content)
                }
            }

            return truncateText(String(content), 100)
        }
        return truncateText(traceInput, 100)
    })
}

// Extract first choice from output JSON (handles both chat and embedding outputs)
function getOutputPreview(traceOutput: string, isEmbedding: boolean = false): string {
    if (!traceOutput) return 'N/A'

    return rememberTextPreview(outputPreviewCache, `${isEmbedding ? '1' : '0'}:${traceOutput}`, () => {
        // If this is a pre-formatted embedding summary string, display it directly
        if (isEmbedding || traceOutput.includes('dim') || traceOutput.includes('Embedding')) {
            // Check if it's already a formatted string like "[1536 dim, ‖v‖=1.00, hash:abc123]"
            if (traceOutput.startsWith('[') && traceOutput.includes('dim')) {
                return truncateText(traceOutput, 100)
            }
        }

        const parsed = safeJsonParse<any>(traceOutput, null)
        if (parsed) {
            // Check if this is an embedding output (has dimension/magnitude/hash)
            if (parsed.dimension || parsed.magnitude || parsed.hash) {
                const dim = parsed.dimension || 'N/A'
                const mag = typeof parsed.magnitude === 'number' ? parsed.magnitude.toFixed(2) : parsed.magnitude || 'N/A'
                const hash = parsed.hash ? parsed.hash.slice(0, 8) : ''
                return `[${dim} dim, ‖v‖=${mag}${hash ? `, #${hash}` : ''}]`
            }

            // Handle chat completion output
            if (Array.isArray(parsed) && parsed.length > 0) {
                const firstChoice = parsed[0]
                let content = firstChoice.message?.content || firstChoice.text || ''

                // Handle content being an object (e.g., {type: "text", text: "..."})
                if (typeof content === 'object' && content !== null) {
                    if (Array.isArray(content)) {
                        // Handle array of content blocks
                        content = content.map(block => block.text || block.content || JSON.stringify(block)).join(' ')
                    } else {
                        // Handle single content object
                        content = content.text || content.content || JSON.stringify(content)
                    }
                }

                return truncateText(String(content), 100)
            }
        }
        return truncateText(traceOutput, 100)
    })
}

function PayloadPreview({ title, raw }: { title: string; raw: string }) {
    const parsed = safeJsonParse<unknown | null>(raw, null)
    const value = parsed ? JSON.stringify(parsed, null, 2) : raw

    return (
        <div>
            <div className="mb-1 text-xs font-medium uppercase tracking-wide text-brand-main-50 light:text-black">
                {title}
            </div>
            <pre className="max-h-72 overflow-auto rounded border border-brand-main-600 bg-black/30 p-2 text-xs whitespace-pre-wrap break-words text-brand-main-50 light:bg-white/60 light:text-black">
                {value || '-'}
            </pre>
        </div>
    )
}

function ObjectPreview({ title, value }: { title: string; value: Record<string, unknown> | undefined }) {
    const hasValue = value && Object.keys(value).length > 0

    return (
        <div>
            <div className="mb-1 text-xs font-medium uppercase tracking-wide text-brand-main-50 light:text-black">
                {title}
            </div>
            <pre className="max-h-72 overflow-auto rounded border border-brand-main-600 bg-black/30 p-2 text-xs whitespace-pre-wrap break-words text-brand-main-50 light:bg-white/60 light:text-black">
                {hasValue ? JSON.stringify(value, null, 2) : '-'}
            </pre>
        </div>
    )
}

function MetricSummary({ value, label }: { value: string; label?: string }) {
    return (
        <span className="inline-flex min-w-0 items-baseline gap-1">
            <span className="truncate font-semibold text-brand-main-50 light:text-black">{value}</span>
            {label && <span className="text-xs uppercase tracking-wide text-brand-main-50 light:text-black">{label}</span>}
        </span>
    )
}

function getTraceField(trace: Trace, keys: string[]): string | undefined {
    for (const key of keys) {
        const custom = trace.customColumns?.[key]
        if (custom !== undefined && custom !== '') return custom
        const metadata = trace.metadata?.[key]
        if (metadata !== undefined && metadata !== '') return metadata
    }
    return undefined
}

function getTraceCreatedTimestamp(trace: Trace): unknown {
    return getTraceField(trace, ['created_at', 'createdAt', 'created', 'trace.created_at']) || trace.startTime
}

function timestampToDate(value: unknown): Date | null {
    if (!value) return null
    if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
    if (typeof value === 'string' || typeof value === 'number') {
        const date = new Date(value)
        return Number.isNaN(date.getTime()) ? null : date
    }
    if (typeof value === 'object') {
        const candidate = value as { toDate?: () => Date; seconds?: bigint | number | string; nanos?: number }
        if (typeof candidate.toDate === 'function') {
            const date = candidate.toDate()
            return Number.isNaN(date.getTime()) ? null : date
        }
        if ('seconds' in candidate) {
            const seconds = Number(candidate.seconds ?? 0)
            const nanos = Number(candidate.nanos ?? 0)
            const date = new Date(seconds * 1000 + Math.floor(nanos / 1_000_000))
            return Number.isNaN(date.getTime()) ? null : date
        }
    }
    return null
}

function formatTimeInZone(date: Date, timeZone?: string): string {
    return new Intl.DateTimeFormat(undefined, {
        timeZone,
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    }).format(date)
}

function formatDateInZone(date: Date, timeZone?: string): string {
    return new Intl.DateTimeFormat(undefined, {
        timeZone,
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        year: 'numeric',
    }).format(date)
}

function getLocalTimezoneLabel(date: Date): string {
    const parts = new Intl.DateTimeFormat(undefined, { timeZoneName: 'shortOffset' }).formatToParts(date)
    return parts.find((part) => part.type === 'timeZoneName')?.value || Intl.DateTimeFormat().resolvedOptions().timeZone || 'Local'
}

function TimestampConversionTooltip({ value }: { value: unknown }) {
    const date = timestampToDate(value)
    if (!date) {
        return <div className="px-3 py-2 text-sm text-brand-main-50 light:text-black">No timestamp</div>
    }

    return (
        <div className="min-w-72 px-3 py-2">
            <div className="mb-2 flex items-center justify-between gap-4 border-b border-white/10 pb-2 text-sm light:border-black/10">
                <span className="text-brand-main-50 light:text-black">Time conversion</span>
                <span className="text-brand-main-50 light:text-black">{formatRelativeTime(date)}</span>
            </div>
            <div className="grid grid-cols-[76px_1fr_1.2fr] gap-x-4 gap-y-2 text-sm text-brand-main-50 light:text-black">
                <span>{getLocalTimezoneLabel(date)}</span>
                <span>{formatTimeInZone(date)}</span>
                <span>{formatDateInZone(date)}</span>
                <span>UTC</span>
                <span>{formatTimeInZone(date, 'UTC')}</span>
                <span>{formatDateInZone(date, 'UTC')}</span>
            </div>
        </div>
    )
}

function getTraceNumberField(trace: Trace, keys: string[]): number | undefined {
    const raw = getTraceField(trace, keys)
    if (raw === undefined) return undefined
    const n = Number(raw)
    return Number.isFinite(n) ? n : undefined
}

function getTraceDurationNsField(trace: Trace, nsKeys: string[], msKeys: string[]): number | undefined {
    const ns = getTraceNumberField(trace, nsKeys)
    if (ns !== undefined) return ns
    const ms = getTraceNumberField(trace, msKeys)
    return ms !== undefined ? ms * 1_000_000 : undefined
}

function getTraceExpected(trace: Trace): string | undefined {
    return getTraceField(trace, ['expected', 'expected_output', 'expectedOutput', 'reference_output', 'referenceOutput'])
}

function getTraceTags(trace: Trace): string[] {
    const raw = getTraceField(trace, ['tags', 'trace.tags', 'labels'])
    if (!raw) return []
    const parsed = safeJsonParse<unknown | null>(raw, null)
    if (Array.isArray(parsed)) return parsed.map(String).filter(Boolean)
    return raw.split(',').map((tag) => tag.trim()).filter(Boolean)
}

function getTraceLlmDurationNs(trace: Trace): number | undefined {
    return getTraceDurationNsField(
        trace,
        ['llm_duration_ns', 'llm.duration_ns', 'llm.duration', 'llm.latency_ns'],
        ['llm_duration_ms', 'llm.duration_ms', 'llm.latency_ms', 'latency_ms'],
    )
}

function getTraceTtftNs(trace: Trace): number | undefined {
    return getTraceDurationNsField(
        trace,
        ['ttft_ns', 'llm.ttft_ns', 'llm.time_to_first_token_ns', 'llm_time_to_first_token_ns'],
        ['ttft_ms', 'llm.ttft_ms', 'llm.stream.time_to_first_token_ms', 'llm_time_to_first_token_ms'],
    )
}

function getTraceLlmCalls(trace: Trace): number | undefined {
    return getTraceNumberField(trace, ['llm_calls', 'llm.calls', 'llm_call_count', 'llm.call_count'])
}

function getTraceToolCalls(trace: Trace): number | undefined {
    return getTraceNumberField(trace, ['tool_calls', 'tool_call_count', 'agent.total_tool_calls', 'agent.turn.tool_calls'])
}

function sumTraceNumbers(rows: Trace[], getter: (trace: Trace) => number | bigint | undefined): number {
    return rows.reduce((sum, trace) => {
        const value = getter(trace)
        if (value === undefined) return sum
        return sum + (typeof value === 'bigint' ? safeBigIntToNumber(value) : value)
    }, 0)
}

function p50TraceNumber(rows: Trace[], getter: (trace: Trace) => number | bigint | undefined): number | undefined {
    const values = rows
        .map((trace) => {
            const value = getter(trace)
            return value === undefined ? undefined : typeof value === 'bigint' ? safeBigIntToNumber(value) : value
        })
        .filter((value): value is number => value !== undefined && Number.isFinite(value) && value > 0)
        .sort((a, b) => a - b)

    if (values.length === 0) return undefined
    return values[Math.floor((values.length - 1) / 2)]
}

export const TracesTable = ({
    pageTraces,
    isLoading,
    selectedTraceId: selectedTraceIdProp,
    hasInstanceData,
    isLiveMode,
    fetchNextPage,
    isFetchingMore,
    error,
}: {
    pageTraces: Trace[],
    isLoading?: boolean,
    isLiveMode?: boolean,
    selectedTraceId?: string,
    hasInstanceData?: boolean,
    fetchNextPage?: () => void,
    hasMore?: boolean,
    isFetchingMore?: boolean,
    error?: Error | null,
}) => {
    const navigate = Route.useNavigate()
    const search = Route.useSearch()
    const hasActiveFilters =
        Boolean(search.q) || ESQL_MANAGED_KEYS.some((k) => Boolean((search as Record<string, unknown>)[k]))
    const selectedTraceId = selectedTraceIdProp || null
    const isExpanded = !!selectedTraceId
    const [isCopied, setIsCopied] = useState(false)
    const [tableTooltipsSuspended, setTableTooltipsSuspended] = useState(isExpanded)

    // Selection state for checkboxes
    const [selectedRows, setSelectedRows] = useState<Set<string | number>>(new Set())

    // Refs for scrolling to selected row
    const rowRefs = useRef<Map<string | number, HTMLTableRowElement | null>>(new Map())

    // Find the current trace from pageTraces (always gets latest data)
    const traceInFilteredList = selectedTraceId
        ? pageTraces.find(trace => trace.traceId === selectedTraceId) ?? null
        : null

    // If trace is not in filtered list but we have a selectedTraceId, fetch it by ID
    // This handles cases where the trace is outside the current time range
    const shouldFetchTrace = !!selectedTraceId && !traceInFilteredList
    const { data: fetchedTrace, isLoading: isLoadingTrace } = useQuery({
        queryKey: ['trace', selectedTraceId],
        queryFn: () => getTrace(selectedTraceId!),
        enabled: shouldFetchTrace,
    })

    // Tenant-defined custom columns. Rendered after the fixed columns; managed
    // via the Columns dialog. Each reads a value the backend resolved into
    // trace.customColumns keyed by the column key.
    const { data: customColumnDefs = EMPTY_CUSTOM_COLUMNS } = useQuery({
        queryKey: CUSTOM_COLUMNS_QUERY_KEY,
        queryFn: () => listCustomColumns(),
        staleTime: 60_000,
    })

    // Use trace from filtered list if available, otherwise use fetched trace
    const currentTrace = traceInFilteredList || fetchedTrace || null
    const isTraceWorkspaceReady = useTraceSheetWorkspaceReady(isExpanded, currentTrace?.traceId || selectedTraceId || undefined)

    // Navigation helpers
    const currentIndex = selectedTraceId
        ? pageTraces.findIndex(trace => trace.traceId === selectedTraceId)
        : -1

    const canGoPrevious = currentIndex > 0
    const canGoNext = currentIndex >= 0 && currentIndex < pageTraces.length - 1

    const handlePrevious = () => {
        if (canGoPrevious) {
            setTableTooltipsSuspended(true)
            startTransition(() => {
                navigate({
                    search: (prev) => ({
                        ...prev,
                        trace: pageTraces[currentIndex - 1].traceId
                    })
                })
            })
        }
    }

    const handleNext = () => {
        if (canGoNext) {
            setTableTooltipsSuspended(true)
            startTransition(() => {
                navigate({
                    search: (prev) => ({
                        ...prev,
                        trace: pageTraces[currentIndex + 1].traceId
                    })
                })
            })
        }
    }

    const handleSheetClose = () => {
        startTransition(() => {
            navigate({
                search: (prev) => ({
                    ...prev,
                    trace: undefined,
                    span: undefined,
                })
            })
        })
    }

    const handleRowClick = (trace: Trace) => {
        setTableTooltipsSuspended(true)
        startTransition(() => {
            navigate({
                search: (prev) => ({
                    ...prev,
                    trace: trace.traceId
                })
            })
        })
    }

    useEffect(() => {
        setTableTooltipsSuspended(isExpanded)
    }, [isExpanded])

    const handleCopy = async (text: string) => {
        await copyToClipboard(text)
        toast.success(`Trace ID copied to clipboard`)
        setIsCopied(true)
        setTimeout(() => setIsCopied(false), 2000)
    }

    // Keyboard navigation
    useKeyboardKeys(
        ['ArrowUp', 'ArrowDown'],
        (key) => {
            if (key === 'ArrowUp') {
                handlePrevious()
            } else if (key === 'ArrowDown') {
                handleNext()
            }
        },
        {
            enabled: isExpanded,
            preventDefault: true
        }
    )

    // Scroll selected trace into view when it changes
    useEffect(() => {
        if (selectedTraceId && rowRefs.current.has(selectedTraceId)) {
            const rowElement = rowRefs.current.get(selectedTraceId)
            if (rowElement) {
                rowElement.scrollIntoView({
                    behavior: 'auto',
                    block: 'nearest',
                })
            }
        }
    }, [selectedTraceId])

    const columns = useMemo<ColumnConfig<Trace>[]>(() => {
        const traceTimeTailColumns: ColumnConfig<Trace>[] = [
        {
            id: 'start_time',
            header: 'Start Time',
            accessor: 'startTime',
            width: 170,
            minWidth: 140,
            sizing: 'content',
            kind: 'date',
            sortable: true,
            tooltip: (trace) => trace.startTime ? <TimestampConversionTooltip value={trace.startTime} /> : false,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 tabular-nums light:text-black'>
                    {rememberTimestampFormat(trace.startTime)}
                </span>
            ),
        },
        {
            id: 'end_time',
            header: 'End Time',
            accessor: 'endTime',
            width: 170,
            minWidth: 140,
            sizing: 'content',
            kind: 'date',
            sortable: true,
            tooltip: (trace) => trace.endTime ? <TimestampConversionTooltip value={trace.endTime} /> : false,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 tabular-nums light:text-black'>
                    {rememberTimestampFormat(trace.endTime)}
                </span>
            ),
        },
    ]

        const baseColumns: ColumnConfig<Trace>[] = [
        {
            id: 'status',
            header: '',
            accessor: 'status',
            width: 40,
            minWidth: 40,
            maxWidth: 40,
            sizing: 'fixed',
            kind: 'status',
            align: 'center',
            tooltip: (trace) => trace.status?.toUpperCase() === 'ERROR' ? 'Error' : 'OK',
            render: (trace: Trace) => {
                // Determine error status based on root span status only
                // errorCount includes non-critical errors (like cache misses) which shouldn't mark the trace as failed
                const isError = trace.status?.toUpperCase() === 'ERROR'
                return (
                    <div className='flex items-center justify-center'>
                        <span
                            className={cn(
                                'size-2 rounded-full ring-4',
                                isError
                                    ? 'bg-rose-400 ring-rose-500/15'
                                    : 'bg-emerald-400/80 ring-emerald-500/10',
                            )}
                        />
                    </div>
                )
            },
        },
        {
            id: 'created_at',
            header: 'Created At',
            width: 170,
            minWidth: 140,
            sizing: 'content',
            kind: 'date',
            sortable: true,
            summary: (rows) => <MetricSummary value={rows.length.toLocaleString()} label="visible" />,
            tooltip: (trace) => <TimestampConversionTooltip value={getTraceCreatedTimestamp(trace)} />,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 tabular-nums light:text-black'>
                    {rememberTimestampFormat(getTraceCreatedTimestamp(trace))}
                </span>
            ),
        },
        {
            id: 'trace_name',
            header: 'Trace Name',
            width: 190,
            minWidth: 120,
            sizing: 'content',
            grow: 1.2,
            tooltip: (trace) => {
                const client = trace.customColumns?.[RESERVED_CLIENT_KEY]
                const name = trace.customColumns?.[RESERVED_TRACE_NAME_KEY] || client
                return [name, client ? `client: ${client}` : ''].filter(Boolean).join(' · ') || false
            },
            render: (trace: Trace) => {
                const client = trace.customColumns?.[RESERVED_CLIENT_KEY]
                const name =
                    trace.customColumns?.[RESERVED_TRACE_NAME_KEY] || client
                if (!name) {
                    return <span className='text-brand-main-50 light:text-black'>—</span>
                }
                const badge = getTraceNameBadge(String(name))
                return (
                    <span className='flex items-center gap-2 min-w-0'>
                        {client ? (
                            <span className='flex size-5 shrink-0 items-center justify-center rounded border border-white/10 bg-white/5 light:border-black/10 light:bg-black/[0.04]'>
                                <ProviderDisplay
                                    providerName={CLIENT_TO_PROVIDER[client] ?? client}
                                    isActive={false}
                                    useImage={true}
                                    size='sm'
                                />
                            </span>
                        ) : (
                            <span
                                className={cn(
                                    'flex size-5 shrink-0 items-center justify-center rounded border',
                                    badge.bg,
                                    badge.border,
                                )}
                            >
                                <Iconify.Icon
                                    icon={badge.icon}
                                    className={cn('size-3.5', badge.iconColor)}
                                />
                            </span>
                        )}
                        <span className='text-sm text-brand-main-50 truncate light:text-black'>
                            {name}
                        </span>
                    </span>
                )
            },
        },
        {
            id: 'trace_id',
            header: 'Trace ID',
            accessor: 'traceId',
            width: 120,
            minWidth: 90,
            kind: 'code',
            copyValue: (trace) => trace.traceId || false,
            tooltip: (trace) => trace.traceId || false,
            render: (trace: Trace) =>
                trace.traceId ? (
                    <span
                        className='text-sm text-brand-main-50 truncate block cursor-pointer hover:text-brand-main-50 light:text-black light:hover:text-black'
                        onClick={(e) => {
                            e.stopPropagation()
                            copyToClipboard(trace.traceId)
                            toast.success('Trace ID copied')
                        }}
                    >
                        {trace.traceId.slice(0, 12)}
                    </span>
                ) : (
                    <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                ),
        },
        {
            id: 'duration',
            header: 'Duration',
            accessor: 'totalDuration',
            width: 120,
            minWidth: 100,
            kind: 'number',
            align: 'right',
            summary: (rows) => {
                const p50 = p50TraceNumber(rows, (trace) => trace.totalDuration as any)
                return <MetricSummary value={p50 === undefined ? '-' : formatDuration(p50)} label="p50" />
            },
            render: (trace: Trace) => {
                return (
                    <span className={cn('text-sm tabular-nums text-brand-main-50 light:text-black')}>
                        {formatDuration(trace.totalDuration)}
                    </span>
                )
            },
        },
        {
            id: 'llm_duration',
            header: 'LLM Duration',
            width: 140,
            minWidth: 110,
            kind: 'number',
            align: 'right',
            summary: (rows) => {
                const p50 = p50TraceNumber(rows, getTraceLlmDurationNs)
                return <MetricSummary value={p50 === undefined ? '-' : formatDuration(p50)} label="p50" />
            },
            render: (trace: Trace) => {
                const duration = getTraceLlmDurationNs(trace)
                return (
                    <span className='text-sm tabular-nums text-brand-main-50 light:text-black'>
                        {duration === undefined ? '-' : formatDuration(duration)}
                    </span>
                )
            },
        },
        {
            id: 'ttft',
            header: 'Time to First Token',
            width: 170,
            minWidth: 130,
            kind: 'number',
            align: 'right',
            summary: (rows) => {
                const p50 = p50TraceNumber(rows, getTraceTtftNs)
                return <MetricSummary value={p50 === undefined ? '-' : formatDuration(p50)} label="p50" />
            },
            render: (trace: Trace) => {
                const ttft = getTraceTtftNs(trace)
                return (
                    <span className='text-sm tabular-nums text-brand-main-50 light:text-black'>
                        {ttft === undefined ? '-' : formatDuration(ttft)}
                    </span>
                )
            },
        },
        {
            id: 'llm_calls',
            header: 'LLM Calls',
            width: 120,
            minWidth: 95,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={sumTraceNumbers(rows, getTraceLlmCalls).toLocaleString()} label="sum" />,
            render: (trace: Trace) => {
                const calls = getTraceLlmCalls(trace)
                return (
                    <span className='text-sm tabular-nums text-brand-main-50 light:text-black'>
                        {calls === undefined ? '-' : calls.toLocaleString()}
                    </span>
                )
            },
        },
        {
            id: 'tool_calls',
            header: 'Tool Calls',
            width: 120,
            minWidth: 95,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={sumTraceNumbers(rows, getTraceToolCalls).toLocaleString()} label="sum" />,
            render: (trace: Trace) => {
                const calls = getTraceToolCalls(trace)
                return (
                    <span className='text-sm tabular-nums text-brand-main-50 light:text-black'>
                        {calls === undefined ? '-' : calls.toLocaleString()}
                    </span>
                )
            },
        },
        {
            id: 'provider',
            header: 'Provider',
            accessor: 'provider',
            width: 140,
            minWidth: 120,
            sizing: 'content',
            summary: (rows) => {
                const providers = new Set(rows.map((trace) => {
                    const model = trace.servedModel || trace.llmModel || trace.requestedModel
                    return trace.provider || inferProviderFromModel(model)
                }).filter(Boolean))
                return <MetricSummary value={providers.size.toLocaleString()} label="providers" />
            },
            tooltip: (trace) => {
                const model = trace.servedModel || trace.llmModel || trace.requestedModel
                const provider = trace.provider || inferProviderFromModel(model)
                return provider ? capitalize(provider) : false
            },
            render: (trace: Trace) => {
                // No explicit provider? Infer one from the model id so external
                // telemetry (e.g. Claude Code) still shows a real logo.
                const model = trace.servedModel || trace.llmModel || trace.requestedModel
                const provider = trace.provider || inferProviderFromModel(model)
                return (
                    <div className='inline-flex items-center gap-2'>
                        <div className='flex items-center justify-center size-7 shrink-0 rounded bg-brand-main-600 border border-brand-main-500'>
                            <ProviderDisplay
                                providerName={provider || 'N/A'}
                                isActive={false}
                                useImage={true}
                                size='sm'
                            />
                        </div>
                        <span className='text-sm text-brand-main-50 font-medium light:text-black'>
                            {provider ? capitalize(provider) : '—'}
                        </span>
                    </div>
                )
            },
        },
        {
            id: 'model',
            header: 'Model',
            accessor: 'servedModel',
            width: 250,
            minWidth: 200,
            maxWidth: 350,
            sizing: 'fluid',
            kind: 'code',
            copyValue: (trace) => trace.servedModel || trace.llmModel || trace.requestedModel || false,
            tooltip: (trace) => trace.servedModel || trace.llmModel || trace.requestedModel || false,
            summary: (rows) => {
                const models = new Set(rows.map((trace) => trace.servedModel || trace.llmModel || trace.requestedModel).filter(Boolean))
                return <MetricSummary value={models.size.toLocaleString()} label="models" />
            },
            render: (trace: Trace) => (
                <div className='flex items-center gap-2 py-1'>
                    <span className='break-words text-sm truncate'>
                        {trace.servedModel || trace.llmModel || trace.requestedModel || '—'}
                    </span>
                </div>
            ),
        },
        {
            id: 'type',
            header: 'Type',
            width: 170,
            minWidth: 110,
            sizing: 'content',
            tooltip: (trace) => trace.traceKinds?.length ? trace.traceKinds.join(', ') : getMethod(trace),
            render: (trace: Trace) => {
                const kinds = trace.traceKinds ?? []
                // Plain gateway calls (no agent/workflow/sandbox spans) fall back to
                // the chat/embeddings method, which is their effective type.
                if (kinds.length === 0) {
                    return (
                        <Badge
                            variant='outline'
                            className='text-sm py-0.5 bg-brand-main-700/40 border-brand-main-500 text-brand-main-50 light:text-black'
                        >
                            {getMethod(trace)}
                        </Badge>
                    )
                }
                return (
                    <div className='flex items-center gap-1 flex-wrap'>
                        {kinds.map((k) => (
                            <Badge
                                key={k}
                                variant='outline'
                                className='text-sm py-0.5 bg-brand-main-700/50 border-brand-main-500 text-brand-main-50 capitalize light:text-black'
                            >
                                {k}
                            </Badge>
                        ))}
                    </div>
                )
            },
        },
        {
            id: 'user',
            header: 'User',
            accessor: 'userId',
            width: 130,
            minWidth: 90,
            kind: 'code',
            copyValue: (trace) => trace.userId || false,
            tooltip: (trace) => trace.userId || false,
            render: (trace: Trace) =>
                trace.userId ? (
                    <span className='flex items-center gap-1 text-sm text-brand-main-50 truncate light:text-black'>
                        <User className='w-3 h-3 text-brand-main-50 shrink-0 light:text-black' />
                        <span className='truncate'>{trace.userId}</span>
                    </span>
                ) : (
                    <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                ),
        },
        {
            id: 'session',
            header: 'Session',
            accessor: 'sessionId',
            width: 130,
            minWidth: 90,
            kind: 'code',
            copyValue: (trace) => trace.sessionId || false,
            tooltip: (trace) => trace.sessionId || false,
            render: (trace: Trace) =>
                trace.sessionId ? (
                    <span className='flex items-center gap-1 text-sm text-brand-main-50 truncate light:text-black'>
                        <Hash className='w-3 h-3 text-brand-main-50 shrink-0 light:text-black' />
                        <span className='truncate'>{trace.sessionId}</span>
                    </span>
                ) : (
                    <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                ),
        },
        {
            id: 'thread',
            header: 'Thread',
            accessor: 'threadId',
            width: 130,
            minWidth: 90,
            kind: 'code',
            copyValue: (trace) => trace.threadId || false,
            tooltip: (trace) => trace.threadId || false,
            render: (trace: Trace) =>
                trace.threadId ? (
                    <span className='flex items-center gap-1 text-sm text-brand-main-50 truncate light:text-black'>
                        <GitBranch className='w-3 h-3 text-brand-main-50 shrink-0 light:text-black' />
                        <span className='truncate'>{trace.threadId}</span>
                    </span>
                ) : (
                    <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                ),
        },
        {
            id: 'input',
            header: 'Input',
            accessor: 'traceInput',
            width: 300,
            maxWidth: 400,
            minWidth: 200,
            sizing: 'fluid',
            grow: 2,
            kind: 'code',
            resizable: true,
            copyValue: (trace) => trace.traceInput || false,
            preview: (trace) => trace.traceInput ? <PayloadPreview title="Input" raw={trace.traceInput} /> : false,
            headerTooltip: 'Trace input preview. Hover truncated cells or open the preview for the full payload.',
            render: (trace: Trace) => {
                const preview = getInputPreview(trace.traceInput)
                return (
                    <span className='text-sm text-brand-main-50 truncate block light:text-black'>{preview}</span>
                )
            },
        },
        {
            id: 'output',
            header: 'Output',
            accessor: 'traceOutput',
            width: 300,
            maxWidth: 400,
            minWidth: 200,
            sizing: 'fluid',
            grow: 2,
            kind: 'code',
            resizable: true,
            copyValue: (trace) => trace.traceOutput || false,
            preview: (trace) => trace.traceOutput ? <PayloadPreview title="Output" raw={trace.traceOutput} /> : false,
            headerTooltip: 'Trace output preview. Hover truncated cells or open the preview for the full payload.',
            render: (trace: Trace) => {
                const preview = getOutputPreview(trace.traceOutput)
                return (
                    <span className='text-sm text-brand-main-50 truncate block light:text-black'>{preview}</span>
                )
            },
        },
        {
            id: 'expected',
            header: 'Expected',
            width: 180,
            minWidth: 120,
            maxWidth: 320,
            sizing: 'fluid',
            grow: 1.2,
            kind: 'code',
            copyValue: (trace) => getTraceExpected(trace) || false,
            preview: (trace) => {
                const expected = getTraceExpected(trace)
                return expected ? <PayloadPreview title="Expected" raw={expected} /> : false
            },
            render: (trace: Trace) => {
                const expected = getTraceExpected(trace)
                return (
                    <span className='text-sm text-brand-main-50 truncate block light:text-black'>
                        {expected ? truncateText(expected, 100) : '-'}
                    </span>
                )
            },
        },
        {
            id: 'tags',
            header: 'Tags',
            width: 160,
            minWidth: 100,
            maxWidth: 280,
            sizing: 'fluid',
            grow: 1,
            copyValue: (trace) => {
                const tags = getTraceTags(trace)
                return tags.length ? tags.join(', ') : false
            },
            render: (trace: Trace) => {
                const tags = getTraceTags(trace)
                if (tags.length === 0) return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <span className='flex min-w-0 items-center gap-1 overflow-hidden'>
                        {tags.slice(0, 2).map((tag) => (
                            <Badge
                                key={tag}
                                variant='outline'
                                className='max-w-24 truncate px-1.5 py-0 text-xs text-brand-main-50 border-brand-main-500 bg-brand-main-700/40 light:text-black'
                            >
                                {tag}
                            </Badge>
                        ))}
                        {tags.length > 2 && (
                            <span className='text-xs text-brand-main-50 light:text-black'>+{tags.length - 2}</span>
                        )}
                    </span>
                )
            },
        },
        {
            id: 'cost',
            header: 'Cost',
            accessor: 'totalCost',
            width: 110,
            minWidth: 90,
            kind: 'money',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatCost(rows.reduce((sum, trace) => sum + trace.totalCost, 0))} label="sum" />,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 light:text-black'>
                    {formatCost(trace.totalCost)}
                </span>
            ),
        },
        {
            id: 'tokens',
            header: 'Tokens',
            width: 120,
            minWidth: 90,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatTokensCompact(sumTraceNumbers(rows, (trace) => trace.tokenBreakdown?.totalTokens))} label="sum" />,
            render: (trace: Trace) => {
                const total = trace.tokenBreakdown?.totalTokens
                const formatted = formatTokensCompact(total)
                if (formatted === '-') return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <span className='text-sm text-brand-main-50 flex items-center gap-1 light:text-black'>
                        <Zap className='w-3 h-3 text-brand-main-50 light:text-black' />
                        {formatted}
                    </span>
                )
            },
        },
        {
            id: 'input_tokens',
            header: 'In Tokens',
            width: 100,
            minWidth: 80,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatTokensCompact(sumTraceNumbers(rows, (trace) => trace.tokenBreakdown?.inputTokens))} label="sum" />,
            render: (trace: Trace) => {
                const v = trace.tokenBreakdown?.inputTokens
                if (!v || Number(v) <= 0) return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <span className='text-sm text-brand-main-50 light:text-black'>
                        {formatTokensCompact(v)}
                    </span>
                )
            },
        },
        {
            id: 'output_tokens',
            header: 'Out Tokens',
            width: 100,
            minWidth: 80,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatTokensCompact(sumTraceNumbers(rows, (trace) => trace.tokenBreakdown?.outputTokens))} label="sum" />,
            render: (trace: Trace) => {
                const v = trace.tokenBreakdown?.outputTokens
                if (!v || Number(v) <= 0) return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <span className='text-sm text-brand-main-50 light:text-black'>
                        {formatTokensCompact(v)}
                    </span>
                )
            },
        },
        {
            id: 'cached',
            header: 'Cached',
            width: 100,
            minWidth: 70,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatTokensCompact(sumTraceNumbers(rows, (trace) => trace.tokenBreakdown?.promptDetails?.cachedTokens))} label="sum" />,
            render: (trace: Trace) => {
                const cached = trace.tokenBreakdown?.promptDetails?.cachedTokens
                if (!cached || Number(cached) <= 0) {
                    return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                }
                return (
                    <span className='text-sm text-brand-main-50 flex items-center gap-1 light:text-black'>
                        <Database className='w-3 h-3 text-brand-main-50 light:text-black' />
                        {formatTokensCompact(cached)}
                    </span>
                )
            },
        },
        {
            id: 'reasoning',
            header: 'Reasoning',
            width: 100,
            minWidth: 80,
            kind: 'number',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatTokensCompact(sumTraceNumbers(rows, (trace) => trace.tokenBreakdown?.completionDetails?.reasoningTokens))} label="sum" />,
            render: (trace: Trace) => {
                const reasoning = trace.tokenBreakdown?.completionDetails?.reasoningTokens
                if (!reasoning || Number(reasoning) <= 0) {
                    return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                }
                return (
                    <span className='text-sm text-brand-main-50 flex items-center gap-1 light:text-black'>
                        <Brain className='w-3 h-3 text-brand-main-50 light:text-black' />
                        {formatTokensCompact(reasoning)}
                    </span>
                )
            },
        },
        {
            id: 'errors',
            header: 'Errors',
            width: 80,
            minWidth: 60,
            kind: 'number',
            align: 'center',
            summary: (rows) => <MetricSummary value={sumTraceNumbers(rows, (trace) => trace.errorCount).toLocaleString()} label="sum" />,
            render: (trace: Trace) => {
                const n = trace.errorCount || 0
                if (n <= 0) return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <Badge
                        variant='outline'
                        className='text-sm py-0.5 bg-rose-500/10 text-rose-400 border-rose-500/30'
                    >
                        {n}
                    </Badge>
                )
            },
        },
        {
            id: 'metadata',
            header: 'Metadata',
            accessor: 'metadata',
            width: 110,
            minWidth: 80,
            kind: 'json',
            align: 'center',
            copyValue: (trace) => trace.metadata ? JSON.stringify(trace.metadata, null, 2) : false,
            preview: (trace) => trace.metadata ? <ObjectPreview title="Metadata" value={trace.metadata as Record<string, unknown>} /> : false,
            render: (trace: Trace) => {
                const count = trace.metadata ? Object.keys(trace.metadata).length : 0
                if (count === 0) return <span className='text-sm text-brand-main-50 light:text-black'>-</span>
                return (
                    <Badge
                        variant='outline'
                        className='text-sm py-0.5 bg-brand-main-700/50 border-brand-main-500 text-brand-main-50 light:text-black'
                    >
                        {count} {count === 1 ? 'key' : 'keys'}
                    </Badge>
                )
            },
        },
        {
            id: 'spans',
            header: 'Spans',
            width: 80,
            minWidth: 60,
            kind: 'number',
            align: 'center',
            summary: (rows) => <MetricSummary value={sumTraceNumbers(rows, (trace) => trace.spanCount).toLocaleString()} label="sum" />,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 flex items-center gap-1 light:text-black'>
                    <Layers className='w-3 h-3 text-brand-main-50 light:text-black' />
                    {trace.spanCount || '-'}
                </span>
            ),
        },
    ]

    // Keep lifecycle timestamps before tenant-defined dimensions so custom
    // columns remain the rightmost extension area.
        baseColumns.push(...traceTimeTailColumns)

        for (const def of customColumnDefs) {
            baseColumns.push({
            id: `custom:${def.key}`,
            header: def.label || def.key,
            width: 130,
            minWidth: 90,
            sizing: def.valueType === 'text' ? 'fluid' : 'content',
            kind: def.valueType === 'number' ? 'number' : def.valueType === 'date' ? 'date' : 'text',
            align: def.valueType === 'number' ? 'right' : 'left',
            copyValue: (trace) => trace.customColumns?.[def.key] || false,
            tooltip: (trace) => trace.customColumns?.[def.key] || false,
            render: (trace: Trace) => (
                <span className='text-sm text-brand-main-50 truncate block light:text-black'>
                    {formatCustomColumnValue(trace.customColumns?.[def.key], def.valueType)}
                </span>
            ),
            })
        }

        return baseColumns
    }, [customColumnDefs])

    return (
        <div className='flex-1 min-h-0 flex flex-col overflow-auto antialiased scrollbar-macos'>
            <ResponsiveTable
                tableId="observability.traces"
                enableColumnReorder
                forceLeftAlign
                columns={columns}
                data={pageTraces}
                isLoading={isLoading}
                onRowClick={handleRowClick}
                enableSelection
                selectedRows={selectedRows}
                onSelectionChange={setSelectedRows}
                rowKey={(trace) => trace.traceId}
                rowClassName={(trace) => cn(
                    'transition-colors',
                    trace.traceId === selectedTraceId
                        ? 'bg-brand-secondary-500/20'
                        : 'hover:bg-brand-main-700/30',
                )}
                rowRefs={rowRefs}
                minTableWidth='1800px'
                enableCellTooltips={!tableTooltipsSuspended}
                enableVirtualization={true}
                estimatedRowHeight={41}
                loadingState={
                    <div className='flex text-sm items-center justify-center'>
                        <div className='flex items-center justify-center space-x-2'>
                            <Loader2 className='size-4 animate-spin text-brand-main-100' />
                            <span className='text-brand-main-100 font-normal'>Loading traces...</span>
                        </div>
                    </div>
                }
                emptyMessage={
                    <div className='flex text-sm items-center justify-center'>
                        {/* State 1: Error State */}
                        {error ? (
                            <div className='flex flex-col items-center justify-center space-y-3 text-center px-4'>
                                <AlertCircle className='size-12 text-rose-400' />
                                <div className='space-y-1'>
                                    <div className='text-brand-main-50 font-semibold light:text-black'>Error Loading Traces</div>
                                    <div className='text-brand-main-50 text-sm max-w-md light:text-black'>
                                        {error.message || 'An unexpected error occurred while loading traces.'}
                                    </div>
                                </div>
                            </div>
                        ) : /* State 2: Empty/Onboarding State - First time user, no data yet */
                            hasInstanceData === false && pageTraces.length === 0 && !isLoading ? (
                                <div className='flex flex-col items-center justify-center'>
                                    <div className='relative mb-6'>
                                        <div className='absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl' />
                                        <div className='relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4'>
                                            <Iconify.Icon icon='lucide:list-tree' className='size-8 text-brand-secondary-400' />
                                        </div>
                                    </div>
                                    <h3 className='text-base font-medium text-brand-main-50 mb-2 light:text-brand-main-50'>Welcome to Everstack Traces</h3>
                                    <p className='text-sm text-brand-main-50 max-w-sm text-center leading-relaxed mb-4 light:text-black'>
                                        Start sending requests through your gateway to see traces appear here in real-time.
                                    </p>
                                    <div className='p-3 bg-brand-main-900/50 rounded-lg border border-brand-main-600/30 max-w-lg'>
                                        <div className='text-brand-main-50 text-sm text-left space-y-2 light:text-black'>
                                            <div className='font-semibold text-brand-main-50 light:text-black'>Getting Started:</div>
                                            <ol className='list-decimal list-inside space-y-1 text-brand-main-50 light:text-black'>
                                                <li>Configure your API key in the gateway settings</li>
                                                <li>Send your first request through the gateway</li>
                                                <li>Watch your traces appear here automatically</li>
                                            </ol>
                                        </div>
                                    </div>
                                </div>
                            ) : /* State 3: Listening State - Waiting for traces */
                                pageTraces.length === 0 ? (
                                    isLiveMode ? (
                                        <div className='flex items-center justify-center space-x-2'>
                                            <Loader2 className='size-4 animate-spin text-brand-main-100' />
                                            <span className='text-brand-main-100 font-normal'>Listening for traces...</span>
                                        </div>
                                    ) : hasActiveFilters ? (
                                        <SearchEmptyState
                                            currentRange={(search.range as string) || '7d'}
                                            onWiden={(range) =>
                                                navigate({
                                                    search: (prev) => ({
                                                        ...prev,
                                                        range,
                                                        from: undefined,
                                                        to: undefined,
                                                        live: 'false',
                                                    }),
                                                    replace: true,
                                                })
                                            }
                                        />
                                    ) : (
                                        <div className='flex flex-col items-center justify-center space-y-3 text-center px-4'>
                                            <Iconify.Icon icon="tabler:search-off" className='size-10 text-brand-main-50 light:text-black' />
                                            <div className='space-y-1'>
                                                <div className='text-brand-main-50 font-semibold light:text-black'>No traces found</div>
                                                <div className='text-brand-main-50 text-sm max-w-md light:text-black'>
                                                    No traces found for the selected time range.
                                                </div>
                                            </div>
                                        </div>
                                    )
                                ) : null}
                    </div>
                }
                onScrollNearEnd={fetchNextPage}
                isLoadingMore={isFetchingMore}
            />
            <TraceSidePanel
                open={isExpanded}
                onClose={handleSheetClose}
                header={(
                    <div className='flex min-w-0 items-center mr-2'>
                        <div className='flex items-center gap-2 mr-2'>
                            <NavigationButtons
                                canGoPrevious={canGoPrevious}
                                canGoNext={canGoNext}
                                onPrevious={handlePrevious}
                                onNext={handleNext}
                                previousLabel='Previous Trace'
                                nextLabel='Next Trace'
                                iconClassName='size-3'
                            />
                        </div>
                        {selectedTraceId && (
                            <div className='flex min-w-0 items-center justify-start gap-2 py-2 text-brand-main-50 font-semibold light:text-brand-main-50'>
                                <Tooltip content={
                                    <div className='flex items-center gap-2 px-2 py-1'>
                                        <span className='text-brand-main-50 text-sm light:text-black'>Copy Trace ID</span>
                                    </div>
                                }>
                                    <Badge variant='secondary' className='text-sm rounded-sm py-1.5' onClick={() => handleCopy(selectedTraceId)}>
                                        <span className='text-brand-main-50 text-sm light:text-black'>{selectedTraceId}</span>
                                    </Badge>
                                </Tooltip>
                                {isCopied && (
                                    <div>
                                        <Check className='size-3 text-green-500' />
                                    </div>
                                )}
                                <Link
                                    to="/observability/traces/compare"
                                    search={{ a: selectedTraceId }}
                                    className="inline-flex items-center gap-1 text-xs font-normal text-brand-main-50 hover:text-brand-secondary-300 border border-brand-main-500 rounded px-2 py-0.5 transition-colors light:text-black"
                                    title="Compare this trace with another"
                                >
                                    <GitCompare className="h-3 w-3" />
                                    Compare
                                </Link>
                            </div>
                        )}
                    </div>
                )}
            >
                <div className="flex min-h-0 flex-1 flex-col">
                    {isLoadingTrace && shouldFetchTrace ? (
                        <TraceSheetSkeleton label="Loading trace" />
                    ) : currentTrace ? (
                        isTraceWorkspaceReady ? (
                            <Suspense fallback={<TraceSheetOpeningState label="Loading trace workspace" />}>
                                <TraceSheet
                                    trace={currentTrace}
                                    traces={pageTraces}
                                />
                            </Suspense>
                        ) : (
                            <TraceSheetOpeningState />
                        )
                    ) : selectedTraceId ? (
                        <div className="flex h-64 items-center justify-center">
                            <div className="flex flex-col items-center gap-2 text-center px-4">
                                <Iconify.Icon icon="tabler:alert-circle" className="size-12 text-brand-main-50 light:text-black" />
                                <div className="text-brand-main-50 font-semibold light:text-black">Trace not found</div>
                                <div className="text-brand-main-50 text-sm light:text-black">
                                    Trace ID: <span className="">{selectedTraceId}</span>
                                </div>
                            </div>
                        </div>
                    ) : null}
                </div>
            </TraceSidePanel>
        </div>
    )
}
