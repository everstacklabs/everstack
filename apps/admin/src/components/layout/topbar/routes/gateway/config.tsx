import { type ActionGroup } from '@/components/layout/topbar/types'
import { GatewayConfigSaveActions } from './config-actions'

export const GatewayConfigActions: ActionGroup[] = [
    {
        title: 'Configuration',
        actions: [
            {
                type: 'custom',
                key: 'gateway-config-save-actions',
                label: 'Save Actions',
                component: GatewayConfigSaveActions,
            }
        ]
    }
]

