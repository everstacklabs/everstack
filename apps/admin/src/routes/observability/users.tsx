import { useMemo } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useTraceUsers } from '@/hooks/observability/use-users'
import { Iconify } from '@everstack/ui/icons'
import { Loader } from '@everstack/ui/components'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'

type ProtoTimestamp = { seconds?: bigint | number; nanos?: number; toDate?: () => Date }

export const Route = createFileRoute('/observability/users')({
    component: UsersPage,
})

function tsToDate(ts: ProtoTimestamp | undefined): Date | null {
    if (!ts) return null
    if (typeof (ts as any).toDate === 'function') return (ts as any).toDate()
    const seconds = typeof ts.seconds === 'bigint' ? Number(ts.seconds) : Number(ts.seconds ?? 0)
    return new Date(seconds * 1000 + Math.floor((ts.nanos ?? 0) / 1_000_000))
}

function UsersPage() {
    const navigate = useNavigate()
    const { data, isLoading } = useTraceUsers()

    const users = data?.users ?? []

    const columns: ColumnConfig<any>[] = useMemo(() => [
        {
            id: 'userId',
            header: 'User ID',
            width: 220,
            minWidth: 140,
            render: (u: any) => (
                <span className="truncate font-mono font-medium text-brand-secondary-100 text-xs">
                    {u.userId?.length > 24 ? `${u.userId.substring(0, 24)}...` : u.userId}
                </span>
            ),
        },
        {
            id: 'sessions',
            header: 'Sessions',
            width: 90,
            minWidth: 60,
            render: (u: any) => (
                <span className="text-xs text-brand-main-100">{u.sessionCount}</span>
            ),
        },
        {
            id: 'traces',
            header: 'Traces',
            width: 80,
            minWidth: 60,
            render: (u: any) => (
                <span className="text-xs text-brand-main-100">{u.traceCount}</span>
            ),
        },
        {
            id: 'tokens',
            header: 'Total Tokens',
            width: 120,
            minWidth: 80,
            render: (u: any) => (
                <span className="text-xs text-brand-main-100">
                    {Number(u.totalTokens).toLocaleString()}
                </span>
            ),
        },
        {
            id: 'cost',
            header: 'Cost',
            width: 90,
            minWidth: 70,
            render: (u: any) => (
                <span className="text-xs text-brand-main-100">${u.totalCost?.toFixed(4) ?? '0.0000'}</span>
            ),
        },
        {
            id: 'errorRate',
            header: 'Error Rate',
            width: 100,
            minWidth: 80,
            render: (u: any) => (
                <span className={`text-xs ${u.errorRate > 0.05 ? 'text-red-400 light:text-red-600' : 'text-brand-main-100'}`}>
                    {(u.errorRate * 100).toFixed(1)}%
                </span>
            ),
        },
        {
            id: 'lastSeen',
            header: 'Last Seen',
            width: 160,
            minWidth: 140,
            render: (u: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {tsToDate(u.lastSeen)?.toLocaleString() ?? '-'}
                </span>
            ),
        },
    ], [])

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            {isLoading ? (
                <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                    <Loader loaderText="Loading users..." />
                </div>
            ) : users.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="lucide:user-plus" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No users found</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        Users are created automatically when traces include a user_id. Start sending traces with user IDs to track per-user usage.
                    </p>
                </div>
            ) : (
                <ResponsiveTable
                    columns={columns}
                    data={users}
                    enableResizing={true}
                    minTableWidth="100%"
                    emptyMessage="No users found."
                    onRowClick={(u: any) => navigate({ to: '/observability/users/$userId', params: { userId: u.userId } })}
                    rowKey={(u: any) => u.userId}
                />
            )}
        </div>
    )
}
