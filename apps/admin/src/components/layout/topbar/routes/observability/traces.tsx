import { Download } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'

export const ObservabilityTracesActions: ActionGroup[] = [
    {
        title: 'Traces',
        actions: [
            {
                type: 'button',
                key: 'export-traces',
                label: 'Export Traces',
                icon: Download,
                variant: 'outline',
                className: 'border-brand-main-600 text-white light:text-brand-main-50 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50',
                onClick: () => () => {
                    // TODO: Implement export functionality
                    console.log('Export traces clicked')
                },
            }
        ]
    }
]

