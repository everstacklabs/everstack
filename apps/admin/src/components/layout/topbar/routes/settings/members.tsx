import { Plus } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'

export const SettingsMembersActions: ActionGroup[] = [
    {
        title: 'Team',
        actions: [
            {
                type: 'button',
                key: 'invite-member',
                label: 'Invite Member',
                icon: Plus,
                variant: 'default',
                requiredPermission: 'org:manage_members',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            }
        ]
    },
]
