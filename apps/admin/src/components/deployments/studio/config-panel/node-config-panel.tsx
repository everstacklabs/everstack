import { AnimatePresence, motion } from 'framer-motion'
import { Icon } from '@iconify/react'
import { Input } from '@everstack/ui/components'
import { useStudioStore } from '@/stores/studio-store'
import type { StudioNodeData, NodeConfig } from '../types'
import { NODE_REGISTRY } from '../node-registry'
import { ConfigFormForType } from './config-forms'

export function NodeConfigPanel() {
    const selectedNodeId = useStudioStore((s) => s.selectedNodeId)
    const isOpen = useStudioStore((s) => s.isConfigPanelOpen)
    const nodes = useStudioStore((s) => s.nodes)
    const selectNode = useStudioStore((s) => s.selectNode)
    const updateNodeConfig = useStudioStore((s) => s.updateNodeConfig)
    const updateNodeLabel = useStudioStore((s) => s.updateNodeLabel)
    const removeNode = useStudioStore((s) => s.removeNode)

    const selectedNode = selectedNodeId
        ? nodes.find((n) => n.id === selectedNodeId)
        : null
    const nodeData = selectedNode?.data as StudioNodeData | undefined
    const meta = nodeData ? NODE_REGISTRY[nodeData.nodeType] : null

    const handleConfigChange = (config: NodeConfig) => {
        if (selectedNodeId) {
            updateNodeConfig(selectedNodeId, config)
        }
    }

    const handleLabelChange = (label: string) => {
        if (selectedNodeId) {
            updateNodeLabel(selectedNodeId, label)
        }
    }

    return (
        <AnimatePresence>
            {isOpen && selectedNode && nodeData && meta && (
                <motion.div
                    initial={{ x: 320, opacity: 0 }}
                    animate={{ x: 0, opacity: 1 }}
                    exit={{ x: 320, opacity: 0 }}
                    transition={{ type: 'spring', damping: 25, stiffness: 300 }}
                    className="absolute right-0 top-0 bottom-0 w-[320px] border-l border-brand-main-700 bg-brand-main-900/95 backdrop-blur-sm overflow-y-auto z-10"
                >
                    {/* Header */}
                    <div className="flex items-center justify-between border-b border-brand-main-700 px-4 py-3">
                        <div className="flex items-center gap-2">
                            <div
                                className="flex h-6 w-6 items-center justify-center rounded"
                                style={{ backgroundColor: `${meta.color}20`, color: meta.color }}
                            >
                                <Icon icon={meta.icon} className="h-3.5 w-3.5" />
                            </div>
                            <span className="text-sm font-medium text-white light:text-brand-main-50">{meta.label}</span>
                        </div>
                        <div className="flex items-center gap-1">
                            <button
                                onClick={() => selectedNodeId && removeNode(selectedNodeId)}
                                className="rounded p-1 text-brand-main-400 hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                                title="Delete node"
                            >
                                <Icon icon="lucide:trash-2" className="h-4 w-4" />
                            </button>
                            <button
                                onClick={() => selectNode(null)}
                                className="rounded p-1 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
                            >
                                <Icon icon="lucide:x" className="h-4 w-4" />
                            </button>
                        </div>
                    </div>

                    {/* Node label */}
                    <div className="border-b border-brand-main-700 px-4 py-3">
                        <label className="text-xs text-brand-main-400 mb-1 block">Label</label>
                        <Input
                            value={nodeData.label}
                            onChange={(e) => handleLabelChange(e.target.value)}
                            className="h-8 bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-sm"
                        />
                    </div>

                    {/* Config form */}
                    <div className="px-4 py-4">
                        <ConfigFormForType
                            nodeType={nodeData.nodeType}
                            config={nodeData.config}
                            onChange={handleConfigChange}
                        />
                    </div>
                </motion.div>
            )}
        </AnimatePresence>
    )
}
