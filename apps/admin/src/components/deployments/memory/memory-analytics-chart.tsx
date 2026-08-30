import { ui } from '@everstack/ui'
import { useMemo } from 'react'
import dayjs from 'dayjs'
import type { AnalyticsBucket } from '@/server/memory'

const { EChart, brandTooltip, categoryAxis, valueAxis, baseGrid, BRAND_PALETTE, SEMANTIC, useChartMode } = ui

interface MemoryAnalyticsChartProps {
    buckets: AnalyticsBucket[]
}

interface ChartPoint {
    timestamp: number
    date: string
    query: number
    store: number
    delete: number
    error: number
}

function getTickFormat(buckets: AnalyticsBucket[]): string {
    if (buckets.length < 2) return 'HH:mm'
    const first = new Date(buckets[0].timestamp).getTime()
    const last = new Date(buckets[buckets.length - 1].timestamp).getTime()
    const duration = last - first
    if (duration <= 86400000) return 'HH:mm'
    if (duration <= 604800000) return 'MMM D HH:mm'
    return 'MMM D'
}

export function MemoryAnalyticsChart({ buckets }: MemoryAnalyticsChartProps) {
    // BRAND_PALETTE/SEMANTIC resolve against the current theme at read time;
    // `mode` in the memo deps rebuilds the option on theme toggle.
    const mode = useChartMode()
    const chartData = useMemo<ChartPoint[]>(() => {
        return buckets.map((b) => {
            const ts = new Date(b.timestamp).getTime()
            return {
                timestamp: ts,
                date: dayjs(ts).format('MMM D HH:mm'),
                query: b.queryCount ?? 0,
                store: b.storeCount ?? 0,
                delete: b.deleteCount ?? 0,
                error: b.errorCount ?? 0,
            }
        })
    }, [buckets])

    const tickFormat = useMemo(() => getTickFormat(buckets), [buckets])

    // query/store/delete/error mapped onto brand tokens (canvas can't read the
    // CSS vars the old recharts config used). Error is the semantic red.
    const SERIES = useMemo(
        () =>
            [
                { key: 'query', name: 'Queries', color: BRAND_PALETTE[0] },
                { key: 'store', name: 'Stores', color: BRAND_PALETTE[1] },
                { key: 'delete', name: 'Deletes', color: BRAND_PALETTE[3] },
                { key: 'error', name: 'Errors', color: SEMANTIC.error },
            ] as const,
        // BRAND_PALETTE/SEMANTIC are theme-aware getters; rebuild on toggle.
        [mode],
    )

    const option = useMemo(() => {
        const lastIdx = SERIES.length - 1
        return {
            grid: baseGrid({ left: 4, right: 4, top: 12, bottom: 4 }),
            tooltip: brandTooltip({
                hideZero: true,
                headerFormatter: (v) => dayjs(Number(v)).format('MMM D, HH:mm'),
            }),
            xAxis: categoryAxis(
                chartData.map((d) => d.timestamp),
                {
                    axisLabel: {
                        hideOverlap: true,
                        formatter: (v: number) => dayjs(Number(v)).format(tickFormat),
                    },
                },
            ),
            yAxis: valueAxis(undefined, { position: 'left' }),
            series: SERIES.map((s, i) => ({
                name: s.name,
                type: 'bar',
                stack: 'ops',
                barWidth: 8,
                data: chartData.map((d) => d[s.key]),
                itemStyle: {
                    color: s.color,
                    borderRadius: i === lastIdx ? [4, 4, 0, 0] : [0, 0, 0, 0],
                },
            })),
        }
    }, [chartData, tickFormat, SERIES])

    if (chartData.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center h-[150px]">
                <p className="text-sm text-white/40 light:text-black/40">No analytics data for this time range.</p>
            </div>
        )
    }

    return (
        <div className="w-full bg-brand-main-950 select-none">
            <div className="py-4 px-2">
                <EChart option={option} height={150} />
            </div>
        </div>
    )
}
