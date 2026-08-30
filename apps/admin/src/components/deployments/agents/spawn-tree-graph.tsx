import { useMemo } from 'react'
import { cn } from '@/lib/utils'
import { Iconify } from '@everstack/ui/icons'

type SpawnNode = {
    id: string
    parentId?: string
    agentId?: string
    agentName?: string
    role?: string
    task: string
    status: string // running, completed, failed
    depth: number
    tokenCount?: number
    toolCalls?: number
}

type SpawnTreeGraphProps = {
    nodes: SpawnNode[]
    onNodeClick?: (nodeId: string) => void
    selectedNodeId?: string
}

const STATUS_STYLES: Record<string, { bg: string; border: string; dot: string }> = {
    running: { bg: 'bg-blue-50', border: 'border-blue-300', dot: 'bg-blue-500 animate-pulse' },
    completed: { bg: 'bg-emerald-50', border: 'border-emerald-300', dot: 'bg-emerald-500' },
    failed: { bg: 'bg-red-50', border: 'border-red-300', dot: 'bg-red-500' },
}

type TreeNode = SpawnNode & { children: TreeNode[] }

function buildTree(nodes: SpawnNode[]): TreeNode[] {
    const nodeMap = new Map<string, TreeNode>()
    const roots: TreeNode[] = []

    for (const node of nodes) {
        nodeMap.set(node.id, { ...node, children: [] })
    }

    for (const node of nodes) {
        const treeNode = nodeMap.get(node.id)!
        if (node.parentId && nodeMap.has(node.parentId)) {
            nodeMap.get(node.parentId)!.children.push(treeNode)
        } else {
            roots.push(treeNode)
        }
    }

    return roots
}

function TreeNodeComponent({
    node,
    onNodeClick,
    selectedNodeId,
}: {
    node: TreeNode
    onNodeClick?: (id: string) => void
    selectedNodeId?: string
}) {
    const styles = STATUS_STYLES[node.status] ?? STATUS_STYLES.running
    const isSelected = node.id === selectedNodeId

    return (
        <div className="relative">
            {/* Connector line */}
            <div className="flex items-start">
                <button
                    onClick={() => onNodeClick?.(node.id)}
                    className={cn(
                        'relative flex min-w-[200px] max-w-[280px] flex-col gap-1 rounded-lg border p-3 text-left transition-all hover:shadow-sm',
                        styles.bg,
                        styles.border,
                        isSelected && 'ring-2 ring-blue-500',
                    )}
                >
                    <div className="flex items-center gap-2">
                        <span className={cn('h-2 w-2 shrink-0 rounded-full', styles.dot)} />
                        <span className="truncate text-sm font-medium">
                            {node.role ?? node.agentName ?? 'Agent'}
                        </span>
                    </div>
                    <p className="line-clamp-2 text-xs text-muted-foreground">{node.task}</p>
                    <div className="flex items-center gap-3 text-xs text-muted-foreground">
                        {node.tokenCount !== undefined && (
                            <span className="flex items-center gap-0.5">
                                <Iconify.Icon icon="lucide:coins" className="h-3 w-3" />
                                {node.tokenCount.toLocaleString()}
                            </span>
                        )}
                        {node.toolCalls !== undefined && (
                            <span className="flex items-center gap-0.5">
                                <Iconify.Icon icon="lucide:wrench" className="h-3 w-3" />
                                {node.toolCalls}
                            </span>
                        )}
                    </div>
                </button>
            </div>

            {/* Children */}
            {node.children.length > 0 && (
                <div className="ml-8 mt-2 space-y-2 border-l-2 border-neutral-200 pl-4">
                    {node.children.map((child) => (
                        <TreeNodeComponent
                            key={child.id}
                            node={child}
                            onNodeClick={onNodeClick}
                            selectedNodeId={selectedNodeId}
                        />
                    ))}
                </div>
            )}
        </div>
    )
}

export function SpawnTreeGraph({ nodes, onNodeClick, selectedNodeId }: SpawnTreeGraphProps) {
    const tree = useMemo(() => buildTree(nodes), [nodes])

    if (nodes.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
                <Iconify.Icon icon="lucide:git-branch" className="mb-2 h-8 w-8 opacity-40" />
                <p className="text-sm">No spawn tree for this session</p>
            </div>
        )
    }

    return (
        <div className="space-y-3 p-4">
            {tree.map((root) => (
                <TreeNodeComponent
                    key={root.id}
                    node={root}
                    onNodeClick={onNodeClick}
                    selectedNodeId={selectedNodeId}
                />
            ))}
        </div>
    )
}
