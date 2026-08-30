import { createFileRoute } from '@tanstack/react-router'
import { useAgent } from '@/hooks/deployments/use-agents'
import { AgentDeploymentsTab } from '@/components/deployments/agents/agent-deployments-tab'

export const Route = createFileRoute('/deployments/agents/$agentId/api')({
    component: ApiRoute,
})

function ApiRoute() {
    const { agentId } = Route.useParams()
    const { data: agent } = useAgent(agentId)

    return (
        <div className="h-full overflow-y-auto p-4 flex flex-col gap-4">
            <AgentDeploymentsTab agentId={agentId} agentName={agent?.name ?? ''} />
        </div>
    )
}
