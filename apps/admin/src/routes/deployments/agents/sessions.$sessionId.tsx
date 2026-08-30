import { useEffect } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useSession_ } from '@/hooks/deployments/use-agents'
import { SessionTimeline } from '@/components/deployments/agents'
import { Loader } from '@everstack/ui/components'
import { useAgentSessionStore } from '@/stores/agent-session-store'

export const Route = createFileRoute('/deployments/agents/sessions/$sessionId')({
    component: SessionDetailPage,
})

function SessionDetailPage() {
    const { sessionId } = Route.useParams()
    const { data: session, isLoading, error } = useSession_(sessionId)

    // Clean up store entry when leaving the page, not on terminal status.
    // Removing the session entry while the async CQRS projection hasn't
    // finished writing turns to the read-model causes history to appear lost.
    useEffect(() => {
        return () => {
            useAgentSessionStore.getState().removeSession(sessionId)
        }
    }, [sessionId])

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading session..." />
            </div>
        )
    }

    if (error || !session) {
        return (
            <div className="flex-1 flex flex-col items-center justify-center gap-4">
                <div className="text-red-400 light:text-red-600">{error?.message ??'Session not found'}</div>
                <Link to="/deployments/agents" className="text-sm text-brand-secondary-400 hover:text-brand-secondary-300">
                    Back to agents
                </Link>
            </div>
        )
    }

    return (
        <div className="flex flex-col h-full w-full">
            <div className="flex-1 overflow-hidden">
                <SessionTimeline session={session} />
            </div>
        </div>
    )
}
