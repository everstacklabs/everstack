import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { type RequestLog } from '@everstack/proto/everstack/logs/v1/logs_pb'
import { Iconify, Loader2, ui } from '@everstack/ui'
import { formatRelativeTime } from '@everstack/utils/functions/datetime'
import { useRef, useEffect, useMemo } from 'react'
import { ProviderDisplay } from '../providers/provider-icon'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { Route } from '@/routes/observability/logs'
import { useKeyboardKeys } from '@/hooks/use-keyboard-key'
import { safeBigIntToNumber } from '@/utils/trace-formatters'
import { LogDetailSheet } from './log-detail-sheet'

const { Badge } = ui

// Format milliseconds to human-readable
function formatLatency(ms: number | bigint): string {
    const num = typeof ms === 'bigint' ? safeBigIntToNumber(ms) : ms
    if (num < 1000) return `${num}ms`
    return `${(num / 1000).toFixed(2)}s`
}

// Format tokens with comma separators
function formatTokens(tokens: number | bigint): string {
    const num = typeof tokens === 'bigint' ? safeBigIntToNumber(tokens) : tokens
    return num.toLocaleString()
}

// Format cost in USD
function formatCost(cost: number): string {
    return `$${cost.toFixed(4)}`
}

function LogPayloadPreview({ log }: { log: RequestLog }) {
    const payload = log.payload ? safeParseJson(log.payload) : null
    return (
        <div className="space-y-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
                <span className="text-white/40 light:text-black/40">Correlation</span>
                <span className="font-mono text-white/80 light:text-black/80">{log.correlationId || '-'}</span>
                <span className="text-white/40 light:text-black/40">Trace</span>
                <span className="font-mono text-white/80 light:text-black/80">{log.traceId || '-'}</span>
                <span className="text-white/40 light:text-black/40">Span</span>
                <span className="font-mono text-white/80 light:text-black/80">{log.spanId || '-'}</span>
                <span className="text-white/40 light:text-black/40">Attempts</span>
                <span className="font-mono text-white/80 light:text-black/80">{log.attemptCount || 1}</span>
            </div>
            {(log.requestText || log.responseText) && (
                <div className="space-y-2">
                    {log.requestText && (
                        <PreviewBlock title="Request" value={log.requestText} />
                    )}
                    {log.responseText && (
                        <PreviewBlock title="Response" value={log.responseText} />
                    )}
                </div>
            )}
            {payload !== null && <PreviewBlock title="Payload" value={JSON.stringify(payload, null, 2) ?? String(payload)} />}
        </div>
    )
}

function PreviewBlock({ title, value }: { title: string; value: string }) {
    return (
        <div>
            <div className="mb-1 text-[10px] font-medium uppercase tracking-wide text-white/35 light:text-black/35">{title}</div>
            <pre className="max-h-40 overflow-auto rounded border border-brand-main-600 bg-black/30 p-2 font-mono text-[11px] whitespace-pre-wrap break-words text-white/75 light:bg-white/60 light:text-black/75">
                {value}
            </pre>
        </div>
    )
}

function safeParseJson(raw: string): unknown | null {
    try {
        return JSON.parse(raw)
    } catch {
        return null
    }
}

function MetricSummary({ value, label }: { value: string; label?: string }) {
    return (
        <span className="inline-flex min-w-0 items-baseline gap-1">
            <span className="truncate font-semibold text-white/85 light:text-black/85">{value}</span>
            {label && <span className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">{label}</span>}
        </span>
    )
}

function sumLogs(rows: RequestLog[], getter: (log: RequestLog) => number | bigint): number {
    return rows.reduce((total, row) => {
        const value = getter(row)
        return total + (typeof value === 'bigint' ? safeBigIntToNumber(value) : value)
    }, 0)
}

function p50Logs(rows: RequestLog[], getter: (log: RequestLog) => number | bigint): number {
    const values = rows
        .map((row) => {
            const value = getter(row)
            return typeof value === 'bigint' ? safeBigIntToNumber(value) : value
        })
        .filter((value) => Number.isFinite(value) && value > 0)
        .sort((a, b) => a - b)

    if (values.length === 0) return 0
    return values[Math.floor((values.length - 1) / 2)]
}

export const LogsTable = ({
    pageLogs,
    isLoading,
    selectedLogId: selectedLogIdProp,
    hasInstanceData,
    isLiveMode,
    fetchNextPage,
    isFetchingMore,
}: {
    pageLogs: RequestLog[],
    isLoading?: boolean,
    isLiveMode?: boolean,
    selectedLogId?: string,
    hasInstanceData?: boolean,
    fetchNextPage?: () => void,
    isFetchingMore?: boolean,
}) => {
    const navigate = Route.useNavigate()
    const selectedLogId = selectedLogIdProp || null
    const isExpanded = !!selectedLogId

    // Refs for scrolling to selected row
    const rowRefs = useRef<Map<string | number, HTMLTableRowElement | null>>(new Map())

    // Find the current log from pageLogs (always gets latest data)
    const currentLog = selectedLogId
        ? pageLogs.find(log => log.correlationId === selectedLogId) ?? null
        : null

    // Navigation helpers
    const currentIndex = selectedLogId
        ? pageLogs.findIndex(log => log.correlationId === selectedLogId)
        : -1

    const canGoPrevious = currentIndex > 0
    const canGoNext = currentIndex >= 0 && currentIndex < pageLogs.length - 1

    const handlePrevious = () => {
        if (canGoPrevious) {
            navigate({
                search: (prev) => ({
                    ...prev,
                    log: pageLogs[currentIndex - 1].correlationId
                })
            })
        }
    }

    const handleNext = () => {
        if (canGoNext) {
            navigate({
                search: (prev) => ({
                    ...prev,
                    log: pageLogs[currentIndex + 1].correlationId
                })
            })
        }
    }

    const handleSheetClose = () => {
        navigate({
            search: (prev) => ({
                ...prev,
                log: undefined
            })
        })
    }

    const handleRowClick = (log: RequestLog) => {
        navigate({
            search: (prev) => ({
                ...prev,
                log: log.correlationId
            })
        })
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

    // Scroll selected log into view when it changes
    useEffect(() => {
        if (selectedLogId && rowRefs.current.has(selectedLogId)) {
            const rowElement = rowRefs.current.get(selectedLogId)
            if (rowElement) {
                rowElement.scrollIntoView({
                    behavior: 'smooth',
                    block: 'nearest',
                })
            }
        }
    }, [selectedLogId])

    const columns = useMemo<ColumnConfig<RequestLog>[]>(() => [
        {
            id: 'time',
            header: 'Time',
            width: 150,
            minWidth: 120,
            sizing: 'content',
            kind: 'date',
            tooltip: (log) => log.firstTimestamp || undefined,
            summary: (rows) => <MetricSummary value={rows.length.toLocaleString()} label="visible" />,
            render: (log: RequestLog) => (
                <span className='text-sm text-white/70 light:text-black/70'>
                    {formatRelativeTime(log.timestamp)}
                </span>
            ),
        },
        {
            id: 'provider',
            header: 'Provider',
            accessor: 'provider',
            width: 140,
            minWidth: 120,
            sizing: 'content',
            tooltip: (log) => log.provider || false,
            summary: (rows) => {
                const providers = new Set(rows.map((log) => log.provider).filter(Boolean))
                return <MetricSummary value={providers.size.toLocaleString()} label="providers" />
            },
            render: (log: RequestLog) => (
                <div className='inline-flex items-center gap-2 bg-brand-main-800/50 border border-brand-main-600/30 rounded-md px-2.5 w-fit'>
                    <ProviderDisplay
                        providerName={log.provider || 'N/A'}
                        isActive={false}
                        useImage={true}
                    />
                    <span className='text-xs text-white/90 light:text-black/90 font-medium'>
                        {capitalize(log.provider || 'N/A')}
                    </span>
                </div>
            ),
        },
        {
            id: 'model',
            header: 'Model',
            accessor: 'servedModel',
            width: 250,
            minWidth: 180,
            maxWidth: 350,
            sizing: 'fluid',
            kind: 'code',
            copyValue: (log) => log.servedModel || log.model || false,
            tooltip: (log) => [log.servedModel || log.model, log.fallbackOccurred ? 'fallback used' : ''].filter(Boolean).join(' · ') || false,
            preview: (log) => <LogPayloadPreview log={log} />,
            summary: (rows) => {
                const models = new Set(rows.map((log) => log.servedModel || log.model).filter(Boolean))
                return <MetricSummary value={models.size.toLocaleString()} label="models" />
            },
            render: (log: RequestLog) => (
                <div className='flex items-center gap-2 max-w-full overflow-hidden'>
                    <span className='break-words text-sm truncate'>{log.servedModel || log.model || 'N/A'}</span>
                    {log.fallbackOccurred && (
                        <Badge variant='warning' className='text-[10px] px-1.5 py-0 flex-shrink-0'>
                            FALLBACK
                        </Badge>
                    )}
                </div>
            ),
        },
        {
            id: 'latency',
            header: 'Latency',
            accessor: 'latencyMs',
            width: 110,
            minWidth: 90,
            kind: 'number',
            align: 'right',
            tooltip: (log) => `${formatLatency(log.latencyMs)} latency`,
            summary: (rows) => <MetricSummary value={formatLatency(p50Logs(rows, (log) => log.latencyMs))} label="p50" />,
            render: (log: RequestLog) => (
                <span className='font-mono text-sm'>{formatLatency(log.latencyMs)}</span>
            ),
        },
        {
            id: 'tokens',
            header: 'Tokens',
            accessor: 'totalTokens',
            width: 110,
            minWidth: 90,
            kind: 'number',
            align: 'right',
            tooltip: (log) => `${formatTokens(log.promptTokens)} prompt · ${formatTokens(log.completionTokens)} completion`,
            summary: (rows) => <MetricSummary value={formatTokens(sumLogs(rows, (log) => log.totalTokens))} label="sum" />,
            render: (log: RequestLog) => (
                <span className='font-mono text-sm'>
                    {log.totalTokens > 0 ? formatTokens(log.totalTokens) : '-'}
                </span>
            ),
        },
        {
            id: 'cost',
            header: 'Cost',
            accessor: 'cost',
            width: 110,
            minWidth: 90,
            kind: 'money',
            align: 'right',
            summary: (rows) => <MetricSummary value={formatCost(rows.reduce((total, log) => total + log.cost, 0))} label="sum" />,
            render: (log: RequestLog) => (
                <span className='font-mono text-sm'>
                    {log.cost > 0 ? formatCost(log.cost) : '-'}
                </span>
            ),
        },
        {
            id: 'status',
            header: 'Status',
            accessor: 'status',
            width: 110,
            minWidth: 90,
            sizing: 'content',
            kind: 'status',
            align: 'center',
            tooltip: (log) => log.status || false,
            summary: (rows) => {
                const failures = rows.filter((log) => log.status?.toLowerCase() === 'error').length
                return <MetricSummary value={failures ? `${failures}` : '0'} label="errors" />
            },
            render: (log: RequestLog) => (
                <Badge variant={log.status.toLowerCase() === 'success' ? 'success' : log.status === 'error' ? 'error' : 'warning'} className='text-xs'>
                    {log.status.toUpperCase()}
                </Badge>
            ),
        },
    ], [])

    return (
        <div className='flex-1 min-h-0 flex flex-col overflow-hidden'>
            <ResponsiveTable
                tableId="observability.logs"
                enableColumnReorder
                forceLeftAlign
                columns={columns}
                data={pageLogs}
                isLoading={isLoading}
                onRowClick={handleRowClick}
                rowKey={(log) => log.correlationId}
                rowClassName={(log) => log.correlationId === selectedLogId ? 'bg-brand-secondary-500/20' : ''}
                rowRefs={rowRefs}
                enableCellTooltips
                enableVirtualization={true}
                estimatedRowHeight={41}
                emptyMessage={
                    <div className='flex text-sm items-center justify-center h-full'>
                        {hasInstanceData === false && pageLogs.length === 0 && !isLoading ? (
                            <div className='flex flex-col items-center justify-center'>
                                <div className='relative mb-6'>
                                    <div className='absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl' />
                                    <div className='relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4'>
                                        <Iconify.Icon icon='tabler:logs' className='size-8 text-brand-secondary-400' />
                                    </div>
                                </div>
                                <h3 className='text-base font-medium text-white light:text-brand-main-50 mb-2'>Welcome to Everstack Logs</h3>
                                <p className='text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed'>
                                    Start sending requests through your gateway to see logs appear here in real-time.
                                </p>
                            </div>
                        ) : pageLogs.length === 0 ? (
                            isLiveMode ? (
                                !isLoading ? (
                                    <div className='flex items-center justify-center h-full space-x-2'>
                                        <Loader2 className='w-4 h-4 animate-spin text-white/70 light:text-black/70' />
                                        <span className='text-white/70 light:text-black/70'>Listening for logs...</span>
                                    </div>
                                ) : (
                                    <div className='flex items-center justify-center h-full space-x-2'>
                                        <Loader2 className='w-4 h-4 animate-spin text-white/70 light:text-black/70' />
                                        <span className='text-white/70 light:text-black/70'>Loading logs...</span>
                                    </div>
                                )
                            ) : (
                                isLoading ? (
                                    <div className='flex items-center justify-center h-full space-x-2'>
                                        <Loader2 className='w-4 h-4 animate-spin text-white/70 light:text-black/70' />
                                        <span className='text-white/70 light:text-black/70'>Loading logs...</span>
                                    </div>
                                ) : (
                                    <div className='flex items-center justify-center h-full space-x-2'>
                                        <span className='text-white/70 light:text-black/70'>No logs found for this time range.</span>
                                    </div>
                                )
                            )
                        ) : null}
                    </div>}
                minTableWidth='900px'
                onScrollNearEnd={fetchNextPage}
                isLoadingMore={isFetchingMore}
            />
            <LogDetailSheet
                log={currentLog}
                open={isExpanded}
                onClose={handleSheetClose}
                onPrevious={handlePrevious}
                onNext={handleNext}
                canGoPrevious={canGoPrevious}
                canGoNext={canGoNext}
            />
        </div>
    )
}
