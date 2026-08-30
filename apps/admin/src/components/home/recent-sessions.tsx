import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { MessageSquare } from 'lucide-react'
import { listSessions } from '@/server/agents'
import { useAgents } from '@/hooks/deployments/use-agents'

export function RecentSessions() {
  const navigate = useNavigate()
  const { data: agents } = useAgents({ enabled: true, limit: 50 })

  // Build a map of agent names for display
  const agentNames = new Map<string, string>()
  for (const a of agents ?? []) {
    agentNames.set(a.id, a.name)
  }

  // Fetch recent sessions across all agents
  const { data: sessionsResp, isLoading } = useQuery({
    queryKey: ['recent-sessions'],
    queryFn: () => listSessions({ tenantId: '', limit: 3 }),
    staleTime: 30_000,
  })

  const sessions = sessionsResp?.sessions ?? []

  if (isLoading) {
    return (
      <div className="space-y-2">
        <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
          Recent sessions
        </h3>
        <div className="space-y-1">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="h-10 animate-pulse rounded-md border border-brand-main-600 bg-brand-main-800/30"
            />
          ))}
        </div>
      </div>
    )
  }

  if (!sessions.length) {
    return (
      <div className="space-y-2">
        <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
          Recent sessions
        </h3>
        <p className="text-xs text-white/30 light:text-black/30">No recent conversations.</p>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
        Recent sessions
      </h3>
      <div className="space-y-0.5">
        {sessions.map((session) => {
          const agentName =
            agentNames.get(session.agentId) ?? 'Unknown agent'
          const preview =
            session.summary ||
            (session.turns?.[0]?.userInput?.slice(0, 60)) ||
            'Empty session'

          return (
            <button
              key={session.id}
              onClick={() =>
                navigate({
                  to: `/deployments/agents/${session.agentId}/chat`,
                })
              }
              className="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-brand-main-800/60 group"
            >
              <MessageSquare className="size-3.5 shrink-0 text-white/20 light:text-black/20 group-hover:text-white/35 light:group-hover:text-black/35" />
              <div className="min-w-0 flex-1">
                <p className="text-xs text-white/60 light:text-black/60 group-hover:text-white/80 light:group-hover:text-black/80 truncate">
                  {preview}
                </p>
                <p className="text-[10px] text-white/25 light:text-black/25 mt-0.5">
                  {agentName} &middot; {session.turnCount} turn
                  {session.turnCount !== 1 ? 's' : ''}
                </p>
              </div>
            </button>
          )
        })}
      </div>
    </div>
  )
}
