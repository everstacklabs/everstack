import { useNavigate } from '@tanstack/react-router'
import { useDeleteWorkflow } from '@/hooks/deployments/use-workflows'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Trash2, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useMemo, useState } from 'react'
import type { Workflow } from '@/server/workflows'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button } = ui

interface WorkflowsTableProps {
    workflows: Workflow[]
}

export function WorkflowsTable({ workflows }: WorkflowsTableProps) {
    const navigate = useNavigate()
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const deleteWorkflowMutation = useDeleteWorkflow()

    const sortedWorkflows = useMemo(() => {
        return [...workflows].sort((a, b) => {
            const aTime = a.createdAt?.seconds ? Number(a.createdAt.seconds) : 0
            const bTime = b.createdAt?.seconds ? Number(b.createdAt.seconds) : 0
            return bTime - aTime
        })
    }, [workflows])

    const handleDelete = async (id: string) => {
        try {
            await deleteWorkflowMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Workflow deleted successfully')
        } catch (error) {
            console.error('Failed to delete workflow:', error)
            toast.error('Failed to delete workflow')
        }
    }

    const handleRowClick = (workflow: Workflow) => {
        navigate({ to: '/deployments/studio/$workflowId', params: { workflowId: workflow.id } })
    }

    const columns: ColumnConfig<Workflow>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 280,
            minWidth: 200,
            render: (w: Workflow) => (
                <div className='flex flex-col'>
                    <span className='truncate text-xs font-medium text-brand-secondary-100'>{w.name}</span>
                    {w.description && (
                        <span className='truncate text-xs text-brand-main-100'>{w.description}</span>
                    )}
                </div>
            ),
        },
        {
            id: 'enabled',
            header: 'Status',
            width: 100,
            minWidth: 80,
            render: (w: Workflow) => (
                <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium border ${
                    w.enabled
                        ? 'bg-emerald-500/20 text-emerald-300 light:text-emerald-600 border-emerald-500/30'
                        : 'bg-amber-500/20 text-amber-300 light:text-amber-700 border-amber-500/30'
                }`}>
                    {w.enabled ? 'Published' : 'Draft'}
                </span>
            ),
        },
        {
            id: 'version',
            header: 'Version',
            width: 80,
            minWidth: 60,
            render: (w: Workflow) => (
                <span className='text-xs text-brand-main-100'>v{w.version ?? 1}</span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 140,
            render: (w: Workflow) => (
                <span className='truncate text-xs text-brand-main-100'>{formatTimestamp(w.createdAt)}</span>
            ),
        },
        {
            id: 'updatedAt',
            header: 'Updated',
            width: 160,
            minWidth: 140,
            render: (w: Workflow) => (
                <span className='truncate text-xs text-brand-main-100'>{formatTimestamp(w.updatedAt)}</span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 50,
            minWidth: 50,
            maxWidth: 50,
            resizable: false,
            render: (w: Workflow) => (
                <div className='flex items-center justify-center'>
                    <button
                        type='button'
                        className='p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors'
                        onClick={(e) => {
                            e.stopPropagation()
                            setDeleteConfirmId(w.id)
                            setDeleteConfirmName(w.name)
                        }}
                        title='Delete workflow'
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={sortedWorkflows}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={
                    <div className="flex flex-col items-center justify-center py-12">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:rectangle-group" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No workflows found</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a workflow to get started.
                        </p>
                    </div>
                }
                onRowClick={handleRowClick}
            />

            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className='w-[500px]'>
                    <DialogTitle>Delete Workflow</DialogTitle>
                    <DialogDescription className='text-brand-main-100'>
                        Are you sure you want to delete <strong className='text-brand-main-100'>{deleteConfirmName}</strong>? This action cannot be undone.
                    </DialogDescription>
                    <div className='flex justify-end gap-3 mt-4'>
                        <Button
                            variant='outline'
                            onClick={() => setDeleteConfirmId(null)}
                            disabled={deleteWorkflowMutation.isPending}
                        >
                            Cancel
                        </Button>
                        <Button
                            variant='destructive'
                            className='bg-destructive/60 text-brand-main-100 hover:bg-destructive/90'
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteWorkflowMutation.isPending}
                        >
                            {deleteWorkflowMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}
