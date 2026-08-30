import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { listRichTraces } from '@/server/traces'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Loader } from '@everstack/ui/components'

type ProtoTimestamp = { seconds?: bigint | number; nanos?: number; toDate?: () => Date }

function tsToDate(ts: ProtoTimestamp | undefined): Date | null {
    if (!ts) return null
    if (typeof (ts as any).toDate === 'function') return (ts as any).toDate()
    const seconds = typeof ts.seconds === 'bigint' ? Number(ts.seconds) : Number(ts.seconds ?? 0)
    return new Date(seconds * 1000 + Math.floor((ts.nanos ?? 0) / 1_000_000))
}

function formatLatency(seconds: number): string {
    const ms = seconds * 1000
    if (ms < 1000) return `${ms.toFixed(0)}ms`
    if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`
    return `${(ms / 60_000).toFixed(1)}m`
}

interface LinkedTracesTableProps {
    sessionId?: string
    userId?: string
    limit?: number
}

export function LinkedTracesTable({ sessionId, userId, limit = 100 }: LinkedTracesTableProps) {
    const navigate = useNavigate()
    const { data, isLoading } = useQuery({
        queryKey: ['linked-rich-traces', { sessionId, userId, limit }],
        queryFn: () => listRichTraces({ sessionId, userId, limit }),
        enabled: !!sessionId || !!userId,
        staleTime: 15_000,
    })

    const traces = data?.traces ?? []

    const columns: ColumnConfig<any>[] = useMemo(() => [
        {
            id: 'timestamp',
            header: 'Time',
            width: 170,
            minWidth: 140,
            render: (t: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {tsToDate(t.timestamp)?.toLocaleString() ?? '-'}
                </span>
            ),
        },
        {
            id: 'name',
            header: 'Name',
            width: 200,
            minWidth: 120,
            render: (t: any) => (
                <span className="truncate text-xs text-brand-main-100">{t.name || t.id.substring(0, 12) + '…'}</span>
            ),
        },
        {
            id: 'environment',
            header: 'Env',
            width: 100,
            minWidth: 80,
            render: (t: any) => (
                <span className="text-xs text-brand-main-100">{t.environment || '-'}</span>
            ),
        },
        {
            id: 'latency',
            header: 'Latency',
            width: 90,
            minWidth: 70,
            render: (t: any) => (
                <span className="text-xs text-brand-main-100">{formatLatency(t.latency || 0)}</span>
            ),
        },
        {
            id: 'cost',
            header: 'Cost',
            width: 90,
            minWidth: 70,
            render: (t: any) => (
                <span className="text-xs text-brand-main-100">${(t.totalCost || 0).toFixed(4)}</span>
            ),
        },
        {
            id: 'observations',
            header: 'Spans',
            width: 70,
            minWidth: 50,
            render: (t: any) => (
                <span className="text-xs text-brand-main-100">{t.observations?.length ?? 0}</span>
            ),
        },
    ], [])

    if (isLoading) {
        return (
            <div className="flex items-center justify-center py-8 text-white/60 light:text-black/60">
                <Loader loaderText="Loading traces..." />
            </div>
        )
    }

    if (traces.length === 0) {
        return (
            <div className="text-center py-6 text-white/30 text-sm light:text-black/30">
                No traces found for this {sessionId ? 'session' : 'user'}.
            </div>
        )
    }

    return (
        <ResponsiveTable
            columns={columns}
            data={traces}
            enableResizing={true}
            minTableWidth="100%"
            emptyMessage="No traces."
            onRowClick={(t: any) =>
                navigate({
                    to: '/observability/traces',
                    search: (prev: any) => ({ ...prev, trace: t.id }),
                })
            }
            rowKey={(t: any) => t.id}
        />
    )
}
