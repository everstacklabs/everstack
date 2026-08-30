import { createFileRoute } from '@tanstack/react-router'
import { useEffect } from 'react'
import { StudioLayout } from '@/components/deployments/studio'
import { useStudioStore } from '@/stores/studio-store'

export const Route = createFileRoute('/deployments/studio/new')({
    component: NewWorkflowPage,
})

function NewWorkflowPage() {
    const reset = useStudioStore((s) => s.reset)

    useEffect(() => {
        reset()
    }, [reset])

    return <StudioLayout />
}
