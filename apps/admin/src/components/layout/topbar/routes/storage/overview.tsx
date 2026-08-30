import { Plus } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { getApiKeyTypeOptions } from '@/lib/api-key-utils'

export const StorageOverviewActions: ActionGroup[] = [
    {
        title: 'Storage',
        actions: [
            // Search actions
            {
                type: 'search',
                key: 'search-keys',
                label: 'Search',
                searchParam: 'search',
                placeholder: 'Search by name or hash...',
                debounceMs: 300,
            },
            // Filter actions
            {
                type: 'filter',
                key: 'filter-type',
                label: 'Type',
                filterType: 'select',
                storeKey: 'type',
                storeAction: 'setType',
                options: getApiKeyTypeOptions(),
            },
            {
                type: 'filter',
                key: 'filter-status',
                label: 'Status',
                filterType: 'select',
                storeKey: 'status',
                storeAction: 'setStatus',
                options: [
                    { value: 'all', label: 'All Status' },
                    { value: 'active', label: 'Active' },
                    { value: 'expired', label: 'Expired' },
                ],
            },
        ]
    },
    {
        actions: [
            // Button actions
            {
                type: 'button',
                key: 'create-api-key',
                label: 'Create New Key',
                icon: Plus,
                variant: 'default',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            }
        ]
    }
]

