import { memo, useCallback, useMemo, useRef, type ComponentProps } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Icon } from '@iconify/react'
import type { StudioNode, StudioNodeData } from '../types'
import { NODE_REGISTRY } from '../node-registry'
import { useStudioStore } from '@/stores/studio-store'
import { useExecutionStore } from '@/stores/execution-store'
import { useVersionDiffContext } from './version-diff-context'

type HandleStyle = ComponentProps<typeof Handle>['style']

function ClickableHandle({ nodeId, handleDef, style, children }: {
    nodeId: string
    handleDef: { id: string; label?: string }
    style?: HandleStyle
    children?: React.ReactNode
}) {
    const openHandlePicker = useStudioStore((s) => s.openHandlePicker)
    const mouseDownRef = useRef<{ x: number; y: number; time: number } | null>(null)

    const onMouseDown = useCallback((e: React.MouseEvent) => {
        mouseDownRef.current = { x: e.clientX, y: e.clientY, time: Date.now() }
    }, [])

    const onMouseUp = useCallback((e: React.MouseEvent) => {
        const start = mouseDownRef.current
        if (!start) return
        mouseDownRef.current = null

        const dx = Math.abs(e.clientX - start.x)
        const dy = Math.abs(e.clientY - start.y)
        const elapsed = Date.now() - start.time

        if (dx < 5 && dy < 5 && elapsed < 200) {
            e.stopPropagation()
            openHandlePicker(nodeId, handleDef.id, { x: e.clientX, y: e.clientY })
        }
    }, [nodeId, handleDef.id, openHandlePicker])

    return (
        <Handle
            type="source"
            position={Position.Bottom}
            id={handleDef.id}
            className="!w-3 !h-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
            style={style}
            onMouseDown={onMouseDown}
            onMouseUp={onMouseUp}
        >
            {children}
        </Handle>
    )
}

function StudioNodeComponent({ id, data, selected }: NodeProps<StudioNode>) {
    const nodeData = data as StudioNodeData
    const meta = NODE_REGISTRY[nodeData.nodeType] ?? NODE_REGISTRY.start
    const selectNode = useStudioStore((s) => s.selectNode)

    // Execution state
    const activeNodeId = useExecutionStore((s) => s.activeNodeId)
    const completedNodeIds = useExecutionStore((s) => s.completedNodeIds)
    const errorNodeId = useExecutionStore((s) => s.errorNodeId)

    // Version diff state (from context — works in both main canvas and preview dialog)
    const versionDiff = useVersionDiffContext()
    const isDiffHighlighted = versionDiff.activeVersion !== null && versionDiff.nodeIds.has(id)

    const isActive = activeNodeId === id
    const isCompleted = completedNodeIds.includes(id)
    const isError = errorNodeId === id

    const sourceHandles = useMemo(
        () => meta.handles.filter((h) => h.type === 'source'),
        [meta.handles]
    )
    const targetHandles = useMemo(
        () => meta.handles.filter((h) => h.type === 'target'),
        [meta.handles]
    )

    const handleClick = () => {
        selectNode(id)
    }

    // Build execution overlay classes
    const executionClasses = isActive
        ? 'ring-2 ring-blue-500 animate-pulse border-blue-500/50'
        : isError
            ? 'ring-2 ring-red-500 border-red-500/50'
            : isCompleted
                ? 'border-emerald-500/50'
                : ''

    // Build version diff overlay classes
    const diffClasses = isDiffHighlighted
        ? 'ring ring-brand-secondary-400/80 border-brand-secondary-400/60 shadow-[0_0_12px_rgba(251,191,36,0.3)]'
        : ''

    return (
        <div
            onClick={handleClick}
            className="relative w-[220px] hover:cursor-pointer"
        >
            {/* Target handles (top) */}
            {targetHandles.map((h) => (
                <Handle
                    key={h.id}
                    type="target"
                    position={Position.Top}
                    id={h.id}
                    className="!w-3 !h-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
                />
            ))}

            {/* Node body */}
            <div
                className={`rounded-lg border bg-brand-main-800 px-4 py-3 shadow-lg transition-all ${selected
                    ? 'ring-3 ring-brand-secondary-500 border-transparent'
                    : 'border-brand-main-600 hover:border-brand-main-500'
                    } ${executionClasses} ${diffClasses}`}
            >
                {/* Header */}
                <div className="flex items-center gap-2">
                    <div
                        className="flex h-7 w-7 items-center justify-center rounded"
                        style={{ backgroundColor: `${meta.color}20`, color: meta.color }}
                    >
                        <Icon icon={meta.icon} className="h-4 w-4" />
                    </div>
                    <div className="flex-1 min-w-0">
                        <div className="text-sm font-medium text-white light:text-brand-main-50 truncate">
                            {nodeData.label}
                        </div>
                        <div className="text-xs text-brand-main-400">
                            {meta.label}
                        </div>
                    </div>
                    {nodeData.isConfigured && (
                        <div className="h-2 w-2 rounded-full bg-emerald-500" title="Configured" />
                    )}
                </div>

                {/* Config summary */}
                <ConfigSummary nodeType={nodeData.nodeType} config={nodeData.config as Record<string, any>} />

                {/* Version diff indicator */}
                {isDiffHighlighted && (
                    <div className="mt-2 flex items-center gap-1 text-[10px] text-brand-secondary-300 border-t border-brand-secondary-400/20 pt-1.5">
                        <Icon icon="lucide:history" className="h-3 w-3" />
                        Changed in v{versionDiff.activeVersion}
                    </div>
                )}
            </div>

            {/* Source handles (bottom) */}
            {sourceHandles.length === 1 ? (
                <ClickableHandle nodeId={id} handleDef={sourceHandles[0]} />
            ) : (
                sourceHandles.map((h, i) => {
                    const offset = ((i + 1) / (sourceHandles.length + 1)) * 100
                    return (
                        <ClickableHandle
                            key={h.id}
                            nodeId={id}
                            handleDef={h}
                            style={{ left: `${offset}%` }}
                        >
                            <span className="absolute top-3 left-1/2 -translate-x-1/2 text-[10px] text-brand-main-400 whitespace-nowrap pointer-events-none">
                                {h.label}
                            </span>
                        </ClickableHandle>
                    )
                })
            )}
        </div>
    )
}

function ConfigSummary({ nodeType, config }: { nodeType: string; config: Record<string, any> }) {
    let summary = ''

    switch (nodeType) {
        case 'start':
            summary = config.systemPrompt ? 'System prompt' : 'Entry point'
            break
        case 'auth': {
            const mode = config.mode ?? 'api_key'
            const modeLabels: Record<string, string> = { api_key: 'API Key', jwt: 'JWT', webhook: 'Webhook', none: 'None' }
            summary = modeLabels[mode] ?? mode
            break
        }
        case 'rateLimiter':
            summary = 'Provider-based'
            break
        case 'cache':
            summary = `${config.type ?? 'semantic'}, TTL: ${config.ttl ?? 3600}s`
            break
        case 'provider':
            if (config.providerType || config.model) {
                summary = `${config.providerType ?? ''}${config.model ? ` / ${config.model}` : ''}`
            }
            break
        case 'function':
            summary = config.functionName ? String(config.functionName) : ''
            break
        case 'loadBalancer': {
            const strategy = config.strategy ?? 'router'
            const strategyLabels: Record<string, string> = { router: 'Router', round_robin: 'Round Robin', weighted: 'Weighted', random: 'Random' }
            summary = strategyLabels[strategy] ?? strategy
            break
        }
        case 'httpRequest': {
            const method = config.method ?? 'GET'
            let host = ''
            if (config.url) {
                try { host = new URL(config.url).hostname } catch { host = config.url }
            }
            summary = host ? `${method} ${host}` : method
            break
        }
        case 'webhook': {
            const wMethod = config.method ?? 'POST'
            const wUrl = config.url ? (config.url.length > 30 ? `${config.url.slice(0, 30)}...` : config.url) : ''
            summary = wUrl ? `${wMethod} ${wUrl}` : wMethod
            break
        }
        case 'ifElse': {
            const cType = config.conditionType ?? 'expression'
            const cExpr = config.conditionExpression
                ? (config.conditionExpression.length > 30 ? `${config.conditionExpression.slice(0, 30)}...` : config.conditionExpression)
                : ''
            summary = cExpr ? `${cType}: ${cExpr}` : cType
            break
        }
        case 'response':
            summary = config.streaming ? 'Streaming' : 'Batch'
            break
        default:
            break
    }

    if (!summary) return null

    return (
        <div className="mt-2 text-xs text-brand-main-300 truncate border-t border-brand-main-700 pt-2">
            {summary}
        </div>
    )
}

export default memo(StudioNodeComponent)
