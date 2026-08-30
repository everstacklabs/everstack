import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { CreateSandboxPage } from '@/components/deployments/sandbox/create-sandbox-page'

const newSandboxSearchSchema = z.object({
    // When set, the create page pre-fills its form from this existing
    // sandbox so users can edit & recreate. Used by the "Edit configuration"
    // entry on orphan (non-agent-owned) sandboxes — sandboxes have no
    // in-place update RPC, so editing means recreating.
    from: z.string().optional(),
})

export const Route = createFileRoute('/deployments/sandboxes/new')({
    component: NewSandboxRoute,
    validateSearch: newSandboxSearchSchema,
})

function NewSandboxRoute() {
    const { from } = Route.useSearch()
    return <CreateSandboxPage recreateFromSandboxId={from} />
}
