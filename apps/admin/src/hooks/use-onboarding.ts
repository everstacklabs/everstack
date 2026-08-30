import { useEffect, useMemo, useState } from 'react'
import {
  type OnboardingPath,
  useOnboardingStore,
} from '@/stores/onboarding-store'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'
import { useApiKeys } from '@/hooks/vault/use-api-keys'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useInstanceHasData } from '@/hooks/use-instance-has-data'
import {
  INTERFACE_DENSITY_CHANGED_EVENT,
  readStoredInterfaceDensity,
} from '@/lib/interface-density'

export interface OnboardingStep {
  id: string
  label: string
  description: string
  href: string
  icon: string
  complete: boolean
}

export interface OnboardingLaunchStep extends OnboardingStep {
  actionLabel: string
  helper: string
}

export function useOnboarding() {
  const dismissed = useOnboardingStore((s) => s.dismissed)
  const minimized = useOnboardingStore((s) => s.minimized)
  const celebrationShown = useOnboardingStore((s) => s.celebrationShown)
  const selectedPath = useOnboardingStore((s) => s.selectedPath)
  const sandboxSkipped = useOnboardingStore((s) => s.sandboxSkipped)
  const hydrated = useOnboardingStore((s) => s.hydrated)
  const [hasDensityPreference, setHasDensityPreference] = useState(
    () => readStoredInterfaceDensity() !== null,
  )

  useEffect(() => {
    const syncDensityPreference = () => {
      setHasDensityPreference(readStoredInterfaceDensity() !== null)
    }

    syncDensityPreference()
    window.addEventListener(
      INTERFACE_DENSITY_CHANGED_EVENT,
      syncDensityPreference,
    )
    window.addEventListener('storage', syncDensityPreference)
    return () => {
      window.removeEventListener(
        INTERFACE_DENSITY_CHANGED_EVENT,
        syncDensityPreference,
      )
      window.removeEventListener('storage', syncDensityPreference)
    }
  }, [])

  // Step 1: LLM Provider configured
  const { data: providersData, isLoading: providersLoading } =
    useConfiguredProviders()
  const hasProvider = (providersData?.providers?.length ?? 0) > 0
  const hasOpenAI =
    providersData?.providers?.some((p) => p.catalog?.name === 'openai') ?? false

  // Step 2: Agent created
  const { data: agents, isLoading: agentsLoading } = useAgents()
  const hasAgent = (agents?.length ?? 0) > 0
  const firstAgent = agents?.[0] ?? null
  const firstAgentId = firstAgent?.id ?? null

  // A sandboxed agent has sandbox.enabled in its config blob (same shape the
  // agent form reads). Mirrors parseExistingSandboxConfig in agent-form.tsx.
  const sandboxConfig = (
    firstAgent?.config as Record<string, unknown> | undefined
  )?.sandbox as { enabled?: boolean } | undefined
  const hasSandbox = sandboxConfig?.enabled === true

  // Step 3: API Key created
  const { data: apiKeys, isLoading: apiKeysLoading } = useApiKeys()
  const hasApiKey = (apiKeys?.length ?? 0) > 0

  // Step 4: Data flowing
  const { hasInstanceData } = useInstanceHasData('logs')
  const hasData = hasInstanceData === true

  // Intentionally exclude the logs/data probe from isLoading: it streams and
  // can hang on a half-up instance, which would block the entire launch
  // center on a spinner. hasData simply stays false until it resolves, which
  // only affects the final "first request" step.
  const isLoading = providersLoading || agentsLoading || apiKeysLoading

  const testHref = firstAgentId
    ? `/deployments/agents/${firstAgentId}/chat`
    : '/deployments/agents'

  const steps: OnboardingStep[] = useMemo(
    () => [
      {
        id: 'provider',
        label: 'Connect an LLM Provider',
        description: 'Add your OpenAI, Anthropic, or other API key',
        href: '/vault/llm-providers',
        icon: 'hugeicons:ai-lock',
        complete: hasProvider,
      },
      {
        id: 'agent',
        label: 'Create your first Agent',
        description: 'Configure an AI agent with a model and tools',
        href: '/deployments/agents',
        icon: 'ri:apps-ai-line',
        complete: hasAgent || hasData,
      },
      {
        id: 'api-key',
        label: 'Generate an API Key',
        description: 'Create a key to authenticate gateway requests',
        href: '/vault/api-keys',
        icon: 'ph:key-bold',
        complete: hasApiKey,
      },
      {
        id: 'test',
        label: 'Test your Agent',
        description: 'Chat with your agent or call it via API',
        href: testHref,
        icon: 'tabler:logs',
        complete: hasData,
      },
    ],
    [hasProvider, hasAgent, hasApiKey, hasData, testHref],
  )

  const agentLaunchSteps: OnboardingLaunchStep[] = useMemo(
    () => [
      {
        id: 'density',
        label: 'Choose layout density',
        description:
          'Pick how much information the admin UI should show at once.',
        helper:
          'Comfortable gives controls more room; Compact keeps tables and dashboards dense.',
        actionLabel: hasDensityPreference ? 'Layout selected' : 'Choose layout',
        href: '/settings/general',
        icon: 'lucide:layout-dashboard',
        complete: hasDensityPreference,
      },
      {
        id: 'provider',
        label: 'Connect an LLM provider',
        description:
          'Authorize Everstack to call your preferred model provider.',
        helper:
          'Start with OpenAI, Anthropic, Google, Groq, or any configured catalog provider.',
        actionLabel: hasProvider ? 'Provider connected' : 'Connect provider',
        href: '/vault/llm-providers',
        icon: 'hugeicons:ai-lock',
        complete: hasProvider,
      },
      {
        id: 'agent',
        label: 'Create a starter agent',
        description:
          'Create an agent that can receive chat messages and API calls.',
        helper:
          'Keep it simple first; tools, memory, and skills can be added later.',
        actionLabel: hasAgent ? 'Agent created' : 'Create agent',
        href: '/deployments/agents',
        icon: 'ri:apps-ai-line',
        complete: hasAgent || hasData,
      },
      {
        id: 'sandbox',
        label: 'Provision a sandbox',
        description:
          'Give the agent a sandbox so it can run code, shell, and tools.',
        helper:
          'Optional for chat-only agents; required for code execution, files, and computer use.',
        actionLabel: hasSandbox ? 'Sandbox ready' : 'Add a sandbox',
        href: '/deployments/agents',
        icon: 'lucide:box',
        complete: hasSandbox || sandboxSkipped,
      },
      {
        id: 'api-key',
        label: 'Generate an API key',
        description:
          'Create a bearer token for gateway and deployed-agent requests.',
        helper:
          'You only see the secret once, so copy it into your development environment.',
        actionLabel: hasApiKey ? 'Key generated' : 'Generate key',
        href: '/vault/api-keys',
        icon: 'ph:key-bold',
        complete: hasApiKey,
      },
      {
        id: 'test',
        label: 'Send the first request',
        description:
          'Chat with the agent or invoke it through the API and watch telemetry light up.',
        helper:
          'A successful request unlocks logs, traces, and agent sessions.',
        actionLabel: hasData ? 'Request observed' : 'Run test',
        href: testHref,
        icon: 'tabler:route',
        complete: hasData,
      },
    ],
    [
      hasDensityPreference,
      hasProvider,
      hasAgent,
      hasApiKey,
      hasData,
      hasSandbox,
      sandboxSkipped,
      testHref,
    ],
  )

  const gatewayLaunchSteps: OnboardingLaunchStep[] = useMemo(
    () => [
      {
        id: 'density',
        label: 'Choose layout density',
        description:
          'Pick how much information the admin UI should show at once.',
        helper:
          'Comfortable gives controls more room; Compact keeps tables and dashboards dense.',
        actionLabel: hasDensityPreference ? 'Layout selected' : 'Choose layout',
        href: '/settings/general',
        icon: 'lucide:layout-dashboard',
        complete: hasDensityPreference,
      },
      {
        id: 'provider',
        label: 'Connect an LLM provider',
        description:
          'Add a provider key so the OpenAI-compatible gateway can route traffic.',
        helper:
          'This powers chat completions, responses, fallback, and observability.',
        actionLabel: hasProvider ? 'Provider connected' : 'Connect provider',
        href: '/vault/llm-providers',
        icon: 'hugeicons:ai-lock',
        complete: hasProvider,
      },
      {
        id: 'api-key',
        label: 'Generate an API key',
        description: 'Create a gateway key for authenticated API traffic.',
        helper:
          'Use this key in the Authorization header for local and production calls.',
        actionLabel: hasApiKey ? 'Key generated' : 'Generate key',
        href: '/vault/api-keys',
        icon: 'ph:key-bold',
        complete: hasApiKey,
      },
      {
        id: 'test',
        label: 'Send a gateway request',
        description:
          'Call `/v1/responses` or `/v1/chat/completions` through Everstack.',
        helper:
          'Once traffic lands, traces and logs become your source of truth.',
        actionLabel: hasData ? 'Traffic observed' : 'View request snippets',
        href: '/observability/logs',
        icon: 'mingcute:route-line',
        complete: hasData,
      },
    ],
    [hasDensityPreference, hasProvider, hasApiKey, hasData],
  )

  const launchStepsByPath: Record<OnboardingPath, OnboardingLaunchStep[]> =
    useMemo(
      () => ({
        agent: agentLaunchSteps,
        gateway: gatewayLaunchSteps,
        production: [],
      }),
      [agentLaunchSteps, gatewayLaunchSteps],
    )

  const activePath = selectedPath ?? 'agent'
  const launchSteps = launchStepsByPath[activePath]
  const launchCompletedCount = launchSteps.filter((s) => s.complete).length
  const launchAllComplete =
    launchSteps.length > 0 && launchCompletedCount === launchSteps.length

  const completedCount = steps.filter((s) => s.complete).length
  const allComplete = completedCount === steps.length
  // Gate on hydration so the sidebar doesn't flash an onboarding affordance
  // to an already-onboarded user before server state loads.
  const isVisible = hydrated && !dismissed && !launchAllComplete

  return {
    steps,
    completedCount,
    allComplete,
    isVisible,
    minimized,
    dismissed,
    celebrationShown,
    hydrated,
    isLoading,
    firstAgentId,
    firstAgent,
    hasOpenAI,
    selectedPath,
    activePath,
    launchSteps,
    launchCompletedCount,
    launchAllComplete,
  }
}
