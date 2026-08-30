import { useEffect } from 'react'
import { useStudioStore } from '@/stores/studio-store'

export function useStudioKeyboard() {
    const undo = useStudioStore((s) => s.undo)
    const redo = useStudioStore((s) => s.redo)
    const removeNode = useStudioStore((s) => s.removeNode)
    const selectNode = useStudioStore((s) => s.selectNode)

    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            // Don't intercept when typing in inputs
            const target = e.target as HTMLElement
            if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
                return
            }

            // Ctrl/Cmd+Z = Undo
            if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
                e.preventDefault()
                undo()
                return
            }

            // Ctrl/Cmd+Shift+Z or Ctrl/Cmd+Y = Redo
            if ((e.ctrlKey || e.metaKey) && (e.key === 'Z' || e.key === 'y') && (e.shiftKey || e.key === 'y')) {
                e.preventDefault()
                redo()
                return
            }

            // Ctrl/Cmd+S = Suppress browser save dialog (autosave handles saving)
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault()
                return
            }

            // Escape = Deselect
            if (e.key === 'Escape') {
                selectNode(null)
                return
            }

            // Delete/Backspace = Remove selected node
            if (e.key === 'Delete' || e.key === 'Backspace') {
                const selectedId = useStudioStore.getState().selectedNodeId
                if (selectedId) {
                    e.preventDefault()
                    removeNode(selectedId)
                }
            }
        }

        window.addEventListener('keydown', handler)
        return () => window.removeEventListener('keydown', handler)
    }, [undo, redo, removeNode, selectNode])
}
