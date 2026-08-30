import { AgentCardBlock } from './agent-card-block'

interface AgentListBlockProps {
  data: Record<string, unknown>
}

export function AgentListBlock({ data }: AgentListBlockProps) {
  const agents = (data.agents as Record<string, unknown>[]) ?? []
  const total = (data.total as number) ?? agents.length

  if (agents.length === 0) {
    return (
      <div className="my-2 rounded-lg border border-brand-main-600 bg-brand-main-800/60 p-4 text-center">
        <p className="text-sm text-white/50 light:text-black/50">No agents found.</p>
      </div>
    )
  }

  return (
    <div className="my-2 space-y-1">
      <div className="text-[11px] text-white/40 px-1 mb-1 light:text-black/40">
        {total} agent{total !== 1 ? 's' : ''}
      </div>
      {agents.map((agent) => (
        <AgentCardBlock key={agent.id as string} data={agent} />
      ))}
    </div>
  )
}
