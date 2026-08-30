import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
    ReactFlow,
    Background,
    BackgroundVariant,
    MiniMap,
    MarkerType,
    type ReactFlowInstance,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { ui } from '@everstack/ui'
import { useStudioStore } from '@/stores/studio-store'
import type { StudioNode, StudioEdge } from '../types'
import StudioNodeComponent from './studio-node'
import { StudioEdgeComponent } from './studio-edge'
import { useStudioDnd } from '../hooks/use-studio-dnd'
import { CanvasControls } from './canvas-controls'
import { NodeContextMenu, type NodeContextMenuState } from './node-context-menu'
import { HandleNodePicker } from './handle-node-picker'

// Context to share ReactFlow instance with palette
import { createContext, useContext } from 'react'
export const ReactFlowInstanceContext = createContext<React.RefObject<ReactFlowInstance<StudioNode, StudioEdge> | null> | null>(null)

export function StudioCanvas() {
    const isLight = ui.useChartMode() === 'light'
    const reactFlowWrapper = useRef<HTMLDivElement>(null)
    const reactFlowInstance = useRef<ReactFlowInstance<StudioNode, StudioEdge> | null>(null)
    const contextValue = useContext(ReactFlowInstanceContext)

    // Update parent context ref when instance changes
    const onInit = useCallback((instance: ReactFlowInstance<StudioNode, StudioEdge>) => {
        reactFlowInstance.current = instance
        if (contextValue) {
            contextValue.current = instance
        }
    }, [contextValue])

    const nodes = useStudioStore((s) => s.nodes)
    const edges = useStudioStore((s) => s.edges)
    const selectedNodeId = useStudioStore((s) => s.selectedNodeId)
    const onNodesChange = useStudioStore((s) => s.onNodesChange)
    const onEdgesChange = useStudioStore((s) => s.onEdgesChange)
    const onConnect = useStudioStore((s) => s.onConnect)
    const selectNode = useStudioStore((s) => s.selectNode)
    const setViewport = useStudioStore((s) => s.setViewport)
    const setHoveredEdgeId = useStudioStore((s) => s.setHoveredEdgeId)
    const closeHandlePicker = useStudioStore((s) => s.closeHandlePicker)

    const nodeTypes = useMemo(() => ({
        studioNode: StudioNodeComponent,
    }), [])

    const edgeTypes = useMemo(() => ({
        studioEdge: StudioEdgeComponent,
    }), [])

    // Sync node selection state with ReactFlow
    const nodesWithSelection = useMemo(() => {
        return nodes.map((node) => ({
            ...node,
            selected: node.id === selectedNodeId,
        })) as StudioNode[]
    }, [nodes, selectedNodeId])

    const { onDragOver, onDrop } = useStudioDnd(reactFlowInstance)

    // --- Canvas control states ---
    const [isLocked, setIsLocked] = useState(false)
    const [isSnapToGrid, setIsSnapToGrid] = useState(true)
    const [isMinimapPinned, setIsMinimapPinned] = useState(false)

    // Minimap auto-show on pan/zoom (when not pinned)
    const [minimapAutoVisible, setMinimapAutoVisible] = useState(false)
    const hideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    const minimapVisible = isMinimapPinned || minimapAutoVisible

    const onMoveStart = useCallback(() => {
        if (hideTimerRef.current) {
            clearTimeout(hideTimerRef.current)
            hideTimerRef.current = null
        }
        setMinimapAutoVisible(true)
    }, [])

    const onMoveEnd = useCallback(
        (_: unknown, viewport: { x: number; y: number; zoom: number }) => {
            setViewport(viewport)
            hideTimerRef.current = setTimeout(() => {
                setMinimapAutoVisible(false)
            }, 1500)
        },
        [setViewport]
    )

    useEffect(() => {
        return () => {
            if (hideTimerRef.current) clearTimeout(hideTimerRef.current)
        }
    }, [])

    // --- Node context menu ---
    const [nodeContextMenu, setNodeContextMenu] = useState<NodeContextMenuState | null>(null)

    const onNodeContextMenu = useCallback(
        (event: React.MouseEvent, node: StudioNode) => {
            event.preventDefault()
            setNodeContextMenu({ nodeId: node.id, x: event.clientX, y: event.clientY })
        },
        []
    )

    const closeNodeContextMenu = useCallback(() => {
        setNodeContextMenu(null)
    }, [])

    const onPaneClick = useCallback(() => {
        selectNode(null)
        setNodeContextMenu(null)
        closeHandlePicker()
    }, [selectNode, closeHandlePicker])

    const onNodeClick = useCallback(
        (_: unknown, { id }: { id: string }) => {
            selectNode(id)
        },
        [selectNode]
    )

    // --- Edge hover ---
    const onEdgeMouseEnter = useCallback(
        (_: React.MouseEvent, edge: StudioEdge) => {
            setHoveredEdgeId(edge.id)
        },
        [setHoveredEdgeId]
    )

    const onEdgeMouseLeave = useCallback(() => {
        setHoveredEdgeId(null)
    }, [setHoveredEdgeId])

    return (
        <div ref={reactFlowWrapper} className="relative h-full w-full">
            <ReactFlow
                nodes={nodesWithSelection}
                edges={edges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onInit={onInit}
                onMoveStart={onMoveStart}
                onMoveEnd={onMoveEnd}
                onPaneClick={onPaneClick}
                onNodeClick={onNodeClick}
                onNodeContextMenu={onNodeContextMenu}
                onEdgeMouseEnter={onEdgeMouseEnter}
                onEdgeMouseLeave={onEdgeMouseLeave}
                onDragOver={onDragOver}
                onDrop={onDrop}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                fitView
                fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
                defaultViewport={{ x: 0, y: 0, zoom: 1 }}
                panOnDrag={false}
                selectionOnDrag
                panOnScroll
                zoomOnScroll
                snapToGrid={isSnapToGrid}
                snapGrid={[16, 16]}
                nodesDraggable={!isLocked}
                nodesConnectable={!isLocked}
                elementsSelectable={!isLocked}
                deleteKeyCode={['Delete', 'Backspace']}
                className="studio-flow"
                proOptions={{ hideAttribution: true }}
                defaultEdgeOptions={{
                    type: 'studioEdge',
                    animated: true,
                    style: { stroke: isLight ? '#b0b0b0' : '#525252', strokeWidth: 1 },
                    markerEnd: {
                        type: MarkerType.ArrowClosed,
                        width: 8,
                        height: 8,
                        color: isLight ? '#b0b0b0' : '#525252',
                    },
                }}
            >
                <Background
                    variant={BackgroundVariant.Dots}
                    gap={20}
                    size={1}
                    color={isLight ? '#d4d4d4' : '#323232'}
                />
                <MiniMap
                    position="bottom-right"
                    pannable zoomable
                    className="!bg-brand-main-900 !border-brand-main-600"
                    maskColor={isLight ? 'rgba(255, 255, 255, 0.6)' : 'rgba(0, 0, 0, 0.6)'}
                    nodeColor={isLight ? '#a3a3a3' : '#525252'}
                    style={{
                        opacity: minimapVisible ? 1 : 0,
                        pointerEvents: minimapVisible ? 'auto' : 'none',
                        transition: 'opacity 0.3s ease-in-out',
                    }}
                />
            </ReactFlow>

            {/* Custom controls overlay */}
            <CanvasControls
                isLocked={isLocked}
                onToggleLock={() => setIsLocked((v) => !v)}
                isSnapToGrid={isSnapToGrid}
                onToggleSnapToGrid={() => setIsSnapToGrid((v) => !v)}
                isMinimapPinned={isMinimapPinned}
                onToggleMinimap={() => setIsMinimapPinned((v) => !v)}
            />

            {/* Node context menu */}
            <NodeContextMenu menu={nodeContextMenu} onClose={closeNodeContextMenu} />

            {/* Handle node picker */}
            <HandleNodePicker />
        </div>
    )
}
