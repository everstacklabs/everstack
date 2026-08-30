import { useLicenseStatus } from '@/hooks/license/use-license-status'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'

dayjs.extend(relativeTime)

export function LicenseLastUpdated() {
    const { dataUpdatedAt } = useLicenseStatus({
        enablePolling: true,
        pollingInterval: 30000,
    })

    return (
        <span className="text-sm text-muted-foreground">
            Updated {dayjs(dataUpdatedAt).fromNow()}
        </span>
    )
}
