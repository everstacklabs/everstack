import { createFileRoute } from '@tanstack/react-router'
import { useAgent } from '@/hooks/deployments/use-agents'
import { AgentMemoryTab } from '@/components/deployments/agents/agent-memory-tab'

export const Route = createFileRoute('/deployments/agents/$agentId/memory')({
    component: MemoryRoute,
})

function MemoryRoute() {
    const { agentId } = Route.useParams()
    const { data: agent } = useAgent(agentId)

    return (
        <div className="h-full overflow-y-auto p-4 flex flex-col">
            <AgentMemoryTab agentId={agentId} memoryEnabled={(agent?.config as any)?.memory?.enabled === true} />
        </div>
    )
}
