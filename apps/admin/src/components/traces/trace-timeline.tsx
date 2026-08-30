import { useQuery } from '@tanstack/react-query'
import { getTraceByID } from '@/server/traces'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { ui } from '@everstack/ui'
import { Loader2, ChevronDown, ChevronRight, Maximize2 } from 'lucide-react'
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { cn } from '@everstack/utils/functions/cn'
import { Route } from '@/routes/observability/traces'
import { formatDuration } from '@/utils/trace-formatters'
import { getSpanDisplayConfig } from '@/utils/span-title-name-map'
import { categoryTimelineColors, categoryLabels } from '@/utils/span-display-helpers'
import { timelineStatusColors } from './trace-viz'

const { Tooltip, TooltipProvider, Button } = ui

interface TraceTimelineProps {
    traceId: string
    onSpanHover?: (spanId: string | undefined) => void
    selectedSpanId?: string
}

// Muted status colours (green = ok, rose = error, slate = unset) come from the
// shared trace-viz palette so no raw hex lives in the component.
function statusColorSet(statusCode: string): { fill: string; border: string } {
    return (
        timelineStatusColors[statusCode as keyof typeof timelineStatusColors] ??
        timelineStatusColors.UNSET
    )
}

function getStatusColor(statusCode: string): string {
    return statusColorSet(statusCode).fill
}

function getStatusBorderColor(statusCode: string): string {
    return statusColorSet(statusCode).border
}

// Build hierarchical structure from flat span list
function buildSpanHierarchy(spans: Span[]): Map<string, Span[]> {
    const hierarchy = new Map<string, Span[]>()

    spans.forEach(span => {
        const parentId = span.parentSpanId || 'root'
        if (!hierarchy.has(parentId)) {
            hierarchy.set(parentId, [])
        }
        hierarchy.get(parentId)!.push(span)
    })

    return hierarchy
}

// Calculate span row index for Y positioning (each span gets its own row)
function calculateSpanRows(
    span: Span,
    hierarchy: Map<string, Span[]>,
    rows: Map<string, number>,
    currentRow: { value: number }
): void {
    rows.set(span.spanId, currentRow.value)
    currentRow.value++

    const children = hierarchy.get(span.spanId) || []
    children.forEach(child => {
        calculateSpanRows(child, hierarchy, rows, currentRow)
    })
}

interface TimelineSpanBarProps {
    span: Span
    startTime: bigint
    endTime: bigint
    rowIndex: number
    hierarchyDepth: number
    hasChildren: boolean
    isLast: boolean
    isExpanded: boolean
    onToggleExpand: () => void
    onClick?: () => void
    onSpanHover?: (spanId: string | undefined) => void
    selectedSpanId?: string
    zoomRange: { start: number; end: number }
}

function TimelineSpanBar({ span, startTime, endTime, rowIndex, hierarchyDepth, hasChildren, isLast, isExpanded, onToggleExpand, onClick, onSpanHover, selectedSpanId, zoomRange }: TimelineSpanBarProps) {
    const [isHovered, setIsHovered] = useState(false)
    const isSelected = selectedSpanId === span.spanId
    const navigate = Route.useNavigate()
    const chartMode = ui.useChartMode()

    // Get display configuration for this span
    const displayConfig = useMemo(() => getSpanDisplayConfig(span), [span])
    const timelineColors = categoryTimelineColors[displayConfig.category]

    // Calculate position and width as percentages
    const totalDuration = Number(endTime - startTime)
    const spanStart = span.timestamp ? (
        (typeof span.timestamp.seconds === 'bigint' ? span.timestamp.seconds : BigInt(span.timestamp.seconds || 0)) * BigInt(1_000_000_000) +
        (typeof span.timestamp.nanos === 'bigint' ? span.timestamp.nanos : BigInt(span.timestamp.nanos || 0))
    ) : BigInt(0)
    const relativeStart = Number(spanStart - startTime) / totalDuration
    const spanDuration = typeof span.duration === 'bigint' ? span.duration : BigInt(span.duration || 0)
    const spanWidth = Number(spanDuration) / totalDuration

    // Apply zoom range to convert to zoomed coordinates
    const zoomStart = zoomRange.start / 100
    const zoomEnd = zoomRange.end / 100
    const zoomWidth = zoomEnd - zoomStart

    // Convert span position to zoomed view
    const zoomedLeft = (relativeStart - zoomStart) / zoomWidth
    const zoomedWidth = spanWidth / zoomWidth

    // Clamp to 0-100% to prevent overflow
    const leftPercent = Math.max(0, Math.min(100, zoomedLeft * 100))
    const widthPercent = Math.max(0, Math.min(100 - leftPercent, zoomedWidth * 100))

    // Check if span is visible in the current zoom range
    const spanEnd = relativeStart + spanWidth
    const isVisible = spanEnd > zoomStart && relativeStart < zoomEnd

    // If not visible, don't render
    if (!isVisible) return null

    // Calculate Y position based on row index (each span gets its own row)
    const rowHeight = 36
    const headerHeight = 32 // Height of duration header bar (8 * 4 = 32px)
    const topPosition = rowIndex * rowHeight + headerHeight

    // Use category-based colors for timeline bars, but override with status for errors
    const isError = span.statusCode === 'ERROR'
    const color = isError ? getStatusColor(span.statusCode) : timelineColors.fill
    const borderColor = isError ? getStatusBorderColor(span.statusCode) : timelineColors.border

    // Extract enhanced observability data
    const nodeName = span.spanAttributes?.['span.node']
    const observationType = span.spanAttributes?.['observation.type']

    return (
        <>
            {/* Row background with hover effect */}
            <div
                className={cn(
                    "absolute left-0 right-0 border-b border-white/5 light:border-black/5 transition-all cursor-pointer",
                    "hover:bg-white/5 light:hover:bg-black/5",
                    rowIndex === 0 && "border-t border-white/10 light:border-black/10",
                    isSelected && "bg-brand-secondary-600/30 border-brand-secondary-500/30",
                    isHovered && !isSelected && "bg-white/[0.03] light:bg-black/[0.03]"
                )}
                style={{
                    top: `${topPosition}px`,
                    height: `${rowHeight}px`,
                }}
                onMouseEnter={() => {
                    setIsHovered(true)
                    onSpanHover?.(span.spanId)
                }}
                onMouseLeave={() => {
                    setIsHovered(false)
                    onSpanHover?.(undefined)
                }}
                onClick={onClick}
            />

            {/* Span label on the left with tree structure */}
            <div
                className="absolute left-0 z-10 pointer-events-auto"
                style={{
                    top: `${topPosition}px`,
                    height: `${rowHeight}px`,
                    width:'280px',
                }}
            >
                {/* Tree hierarchy lines */}
                {hierarchyDepth > 0 && (
                    <>
                        {/* Vertical line from parent */}
                        <div
                            className="absolute bottom-0 border-l border-brand-main-400"
                            style={{
                                left: `${(hierarchyDepth - 1) * 24 + 14}px`,
                                top: 0
                            }}
                        />

                        {/* Horizontal line to node */}
                        <div
                            className={"absolute border-t border-brand-main-400"}
                            style={{
                                left: `${(hierarchyDepth - 1) * 24 + 14}px`,
                                top: `${rowHeight / 2}px`,
                                width: '16px',
                            }}
                        />

                        {/* Hide vertical line below if this is the last child */}
                        {isLast && (
                            <div
                                className={cn("absolute bg-brand-main-700", isSelected && "bg-brand-main-700/5")}
                                style={{
                                    left: `${(hierarchyDepth - 1) * 24 + 14}px`,
                                    top: `${rowHeight / 2 + 1}px`,
                                    bottom: 0,
                                    width: "1px",
                                }}
                            />
                        )}
                    </>
                )}

                {/* Clickable span row */}
                <div
                    className="flex items-center gap-2 text-xs px-3 h-full cursor-pointer"
                    style={{
                        paddingLeft: `${hierarchyDepth * 24 + 6}px`
                    }}
                    onClick={() => {
                        navigate({
                            search: (prev) => ({
                                ...prev,
                                span: span.spanId || undefined,
                            })
                        })
                    }}
                >
                    {/* Expand/collapse chevron */}
                    {hasChildren ? (
                        <div
                            className="flex-shrink-0 flex items-center justify-center w-4 h-4 rounded bg-brand-secondary-600/30 hover:bg-brand-secondary-600/50 transition"
                            onClick={(e) => {
                                e.stopPropagation()
                                onToggleExpand()
                            }}
                        >
                            {isExpanded ? (
                                <ChevronDown className="w-3 h-3 text-brand-main-50 light:text-black" />
                            ) : (
                                <ChevronRight className="w-3 h-3 text-brand-main-50 light:text-black" />
                            )}
                        </div>
                    ) : (
                        <div className="w-4" /> // Spacer for alignment
                    )}

                    {/* Category indicator */}
                    <span
                        className="inline-flex items-center justify-center w-1.5 h-1.5 rounded-full flex-shrink-0"
                        style={{ backgroundColor: timelineColors.fill }}
                    />

                    {/* Span name (formatted) */}
                    <span className="truncate text-brand-main-50 light:text-black font-medium">{displayConfig.title}</span>
                    {displayConfig.subtitle && (
                        <span className="truncate text-brand-main-50 light:text-black text-[10px]">{displayConfig.subtitle}</span>
                    )}
                </div>
            </div>

            {/* Span bar container - positioned in timeline column */}
            <div
                className="absolute z-20 pointer-events-none"
                style={{
                    left:'280px', // 280px + 12px padding
                    right: '100px', // 100px + 12px padding
                    top: `${topPosition}px`,
                    height: `${rowHeight}px`,
                }}
            >
                <TooltipProvider>
                    <Tooltip content={
                        <div className="p-2 space-y-1">
                            <div className="font-semibold text-sm">{displayConfig.title}</div>
                            {displayConfig.subtitle && (
                                <div className="text-xs text-brand-main-50 light:text-black">{displayConfig.subtitle}</div>
                            )}
                            <div className="flex items-center gap-1 text-xs">
                                <span
                                    className="inline-flex items-center justify-center w-2 h-2 rounded-full"
                                    style={{ backgroundColor: timelineColors.fill }}
                                />
                                {/* The pale border tone reads well on the dark tooltip; on light, use the deeper fill tone. */}
                                <span style={{ color: chartMode ==='light' ? timelineColors.fill : timelineColors.border }}>{categoryLabels[displayConfig.category]}</span>
                            </div>
                            {nodeName && (
                                <div className="text-xs text-brand-main-50 light:text-black">Node: {nodeName}</div>
                            )}
                            {observationType && (
                                <div className="text-xs text-brand-main-50 light:text-black">Type: {observationType}</div>
                            )}
                            <div className="text-xs text-brand-main-50 light:text-black">Duration: {formatDuration(span.duration)}</div>
                            <div className="text-xs text-brand-main-50 light:text-black">Status: {span.statusCode}</div>
                            <div className="text-xs text-brand-main-50 light:text-black">Kind: {span.spanKind}</div>
                        </div>
                    }>
                        <div
                            className={cn(
                                "absolute rounded transition-all text-brand-secondary-500 cursor-pointer pointer-events-auto",
                                isSelected && "ring-2 ring-brand-secondary-500 ring-offset-1 ring-offset-brand-main-700",
                                isHovered && !isSelected && "ring-1 ring-white/30 light:ring-black/30"
                            )}
                            style={{
                                left: `${leftPercent}%`,
                                width: `${Math.max(widthPercent, 0.5)}%`,
                                top:'6px',
                                height: '24px',
                                backgroundColor: color,
                                border: isSelected ? `1px solid ${borderColor}` : '1px solid transparent',
                                boxShadow: isSelected ? `0 0 12px ${color}40` : isHovered ? `0 0 8px ${color}30` : 'none',
                            }}
                            onMouseEnter={() => {
                                setIsHovered(true)
                                onSpanHover?.(span.spanId)
                            }}
                            onMouseLeave={() => {
                                setIsHovered(false)
                                onSpanHover?.(undefined)
                            }}
                            onClick={onClick}
                        >
                            {/* Duration label inside bar */}
                            {widthPercent > 5 && (
                                <div className="px-2 text-[10px] text-brand-secondary-700 font-semibold truncate leading-6 flex items-center h-full">
                                    {formatDuration(span.duration)}
                                </div>
                            )}
                        </div>
                    </Tooltip>
                </TooltipProvider>
            </div>

            {/* Duration on the right */}
            <div
                className="absolute right-0 flex items-center justify-end px-3 pointer-events-none z-10"
                style={{
                    top: `${topPosition}px`,
                    height: `${rowHeight}px`,
                    width: '100px',
                }}
            >
                <span className="text-[11px] text-brand-main-50 light:text-black ">
                    {formatDuration(span.duration)}
                </span>
            </div>
        </>
    )
}

export function TraceTimeline({ traceId, onSpanHover, selectedSpanId }: TraceTimelineProps) {
    const [expandedMap, setExpandedMap] = useState<Map<string, boolean>>(new Map())
    const [zoomRange, setZoomRange] = useState({ start: 0, end: 100 })
    const [isDragging, setIsDragging] = useState(false)
    const [dragStart, setDragStart] = useState<number | null>(null)
    const [dragEnd, setDragEnd] = useState<number | null>(null)
    const timelineRef = useRef<HTMLDivElement>(null)
    const chartMode = ui.useChartMode()

    const { data: spans, isLoading, error } = useQuery({
        queryKey: ['trace-spans', traceId],
        queryFn: () => getTraceByID(traceId),
        enabled: !!traceId,
    })

    const toggleExpanded = (spanId: string) => {
        setExpandedMap(prev => {
            const newMap = new Map(prev)
            newMap.set(spanId, !prev.get(spanId))
            return newMap
        })
    }

    const handleTimelineMouseMove = useCallback((e: MouseEvent) => {
        if (!timelineRef.current) return
        const rect = timelineRef.current.getBoundingClientRect()
        const percent = Math.max(0, Math.min(100, ((e.clientX - rect.left) / rect.width) * 100))
        setDragEnd(percent)
    }, [])

    const handleTimelineMouseUp = useCallback(() => {
        setIsDragging(false)

        setDragStart(prev => {
            setDragEnd(endPrev => {
                if (prev === null || endPrev === null) return null

                const start = Math.min(prev, endPrev)
                const end = Math.max(prev, endPrev)

                // Only apply zoom if selection is at least 2% wide
                if (end - start > 2) {
                    setZoomRange({ start, end })
                }

                return null
            })
            return null
        })
    }, [])

    const handleTimelineMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
        if (!timelineRef.current) return
        const rect = timelineRef.current.getBoundingClientRect()
        const percent = ((e.clientX - rect.left) / rect.width) * 100
        setIsDragging(true)
        setDragStart(percent)
        setDragEnd(percent)
    }

    const handleResetZoom = () => {
        setZoomRange({ start: 0, end: 100 })
    }

    useEffect(() => {
        if (isDragging) {
            window.addEventListener('mousemove', handleTimelineMouseMove)
            window.addEventListener('mouseup', handleTimelineMouseUp)
            return () => {
                window.removeEventListener('mousemove', handleTimelineMouseMove)
                window.removeEventListener('mouseup', handleTimelineMouseUp)
            }
        }
    }, [isDragging, handleTimelineMouseMove, handleTimelineMouseUp])

    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-64">
                <Loader2 className="size-6 animate-spin text-brand-main-50 light:text-black" />
            </div>
        )
    }

    if (error) {
        return (
            <div className="flex items-center justify-center h-64 text-rose-300 light:text-rose-600">
                Error loading trace timeline: {error instanceof Error ? error.message :'Unknown error'}
            </div>
        )
    }

    if (!spans || spans.length === 0) {
        return (
            <div className="flex items-center justify-center h-64 text-brand-main-50 light:text-black">
                No spans found for this trace
            </div>
        )
    }

    // Calculate timeline bounds (convert protobuf Timestamp to nanoseconds)
    const timestamps = spans
        .map(s => s.timestamp ? (
            (typeof s.timestamp.seconds ==='bigint' ? s.timestamp.seconds : BigInt(s.timestamp.seconds || 0)) * BigInt(1_000_000_000) +
            (typeof s.timestamp.nanos === 'bigint' ? s.timestamp.nanos : BigInt(s.timestamp.nanos || 0))
        ) : BigInt(0))
        .filter(t => t > BigInt(0))

    const startTime = timestamps.length > 0 ? timestamps.reduce((min, t) => t < min ? t : min) : BigInt(0)
    const endTimes = spans.map(s => {
        const ts = s.timestamp ? (
            (typeof s.timestamp.seconds === 'bigint' ? s.timestamp.seconds : BigInt(s.timestamp.seconds || 0)) * BigInt(1_000_000_000) +
            (typeof s.timestamp.nanos === 'bigint' ? s.timestamp.nanos : BigInt(s.timestamp.nanos || 0))
        ) : BigInt(0)
        const duration = typeof s.duration === 'bigint' ? s.duration : BigInt(s.duration || 0)
        return ts + duration
    })
    const endTime = endTimes.length > 0 ? endTimes.reduce((max, t) => t > max ? t : max) : BigInt(0)

    // Build hierarchy and calculate row positions
    const hierarchy = buildSpanHierarchy(spans)
    const rows = new Map<string, number>()
    const hierarchyDepths = new Map<string, number>()

    // Helper to calculate hierarchy depth
    function setHierarchyDepth(span: Span, depth: number) {
        hierarchyDepths.set(span.spanId, depth)
        const children = hierarchy.get(span.spanId) || []
        children.forEach(child => setHierarchyDepth(child, depth + 1))
    }

    // Find root spans (no parent or parent not in list)
    const rootSpans = spans.filter(s => !s.parentSpanId || !spans.find(parent => parent.spanId === s.parentSpanId))

    // Calculate row positions and hierarchy depths
    const currentRow = { value: 0 }
    rootSpans.forEach(root => {
        setHierarchyDepth(root, 0)
        calculateSpanRows(root, hierarchy, rows, currentRow)
    })

    // Helper to check if a span should be visible (all ancestors must be expanded)
    const isSpanVisible = (span: Span): boolean => {
        let current: string | undefined = span.parentSpanId
        while (current) {
            const isParentExpanded = expandedMap.get(current) ?? true
            if (!isParentExpanded) return false

            // Find parent span to continue up the tree
            const parentSpan = spans.find(s => s.spanId === current)
            current = parentSpan?.parentSpanId
        }
        return true
    }

    // Filter visible spans
    const visibleSpans = spans.filter(isSpanVisible)

    const totalRows = currentRow.value
    const rowHeight = 36
    const headerHeight = 32
    const timelineHeight = totalRows * rowHeight + headerHeight + 8 // rows + header + padding

    const totalDuration = endTime - startTime
    const totalMs = Number(totalDuration) / 1_000_000

    return (
        <div className="space-y-3 pb-4">
            {/* Timeline Header */}
            <div className="flex items-center justify-between px-4 py-2.5 bg-brand-main-500/50 rounded-md border border-brand-main-500/20">
                <div className="flex items-center gap-4">
                    <div className="text-xs text-brand-main-50 light:text-black">
                        Total Duration: <span className="text-brand-main-50 light:text-brand-main-50 font-semibold ml-1">{formatDuration(totalDuration)}</span>
                    </div>
                    <div className="w-px h-4 bg-white/20 light:bg-black/20" />
                    <div className="text-xs text-brand-main-50 light:text-black">
                        Spans: <span className="text-brand-main-50 light:text-brand-main-50 font-semibold ml-1">{spans.length}</span>
                    </div>
                </div>

                {/* Legend */}
                <div className="flex items-center gap-3 text-[10px]">
                    <div className="flex items-center gap-1.5">
                        <div className="w-3 h-3 rounded-sm" style={{ backgroundColor: getStatusColor('OK') }} />
                        <span className="text-brand-main-50 light:text-black">Success</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                        <div className="w-3 h-3 rounded-sm" style={{ backgroundColor: getStatusColor('ERROR') }} />
                        <span className="text-brand-main-50 light:text-black">Error</span>
                    </div>
                    <div className="flex items-center gap-1.5">
                        <div className="w-3 h-3 rounded-sm" style={{ backgroundColor: getStatusColor('UNSET') }} />
                        <span className="text-brand-main-50 light:text-black">Unset</span>
                    </div>
                </div>
            </div>

            {/* Timeline Canvas */}
            <div className="relative w-full border border-white/10 light:border-black/10 rounded-lg overflow-hidden">
                {/* Column Headers */}
                <div className="sticky top-0 z-30 flex items-center gap-3 px-3 py-2 bg-brand-main-700/50 border-b border-white/10 light:border-black/10 backdrop-blur-sm">
                    <div className="flex-shrink-0 text-[10px] font-semibold text-brand-main-50 light:text-black uppercase tracking-wider" style={{ width: '280px' }}>
                        Span Name
                    </div>
                    <div className="flex-1 text-[10px] font-semibold text-brand-main-50 light:text-black uppercase tracking-wider">
                        Timeline
                    </div>
                    <div className="flex-shrink-0 w-[100px] text-right text-[10px] font-semibold text-brand-main-50 light:text-black uppercase tracking-wider">
                        Duration
                    </div>
                </div>

                <div
                    className="relative w-full"
                    style={{ minHeight: `${timelineHeight}px` }}
                >
                    {/* Duration header bar - positioned in timeline column */}
                    <div
                        ref={timelineRef}
                        className={cn(
                            "absolute top-0 h-8 flex items-center bg-brand-main-700/30 z-10",
                            isDragging ? "cursor-col-resize" : "cursor-crosshair"
                        )}
                        style={{ left: '280px', right: '100px', paddingLeft: '12px', paddingRight: '12px' }}
                        onMouseDown={handleTimelineMouseDown}
                    >
                        {/* Zoom range indicator (when zoomed) */}
                        {(zoomRange.start > 0 || zoomRange.end < 100) && (
                            <>
                                <div
                                    className="absolute select-none inset-y-0 bg-brand-secondary-500/10 border-l-2 border-r-2 border-brand-secondary-500/50"
                                    style={{
                                        left: `${zoomRange.start}%`,
                                        width: `${zoomRange.end - zoomRange.start}%`,
                                    }}
                                />
                                {/* Reset zoom button */}
                                <Tooltip content="Reset Zoom (Double Click)">
                                    <div
                                        className="absolute top-1 z-20 -right-24"
                                        onDoubleClick={(e) => {
                                            e.stopPropagation()
                                            handleResetZoom()
                                        }}
                                    >
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={(e) => {
                                                e.stopPropagation()
                                                handleResetZoom()
                                            }}
                                            className="h-6 w-6 p-0"
                                        >
                                            <Maximize2 className="w-3 h-3" />
                                        </Button>
                                    </div>
                                </Tooltip>
                            </>
                        )}

                        {/* Active selection during drag */}
                        {isDragging && dragStart !== null && dragEnd !== null && (
                            <div
                                className="absolute inset-y-0 bg-brand-main-400/30 border-l-2 border-r-2 border-brand-main-400 pointer-events-none"
                                style={{
                                    left: `${Math.min(dragStart, dragEnd)}%`,
                                    width: `${Math.abs(dragEnd - dragStart)}%`,
                                }}
                            />
                        )}

                        {/* Duration scale markers - adjusted for zoom */}
                        {[0, 25, 50, 75, 100].map(percent => {
                            // Calculate actual time based on zoom range
                            const zoomStart = zoomRange.start / 100
                            const zoomEnd = zoomRange.end / 100
                            const zoomWidth = zoomEnd - zoomStart
                            const actualTimePercent = zoomStart + (percent / 100) * zoomWidth
                            const displayMs = totalMs * actualTimePercent

                            return (
                                <div
                                    key={percent}
                                    className="absolute flex flex-col select-none items-center pointer-events-none"
                                    style={{ left: `${percent}%`, transform: 'translateX(-50%)' }}
                                >
                                    <div className="text-[10px] text-brand-main-50 light:text-black font-semibold">
                                        {displayMs.toFixed(0)}ms
                                    </div>
                                </div>
                            )
                        })}
                    </div>

                    {/* Grid lines - positioned in timeline column */}
                    <div
                        className="absolute top-8 bottom-0 z-0"
                        style={{ left: '280px', right: '100px', paddingLeft: '12px', paddingRight: '12px' }}
                    >
                        {[0, 25, 50, 75, 100].map(percent => (
                            <div
                                key={percent}
                                className="absolute top-0 bottom-0 border-l"
                                style={{
                                    left: `${percent}%`,
                                    borderColor: chartMode === 'light'
                                        ? (percent === 0 || percent === 100 ? 'rgba(0, 0, 0, 0.15)' : 'rgba(0, 0, 0, 0.1)')
                                        : (percent === 0 || percent === 100 ? 'rgba(255, 255, 255, 0.15)' : 'rgba(255, 255, 255, 0.1)')
                                }}
                            />
                        ))}
                    </div>

                    {/* Span bars */}
                    <div className="relative pt-8">
                        {visibleSpans.map((span) => {
                            const children = hierarchy.get(span.spanId) || []
                            const hasChildren = children.length > 0
                            const isExpanded = expandedMap.get(span.spanId) ?? true

                            // Determine if this is the last child of its parent
                            const parent = span.parentSpanId
                            const siblings = parent ? (hierarchy.get(parent) || []) : rootSpans
                            const isLast = siblings[siblings.length - 1]?.spanId === span.spanId

                            return (
                                <TimelineSpanBar
                                    key={span.spanId}
                                    span={span}
                                    startTime={startTime}
                                    endTime={endTime}
                                    rowIndex={rows.get(span.spanId) || 0}
                                    hierarchyDepth={hierarchyDepths.get(span.spanId) || 0}
                                    hasChildren={hasChildren}
                                    isLast={isLast}
                                    isExpanded={isExpanded}
                                    onToggleExpand={() => toggleExpanded(span.spanId)}
                                    onSpanHover={onSpanHover}
                                    selectedSpanId={selectedSpanId}
                                    zoomRange={zoomRange}
                                />
                            )
                        })}
                    </div>
                </div>
            </div>
        </div>
    )
}


