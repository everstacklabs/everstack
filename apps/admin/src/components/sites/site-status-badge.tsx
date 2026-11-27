import type { SiteStatus } from '@/server/sites'

const STATUS_DISPLAY: Record<SiteStatus, { label: string; className: string }> = {
    active: { label: 'Active', className: 'bg-success-surface-strong text-success' },
    expired: { label: 'Expired', className: 'bg-warning-surface-strong text-warning' },
    disabled: { label: 'Disabled', className: 'bg-danger-surface-strong text-danger' },
    unknown: { label: 'Unknown', className: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
}

export function SiteStatusBadge({ status }: { status: SiteStatus }) {
    const display = STATUS_DISPLAY[status] ?? STATUS_DISPLAY.unknown
    return (
        <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium whitespace-nowrap ${display.className}`}>
            {display.label}
        </span>
    )
}
