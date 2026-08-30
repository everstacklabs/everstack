import { useMemo, useState, useCallback } from 'react'
import { Link } from '@tanstack/react-router'
import {
    ExternalLink,
    Play,
} from 'lucide-react'
import {
    ReactFlow,
    ReactFlowProvider,
    type Node,
    type Edge,
    Background,
    BackgroundVariant,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'

import { NODE_REGISTRY, getDefaultConfig } from '../studio/node-registry'
import type { StudioNodeType, StudioNodeData, NodeConfig } from '../studio/types'
import ReadOnlyStudioNode from '../studio/canvas/read-only-studio-node'
import { executeWorkflow } from '@/server/workflow-execution'
import { useSession } from '@/hooks/auth/use-auth'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface WorkflowNodeStatus {
    id: string
    type: string
    label: string
    status: 'ready' | 'needs_config'
}

interface WorkflowEdge {
    id: string
    source: string
    target: string
    sourceHandle?: string
    targetHandle?: string
    source_handle?: string
    target_handle?: string
}

interface WorkflowResultData {
    workflow_id: string
    name: string
    description?: string
    nodes: WorkflowNodeStatus[]
    edges: WorkflowEdge[]
    studio_url: string
}

// ---------------------------------------------------------------------------
// Node types — reuse the real Studio node (read-only variant)
// ---------------------------------------------------------------------------

const cardNodeTypes = { studioNode: ReadOnlyStudioNode }

// ---------------------------------------------------------------------------
// WorkflowResultCard
// ---------------------------------------------------------------------------

export function WorkflowResultCard({ resultJson }: { resultJson: string }) {
    const parsed = useMemo<WorkflowResultData | null>(() => {
        try {
            return JSON.parse(resultJson)
        } catch {
            return null
        }
    }, [resultJson])

    const { data: session } = useSession()
    const tenantId = session?.user?.organizations?.[0]?.id ?? ''
    const [running, setRunning] = useState(false)
    const [runResult, setRunResult] = useState<string | null>(null)
    const [runError, setRunError] = useState<string | null>(null)

    const handleRun = useCallback(() => {
        if (!parsed || !tenantId || running) return
        setRunning(true)
        setRunResult(null)
        setRunError(null)

        executeWorkflow(
            parsed.workflow_id,
            tenantId,
            [{ role: 'user', content: 'Hello' }],
            () => {},
            (error) => {
                setRunError(error.message)
                setRunning(false)
            },
            (content) => {
                setRunResult(content || 'Workflow completed successfully.')
                setRunning(false)
            },
        )
    }, [parsed, tenantId, running])

    if (!parsed) {
        return (
            <pre className="text-[11px] font-mono text-white/50 whitespace-pre-wrap break-all light:text-black/50">
                {resultJson}
            </pre>
        )
    }

    // Build node type lookup
    const nodeTypeMap = new Map(parsed.nodes.map((n) => [n.id, n.type]))

    // Transform into proper StudioNode format so ReadOnlyStudioNode renders correctly
    const rfNodes: Node[] = parsed.nodes.map((n, i) => {
        const nodeType = (n.type in NODE_REGISTRY ? n.type : 'start') as StudioNodeType
        const defaultCfg = getDefaultConfig(nodeType) as Record<string, unknown>
        return {
            id: n.id,
            type: 'studioNode',
            position: { x: 100, y: i * 140 },
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
    })

    const rfEdges: Edge[] = parsed.edges.map((e) => {
        let sh = e.sourceHandle ?? e.source_handle
        let th = e.targetHandle ?? e.target_handle

        if (!sh || sh === 'null') {
            const srcType = nodeTypeMap.get(e.source) as StudioNodeType | undefined
            sh = (srcType && NODE_REGISTRY[srcType]?.handles.find((h) => h.type === 'source')?.id) ?? 'out'
        }
        if (!th || th === 'null') {
            const tgtType = nodeTypeMap.get(e.target) as StudioNodeType | undefined
            th = (tgtType && NODE_REGISTRY[tgtType]?.handles.find((h) => h.type === 'target')?.id) ?? 'in'
        }

        return {
            id: e.id,
            source: e.source,
            target: e.target,
            sourceHandle: sh,
            targetHandle: th,
            style: { stroke: '#ffffff20', strokeWidth: 1.5 },
            animated: true,
        }
    })

    const needsConfig = parsed.nodes.some((n) => n.status === 'needs_config')

    return (
        <div className="space-y-3">
            {/* Header */}
            <div className="flex items-center justify-between">
                <div>
                    <h4 className="text-sm font-medium text-white/90 light:text-black/90">{parsed.name}</h4>
                    {parsed.description && (
                        <p className="text-[11px] text-white/40 mt-0.5 light:text-black/40">{parsed.description}</p>
                    )}
                </div>
                <span
                    className={`px-2 py-0.5 rounded-full text-[10px] font-medium ${
                        needsConfig
                            ? 'bg-amber-500/15 text-amber-400'
                            : 'bg-green-500/15 text-green-400'
                    }`}
                >
                    {needsConfig ? 'Needs Config' : 'Ready'}
                </span>
            </div>

            {/* React Flow canvas — same nodes as Studio */}
            <div className="rounded-lg border border-brand-main-700/30 bg-brand-main-950/50 overflow-hidden" style={{ height: 300 }}>
                <ReactFlowProvider>
                    <ReactFlow
                        nodes={rfNodes}
                        edges={rfEdges}
                        nodeTypes={cardNodeTypes}
                        nodesDraggable={false}
                        nodesConnectable={false}
                        elementsSelectable={false}
                        panOnDrag={false}
                        zoomOnScroll={false}
                        zoomOnPinch={false}
                        zoomOnDoubleClick={false}
                        preventScrolling={false}
                        fitView
                        fitViewOptions={{ padding: 0.3 }}
                        proOptions={{ hideAttribution: true }}
                    >
                        <Background variant={BackgroundVariant.Dots} gap={16} size={0.5} color="#ffffff08" />
                    </ReactFlow>
                </ReactFlowProvider>
            </div>

            {/* Action buttons */}
            <div className="flex items-center gap-2">
                <Link
                    to="/deployments/studio/$workflowId"
                    params={{ workflowId: parsed.workflow_id }}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-[11px] font-medium bg-brand-secondary-500/15 text-brand-secondary-300 hover:bg-brand-secondary-500/25 transition-colors"
                >
                    <ExternalLink className="w-3 h-3" />
                    Open in Studio
                </Link>
                <button
                    type="button"
                    onClick={handleRun}
                    disabled={running}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-[11px] font-medium bg-green-500/15 text-green-400 hover:bg-green-500/25 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                    <Play className="w-3 h-3" />
                    {running ? 'Running...' : 'Run'}
                </button>
            </div>

            {/* Run result */}
            {runResult && (
                <div className="rounded bg-green-950/30 border border-green-500/15 px-3 py-2">
                    <div className="text-[10px] uppercase tracking-wider text-green-400/60 mb-1">Result</div>
                    <pre className="text-[11px] font-mono text-green-300/80 whitespace-pre-wrap break-all max-h-48 overflow-y-auto">
                        {runResult}
                    </pre>
                </div>
            )}
            {runError && (
                <div className="rounded bg-red-950/30 border border-red-500/15 px-3 py-2">
                    <div className="text-[10px] uppercase tracking-wider text-red-400/60 mb-1">Error</div>
                    <pre className="text-[11px] font-mono text-red-300/80 whitespace-pre-wrap break-all">
                        {runError}
                    </pre>
                </div>
            )}
        </div>
    )
}
