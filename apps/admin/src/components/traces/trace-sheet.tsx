import { Iconify, ui } from '@everstack/ui'
import type { Trace } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { lazy, memo, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronRight, Search } from 'lucide-react'
import { TraceTreeView, type TraceTreeViewRef } from './trace-tree-view'
import { spanMatchesQuery } from '@/utils/trace-search'
import { InputWithIcon } from '@everstack/ui/components'
import { Route } from '@/routes/observability/traces'
import { cn } from '@everstack/utils/functions/cn'
import { useTracesStore } from '@/stores/traces-store'
import { motion, AnimatePresence } from 'framer-motion'
import { useQuery } from '@tanstack/react-query'
import { getTraceByID } from '@/server/traces'
import { TraceSheetSkeleton } from './trace-sheet-skeleton'

const { ResizablePanel, ResizablePanelGroup, ResizableHandle, Switch, Label, Popover, PopoverContent, PopoverTrigger, Tooltip, TooltipProvider, Button } = ui

const TraceTimeline = lazy(() => import('./trace-timeline').then((module) => ({ default: module.TraceTimeline })))
const AgentGraph = lazy(() => import('./agent-graph').then((module) => ({ default: module.AgentGraph })))
const TraceOverview = lazy(() => import('./trace-overview').then((module) => ({ default: module.TraceOverview })))
const TraceLogs = lazy(() => import('./trace-logs').then((module) => ({ default: module.TraceLogs })))

interface ImperativePanelHandle {
    collapse: () => void
    expand: () => void
    resize: (size: number) => void
    getSize: () => number
    isCollapsed: () => boolean
    isExpanded: () => boolean
    getId: () => string
}

interface TraceSheetProps {
    trace: Trace
    traces: Trace[]
}

function useDeferredSheetReady(key: string): boolean {
    const [ready, setReady] = useState(false)

    useEffect(() => {
        setReady(false)
        if (typeof window === 'undefined') {
            setReady(true)
            return
        }

        let secondFrame = 0
        const firstFrame = window.requestAnimationFrame(() => {
            secondFrame = window.requestAnimationFrame(() => setReady(true))
        })

        return () => {
            window.cancelAnimationFrame(firstFrame)
            if (secondFrame) window.cancelAnimationFrame(secondFrame)
        }
    }, [key])

    return ready
}

function TracePanelLoading({
    label = 'Loading trace view',
    variant = 'detail',
}: {
    label?: string
    variant?: 'tree' | 'detail'
}) {
    return <TraceSheetSkeleton variant={variant} label={label} className="h-full" />
}

export const TraceSheet = memo(function TraceSheet({ trace }: TraceSheetProps) {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const selectedSpanId = search.span as string | undefined
    const isCollapsed = search.panelCollapsed === 'true'
    const isAllExpanded = search.treeExpanded !== 'false' // Default to true
    const showTimeline = search.timeline === 'true'
    const showGraph = search.graph === 'true'
    const showLogs = search.logs === 'true'
    const { showMetadata, showDuration, setShowMetadata, setShowDuration } = useTracesStore()
    const leftPanelRef = useRef<HTMLDivElement | null>(null)
    const panelRef = useRef<ImperativePanelHandle>(null)
    const panelElementRef = useRef<HTMLDivElement>(null)
    const treeViewRef = useRef<TraceTreeViewRef>(null)
    // Animate the panel width only when NOT dragging (programmatic collapse),
    // so live resizing tracks the cursor instantly instead of lagging behind.
    const [isResizing, setIsResizing] = useState(false)
    const [hoveredSpanId, setHoveredSpanId] = useState<string | undefined>(undefined)
    const [layoutVersion, setLayoutVersion] = useState(0)
    const [searchQuery, setSearchQuery] = useState('')
    const isContentReady = useDeferredSheetReady(trace.traceId)

    // Fetch spans to get selected span details
    const { data: spans } = useQuery({
        queryKey: ['trace-spans', trace.traceId],
        queryFn: () => getTraceByID(trace.traceId),
        enabled: isContentReady && !!trace.traceId,
    })

    // Find the selected span
    const selectedSpan = selectedSpanId ? spans?.find(s => s.spanId === selectedSpanId) : undefined

    // Live count of spans matching the in-trace search box.
    const searchMatchCount = useMemo(() => {
        const q = searchQuery.trim()
        if (!q || !spans) return 0
        let n = 0
        for (const s of spans) if (spanMatchesQuery(s, q)) n++
        return n
    }, [spans, searchQuery])

    // Session id for correlated logs (fallback path when log records carry no
    // trace id). Derived from the trace's spans.
    const sessionId = useMemo(() => {
        for (const s of spans ?? []) {
            const v = s.spanAttributes?.['session.id'] || s.spanAttributes?.['session_id']
            if (v) return v
        }
        return undefined
    }, [spans])

    useEffect(() => {
        if (!leftPanelRef.current) return
        const el = leftPanelRef.current
        const ro = new ResizeObserver((entries) => {
            for (const entry of entries) {
                const width = entry.contentRect.width
                const shouldBeCollapsed = width <= 30
                if (shouldBeCollapsed !== isCollapsed) {
                    navigate({
                        search: (prev) => ({
                            ...prev,
                            panelCollapsed: shouldBeCollapsed ? 'true' : undefined,
                        }),
                        replace: true,
                    })
                }
            }
        })
        ro.observe(el)
        return () => ro.disconnect()
    }, [isCollapsed, navigate])

    // Check if all nodes are expanded and sync to URL
    useEffect(() => {
        if (!isContentReady || !treeViewRef.current) return
        const checkExpanded = () => {
            const allExpanded = treeViewRef.current?.isAllExpanded() ?? true
            if ((allExpanded && isAllExpanded !== true) || (!allExpanded && isAllExpanded === true)) {
                navigate({
                    search: (prev) => ({
                        ...prev,
                        treeExpanded: allExpanded ? undefined : 'false',
                    }),
                    replace: true,
                })
            }
        }
        // Check initially and periodically to sync with URL
        checkExpanded()
        const interval = setInterval(checkExpanded, 100)
        return () => clearInterval(interval)
    }, [trace.traceId, isAllExpanded, navigate, isContentReady])

    // Collapse left panel when timeline is shown
    useEffect(() => {
        if (showTimeline && panelRef.current) {
            panelRef.current.collapse()
        }
    }, [showTimeline])

    return (
        <TooltipProvider>
            <ResizablePanelGroup
                key={layoutVersion}
                direction="horizontal"
                className="flex-1 flex-col h-full"
            >
                <ResizablePanel
                    ref={panelRef}
                    className={cn(
                        'min-w-0 overflow-hidden',
                        !isResizing &&
                            'transition-[flex-basis,flex-grow] duration-300 ease-in-out',
                    )}
                    defaultSize={33}
                    collapsible={true}
                    minSize={1}
                    maxSize={55}
                >
                    <div ref={panelElementRef} className='w-full h-full'>
                        <div ref={leftPanelRef} className='w-full h-full'>
                            <AnimatePresence mode="wait">
                                {isCollapsed ? (
                                    <motion.div
                                        key="collapsed"
                                        initial={{ opacity: 0 }}
                                        animate={{ opacity: 1 }}
                                        exit={{ opacity: 0 }}
                                        transition={{ duration: 0.3, ease: "easeInOut" }}
                                        onClick={() => {
                                            navigate({
                                                search: (prev) => ({
                                                    ...prev,
                                                    panelCollapsed: undefined,
                                                }),
                                                replace: true,
                                            })
                                            setLayoutVersion((v) => v + 1)
                                        }}
                                        className='w-full h-full flex items-center justify-center cursor-pointer rounded-sm p-1 hover:bg-brand-main-500/50'
                                    >
                                        <motion.div
                                            className='flex flex-col items-center justify-center gap-2 text-brand-main-50 light:text-black'
                                            whileHover={{ scale: 1.1 }}
                                            transition={{ duration: 0.1 }}
                                        >
                                            <ChevronRight className='w-4 h-4' />
                                        </motion.div>
                                    </motion.div>
                                ) : (
                                    <motion.div
                                        key="expanded"
                                        initial={{ opacity: 0, x: -20 }}
                                        animate={{ opacity: 1, x: 0 }}
                                        exit={{ opacity: 0, x: -20 }}
                                        transition={{ duration: 0.1, ease: "easeInOut" }}
                                        className='w-full h-full'
                                    >
                                        <div data-slot="trace-details-tree-toolbar" className='flex items-center justify-start gap-2 w-full pt-2 px-1.5'>
                                            <InputWithIcon
                                                icon={<Search className='w-4 h-4' />}
                                                placeholder='Search spans'
                                                className='border'
                                                containerClassName='flex-1 min-w-0'
                                                value={searchQuery}
                                                onChange={(e) => setSearchQuery(e.target.value)}
                                            />
                                            {searchQuery.trim() && (
                                                <span className='shrink-0 text-[10px] text-brand-main-50 whitespace-nowrap light:text-black'>
                                                    {searchMatchCount} match{searchMatchCount === 1 ? '' : 'es'}
                                                </span>
                                            )}
                                            <div className='flex items-center justify-center gap-0.5'>
                                                {isAllExpanded ? (
                                                    <Tooltip content="Collapse All">
                                                        <Button
                                                            onClick={() => {
                                                                treeViewRef.current?.collapseAll()
                                                                navigate({
                                                                    search: (prev) => ({
                                                                        ...prev,
                                                                        treeExpanded: 'false',
                                                                    }),
                                                                    replace: true,
                                                                })
                                                            }}
                                                            variant='ghost'
                                                            size='icon'
                                                            title="Collapse All"
                                                        >
                                                            <Iconify.Icon icon='bi:arrows-collapse' className='w-5 h-5 text-brand-main-50 light:text-black' />
                                                        </Button>
                                                    </Tooltip>
                                                ) : (
                                                    <Tooltip content="Expand All">
                                                        <Button
                                                            onClick={() => {
                                                                treeViewRef.current?.expandAll()
                                                                navigate({
                                                                    search: (prev) => ({
                                                                        ...prev,
                                                                        treeExpanded: undefined,
                                                                    }),
                                                                    replace: true,
                                                                })
                                                            }}
                                                            variant='ghost'
                                                            size='icon'
                                                            title="Expand All"
                                                        >
                                                            <Iconify.Icon icon='bi:arrows-expand' className='w-5 h-5 text-brand-main-50 light:text-black' />
                                                        </Button>
                                                    </Tooltip>
                                                )}
                                                <Tooltip content={showTimeline ? "Hide Timeline" : "Show Timeline"}>
                                                    <Button
                                                        variant='ghost'
                                                        size='icon'
                                                        onClick={() => {
                                                            navigate({
                                                                search: (prev) => ({
                                                                    ...prev,
                                                                    timeline: showTimeline ? undefined : 'true',
                                                                    graph: undefined,
                                                                    view: undefined,
                                                                }),
                                                                replace: true,
                                                            })
                                                        }}
                                                    >
                                                        <Iconify.Icon
                                                            icon='ic:round-view-timeline'
                                                            className={cn('w-6 h-6 transition-colors', showTimeline ? 'text-brand-secondary-400' : 'text-brand-main-50 light:text-black')}
                                                        />
                                                    </Button>
                                                </Tooltip>
                                                <Tooltip content={showGraph ? "Hide Graph" : "Agent Graph"}>
                                                    <Button
                                                        variant='ghost'
                                                        size='icon'
                                                        onClick={() => {
                                                            navigate({
                                                                search: (prev) => ({
                                                                    ...prev,
                                                                    graph: showGraph ? undefined : 'true',
                                                                    timeline: undefined,
                                                                    view: undefined,
                                                                }),
                                                                replace: true,
                                                            })
                                                        }}
                                                    >
                                                        <Iconify.Icon
                                                            icon='hugeicons:workflow-square-06'
                                                            className={cn('w-5 h-5 transition-colors', showGraph ? 'text-brand-secondary-400' : 'text-brand-main-50 light:text-black')}
                                                        />
                                                    </Button>
                                                </Tooltip>
                                                <Tooltip content={showLogs ? "Hide Logs" : "Logs"}>
                                                    <Button
                                                        variant='ghost'
                                                        size='icon'
                                                        onClick={() => {
                                                            navigate({
                                                                search: (prev) => ({
                                                                    ...prev,
                                                                    logs: showLogs ? undefined : 'true',
                                                                    graph: undefined,
                                                                    timeline: undefined,
                                                                    view: undefined,
                                                                }),
                                                                replace: true,
                                                            })
                                                        }}
                                                    >
                                                        <Iconify.Icon
                                                            icon='ph:scroll-duotone'
                                                            className={cn('w-5 h-5 transition-colors', showLogs ? 'text-brand-secondary-400' : 'text-brand-main-50 light:text-black')}
                                                        />
                                                    </Button>
                                                </Tooltip>
                                                <Popover>
                                                    <Tooltip content="Options">
                                                        <PopoverTrigger asChild>
                                                            <Button variant='ghost' size='icon'>
                                                                <Iconify.Icon icon='mynaui:cog-two' className='w-6 h-6 text-brand-main-50 light:text-black' />
                                                            </Button>
                                                        </PopoverTrigger>
                                                    </Tooltip>
                                                    <PopoverContent side='bottom' align='end' className='p-1 w-auto'>
                                                        <div className='flex items-start justify-start gap-2 border-b border-brand-main-600 p-2'>
                                                            <span className='text-xs font-semibold'>
                                                                Trace Options
                                                            </span>
                                                        </div>
                                                        <div className='flex flex-col items-start justify-start w-full'>
                                                            <div className='flex items-center justify-between gap-2 p-2'>
                                                                <Label htmlFor="show-trace-metadata" className='text-xs'>Show Trace Metadata</Label>
                                                                <Switch id="show-trace-metadata" checked={showMetadata} onCheckedChange={setShowMetadata} />
                                                            </div>

                                                            <div className='flex items-center justify-between gap-2 w-full p-2' id="show-status-badge">
                                                                <Label htmlFor="show-duration" className='text-xs'>Show Duration</Label>
                                                                <Switch id="show-duration" checked={showDuration} onCheckedChange={setShowDuration} />
                                                            </div>
                                                        </div>
                                                    </PopoverContent>
                                                </Popover>
                                            </div>
                                        </div>
                                        <div data-slot="trace-details-tree-body" className='h-full overflow-auto px-1.5'>
                                            {isContentReady ? (
                                                <TraceTreeView
                                                    ref={treeViewRef}
                                                    traceId={trace.traceId}
                                                    hoveredSpanId={hoveredSpanId}
                                                    searchQuery={searchQuery}
                                                />
                                            ) : (
                                                <TracePanelLoading variant="tree" label="Preparing spans" />
                                            )}
                                        </div>
                                    </motion.div>
                                )}
                            </AnimatePresence>
                        </div>
                    </div>
                </ResizablePanel>
                <ResizableHandle onDragging={setIsResizing} />
                <ResizablePanel
                    className='min-w-0 overflow-hidden'
                    defaultSize={67}
                    minSize={40}
                >
                    {!isContentReady ? (
                        <TracePanelLoading label="Preparing trace" />
                    ) : (
                        <Suspense fallback={<TracePanelLoading />}>
                            {showGraph ? (
                                <div className='h-full w-full'>
                                    <AgentGraph
                                        spans={spans ?? []}
                                        selectedSpanId={selectedSpanId}
                                        onSelectSpan={(id) => navigate({ search: (prev) => ({ ...prev, span: id }), replace: true })}
                                    />
                                </div>
                            ) : showLogs ? (
                                <div className='h-full overflow-auto'>
                                    <TraceLogs traceId={trace.traceId} sessionId={sessionId} selectedSpanId={selectedSpanId} />
                                </div>
                            ) : (
                                <div className='h-full overflow-auto'>
                                    {showTimeline && (
                                        <div className='px-2 overflow-auto'>
                                            <TraceTimeline traceId={trace.traceId} onSpanHover={setHoveredSpanId} selectedSpanId={selectedSpanId} />
                                        </div>
                                    )}
                                    <TraceOverview trace={trace} selectedSpan={selectedSpan} allSpans={spans} />
                                </div>
                            )}
                        </Suspense>
                    )}
                </ResizablePanel>
            </ResizablePanelGroup>
        </TooltipProvider >
    )
})
