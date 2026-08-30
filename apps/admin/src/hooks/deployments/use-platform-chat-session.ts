import { useQuery } from '@tanstack/react-query'
import { getOrCreatePlatformAgent } from '@/server/platform-agent'
import { useAgentChatSession } from './use-agent-chat-session'

const PLATFORM_AGENT_KEY = ['platform-agent']

/**
 * Manages the platform agent chat session for the chat-first UI.
 * Automatically resolves (or creates) the platform meta-agent,
 * then delegates to useAgentChatSession for session management.
 */
export function usePlatformChatSession() {
  const {
    data: platformAgent,
    isLoading: isAgentLoading,
    error: agentError,
  } = useQuery({
    queryKey: PLATFORM_AGENT_KEY,
    queryFn: getOrCreatePlatformAgent,
    staleTime: Infinity,
    retry: 2,
  })

  const agentId = platformAgent?.id ?? ''

  // Pass agentId directly — useAgentChatSession's query is gated by
  // `enabled: !!orgId && !!agentId` so it won't fire when agentId is ''.
  // The override-reset effect (line 160) fires when agentId changes from
  // '' to the real ID, but that's fine — there's no override to lose
  // since the component just mounted.
  const chatSession = useAgentChatSession(agentId)

  return {
    platformAgent,
    isAgentLoading,
    agentError,
    agentId,
    ...chatSession,
  }
}
