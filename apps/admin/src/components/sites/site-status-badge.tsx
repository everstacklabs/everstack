import type { SiteStatus } from '@/server/sites'

const STATUS_DISPLAY: Record<SiteStatus, { label: string; className: string }> = {
    active: { label: 'Active', className: 'bg-green-500/20 text-green-300 light:text-green-600' },
    expired: { label: 'Expired', className: 'bg-yellow-500/20 text-yellow-300 light:text-yellow-700' },
    disabled: { label: 'Disabled', className: 'bg-red-500/20 text-red-300 light:text-red-600' },
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
