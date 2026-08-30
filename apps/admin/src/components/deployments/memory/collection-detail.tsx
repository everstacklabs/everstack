import { useCollection } from '@/hooks/deployments/use-memory'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { QueryPlayground } from './query-playground'

interface Props {
    collectionName: string
}

export function CollectionDetail({ collectionName }: Props) {
    const { data: collection, isLoading, error } = useCollection(collectionName)

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading collection..." />
            </div>
        )
    }

    if (error) {
        return (
            <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
                Error: {error.message}
            </div>
        )
    }

    if (!collection) {
        return (
            <div className="flex-1 flex flex-col items-center justify-center gap-4 text-white/60 light:text-black/60">
                <span className="bg-brand-secondary-200 rounded-md p-2 inline-block">
                    <Iconify.Icon icon="lucide:brain" className="size-10 text-brand-secondary-700" />
                </span>
                <p className="text-lg font-medium text-white/80 light:text-black/80">Collection not found</p>
            </div>
        )
    }

    return (
        <div className="flex flex-col h-full w-full overflow-auto">
            {/* Metadata Card */}
            <div className="px-4 py-3">
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                    <MetadataItem label="Documents" value={String(collection.documentCount)} />
                    <MetadataItem label="Embedding Model" value={collection.embeddingModel || 'default'} />
                    <MetadataItem label="Dimension" value={String(collection.embeddingDimension)} />
                    <MetadataItem label="Distance Metric" value={collection.distanceMetric} />
                </div>
            </div>

            {/* Query Playground */}
            <div className="flex-1 px-4 py-3 min-h-0">
                <QueryPlayground collectionName={collectionName} />
            </div>
        </div>
    )
}

function MetadataItem({ label, value }: { label: string; value: string }) {
    return (
        <div className="bg-brand-main-800/50 border border-brand-main-700 rounded-lg p-3">
            <div className="text-xs text-white/40 light:text-black/40 uppercase tracking-wider">{label}</div>
            <div className="text-sm text-white/80 light:text-black/80 mt-1 font-mono">{value}</div>
        </div>
    )
}
