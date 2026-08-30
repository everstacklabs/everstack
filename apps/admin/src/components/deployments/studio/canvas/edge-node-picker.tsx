import { useEffect, useRef } from 'react'
import { Icon } from '@iconify/react'
import { NODE_CATEGORIES } from '../node-registry'
import { useStudioStore } from '@/stores/studio-store'
import type { StudioNodeData, StudioNodeType } from '../types'

interface EdgeNodePickerProps {
    edgeId: string
    onClose: () => void
}

export function EdgeNodePicker({ edgeId, onClose }: EdgeNodePickerProps) {
    const ref = useRef<HTMLDivElement>(null)
    const insertNodeOnEdge = useStudioStore((s) => s.insertNodeOnEdge)
    const nodes = useStudioStore((s) => s.nodes)

    // Click-outside detection
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (ref.current && !ref.current.contains(e.target as Node)) {
                onClose()
            }
        }
        document.addEventListener('mousedown', handler)
        return () => document.removeEventListener('mousedown', handler)
    }, [onClose])

    const getNodeCount = (type: StudioNodeType) =>
        nodes.filter((n) => (n.data as StudioNodeData).nodeType === type).length

    const handleSelect = (type: StudioNodeType) => {
        insertNodeOnEdge(edgeId, type)
        onClose()
    }

    return (
        <div
            ref={ref}
            className="rounded-lg border border-brand-main-600 bg-brand-main-800 shadow-xl max-h-[360px] overflow-y-auto"
            onWheel={(e) => e.stopPropagation()}
        >
            {NODE_CATEGORIES.map((cat) => (
                <div key={cat.category}>
                    <div className="sticky top-0 bg-brand-main-800 px-3 py-1.5 text-[10px] font-medium text-brand-main-400 uppercase tracking-wider border-b border-brand-main-700">
                        {cat.label}
                    </div>
                    {cat.nodes.map((meta) => {
                        const count = getNodeCount(meta.type)
                        const disabled = !!(meta.maxInstances && count >= meta.maxInstances)
                        return (
                            <button
                                key={meta.type}
                                onClick={() => !disabled && handleSelect(meta.type)}
                                disabled={disabled}
                                className={`flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors ${disabled
                                        ? 'opacity-40 cursor-not-allowed'
                                        : 'hover:bg-brand-main-700 cursor-pointer'
                                    }`}
                            >
                                <div
                                    className="flex h-5 w-5 items-center justify-center rounded shrink-0"
                                    style={{
                                        backgroundColor: `${meta.color}20`,
                                        color: meta.color,
                                    }}
                                >
                                    <Icon icon={meta.icon} className="h-3 w-3" />
                                </div>
                                <span className="text-xs text-brand-main-200 truncate">
                                    {meta.label}
                                </span>
                            </button>
                        )
                    })}
                </div>
            ))}
        </div>
    )
}
