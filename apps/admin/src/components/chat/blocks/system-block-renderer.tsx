import { AgentCardBlock } from './agent-card-block'
import { AgentListBlock } from './agent-list-block'

interface SystemBlockRendererProps {
  data: Record<string, unknown>
}

export function SystemBlockRenderer({ data }: SystemBlockRendererProps) {
  const blockType = data?.block_type as string
  const payload = data?.payload as Record<string, unknown> | undefined

  if (!blockType || !payload) return null

  switch (blockType) {
    case 'agent_card':
      return <AgentCardBlock data={payload} />
    case 'agent_list':
      return <AgentListBlock data={payload} />
    default:
      return null
  }
}
