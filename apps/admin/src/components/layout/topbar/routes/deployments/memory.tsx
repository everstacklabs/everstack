import { useState } from 'react'
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { AddDocumentsDialog } from '@/components/deployments/memory/add-documents-dialog'
import { useDeleteCollection } from '@/hooks/deployments/use-memory'

function MemoryBreadcrumb() {
    const { pathname } = useLocation()
    const segments = pathname.split('/').filter(Boolean)
    // pathname: /deployments/memory/{collectionName}
    const collectionName = segments.length > 2 ? decodeURIComponent(segments[2]) : ''

    return (
        <div className="flex items-center gap-2">
            {/* {collectionName && (
                <Button
                    variant="ghost"
                    onClick={() => navigate({ to: '/deployments/memory' })}
                    className="h-7 w-7 mr-2"
                >
                    <Iconify.Icon icon="lucide:arrow-left" className="w-3 h-3" />
                </Button>
            )} */}
            <Link to="/deployments/memory" search={{ tab: 'collections' }} className="text-sm font-semibold text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors">
                Memory
            </Link>
            {collectionName && (
                <>
                    <span className="text-white/30 light:text-black/30 text-sm">/</span>
                    <span className="text-sm text-white light:text-brand-main-50 font-normal">
                        {collectionName}
                    </span>
                </>
            )}
        </div>
    )
}

function AddDocumentsButton() {
    const { pathname } = useLocation()
    const collectionName = decodeURIComponent(pathname.split('/').filter(Boolean)[2] ?? '')
    const [open, setOpen] = useState(false)

    return (
        <>
            <Button variant="default" onClick={() => setOpen(true)}>
                Add Documents
            </Button>
            {collectionName && (
                <AddDocumentsDialog
                    collectionName={collectionName}
                    open={open}
                    onOpenChange={setOpen}
                />
            )}
        </>
    )
}

function DeleteCollectionButton() {
    const { pathname } = useLocation()
    const navigate = useNavigate()
    const collectionName = decodeURIComponent(pathname.split('/').filter(Boolean)[2] ?? '')
    const deleteMutation = useDeleteCollection()

    const handleDelete = () => {
        if (!confirm(`Delete collection "${collectionName}"? This cannot be undone.`)) return
        deleteMutation.mutate(collectionName, {
            onSuccess: () => {
                toast.success(`Collection "${collectionName}" deleted`)
                navigate({ to: '/deployments/memory' })
            },
            onError: (err) => toast.error(`Failed to delete: ${err.message}`),
        })
    }

    return (
        <Button variant="destructive" size={"default"} onClick={handleDelete} disabled={deleteMutation.isPending}>
            {deleteMutation.isPending ? 'Deleting...' : 'Delete Collection'}
        </Button>
    )
}

export const DeploymentsMemoryActions: ActionGroup[] = [
    {
        title: 'Memory',
        actions: [],
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'create-collection',
                label: 'Create Collection',
                component: CreateCollectionButton,
            },
        ],
    },
]

function CreateCollectionButton() {
    const navigate = useNavigate()
    return (
        <Button
            variant="default"
            size="default"
            onClick={() => navigate({ to: '/deployments/memory', search: { tab: 'collections' } })}
        >
            Create Collection
        </Button>
    )
}

export const DeploymentsMemoryDetailActions: ActionGroup[] = [
    {
        title: <MemoryBreadcrumb />,
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'add-documents',
                label: 'Add Documents',
                component: AddDocumentsButton,
            },
            {
                type: 'custom',
                key: 'delete-collection',
                label: 'Delete Collection',
                component: DeleteCollectionButton,
            },
        ],
    },
]
