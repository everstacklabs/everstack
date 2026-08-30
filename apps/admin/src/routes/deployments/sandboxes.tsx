import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/sandboxes')({
    component: SandboxesLayout,
})

function SandboxesLayout() {
    return <Outlet />
}
