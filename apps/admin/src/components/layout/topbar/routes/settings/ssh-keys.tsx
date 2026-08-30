import { Plus } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'

export const SettingsSSHKeysActions: ActionGroup[] = [
    {
        title: 'SSH Keys',
        actions: [
            {
                type: 'button',
                key: 'add-ssh-key',
                label: 'Add SSH Key',
                icon: Plus,
                variant: 'default',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            }
        ]
    }
]
