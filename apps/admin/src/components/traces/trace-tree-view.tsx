import { useQuery } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { getTraceTree } from '@/server/traces'
import type { Span, SpanTreeNode } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { ui, Iconify } from '@everstack/ui'
import { Route } from '@/routes/observability/traces'
import { ChevronDown, ChevronRight, Loader2 } from 'lucide-react'
import { useState, useRef, useEffect, useCallback, useImperativeHandle, forwardRef, useMemo } from 'react'
import { flattenSpanTree, countSpanNodes } from '@/utils/trace-tree-flatten'
import { collectSearchMatches } from '@/utils/trace-search'
import { cn } from '@everstack/utils/functions/cn'
import { useTracesStore } from '@/stores/traces-store'
import { formatDuration, formatCost, calculatePercentage } from '@/utils/trace-formatters'
import { getSpanDisplayConfig } from '@/utils/span-title-name-map'
import { categoryIcons, categoryColors, categoryLabels, categoryChipSolid, getTraceNameBadge } from '@/utils/span-display-helpers'
import { getProviderAsset } from '@/components/providers/provider-icon'
import { getSpanCostUSD } from '@/utils/span-metrics'
import { costBadgeCls, statusBadge, statusTint } from './trace-viz'
import { DollarSign } from 'lucide-react'

const { Badge, Collapsible, CollapsibleTrigger, CollapsibleContent } = ui

export interface TraceTreeViewRef {
    expandAll: () => void
    collapseAll: () => void
    isAllExpanded: () => boolean
}

interface TraceTreeViewProps {
    traceId: string
    hoveredSpanId?: string
    /** In-trace search query. When non-empty the tree filters to matching spans
     *  (plus their ancestors for context) and highlights the matches. */
    searchQuery?: string
}

// During an active search every node is force-expanded so a matching descendant
// is never hidden behind a collapsed ancestor: flattening against a map that
// reports nothing collapsed yields the whole tree, which we then filter to the
// visible set. Module-level for a stable reference across renders.
const NO_COLLAPSE: Map<string, boolean> = new Map()

// Tree layout constants
const INDENT_PER_LEVEL = 24  // pixels per depth level
const CONNECTOR_OFFSET = 12  // horizontal offset from left edge to vertical line center
const ROW_HEIGHT = 36        // base row height without metadata
const ROW_HEIGHT_WITH_META = 56  // row height with metadata

// Above this many spans, the recursive renderer janks; switch to a virtualized
// flat list (performance mode: depth indentation instead of connector lines).
const VIRTUALIZE_THRESHOLD = 200
const VIRTUAL_ROW_HEIGHT = 40

import type { SpanDisplayConfig } from '@/utils/traces-common'

// Render the span's category chip. Provider (model) spans show the real
// provider/model brand logo (Claude, Cohere, ...) on a neutral chip; every other
// category gets a full-colour chip with a dark on-hue glyph.
function getCategoryIcon(displayConfig: SpanDisplayConfig, isRoot = false) {
    const { category } = displayConfig
    const baseClasses = "w-5 h-5 rounded flex items-center justify-center flex-shrink-0"

    if (category === 'provider' && displayConfig.provider) {
        const asset = getProviderAsset(displayConfig.provider)
        if (asset.type === 'image') {
            return (
                <div className={cn(baseClasses, "bg-white/10 border border-white/10 light:bg-black/10 light:border-black/10")}>
                    <img
                        src={asset.value}
                        alt=""
                        className={cn("w-3.5 h-3.5 object-contain", asset.light && "brightness-0 invert")}
                    />
                </div>
            )
        }
    }

    // Root span with no real semantic category falls through to the muted
    // `internal` chip — the same boring gray the trace list had. Give it the
    // colourful, name-hashed identicon badge so the trace's own row in the sheet
    // matches its badge in the list.
    if (isRoot && category === 'internal') {
        const badge = getTraceNameBadge(displayConfig.title)
        return (
            <div className={cn(baseClasses, badge.bg, "border", badge.border)}>
                <Iconify.Icon icon={badge.icon} className={cn("w-3.5 h-3.5", badge.iconColor)} />
            </div>
        )
    }

    const chip = categoryChipSolid[category]
    return (
        <div className={cn(baseClasses, chip.bg, "border", chip.border)}>
            <Iconify.Icon icon={categoryIcons[category]} className={cn("w-3.5 h-3.5", chip.icon)} />
        </div>
    )
}

interface SpanNodeProps {
    node: SpanTreeNode
    totalDuration: number | bigint
    depth: number
    isLast?: boolean
    expandedMap: Map<string, boolean>
    onExpandedChange: (spanId: string, expanded: boolean) => void
    hoveredSpanId?: string
}

function SpanNode({ node, totalDuration, depth, isLast = false, expandedMap, onExpandedChange, hoveredSpanId }: SpanNodeProps) {
    const navigate = Route.useNavigate()
    const search = Route.useSearch()
    const selectedSpanId = search.span as string | undefined
    const { showMetadata, showDuration } = useTracesStore()
    const hasChildren = node.children && node.children.length > 0
    const span = node.span

    if (!span) return null

    // Get display configuration for this span
    const displayConfig = useMemo(() => getSpanDisplayConfig(span), [span])
    const colors = categoryColors[displayConfig.category]

    const spanId = span.spanId || ''
    const isExpanded = expandedMap.get(spanId) ?? true
    const handleExpandedChange = (expanded: boolean) => {
        onExpandedChange(spanId, expanded)
    }

    const statusBadgeVariant =
        span.statusCode?.toUpperCase() === 'ERROR'
            ? 'error'
            : span.statusCode?.toUpperCase() === 'OK'
                ? 'success'
                : 'default'

    const nodeName = span.spanAttributes?.['span.node']
    const observationType = span.spanAttributes?.['observation.type']

    // Calculate dynamic positions based on constants
    const lineLeft = (depth - 0.9) * INDENT_PER_LEVEL + CONNECTOR_OFFSET
    const horizontalLineWidth = INDENT_PER_LEVEL - CONNECTOR_OFFSET + 16 // extends to chevron area
    const verticalLineTop = showMetadata ? -ROW_HEIGHT_WITH_META + ROW_HEIGHT / 2 : -ROW_HEIGHT / 2
    const connectorTop = ROW_HEIGHT / 1.9  // center of the row

    return (
        <div className="relative w-full">
            {depth > 0 && (
                <>
                    {/* Vertical line from parent - extends from previous row to current connector point */}
                    <div
                        className="absolute border-l border-brand-main-400"
                        style={{
                            left: lineLeft,
                            top: verticalLineTop,
                            height: isLast
                                ? Math.abs(verticalLineTop) + connectorTop  // stop at connector for last child
                                : `calc(100% + ${Math.abs(verticalLineTop)}px)`, // continue through for non-last
                        }}
                    />

                    {/* Horizontal line to node - connects vertical line to chevron */}
                    <div
                        className="absolute border-t border-brand-main-400"
                        style={{
                            left: lineLeft,
                            top: connectorTop,
                            width: horizontalLineWidth,
                        }}
                    />
                </>
            )}

            <Collapsible open={isExpanded} onOpenChange={handleExpandedChange}>
                <div
                    className={cn(
                        'relative flex w-full border border-transparent items-start justify-start gap-2 py-2 px-2 rounded transition-colors group cursor-pointer',
                        selectedSpanId === span.spanId ? 'bg-brand-secondary-500/10 border-brand-main-600/30' :
                            hoveredSpanId === span.spanId ? 'bg-brand-secondary-500/20 border-brand-secondary-500/50 ring-1 ring-brand-secondary-500/30' :
                                'hover:bg-brand-secondary-500/10 hover:border-brand-main-600/30'
                    )}
                    style={{ paddingLeft: `${depth * INDENT_PER_LEVEL + 5}px` }}
                    onClick={() => {
                        navigate({
                            search: (prev) => ({
                                ...prev,
                                span: span?.spanId || undefined,
                            })
                        })
                    }}
                >
                    {hasChildren ? (
                        <CollapsibleTrigger asChild>
                            <div className="flex-shrink-0 mt-0.5 z-10" onClick={(e) => e.stopPropagation()}>
                                <div className="flex items-center justify-center p-0.5 rounded bg-brand-main-450">
                                    {isExpanded ? (
                                        <ChevronDown className="w-3.5 h-3.5 text-brand-main-50 group-hover:text-brand-main-50 transition light:text-black light:group-hover:text-black" />
                                    ) : (
                                        <ChevronRight className="w-3.5 h-3.5 text-brand-main-50 group-hover:text-brand-main-50 transition light:text-black light:group-hover:text-black" />
                                    )}
                                </div>
                            </div>
                        </CollapsibleTrigger>
                    ) : (
                        <div className="w-3.5 flex-shrink-0" />
                    )}

                    {/* Node content */}
                    <div className="flex-1 flex-col min-w-0 flex items-start justify-start gap-2 overflow-clip">
                        <div className="flex items-center gap-2 justify-between w-full text-sm text-brand-main-50 truncate font-normal light:text-black">
                            <div className="flex items-center gap-2">
                                {getCategoryIcon(displayConfig, depth === 0)}
                                <span className="truncate text-[13px] leading-5">{displayConfig.title}</span>
                                {displayConfig.subtitle && (
                                    <span className="truncate text-[12px] leading-4 text-brand-main-50 light:text-black">{displayConfig.subtitle}</span>
                                )}
                            </div>
                            <div className="flex items-center gap-2 flex-shrink-0">
                                {/* Cost badge (P0.4) */}
                                {(() => {
                                    const costTotal = getSpanCostUSD(span)
                                    if (costTotal <= 0) return null
                                    return (
                                        <Badge className={cn('h-5 gap-0.5 border px-1.5 py-0 text-[11px] font-normal', costBadgeCls)}>
                                            <DollarSign className="h-2.5 w-2.5" />
                                            {formatCost(costTotal)}
                                        </Badge>
                                    )
                                })()}
                                {/* Category badge */}
                                <Badge className={cn("h-5 px-1.5 py-0 text-[11px] font-normal", colors.bg, colors.text, "border", colors.border)}>
                                    {categoryLabels[displayConfig.category]}
                                </Badge>
                                {showDuration && (
                                    <div className="flex items-center gap-2 text-[12px] text-brand-main-50 light:text-black">
                                        <span>{formatDuration(span.duration)}</span>
                                        <span className="text-brand-main-50 light:text-black">({calculatePercentage(span.duration, totalDuration)})</span>
                                    </div>
                                )}
                            </div>
                        </div>
                        {showMetadata && <div className="flex items-start justify-start gap-2">
                            {nodeName && (
                                <Badge className={cn('h-5 border px-1.5 py-0 text-[11px] font-normal', statusBadge('neutral'))}>
                                    {nodeName}
                                </Badge>
                            )}
                            {observationType && (
                                <Badge className={cn('h-5 border px-1.5 py-0 text-[11px] font-normal', statusBadge('neutral'))}>
                                    {observationType}
                                </Badge>
                            )}
                            <Badge variant={statusBadgeVariant} className="h-5 px-1.5 py-0 text-[11px] font-normal">
                                {span.statusCode}
                            </Badge>
                        </div>}
                    </div>

                </div>

                {/* Children */}
                {hasChildren && isExpanded && (
                    <CollapsibleContent>
                        <div>
                            {node.children.map((child, idx) => (
                                <SpanNode
                                    key={child.span?.spanId || idx}
                                    node={child}
                                    totalDuration={totalDuration}
                                    depth={depth + 1}
                                    isLast={idx === node.children.length - 1}
                                    expandedMap={expandedMap}
                                    onExpandedChange={onExpandedChange}
                                    hoveredSpanId={hoveredSpanId}
                                />
                            ))}
                        </div>
                    </CollapsibleContent>
                )}
            </Collapsible>
        </div>
    )
}

/**
 * A single flat span row for the virtualized (performance-mode) renderer. No
 * connector lines: depth is shown by indentation so each row is independent and
 * can be windowed. Keeps the same icon/title/badges and click-to-select.
 */
function FlatSpanRow({
    span,
    depth,
    hasChildren,
    expanded,
    selected,
    hovered,
    onToggle,
    isMatch = false,
    dim = false,
}: {
    span: Span
    depth: number
    hasChildren: boolean
    expanded: boolean
    selected: boolean
    hovered: boolean
    onToggle: () => void
    /** Highlight this row as a search match. */
    isMatch?: boolean
    /** Fade this row (a context ancestor of a match, not itself a match). */
    dim?: boolean
}) {
    const navigate = Route.useNavigate()
    const displayConfig = getSpanDisplayConfig(span)
    const statusBadgeVariant =
        span.statusCode?.toUpperCase() === 'ERROR'
            ? 'error'
            : span.statusCode?.toUpperCase() === 'OK'
                ? 'success'
                : 'default'

    return (
        <div
            className={cn(
                'flex w-full items-center gap-2 rounded border border-transparent px-2 py-1.5 text-sm transition-colors cursor-pointer',
                selected
                    ? 'bg-brand-secondary-500/10 border-brand-main-600/30'
                    : isMatch
                        ? 'bg-brand-secondary-500/15 border-brand-secondary-500/40 ring-1 ring-brand-secondary-500/30'
                        : hovered
                            ? 'bg-brand-secondary-500/20 border-brand-secondary-500/50'
                            : 'hover:bg-brand-secondary-500/10 hover:border-brand-main-600/30',
                dim && !selected && 'opacity-45',
            )}
            style={{ paddingLeft: `${depth * INDENT_PER_LEVEL + 6}px`, height: VIRTUAL_ROW_HEIGHT }}
            onClick={() => navigate({ search: (prev) => ({ ...prev, span: span.spanId || undefined }) })}
        >
            {hasChildren ? (
                <button
                    type="button"
                    className="flex-shrink-0 rounded bg-brand-main-450 p-0.5"
                    onClick={(e) => {
                        e.stopPropagation()
                        onToggle()
                    }}
                >
                    {expanded ? (
                        <ChevronDown className="h-3.5 w-3.5 text-brand-main-50 light:text-black" />
                    ) : (
                        <ChevronRight className="h-3.5 w-3.5 text-brand-main-50 light:text-black" />
                    )}
                </button>
            ) : (
                <div className="w-4 flex-shrink-0" />
            )}
            {getCategoryIcon(displayConfig, depth === 0)}
            <span className="truncate text-[13px] leading-5 text-brand-main-50 light:text-black">{displayConfig.title}</span>
            {displayConfig.subtitle && (
                <span className="truncate text-[12px] leading-4 text-brand-main-50 light:text-black">{displayConfig.subtitle}</span>
            )}
            <div className="ml-auto flex flex-shrink-0 items-center gap-2">
                <span className="text-[12px] text-brand-main-50 light:text-black">{formatDuration(span.duration)}</span>
                <Badge variant={statusBadgeVariant} className="h-5 px-1.5 py-0 text-[11px] font-normal">
                    {span.statusCode}
                </Badge>
            </div>
        </div>
    )
}

/** Virtualized flat-list renderer for very large traces. */
function VirtualizedSpanTree({
    root,
    expandedMap,
    onExpandedChange,
    hoveredSpanId,
    totalNodes,
    searchActive = false,
    visibleIds,
    matchIds,
}: {
    root: SpanTreeNode
    expandedMap: Map<string, boolean>
    onExpandedChange: (spanId: string, expanded: boolean) => void
    hoveredSpanId?: string
    totalNodes: number
    /** When true, render filtered search results instead of the full tree. */
    searchActive?: boolean
    /** Spans to show (matches + their ancestors). Only used when searchActive. */
    visibleIds?: Set<string>
    /** Spans that actually matched the query (highlighted). */
    matchIds?: Set<string>
}) {
    const search = Route.useSearch()
    const selectedSpanId = search.span as string | undefined
    const scrollRef = useRef<HTMLDivElement>(null)

    // During search, flatten the whole tree (force-expanded) then keep only the
    // visible set, so a match nested under a collapsed ancestor still shows with
    // its path. Otherwise honour the user's collapse state.
    const rows = useMemo(() => {
        const all = flattenSpanTree(root, searchActive ? NO_COLLAPSE : expandedMap)
        if (searchActive && visibleIds) {
            return all.filter((r) => visibleIds.has(r.spanId))
        }
        return all
    }, [root, expandedMap, searchActive, visibleIds])

    const virtualizer = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => VIRTUAL_ROW_HEIGHT,
        overscan: 12,
    })

    const matchCount = matchIds?.size ?? 0

    return (
        <div className="flex h-full flex-col">
            <div className="flex items-center gap-2 px-2 py-1 text-[11px] text-brand-main-50 light:text-black">
                {searchActive ? (
                    <>
                        <span className="rounded border border-brand-secondary-500/40 bg-brand-secondary-500/10 px-1.5 py-0.5 text-brand-secondary-300">
                            Search
                        </span>
                        {matchCount.toLocaleString()} match{matchCount === 1 ? '' : 'es'}
                    </>
                ) : (
                    <>
                        <span className="rounded border border-brand-main-600 bg-brand-main-800/50 px-1.5 py-0.5">
                            Performance mode
                        </span>
                        {totalNodes.toLocaleString()} spans, windowed for speed
                    </>
                )}
            </div>
            <div ref={scrollRef} className="min-h-0 flex-1 overflow-auto">
                <div style={{ height: `${virtualizer.getTotalSize()}px`, position: 'relative', width: '100%' }}>
                    {virtualizer.getVirtualItems().map((vi) => {
                        const row = rows[vi.index]
                        const isMatch = searchActive ? matchIds?.has(row.spanId) ?? false : false
                        return (
                            <div
                                key={row.spanId}
                                style={{
                                    position: 'absolute',
                                    top: 0,
                                    left: 0,
                                    width: '100%',
                                    transform: `translateY(${vi.start}px)`,
                                }}
                            >
                                <FlatSpanRow
                                    span={row.span as Span}
                                    depth={row.depth}
                                    hasChildren={row.hasChildren}
                                    expanded={searchActive ? true : expandedMap.get(row.spanId) ?? true}
                                    selected={selectedSpanId === row.spanId}
                                    hovered={hoveredSpanId === row.spanId}
                                    onToggle={() =>
                                        onExpandedChange(row.spanId, !(expandedMap.get(row.spanId) ?? true))
                                    }
                                    isMatch={isMatch}
                                    dim={searchActive && !isMatch}
                                />
                            </div>
                        )
                    })}
                </div>
            </div>
        </div>
    )
}

export const TraceTreeView = forwardRef<TraceTreeViewRef, TraceTreeViewProps>(
    ({ traceId, hoveredSpanId, searchQuery }, ref) => {
        const { data: treeRoot, isLoading, error } = useQuery({
            queryKey: ['trace-tree', traceId],
            queryFn: () => getTraceTree(traceId),
            enabled: !!traceId,
        })

        // Track expanded state for all nodes by spanId
        const [expandedMap, setExpandedMap] = useState<Map<string, boolean>>(new Map())
        const treeRef = useRef<SpanTreeNode | null>(null)

        // Build a set of all span IDs in the tree
        const getAllSpanIds = useCallback((node: SpanTreeNode): string[] => {
            const ids: string[] = []
            if (node.span?.spanId) {
                ids.push(node.span.spanId)
            }
            if (node.children) {
                for (const child of node.children) {
                    ids.push(...getAllSpanIds(child))
                }
            }
            return ids
        }, [])

        // Get only span IDs of nodes that have children
        const getNodesWithChildren = useCallback((node: SpanTreeNode): string[] => {
            const ids: string[] = []
            if (node.children && node.children.length > 0 && node.span?.spanId) {
                ids.push(node.span.spanId)
            }
            if (node.children) {
                for (const child of node.children) {
                    ids.push(...getNodesWithChildren(child))
                }
            }
            return ids
        }, [])

        // Initialize all nodes as expanded by default
        useEffect(() => {
            if (treeRoot) {
                treeRef.current = treeRoot
                const allIds = getAllSpanIds(treeRoot)
                const newMap = new Map<string, boolean>()
                allIds.forEach(id => newMap.set(id, true))
                setExpandedMap(newMap)
            }
        }, [treeRoot, getAllSpanIds])

        // Expose expandAll and collapseAll via ref
        useImperativeHandle(ref, () => ({
            expandAll: () => {
                if (treeRef.current) {
                    const allIds = getAllSpanIds(treeRef.current)
                    const newMap = new Map<string, boolean>()
                    allIds.forEach(id => newMap.set(id, true))
                    setExpandedMap(newMap)
                }
            },
            collapseAll: () => {
                if (treeRef.current) {
                    const allIds = getAllSpanIds(treeRef.current)
                    const newMap = new Map<string, boolean>()
                    allIds.forEach(id => newMap.set(id, false))
                    setExpandedMap(newMap)
                }
            },
            isAllExpanded: () => {
                if (!treeRef.current) return true
                const nodesWithChildren = getNodesWithChildren(treeRef.current)
                if (nodesWithChildren.length === 0) return true
                // Check if all nodes with children are expanded
                return nodesWithChildren.every(id => expandedMap.get(id) === true)
            },
        }), [getAllSpanIds, getNodesWithChildren, expandedMap])

        const handleExpandedChange = useCallback((spanId: string, expanded: boolean) => {
            setExpandedMap(prev => {
                const next = new Map(prev)
                next.set(spanId, expanded)
                return next
            })
        }, [])

        // In-trace search: collect the spans that match the query plus every
        // ancestor of a match (so the path to each hit stays visible). Recomputed
        // only when the tree or the query changes.
        const query = (searchQuery ?? '').trim()
        const searchActive = query.length > 0
        const { matchIds, visibleIds } = useMemo(
            () => collectSearchMatches(treeRoot, query),
            [treeRoot, query],
        )

        if (isLoading) {
            return (
                <div className="flex items-center justify-center h-64">
                    <Loader2 className="size-6 animate-spin text-brand-main-50 light:text-black" />
                </div>
            )
        }

        if (error) {
            return (
                <div className={cn('flex items-center justify-center h-64', statusTint.error.text)}>
                    Error loading trace tree: {error instanceof Error ? error.message : 'Unknown error'}
                </div>
            )
        }

        if (!treeRoot || !treeRoot.span) {
            return (
                <div className="flex items-center justify-center h-64 text-brand-main-50 light:text-black">
                    No trace data found
                </div>
            )
        }

        // Get total duration from root span
        const totalDuration = treeRoot.span.duration
        const totalNodes = countSpanNodes(treeRoot)

        // Active search: render filtered, force-expanded results through the flat
        // (virtualized) path so it stays fast even on very large traces.
        if (searchActive) {
            if (matchIds.size === 0) {
                return (
                    <div className="flex h-64 flex-col items-center justify-center gap-1 px-4 text-center text-sm text-brand-main-50 light:text-black">
                        <span>No spans match &ldquo;{query}&rdquo;</span>
                        <span className="text-xs text-brand-main-50 light:text-black">Searches span name, type, status, and I/O.</span>
                    </div>
                )
            }
            return (
                <VirtualizedSpanTree
                    root={treeRoot}
                    expandedMap={expandedMap}
                    onExpandedChange={handleExpandedChange}
                    hoveredSpanId={hoveredSpanId}
                    totalNodes={totalNodes}
                    searchActive
                    visibleIds={visibleIds}
                    matchIds={matchIds}
                />
            )
        }

        if (totalNodes > VIRTUALIZE_THRESHOLD) {
            return (
                <VirtualizedSpanTree
                    root={treeRoot}
                    expandedMap={expandedMap}
                    onExpandedChange={handleExpandedChange}
                    hoveredSpanId={hoveredSpanId}
                    totalNodes={totalNodes}
                />
            )
        }

        return (
            <div className="py-2">
                <SpanNode
                    node={treeRoot}
                    totalDuration={totalDuration}
                    depth={0}
                    expandedMap={expandedMap}
                    onExpandedChange={handleExpandedChange}
                    hoveredSpanId={hoveredSpanId}
                />
            </div>
        )
    }
)
