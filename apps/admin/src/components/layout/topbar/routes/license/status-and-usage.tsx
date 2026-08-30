import { type ActionGroup } from '@/components/layout/topbar/types'
import { Icon } from '@iconify/react'
import { Button } from '@everstack/ui/components'
import { Link } from '@tanstack/react-router'

export const LicenseStatusAndUsageActions: ActionGroup[] = [
    {
        title: 'Status & Usage',
        actions: [
            // Last updated timestamp
            {
                type: 'custom',
                key: 'last-updated',
                label: 'Last Updated',
                component: () => (
                    <Link to="/settings/billing"
                        search={{ upgrade_success: false, plan: undefined }}>
                        <Button variant="outline" className="gap-2">
                            <Icon icon="stash:billing-info-duotone" className="h-4 w-4" />
                            Manage Subscription
                        </Button>
                    </Link>
                )
            },
        ]
    },
]
