import { useCollections, useDeleteCollection } from '@/hooks/deployments/use-memory'
import { Button, Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { useNavigate } from '@tanstack/react-router'
import { MemoryServiceError, type MemoryCollection } from '@/server/memory'

interface Props {
    onCreateClick: () => void
}

export function CollectionList({ onCreateClick }: Props) {
    const { data: collections, isLoading, error } = useCollections()
    const deleteMutation = useDeleteCollection()
    const navigate = useNavigate()

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading collections..." />
            </div>
        )
    }

    if (error) {
        const isServiceDown = error instanceof MemoryServiceError && error.detail.includes('connection refused')
        return (
            <div className="flex-1 flex flex-col items-center justify-center pb-24 px-4">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-red-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon
                            icon={isServiceDown ? 'lucide:cloud-off' : 'lucide:alert-triangle'}
                            className="size-8 text-red-400 light:text-red-600"
                        />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                    {isServiceDown ? 'Memory service unavailable' : 'Failed to load collections'}
                </h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">{error.message}</p>
                <Button
                    variant="outline"
                    onClick={() => window.location.reload()}
                >
                    Retry
                </Button>
            </div>
        )
    }

    if (!collections || collections.length === 0) {
        return (
            <div className="flex-1 flex flex-col items-center justify-center pb-24">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon icon="lucide:brain" className="size-8 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No memory collections</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                    Create a collection to start storing vector memories.
                </p>
                <Button variant="default" onClick={onCreateClick}>
                    Create Collection
                </Button>
            </div>
        )
    }

    const columns: ColumnConfig<MemoryCollection>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 200,
            minWidth: 150,
            render: (c) => (
                <span className="text-sm text-brand-secondary-300 font-medium">{c.name}</span>
            ),
        },
        {
            id: 'documents',
            header: 'Documents',
            width: 100,
            minWidth: 80,
            render: (c) => <span className="text-sm text-white/70 light:text-black/70">{c.documentCount}</span>,
        },
        {
            id: 'embeddingModel',
            header: 'Embedding Model',
            width: 200,
            minWidth: 150,
            render: (c) => (
                <span className="text-sm text-white/60 light:text-black/60 font-mono">{c.embeddingModel || 'default'}</span>
            ),
        },
        {
            id: 'distanceMetric',
            header: 'Distance Metric',
            width: 120,
            minWidth: 100,
            render: (c) => (
                <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-indigo-500/20 text-indigo-300 light:text-indigo-600 border border-indigo-500/30">
                    {c.distanceMetric}
                </span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 130,
            render: (c) => (
                <span className="text-sm text-white/50 light:text-black/50">
                    {c.createdAt ? new Date(c.createdAt).toLocaleDateString() : '-'}
                </span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 50,
            minWidth: 50,
            render: (c) => (
                <button
                    onClick={(e) => {
                        e.stopPropagation()
                        if (confirm(`Delete collection "${c.name}"? This cannot be undone.`)) {
                            deleteMutation.mutate(c.name, {
                                onSuccess: () => toast.success(`Collection "${c.name}" deleted`),
                                onError: (err) => toast.error(`Failed to delete: ${err.message}`),
                            })
                        }
                    }}
                    className="p-1 text-white/40 light:text-black/40 hover:text-red-400 light:hover:text-red-600 transition-colors"
                    title="Delete collection"
                >
                    <Iconify.Icon icon="lucide:trash-2" className="w-4 h-4" />
                </button>
            ),
        },
    ]

    return (
        <div className="flex-1 flex flex-col overflow-hidden">
            <ResponsiveTable
                data={collections}
                columns={columns}
                rowKey={(c) => c.id || c.name}
                onRowClick={(c) => navigate({ to: '/deployments/memory/$collectionName', params: { collectionName: c.name } })}
            />
        </div>
    )
}
