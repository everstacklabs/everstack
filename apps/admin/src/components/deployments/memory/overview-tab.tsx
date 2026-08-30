import { useState, useMemo } from 'react'
import { useCollections, useMemoryAnalytics } from '@/hooks/deployments/use-memory'
import { Button, Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { MemoryAnalyticsChart } from './memory-analytics-chart'
import { MemoryServiceError } from '@/server/memory'

type TimeRange = '1h' | '6h' | '24h' | '7d' | '30d'

const TIME_RANGES: { key: TimeRange; label: string }[] = [
    { key: '1h', label: '1h' },
    { key: '6h', label: '6h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
]

function getTimeRange(range: TimeRange): { from: string; to: string } {
    const now = new Date()
    const to = now.toISOString()
    const ms: Record<TimeRange, number> = {
        '1h': 3600000,
        '6h': 21600000,
        '24h': 86400000,
        '7d': 604800000,
        '30d': 2592000000,
    }
    const from = new Date(now.getTime() - ms[range]).toISOString()
    return { from, to }
}

export function OverviewTab() {
    const { data: collections, isLoading, error } = useCollections()
    const [timeRange, setTimeRange] = useState<TimeRange>('24h')

    const { from, to } = useMemo(() => getTimeRange(timeRange), [timeRange])
    const { data: analytics, isLoading: analyticsLoading } = useMemoryAnalytics(from, to)

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70 -mt-20">
                <Loader loaderText="Loading memory overview..." />
            </div>
        )
    }

    if (error) {
        const isServiceDown = error instanceof MemoryServiceError && error.detail.includes('connection refused')
        return (
            <div className="flex-1 flex flex-col items-center justify-center pb-24 px-4">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-red-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon
                            icon={isServiceDown ? 'lucide:cloud-off' : 'lucide:alert-triangle'}
                            className="size-8 text-red-400 light:text-red-600"
                        />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                    {isServiceDown ? 'Memory service unavailable' : 'Failed to load overview'}
                </h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">{error.message}</p>
                <Button
                    variant="outline"
                    onClick={() => window.location.reload()}
                >
                    Retry
                </Button>
            </div>
        )
    }

    if (!collections || collections.length === 0) {
        return (
            <div className="flex-1 flex flex-col items-center justify-center pb-24">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon icon="lucide:brain" className="size-8 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No memory collections yet</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                    Create a collection to start storing vector memories and see stats here.
                </p>
            </div>
        )
    }

    const totalDocs = collections.reduce((acc, c) => acc + c.documentCount, 0)
    const embeddingModels = new Set(collections.map((c) => c.embeddingModel || 'default'))
    const metricCounts = collections.reduce<Record<string, number>>((acc, c) => {
        const m = c.distanceMetric || 'cosine'
        acc[m] = (acc[m] || 0) + 1
        return acc
    }, {})
    const metricSummary = Object.entries(metricCounts)
        .map(([m, count]) => `${m} (${count})`)
        .join(', ')

    const errorRate = analytics && analytics.totalRequests > 0
        ? ((analytics.totalErrors / analytics.totalRequests) * 100).toFixed(1)
        : '0.0'

    return (
        <div className="px-4 space-y-6 overflow-y-auto">
            {/* Summary Stats */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <StatCard label="Total Collections" value={String(collections.length)} />
                <StatCard label="Total Documents" value={totalDocs.toLocaleString()} />
                <StatCard
                    label="Embedding Models"
                    value={String(embeddingModels.size)}
                    subtitle={[...embeddingModels].join(', ')}
                />
                <StatCard label="Distance Metrics" value={String(Object.keys(metricCounts).length)} subtitle={metricSummary} />
            </div>

            {/* Usage Analytics */}
            <div className="space-y-3">
                <div className="flex items-center justify-between">
                    <h3 className="text-sm font-medium text-white/70 light:text-black/70">Usage Analytics</h3>
                    <div className="flex gap-1 bg-brand-main-800/50 border border-brand-main-600 rounded-md p-0.5">
                        {TIME_RANGES.map(({ key, label }) => (
                            <button
                                key={key}
                                onClick={() => setTimeRange(key)}
                                className={`px-2.5 py-1 text-xs rounded transition-colors ${timeRange === key
                                    ? 'bg-brand-secondary-600 text-white'
                                    : 'text-white/50 light:text-black/50 hover:text-white/80 light:hover:text-black/80'
                                    } light:hover:text-black/80`}
                            >
                                {label}
                            </button>
                        ))}
                    </div>
                </div>

                <div className="border border-brand-main-600 rounded-lg overflow-hidden">
                    {analyticsLoading ? (
                        <div className="flex items-center justify-center h-[150px] text-white/40 light:text-black/40">
                            <Loader loaderText="Loading analytics..." />
                        </div>
                    ) : (
                        <MemoryAnalyticsChart buckets={analytics?.buckets ?? []} />
                    )}
                </div>

                {/* Analytics stat cards */}
                <div className="grid grid-cols-3 gap-4">
                    <StatCard
                        label="Requests"
                        value={analytics?.totalRequests?.toLocaleString() ?? '0'}
                    />
                    <StatCard
                        label="Avg Latency"
                        value={analytics ? `${analytics.avgLatencyMs.toFixed(0)}ms` : '—'}
                    />
                    <StatCard
                        label="Error Rate"
                        value={`${errorRate}%`}
                    />
                </div>
            </div>

            {/* Top Collections */}
            {analytics && analytics.topCollections.length > 0 && (
                <div className="space-y-2">
                    <h3 className="text-sm font-medium text-white/70 light:text-black/70">Top Collections</h3>
                    <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg divide-y divide-brand-main-600">
                        {analytics.topCollections.map((c) => (
                            <div key={c.collectionName} className="flex items-center justify-between px-3 py-2">
                                <span className="text-sm text-white light:text-brand-main-50 truncate">{c.collectionName}</span>
                                <span className="text-xs text-white/50 light:text-black/50 ml-2 shrink-0">{c.requestCount} requests</span>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </div>
    )
}

function StatCard({ label, value, subtitle }: { label: string; value: string; subtitle?: string }) {
    return (
        <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-3">
            <p className="text-xs text-white/50 light:text-black/50 mb-1">{label}</p>
            <p className="text-xl font-bold text-white light:text-brand-main-50 truncate">{value}</p>
            {subtitle && <p className="text-[11px] text-white/30 light:text-black/30 truncate">{subtitle}</p>}
        </div>
    )
}
