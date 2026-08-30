import { createFileRoute } from '@tanstack/react-router'
import { useAgentDeployments } from '@/hooks/deployments/use-agent-deployments'
import { AgentA2aCard } from '@/components/deployments/agents/agent-a2a-card'

export const Route = createFileRoute('/deployments/agents/$agentId/a2a')({
    component: A2aRoute,
})

function A2aRoute() {
    const { agentId } = Route.useParams()
    const { data: deployments } = useAgentDeployments(agentId)
    // Must be an *active* deployment: the A2A/MCP runner resolves the active
    // deployment specifically, so a paused/retired one is not callable.
    const hasDeployment = deployments?.some((d) => d.status === 'active') ?? false

    return (
        <div className="h-full overflow-y-auto p-4 flex flex-col gap-4">
            <AgentA2aCard agentId={agentId} hasDeployment={hasDeployment} />
        </div>
    )
}
