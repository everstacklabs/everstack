import { Navigate, createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/agents/$agentId/triggers')({
  component: TriggersRoute,
})

function TriggersRoute() {
  const { agentId } = Route.useParams()

  return (
    <Navigate
      to="/deployments/agents/$agentId/automations"
      params={{ agentId }}
      replace
    />
  )
}
