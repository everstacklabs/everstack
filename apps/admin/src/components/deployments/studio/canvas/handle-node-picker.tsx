import { useEffect, useRef } from 'react'
import { Icon } from '@iconify/react'
import { useStudioStore } from '@/stores/studio-store'
import { NODE_CATEGORIES } from '../node-registry'
import type { StudioNodeData, StudioNodeType } from '../types'

export function HandleNodePicker() {
    const pickerState = useStudioStore((s) => s.handlePickerState)
    const closeHandlePicker = useStudioStore((s) => s.closeHandlePicker)
    const addNodeFromHandle = useStudioStore((s) => s.addNodeFromHandle)
    const nodes = useStudioStore((s) => s.nodes)
    const ref = useRef<HTMLDivElement>(null)

    // Close on click-outside
    useEffect(() => {
        if (!pickerState) return
        const handler = (e: MouseEvent) => {
            if (ref.current && !ref.current.contains(e.target as Node)) {
                closeHandlePicker()
            }
        }
        document.addEventListener('mousedown', handler)
        return () => document.removeEventListener('mousedown', handler)
    }, [pickerState, closeHandlePicker])

    if (!pickerState) return null

    // Count existing instances per type
    const instanceCounts = new Map<string, number>()
    for (const node of nodes) {
        const t = (node.data as StudioNodeData).nodeType
        instanceCounts.set(t, (instanceCounts.get(t) ?? 0) + 1)
    }

    const handleSelect = (type: StudioNodeType) => {
        addNodeFromHandle(type)
    }

    return (
        <div
            ref={ref}
            className="fixed z-50 w-56 rounded-lg border border-brand-main-600 bg-brand-main-800 shadow-xl py-1 overflow-y-auto max-h-80"
            style={{
                left: pickerState.screenPosition.x,
                top: pickerState.screenPosition.y + 8,
            }}
        >
            {NODE_CATEGORIES.map((cat) => (
                <div key={cat.category}>
                    <div className="px-3 py-1.5 text-[10px] uppercase tracking-wider text-brand-main-400 font-semibold">
                        {cat.label}
                    </div>
                    {cat.nodes.map((meta) => {
                        const count = instanceCounts.get(meta.type) ?? 0
                        const disabled = meta.maxInstances != null && count >= meta.maxInstances

                        return (
                            <button
                                key={meta.type}
                                type="button"
                                disabled={disabled}
                                onClick={() => handleSelect(meta.type)}
                                className={`w-full flex items-center gap-2 px-3 py-1.5 text-sm text-left transition-colors ${
                                    disabled
                                        ? 'text-brand-main-500 cursor-not-allowed'
                                        : 'text-brand-main-200 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50'
                                } light:hover:text-brand-main-50`}
                            >
                                <span
                                    className="flex h-5 w-5 items-center justify-center rounded"
                                    style={{
                                        backgroundColor: disabled ? undefined : `${meta.color}20`,
                                        color: disabled ? undefined : meta.color,
                                    }}
                                >
                                    <Icon icon={meta.icon} className="h-3.5 w-3.5" />
                                </span>
                                <span>{meta.label}</span>
                            </button>
                        )
                    })}
                </div>
            ))}
        </div>
    )
}
