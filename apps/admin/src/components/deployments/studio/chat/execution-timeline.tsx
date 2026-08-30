import { Icon } from '@iconify/react'
import { useExecutionStore, type ExecutionEvent } from '@/stores/execution-store'
import { NODE_REGISTRY } from '../node-registry'
import type { StudioNodeType } from '../types'

interface ExecutionTimelineProps {
    staticEvents?: ExecutionEvent[]
}

export function ExecutionTimeline({ staticEvents }: ExecutionTimelineProps) {
    const liveEvents = useExecutionStore((s) => s.events)
    const activeNodeId = useExecutionStore((s) => s.activeNodeId)

    const events = staticEvents ?? liveEvents
    const isStatic = !!staticEvents

    // Group events by node (deduplicate, show latest status per node)
    const nodeEvents = getNodeEvents(events)

    if (nodeEvents.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center py-10">
                <div className="relative mb-5">
                    <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-lg" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
                        <Icon icon="lucide:activity" className="size-6 text-brand-secondary-400" />
                    </div>
                </div>
                <p className="text-sm text-white/50 light:text-black/50">No execution events yet</p>
            </div>
        )
    }

    return (
        <div className="flex flex-col gap-1 py-2">
            {nodeEvents.map((nodeEvt, idx) => {
                const meta = NODE_REGISTRY[nodeEvt.nodeType as StudioNodeType]
                const isActive = !isStatic && nodeEvt.nodeId === activeNodeId
                const isCompleted = nodeEvt.status === 'completed'
                const isError = nodeEvt.status === 'error'

                return (
                    <div key={`${nodeEvt.nodeId}-${idx}`} className="flex items-center gap-2 px-3 py-1.5">
                        {/* Status indicator */}
                        <div className="flex-shrink-0">
                            {isActive && (
                                <div className="h-5 w-5 rounded-full border-2 border-blue-500 animate-pulse flex items-center justify-center">
                                    <div className="h-2 w-2 rounded-full bg-blue-500" />
                                </div>
                            )}
                            {isCompleted && (
                                <div className="h-5 w-5 rounded-full bg-emerald-500/20 flex items-center justify-center">
                                    <Icon icon="lucide:check" className="h-3 w-3 text-emerald-400 light:text-emerald-600" />
                                </div>
                            )}
                            {isError && (
                                <div className="h-5 w-5 rounded-full bg-red-500/20 flex items-center justify-center">
                                    <Icon icon="lucide:x" className="h-3 w-3 text-red-400 light:text-red-600" />
                                </div>
                            )}
                            {!isActive && !isCompleted && !isError && (
                                <div className="h-5 w-5 rounded-full border border-brand-main-600 flex items-center justify-center">
                                    <div className="h-2 w-2 rounded-full bg-brand-main-500" />
                                </div>
                            )}
                        </div>

                        {/* Node icon + label */}
                        <div
                            className="flex h-5 w-5 items-center justify-center rounded flex-shrink-0"
                            style={{
                                backgroundColor: meta ? `${meta.color}20` : '#33333320',
                                color: meta?.color ?? '#999',
                            }}
                        >
                            <Icon
                                icon={meta?.icon ?? 'lucide:circle'}
                                className="h-3 w-3"
                            />
                        </div>

                        <span className="text-xs text-brand-main-200 truncate flex-1">
                            {nodeEvt.nodeLabel || nodeEvt.nodeType}
                        </span>

                        {/* Duration */}
                        {nodeEvt.durationMs !== undefined && nodeEvt.durationMs > 0 && (
                            <span className="text-[10px] text-brand-main-400 tabular-nums flex-shrink-0">
                                {nodeEvt.durationMs}ms
                            </span>
                        )}

                        {/* Error message */}
                        {isError && nodeEvt.error && (
                            <span className="text-[10px] text-red-400 light:text-red-600 truncate max-w-[120px]" title={nodeEvt.error}>
                                {nodeEvt.error}
                            </span>
                        )}
                    </div>
                )
            })}
        </div>
    )
}

interface NodeEventSummary {
    nodeId: string
    nodeType: string
    nodeLabel: string
    status: 'started' | 'completed' | 'error'
    durationMs?: number
    error?: string
}

function getNodeEvents(events: ExecutionEvent[]): NodeEventSummary[] {
    const nodeMap = new Map<string, NodeEventSummary>()
    const order: string[] = []

    for (const evt of events) {
        if (evt.type === 'chunk' || evt.type === 'done' || evt.type === 'error') continue
        if (!evt.nodeId) continue

        if (!nodeMap.has(evt.nodeId)) {
            order.push(evt.nodeId)
        }

        const status = evt.type === 'node.started'
            ? 'started'
            : evt.type === 'node.completed'
                ? 'completed'
                : evt.type === 'node.error'
                    ? 'error'
                    : 'started'

        nodeMap.set(evt.nodeId, {
            nodeId: evt.nodeId,
            nodeType: evt.nodeType,
            nodeLabel: evt.nodeLabel,
            status,
            durationMs: evt.durationMs,
            error: evt.error,
        })
    }

    return order.map((id) => nodeMap.get(id)!).filter(Boolean)
}
