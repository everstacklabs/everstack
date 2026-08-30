import { createFileRoute } from '@tanstack/react-router'
import { useMemo, useEffect, useRef, useState } from 'react'
import { streamTraces } from '@/server/traces'
import type { Trace } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { TracesControls } from '@/components/traces/traces-controls'
import { SavedViews } from '@/components/traces/saved-views'
import { TracesTable } from '@/components/traces/traces-table'
import { TracesChart } from '@/components/traces/traces-chart'
import {
  TIME_RANGE_LABELS,
  calculateTimeRange,
  shouldBeLiveMode,
} from '@/lib/time-ranges'
import { ui } from '@everstack/ui'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

const {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
  TooltipProvider,
} = ui
import { z } from 'zod'
import type { TimeRangePreset } from '@/stores/logs-store'
import { timestampFromDate, create } from '@everstack/client'
import { ListTracesRequestSchema } from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import { compileToClauses, parseEsql } from '@/utils/esql'
import { useStreamingQuery } from '@/hooks/use-streaming-query'
import { useInstanceHasData } from '@/hooks/use-instance-has-data'
import { cn } from '@everstack/utils/functions/cn'
import { TraceToolbar } from '@/components/traces/trace-filter-bar'

// URL search params schema
const tracesSearchSchema = z.object({
  live: z.string().optional().default('true'),
  range: z
    .enum(
      Object.keys(TIME_RANGE_LABELS) as [TimeRangePreset, ...TimeRangePreset[]],
    )
    .optional()
    .default('15m'),
  from: z.string().optional(),
  to: z.string().optional(),
  queries: z.string().optional(), // JSON-encoded EVS query chips for the search bar
  q: z.string().optional(), // canonical ESQL string (source for Tier-2 span clauses)
  query: z.string().optional(),
  trace: z.string().optional(), // Selected trace ID
  span: z.string().optional(), // Selected span ID
  correlationId: z.string().optional(), // Filter by correlation_id
  panelCollapsed: z.string().optional(), // Left panel collapsed state
  treeExpanded: z.string().optional(), // Tree fully expanded state
  timeline: z.string().optional(), // Show classic timeline (waterfall-style) view
  graph: z.string().optional(), // Show agent trajectory graph view
  logs: z.string().optional(), // Show correlated OTLP logs for the trace
  // Multi-dimension filters (P0.3)
  statusCode: z.string().optional(), // OK, ERROR
  model: z.string().optional(),
  provider: z.string().optional(),
  userId: z.string().optional(),
  sessionId: z.string().optional(),
  threadId: z.string().optional(),
  environment: z.string().optional(),
  tags: z.string().optional(), // Comma-separated tags
  metadata: z.string().optional(), // Comma-separated "key=value" predicates
  minCost: z.string().optional(),
  maxCost: z.string().optional(),
  minDuration: z.string().optional(), // in ms
  maxDuration: z.string().optional(), // in ms
  showOperational: z.string().optional(), // 'true' to include operational/noise traces
})

export const Route = createFileRoute('/observability/traces')({
  component: Traces,
  validateSearch: tracesSearchSchema,
})

/** Tier-2 span clauses parsed from the canonical ESQL `?q=` param. */
function esqlSpanClauses(
  q: string | undefined,
): { scope: string; field: string; op: string; value: string }[] {
  if (!q) return []
  const parsed = parseEsql(q)
  if (!parsed.ok) return []
  return compileToClauses(parsed.query).clauses.map((c) => ({
    scope: c.scope,
    field: c.field,
    op: c.op,
    value: c.value,
  }))
}

function Traces() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()

  const isLiveMode = search.live === 'true'
  const timeRange = (search.range ||
    (search.from && search.to ? 'custom' : '15m')) as TimeRangePreset

  // Calculate custom range
  const customRange = useMemo(() => {
    if (search.from && search.to) {
      return {
        start: new Date(search.from),
        end: new Date(search.to),
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
    const customRangeChanged =
      JSON.stringify(prevCustomRangeRef.current) !== JSON.stringify(customRange)

    if (timeRangeChanged || customRangeChanged) {
      const shouldBeLive = shouldBeLiveMode(timeRange, customRange)

      // Only navigate if there's a mismatch
      if (isLiveMode !== shouldBeLive) {
        navigate({
          search: (prev) => ({
            ...prev,
            live: shouldBeLive ? 'true' : 'false',
          }),
          replace: true, // Use replace to avoid cluttering history
        })
      }

      // Update refs
      prevTimeRangeRef.current = timeRange
      prevCustomRangeRef.current = customRange
    }
  }, [timeRange, customRange, isLiveMode, navigate])

  // Real-time current time state for live mode (only for client-side filtering/display)
  const [currentTime, setCurrentTime] = useState(() => new Date())

  // Calculate stable time range for query key - for presets, round to nearest minute to ensure cache stability
  // This should NOT change in live mode to prevent query key changes and cancellations
  const { stableFrom, stableTo } = useMemo(() => {
    if (customRange) {
      // Custom range: use exact timestamps from URL
      return {
        stableFrom: customRange.start.toISOString(),
        stableTo: customRange.end.toISOString(),
      }
    }

    // Preset range: round to nearest minute for cache stability across route switches
    const now = new Date()
    now.setSeconds(0, 0) // Round to minute
    const result = calculateTimeRange(timeRange, null)

    return {
      stableFrom: result.from,
      stableTo: now.toISOString(), // Use rounded 'now' for consistent cache key
    }
  }, [timeRange, customRange])

  // Calculate dynamic time range for client-side filtering (uses currentTime in live mode)
  const { from, to } = useMemo(() => {
    if (customRange) {
      // Custom range: use exact timestamps from URL, or current time if live mode
      if (isLiveMode) {
        return {
          from: customRange.start.toISOString(),
          to: currentTime.toISOString(),
        }
      }
      return {
        from: customRange.start.toISOString(),
        to: customRange.end.toISOString(),
      }
    }

    // Preset range: use current time if live mode, otherwise calculate
    const now = isLiveMode ? currentTime : new Date()
    now.setSeconds(0, 0) // Round to minute
    const result = calculateTimeRange(timeRange, null)

    return {
      from: result.from,
      to: now.toISOString(),
    }
  }, [timeRange, customRange, isLiveMode, currentTime])

  // Check if instance has any historical data (for empty state messaging)
  const { hasInstanceData } = useInstanceHasData('traces')

  // Use streaming query hook
  // Use stableFrom/stableTo for query key to prevent cancellations in live mode
  // Memoize filter params for stable query key
  const filterParams = useMemo(
    () => ({
      statusCode: search.statusCode,
      model: search.model,
      provider: search.provider,
      userId: search.userId,
      sessionId: search.sessionId,
      threadId: search.threadId,
      query: search.query,
      environment: search.environment,
      tags: search.tags,
      metadata: search.metadata,
      minCost: search.minCost,
      maxCost: search.maxCost,
      minDuration: search.minDuration,
      maxDuration: search.maxDuration,
      showOperational: search.showOperational,
      q: search.q,
    }),
    [
      search.q,
      search.statusCode,
      search.model,
      search.provider,
      search.userId,
      search.sessionId,
      search.threadId,
      search.query,
      search.environment,
      search.tags,
      search.metadata,
      search.minCost,
      search.maxCost,
      search.minDuration,
      search.maxDuration,
      search.showOperational,
    ],
  )

  const {
    data: traces = [],
    isLoading,
    isFetching,
    error,
    refetch,
    fetchNextPage,
    hasMore,
    isFetchingMore,
  } = useStreamingQuery<Trace>({
    queryKeyPrefix: 'traces',
    from: stableFrom,
    to: stableTo,
    isLiveMode,
    enableInfiniteScroll: true,
    pageSize: 100,
    queryKeyExtra: filterParams,
    streamFn: async function* (signal, offset = 0, limit = 100) {
      // Always respect the specified time range
      // Don't pass correlationId to backend - we'll filter client-side to show all traces in range
      // For direct trace links, the TracesTable component uses getTrace() fallback
      // Capture current values to avoid closure issues
      const currentFrom = stableFrom
      const currentTo = stableTo

      const fromTime = timestampFromDate(new Date(currentFrom))
      const toTimestamp = isLiveMode
        ? undefined
        : timestampFromDate(new Date(currentTo))

      const request = create(ListTracesRequestSchema, {
        from: fromTime,
        to: toTimestamp,
        limit,
        offset,
        tenantId: '',
        model: search.model || '',
        provider: search.provider || '',
        statusCode: search.statusCode || '',
        correlationId: '', // Don't filter by correlationId - fetch all traces in range
        userId: search.userId || '',
        sessionId: search.sessionId || '',
        threadId: search.threadId || '',
        query: search.query || '',
        environment: search.environment || '',
        tags: search.tags
          ? search.tags
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean)
          : [],
        metadata: search.metadata
          ? search.metadata
            .split(',')
            .map((m) => m.trim())
            .filter(Boolean)
          : [],
        minCost: search.minCost ? parseFloat(search.minCost) : undefined,
        maxCost: search.maxCost ? parseFloat(search.maxCost) : undefined,
        minDurationNs: search.minDuration
          ? BigInt(Math.round(parseFloat(search.minDuration) * 1_000_000))
          : undefined,
        maxDurationNs: search.maxDuration
          ? BigInt(Math.round(parseFloat(search.maxDuration) * 1_000_000))
          : undefined,
        includeOperational: search.showOperational === 'true',
        filters: esqlSpanClauses(search.q),
      })

      for await (const trace of streamTraces(request, { signal })) {
        if (trace) {
          yield trace
        }
      }
    },
    getItemId: (trace) => trace.traceId,
    getItemTimestamp: (trace) => {
      if (!trace.startTime) return 0
      // Convert protobuf timestamp to milliseconds
      const seconds =
        typeof trace.startTime.seconds === 'bigint'
          ? safeBigIntToNumber(trace.startTime.seconds)
          : Number(trace.startTime.seconds || 0)
      const nanos =
        typeof trace.startTime.nanos === 'bigint'
          ? safeBigIntToNumber(trace.startTime.nanos)
          : Number(trace.startTime.nanos || 0)
      return seconds * 1000 + Math.floor(nanos / 1000000)
    },
    queryOptions: {
      staleTime: isLiveMode ? 0 : 30000,
    },
  })

  // Update current time only when new traces arrive in live mode
  useEffect(() => {
    if (!isLiveMode || traces.length === 0) return

    // Update to current time only once per new trace batch
    setCurrentTime(new Date())
  }, [traces.length, isLiveMode])

  // Client-side sorting + time filtering
  const sortedTraces = useMemo(() => {
    const fromTime = new Date(from).getTime()
    const toTime = new Date(to).getTime()

    // Helper to convert Timestamp to milliseconds
    const timestampToMs = (ts: any): number => {
      if (!ts) return 0
      if (typeof ts.toDate === 'function') return ts.toDate().getTime()
      if (ts.seconds !== undefined) {
        // Protobuf timestamp with seconds and nanos
        const seconds =
          typeof ts.seconds === 'bigint'
            ? safeBigIntToNumber(ts.seconds)
            : Number(ts.seconds || 0)
        const nanos =
          typeof ts.nanos === 'bigint'
            ? safeBigIntToNumber(ts.nanos)
            : Number(ts.nanos || 0)
        return seconds * 1000 + Math.floor(nanos / 1000000)
      }
      return new Date(ts).getTime()
    }

    return [...traces]
      .filter((trace) => {
        if (!trace.startTime) return false
        const traceTime = timestampToMs(trace.startTime)

        // In live mode, only filter by 'from' to allow new traces to appear
        // In paused mode, filter by both 'from' and 'to' to prevent stale data
        if (isLiveMode) {
          return traceTime >= fromTime
        } else {
          return traceTime >= fromTime && traceTime <= toTime
        }
      })
      .sort((a, b) => {
        const aTime = timestampToMs(a.startTime)
        const bTime = timestampToMs(b.startTime)
        return bTime - aTime
      })
  }, [traces, from, to, isLiveMode])

  // Auto-select matching trace when correlationId is provided
  // We need to fetch the trace separately since we don't filter by correlationId in the main query
  useEffect(() => {
    if (search.correlationId && !search.trace && !isLoading) {
      // Make a separate request to find the trace by correlationId
      const findTraceByCorrelationId = async () => {
        try {
          const fromTime = timestampFromDate(new Date(stableFrom))
          const toTime = timestampFromDate(new Date(stableTo))
          const request = create(ListTracesRequestSchema, {
            from: fromTime,
            to: toTime,
            limit: 1,
            offset: 0,
            tenantId: '',
            model: '',
            provider: '',
            statusCode: '',
            correlationId: search.correlationId || '',
            includeOperational: true, // direct lookup: never hide the target
          })

          // Get just the first trace that matches
          for await (const trace of streamTraces(request)) {
            if (trace?.traceId) {
              navigate({
                search: (prev) => ({
                  ...prev,
                  trace: trace.traceId,
                  correlationId: undefined, // Clear correlationId to prevent re-fetching
                }),
                replace: true,
              })
              break
            }
          }
        } catch (error) {
          console.error('Error finding trace by correlationId:', error)
        }
      }

      findTraceByCorrelationId()
    }
  }, [
    search.correlationId,
    search.trace,
    isLoading,
    navigate,
    stableFrom,
    stableTo,
  ])

  // Only show loading if we're fetching AND don't have any data (prevents flash with cached data)
  // Also ensure we don't show loading if we have cached data or if we're in live mode
  const showLoading =
    !isLiveMode && (isLoading || isFetching) && traces.length === 0

  const [activeChart, setActiveChart] = useState<'total' | 'success' | 'error'>(
    'total',
  )

  const totals = useMemo(() => {
    // Determine error status based on root span status only
    // errorCount includes non-critical errors (like cache misses) which shouldn't mark the trace as failed
    return {
      total: sortedTraces.length,
      success: sortedTraces.filter((t) => t.status?.toUpperCase() !== 'ERROR')
        .length,
      error: sortedTraces.filter((t) => t.status?.toUpperCase() === 'ERROR')
        .length,
    }
  }, [sortedTraces])

  const chartConfig = {
    total: { label: 'Total' },
    success: { label: 'Success' },
    error: { label: 'Error' },
  }

  return (
    <TooltipProvider>
      <div data-density="compact" className="flex flex-col h-full w-full ">
        <div className="flex items-center justify-between bg-brand-main-950 border-b border-brand-main-600">
          <TracesControls
            isLoading={showLoading}
            onRefresh={() => {
              refetch({ cancelRefetch: false })
            }}
          />
          <SavedViews
            path="/observability/traces"
            search={search as Record<string, unknown>}
            onApply={(s) => navigate({ search: () => s as any, replace: true })}
            className="px-2"
          />
        </div>
        <TraceToolbar />
        {/* Traces chart accordion */}
        <div className="flex-1 min-h-0 h-full  overflow-hidden flex flex-col">
          <TracesTable
            pageTraces={sortedTraces}
            isLiveMode={isLiveMode}
            isLoading={showLoading}
            selectedTraceId={search.trace}
            hasInstanceData={hasInstanceData}
            fetchNextPage={fetchNextPage}
            hasMore={hasMore}
            isFetchingMore={isFetchingMore}
            error={error}
          />
          <Accordion
            type="single"
            collapsible
            className="focus-within:outline-none focus-visible:ring-0"
          >
            <AccordionItem
              value="chart"
              className="border-y border-brand-main-600  bg-brand-main-950"
            >
              <AccordionTrigger
                direction="up"
                className="px-4 py-2 hover:no-underline focus-within:outline-none focus-visible:ring-0"
              >
                <div className="flex items-center justify-between w-full pr-2">
                  <span className="text-sm font-semibold text-white">
                    Trace Activity
                  </span>
                  <div className="flex items-center gap-0">
                    {(['total', 'success', 'error'] as const).map((key) => {
                      const chart = key as keyof typeof chartConfig
                      return (
                        <div
                          key={chart}
                          className={cn(
                            "flex items-center gap-2 after:content-['|'] after:w-1 after:mr-2 after:opacity-50 last:after:hidden after:ml-1",
                            activeChart === chart
                              ? 'text-brand-secondary-500 after:text-white/50 after:opacity-50'
                              : 'text-white/50 after:opacity-50',
                          )}
                          onClick={(e) => {
                            e.stopPropagation()
                            setActiveChart(chart)
                          }}
                        >
                          <span className="text-xs ">
                            {chartConfig[chart].label}
                          </span>
                          <span className="text-xs leading-none font-semibold">
                            {totals[chart].toLocaleString()}
                          </span>
                        </div>
                      )
                    })}
                  </div>
                </div>
              </AccordionTrigger>
              <AccordionContent className="px-0 py-0">
                <TracesChart
                  traces={sortedTraces}
                  from={from}
                  to={to}
                  activeChart={activeChart}
                  onChartChange={setActiveChart}
                  totals={totals}
                  onZoom={(zoomFrom, zoomTo) => {
                    navigate({
                      search: (prev) => ({
                        ...prev,
                        from: zoomFrom,
                        to: zoomTo,
                        live: 'false',
                        range: 'custom',
                      }),
                      replace: true,
                    })
                  }}
                  isLiveMode={isLiveMode}
                />
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        </div>
      </div>
    </TooltipProvider>
  )
}
