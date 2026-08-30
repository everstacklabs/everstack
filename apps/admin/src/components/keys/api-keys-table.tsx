import { useApiKeys, useDeleteApiKey } from '@/hooks/vault/use-api-keys'
import { type ApiKey } from '@/server/api-keys'
import { useApiKeyFilters } from '@/stores/filters/api-keys-filters'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'
import { RotateCcw, Trash2, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { formatTimestamp, truncateString } from '@everstack/utils/functions/index'
import { useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button } = ui

export const ApiKeysTable = ({ apiKeys }: { apiKeys: ApiKey[] }) => {
    // const [copiedId, setCopiedId] = useState<string | null>(null)
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const deleteApiKeyMutation = useDeleteApiKey()
    const listApiKeysQuery = useApiKeys()

    // Get search and filter state
    const search = useSearch({ strict: false })
    const filters = useApiKeyFilters()

    // Apply search and filters to the data
    const filteredApiKeys = useMemo(() => {
        let filtered = [...apiKeys]

        // Apply search filter
        const searchTerm = (search as any)?.search?.toLowerCase()
        if (searchTerm) {
            filtered = filtered.filter(apiKey =>
                apiKey.name.toLowerCase().includes(searchTerm) ||
                apiKey.hash.toLowerCase().includes(searchTerm)
            )
        }

        // Apply type filter
        if (filters.type !== 'all') {
            filtered = filtered.filter(apiKey => apiKey.type === filters.type)
        }

        // Apply status filter (for now, we'll assume all are active)
        if (filters.status !== 'all') {
            // TODO: Implement actual status checking when available
            // For now, all keys are considered active
        }

        // Apply sorting
        filtered.sort((a, b) => {
            let comparison = 0
            switch (filters.sortBy) {
                case 'name':
                    comparison = a.name.localeCompare(b.name)
                    break
                case 'type':
                    comparison = a.type - b.type
                    break
                case 'createdAt':
                    const aTime = a.createdAt?.seconds ? (typeof a.createdAt.seconds === 'bigint' ? safeBigIntToNumber(a.createdAt.seconds) : Number(a.createdAt.seconds)) : 0
                    const bTime = b.createdAt?.seconds ? (typeof b.createdAt.seconds === 'bigint' ? safeBigIntToNumber(b.createdAt.seconds) : Number(b.createdAt.seconds)) : 0
                    comparison = aTime - bTime
                    break
            }
            return filters.sortOrder === 'asc' ? comparison : -comparison
        })

        return filtered
    }, [apiKeys, search, filters])


    const handleRegenerate = async () => {
        try {
            // For now, show a message that this feature is coming soon
            toast.info('Regenerate feature coming soon! For now, delete this key and create a new one.')
            listApiKeysQuery.refetch()
        } catch (error) {
            console.error('Failed to regenerate API key:', error)
            toast.error('Failed to regenerate API key')
        }
    };

    const handleDelete = async (id: string) => {
        try {
            await deleteApiKeyMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            toast.success('API key deleted successfully!')
            listApiKeysQuery.refetch()
        } catch (error) {
            console.error('Failed to delete API key:', error)
            toast.error('Failed to delete API key')
        }
    };

    const getTypeLabel = (type: ApiKeyType) => {
        switch (type) {
            case ApiKeyType.USER:
                return 'User Account'
            case ApiKeyType.ORG:
                return 'Service Account'
            default:
                return 'Unknown'
        }
    }

    const getTypeBadgeColor = (type: ApiKeyType) => {
        switch (type) {
            case ApiKeyType.USER:
                return 'bg-blue-500/20 text-blue-300 light:text-blue-600'
            case ApiKeyType.ORG:
                return 'bg-purple-500/20 text-purple-300 light:text-purple-600'
            default:
                return 'bg-gray-500/20 text-gray-300 light:text-gray-700'
        }
    }

    const columns: ColumnConfig<ApiKey>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 200,
            minWidth: 150,
            render: (apiKey: ApiKey) => (
                <span className='truncate'>{apiKey.name}</span>
            )
        },
        {
            id: 'type',
            header: 'Type',
            width: 140,
            minWidth: 120,
            render: (apiKey: ApiKey) => (
                <span className={`px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap ${getTypeBadgeColor(apiKey.type)}`}>
                    {getTypeLabel(apiKey.type)}
                </span>
            )
        },
        {
            id: 'apiKey',
            header: 'API Key',
            width: 300,
            minWidth: 250,
            render: (apiKey: ApiKey) => (
                <div className='flex items-center justify-start gap-2 w-full min-w-0'>
                    <div className='min-w-0 flex-1'>
                        <span className='font-mono text-xs block truncate text-white/50 light:text-black/50'>
                            {apiKey.sensitiveId
                                ? truncateString(apiKey.sensitiveId)
                                : 'N/A'}
                        </span>
                    </div>
                </div>
            )
        },
        {
            id: 'createdAt',
            header: 'Created At',
            width: 180,
            minWidth: 140,
            render: (apiKey: ApiKey) => (
                <span className='truncate text-sm text-white/70 light:text-black/70'>{formatTimestamp(apiKey.createdAt)}</span>
            )
        },
        {
            id: 'createdBy',
            header: 'Created By',
            width: 150,
            minWidth: 120,
            render: (apiKey: ApiKey) => (
                <span className='truncate text-sm text-white/70 light:text-black/70'>{apiKey.userId || 'System'}</span>
            )
        },
        {
            id: 'actions',
            header: '',
            width: 80,
            minWidth: 80,
            maxWidth: 80,
            resizable: false,
            render: (apiKey: ApiKey) => (
                <div className='flex items-center gap-2 justify-center'>
                    <button
                        type='button'
                        className='p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors'
                        onClick={handleRegenerate}
                        title='Regenerate API key (creates new key, revokes old one)'
                    >
                        <RotateCcw size={14} />
                    </button>
                    <button
                        type='button'
                        className='p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors'
                        onClick={() => setDeleteConfirmId(apiKey.id)}
                        title='Delete API key'
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            )
        }
    ];

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={filteredApiKeys}
                enableResizing={false}
                minTableWidth="100%"
                emptyMessage={apiKeys.length === 0 ? 'No API keys found.' : 'No API keys match your filters.'}
            />

            {/* Delete Confirmation Dialog */}
            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className='w-[500px]'>
                    <DialogTitle>Delete API Key</DialogTitle>
                    <DialogDescription className='text-white/70 light:text-black/70'>
                        Are you sure you want to delete this API key? This action cannot be undone and any applications using this key will lose access.
                    </DialogDescription>
                    <div className='flex justify-end gap-3 mt-4'>
                        <Button
                            variant='outline'
                            onClick={() => setDeleteConfirmId(null)}
                            disabled={deleteApiKeyMutation.isPending}
                        >
                            Cancel
                        </Button>
                        <Button
                            variant='destructive'
                            className='bg-destructive/60 text-white hover:bg-destructive/90 light:text-brand-main-50'
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteApiKeyMutation.isPending}
                        >
                            {deleteApiKeyMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}