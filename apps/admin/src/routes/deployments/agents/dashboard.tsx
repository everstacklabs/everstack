import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/agents/dashboard')({
  beforeLoad: () => {
    throw redirect({
      to: '/deployments/agents',
      search: { tab: 'runtime', mode: 'all' },
    })
  },
})
