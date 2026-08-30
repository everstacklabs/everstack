import { Route } from '@/routes/observability/logs'
import { ObservabilityControls } from '@/components/common/observability-controls'

interface LogsControlsProps {
    isLoading?: boolean
    onRefresh?: () => void
}

export function LogsControls({ isLoading, onRefresh }: LogsControlsProps) {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()

    // Parse URL params
    const isLiveMode = search.live === 'true'
    const timeRange = search.range as any

    return (
        <ObservabilityControls
            search={search}
            navigate={navigate}
            isLiveMode={isLiveMode}
            timeRange={timeRange}
            searchPlaceholder="Search Logs..."
            isLoading={isLoading}
            onRefresh={onRefresh}
        />
    )
}
