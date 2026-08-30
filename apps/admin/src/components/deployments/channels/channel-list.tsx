import { useMemo, useState } from 'react'
import { Trash2, Pencil, ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { toast } from '@everstack/ui/components'
import { useSearch } from '@tanstack/react-router'
import { useChannels, useDeleteChannel, useChannelStatuses } from '@/hooks/deployments/use-channels'
import { useAgents } from '@/hooks/deployments/use-agents'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { ChannelStatusBadge, platformLabel, sessionModeLabel } from './channel-status-badge'
import { ChannelConfigSheet } from './channel-config-sheet'
import { ChannelStatus, type ChannelConfig } from '@/server/channels'
import { usePlanLimit } from '@/hooks/license/use-license-status'

const CHANNEL_STATUS_NAMES: Record<number, string> = {
    [ChannelStatus.CONNECTED]: 'CHANNEL_STATUS_CONNECTED',
    [ChannelStatus.DISCONNECTED]: 'CHANNEL_STATUS_DISCONNECTED',
    [ChannelStatus.ERROR]: 'CHANNEL_STATUS_ERROR',
    [ChannelStatus.CONNECTING]: 'CHANNEL_STATUS_CONNECTING',
}

const {
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    Button,
    Switch,
} = ui

export function ChannelList() {
    const { data: channels } = useChannels()
    const { data: statuses } = useChannelStatuses()
    const { data: agents } = useAgents()
    const deleteMutation = useDeleteChannel()
    // No local ceiling to fall back on: self-hosted instances have no channel
    // cap, so guessing one here would show an upgrade banner to someone who
    // has nothing to upgrade to. Show a cap only when the gateway reports one.
    const channelLimit = usePlanLimit('CHANNELS')
    const channelCount = channels?.length ?? 0
    const showChannelLimitBanner = channelLimit !== null && channelCount >= channelLimit

    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const [editChannelId, setEditChannelId] = useState<string | null>(null)

    const search = useSearch({ strict: false })
    const sourceChannels = channels ?? []

    const agentNameMap = useMemo(() => {
        const map = new Map<string, string>()
        for (const agent of agents ?? []) {
            map.set(agent.id, agent.name)
        }
        return map
    }, [agents])

    const statusMap = useMemo(() => {
        return new Map((statuses ?? []).map((s) => [s.channelId, s.status]))
    }, [statuses])

    const filteredChannels = useMemo(() => {
        let filtered = [...sourceChannels]

        const searchTerm = (search as Record<string, unknown>)?.search as string | undefined
        if (searchTerm) {
            const term = searchTerm.toLowerCase()
            filtered = filtered.filter(c =>
                c.name.toLowerCase().includes(term) ||
                platformLabel(c.platform?.toString() ?? '').label.toLowerCase().includes(term) ||
                (c.agentId ? agentNameMap.get(c.agentId) ?? '' : 'dispatcher').toLowerCase().includes(term)
            )
        }

        return filtered
    }, [sourceChannels, search, agentNameMap])

    const handleDelete = async (id: string) => {
        try {
            await deleteMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Channel deleted successfully')
        } catch {
            toast.error('Failed to delete channel')
        }
    }

    const columns: ColumnConfig<ChannelConfig>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 220,
            minWidth: 140,
            render: (channel: ChannelConfig) => {
                const status = statusMap.get(channel.id) ?? ChannelStatus.DISCONNECTED
                const statusColor = status === ChannelStatus.CONNECTED
                    ? '#22c55e'
                    : status === ChannelStatus.ERROR
                        ? '#ef4444'
                        : status === ChannelStatus.CONNECTING
                            ? '#eab308'
                            : '#64748b'

                return (
                    <div className="flex min-w-0 items-center gap-2">
                        <span
                            className="h-2.5 w-2.5 shrink-0 rounded-full border border-white/20 light:border-black/20"
                            style={{ backgroundColor: statusColor }}
                        />
                        <span className="truncate font-medium text-brand-secondary-100 text-xs">
                            {channel.name}
                        </span>
                    </div>
                )
            },
        },
        {
            id: 'platform',
            header: 'Platform',
            width: 130,
            minWidth: 100,
            render: (channel: ChannelConfig) => {
                const info = platformLabel(channel.platform?.toString() ?? '')
                return (
                    <span className="inline-flex items-center gap-1.5 text-xs text-white/80 light:text-black/80">
                        <Iconify.Icon icon={info.icon} className="h-3.5 w-3.5" />
                        {info.label}
                    </span>
                )
            },
        },
        {
            id: 'agent',
            header: 'Agent',
            width: 180,
            minWidth: 120,
            render: (channel: ChannelConfig) => {
                if (!channel.agentId) {
                    return (
                        <span className="inline-flex items-center gap-1 text-xs text-amber-400/80 light:text-amber-700/80">
                            <Iconify.Icon icon="lucide:route" className="h-3 w-3" />
                            Dispatcher
                        </span>
                    )
                }
                return (
                    <span className="truncate text-xs text-brand-main-100">
                        {agentNameMap.get(channel.agentId) ?? channel.agentId.slice(0, 12) + '...'}
                    </span>
                )
            },
        },
        {
            id: 'sessionMode',
            header: 'Session Mode',
            width: 120,
            minWidth: 90,
            render: (channel: ChannelConfig) => {
                const label = sessionModeLabel(channel.sessionMode?.toString() ?? '')
                return (
                    <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-blue-500/15 text-blue-300 light:text-blue-600">
                        {label}
                    </span>
                )
            },
        },
        {
            id: 'status',
            header: 'Status',
            width: 120,
            minWidth: 100,
            render: (channel: ChannelConfig) => {
                const status = statusMap.get(channel.id) ?? ChannelStatus.DISCONNECTED
                return <ChannelStatusBadge status={CHANNEL_STATUS_NAMES[status] ?? 'CHANNEL_STATUS_DISCONNECTED'} />
            },
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (channel: ChannelConfig) => (
                <div data-row-actions>
                    <Switch checked={channel.enabled} disabled />
                </div>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 80,
            minWidth: 80,
            maxWidth: 80,
            resizable: false,
            render: (channel: ChannelConfig) => (
                <div className="flex items-center gap-2 justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                        onClick={() => setEditChannelId(channel.id)}
                        title="Edit channel"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                        onClick={() => {
                            setDeleteConfirmId(channel.id)
                            setDeleteConfirmName(channel.name)
                        }}
                        title="Delete channel"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            {showChannelLimitBanner && (
                <div className="flex items-center gap-2 border-b border-amber-500/20 bg-amber-500/5 px-4 py-2.5 shrink-0">
                    <Iconify.Icon icon="lucide:info" className="h-4 w-4 text-amber-400 light:text-amber-700 shrink-0" />
                    <span className="text-xs text-amber-300 light:text-amber-700">
                        Channel limit reached ({channelCount}/{channelLimit}).
                        Upgrade your plan to connect more channels.
                    </span>
                    <a
                        href="https://everstack.ai/pricing"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="ml-auto text-xs text-amber-400 light:text-amber-700 hover:text-amber-300 light:hover:text-amber-700 underline shrink-0"
                    >
                        View plans
                    </a>
                </div>
            )}
            <ResponsiveTable
                columns={columns}
                data={filteredChannels}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={sourceChannels.length === 0 ? (
                    <div className="flex flex-col items-center justify-center py-12">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="lucide:message-square" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No channels configured</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Add a channel to connect an agent to a messaging platform.
                        </p>
                    </div>
                ) : 'No channels match your search.'}
                rowKey={(channel) => channel.id}
            />

            {/* Delete Confirmation */}
            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete Channel</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? This will disconnect the bot and terminate active sessions.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteMutation.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteMutation.isPending}
                        >
                            {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            {/* Edit Sheet */}
            <ChannelConfigSheet
                open={editChannelId !== null}
                onOpenChange={(open) => { if (!open) setEditChannelId(null) }}
                mode="edit"
                channelId={editChannelId ?? undefined}
            />
        </div>
    )
}
