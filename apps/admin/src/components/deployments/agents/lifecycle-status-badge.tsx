import { AgentLifecycleStatus } from '@/server/agents'

export const LIFECYCLE_STATUS_META: Record<number, { label: string; colors: string; pulse?: boolean; spin?: boolean }> = {
    [AgentLifecycleStatus.CREATED]: { label: 'Created', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
    [AgentLifecycleStatus.PROVISIONING]: { label: 'Provisioning', colors: 'bg-info-surface-strong text-info', spin: true },
    [AgentLifecycleStatus.RUNNING]: { label: 'Running', colors: 'bg-success-surface-strong text-success', pulse: true },
    [AgentLifecycleStatus.IDLE]: { label: 'Idle', colors: 'bg-warning-surface-strong text-warning' },
    [AgentLifecycleStatus.SLEEPING]: { label: 'Sleeping', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
    [AgentLifecycleStatus.WAKING]: { label: 'Waking', colors: 'bg-warning-surface-strong text-warning' },
    [AgentLifecycleStatus.FAILED]: { label: 'Failed', colors: 'bg-danger-surface-strong text-danger' },
    [AgentLifecycleStatus.TERMINATED]: { label: 'Terminated', colors: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
}

export function LifecycleStatusBadge({ status }: { status: AgentLifecycleStatus }) {
    const meta = LIFECYCLE_STATUS_META[status] ?? LIFECYCLE_STATUS_META[AgentLifecycleStatus.CREATED]
    return (
        <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium ${meta.colors}`}>
            {meta.pulse && <span className="h-1.5 w-1.5 rounded-full bg-success-solid animate-pulse" />}
            {meta.spin && <span className="h-1.5 w-1.5 rounded-full border border-info-solid border-t-transparent animate-spin" />}
            {meta.label}
        </span>
    )
}
