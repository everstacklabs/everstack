import { ReactFlowProvider } from '@xyflow/react'
import { NodePalette } from '../palette/node-palette'
import { StudioCanvas, ReactFlowInstanceContext } from '../canvas/studio-canvas'
import { NodeConfigPanel } from '../config-panel/node-config-panel'
import { useRef } from 'react'
import type { ReactFlowInstance } from '@xyflow/react'
import type { StudioNode, StudioEdge } from '../types'

export function StudioContent() {
    const reactFlowInstance = useRef<ReactFlowInstance<StudioNode, StudioEdge> | null>(null)

    return (
        <ReactFlowProvider>
            <ReactFlowInstanceContext.Provider value={reactFlowInstance}>
                <div className="flex flex-1 overflow-hidden">
                    <NodePalette />
                    <div className="relative flex-1">
                        <StudioCanvas />
                        <NodeConfigPanel />
                    </div>
                </div>
            </ReactFlowInstanceContext.Provider>
        </ReactFlowProvider>
    )
}
