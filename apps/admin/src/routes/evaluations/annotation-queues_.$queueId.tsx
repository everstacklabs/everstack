import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useAnnotationQueue, useQueueItems, useQueueStats } from '@/hooks/evaluations/use-annotations'
import { ui } from '@everstack/ui'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ClipboardList, Clock, CheckCircle, SkipForward } from 'lucide-react'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'

const {
    Card,
    CardContent,
} = ui

export const Route = createFileRoute('/evaluations/annotation-queues_/$queueId')({
    component: AnnotationQueueDetailPage,
})

function MetricCard({
    icon,
    label,
    value,
}: {
    icon: React.ReactNode
    label: string
    value: string | number
}) {
    return (
        <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
                <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 min-w-0">
                        <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                            <div className="text-brand-secondary-300">{icon}</div>
                        </div>
                        <div className="min-w-0">
                            <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide truncate">
                                {label}
                            </div>
                        </div>
                    </div>
                    <div className="text-sm font-semibold text-white light:text-brand-main-50">
                        {value}
                    </div>
                </div>
            </CardContent>
        </Card>
    )
}

const ITEM_STATUS_ENUM_TO_KEY: Record<number, string> = {
    0: 'pending',    // UNSPECIFIED
    1: 'pending',    // PENDING
    2: 'in_progress', // IN_PROGRESS
    3: 'completed',  // COMPLETED
    4: 'skipped',    // SKIPPED
}

function itemStatusBadge(status: string | number) {
    const map: Record<string, { className: string; label: string }> = {
        pending: { className: 'bg-amber-500/15 text-amber-300 light:text-amber-700', label: 'Pending' },
        in_progress: { className: 'bg-blue-500/15 text-blue-300 light:text-blue-600', label: 'In Progress' },
        completed: { className: 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600', label: 'Completed' },
        skipped: { className: 'bg-white/10 light:bg-black/10 text-white/45 light:text-black/45', label: 'Skipped' },
    }
    const key = typeof status === 'number' ? ITEM_STATUS_ENUM_TO_KEY[status] ?? 'pending' : status?.toLowerCase()
    const s = map[key] ?? map.pending
    return (
        <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${s.className}`}>
            {s.label}
        </span>
    )
}

function AnnotationQueueDetailPage() {
    const gate = useFeatureGate(FeatureKey.EVALUATIONS)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Annotation Queues"
                description="Human review queues for data labeling and quality assurance."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <AnnotationQueueDetailPageContent />
}

function AnnotationQueueDetailPageContent() {
    const { queueId } = Route.useParams()
    const { data: queue, isLoading: queueLoading } = useAnnotationQueue(queueId)
    const { data: items, isLoading: itemsLoading } = useQueueItems(queueId)
    const { data: stats } = useQueueStats(queueId)

    const columns: ColumnConfig<any>[] = useMemo(() => [
        {
            id: 'traceId',
            header: 'Trace ID',
            width: 180,
            minWidth: 120,
            render: (item: any) => (
                <span className="truncate font-mono text-xs text-white light:text-brand-main-50">
                    {item.traceId?.substring(0, 12) ?? '-'}...
                </span>
            ),
        },
        {
            id: 'status',
            header: 'Status',
            width: 110,
            minWidth: 90,
            render: (item: any) => itemStatusBadge(item.status),
        },
        {
            id: 'annotator',
            header: 'Annotator',
            width: 150,
            minWidth: 100,
            render: (item: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {item.annotatorId || '-'}
                </span>
            ),
        },
        {
            id: 'scores',
            header: 'Scores',
            width: 220,
            minWidth: 120,
            render: (item: any) => (
                <span className="text-xs text-white/60 light:text-black/60">
                    {item.scores && Object.keys(item.scores).length > 0
                        ? Object.entries(item.scores).map(([k, v]) => `${k}: ${v}`).join(', ')
                        : '-'}
                </span>
            ),
        },
        {
            id: 'annotatedAt',
            header: 'Annotated At',
            width: 160,
            minWidth: 140,
            render: (item: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {item.annotatedAt ? new Date(item.annotatedAt).toLocaleString() : '-'}
                </span>
            ),
        },
    ], [])

    if (queueLoading) {
        return (
            <div className="flex-1 flex items-center justify-center">
                <Loader loaderText="Loading annotation queue..." />
            </div>
        )
    }

    if (!queue) {
        return (
            <div className="flex flex-col flex-1 items-center justify-center text-white/70 light:text-black/70 gap-4">
                <div className="text-center flex flex-col justify-center items-center space-y-2">
                    <span className="bg-brand-secondary-200 rounded-md p-2 inline-block mb-4">
                        <Iconify.Icon icon="heroicons:clipboard-document-check" className="size-10 text-brand-secondary-700" />
                    </span>
                    <h3 className="text-lg font-medium text-white light:text-brand-main-50">Queue not found</h3>
                    <p className="text-sm w-2/3 mb-4 text-center text-white/60 light:text-black/60">
                        The annotation queue you're looking for doesn't exist or has been deleted.
                    </p>
                </div>
            </div>
        )
    }

    const totalItems = (stats as any)?.totalItems ?? 0
    const pendingItems = (stats as any)?.pendingItems ?? 0
    const completedItems = (stats as any)?.completedItems ?? 0
    const skippedItems = (stats as any)?.skippedItems ?? 0

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            {/* Metric cards */}
            <div className="flex-shrink-0 p-3 space-y-3">
                {/* Metric cards */}
                <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-2">
                    <MetricCard
                        icon={<ClipboardList className="h-4 w-4" />}
                        label="Total Items"
                        value={totalItems}
                    />
                    <MetricCard
                        icon={<Clock className="h-4 w-4" />}
                        label="Pending"
                        value={pendingItems}
                    />
                    <MetricCard
                        icon={<CheckCircle className="h-4 w-4" />}
                        label="Completed"
                        value={completedItems}
                    />
                    <MetricCard
                        icon={<SkipForward className="h-4 w-4" />}
                        label="Skipped"
                        value={skippedItems}
                    />
                </div>
            </div>

            {/* Table */}
            {itemsLoading ? (
                <div className="flex-1 flex items-center justify-center">
                    <Loader loaderText="Loading items..." />
                </div>
            ) : (
                <ResponsiveTable
                    columns={columns}
                    data={items ?? []}
                    enableResizing={true}
                    minTableWidth="100%"
                    rowKey={(item: any) => item.id}
                    emptyMessage={
                        <div className="flex flex-col items-center justify-center gap-3">
                            <span className="bg-brand-secondary-200 rounded-md p-2 inline-block">
                                <Iconify.Icon icon="heroicons:inbox" className="size-8 text-brand-secondary-700" />
                            </span>
                            <p className="text-sm text-white/40 light:text-black/40">No items in this queue. Add items from traces to get started.</p>
                        </div>
                    }
                />
            )}
        </div>
    )
}
