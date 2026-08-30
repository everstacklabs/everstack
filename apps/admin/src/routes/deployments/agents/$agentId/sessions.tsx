import { createFileRoute } from '@tanstack/react-router'
import { useSessions } from '@/hooks/deployments/use-agents'
import { SessionsList } from '@/components/deployments/agents'
import { Iconify } from '@everstack/ui/icons'

export const Route = createFileRoute('/deployments/agents/$agentId/sessions')({
    component: SessionsRoute,
})

function SessionsRoute() {
    const { agentId } = Route.useParams()
    const { data: sessions = [] } = useSessions({ agentId })

    return (
        <div className="h-full overflow-y-auto p-4 flex flex-col">
            {sessions.length > 0 ? (
                <SessionsList sessions={sessions} />
            ) : (
                <div className="flex flex-col items-center justify-center flex-1">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="heroicons:chat-bubble-left-right" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sessions yet</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        Sessions are created when the agent is run via the API or chat.
                    </p>
                </div>
            )}
        </div>
    )
}
