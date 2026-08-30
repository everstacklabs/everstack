import { AgentLifecycleStatus } from '@/server/agents'

export const LIFECYCLE_STATUS_META: Record<number, { label: string; colors: string; pulse?: boolean; spin?: boolean }> = {
    [AgentLifecycleStatus.CREATED]: { label: 'Created', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
    [AgentLifecycleStatus.PROVISIONING]: { label: 'Provisioning', colors: 'bg-blue-500/20 text-blue-300 light:text-blue-600', spin: true },
    [AgentLifecycleStatus.RUNNING]: { label: 'Running', colors: 'bg-green-500/20 text-green-300 light:text-green-600', pulse: true },
    [AgentLifecycleStatus.IDLE]: { label: 'Idle', colors: 'bg-amber-500/20 text-amber-300 light:text-amber-700' },
    [AgentLifecycleStatus.SLEEPING]: { label: 'Sleeping', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
    [AgentLifecycleStatus.WAKING]: { label: 'Waking', colors: 'bg-yellow-500/20 text-yellow-300 light:text-yellow-700' },
    [AgentLifecycleStatus.FAILED]: { label: 'Failed', colors: 'bg-red-500/20 text-red-300 light:text-red-600' },
    [AgentLifecycleStatus.TERMINATED]: { label: 'Terminated', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
}

export function LifecycleStatusBadge({ status }: { status: AgentLifecycleStatus }) {
    const meta = LIFECYCLE_STATUS_META[status] ?? LIFECYCLE_STATUS_META[AgentLifecycleStatus.CREATED]
    return (
        <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium ${meta.colors}`}>
            {meta.pulse && <span className="h-1.5 w-1.5 rounded-full bg-green-400 animate-pulse" />}
            {meta.spin && <span className="h-1.5 w-1.5 rounded-full border border-blue-400 border-t-transparent animate-spin" />}
            {meta.label}
        </span>
    )
}
