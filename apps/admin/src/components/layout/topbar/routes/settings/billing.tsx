import { type ActionGroup } from '@/components/layout/topbar/types'
import { Sparkles } from '@everstack/ui/icons'

export const SettingsBillingActions: ActionGroup[] = [
    {
        title: 'Billing',
    },
    {
        actions: [
            {
                type: 'button',
                key: 'upgrade-plan',
                label: 'Upgrade Plan',
                icon: Sparkles,
                variant: 'default',
                onClick: () => () => {
                    window.dispatchEvent(new CustomEvent('evs:open-upgrade-dialog'))
                },
            }
        ]
    }
]
