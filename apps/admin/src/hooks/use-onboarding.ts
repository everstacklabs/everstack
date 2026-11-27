import { useMemo } from 'react'
import {
  type OnboardingPath,
  useOnboardingStore,
} from '@/stores/onboarding-store'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'
import { useApiKeys } from '@/hooks/vault/use-api-keys'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { useModelTrial } from '@/hooks/billing/use-model-trial'
import { useInstanceHasData } from '@/hooks/use-instance-has-data'
import {
  isUsableProvider,
  pickServableModel,
  pickTestModel,
  type ServableModels,
  type TestModel,
  type TestModelProvider,
} from '@/lib/first-request'

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

const EMPTY_PROVIDERS: TestModelProvider[] = []
const EMPTY_SERVABLE: ServableModels[] = []

/**
 * Resolves which launch step the wizard shows. A step pinned in the URL
 * (`obStep`, from a deep link or a spine click) wins; otherwise the first
 * incomplete step, so the flow auto-advances. The URL carries a step id, never
 * an index, so a stale id from an older flow (the removed `density` step, say)
 * simply falls through to the first incomplete step instead of breaking.
 */
export function resolveCurrentStep<T extends { id: string; complete: boolean }>(
  steps: readonly T[],
  pinnedId: string | undefined,
): T | undefined {
  return (
    steps.find((s) => s.id === pinnedId) ??
    steps.find((s) => !s.complete) ??
    steps[steps.length - 1]
  )
}

export function useOnboarding() {
  const dismissed = useOnboardingStore((s) => s.dismissed)
  const minimized = useOnboardingStore((s) => s.minimized)
  const celebrationShown = useOnboardingStore((s) => s.celebrationShown)
  const selectedPath = useOnboardingStore((s) => s.selectedPath)
  const sandboxSkipped = useOnboardingStore((s) => s.sandboxSkipped)
  const hydrated = useOnboardingStore((s) => s.hydrated)

  // Step 1: LLM Provider configured. The list holds every catalog provider,
  // configured or not, so only active rows with enabled models count.
  const { data: providersData, isLoading: providersLoading } =
    useConfiguredProviders()
  const usableProviders = useMemo(
    () => (providersData?.providers ?? EMPTY_PROVIDERS).filter(isUsableProvider),
    [providersData],
  )
  // What the gateway itself says it can route (the data behind /v1/models).
  // Covers providers configured through YAML rather than the console, which
  // ListConfiguredProviders does not see.
  const { data: servableModels, isLoading: servableLoading } =
    useGatewayModels()
  // Free model requests (cloud only; null when off or unavailable). While they
  // are active and the org has no key of its own, the playground runs on the
  // trial model so the first request works before any provider is connected.
  const { data: trialData, isLoading: trialLoading } = useModelTrial()
  const trial = trialData ?? null
  // True until every read that decides the test model has answered, so the
  // playground can hold its place instead of flashing "add a key" at a user
  // whose free requests or provider are still loading.
  const modelLoading = providersLoading || servableLoading || trialLoading
  // Cheap model for whichever provider is connected; drives the in-console
  // test request and the gateway quickstart snippet. Null means nothing is
  // routable yet, and no test request is attempted.
  const ownModel: TestModel | null = useMemo(
    () =>
      pickTestModel(usableProviders) ??
      pickServableModel(servableModels ?? EMPTY_SERVABLE),
    [usableProviders, servableModels],
  )
  // Gateway path only: a starter agent carries tools and a routed model, which
  // free requests refuse, so the agent path still asks for a key.
  const usingTrial =
    selectedPath === 'gateway' &&
    ownModel === null &&
    trial?.state === 'active'
  const testModel: TestModel | null =
    ownModel ??
    (usingTrial && trial ? { provider: 'openai', model: trial.model } : null)
  const hasProvider = testModel !== null
  const hasOpenAI =
    usableProviders.some((p) => p.catalog?.name === 'openai') ||
    testModel?.provider === 'openai'

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
        id: 'provider',
        label: 'Connect an LLM provider',
        description:
          'Add a provider key so the OpenAI-compatible gateway can route traffic.',
        helper:
          'This powers chat completions, responses, fallback, and observability.',
        actionLabel: usingTrial
          ? 'Free requests active'
          : hasProvider
            ? 'Provider connected'
            : 'Connect provider',
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
          'Call /v1/chat/completions or /v1/responses through Everstack.',
        helper:
          'Once traffic lands, traces and logs become your source of truth.',
        actionLabel: hasData ? 'Traffic observed' : 'View request snippets',
        href: '/observability/logs',
        icon: 'mingcute:route-line',
        complete: hasData,
      },
    ],
    [hasProvider, usingTrial, hasApiKey, hasData],
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
    hasProvider,
    testModel,
    modelLoading,
    trial,
    usingTrial,
    selectedPath,
    activePath,
    launchSteps,
    launchCompletedCount,
    launchAllComplete,
  }
}
