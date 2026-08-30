import { type ActionGroup } from '@/components/layout/topbar/types'

export const DeploymentsFunctionsActions: ActionGroup[] = [
    {
        title: 'Functions',
        actions: [
            {
                type: 'search',
                key: 'search-functions',
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
                key: 'create-function',
                requiredPermission: 'resource:create',
                label: 'Create Function',
                variant: 'default',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            }
        ]
    }
]
