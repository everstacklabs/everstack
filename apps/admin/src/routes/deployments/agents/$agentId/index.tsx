import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/deployments/agents/$agentId/')({
    beforeLoad: async ({ params }) => {
        // Default to chat; the chat route itself handles the ephemeral → overview redirect.
        throw redirect({
            to: '/deployments/agents/$agentId/chat',
            params: { agentId: params.agentId },
        })
    },
    component: () => null,
})
