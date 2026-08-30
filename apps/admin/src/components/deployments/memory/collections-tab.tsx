import { useState } from 'react'
import { CollectionList } from './collection-list'
import { CreateCollectionDialog } from './create-collection-dialog'

export function CollectionsTab() {
    const [createOpen, setCreateOpen] = useState(false)

    return (
        <>
            <CollectionList onCreateClick={() => setCreateOpen(true)} />
            <CreateCollectionDialog open={createOpen} onOpenChange={setCreateOpen} />
        </>
    )
}
