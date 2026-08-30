import { useCallback, type MutableRefObject, type DragEvent } from 'react'
import type { ReactFlowInstance } from '@xyflow/react'
import { useStudioStore } from '@/stores/studio-store'
import type { StudioNodeType, StudioNode, StudioEdge } from '../types'

export function useStudioDnd(reactFlowInstance: MutableRefObject<ReactFlowInstance<StudioNode, StudioEdge> | null>) {
    const addNode = useStudioStore((s) => s.addNode)

    const onDragOver = useCallback((event: DragEvent) => {
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
    }, [])

    const onDrop = useCallback(
        (event: DragEvent) => {
            event.preventDefault()

            const nodeType = event.dataTransfer.getData('application/studio-node-type') as StudioNodeType
            if (!nodeType) return

            const instance = reactFlowInstance.current
            if (!instance) return

            const position = instance.screenToFlowPosition({
                x: event.clientX,
                y: event.clientY,
            })

            addNode(nodeType, position)
        },
        [addNode, reactFlowInstance]
    )

    return { onDragOver, onDrop }
}
