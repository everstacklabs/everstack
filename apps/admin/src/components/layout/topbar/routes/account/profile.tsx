import { type ActionGroup } from '@/components/layout/topbar/types'
import { Edit } from '@everstack/ui/icons'

export const AccountProfileActions: ActionGroup[] = [
    {
        title: 'Profile',
        actions: [
            {
                type: 'button',
                key: 'edit-profile',
                label: 'Edit Profile',
                icon: Edit,
                variant: 'default',
                onClick: () => () => {
                    // TODO: Implement edit profile functionality
                    console.log('Edit profile clicked')
                },
            }
        ]
    }
]

