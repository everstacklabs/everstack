import { Download } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'

export const SettingsEventsActions: ActionGroup[] = [
    {
        title: 'System Events',
        actions: [
            {
                type: 'button',
                key: 'export-events',
                label: 'Export Events',
                icon: Download,
                variant: 'outline',
                className: 'border-brand-main-600 text-white light:text-brand-main-50 hover:bg-brand-main-800',
                onClick: () => () => {
                    // TODO: Implement export functionality
                    console.log('Export events clicked')
                },
            }
        ]
    }
]

