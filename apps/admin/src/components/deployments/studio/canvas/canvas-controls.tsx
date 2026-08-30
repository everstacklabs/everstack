import { useReactFlow } from '@xyflow/react'
import { Icon } from '@iconify/react'
import { useStudioStore } from '@/stores/studio-store'

interface CanvasControlsProps {
    isLocked: boolean
    onToggleLock: () => void
    isSnapToGrid: boolean
    onToggleSnapToGrid: () => void
    isMinimapPinned: boolean
    onToggleMinimap: () => void
}

export function CanvasControls({
    isLocked,
    onToggleLock,
    isSnapToGrid,
    onToggleSnapToGrid,
    isMinimapPinned,
    onToggleMinimap,
}: CanvasControlsProps) {
    const { zoomIn, zoomOut, fitView } = useReactFlow()
    const zoom = useStudioStore((s) => s.viewport.zoom)
    const zoomPercentage = Math.round(zoom * 100)

    return (
        <div className="absolute bottom-4 left-4 z-10 flex items-center overflow-hidden rounded-lg border border-brand-main-600 bg-brand-main-800/90 shadow-lg backdrop-blur-sm">
            {/* Zoom out */}
            <ControlButton
                icon="lucide:minus"
                title="Zoom out"
                onClick={() => zoomOut()}
            />

            {/* Zoom percentage */}
            <div className="flex h-8 w-12 items-center justify-center border-x border-brand-main-600/50 text-xs font-medium tabular-nums text-brand-main-200">
                {zoomPercentage}%
            </div>

            {/* Zoom in */}
            <ControlButton
                icon="lucide:plus"
                title="Zoom in"
                onClick={() => zoomIn()}
            />

            <Separator />

            {/* Fit to view */}
            <ControlButton
                icon="lucide:maximize-2"
                title="Fit to view"
                onClick={() => fitView({ padding: 0.2 })}
            />

            <Separator />

            {/* Lock / unlock interactivity */}
            <ControlButton
                icon={isLocked ? 'lucide:lock' : 'lucide:unlock'}
                title={isLocked ? 'Unlock canvas' : 'Lock canvas'}
                onClick={onToggleLock}
                isActive={isLocked}
            />

            {/* Snap to grid */}
            <ControlButton
                icon="lucide:grid-3x3"
                title={isSnapToGrid ? 'Disable snap to grid' : 'Enable snap to grid'}
                onClick={onToggleSnapToGrid}
                isActive={isSnapToGrid}
            />

            {/* Minimap toggle */}
            <ControlButton
                icon="lucide:map"
                title={isMinimapPinned ? 'Auto-hide minimap' : 'Pin minimap'}
                onClick={onToggleMinimap}
                isActive={isMinimapPinned}
            />
        </div>
    )
}

function ControlButton({
    icon,
    title,
    onClick,
    isActive,
}: {
    icon: string
    title: string
    onClick: () => void
    isActive?: boolean
}) {
    return (
        <button
            onClick={onClick}
            title={title}
            className={`flex h-8 w-8 items-center justify-center transition-colors ${isActive
                    ? 'bg-brand-main-700 text-brand-secondary-400'
                    : 'text-brand-main-300 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50'
                } light:hover:text-brand-main-50`}
        >
            <Icon icon={icon} className="h-3.5 w-3.5" />
        </button>
    )
}

function Separator() {
    return <div className="h-5 w-px bg-brand-main-600" />
}
