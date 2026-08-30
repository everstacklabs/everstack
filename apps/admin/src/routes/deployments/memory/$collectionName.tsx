import { createFileRoute } from '@tanstack/react-router'
import { CollectionDetail } from '@/components/deployments/memory/collection-detail'

export const Route = createFileRoute('/deployments/memory/$collectionName')({
    component: CollectionDetailPage,
})

function CollectionDetailPage() {
    const { collectionName } = Route.useParams()
    return <CollectionDetail collectionName={collectionName} />
}
