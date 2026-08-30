import { listAgents, createAgent, updateAgent } from './agents'
import { listGatewayModels } from './gateway'
import type { AgentDefinition } from '@everstack/proto/everstack/agents/v1/agents_pb'

const PLATFORM_AGENT_NAME = '__platform__'

const PLATFORM_AGENT_TOOLS = [
  'platform_create_agent',
  'platform_list_agents',
  'platform_get_agent',
  'platform_update_agent',
  'platform_delete_agent',
]

/**
 * Returns all available model strings from the gateway (only active/configured providers).
 */
async function getAvailableModels(): Promise<{ models: Set<string>; defaultModel: string }> {
  const providers = await listGatewayModels()
  if (!providers?.length) return { models: new Set(), defaultModel: '' }

  const allModels = new Set<string>()
  for (const p of providers) {
    for (const m of p.models) allModels.add(m)
  }

  // Pick default: prefer anthropic, then openai, then first
  const preferred = ['anthropic', 'openai']
  for (const name of preferred) {
    const p = providers.find((g) => g.provider.toLowerCase() === name)
    if (p?.models?.length) return { models: allModels, defaultModel: p.models[0] }
  }

  const first = providers[0]?.models?.[0] ?? ''
  return { models: allModels, defaultModel: first }
}

/**
 * Gets the platform agent for the chat-first UI.
 * If one doesn't exist, creates it with the first available gateway model.
 * If it exists but its model is no longer available, updates it.
 *
 * Each step throws a labeled error so the UI can surface exactly which
 * RPC failed (listAgents / listModels / createAgent / updateAgent).
 */
export async function getOrCreatePlatformAgent(): Promise<AgentDefinition> {
  // Find existing platform agent FIRST. If it already exists, a transient
  // gateway hiccup must not block initialization — only the create path
  // truly needs a default model.
  const res = await listAgents({ includeHidden: true, limit: 200 }).catch((e) => {
    throw new Error(`listAgents failed: ${e instanceof Error ? e.message : String(e)}`)
  })
  const existing = res.agents.find((a) => a.name === PLATFORM_AGENT_NAME)

  const { models: availableModels, defaultModel } = await getAvailableModels().catch((e) => {
    if (existing) return { models: new Set<string>(), defaultModel: '' }
    throw new Error(`gateway listModels failed: ${e instanceof Error ? e.message : String(e)}`)
  })

  if (existing) {
    // Check if the agent's model is still available in the gateway
    if (existing.model && !availableModels.has(existing.model) && defaultModel) {
      // Model no longer valid — update to a working one
      await updateAgent({
        tenantId: '',
        id: existing.id,
        model: defaultModel,
      }).catch((e) => {
        throw new Error(`updateAgent failed (model swap to ${defaultModel}): ${e instanceof Error ? e.message : String(e)}`)
      })
      // Return with updated model
      return { ...existing, model: defaultModel } as AgentDefinition
    }
    return existing
  }

  // No platform agent exists — create one
  if (!defaultModel) {
    throw new Error(
      `No LLM providers configured (gateway returned ${availableModels.size} models). ` +
      `Open Vault and connect a provider, then refresh.`,
    )
  }

  const createRes = await createAgent({
    tenantId: '',
    name: PLATFORM_AGENT_NAME,
    model: defaultModel,
    description: 'Platform assistant for managing agents and infrastructure via chat.',
    systemPrompt: '',
    tools: PLATFORM_AGENT_TOOLS,
    hidden: true,
  }).catch((e) => {
    throw new Error(`createAgent failed (model=${defaultModel}): ${e instanceof Error ? e.message : String(e)}`)
  })

  if (!createRes.agent) {
    throw new Error('createAgent returned no agent in response')
  }

  return createRes.agent
}
