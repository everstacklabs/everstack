import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useWorkflows } from '@/hooks/deployments/use-workflows'
import { WorkflowsTable } from '@/components/deployments/studio/list/workflows-table'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader } from '@everstack/ui/components'

export const Route = createFileRoute('/deployments/studio/')({
    component: StudioListPage,
})

function StudioListPage() {
    const { data: workflows = [], isLoading, error } = useWorkflows()
    const navigate = useNavigate()

    return (
        <div className='flex flex-col h-full w-full'>
            <div className='min-h-0 h-full justify-center items-center overflow-hidden flex flex-col'>
                {isLoading ? (
                    <div className='flex-1 flex items-center justify-center text-white/70 light:text-black/70'>
                        <Loader loaderText='Loading workflows...' />
                    </div>
                ) : error ? (
                    <div className='flex-1 flex items-center justify-center text-red-400 light:text-red-600'>
                        Error loading workflows: {error.message}
                    </div>
                ) : workflows.length === 0 ? (
                    <div className='flex-1 flex flex-col h-full items-center justify-center pb-24'>
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:rectangle-group" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No workflows yet</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                            Create visual workflows to orchestrate your AI gateway pipeline with drag-and-drop nodes.
                        </p>
                        <Button variant='default' onClick={() => navigate({ to: '/deployments/studio/new' })}>
                            <div className='flex items-center gap-2'>
                                <Iconify.Icon icon="heroicons:plus" className='size-4' />
                                Create Workflow
                            </div>
                        </Button>
                    </div>
                ) : (
                    <WorkflowsTable workflows={workflows} />
                )}
            </div>
        </div>
    )
}
