import { createFileRoute } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { streamEvents } from '@/server/events'
import type { Event } from '@everstack/proto/everstack/events/v1/events_service_pb'
import { EventsTable } from '@/components/events/events-table'
import { EventsControls } from '@/components/events/events-controls'
import { TIME_RANGE_LABELS, calculateTimeRange } from '@/lib/time-ranges'
import { z } from 'zod'
import type { TimeRangePreset } from '@/stores/logs-store'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner, FeatureGateInfoBanner } from '@/components/ee/feature-gate-banner'

// URL search params schema
const eventsSearchSchema = z.object({
    range: z.enum(Object.keys(TIME_RANGE_LABELS) as [TimeRangePreset, ...TimeRangePreset[]]).optional().default('24h'),
    from: z.string().optional(),
    to: z.string().optional(),
    query: z.string().optional(),
})

export const Route = createFileRoute('/settings/events')({
    component: Events,
    validateSearch: eventsSearchSchema,
})

function Events() {
    const gate = useFeatureGate(FeatureKey.AUDIT_LOGS)

    // CE or cloud free/basic: show full-page upgrade prompt
    if (gate.isBlocked) {
        return (
            <div className="flex flex-col h-screen w-full overflow-hidden">
                <FeatureGateBanner
                    featureName="Audit Logs"
                    description="Track all system events and actions across your organization. Monitor API calls, configuration changes, and security events in real time."
                    requiredTier="Pro"
                    upgradeUrl={gate.upgradeUrl}
                    isCE={gate.isCE}
                />
            </div>
        )
    }

    // Pro tier: limited to 24h, Enterprise: full access
    const maxRange = gate.tier === 'pro' ? '24h' : undefined

    return <EventsContent maxRange={maxRange} gate={gate} />
}

function EventsContent({ maxRange, gate }: { maxRange?: string; gate: ReturnType<typeof useFeatureGate> }) {
    const qc = useQueryClient()
    const search = Route.useSearch()

    // For Pro tier, force max 24h range
    const effectiveRange = useMemo(() => {
        const raw = (search.range || '24h') as TimeRangePreset
        if (!maxRange) return raw
        // If user selected a range larger than 24h, clamp to 24h
        const rangeOrder: TimeRangePreset[] = ['15m', '6h', '12h', '24h', '3d', '7d', '14d', '30d', '90d']
        const maxIdx = rangeOrder.indexOf(maxRange as TimeRangePreset)
        const curIdx = rangeOrder.indexOf(raw)
        if (curIdx > maxIdx && raw !== 'custom') return maxRange as TimeRangePreset
        return raw
    }, [search.range, maxRange])

    const timeRange = effectiveRange

    const customRange = useMemo(() => {
        if (search.from && search.to) {
            return {
                start: new Date(search.from),
                end: new Date(search.to)
            }
        }
        return null
    }, [search.from, search.to])

    // Calculate time range
    const { stableFrom, stableTo } = useMemo(() => {
        if (customRange) {
            return {
                stableFrom: customRange.start.toISOString(),
                stableTo: customRange.end.toISOString()
            }
        }

        const now = new Date()
        const result = calculateTimeRange(timeRange, null)

        return {
            stableFrom: result.from,
            stableTo: now.toISOString()
        }
    }, [timeRange, customRange])

    const queryKey = useMemo(() => ['events', { from: stableFrom, to: stableTo }], [stableFrom, stableTo])

    // Fetch events — stream never closes (server keeps connection open),
    // so we push data into the cache progressively as it arrives.
    const { data: events = [], refetch, error } = useQuery<Event[]>({
        queryKey,
        queryFn: async ({ signal }) => {
            const acc: Event[] = []
            const seen = new Set<string>()
            try {
                for await (const ev of streamEvents({
                    pageSize: 500,
                    from: stableFrom,
                    to: stableTo,
                }, { signal })) {
                    if (!ev.id || seen.has(ev.id)) continue
                    seen.add(ev.id)
                    acc.push(ev)
                    qc.setQueryData(queryKey, acc.slice())
                }
            } catch (err) {
                if (err instanceof Error && err.name === 'AbortError') return acc
                throw err
            }
            return acc
        },
        refetchOnWindowFocus: false,
        refetchOnMount: true,
        staleTime: 30000,
    })

    // Client-side sorting (newest first)
    const sortedEvents = useMemo(() => {
        return [...events]
            .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
    }, [events])

    const showLoading = events.length === 0 && !error

    return (
        <div className="flex flex-col h-screen w-full overflow-hidden">
            {gate.tier === 'pro' && (
                <FeatureGateInfoBanner
                    message="Scale shows the last 24 hours of events. Upgrade to Enterprise for full history."
                    upgradeUrl={gate.upgradeUrl}
                />
            )}
            <EventsControls
                isLoading={showLoading}
                onRefresh={() => { refetch({ cancelRefetch: false }) }}
                maxRange={maxRange}
            />
            <div className="flex-1 min-h-0 overflow-hidden flex flex-col">
                <EventsTable
                    pageEvents={sortedEvents}
                    isLoading={showLoading}
                    error={error}
                />
            </div>
        </div>
    )
}
