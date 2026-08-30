import type { StudioNode, StudioEdge, StudioNodeType, NodeConfig } from '../types'
import { getDefaultConfig, NODE_REGISTRY } from '../node-registry'
import type { WorkflowNode, WorkflowEdge } from '@/server/workflows'

/**
 * Maps a proto node type string to a StudioNodeType.
 * Handles backward compatibility and invalid types gracefully.
 */
function mapNodeType(type: string): StudioNodeType {
    if (type === 'trigger') return 'start'
    // If the type exists in the registry, use it directly
    if (type in NODE_REGISTRY) return type as StudioNodeType
    // Fallback: treat unknown types (e.g. "studioNode") as "start"
    return 'start'
}

/**
 * Converts proto WorkflowNode[] to StudioNode[].
 */
export function convertProtoNodesToStudio(protoNodes: WorkflowNode[]): StudioNode[] {
    return protoNodes.map((n) => {
        const nodeType = mapNodeType(n.type)
        const defaultCfg = getDefaultConfig(nodeType) as Record<string, unknown>
        const protoCfg = (n.config ?? {}) as Record<string, unknown>
        const config = { ...defaultCfg, ...protoCfg } as NodeConfig

        return {
            id: n.id,
            type: 'studioNode',
            position: { x: n.position?.x ?? 0, y: n.position?.y ?? 0 },
            data: {
                nodeType,
                label: n.label || nodeType,
                config,
                isConfigured: Object.keys(protoCfg).length > 0,
            },
        }
    })
}

/**
 * Converts proto WorkflowEdge[] to StudioEdge[].
 * Sets edge type to 'studioEdge' for custom rendering.
 * Resolves missing/null handle IDs to defaults from the node registry.
 */
export function convertProtoEdgesToStudio(protoEdges: WorkflowEdge[], protoNodes?: WorkflowNode[]): StudioEdge[] {
    // Build a lookup: nodeId → nodeType for resolving default handles
    const nodeTypeMap = new Map<string, StudioNodeType>()
    if (protoNodes) {
        for (const n of protoNodes) {
            nodeTypeMap.set(n.id, mapNodeType(n.type))
        }
    }

    return protoEdges.map((e) => {
        let sourceHandle = e.sourceHandle
        let targetHandle = e.targetHandle

        // Resolve null/empty/invalid handle IDs to the first handle of the correct type
        if (!sourceHandle || sourceHandle === 'null') {
            const sourceType = nodeTypeMap.get(e.source)
            if (sourceType) {
                const meta = NODE_REGISTRY[sourceType]
                const firstSource = meta?.handles.find((h) => h.type === 'source')
                sourceHandle = firstSource?.id ?? 'out'
            } else {
                sourceHandle = 'out'
            }
        }
        if (!targetHandle || targetHandle === 'null') {
            const targetType = nodeTypeMap.get(e.target)
            if (targetType) {
                const meta = NODE_REGISTRY[targetType]
                const firstTarget = meta?.handles.find((h) => h.type === 'target')
                targetHandle = firstTarget?.id ?? 'in'
            } else {
                targetHandle = 'in'
            }
        }

        return {
            id: e.id,
            source: e.source,
            target: e.target,
            sourceHandle,
            targetHandle,
            type: 'studioEdge',
        }
    })
}
