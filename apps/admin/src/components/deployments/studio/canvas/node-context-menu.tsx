import { useCallback } from 'react'
import { Settings, Copy, Trash2 } from 'lucide-react'
import { ui } from '@everstack/ui'
import { useStudioStore } from '@/stores/studio-store'

const {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuShortcut,
} = ui

export interface NodeContextMenuState {
    nodeId: string
    x: number
    y: number
}

interface NodeContextMenuProps {
    menu: NodeContextMenuState | null
    onClose: () => void
}

export function NodeContextMenu({ menu, onClose }: NodeContextMenuProps) {
    const selectNode = useStudioStore((s) => s.selectNode)
    const duplicateNode = useStudioStore((s) => s.duplicateNode)
    const removeNode = useStudioStore((s) => s.removeNode)

    const handleSettings = useCallback(() => {
        if (menu) selectNode(menu.nodeId)
        onClose()
    }, [menu, selectNode, onClose])

    const handleDuplicate = useCallback(() => {
        if (menu) duplicateNode(menu.nodeId)
        onClose()
    }, [menu, duplicateNode, onClose])

    const handleDelete = useCallback(() => {
        if (menu) removeNode(menu.nodeId)
        onClose()
    }, [menu, removeNode, onClose])

    return (
        <DropdownMenu open={!!menu} onOpenChange={(open) => { if (!open) onClose() }}>
            <DropdownMenuContent
                className="w-48 bg-brand-main-600 border-brand-main-500 text-brand-main-100"
                style={{
                    position: 'fixed',
                    left: menu?.x ?? 0,
                    top: menu?.y ?? 0,
                }}
                side="bottom"
                align="start"
                sideOffset={0}
            >
                <DropdownMenuItem
                    onSelect={handleSettings}
                    className="text-brand-main-50 border border-transparent hover:border-brand-secondary-500/50 hover:bg-brand-secondary-500/15"
                >
                    <Settings className="h-4 w-4 text-brand-main-300" />
                    Settings
                </DropdownMenuItem>
                <DropdownMenuItem
                    onSelect={handleDuplicate}
                    className="text-brand-main-50 border border-transparent hover:border-brand-secondary-500/50  hover:bg-brand-secondary-500/15"
                >
                    <Copy className="h-4 w-4 text-brand-main-300" />
                    Duplicate
                </DropdownMenuItem>
                <DropdownMenuSeparator className="bg-brand-main-500" />
                <DropdownMenuItem
                    onSelect={handleDelete}
                    className="text-red-400 light:text-red-600 border border-transparent hover:border-red-500/10  hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600 flex items-center gap-2"
                >
                    <Trash2 className="h-4 w-4" />
                    Delete
                    <DropdownMenuShortcut className="text-brand-main-300">
                        ⌫
                    </DropdownMenuShortcut>
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    )
}
