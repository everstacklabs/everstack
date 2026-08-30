import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/studio')({
  component: StudioLayoutRoute,
})

function StudioLayoutRoute() {
  return <Outlet />
}
