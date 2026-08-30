import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'
import { z } from 'zod'
import { StudioLayout } from '@/components/deployments/studio'
import { useStudioStore } from '@/stores/studio-store'
import { useWorkflow } from '@/hooks/deployments/use-workflows'
import { convertProtoNodesToStudio, convertProtoEdgesToStudio } from '@/components/deployments/studio/utils/convert-workflow'
import { Loader } from '@everstack/ui/components'

const workflowSearchSchema = z.object({
    version: z.coerce.number().int().positive().optional().catch(undefined),
})

export const Route = createFileRoute('/deployments/studio/$workflowId')({
    component: EditWorkflowPage,
    validateSearch: workflowSearchSchema,
})

function EditWorkflowPage() {
    const { workflowId } = Route.useParams()
    const { version: previewVersion } = Route.useSearch()
    const { data: workflow, isLoading, error } = useWorkflow(workflowId)
    const setWorkflowData = useStudioStore((s) => s.setWorkflowData)
    const storeWorkflowId = useStudioStore((s) => s.workflowId)

    // The store is already populated when navigating from a fresh save (toolbar sets it
    // before navigating). Use it directly to avoid a race condition where the backend
    // projection hasn't written the row to the DB yet.
    const storeHasWorkflow = storeWorkflowId === workflowId

    useEffect(() => {
        if (workflow && workflow.id !== storeWorkflowId) {
            const rawNodes = workflow.nodes ?? []
            const nodes = convertProtoNodesToStudio(rawNodes)
            const edges = convertProtoEdgesToStudio(workflow.edges ?? [], rawNodes)
            setWorkflowData({
                id: workflow.id,
                tenantId: workflow.tenantId,
                name: workflow.name,
                description: workflow.description,
                nodes,
                edges,
                viewport: workflow.viewport
                    ? { x: workflow.viewport.x, y: workflow.viewport.y, zoom: workflow.viewport.zoom }
                    : undefined,
                enabled: workflow.enabled ?? true,
                version: workflow.version ?? 1,
            })
        }
    }, [workflow, storeWorkflowId, setWorkflowData])

    if (storeHasWorkflow) {
        return <StudioLayout previewVersion={previewVersion ?? null} />
    }

    if (isLoading) {
        return (
            <div className="flex h-screen items-center justify-center bg-brand-main-950">
                <Loader loaderText="Loading workflow..." />
            </div>
        )
    }

    if (error) {
        return (
            <div className="flex h-screen items-center justify-center bg-brand-main-950 text-red-400 light:text-red-600">
                Error loading workflow: {error.message}
            </div>
        )
    }

    return <StudioLayout previewVersion={previewVersion ?? null} />
}
