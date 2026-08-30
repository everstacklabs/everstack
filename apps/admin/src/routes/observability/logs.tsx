import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useRef, useEffect } from 'react'
import { streamLogs } from '@/server/logs'
import type { RequestLog } from '@everstack/proto/everstack/logs/v1/logs_pb'
import { LogsConsole } from '@/components/logs/logs-console'
import { LogsControls } from '@/components/logs/logs-controls'
import { LogColumnManager } from '@/components/logs/log-column-manager'
import { SavedViews } from '@/components/traces/saved-views'
import { TIME_RANGE_LABELS, calculateTimeRange, shouldBeLiveMode } from '@/lib/time-ranges'
import { z } from 'zod'
import type { TimeRangePreset } from '@/stores/logs-store'
import { useStreamingQuery } from '@/hooks/use-streaming-query'
import { useInstanceHasData } from '@/hooks/use-instance-has-data'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

// URL search params schema
const logsSearchSchema = z.object({
    live: z.string().optional().default('true'),
    range: z.enum(Object.keys(TIME_RANGE_LABELS) as [TimeRangePreset, ...TimeRangePreset[]]).optional().default('15m'),
    from: z.string().optional(),
    to: z.string().optional(),
    query: z.string().optional(),
    log: z.string().optional(), // Selected log correlation ID
})

export const Route = createFileRoute('/observability/logs')({
    component: Logs,
    validateSearch: logsSearchSchema,
})

function Logs() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()

    const isLiveMode = search.live === 'true'
    const timeRange = (search.range || '15m') as TimeRangePreset

    const customRange = useMemo(() => {
        if (search.from && search.to) {
            return {
                start: new Date(search.from),
                end: new Date(search.to)
            }
        }
        return null
    }, [search.from, search.to])

    // Auto-switch between live and paused mode based on time range
    // Only auto-switch when time range changes, not when user manually toggles
    const prevTimeRangeRef = useRef(timeRange)
    const prevCustomRangeRef = useRef(customRange)

    useEffect(() => {
        // Check if time range actually changed (not just mode toggle)
        const timeRangeChanged = prevTimeRangeRef.current !== timeRange
        const customRangeChanged = JSON.stringify(prevCustomRangeRef.current) !== JSON.stringify(customRange)

        if (timeRangeChanged || customRangeChanged) {
            const shouldBeLive = shouldBeLiveMode(timeRange, customRange)

            // Only navigate if there's a mismatch
            if (isLiveMode !== shouldBeLive) {
                navigate({
                    search: (prev) => ({
                        ...prev,
                        live: shouldBeLive ? 'true' : 'false'
                    }),
                    replace: true, // Use replace to avoid cluttering history
                })
            }

            // Update refs
            prevTimeRangeRef.current = timeRange
            prevCustomRangeRef.current = customRange
        }
    }, [timeRange, customRange, isLiveMode, navigate])

    // Calculate time range - for presets, round to nearest minute to ensure cache stability
    const { stableFrom, stableTo } = useMemo(() => {
        if (customRange) {
            // Custom range: use exact timestamps from URL
            return {
                stableFrom: customRange.start.toISOString(),
                stableTo: customRange.end.toISOString()
            }
        }

        // Preset range: round to nearest minute for cache stability across route switches
        const now = new Date()
        now.setSeconds(0, 0) // Round to minute
        const result = calculateTimeRange(timeRange, null)

        return {
            stableFrom: result.from,
            stableTo: now.toISOString() // Use rounded 'now' for consistent cache key
        }
    }, [timeRange, customRange])

    // Check if instance has any historical data (for empty state messaging)
    const { hasInstanceData } = useInstanceHasData('logs')

    // Use streaming query hook
    const { data: logs = [], isLoading, isFetching, refetch, fetchNextPage, isFetchingMore } = useStreamingQuery<RequestLog>({
        queryKeyPrefix: 'logs',
        from: stableFrom,
        to: stableTo,
        isLiveMode,
        enableInfiniteScroll: true,
        pageSize: 500,
        streamFn: async function* (signal, offset = 0, limit = 500) {
            // Capture current values to avoid closure issues
            const currentFrom = stableFrom
            const currentTo = stableTo

            for await (const log of streamLogs({
                pageSize: limit,
                offset: offset,
                from: currentFrom,
                to: isLiveMode ? undefined : currentTo, // Don't send 'to' in live mode to enable tail loop
            }, { signal })) {
                if (log) {
                    yield log
                }
            }
        },
        getItemId: (log) => log.correlationId,
        getItemTimestamp: (log) => log.timestamp?.seconds ? (typeof log.timestamp.seconds === 'bigint' ? safeBigIntToNumber(log.timestamp.seconds) : Number(log.timestamp.seconds)) * 1000 : 0,
        queryOptions: {
            staleTime: isLiveMode ? 0 : 30000, // In live mode, always refetch; in historical mode, cache for 30s
        }
    })

    // Track newly arrived ids to highlight for a short time
    const initializedRef = useRef(false)
    const seenRef = useRef<Set<string>>(new Set())
    const addedAtRef = useRef<Map<string, number>>(new Map())
    const now = Date.now()

    // Reset tracking refs when time range or mode changes (prevents stale data)
    useEffect(() => {
        initializedRef.current = false
        seenRef.current.clear()
        addedAtRef.current.clear()
    }, [stableFrom, stableTo, isLiveMode])

    if (logs.length > 0) {
        if (!initializedRef.current) {
            for (const log of logs) {
                if (log.correlationId) seenRef.current.add(log.correlationId)
            }
            initializedRef.current = true
        } else {
            for (const log of logs) {
                if (log.correlationId && !seenRef.current.has(log.correlationId)) {
                    seenRef.current.add(log.correlationId)
                    addedAtRef.current.set(log.correlationId, now)
                }
            }
        }
    }

    const sortedLogs = useMemo(() => {
        const fromTime = new Date(stableFrom).getTime()
        const toTime = new Date(stableTo).getTime()

        return [...logs]
            .filter(log => {
                if (!log.timestamp?.seconds) return false
                const logTime = (typeof log.timestamp.seconds === 'bigint' ? safeBigIntToNumber(log.timestamp.seconds) : Number(log.timestamp.seconds)) * 1000
                if (isLiveMode) {
                    return logTime >= fromTime
                } else {
                    return logTime >= fromTime && logTime <= toTime
                }
            })
            .sort((a, b) => {
                const aTime = a.timestamp?.seconds ? (typeof a.timestamp.seconds === 'bigint' ? safeBigIntToNumber(a.timestamp.seconds) : Number(a.timestamp.seconds)) * 1000 : 0
                const bTime = b.timestamp?.seconds ? (typeof b.timestamp.seconds === 'bigint' ? safeBigIntToNumber(b.timestamp.seconds) : Number(b.timestamp.seconds)) * 1000 : 0
                return bTime - aTime
            })
    }, [logs, stableFrom, stableTo, isLiveMode])

    // Only show loading if we're fetching AND don't have any data (prevents flash with cached data)
    const showLoading = !isLiveMode && (isLoading || isFetching) && logs.length === 0

    return (
        <div data-density="compact" className="flex flex-col h-screen w-full overflow-hidden">
            <LogsControls isLoading={showLoading} onRefresh={() => {
                refetch({ cancelRefetch: false })
            }} />
            <div className="flex items-center justify-end gap-2 px-2 py-1.5 -mt-0.5 border-y border-brand-main-500 bg-brand-main-950">
                <LogColumnManager />
                <SavedViews
                    path="/observability/logs"
                    search={search as Record<string, unknown>}
                    onApply={(s) => navigate({ search: () => s as any, replace: true })}
                />
            </div>
            <div className="flex-1 min-h-0 overflow-hidden flex flex-col">
                <LogsConsole
                    pageLogs={sortedLogs}
                    isLiveMode={isLiveMode}
                    isLoading={showLoading}
                    selectedLogId={search.log}
                    hasInstanceData={hasInstanceData}
                    fetchNextPage={fetchNextPage}
                    isFetchingMore={isFetchingMore}
                />
            </div>
        </div>
    )
}
