import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/')({
  beforeLoad: async () => {
    // Perform any necessary data fetching or setup before loading the route
    throw redirect({
      to: '/deployments/studio'
    })
  }
})