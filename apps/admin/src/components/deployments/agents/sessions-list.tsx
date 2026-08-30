import { useMemo } from 'react'
import { useAgents, useCancelSession } from '@/hooks/deployments/use-agents'
import type { AgentSession } from '@/server/agents'
import { SessionStatus } from '@/server/agents'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useNavigate } from '@tanstack/react-router'
import { PlatformSourceBadge } from '@/components/deployments/channels/channel-status-badge'

const SESSION_STATUS_STYLES: Record<number, { label: string; className: string }> = {
    [SessionStatus.CREATED]: { label: 'Created', className: 'bg-brand-main-700/40 text-brand-main-200' },
    [SessionStatus.RUNNING]: { label: 'Running', className: 'bg-brand-secondary-600/20 text-brand-secondary-300' },
    [SessionStatus.WAITING_FOR_INPUT]: { label: 'Waiting', className: 'bg-brand-secondary-500/15 text-brand-secondary-200' },
    [SessionStatus.WAITING_FOR_APPROVAL]: { label: 'Approval', className: 'bg-brand-secondary-700/25 text-brand-secondary-300' },
    [SessionStatus.COMPLETED]: { label: 'Completed', className: 'bg-brand-secondary-400 text-brand-secondary-800' },
    [SessionStatus.FAILED]: { label: 'Failed', className: 'bg-red-500/15 text-red-400 light:text-red-600' },
    [SessionStatus.CANCELLED]: { label: 'Cancelled', className: 'bg-brand-main-700/30 text-brand-main-300' },
}

interface SessionsListProps {
    sessions: AgentSession[]
}

export function SessionsList({ sessions }: SessionsListProps) {
    const cancelMutation = useCancelSession()
    const { data: agents = [] } = useAgents()
    const navigate = useNavigate()

    const agentNameMap = useMemo(() => {
        const map = new Map<string, string>()
        for (const agent of agents) {
            map.set(agent.id, agent.name)
        }
        return map
    }, [agents])

    const sortedSessions = useMemo(() => {
        return [...sessions].sort((a, b) => {
            const aTime = a.createdAt?.seconds ? Number(a.createdAt.seconds) : 0
            const bTime = b.createdAt?.seconds ? Number(b.createdAt.seconds) : 0
            return bTime - aTime
        })
    }, [sessions])

    const handleCancel = async (e: React.MouseEvent, sessionId: string) => {
        e.stopPropagation()
        try {
            await cancelMutation.mutateAsync(sessionId)
            toast.success('Session cancelled')
        } catch {
            toast.error('Failed to cancel session')
        }
    }

    const columns: ColumnConfig<AgentSession>[] = [
        {
            id: 'id',
            header: 'Session ID',
            width: 160,
            minWidth: 140,
            render: (session: AgentSession) => (
                <span className="font-mono text-xs text-brand-secondary-400 whitespace-nowrap overflow-visible">
                    {session.id.slice(0, 12)}...
                </span>
            ),
        },
        {
            id: 'agentId',
            header: 'Agent',
            width: 180,
            minWidth: 120,
            render: (session: AgentSession) => (
                <span className="text-sm text-white/90 light:text-black/90">
                    {agentNameMap.get(session.agentId) ?? session.agentId.slice(0, 12) + '...'}
                </span>
            ),
        },
        {
            id: 'status',
            header: 'Status',
            width: 110,
            minWidth: 90,
            render: (session: AgentSession) => {
                const style = SESSION_STATUS_STYLES[session.status] ?? { label: 'Unknown', className: 'bg-brand-main-700/30 text-brand-main-300' }
                return (
                    <span className={`inline-block px-2 py-0.5 rounded-full text-[10px] font-medium whitespace-nowrap ${style.className}`}>
                        {style.label}
                    </span>
                )
            },
        },
        {
            id: 'source',
            header: 'Source',
            width: 100,
            minWidth: 80,
            render: (session: AgentSession) => {
                const source = (session as any).source ?? 'admin_ui'
                return <PlatformSourceBadge source={source} />
            },
        },
        {
            id: 'turnCount',
            header: 'Turns',
            width: 70,
            minWidth: 60,
            render: (session: AgentSession) => (
                <span className="text-sm text-white/70 light:text-black/70 tabular-nums">{session.turnCount}</span>
            ),
        },
        {
            id: 'totalTokens',
            header: 'Tokens',
            width: 100,
            minWidth: 80,
            render: (session: AgentSession) => (
                <span className="text-sm text-white/70 light:text-black/70 tabular-nums">
                    {session.totalTokens > 0 ? session.totalTokens.toLocaleString() : '-'}
                </span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 170,
            minWidth: 140,
            render: (session: AgentSession) => (
                <span className="text-sm text-white/50 light:text-black/50">{formatTimestamp(session.createdAt)}</span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 80,
            minWidth: 80,
            maxWidth: 80,
            resizable: false,
            render: (session: AgentSession) => {
                const isActive = session.status === SessionStatus.RUNNING || session.status === SessionStatus.WAITING_FOR_INPUT || session.status === SessionStatus.WAITING_FOR_APPROVAL
                if (!isActive) return null
                return (
                    <div data-row-actions>
                        <button
                            type="button"
                            className="px-2 py-1 rounded text-xs text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-700 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                            onClick={(e) => handleCancel(e, session.id)}
                            disabled={cancelMutation.isPending}
                        >
                            Cancel
                        </button>
                    </div>
                )
            },
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={sortedSessions}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={
                    <div className="flex flex-col items-center justify-center py-12">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:chat-bubble-left-right" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sessions found</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Sessions will appear here when agents are run.
                        </p>
                    </div>
                }
                onRowClick={(session) => navigate({ to: '/deployments/agents/sessions/$sessionId', params: { sessionId: session.id } })}
                rowKey={(session) => session.id}
            />
        </div>
    )
}
