import { type ActionGroup } from '@/components/layout/topbar/types'

export const DeploymentsVoiceActions: ActionGroup[] = [
    {
        title: 'Voice Profiles',
        actions: [
            {
                type: 'search',
                key: 'search-voice',
                label: 'Search',
                searchParam: 'search',
                placeholder: 'Search by name or description...',
                debounceMs: 300,
            },
        ]
    },
    {
        actions: [
            {
                type: 'button',
                key: 'create-voice-profile',
                label: 'Create Profile',
                variant: 'default',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            }
        ]
    }
]
