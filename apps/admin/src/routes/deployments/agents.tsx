import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/agents')({
    component: AgentsPage,
})

function AgentsPage() {
    return <Outlet />
}
