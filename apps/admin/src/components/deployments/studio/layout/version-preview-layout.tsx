import { useMemo, useCallback, useState } from 'react'
import { ReactFlow, Background, BackgroundVariant, MarkerType, type NodeTypes, type EdgeTypes, ReactFlowProvider } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Icon } from '@iconify/react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Route } from '@/routes/deployments/studio/$workflowId'
import { useStudioStore } from '@/stores/studio-store'
import { useWorkflowAtVersion, useWorkflowVersionHistory } from '@/hooks/deployments/use-workflows'
import { convertProtoNodesToStudio, convertProtoEdgesToStudio } from '../utils/convert-workflow'
import StudioNodeComponent from '../canvas/studio-node'
import { StudioEdgeComponent } from '../canvas/studio-edge'
import { VersionDiffContext } from '../canvas/version-diff-context'
import type { StudioNode, StudioEdge } from '../types'
import type { WorkflowVersionEntry, WorkflowChangeDetail } from '@/server/workflows'

const { Badge, Button, Collapsible, CollapsibleTrigger, CollapsibleContent } = ui

const CATEGORY_ICONS: Record<string, string> = {
    nodes: 'lucide:box',
    edges: 'lucide:git-branch',
    name: 'lucide:type',
    description: 'lucide:file-text',
    status: 'lucide:zap',
}

const CATEGORY_LABELS: Record<string, string> = {
    nodes: 'Nodes',
    edges: 'Connections',
    name: 'Name',
    description: 'Description',
    status: 'Status',
}

function formatTimestampFull(timestamp: { seconds: bigint; nanos: number } | undefined): string {
    if (!timestamp) return ''
    const date = new Date(Number(timestamp.seconds) * 1000)
    return date.toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
    })
}

function formatTimestampRelative(timestamp: { seconds: bigint; nanos: number } | undefined): string {
    if (!timestamp) return ''
    const date = new Date(Number(timestamp.seconds) * 1000)
    const now = new Date()
    const diffMs = now.getTime() - date.getTime()
    const diffMins = Math.floor(diffMs / 60000)
    const diffHours = Math.floor(diffMs / 3600000)
    const diffDays = Math.floor(diffMs / 86400000)

    if (diffMins < 1) return 'Just now'
    if (diffMins < 60) return `${diffMins}m ago`
    if (diffHours < 24) return `${diffHours}h ago`
    if (diffDays < 7) return `${diffDays}d ago`
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

function getCollapsedSummary(entry: WorkflowVersionEntry): string {
    if (entry.details && entry.details.length > 0) {
        return entry.details
            .map((d) => {
                const label = CATEGORY_LABELS[d.category] || d.category
                if (d.category === 'status') return d.summary
                return `${label} updated`
            })
            .join(', ')
    }
    return entry.changes.join(', ')
}

function ChangeDetailSection({ detail }: { detail: WorkflowChangeDetail }) {
    const icon = CATEGORY_ICONS[detail.category] || 'lucide:circle'
    const label = CATEGORY_LABELS[detail.category] || detail.category

    return (
        <div className="py-1">
            <div className="flex items-center gap-1.5 text-xs text-brand-main-200">
                <Icon icon={icon} className="h-3.5 w-3.5 text-brand-main-400 flex-shrink-0" />
                <span className="font-medium">{label}</span>
                <span className="text-brand-main-400">({detail.summary})</span>
            </div>
            {detail.items && detail.items.length > 0 && (
                <ul className="ml-5 mt-1 space-y-0.5">
                    {detail.items.map((item, idx) => (
                        <li key={idx} className="text-[11px] text-brand-main-300 flex items-center gap-1.5">
                            <span className="text-brand-main-500">-</span>
                            {item}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    )
}

function SidebarVersionEntry({
    entry,
    isLatest,
    isSelected,
    onSelect,
}: {
    entry: WorkflowVersionEntry
    isLatest: boolean
    isSelected: boolean
    onSelect: (version: number) => void
}) {
    const [isOpen, setIsOpen] = useState(false)
    const hasDetails = entry.details && entry.details.length > 0
    const isExpandable = hasDetails || entry.changes.length > 0

    return (
        <div className="relative flex gap-3">
            {/* Timeline dot */}
            <div className="relative z-10 mt-1.5 flex-shrink-0">
                <div
                    className={`h-[15px] w-[15px] rounded-full border-2 ${isSelected
                        ? 'border-brand-secondary-400 bg-brand-secondary-500/40'
                        : isLatest
                            ? 'border-brand-secondary-500 bg-brand-secondary-500/30'
                            : 'border-brand-main-600 bg-brand-main-800'
                        }`}
                />
            </div>

            {/* Content */}
            <div className="flex-1 min-w-0 pb-1">
                {isExpandable ? (
                    <Collapsible open={isOpen} onOpenChange={setIsOpen}>
                        <CollapsibleTrigger className="w-full text-left cursor-pointer">
                            <div className="flex items-start justify-between gap-2">
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-2 mb-0.5">
                                        <Badge
                                            className={`text-[10px] px-1.5 py-0 h-4 font-mono font-normal ${isSelected
                                                ? 'bg-brand-secondary-600/30 text-brand-secondary-200 border border-brand-secondary-400'
                                                : isLatest
                                                    ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500'
                                                    : 'bg-brand-main-600/20 text-brand-main-300 border border-brand-main-500'
                                                }`}
                                        >
                                            v{entry.version}
                                        </Badge>
                                        <span className="text-[11px] text-brand-main-400">
                                            {formatTimestampRelative(entry.timestamp)}
                                        </span>
                                    </div>
                                    <p className="text-xs text-brand-main-300 truncate">
                                        {getCollapsedSummary(entry)}
                                    </p>
                                </div>
                                <Icon
                                    icon={isOpen ? 'lucide:chevron-down' : 'lucide:chevron-right'}
                                    className="h-3.5 w-3.5 text-brand-main-500 mt-1 flex-shrink-0"
                                />
                            </div>
                        </CollapsibleTrigger>
                        <CollapsibleContent>
                            <div className="mt-2 pl-1 border-l border-brand-main-700 ml-1">
                                {hasDetails ? (
                                    entry.details.map((detail, idx) => (
                                        <ChangeDetailSection key={idx} detail={detail} />
                                    ))
                                ) : (
                                    <div className="space-y-0.5 py-1">
                                        {entry.changes.map((change, idx) => (
                                            <p key={idx} className="text-xs text-brand-main-200">
                                                {change}
                                            </p>
                                        ))}
                                    </div>
                                )}

                                {!isSelected && (
                                    <button
                                        onClick={() => onSelect(entry.version)}
                                        className="mt-2 flex items-center gap-1.5 text-[11px] rounded px-2 py-1 text-brand-main-400 hover:text-brand-main-200 hover:bg-brand-main-700/50 transition-colors"
                                    >
                                        <Icon icon="lucide:eye" className="h-3 w-3" />
                                        View version
                                    </button>
                                )}
                            </div>
                        </CollapsibleContent>
                    </Collapsible>
                ) : (
                    <div>
                        <div className="flex items-center gap-2 mb-1">
                            <Badge
                                className={`text-[10px] px-1.5 py-0 h-4 font-mono font-normal ${isSelected
                                    ? 'bg-brand-secondary-600/30 text-brand-secondary-200 border border-brand-secondary-400'
                                    : isLatest
                                        ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500'
                                        : 'bg-brand-main-600/20 text-brand-main-300 border border-brand-main-500'
                                    }`}
                            >
                                v{entry.version}
                            </Badge>
                            <span className="text-[11px] text-brand-main-400">
                                {formatTimestampRelative(entry.timestamp)}
                            </span>
                        </div>
                        <p className="text-xs text-brand-main-300">
                            {getCollapsedSummary(entry)}
                        </p>
                    </div>
                )}
            </div>
        </div>
    )
}

const previewNodeTypes: NodeTypes = { studioNode: StudioNodeComponent }
const previewEdgeTypes: EdgeTypes = { studioEdge: StudioEdgeComponent }

interface VersionPreviewLayoutProps {
    previewVersion: number
}

export function VersionPreviewLayout({ previewVersion }: VersionPreviewLayoutProps) {
    const navigate = Route.useNavigate()
    const workflowId = useStudioStore((s) => s.workflowId)

    const { data, isLoading, error } = useWorkflowAtVersion(workflowId, previewVersion)
    const { data: versions, isLoading: versionsLoading } = useWorkflowVersionHistory(workflowId)

    const workflow = data?.workflow
    const details = data?.details ?? []
    const changes = data?.changes ?? []

    const handleBack = useCallback(() => {
        navigate({ search: (prev) => ({ ...prev, version: undefined }) })
    }, [navigate])

    const handleRestore = useCallback(() => {
        if (!workflow) return

        const rawNodes = workflow.nodes || []
        const studioNodes = convertProtoNodesToStudio(rawNodes)
        const studioEdges = convertProtoEdgesToStudio(workflow.edges || [], rawNodes)

        useStudioStore.getState().setWorkflowData({
            id: useStudioStore.getState().workflowId!,
            name: workflow.name || '',
            description: workflow.description || '',
            nodes: studioNodes,
            edges: studioEdges,
            viewport: workflow.viewport
                ? { x: workflow.viewport.x, y: workflow.viewport.y, zoom: workflow.viewport.zoom }
                : undefined,
            enabled: workflow.enabled ?? false,
            version: useStudioStore.getState().version,
        })

        toast.success(`Restored to version ${previewVersion}`)
        navigate({ search: (prev) => ({ ...prev, version: undefined }) })
    }, [workflow, previewVersion, navigate])

    const handleSelectVersion = useCallback((version: number) => {
        navigate({ search: (prev) => ({ ...prev, version }) })
    }, [navigate])

    // Build node/edge ID sets for highlighting changes
    const changedNodeIds = useMemo(() => {
        const ids = new Set<string>()
        for (const d of details) {
            if (d.category === 'nodes' && d.itemIds) {
                for (const id of d.itemIds) ids.add(id)
            }
        }
        return ids
    }, [details])

    const changedEdgeIds = useMemo(() => {
        const ids = new Set<string>()
        for (const d of details) {
            if (d.category === 'edges' && d.itemIds) {
                for (const id of d.itemIds) ids.add(id)
            }
        }
        return ids
    }, [details])

    // Convert proto nodes/edges to React Flow format
    const nodes: StudioNode[] = useMemo(() => {
        if (!workflow?.nodes) return []
        return convertProtoNodesToStudio(workflow.nodes)
    }, [workflow?.nodes])

    const edges: StudioEdge[] = useMemo(() => {
        if (!workflow?.edges) return []
        return convertProtoEdgesToStudio(workflow.edges, workflow.nodes)
    }, [workflow?.edges, workflow?.nodes])

    const hasCanvasChanges = changedNodeIds.size > 0 || changedEdgeIds.size > 0

    const diffContextValue = useMemo(() => ({
        activeVersion: previewVersion,
        nodeIds: changedNodeIds,
        edgeIds: changedEdgeIds,
    }), [previewVersion, changedNodeIds, changedEdgeIds])

    const sortedVersions = versions ? [...versions].reverse() : []

    return (
        <div className="flex h-screen bg-brand-main-900">
            {/* Left: Preview area */}
            <div className="flex flex-1 flex-col min-w-0">
                {/* Header bar */}
                <div className="flex items-center justify-between border-b border-brand-main-500 px-4 py-2.5 bg-brand-main-950">
                    <div className="flex items-center gap-3">
                        <button
                            onClick={handleBack}
                            className="flex h-7 w-7 items-center justify-center rounded-md text-brand-main-400 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-800 transition-colors"
                        >
                            <Icon icon="lucide:arrow-left" className="h-4 w-4" />
                        </button>
                        <div className="h-4 w-px bg-brand-main-700" />
                        <Badge className="text-[11px] px-1.5 py-0 h-[18px] font-mono bg-brand-secondary-600/15 text-brand-secondary-400 border border-brand-secondary-500/40">
                            v{previewVersion}
                        </Badge>
                        <span className="text-sm text-white light:text-brand-main-50 font-medium">
                            {workflow?.name ?? 'Loading...'}
                        </span>
                        {workflow?.createdAt && (
                            <>
                                <div className="h-3.5 w-px bg-brand-main-700" />
                                <span className="text-[11px] text-brand-main-300">
                                    {formatTimestampFull(workflow.updatedAt ?? workflow.createdAt)}
                                </span>
                            </>
                        )}
                    </div>

                    <div className="flex items-center gap-2">
                        {details.map((d, idx) => (
                            <span
                                key={idx}
                                className="inline-flex items-center gap-1 text-[11px] text-brand-main-300 bg-brand-main-800 border border-brand-main-600 rounded px-2 py-0.5"
                            >
                                <Icon icon={CATEGORY_ICONS[d.category] || 'lucide:circle'} className="h-3 w-3 opacity-60" />
                                {CATEGORY_LABELS[d.category] || d.category}: {d.summary}
                            </span>
                        ))}
                        {details.length === 0 && changes.length > 0 && (
                            <span className="text-[11px] text-brand-main-400">{changes.join(', ')}</span>
                        )}

                        <div className="h-4 w-px bg-brand-main-700 mx-1" />

                        <Button
                            onClick={handleRestore}
                            disabled={isLoading || !!error || !workflow}
                            className="flex items-center gap-1.5 rounded px-3 py-1.5 text-xs font-medium
                                       bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/30
                                       hover:bg-brand-secondary-600/25 hover:text-brand-secondary-200 hover:border-brand-secondary-500/50
                                       disabled:opacity-30 disabled:pointer-events-none transition-all"
                        >
                            <Icon icon="lucide:rotate-ccw" className="h-3.5 w-3.5" />
                            Restore
                        </Button>
                    </div>
                </div>

                {/* Canvas */}
                <div className="flex-1 relative bg-brand-main-950">
                    {isLoading && (
                        <div className="absolute inset-0 flex items-center justify-center">
                            <div className="flex flex-col items-center gap-3 text-brand-main-400">
                                <Icon icon="lucide:loader-2" className="h-5 w-5 animate-spin" />
                                <span className="text-xs">Loading version {previewVersion}...</span>
                            </div>
                        </div>
                    )}

                    {!isLoading && error && (
                        <div className="absolute inset-0 flex flex-col items-center justify-center">
                            <Icon icon="lucide:alert-circle" className="h-6 w-6 text-red-400/60 light:text-red-600/60 mb-2" />
                            <p className="text-sm text-red-400/80 light:text-red-600/80">Failed to load version</p>
                            <p className="text-xs text-red-400/50 light:text-red-600/50 mt-1">{error.message}</p>
                        </div>
                    )}

                    {!isLoading && !error && nodes.length === 0 && (
                        <div className="absolute inset-0 flex flex-col items-center justify-center">
                            <Icon icon="lucide:layout-grid" className="h-6 w-6 text-brand-main-600 mb-2" />
                            <p className="text-xs text-brand-main-500">No nodes in this version</p>
                        </div>
                    )}

                    {nodes.length > 0 && (
                        <VersionDiffContext.Provider value={diffContextValue}>
                            <ReactFlowProvider>
                                <ReactFlow
                                    nodes={nodes}
                                    edges={edges}
                                    nodeTypes={previewNodeTypes}
                                    edgeTypes={previewEdgeTypes}
                                    fitView
                                    panOnDrag={false}
                                    panOnScroll={false}
                                    zoomOnScroll={false}
                                    nodesDraggable={false}
                                    nodesConnectable={false}
                                    elementsSelectable={false}
                                    className="studio-flow"
                                    proOptions={{ hideAttribution: true }}
                                    defaultEdgeOptions={{
                                        type: 'studioEdge',
                                        style: { stroke: '#525252', strokeWidth: 1 },
                                        markerEnd: {
                                            type: MarkerType.ArrowClosed,
                                            width: 8,
                                            height: 8,
                                            color: '#525252',
                                        },
                                    }}
                                >
                                    <Background
                                        variant={BackgroundVariant.Dots}
                                        gap={20}
                                        size={1}
                                        color="#282828"
                                    />
                                </ReactFlow>
                            </ReactFlowProvider>
                        </VersionDiffContext.Provider>
                    )}

                    {/* Changes legend */}
                    {hasCanvasChanges && !isLoading && (
                        <div className="absolute bottom-4 left-1/2 -translate-x-1/2">
                            <div className="flex items-center gap-2 rounded-md border border-brand-secondary-500/20 bg-brand-secondary-950/10 px-3 py-1.5 backdrop-blur-sm">
                                <Icon icon="lucide:info" className="h-3 w-3 text-brand-secondary-400/80" />
                                <span className="text-[11px] text-brand-secondary-300/80">
                                    Highlighted elements changed in this version
                                </span>
                            </div>
                        </div>
                    )}
                </div>
            </div>

            {/* Right: Version history sidebar */}
            <div className="w-96 flex flex-col border-l border-brand-main-800 bg-brand-main-900">
                <div className="flex items-center justify-between gap-2 px-4 py-[13px] border-b border-brand-main-500">
                    <div className="flex items-center gap-2">
                        <Icon icon="lucide:clock" className="h-4 w-4 text-brand-main-300" />
                        <span className="text-white light:text-brand-main-50 text-base font-semibold">Version History</span>
                    </div>
                    <Button variant="ghost" size="xs" onClick={() => navigate({ search: (prev) => ({ ...prev, version: undefined }) })}>
                        <Icon icon="lucide:x" className="h-4 w-4 text-brand-main-300" />
                    </Button>
                </div>

                <div className="flex-1 overflow-y-auto px-4 py-3">
                    {versionsLoading && (
                        <div className="flex items-center justify-center py-12">
                            <Icon icon="lucide:loader-2" className="h-5 w-5 animate-spin text-brand-main-400" />
                        </div>
                    )}

                    {!versionsLoading && sortedVersions.length === 0 && (
                        <div className="flex flex-col items-center justify-center py-12 text-brand-main-400">
                            <Icon icon="lucide:clock" className="h-8 w-8 mb-2 opacity-50" />
                            <p className="text-sm">No version history yet</p>
                            <p className="text-xs mt-1 opacity-70">Changes will appear here after saving</p>
                        </div>
                    )}

                    {!versionsLoading && sortedVersions.length > 0 && (
                        <div className="relative">
                            {/* Vertical timeline line */}
                            <div className="absolute left-[7px] top-2 bottom-2 w-px bg-brand-main-700" />

                            <div className="space-y-4">
                                {sortedVersions.map((entry, index) => (
                                    <SidebarVersionEntry
                                        key={entry.version}
                                        entry={entry}
                                        isLatest={index === 0}
                                        isSelected={entry.version === previewVersion}
                                        onSelect={handleSelectVersion}
                                    />
                                ))}
                            </div>
                        </div>
                    )}
                </div>
            </div>
        </div>
    )
}
