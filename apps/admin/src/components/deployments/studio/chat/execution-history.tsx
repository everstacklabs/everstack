import { useEffect } from 'react'
import { Icon } from '@iconify/react'
import { useExecutionStore } from '@/stores/execution-store'
import { useStudioStore } from '@/stores/studio-store'

function formatTimestamp(unixMs: bigint | number): string {
    const ms = typeof unixMs === 'bigint' ? Number(unixMs) : unixMs
    if (!ms) return ''
    const date = new Date(ms)
    return date.toLocaleString(undefined, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
    })
}

function formatDuration(ms: number): string {
    if (!ms) return '-'
    if (ms < 1000) return `${ms}ms`
    return `${(ms / 1000).toFixed(1)}s`
}

const statusConfig: Record<string, { icon: string; color: string; bg: string }> = {
    running: { icon: 'lucide:loader-2', color: 'text-blue-400 light:text-blue-600', bg: 'bg-blue-500/20' },
    completed: { icon: 'lucide:check', color: 'text-emerald-400 light:text-emerald-600', bg: 'bg-emerald-500/20' },
    failed: { icon: 'lucide:x', color: 'text-red-400 light:text-red-600', bg: 'bg-red-500/20' },
}

const triggerIcons: Record<string, string> = {
    manual: 'lucide:play',
    webhook: 'lucide:webhook',
    schedule: 'lucide:clock',
    replay: 'lucide:refresh-cw',
}

export function ExecutionHistory() {
    const executions = useExecutionStore((s) => s.executions)
    const executionsTotal = useExecutionStore((s) => s.executionsTotal)
    const loadingExecutions = useExecutionStore((s) => s.loadingExecutions)
    const fetchExecutions = useExecutionStore((s) => s.fetchExecutions)
    const selectExecution = useExecutionStore((s) => s.selectExecution)

    const workflowId = useStudioStore((s) => s.workflowId)
    const tenantId = useStudioStore((s) => s.tenantId)

    useEffect(() => {
        if (workflowId && tenantId) {
            fetchExecutions(workflowId, tenantId)
        }
    }, [workflowId, tenantId, fetchExecutions])

    const handleLoadMore = () => {
        if (workflowId && tenantId) {
            fetchExecutions(workflowId, tenantId, { offset: executions.length })
        }
    }

    if (loadingExecutions && executions.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center py-12 text-brand-main-400">
                <Icon icon="lucide:loader-2" className="h-8 w-8 animate-spin mb-2 opacity-50" />
                <span className="text-sm">Loading executions...</span>
            </div>
        )
    }

    if (executions.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center py-12">
                <div className="relative mb-5">
                    <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-lg" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
                        <Icon icon="lucide:history" className="size-6 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-sm font-medium text-white light:text-brand-main-50 mb-1">No executions yet</h3>
                <p className="text-xs text-white/40 light:text-black/40">Execute the workflow to see history here</p>
            </div>
        )
    }

    return (
        <div className="flex flex-col">
            {executions.map((exec) => {
                const status = statusConfig[exec.status] ?? statusConfig.running
                const triggerIcon = triggerIcons[exec.triggerType] ?? 'lucide:play'

                return (
                    <button
                        key={exec.id}
                        onClick={() => selectExecution(exec.id)}
                        className="flex items-center gap-2.5 px-3 py-2.5 border-b border-brand-main-800 hover:bg-brand-main-800/50 transition-colors text-left w-full"
                    >
                        {/* Status badge */}
                        <div className={`flex-shrink-0 h-5 w-5 rounded-full ${status.bg} flex items-center justify-center`}>
                            <Icon
                                icon={status.icon}
                                className={`h-3 w-3 ${status.color} ${exec.status === 'running' ? 'animate-spin' : ''}`}
                            />
                        </div>

                        {/* Main info */}
                        <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-1.5">
                                <Icon icon={triggerIcon} className="h-3 w-3 text-brand-main-400 flex-shrink-0" />
                                <span className="text-xs text-brand-main-200 truncate">
                                    {exec.triggerType}
                                </span>
                                {exec.resolvedModel && (
                                    <span className="text-[10px] text-brand-main-500 truncate">
                                        {exec.resolvedModel}
                                    </span>
                                )}
                            </div>
                            <div className="text-[10px] text-brand-main-500 mt-0.5">
                                {formatTimestamp(exec.startedAt)}
                            </div>
                        </div>

                        {/* Right side: duration & tokens */}
                        <div className="flex-shrink-0 text-right">
                            <div className="text-[10px] text-brand-main-400 tabular-nums">
                                {formatDuration(exec.durationMs)}
                            </div>
                            {exec.totalTokens > 0 && (
                                <div className="text-[10px] text-brand-main-500 tabular-nums">
                                    {exec.totalTokens} tok
                                </div>
                            )}
                        </div>

                        <Icon icon="lucide:chevron-right" className="h-3 w-3 text-brand-main-600 flex-shrink-0" />
                    </button>
                )
            })}

            {/* Load more */}
            {executions.length < executionsTotal && (
                <button
                    onClick={handleLoadMore}
                    disabled={loadingExecutions}
                    className="py-2.5 text-xs text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors disabled:opacity-50"
                >
                    {loadingExecutions ? (
                        <span className="flex items-center justify-center gap-1.5">
                            <Icon icon="lucide:loader-2" className="h-3 w-3 animate-spin" />
                            Loading...
                        </span>
                    ) : (
                        `Load more (${executions.length} of ${executionsTotal})`
                    )}
                </button>
            )}
        </div>
    )
}
