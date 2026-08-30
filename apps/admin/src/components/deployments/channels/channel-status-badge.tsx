import { Iconify } from '@everstack/ui/icons'

const STATUS_CONFIG: Record<string, { label: string; bg: string; text: string; dot: string }> = {
    CHANNEL_STATUS_CONNECTED: {
        label: 'Connected',
        bg: 'bg-emerald-500/15',
        text: 'text-emerald-300 light:text-emerald-600',
        dot: 'bg-emerald-400',
    },
    CHANNEL_STATUS_DISCONNECTED: {
        label: 'Disconnected',
        bg: 'bg-brand-main-700/50',
        text: 'text-brand-main-200',
        dot: 'bg-brand-main-400',
    },
    CHANNEL_STATUS_CONNECTING: {
        label: 'Connecting',
        bg: 'bg-amber-500/15',
        text: 'text-amber-300 light:text-amber-700',
        dot: 'bg-amber-400 animate-pulse',
    },
    CHANNEL_STATUS_ERROR: {
        label: 'Error',
        bg: 'bg-red-500/15',
        text: 'text-red-300 light:text-red-600',
        dot: 'bg-red-400',
    },
}

export function ChannelStatusBadge({ status }: { status: string }) {
    const config = STATUS_CONFIG[status] ?? STATUS_CONFIG.CHANNEL_STATUS_DISCONNECTED

    return (
        <span className={`inline-flex items-center gap-1.5 shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${config.bg} ${config.text}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${config.dot}`} />
            {config.label}
        </span>
    )
}

// Platform icon for tables/badges
const PLATFORM_ICON_MAP: Record<string, { icon: string; label: string; color: string }> = {
    discord: { icon: 'simple-icons:discord', label: 'Discord', color: 'text-[#5865F2]' },
    slack: { icon: 'simple-icons:slack', label: 'Slack', color: 'text-[#E01E5A]' },
    telegram: { icon: 'simple-icons:telegram', label: 'Telegram', color: 'text-[#26A5E4]' },
    admin_ui: { icon: 'lucide:monitor', label: 'UI', color: 'text-white/40 light:text-black/40' },
    api: { icon: 'lucide:code-2', label: 'API', color: 'text-white/40 light:text-black/40' },
}

export function PlatformSourceBadge({ source }: { source: string }) {
    const config = PLATFORM_ICON_MAP[source] ?? PLATFORM_ICON_MAP.admin_ui

    return (
        <span className={`inline-flex items-center gap-1.5 text-xs font-medium ${config.color}`}>
            <Iconify.Icon icon={config.icon} className="h-3.5 w-3.5" />
            {config.label}
        </span>
    )
}

export function platformLabel(platform: string): { label: string; icon: string } {
    switch (platform) {
        case 'PLATFORM_DISCORD': return { label: 'Discord', icon: 'simple-icons:discord' }
        case 'PLATFORM_SLACK': return { label: 'Slack', icon: 'simple-icons:slack' }
        case 'PLATFORM_TELEGRAM': return { label: 'Telegram', icon: 'simple-icons:telegram' }
        default: return { label: platform, icon: 'lucide:help-circle' }
    }
}

export function sessionModeLabel(mode: string): string {
    switch (mode) {
        case 'SESSION_MODE_SHARED': return 'Shared'
        case 'SESSION_MODE_PER_USER': return 'Per User'
        case 'SESSION_MODE_THREAD': return 'Thread'
        default: return mode
    }
}
