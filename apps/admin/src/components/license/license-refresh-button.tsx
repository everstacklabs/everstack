import { useRefreshLicense } from '@/hooks/license/use-license-status'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'

const { Button } = ui

export function LicenseRefreshButton() {
    const refreshMutation = useRefreshLicense()

    const handleRefresh = () => {
        refreshMutation.mutate()
    }

    return (
        <Button
            onClick={handleRefresh}
            disabled={refreshMutation.isPending}
            variant="outline"
            size="sm"
        >
            <Icon
                icon="lucide:refresh-cw"
                className={`mr-2 h-4 w-4 ${refreshMutation.isPending ? 'animate-spin' : ''}`}
            />
            Refresh
        </Button>
    )
}
