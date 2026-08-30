import { useCallback } from 'react'
import {
    BaseEdge,
    EdgeLabelRenderer,
    getSmoothStepPath,
    type EdgeProps,
} from '@xyflow/react'
import { Plus, X } from 'lucide-react'
import { ui } from '@everstack/ui'
import { useStudioStore } from '@/stores/studio-store'
import { useVersionDiffContext } from './version-diff-context'

export function StudioEdgeComponent({
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    style,
    markerEnd,
}: EdgeProps) {
    const hoveredEdgeId = useStudioStore((s) => s.hoveredEdgeId)
    const removeEdge = useStudioStore((s) => s.removeEdge)
    const setPendingEdgeInsert = useStudioStore((s) => s.setPendingEdgeInsert)
    const versionDiff = useVersionDiffContext()

    const isLight = ui.useChartMode() === 'light'
    const isHovered = hoveredEdgeId === id
    const isDiffHighlighted = versionDiff.activeVersion !== null && versionDiff.edgeIds.has(id)

    const [edgePath, labelX, labelY] = getSmoothStepPath({
        sourceX,
        sourceY,
        targetX,
        targetY,
        sourcePosition,
        targetPosition,
    })

    const handleDelete = useCallback(
        (e: React.MouseEvent) => {
            e.stopPropagation()
            removeEdge(id)
        },
        [id, removeEdge]
    )

    const handleAdd = useCallback(
        (e: React.MouseEvent) => {
            e.stopPropagation()
            setPendingEdgeInsert(id)
        },
        [id, setPendingEdgeInsert]
    )

    return (
        <>
            {/* Visible edge */}
            <BaseEdge
                path={edgePath}
                markerEnd={markerEnd}
                style={{
                    ...style,
                    stroke: isDiffHighlighted
                        ? '#fbbf24'
                        : isHovered
                          ? isLight ? '#525252' : '#737373'
                          : isLight ? '#a3a3a3' : '#525252',
                    strokeWidth: isDiffHighlighted ? 2.5 : 2,
                    strokeDasharray: isHovered ? 'none' : undefined,
                    animation: isHovered ? 'none' : undefined,
                    filter: isDiffHighlighted ? 'drop-shadow(0 0 4px rgba(251, 191, 36, 0.5))' : undefined,
                }}
            />

            {/* Hover buttons rendered in HTML layer */}
            {isHovered && (
                <EdgeLabelRenderer>
                    <div
                        className="nodrag nopan pointer-events-auto absolute z-50"
                        style={{
                            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
                        }}
                    >
                        <div className="flex items-center justify-center gap-1">
                            <button
                                onClick={handleAdd}
                                className="flex h-4 w-4 items-center justify-center rounded bg-brand-main-700 border border-brand-main-500 text-brand-main-200 hover:bg-brand-secondary-600 hover:text-white hover:border-brand-secondary-500 transition-colors"
                                title="Add node"
                            >
                                <Plus size={8} />
                            </button>
                            <button
                                onClick={handleDelete}
                                className="flex h-4 w-4 items-center justify-center rounded bg-brand-main-700 border border-brand-main-500 text-brand-main-200 hover:bg-red-600 hover:text-white hover:border-red-500 transition-colors"
                                title="Delete edge"
                            >
                                <X size={8} />
                            </button>
                        </div>
                    </div>
                </EdgeLabelRenderer>
            )}
        </>
    )
}
