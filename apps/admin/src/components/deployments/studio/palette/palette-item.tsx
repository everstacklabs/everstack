import { Icon } from '@iconify/react'
import type { NodeTypeMeta } from '../types'
import { useCallback, useContext } from 'react'
import { useStudioStore } from '@/stores/studio-store'
import { ReactFlowInstanceContext } from '../canvas/studio-canvas'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import type { FeatureKeyType } from '@/config/features'
import { toast } from '@everstack/ui/components'

interface PaletteItemProps {
    meta: NodeTypeMeta
}

export function PaletteItem({ meta }: PaletteItemProps) {
    const addNode = useStudioStore((s) => s.addNode)
    const insertNodeOnEdge = useStudioStore((s) => s.insertNodeOnEdge)
    const pendingEdgeInsertId = useStudioStore((s) => s.pendingEdgeInsertId)
    const reactFlowInstanceRef = useContext(ReactFlowInstanceContext)
    const gate = useFeatureGate(meta.featureKey as FeatureKeyType ?? '')
    const isGated = meta.requiredTier && gate.isBlocked

    const handleClick = useCallback(() => {
        if (isGated) {
            toast.error(`${meta.label} requires ${meta.requiredTier} plan`)
            return
        }

        // If there's a pending edge insert, insert on that edge instead
        if (pendingEdgeInsertId) {
            insertNodeOnEdge(pendingEdgeInsertId, meta.type)
            return
        }

        const instance = reactFlowInstanceRef?.current

        if (instance) {
            // Find the canvas container to calculate its visible center
            const el = document.querySelector('.studio-flow') as HTMLElement | null
            const rect = el?.getBoundingClientRect()
            const centerX = rect ? rect.left + rect.width / 2 : window.innerWidth / 2
            const centerY = rect ? rect.top + rect.height / 2 : window.innerHeight / 2

            // Convert screen center to flow coordinates using the actual viewport
            const position = instance.screenToFlowPosition({ x: centerX, y: centerY })
            addNode(meta.type, position)
        } else {
            // Fallback: add at default position if instance not available
            addNode(meta.type, { x: 250, y: 250 })
        }
    }, [meta.type, meta.label, meta.requiredTier, isGated, addNode, insertNodeOnEdge, pendingEdgeInsertId, reactFlowInstanceRef])

    const handleDragStart = useCallback((event: React.DragEvent) => {
        if (isGated) {
            event.preventDefault()
            return
        }
        event.dataTransfer.setData('application/studio-node-type', meta.type)
        event.dataTransfer.effectAllowed = 'move'
    }, [meta.type, isGated])

    return (
        <div
            draggable={!isGated}
            onDragStart={handleDragStart}
            onClick={handleClick}
            className={`flex items-center gap-2 px-4 py-1.5 transition-colors ${isGated ? 'cursor-not-allowed opacity-60 hover:bg-brand-main-900' : 'cursor-pointer hover:bg-brand-main-800'}`}
        >
            <div
                className="flex h-5 w-5 items-center justify-center rounded shrink-0"
                style={{ backgroundColor: `${meta.color}20`, color: meta.color }}
            >
                <Icon icon={meta.icon} className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm text-brand-main-200 truncate flex-1">{meta.label}</span>
            {meta.requiredTier && isGated && (
                <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-amber-500/15 text-amber-400 light:text-amber-700 border border-amber-500/30">
                    {meta.requiredTier}
                </span>
            )}
        </div>
    )
}
