import { useMemo } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useTraceSessions } from '@/hooks/observability/use-sessions'
import { Iconify, ui } from '@everstack/ui'
import { Loader } from '@everstack/ui/components'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'

const { Badge } = ui

type ProtoTimestamp = { seconds?: bigint | number; nanos?: number; toDate?: () => Date }

export const Route = createFileRoute('/observability/sessions')({
    component: SessionsPage,
})

function tsToDate(ts: ProtoTimestamp | undefined): Date | null {
    if (!ts) return null
    if (typeof (ts as any).toDate === 'function') return (ts as any).toDate()
    const seconds = typeof ts.seconds === 'bigint' ? Number(ts.seconds) : Number(ts.seconds ?? 0)
    return new Date(seconds * 1000 + Math.floor((ts.nanos ?? 0) / 1_000_000))
}

function formatDuration(ns: number): string {
    const ms = ns / 1_000_000
    if (ms < 1000) return `${ms.toFixed(0)}ms`
    if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
    return `${(ms / 60_000).toFixed(1)}m`
}

function SessionsPage() {
    const navigate = useNavigate()
    const { data, isLoading } = useTraceSessions()

    const sessions = data?.sessions ?? []

    const columns: ColumnConfig<any>[] = useMemo(() => [
        {
            id: 'sessionId',
            header: 'Session ID',
            width: 220,
            minWidth: 140,
            render: (s: any) => (
                <span className="truncate font-mono font-medium text-brand-secondary-100 text-xs">
                    {s.sessionId?.length > 20 ? `${s.sessionId.substring(0, 20)}...` : s.sessionId}
                </span>
            ),
        },
        {
            id: 'kind',
            header: 'Kind',
            width: 160,
            minWidth: 100,
            render: (s: any) => {
                const kinds: string[] = s.kinds ?? []
                if (kinds.length === 0) {
                    return <span className="text-xs text-brand-main-100/50">-</span>
                }
                return (
                    <div className="flex items-center gap-1 flex-wrap">
                        {kinds.map((k) => (
                            <Badge
                                key={k}
                                variant="outline"
                                className="text-xs py-0.5 bg-brand-main-700/50 border-brand-main-500 text-white/80 light:text-black/80 capitalize"
                            >
                                {k}
                            </Badge>
                        ))}
                    </div>
                )
            },
        },
        {
            id: 'userId',
            header: 'User',
            width: 160,
            minWidth: 100,
            render: (s: any) => (
                <span className="truncate text-xs text-brand-main-100">{s.userId || '-'}</span>
            ),
        },
        {
            id: 'traceCount',
            header: 'Traces',
            width: 80,
            minWidth: 60,
            render: (s: any) => (
                <span className="text-xs text-brand-main-100">{s.traceCount}</span>
            ),
        },
        {
            id: 'duration',
            header: 'Duration',
            width: 100,
            minWidth: 80,
            render: (s: any) => (
                <span className="text-xs text-brand-main-100">{formatDuration(Number(s.totalDurationNs))}</span>
            ),
        },
        {
            id: 'tokens',
            header: 'Tokens',
            width: 100,
            minWidth: 80,
            render: (s: any) => (
                <span className="text-xs text-brand-main-100">
                    {(Number(s.totalInputTokens) + Number(s.totalOutputTokens)).toLocaleString()}
                </span>
            ),
        },
        {
            id: 'cost',
            header: 'Cost',
            width: 90,
            minWidth: 70,
            render: (s: any) => (
                <span className="text-xs text-brand-main-100">${s.totalCost?.toFixed(4) ?? '0.0000'}</span>
            ),
        },
        {
            id: 'errors',
            header: 'Errors',
            width: 80,
            minWidth: 60,
            render: (s: any) => (
                <span className={`text-xs ${s.errorCount > 0 ? 'text-red-400 light:text-red-600' : 'text-brand-main-100/50'}`}>
                    {s.errorCount ?? 0}
                </span>
            ),
        },
        {
            id: 'environment',
            header: 'Env',
            width: 110,
            minWidth: 80,
            render: (s: any) => (
                <span className="text-xs text-brand-main-100">{s.environment || '-'}</span>
            ),
        },
        {
            id: 'lastActive',
            header: 'Last Active',
            width: 160,
            minWidth: 140,
            render: (s: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {tsToDate(s.lastTraceAt)?.toLocaleString() ?? '-'}
                </span>
            ),
        },
    ], [])

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            {isLoading ? (
                <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                    <Loader loaderText="Loading sessions..." />
                </div>
            ) : sessions.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="lucide:user-plus" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sessions found</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        Sessions are created automatically when traces include a session_id. Start sending traces with session IDs to see them here.
                    </p>
                </div>
            ) : (
                <ResponsiveTable
                    columns={columns}
                    data={sessions}
                    enableResizing={true}
                    minTableWidth="100%"
                    emptyMessage="No sessions found."
                    onRowClick={(s: any) => navigate({ to: '/observability/sessions/$sessionId', params: { sessionId: s.sessionId } })}
                    rowKey={(s: any) => s.sessionId}
                />
            )}
        </div>
    )
}
