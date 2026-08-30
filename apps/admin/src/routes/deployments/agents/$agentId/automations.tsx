import { createFileRoute } from '@tanstack/react-router'
import { AgentTriggersTab } from '@/components/deployments/agents/agent-triggers-tab'

export const Route = createFileRoute(
  '/deployments/agents/$agentId/automations',
)({
  component: AutomationsRoute,
})

function AutomationsRoute() {
  const { agentId } = Route.useParams()

  return (
    <div className="h-full overflow-y-auto">
      <AgentTriggersTab agentId={agentId} />
    </div>
  )
}
