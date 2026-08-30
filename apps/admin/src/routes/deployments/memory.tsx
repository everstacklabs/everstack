import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/memory')({
    component: MemoryLayout,
})

function MemoryLayout() {
    return <Outlet />
}
