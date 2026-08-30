import { useNavigate } from '@tanstack/react-router'
import { useAgents } from '@/hooks/deployments/use-agents'
import { Bot } from 'lucide-react'
import type { AgentDefinition } from '@everstack/proto/everstack/agents/v1/agents_pb'

export function PinnedAgents() {
  const navigate = useNavigate()
  const { data: agents, isLoading } = useAgents({ enabled: true, limit: 3 })

  if (isLoading) {
    return (
      <div className="space-y-1.5">
        <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
          Agents
        </h3>
        {[1, 2, 3].map((i) => (
          <div
            key={i}
            className="h-8 animate-pulse rounded bg-brand-main-800/30"
          />
        ))}
      </div>
    )
  }

  if (!agents?.length) {
    return (
      <div className="space-y-1.5">
        <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
          Agents
        </h3>
        <p className="text-xs text-white/30 light:text-black/30">
          No agents yet.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-1.5">
      <h3 className="text-xs font-medium uppercase tracking-wider text-white/40 light:text-black/40">
        Agents
      </h3>
      <div className="space-y-0.5">
        {agents.map((agent) => (
          <AgentRow
            key={agent.id}
            agent={agent}
            onClick={() =>
              navigate({ to: `/deployments/agents/${agent.id}/chat` })
            }
          />
        ))}
      </div>
    </div>
  )
}

function AgentRow({
  agent,
  onClick,
}: {
  agent: AgentDefinition
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      className="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-brand-main-800/60 group"
    >
      <Bot className="size-3.5 shrink-0 text-white/20 light:text-black/20 group-hover:text-white/35 light:group-hover:text-black/35" />
      <p className="text-xs text-white/60 light:text-black/60 group-hover:text-white/80 light:group-hover:text-black/80 truncate">
        {agent.name}
      </p>
    </button>
  )
}
