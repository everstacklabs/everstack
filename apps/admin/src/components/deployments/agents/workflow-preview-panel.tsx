import { useMemo } from 'react'
import { Link } from '@tanstack/react-router'
import {
    ReactFlow,
    ReactFlowProvider,
    type Node,
    type Edge,
    Background,
    BackgroundVariant,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { ExternalLink } from 'lucide-react'
import { ui } from '@everstack/ui'

import { NODE_REGISTRY, getDefaultConfig } from '../studio/node-registry'
import type { StudioNodeType, StudioNodeData, NodeConfig } from '../studio/types'
import ReadOnlyStudioNode from '../studio/canvas/read-only-studio-node'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface WorkflowPreviewData {
    workflow_id: string
    name: string
    description?: string
    nodes: Array<{ id: string; type: string; label: string; status: string }>
    edges: Array<{
        id: string
        source: string
        target: string
        sourceHandle?: string
        targetHandle?: string
    }>
}

// ---------------------------------------------------------------------------
// Node types — reuse the real Studio node (read-only variant)
// ---------------------------------------------------------------------------

const previewNodeTypes = { studioNode: ReadOnlyStudioNode }

// ---------------------------------------------------------------------------
// WorkflowPreviewPanel
// ---------------------------------------------------------------------------

export function WorkflowPreviewPanel({ data }: { data: WorkflowPreviewData }) {
    const chartMode = ui.useChartMode()
    const isLight = chartMode === 'light'
    const nodeTypeMap = useMemo(
        () => new Map(data.nodes.map((n) => [n.id, n.type])),
        [data.nodes],
    )

    const rfNodes: Node[] = useMemo(
        () =>
            data.nodes.map((n, i) => {
                const nodeType = (n.type in NODE_REGISTRY ? n.type : 'start') as StudioNodeType
                const defaultCfg = getDefaultConfig(nodeType) as Record<string, unknown>
                return {
                    id: n.id,
                    type: 'studioNode',
                    position: { x: 50, y: i * 150 },
                    data: {
                        nodeType,
                        label: n.label || NODE_REGISTRY[nodeType]?.label || nodeType,
                        config: defaultCfg as NodeConfig,
                        isConfigured: n.status === 'ready',
                    } satisfies StudioNodeData,
                    draggable: false,
                    connectable: false,
                    selectable: false,
                }
            }),
        [data.nodes],
    )

    const rfEdges: Edge[] = useMemo(
        () =>
            data.edges.map((e) => {
                let sh = e.sourceHandle
                let th = e.targetHandle
                if (!sh || sh === 'null') {
                    const t = nodeTypeMap.get(e.source) as StudioNodeType | undefined
                    sh = (t && NODE_REGISTRY[t]?.handles.find((h) => h.type === 'source')?.id) ?? 'out'
                }
                if (!th || th === 'null') {
                    const t = nodeTypeMap.get(e.target) as StudioNodeType | undefined
                    th = (t && NODE_REGISTRY[t]?.handles.find((h) => h.type === 'target')?.id) ?? 'in'
                }
                return {
                    id: e.id,
                    source: e.source,
                    target: e.target,
                    sourceHandle: sh,
                    targetHandle: th,
                    style: { stroke: isLight ? '#00000025' : '#ffffff25', strokeWidth: 1.5 },
                    animated: true,
                }
            }),
        [data.edges, nodeTypeMap, isLight],
    )

    const needsConfig = data.nodes.some((n) => n.status === 'needs_config')

    return (
        <div className="flex flex-col h-full">
            {/* Workflow info */}
            <div className="px-3 py-2 border-b border-brand-main-700/30 space-y-1.5 shrink-0">
                <h4 className="text-xs font-medium text-white/80 light:text-black/80">{data.name}</h4>
                {data.description && (
                    <p className="text-[10px] text-white/35 light:text-black/35 leading-tight">{data.description}</p>
                )}
                <div className="flex items-center gap-2">
                    <span
                        className={`px-1.5 py-0.5 rounded-full text-[9px] font-medium ${
                            needsConfig
                                ? 'bg-amber-500/15 text-amber-400 light:text-amber-700'
                                : 'bg-green-500/15 text-green-400 light:text-green-600'
                        }`}
                    >
                        {needsConfig ? 'Needs Config' : 'Ready'}
                    </span>
                    <span className="text-[10px] text-white/25 light:text-black/25">
                        {data.nodes.length} node{data.nodes.length !== 1 ? 's' : ''}
                    </span>
                </div>
            </div>

            {/* React Flow canvas — uses the same node component as Studio */}
            <div className="flex-1 min-h-0">
                <ReactFlowProvider>
                    <ReactFlow
                        nodes={rfNodes}
                        edges={rfEdges}
                        nodeTypes={previewNodeTypes}
                        nodesDraggable={false}
                        nodesConnectable={false}
                        elementsSelectable={false}
                        panOnDrag
                        zoomOnScroll
                        zoomOnPinch
                        zoomOnDoubleClick={false}
                        preventScrolling
                        fitView
                        fitViewOptions={{ padding: 0.3 }}
                        proOptions={{ hideAttribution: true }}
                    >
                        <Background variant={BackgroundVariant.Dots} gap={20} size={0.5} color={isLight ? '#00000008' : '#ffffff08'} />
                    </ReactFlow>
                </ReactFlowProvider>
            </div>

            {/* Actions */}
            <div className="px-3 py-2 border-t border-brand-main-700/30 shrink-0">
                <Link
                    to="/deployments/studio/$workflowId"
                    params={{ workflowId: data.workflow_id }}
                    className="flex items-center justify-center gap-1.5 w-full px-3 py-1.5 rounded-md text-[11px] font-medium bg-brand-secondary-500/15 text-brand-secondary-300 hover:bg-brand-secondary-500/25 transition-colors"
                >
                    <ExternalLink className="w-3 h-3" />
                    Open in Studio
                </Link>
            </div>
        </div>
    )
}
