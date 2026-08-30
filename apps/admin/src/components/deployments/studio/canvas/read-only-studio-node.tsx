import { memo, useMemo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Icon } from '@iconify/react'
import type { StudioNode, StudioNodeData } from '../types'
import { NODE_REGISTRY } from '../node-registry'

/**
 * Read-only variant of StudioNodeComponent for use outside the Studio context
 * (e.g. workflow preview panels, timeline cards). Same visual styling,
 * no store dependencies (useStudioStore, useExecutionStore, useVersionDiffContext).
 */
function ReadOnlyStudioNodeComponent({ data, selected }: NodeProps<StudioNode>) {
    const nodeData = data as StudioNodeData
    const meta = NODE_REGISTRY[nodeData.nodeType] ?? NODE_REGISTRY.start

    const sourceHandles = useMemo(
        () => meta.handles.filter((h) => h.type === 'source'),
        [meta.handles]
    )
    const targetHandles = useMemo(
        () => meta.handles.filter((h) => h.type === 'target'),
        [meta.handles]
    )

    return (
        <div className="relative w-[220px]">
            {/* Target handles (top) */}
            {targetHandles.map((h) => (
                <Handle
                    key={h.id}
                    type="target"
                    position={Position.Top}
                    id={h.id}
                    className="!w-3 !h-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
                    isConnectable={false}
                />
            ))}

            {/* Node body */}
            <div
                className={`rounded-lg border bg-brand-main-800 px-4 py-3 shadow-lg transition-all ${
                    selected
                        ? 'ring-3 ring-brand-secondary-500 border-transparent'
                        : 'border-brand-main-600'
                }`}
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
            </div>

            {/* Source handles (bottom) */}
            {sourceHandles.map((h, i) => {
                const style = sourceHandles.length > 1
                    ? { left: `${((i + 1) / (sourceHandles.length + 1)) * 100}%` }
                    : undefined
                return (
                    <Handle
                        key={h.id}
                        type="source"
                        position={Position.Bottom}
                        id={h.id}
                        className="!w-3 !h-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
                        style={style}
                        isConnectable={false}
                    >
                        {sourceHandles.length > 1 && h.label && (
                            <span className="absolute top-3 left-1/2 -translate-x-1/2 text-[10px] text-brand-main-400 whitespace-nowrap pointer-events-none">
                                {h.label}
                            </span>
                        )}
                    </Handle>
                )
            })}
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

export default memo(ReadOnlyStudioNodeComponent)
