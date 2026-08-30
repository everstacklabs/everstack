import { useMemo } from 'react'
import { Route } from '@/routes/settings/events'
import { ObservabilityControls } from '@/components/common/observability-controls'
import type { TimeRangePreset } from '@/stores/logs-store'

interface EventsControlsProps {
    isLoading?: boolean
    onRefresh?: () => void
    /** When set (e.g. "24h"), disable time range options beyond this value */
    maxRange?: string
}

// Ordered from shortest to longest
const RANGE_ORDER: TimeRangePreset[] = ['15m', '6h', '12h', '24h', '3d', '7d', '14d', '30d', '90d', 'custom']

export function EventsControls({ isLoading, onRefresh, maxRange }: EventsControlsProps) {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()

    const timeRange = search.range as any

    // If maxRange is set, override navigate to clamp range selections
    const wrappedNavigate = useMemo(() => {
        if (!maxRange) return navigate
        const maxIdx = RANGE_ORDER.indexOf(maxRange as TimeRangePreset)

        return (options: any) => {
            const newRange = options?.search?.range as TimeRangePreset | undefined
            if (newRange && newRange !== 'custom') {
                const curIdx = RANGE_ORDER.indexOf(newRange)
                if (curIdx > maxIdx) {
                    // Clamp to maxRange
                    return navigate({
                        ...options,
                        search: { ...options.search, range: maxRange },
                    })
                }
            }
            return navigate(options)
        }
    }, [navigate, maxRange])

    return (
        <ObservabilityControls
            search={search}
            navigate={wrappedNavigate}
            showLiveToggle={false}
            isLiveMode={false}
            timeRange={timeRange}
            searchPlaceholder="Search Events..."
            isLoading={isLoading}
            onRefresh={onRefresh}
        />
    )
}
