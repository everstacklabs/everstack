import { useMemo, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { ReactFlow, Background, BackgroundVariant, type NodeTypes, type EdgeTypes } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Icon } from '@iconify/react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useStudioStore } from '@/stores/studio-store'
import { useWorkflowAtVersion } from '@/hooks/deployments/use-workflows'
import { convertProtoNodesToStudio, convertProtoEdgesToStudio } from '../utils/convert-workflow'
import StudioNodeComponent from '../canvas/studio-node'
import { StudioEdgeComponent } from '../canvas/studio-edge'
import { VersionDiffContext } from '../canvas/version-diff-context'
import { ReactFlowProvider } from '@xyflow/react'
import type { StudioNode, StudioEdge } from '../types'

const { Badge } = ui

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

interface VersionPreviewDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    version: number | null
    onRestore?: () => void
}

function formatTimestamp(timestamp: { seconds: bigint; nanos: number } | undefined): string {
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

const previewNodeTypes: NodeTypes = { studioNode: StudioNodeComponent }
const previewEdgeTypes: EdgeTypes = { studioEdge: StudioEdgeComponent }

export function VersionPreviewDialog({ open, onOpenChange, version, onRestore }: VersionPreviewDialogProps) {
    const workflowId = useStudioStore((s) => s.workflowId)
    const { data, isLoading, error } = useWorkflowAtVersion(
        open ? workflowId : null,
        open ? version : null,
    )

    const workflow = data?.workflow
    const details = data?.details ?? []
    const changes = data?.changes ?? []

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

        toast.success(`Restored to version ${version}`)
        onOpenChange(false)
        onRestore?.()
    }, [workflow, version, onOpenChange, onRestore])

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
        activeVersion: version,
        nodeIds: changedNodeIds,
        edgeIds: changedEdgeIds,
    }), [version, changedNodeIds, changedEdgeIds])

    if (!open) return null

    return createPortal(
        <div className="fixed inset-0 right-[24rem] z-[51] flex flex-col bg-brand-main-950 border-r border-brand-main-800">
            {/* Header */}
            <div className="flex items-center justify-between border-b border-brand-main-800 px-4 py-2.5 bg-brand-main-950">
                <div className="flex items-center gap-3">
                    <button
                        onClick={() => onOpenChange(false)}
                        className="flex h-7 w-7 items-center justify-center rounded-md text-brand-main-400 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-800 transition-colors"
                    >
                        <Icon icon="lucide:arrow-left" className="h-4 w-4" />
                    </button>
                    <div className="h-4 w-px bg-brand-main-700" />
                    <Badge className="text-[11px] px-1.5 py-0 h-[18px] font-mono bg-brand-secondary-600/15 text-brand-secondary-400 border border-brand-secondary-500/40">
                        v{version}
                    </Badge>
                    <span className="text-sm text-white light:text-brand-main-50 font-medium">
                        {workflow?.name ?? 'Loading...'}
                    </span>
                    {workflow?.createdAt && (
                        <>
                            <div className="h-3.5 w-px bg-brand-main-700" />
                            <span className="text-[11px] text-brand-main-500">
                                {formatTimestamp(workflow.updatedAt ?? workflow.createdAt)}
                            </span>
                        </>
                    )}
                </div>

                <div className="flex items-center gap-2">
                    {details.map((d, idx) => (
                        <span
                            key={idx}
                            className="inline-flex items-center gap-1 text-[11px] text-brand-main-400 bg-brand-main-900 border border-brand-main-800 rounded-md px-2 py-0.5"
                        >
                            <Icon icon={CATEGORY_ICONS[d.category] || 'lucide:circle'} className="h-3 w-3 opacity-60" />
                            {CATEGORY_LABELS[d.category] || d.category}: {d.summary}
                        </span>
                    ))}
                    {details.length === 0 && changes.length > 0 && (
                        <span className="text-[11px] text-brand-main-500">{changes.join(', ')}</span>
                    )}

                    <div className="h-4 w-px bg-brand-main-700 mx-1" />

                    <button
                        onClick={handleRestore}
                        disabled={isLoading || !!error || !workflow}
                        className="flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium
                                   bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/30
                                   hover:bg-brand-secondary-600/25 hover:text-brand-secondary-200 hover:border-brand-secondary-500/50
                                   disabled:opacity-30 disabled:pointer-events-none transition-all"
                    >
                        <Icon icon="lucide:rotate-ccw" className="h-3.5 w-3.5" />
                        Restore
                    </button>
                </div>
            </div>

            {/* Canvas */}
            <div className="flex-1 relative bg-brand-main-950">
                {isLoading && (
                    <div className="absolute inset-0 flex items-center justify-center z-10">
                        <div className="flex flex-col items-center gap-3 text-brand-main-400">
                            <Icon icon="lucide:loader-2" className="h-5 w-5 animate-spin" />
                            <span className="text-xs">Loading version {version}...</span>
                        </div>
                    </div>
                )}

                {!isLoading && error && (
                    <div className="absolute inset-0 flex flex-col items-center justify-center z-10">
                        <Icon icon="lucide:alert-circle" className="h-6 w-6 text-red-400/60 light:text-red-600/60 mb-2" />
                        <p className="text-sm text-red-400/80 light:text-red-600/80">Failed to load version</p>
                        <p className="text-xs text-red-400/50 light:text-red-600/50 mt-1">{error.message}</p>
                    </div>
                )}

                {!isLoading && !error && nodes.length === 0 && (
                    <div className="absolute inset-0 flex flex-col items-center justify-center z-10">
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
                                panOnDrag
                                panOnScroll
                                zoomOnScroll
                                nodesDraggable={false}
                                nodesConnectable={false}
                                elementsSelectable={false}
                                className="studio-flow"
                                proOptions={{ hideAttribution: true }}
                                defaultEdgeOptions={{
                                    type: 'studioEdge',
                                    style: { stroke: '#525252', strokeWidth: 1 },
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
                    <div className="absolute bottom-4 left-1/2 -translate-x-1/2 z-50">
                        <div className="flex items-center gap-2 rounded-md border border-brand-main-500/20 bg-brand-main-950/80 px-3 py-1.5 backdrop-blur-sm">
                            <Icon icon="lucide:info" className="h-3 w-3 text-brand-main-400/80" />
                            <span className="text-[11px] text-brand-main-300/80">
                                Highlighted elements changed in this version
                            </span>
                        </div>
                    </div>
                )}
            </div>
        </div>,
        document.body,
    )
}
