import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import { Button } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'
import { InterfaceDensityCards } from '@/components/common/interface-density-cards'
import { getApiBaseUrl } from '@/lib/api-url'
import { isCloudManaged } from '@/lib/cloud-mode'
import {
  type InterfaceDensity,
  persistInterfaceDensity,
  readStoredInterfaceDensity,
} from '@/lib/interface-density'
import { AgentFormDialog } from '@/components/deployments/agents'
import type { AgentDefinition } from '@/server/agents'
import { ConfigureProviderSheet } from '@/components/providers'
import { useCreateApiKey } from '@/hooks/vault/use-api-keys'
import { useCreateAgent, useUpdateAgent } from '@/hooks/deployments/use-agents'
import { useCreateSandbox } from '@/hooks/deployments/use-sandbox'
import { AgentLifecycleMode, type CreateAgentParams } from '@/server/agents'
import type { SandboxTemplate } from '@/server/sandbox'
import {
  useOnboarding,
  type OnboardingLaunchStep,
} from '@/hooks/use-onboarding'
import {
  type OnboardingPath,
  useOnboardingStore,
} from '@/stores/onboarding-store'

const {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  CodeBlock,
  CodeBlockBody,
  CodeBlockContent,
  CodeBlockCopyButton,
  CodeBlockItem,
} = ui

// Brand sans (Geist, loaded via the Google Fonts link in index.html), applied
// scoped to onboarding. No monospace anywhere, per design direction.
const BRAND_SANS =
  '"Geist", -apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", sans-serif'

type LaunchPathConfig = {
  id: OnboardingPath
  title: string
  eyebrow: string
  description: string
  icon: string
  estimate: string
  railHeadline: string
  railSub: string
}

const LAUNCH_PATHS: LaunchPathConfig[] = [
  {
    id: 'agent',
    title: 'Agent API',
    eyebrow: 'Recommended',
    description: 'Create a deployable agent and verify it with a real request.',
    icon: 'ri:apps-ai-line',
    estimate: '~5 min',
    railHeadline: 'Ship your first agent.',
    railSub:
      'Connect a model, create an agent, then watch its first request land in your logs.',
  },
  {
    id: 'gateway',
    title: 'Gateway Proxy',
    eyebrow: 'Fast path',
    description: 'Route OpenAI-compatible traffic through Everstack.',
    icon: 'mingcute:route-line',
    estimate: '~3 min',
    railHeadline: 'Route your first request.',
    railSub:
      'Point traffic at Everstack with the SDK and watch it appear in your logs.',
  },
  {
    id: 'production',
    title: 'Hosted Instance',
    eyebrow: 'Cloud',
    description: 'Managed regions, billing, and team access.',
    icon: 'lucide:server-cog',
    estimate: 'Cloud',
    railHeadline: '',
    railSub: '',
  },
]

const PROVIDERS = [
  { name: 'openai', label: 'OpenAI', icon: 'simple-icons:openai' },
  { name: 'anthropic', label: 'Anthropic', icon: 'simple-icons:anthropic' },
  { name: 'google', label: 'Google', icon: 'simple-icons:google' },
  { name: 'groq', label: 'Groq', icon: 'simple-icons:groq' },
]

type LangKey = 'curl' | 'node' | 'go' | 'python'
type LangSnippets = Record<LangKey, string>

const CODE_TABS: { value: LangKey; label: string; language: string }[] = [
  { value: 'curl', label: 'cURL', language: 'bash' },
  { value: 'node', label: 'Node', language: 'typescript' },
  { value: 'go', label: 'Go', language: 'go' },
  { value: 'python', label: 'Python', language: 'python' },
]

function gatewayBase(): string {
  try {
    const b = getApiBaseUrl()
    if (b) return b.replace(/\/$/, '')
  } catch {
    // fall through to origin
  }
  return typeof window !== 'undefined'
    ? window.location.origin
    : 'https://your-instance.everstack.ai'
}

function defaultModel(hasOpenAI: boolean): string {
  return hasOpenAI ? '@openai/gpt-4o' : '@anthropic/claude-sonnet-4-20250514'
}

// One-click starter agents. Each pre-fills a coherent name + instructions so a
// new user never faces a blank form. `build` takes the resolved default model
// so the agent uses whichever provider was just connected. The Coder template
// flips on the sandbox (config.sandbox.enabled), which also satisfies the
// onboarding sandbox step — no separate provisioning needed at create time.
type AgentTemplate = {
  id: string
  icon: string
  title: string
  tagline: string
  accent: boolean
  // Short capability/specialty labels shown as chips so the setup is
  // explorable at a glance. They describe the agent's focus, derived from its
  // instructions and defaults (the Coder template's "Code execution" maps to a
  // real sandbox).
  tags: string[]
  systemPrompt: string
  build: (model: string) => Omit<CreateAgentParams, 'tenantId'>
}

const SUPPORT_PROMPT =
  'You are a friendly customer support assistant. Answer questions clearly and concisely, keep a warm tone, and ask one clarifying question when a request is ambiguous.'
const CODER_PROMPT =
  'You are a capable coding agent. Write correct, well-structured code, run it in your sandbox to verify it works, and explain what you changed. Prefer small, testable steps.'
const RESEARCH_PROMPT =
  'You are a thorough research agent. Break a question into sub-topics, reason step by step, and return a structured summary with the key findings and any caveats.'
const ASSISTANT_PROMPT =
  'You are a sharp, helpful general assistant. Draft, summarize, brainstorm, and plan. Be direct, structure longer answers, and surface tradeoffs instead of hedging.'

const AGENT_TEMPLATES: AgentTemplate[] = [
  {
    id: 'support',
    icon: 'lucide:headset',
    title: 'Support assistant',
    tagline: 'Answers customer questions in a friendly tone',
    accent: false,
    tags: ['Customer support', 'FAQ', 'Friendly tone'],
    systemPrompt: SUPPORT_PROMPT,
    build: (model) => ({
      name: 'Support Assistant',
      model,
      description: 'Answers customer questions in a friendly, concise tone.',
      systemPrompt: SUPPORT_PROMPT,
    }),
  },
  {
    id: 'coder',
    icon: 'lucide:terminal',
    title: 'Coding agent',
    tagline: 'Writes and runs code in an isolated sandbox',
    accent: true,
    tags: ['Code execution', 'Shell', 'Debugging'],
    systemPrompt: CODER_PROMPT,
    build: (model) => ({
      name: 'Coding Agent',
      model,
      description: 'Writes, runs, and debugs code inside an isolated sandbox.',
      systemPrompt: CODER_PROMPT,
      // Persistent + sandbox-enabled so a real sandbox comes up (and persists)
      // when the agent first runs.
      lifecycleMode: AgentLifecycleMode.PERSISTENT,
      config: {
        sandbox: {
          enabled: true,
          image: 'ghcr.io/everstacklabs/sandbox:base',
          persistent: true,
        },
      },
    }),
  },
  {
    id: 'research',
    icon: 'lucide:telescope',
    title: 'Research agent',
    tagline: 'Investigates a topic and returns a summary',
    accent: false,
    tags: ['Research', 'Summaries', 'Step-by-step'],
    systemPrompt: RESEARCH_PROMPT,
    build: (model) => ({
      name: 'Research Agent',
      model,
      description: 'Investigates topics and returns structured summaries.',
      systemPrompt: RESEARCH_PROMPT,
    }),
  },
  {
    id: 'assistant',
    icon: 'lucide:sparkles',
    title: 'General assistant',
    tagline: 'Drafts, plans, and brainstorms with you',
    accent: false,
    tags: ['General', 'Drafting', 'Brainstorming'],
    systemPrompt: ASSISTANT_PROMPT,
    build: (model) => ({
      name: 'Personal Assistant',
      model,
      description: 'A sharp general-purpose assistant for everyday work.',
      systemPrompt: ASSISTANT_PROMPT,
    }),
  },
]

// curl + the real Everstack SDKs (@everstack/node, everstack-go, everstack
// python). The key is never inlined as plaintext: snippets keep the
// <YOUR_KEY> placeholder and the real key lives in the click-to-reveal pill.
// cURL authenticates with the x-evs-api-key header (the gateway looks for the
// api-key header, not a Bearer token).
function buildSnippets(
  activePath: OnboardingPath,
  firstAgentId: string | null,
  hasOpenAI: boolean,
): LangSnippets {
  const base = gatewayBase()
  const key = '<YOUR_KEY>'
  const model = defaultModel(hasOpenAI)

  if (activePath === 'gateway') {
    return {
      curl: `curl ${base}/v1/chat/completions \\
  -H "x-evs-api-key: ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${model}","messages":[{"role":"user","content":"Say hello from Everstack"}]}'`,
      node: `import Everstack from "@everstack/node"

const client = new Everstack({
  apiKey: "${key}",
  baseUrl: "${base}",
})

const res = await client.chat.completions.create({
  model: "${model}",
  messages: [{ role: "user", content: "Say hello from Everstack" }],
})

console.log(res.choices[0].message.content)`,
      go: `package main

import (
	"context"
	"fmt"

	everstack "github.com/everstacklabs/everstack-go"
)

func main() {
	client := everstack.NewClient("${key}", everstack.WithBaseURL("${base}"))

	res, err := client.Chat.Completions.Create(context.Background(), &everstack.ChatCompletionParams{
		Model: "${model}",
		Messages: []everstack.Message{
			{Role: "user", Content: "Say hello from Everstack"},
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(*res.Choices[0].Message.Content)
}`,
      python: `from everstack import Everstack

client = Everstack(api_key="${key}", base_url="${base}")

res = client.chat.completions.create(
    model="${model}",
    messages=[{"role": "user", "content": "Say hello from Everstack"}],
)
print(res.choices[0].message.content)`,
    }
  }

  const agentId = firstAgentId ?? '<AGENT_ID>'
  const agentUrl = `${base}/v1/deploy/${agentId}/invoke`
  return {
    curl: `curl -X POST ${agentUrl} \\
  -H "x-evs-api-key: ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"message":"Say hello from Everstack"}'`,
    node: `import Everstack from "@everstack/node"

const client = new Everstack({
  apiKey: "${key}",
  baseUrl: "${base}",
})

const session = await client.agents.sessions.create({ agentId: "${agentId}" })

const turn = await client.agents.sessions.runTurn({
  sessionId: session.session.id,
  input: "Say hello from Everstack",
})

console.log(turn.outputText)`,
    go: `package main

import (
	"context"
	"fmt"

	everstack "github.com/everstacklabs/everstack-go"
)

func main() {
	client := everstack.NewClient("${key}", everstack.WithBaseURL("${base}"))
	ctx := context.Background()

	session, err := client.Agents.Sessions.Create(ctx, map[string]any{
		"agent_id": "${agentId}",
	})
	if err != nil {
		panic(err)
	}

	result, err := client.Agents.Sessions.RunTurn(ctx, session["id"].(string), map[string]any{
		"message": "Say hello from Everstack",
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(result)
}`,
    python: `from everstack import Everstack

client = Everstack(api_key="${key}", base_url="${base}")

session = client.agents.sessions.create(agent_id="${agentId}")

res = client.agents.sessions.send_message(
    session_id=session["id"],
    message="Say hello from Everstack",
)
print(res)`,
  }
}

export function LaunchCenter() {
  const navigate = useNavigate()
  const {
    selectedPath,
    activePath,
    launchSteps,
    launchAllComplete,
    firstAgentId,
    firstAgent,
    hasOpenAI,
  } = useOnboarding()
  const selectPath = useOnboardingStore((s) => s.selectPath)
  const clearPath = useOnboardingStore((s) => s.clearPath)
  const dismiss = useOnboardingStore((s) => s.dismiss)
  const markCelebrationShown = useOnboardingStore((s) => s.markCelebrationShown)
  const search = useSearch({ from: '/' })

  // Restore / reflect the chosen path from the URL (deep-link + refresh).
  useEffect(() => {
    if (search.obPath && search.obPath !== selectedPath) {
      selectPath(search.obPath)
    }
  }, [search.obPath, selectedPath, selectPath])

  const handleSelectPath = (path: OnboardingPath) => {
    selectPath(path)
    navigate({
      to: '/',
      search: (prev) => ({ ...prev, obPath: path, obStep: undefined }),
    })
  }

  const handleClearPath = () => {
    clearPath()
    navigate({
      to: '/',
      search: () => ({ obPath: undefined, obStep: undefined }),
    })
  }

  // Cloud-managed instances run on app.everstack.ai already, so the
  // "Hosted Instance" path is irrelevant there: only Agent + Gateway show.
  const cloud = isCloudManaged()

  const skipForNow = () => {
    dismiss()
    navigate({ to: '/chat', search: { session: undefined, model: undefined } })
  }

  // No isLoading gate: steps default to incomplete until their queries resolve,
  // so the screen renders instantly instead of stranding on a spinner.

  // Already fully set up (including a returning user with fresh local state):
  // go straight to the completion screen. Production never reaches here (its
  // launchSteps is empty, so launchAllComplete is always false).
  if (launchAllComplete) {
    return (
      <CompletionScreen
        activePath={activePath}
        firstAgentId={firstAgentId}
        hasOpenAI={hasOpenAI}
        steps={launchSteps}
        markDone={markCelebrationShown}
      />
    )
  }

  if (selectedPath === null || (cloud && selectedPath === 'production')) {
    return (
      <PathChooser
        cloud={cloud}
        onSelect={handleSelectPath}
        onSkip={skipForNow}
      />
    )
  }

  if (selectedPath === 'production') {
    return <HostedInstanceScreen onBack={handleClearPath} onSkip={skipForNow} />
  }

  return (
    <ConsoleWizard
      activePath={activePath}
      steps={launchSteps}
      firstAgentId={firstAgentId}
      firstAgent={firstAgent}
      hasOpenAI={hasOpenAI}
      onChangePath={handleClearPath}
      onSkip={skipForNow}
    />
  )
}

// ─── Frame + brand ──────────────────────────────────────────────────

function OnboardingFrame({
  left,
  right,
}: {
  left: ReactNode
  right: ReactNode
}) {
  return (
    <div
      className="relative h-full overflow-hidden bg-brand-main-950 text-brand-main-50"
      style={{ fontFamily: BRAND_SANS }}
    >
      <div
        aria-hidden
        className="pointer-events-none absolute -left-40 -top-48 size-[36rem] rounded-full bg-brand-secondary-700/20 blur-[150px]"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute -bottom-40 right-0 size-[28rem] rounded-full bg-brand-secondary-900/40 blur-[150px]"
      />
      <div className="relative grid h-full grid-cols-1 lg:grid-cols-[minmax(0,440px)_minmax(0,1fr)]">
        <div className="flex flex-col overflow-y-auto border-b border-brand-main-800 px-8 py-10 lg:border-b-0 lg:border-r scrollbar-macos">
          {left}
        </div>
        <div className="flex flex-col justify-center overflow-y-auto px-6 py-10 lg:px-12 scrollbar-macos">
          {right}
        </div>
      </div>
    </div>
  )
}

// ─── Path chooser ───────────────────────────────────────────────────

function PathChooser({
  cloud,
  onSelect,
  onSkip,
}: {
  cloud: boolean
  onSelect: (path: OnboardingPath) => void
  onSkip: () => void
}) {
  const paths = cloud
    ? LAUNCH_PATHS.filter((p) => p.id !== 'production')
    : LAUNCH_PATHS
  return (
    <OnboardingFrame
      left={
        <div className="flex min-h-full flex-col">
          <div className="flex flex-1 flex-col justify-center py-10">
            <p className="text-xs font-medium uppercase tracking-[0.2em] text-brand-secondary-300">
              First run
            </p>
            <h1 className="mt-4 text-3xl font-semibold leading-tight tracking-tight md:text-4xl">
              Let&apos;s get your first
              <br />
              request flowing.
            </h1>
            <p className="mt-4 max-w-sm text-sm leading-6 text-brand-main-200">
              Pick how you want to start. Everstack walks you through it one
              step at a time, and you can switch paths whenever you like.
            </p>
          </div>
          <div className="text-[11px] text-brand-main-300">
            everstack · self-hosted
          </div>
        </div>
      }
      right={
        <div className="mx-auto w-full max-w-md">
          <div className="space-y-2.5">
            {paths.map((path, index) => (
              <motion.button
                key={path.id}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: index * 0.06, duration: 0.3 }}
                type="button"
                onClick={() => onSelect(path.id)}
                className="group flex w-full items-center gap-4 rounded border border-brand-main-700 bg-brand-main-900/60 p-4 text-left transition-colors hover:border-brand-secondary-500/50 hover:bg-brand-main-800"
              >
                <span className="flex size-11 shrink-0 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800 text-brand-secondary-300 transition-colors group-hover:border-brand-secondary-500/40 group-hover:bg-brand-secondary-500/10">
                  <Iconify.Icon icon={path.icon} className="size-5" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className="text-sm font-medium text-brand-main-50">
                      {path.title}
                    </span>
                    <span className="rounded border border-brand-main-600 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-brand-main-200">
                      {path.eyebrow}
                    </span>
                  </span>
                  <span className="mt-1 block text-xs leading-5 text-brand-main-200">
                    {path.description}
                  </span>
                </span>
                <span className="flex shrink-0 flex-col items-end gap-1.5">
                  <span className="text-[11px] text-brand-main-300">
                    {path.estimate}
                  </span>
                  <Iconify.Icon
                    icon="lucide:arrow-right"
                    className="size-4 text-brand-main-300 transition-transform group-hover:translate-x-0.5 group-hover:text-brand-secondary-300"
                  />
                </span>
              </motion.button>
            ))}
          </div>
          <button
            type="button"
            onClick={onSkip}
            className="mt-5 w-full text-center text-xs text-brand-main-300 transition-colors hover:text-brand-main-100"
          >
            Skip setup for now
          </button>
        </div>
      }
    />
  )
}

// ─── Wizard (split console) ─────────────────────────────────────────

function ConsoleWizard({
  activePath,
  steps,
  firstAgentId,
  firstAgent,
  hasOpenAI,
  onChangePath,
  onSkip,
}: {
  activePath: OnboardingPath
  steps: OnboardingLaunchStep[]
  firstAgentId: string | null
  firstAgent: AgentDefinition | null
  hasOpenAI: boolean
  onChangePath: () => void
  onSkip: () => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const search = useSearch({ from: '/' })
  const cloud = isCloudManaged()
  const skipSandbox = useOnboardingStore((s) => s.skipSandbox)
  const [providerName, setProviderName] = useState<string | null>(null)
  const [agentDialogOpen, setAgentDialogOpen] = useState(false)
  const [editingAgent, setEditingAgent] = useState<AgentDefinition | null>(null)
  const [testKey, setTestKey] = useState<string | null>(null)

  // A step pinned in the URL wins (deep-link / spine click); otherwise show the
  // first incomplete step so the flow still auto-advances.
  const current =
    steps.find((s) => s.id === search.obStep) ??
    steps.find((s) => !s.complete) ??
    steps[steps.length - 1]
  const isTest = current?.id === 'test'
  const pathConfig =
    LAUNCH_PATHS.find((p) => p.id === activePath) ?? LAUNCH_PATHS[0]
  const snippets = useMemo(
    () => buildSnippets(activePath, firstAgentId, hasOpenAI),
    [activePath, firstAgentId, hasOpenAI],
  )

  // useInstanceHasData('logs') has staleTime: Infinity with all auto-refetch
  // disabled, so while the wizard sits on the final step we poll-invalidate it
  // to detect the first request landing and advance to the completion screen.
  useEffect(() => {
    if (!isTest) return
    const timer = window.setInterval(() => {
      void queryClient.invalidateQueries({ queryKey: ['instance-has-logs'] })
    }, 4000)
    return () => window.clearInterval(timer)
  }, [isTest, queryClient])

  const openResult = () => {
    if (activePath === 'agent' && firstAgentId) {
      navigate({
        to: '/deployments/agents/$agentId/chat',
        params: { agentId: firstAgentId },
      })
      return
    }
    navigate({ to: '/observability/logs' })
  }

  const openAgentDialog = (agent: AgentDefinition | null) => {
    setEditingAgent(agent)
    setAgentDialogOpen(true)
  }

  const goToStep = (id: string) =>
    navigate({ to: '/', search: (prev) => ({ ...prev, obStep: id }) })

  // Advance to the next step in order (used by the sandbox Skip action).
  const goToNextStep = () => {
    const i = steps.findIndex((s) => s.id === current?.id)
    const next = i >= 0 && i < steps.length - 1 ? steps[i + 1] : null
    if (next) goToStep(next.id)
  }

  // Pin the current step so a pick that completes it doesn't yank the panel to
  // the next step via auto-advance; the user moves on with Next when ready.
  const pinCurrent = () => {
    if (current) goToStep(current.id)
  }

  const renderAction = (step: OnboardingLaunchStep): ReactNode => {
    if (step.id === 'provider') {
      return <ProviderQuickPick onSelect={setProviderName} />
    }
    // Agent, sandbox, and test actions all live in the right panel now (the
    // galleries and the StepNav "Open chat" finish button), so the spine
    // carries no inline action for them.
    if (
      step.id === 'density' ||
      step.id === 'agent' ||
      step.id === 'sandbox' ||
      step.id === 'test'
    ) {
      return null
    }
    // api-key step: generate inline and show the masked key pill.
    return (
      <ApiKeyAction testKey={testKey} onKey={setTestKey} onPin={pinCurrent} />
    )
  }

  return (
    <>
      <OnboardingFrame
        left={
          <div className="flex min-h-full flex-col">
            <div className="mt-12">
              <button
                type="button"
                onClick={onChangePath}
                className="inline-flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.16em] text-brand-secondary-300 transition-colors hover:text-brand-secondary-200"
              >
                {pathConfig.title}
                <span className="text-brand-main-500">·</span>
                <span className="text-brand-main-300">change</span>
              </button>
              <h1 className="mt-3 text-2xl font-semibold tracking-tight md:text-[28px]">
                {pathConfig.railHeadline}
              </h1>
              <p className="mt-2 max-w-sm text-sm leading-6 text-brand-main-200">
                {pathConfig.railSub}
              </p>
            </div>
            <div className="mt-9 flex-1">
              <Spine
                steps={steps}
                currentId={current?.id}
                renderAction={renderAction}
                onSelectStep={(id) =>
                  navigate({
                    to: '/',
                    search: (prev) => ({ ...prev, obStep: id }),
                  })
                }
              />
            </div>
            <div className="mt-6 flex items-center gap-4 text-xs text-brand-main-300">
              <button
                type="button"
                onClick={onChangePath}
                className="inline-flex items-center gap-1 transition-colors hover:text-brand-main-100"
              >
                <Iconify.Icon icon="lucide:arrow-left" className="size-3" />
                Different path
              </button>
              <span className="text-brand-main-600">·</span>
              <button
                type="button"
                onClick={onSkip}
                className="transition-colors hover:text-brand-main-100"
              >
                Skip for now
              </button>
            </div>
          </div>
        }
        right={
          <div className="flex w-full flex-col">
            {/* Consistent height so the panel doesn't resize between steps;
                each step's content is built compact enough to fit it without
                scrolling. */}
            <div className="flex min-h-[34rem] w-full flex-col justify-center">
              {current?.id === 'density' ? (
                <DensityGallery onChoose={goToNextStep} />
              ) : current?.id === 'agent' ? (
                <AgentGallery
                  hasOpenAI={hasOpenAI}
                  firstAgent={firstAgent}
                  onScratch={() => openAgentDialog(null)}
                  onPin={pinCurrent}
                />
              ) : current?.id === 'sandbox' ? (
                <SandboxGallery
                  firstAgent={firstAgent}
                  onScratch={() => openAgentDialog(firstAgent)}
                  onSkip={() => {
                    skipSandbox()
                    goToNextStep()
                  }}
                  onPin={pinCurrent}
                />
              ) : (
                <ConsoleTerminal
                  activePath={activePath}
                  snippets={snippets}
                  steps={steps}
                  mode={isTest ? 'listening' : 'waiting'}
                  hasTestKey={!!testKey}
                  runner={
                    // The live in-UI request needs a resolvable tenant, which
                    // only cloud-managed instances provide; self-hosted falls
                    // back to the copy-paste snippets above.
                    isTest && cloud ? (
                      <InlineFirstRequest
                        activePath={activePath}
                        firstAgentId={firstAgentId}
                        hasOpenAI={hasOpenAI}
                        testKey={testKey}
                        onKey={setTestKey}
                      />
                    ) : null
                  }
                />
              )}
            </div>
            <StepNav
              steps={steps}
              currentId={current?.id}
              onGo={goToStep}
              onFinish={openResult}
              finishLabel={activePath === 'agent' ? 'Open chat' : 'View logs'}
            />
          </div>
        }
      />

      {/* Mount dialogs only when opened, so their forms don't eagerly fire
          MCP / Memory / provider queries while closed. */}
      {providerName && (
        <ConfigureProviderSheet
          open
          onOpenChange={(open) => {
            if (!open) setProviderName(null)
          }}
          providerName={providerName}
        />
      )}
      {agentDialogOpen && (
        <AgentFormDialog
          open
          agent={editingAgent}
          onOpenChange={(o) => {
            setAgentDialogOpen(o)
            if (!o) setEditingAgent(null)
          }}
        />
      )}
    </>
  )
}

function Spine({
  steps,
  currentId,
  renderAction,
  onSelectStep,
}: {
  steps: OnboardingLaunchStep[]
  currentId: string | undefined
  renderAction?: (step: OnboardingLaunchStep) => ReactNode
  onSelectStep?: (id: string) => void
}) {
  return (
    <ol>
      {steps.map((step, index) => {
        const isCurrent = step.id === currentId && !step.complete
        const isLast = index === steps.length - 1
        const action = isCurrent && renderAction ? renderAction(step) : null
        return (
          <li
            key={step.id}
            className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5"
          >
            <div className="flex flex-col items-center">
              <motion.span
                initial={false}
                animate={{ scale: isCurrent ? 1.06 : 1 }}
                transition={{ type: 'spring', stiffness: 300, damping: 18 }}
                className={cn(
                  'flex size-7 items-center justify-center rounded border text-[11px] font-semibold',
                  step.complete
                    ? 'border-brand-secondary-500 bg-brand-secondary-500 text-brand-main-950'
                    : isCurrent
                      ? 'border-brand-secondary-400 bg-brand-secondary-400/15 text-brand-secondary-200'
                      : 'border-brand-main-700 bg-brand-main-900 text-brand-main-300',
                )}
              >
                {step.complete ? (
                  <Iconify.Icon icon="lucide:check" className="size-3.5" />
                ) : (
                  index + 1
                )}
              </motion.span>
              {!isLast && (
                <span
                  className={cn(
                    'my-1.5 w-px flex-1',
                    step.complete
                      ? 'bg-brand-secondary-500/40'
                      : 'bg-brand-main-700',
                  )}
                />
              )}
            </div>
            <div className={cn(isLast ? 'pb-0' : 'pb-7')}>
              <button
                type="button"
                onClick={() => onSelectStep?.(step.id)}
                className={cn(
                  'text-left text-sm transition-colors',
                  onSelectStep && 'hover:text-brand-main-50',
                  step.complete
                    ? 'text-brand-main-100'
                    : isCurrent
                      ? 'font-medium text-brand-main-50'
                      : 'text-brand-main-300',
                )}
              >
                {step.label}
              </button>
              {isCurrent && (
                <p className="mt-1 max-w-xs text-xs leading-5 text-brand-main-200">
                  {step.description}
                </p>
              )}
              {action && <div className="mt-3">{action}</div>}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

function DensityGallery({ onChoose }: { onChoose: () => void }) {
  const [density, setDensity] = useState<InterfaceDensity | null>(() =>
    readStoredInterfaceDensity(),
  )

  const chooseDensity = (value: InterfaceDensity) => {
    persistInterfaceDensity(value)
    setDensity(value)
    onChoose()
  }

  return (
    <GalleryShell
      eyebrow="Step · layout"
      title="Choose your layout density"
      subtitle="Pick the default feel before you start. You can change this later in Settings."
      bodyClassName="space-y-2"
    >
      <InterfaceDensityCards value={density} onChange={chooseDensity} />
    </GalleryShell>
  )
}

function ProviderQuickPick({
  onSelect,
}: {
  onSelect: (provider: string) => void
}) {
  const navigate = useNavigate()
  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap gap-2">
        {PROVIDERS.map((provider) => (
          <button
            key={provider.name}
            type="button"
            onClick={() => onSelect(provider.name)}
            className="inline-flex items-center gap-2 rounded border border-brand-main-700 bg-brand-main-900 px-3 py-1.5 text-sm text-brand-main-100 transition-colors hover:border-brand-secondary-500/50 hover:bg-brand-main-800"
          >
            <Iconify.Icon icon={provider.icon} className="size-4" />
            {provider.label}
          </button>
        ))}
      </div>
      <button
        type="button"
        onClick={() => navigate({ to: '/vault/llm-providers' })}
        className="text-xs text-brand-main-300 transition-colors hover:text-brand-main-100"
      >
        Browse all providers
      </button>
    </div>
  )
}

// ApiKeyAction generates a key inline (no dialog). The plaintext is lifted into
// `testKey`, which both injects it into the snippets and powers the live test
// request, then shown as a masked, click-to-reveal-and-copy pill.
function ApiKeyAction({
  testKey,
  onKey,
  onPin,
}: {
  testKey: string | null
  onKey: (key: string) => void
  onPin: () => void
}) {
  const createApiKey = useCreateApiKey()
  const [error, setError] = useState<string | null>(null)

  const generate = async () => {
    setError(null)
    try {
      const res = await createApiKey.mutateAsync({
        name: 'Onboarding key',
        type: ApiKeyType.USER,
      })
      const key = res.apiKey?.hash
      if (!key) throw new Error('No key was returned')
      onKey(key)
      onPin()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create the key')
    }
  }

  if (testKey) {
    return <ApiKeyPill keyValue={testKey} />
  }
  return (
    <div className="space-y-2">
      <Button
        onClick={generate}
        disabled={createApiKey.isPending}
        className="gap-2"
      >
        {createApiKey.isPending ? (
          <Iconify.Icon
            icon="lucide:loader-2"
            className="size-4 animate-spin"
          />
        ) : (
          <Iconify.Icon icon="ph:key-bold" className="size-4" />
        )}
        Generate API key
      </Button>
      {error && (
        <p className="text-[11px] text-red-300 light:text-red-600">{error}</p>
      )}
    </div>
  )
}

function maskApiKey(key: string): string {
  if (key.length <= 12) return key
  // Show only "sk-" + last 4 so the masked form never surfaces the legacy
  // key prefix; the full value appears only on reveal.
  return `sk-${'*'.repeat(17)}${key.slice(-4)}`
}

// ApiKeyPill shows the key masked. A click reveals the full value AND copies it
// to the clipboard, so the secret is never sitting in plain sight by default.
function ApiKeyPill({ keyValue }: { keyValue: string }) {
  const [revealed, setRevealed] = useState(false)
  const [copied, setCopied] = useState(false)

  const handle = async () => {
    setRevealed(true)
    try {
      await navigator.clipboard.writeText(keyValue)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      // Clipboard may be blocked; revealing still lets the user copy by hand.
    }
  }

  return (
    <div className="space-y-1.5">
      <button
        type="button"
        onClick={handle}
        title="Click to reveal and copy"
        className="group flex w-full max-w-sm items-center gap-2 rounded border-2 border-dashed border-brand-main-600 bg-brand-main-900/60 px-3 py-2.5 text-left transition-colors hover:border-brand-secondary-500/60 hover:bg-brand-main-900"
      >
        <Iconify.Icon
          icon="ph:key-bold"
          className="size-3.5 shrink-0 text-brand-secondary-300"
        />
        <span
          className={cn(
            'flex-1 text-xs text-brand-main-100',
            revealed && 'break-all',
          )}
          style={{ fontFamily: BRAND_SANS }}
        >
          {revealed ? keyValue : maskApiKey(keyValue)}
        </span>
        <Iconify.Icon
          icon={copied ? 'lucide:check' : 'lucide:copy'}
          className={cn(
            'size-3.5 shrink-0',
            copied
              ? 'text-emerald-400 light:text-emerald-600'
              : 'text-brand-main-300 group-hover:text-brand-main-100',
          )}
        />
      </button>
      <p className="text-[11px] text-brand-main-300">
        {copied
          ? 'Copied to clipboard.'
          : 'Click to reveal and copy, then paste it into the snippet.'}
      </p>
    </div>
  )
}

// GalleryShell frames the right-panel galleries to match the console terminal:
// same width, entrance, and header treatment.
function GalleryShell({
  eyebrow,
  title,
  subtitle,
  children,
  footer,
  bodyClassName,
}: {
  eyebrow: string
  title: string
  subtitle: string
  children: ReactNode
  footer?: ReactNode
  bodyClassName?: string
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 14 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: 'easeOut' }}
      className="mx-auto w-full max-w-2xl"
    >
      <div className="mb-4">
        <span className="text-[11px] font-medium uppercase tracking-[0.16em] text-brand-secondary-300">
          {eyebrow}
        </span>
        <h2 className="mt-1 text-lg font-semibold text-brand-main-50">
          {title}
        </h2>
        <p className="mt-1 text-xs leading-5 text-brand-main-300">{subtitle}</p>
      </div>
      <div className={bodyClassName ?? 'space-y-2.5'}>{children}</div>
      {footer && <div className="mt-4">{footer}</div>}
    </motion.div>
  )
}

function Chip({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center rounded border border-brand-main-700 bg-brand-main-900 px-2 py-0.5 text-[10px] text-brand-main-200">
      {children}
    </span>
  )
}

// AgentGallery is the explorable agent picker that lives in the right panel. It
// creates on first pick and updates on reselect (keyed off a captured id, not
// firstAgent, to dodge the post-create invalidation window that would otherwise
// spawn duplicates). Reselect replaces the agent's config wholesale so switching
// away from the Coder template correctly drops its sandbox.
function AgentGallery({
  hasOpenAI,
  firstAgent,
  onScratch,
  onPin,
}: {
  hasOpenAI: boolean
  firstAgent: AgentDefinition | null
  onScratch: () => void
  onPin: () => void
}) {
  const createAgent = useCreateAgent()
  const updateAgent = useUpdateAgent()
  const model = defaultModel(hasOpenAI)
  const [createdId, setCreatedId] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const targetId = createdId ?? firstAgent?.id ?? null

  // Highlight the active template: the one just clicked, else inferred from the
  // existing agent's name (templates create with stable names).
  const activeId =
    selectedId ??
    AGENT_TEMPLATES.find((t) => t.build(model).name === firstAgent?.name)?.id ??
    null

  const apply = (template: AgentTemplate) => {
    if (pendingId) return
    setError(null)
    setPendingId(template.id)
    setSelectedId(template.id)
    onPin()
    const params = template.build(model)
    const onError = (err: unknown) => {
      setError(
        err instanceof Error ? err.message : 'Could not apply the template',
      )
    }
    if (targetId) {
      // Wholesale replace so the agent matches the new template exactly.
      updateAgent.mutate(
        {
          id: targetId,
          name: params.name,
          model: params.model,
          description: params.description,
          systemPrompt: params.systemPrompt,
          config: params.config ?? {},
        },
        { onError, onSettled: () => setPendingId(null) },
      )
    } else {
      createAgent.mutate(params, {
        onSuccess: (res) => setCreatedId(res.agent?.id ?? null),
        onError,
        onSettled: () => setPendingId(null),
      })
    }
  }

  return (
    <GalleryShell
      eyebrow="Step · agent"
      title="Choose a starter agent"
      subtitle={`Each comes with instructions and tools, running on ${model}. Pick one, switch any time, then continue.`}
      bodyClassName="space-y-2"
      footer={
        <button
          type="button"
          onClick={onScratch}
          className="text-xs text-brand-main-300 transition-colors hover:text-brand-main-100"
        >
          Start from scratch
        </button>
      }
    >
      {error && (
        <div className="flex items-center gap-1.5 rounded border border-red-500/25 bg-red-500/10 px-3 py-2 text-[11px] text-red-200 light:text-red-700">
          <Iconify.Icon icon="lucide:triangle-alert" className="size-3.5" />
          {error}
        </div>
      )}
      {AGENT_TEMPLATES.map((template) => {
        const isPending = pendingId === template.id
        const isActive = activeId === template.id
        const hasSandbox = !!(
          template.build(model).config as { sandbox?: unknown } | undefined
        )?.sandbox
        return (
          <button
            key={template.id}
            type="button"
            onClick={() => apply(template)}
            disabled={!!pendingId}
            className={cn(
              'group flex w-full flex-col gap-2 rounded border bg-brand-main-900 p-3.5 text-left transition-colors disabled:cursor-default',
              isActive
                ? 'border-brand-secondary-400 bg-brand-main-800/80'
                : template.accent
                  ? 'border-brand-secondary-500/40 hover:border-brand-secondary-400 hover:bg-brand-main-800'
                  : 'border-brand-main-700 hover:border-brand-secondary-500/50 hover:bg-brand-main-800',
            )}
          >
            <div className="flex items-center gap-2.5">
              <span
                className={cn(
                  'flex size-8 shrink-0 items-center justify-center rounded',
                  template.accent || isActive
                    ? 'bg-brand-secondary-500/15 text-brand-secondary-200'
                    : 'bg-brand-main-800 text-brand-main-100',
                )}
              >
                {isPending ? (
                  <Iconify.Icon
                    icon="lucide:loader-2"
                    className="size-4 animate-spin"
                  />
                ) : (
                  <Iconify.Icon icon={template.icon} className="size-4" />
                )}
              </span>
              <span className="text-sm font-medium text-brand-main-50">
                {template.title}
              </span>
              {hasSandbox && (
                <span className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-1.5 py-0.5 text-[10px] text-brand-main-200">
                  <Iconify.Icon icon="lucide:box" className="size-3" />
                  sandbox
                </span>
              )}
              {isActive && (
                <span className="ml-auto inline-flex items-center gap-1 rounded bg-brand-secondary-500/15 px-1.5 py-0.5 text-[10px] text-brand-secondary-200">
                  <Iconify.Icon icon="lucide:check" className="size-3" />
                  Selected
                </span>
              )}
            </div>
            <p className="text-xs leading-5 text-brand-main-200">
              {template.tagline}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {template.tags.map((tag) => (
                <Chip key={tag}>{tag}</Chip>
              ))}
            </div>
          </button>
        )
      })}
    </GalleryShell>
  )
}

// Small built-in fallback so the sandbox gallery always renders something even
// Curated sandbox presets for onboarding. All share the base image (the same
// one the agent form provisions) and differ in resources / framing. A "Custom"
// card alongside these hands off to the full form for anything else.
const SANDBOX_PRESETS: SandboxTemplate[] = [
  {
    id: 'sb_base',
    name: 'Standard',
    slug: 'base',
    description: 'General-purpose Linux for shell, files, and most languages.',
    icon: 'lucide:box',
    iconColor: '',
    image: 'ghcr.io/everstacklabs/sandbox:base',
    cpuLimit: 1,
    memoryMb: 512,
    diskMb: 1024,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'sb_perf',
    name: 'Performance',
    slug: 'perf',
    description: 'More CPU, memory, and disk for heavier builds and tooling.',
    icon: 'lucide:zap',
    iconColor: '',
    image: 'ghcr.io/everstacklabs/sandbox:base',
    cpuLimit: 2,
    memoryMb: 1024,
    diskMb: 2048,
    timeoutSeconds: 600,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'sb_ubuntu',
    name: 'Ubuntu',
    slug: 'ubuntu',
    description: 'Full Ubuntu base with apt and common system packages.',
    icon: 'simple-icons:ubuntu',
    iconColor: '',
    image: 'ghcr.io/everstacklabs/sandbox:base',
    cpuLimit: 1,
    memoryMb: 1024,
    diskMb: 2048,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
]

// SandboxGallery applies a sandbox preset to the agent created earlier. On
// cloud-managed instances it eagerly provisions a real sandbox (CreateSandbox
// spins up the container) and links the agent to it, marking the agent
// persistent so the sandbox is not reaped. Self-hosted has no sandbox backend,
// so there it just records the config (the sandbox comes up when the agent
// runs). Either way the config MERGES into the existing blob so it never wipes
// memory or skills the agent already carries.
function SandboxGallery({
  firstAgent,
  onScratch,
  onSkip,
  onPin,
}: {
  firstAgent: AgentDefinition | null
  onScratch: () => void
  onSkip: () => void
  onPin: () => void
}) {
  const cloud = isCloudManaged()
  const updateAgent = useUpdateAgent()
  const createSandbox = useCreateSandbox()
  const [appliedId, setAppliedId] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [provisioned, setProvisioned] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const apply = async (tpl: SandboxTemplate) => {
    if (pendingId || !firstAgent) return
    setError(null)
    setProvisioned(false)
    setPendingId(tpl.id)
    setAppliedId(tpl.id)
    onPin()
    const existing =
      (firstAgent.config as Record<string, any> | undefined) ?? {}
    try {
      if (cloud) {
        // Eagerly provision a real sandbox, then link the agent to it. Order
        // matters: provision first, link only on success, so a link failure
        // surfaces rather than silently leaving the agent pointing at nothing.
        const sb = await createSandbox.mutateAsync({
          image: tpl.image,
          cpuLimit: tpl.cpuLimit,
          memoryMb: tpl.memoryMb,
          diskMb: tpl.diskMb,
          timeoutSeconds: tpl.timeoutSeconds,
          networkMode: tpl.networkMode,
          name: `${tpl.name} sandbox`,
          idleRetentionSeconds: -1,
        })
        await updateAgent.mutateAsync({
          id: firstAgent.id,
          lifecycleMode: AgentLifecycleMode.PERSISTENT,
          config: {
            ...existing,
            sandbox: {
              enabled: true,
              linked_session_id: sb.sessionId,
              persistent: true,
            },
          },
        })
      } else {
        // No backend to provision against — record the config so the agent is
        // sandbox-enabled and the sandbox comes up on its first run.
        await updateAgent.mutateAsync({
          id: firstAgent.id,
          lifecycleMode: AgentLifecycleMode.PERSISTENT,
          config: {
            ...existing,
            sandbox: {
              enabled: true,
              image: tpl.image,
              cpu_limit: tpl.cpuLimit,
              memory_mb: tpl.memoryMb,
              disk_mb: tpl.diskMb,
              timeout_seconds: tpl.timeoutSeconds,
              network_mode: tpl.networkMode,
              persistent: true,
            },
          },
        })
      }
      setProvisioned(true)
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Could not provision the sandbox',
      )
    } finally {
      setPendingId(null)
    }
  }

  return (
    <GalleryShell
      eyebrow="Step · sandbox"
      title="Give your agent a sandbox"
      subtitle={
        cloud
          ? 'An isolated environment so the agent can run code, shell, and tools. Picking one provisions it now.'
          : 'An isolated environment so the agent can run code, shell, and tools. It spins up on the first run.'
      }
      footer={
        <button
          type="button"
          onClick={onSkip}
          className="text-xs text-brand-main-300 transition-colors hover:text-brand-main-100"
        >
          Skip for now
        </button>
      }
      bodyClassName="space-y-2"
    >
      {error && (
        <div className="flex items-center gap-1.5 rounded border border-red-500/25 bg-red-500/10 px-3 py-2 text-[11px] text-red-200 light:text-red-700">
          <Iconify.Icon icon="lucide:triangle-alert" className="size-3.5" />
          {error}
        </div>
      )}
      {provisioned && !error && (
        <div className="flex items-center gap-1.5 rounded border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-[11px] text-emerald-200 light:text-emerald-700">
          <Iconify.Icon icon="lucide:check" className="size-3.5" />
          {cloud
            ? 'Sandbox provisioned and linked to your agent.'
            : 'Sandbox configured — it spins up on the first run.'}
        </div>
      )}
      {SANDBOX_PRESETS.map((tpl) => {
        const isPending = pendingId === tpl.id
        const isActive = appliedId === tpl.id
        return (
          <button
            key={tpl.id}
            type="button"
            onClick={() => apply(tpl)}
            disabled={!!pendingId}
            className={cn(
              'group flex w-full items-center gap-3 rounded border bg-brand-main-900 p-3.5 text-left transition-colors disabled:cursor-default',
              isActive
                ? 'border-brand-secondary-400 bg-brand-main-800/80'
                : 'border-brand-main-700 hover:border-brand-secondary-500/50 hover:bg-brand-main-800',
            )}
          >
            <span className="flex size-8 shrink-0 items-center justify-center rounded bg-brand-main-800 text-brand-main-100">
              {isPending ? (
                <Iconify.Icon
                  icon="lucide:loader-2"
                  className="size-4 animate-spin"
                />
              ) : (
                <Iconify.Icon
                  icon={tpl.icon || 'lucide:box'}
                  className="size-4"
                />
              )}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5">
                <span className="text-sm font-medium text-brand-main-50">
                  {tpl.name}
                </span>
                {isPending && (
                  <span className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-1.5 py-0.5 text-[10px] text-brand-main-200">
                    {cloud ? 'Provisioning…' : 'Saving…'}
                  </span>
                )}
                {isActive && !isPending && (
                  <span className="inline-flex items-center gap-1 rounded bg-brand-secondary-500/15 px-1.5 py-0.5 text-[10px] text-brand-secondary-200">
                    <Iconify.Icon icon="lucide:check" className="size-3" />
                    {cloud ? 'Ready' : 'Added'}
                  </span>
                )}
              </div>
              <p className="text-xs leading-5 text-brand-main-200">
                {tpl.description}
              </p>
            </div>
            <div className="hidden shrink-0 items-center gap-1.5 sm:flex">
              <Chip>{tpl.cpuLimit} vCPU</Chip>
              <Chip>{tpl.memoryMb} MB</Chip>
              <Chip>{Math.round(tpl.diskMb / 1024)} GB</Chip>
            </div>
          </button>
        )
      })}
      {/* Custom hands off to the full form for a bespoke image, resources, git
          repo, or network policy. */}
      <button
        type="button"
        onClick={onScratch}
        disabled={!!pendingId}
        className="group flex w-full items-center gap-3 rounded border border-dashed border-brand-main-700 bg-brand-main-900/50 p-3.5 text-left transition-colors hover:border-brand-secondary-500/50 hover:bg-brand-main-800 disabled:cursor-default"
      >
        <span className="flex size-8 shrink-0 items-center justify-center rounded bg-brand-main-800 text-brand-main-100">
          <Iconify.Icon icon="lucide:sliders-horizontal" className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <span className="text-sm font-medium text-brand-main-50">Custom</span>
          <p className="text-xs leading-5 text-brand-main-200">
            Set your own image, resources, git repo, and network policy.
          </p>
        </div>
        <Iconify.Icon
          icon="lucide:arrow-up-right"
          className="size-4 shrink-0 text-brand-main-300 group-hover:text-brand-main-100"
        />
      </button>
    </GalleryShell>
  )
}

// StepNav is the persistent Back / Next control under the right panel so the
// user can move through the steps freely, in any order, without relying on
// auto-advance or hunting the spine.
function StepNav({
  steps,
  currentId,
  onGo,
  onFinish,
  finishLabel,
}: {
  steps: OnboardingLaunchStep[]
  currentId: string | undefined
  onGo: (id: string) => void
  onFinish: () => void
  finishLabel: string
}) {
  const i = steps.findIndex((s) => s.id === currentId)
  const idx = i < 0 ? 0 : i
  const prev = idx > 0 ? steps[idx - 1] : null
  const next = idx < steps.length - 1 ? steps[idx + 1] : null
  return (
    <div className="mx-auto mt-5 flex w-full max-w-2xl items-center justify-between">
      <Button
        variant="outline"
        onClick={() => prev && onGo(prev.id)}
        disabled={!prev}
        className="gap-2"
      >
        <Iconify.Icon icon="lucide:arrow-left" className="size-4" />
        Back
      </Button>
      <span className="text-[11px] text-brand-main-300">
        Step {idx + 1} of {steps.length}
      </span>
      {next ? (
        <Button onClick={() => onGo(next.id)} className="gap-2">
          Next
          <Iconify.Icon icon="lucide:arrow-right" className="size-4" />
        </Button>
      ) : (
        // Last step: the terminal action takes the user into the product.
        <Button onClick={onFinish} className="gap-2">
          {finishLabel}
          <Iconify.Icon icon="lucide:arrow-up-right" className="size-4" />
        </Button>
      )}
    </div>
  )
}

// ─── Terminal (right pane) ──────────────────────────────────────────

type TerminalMode = 'waiting' | 'listening' | 'received'

function ConsoleTerminal({
  activePath,
  snippets,
  steps,
  mode,
  hasTestKey,
  runner,
}: {
  activePath: OnboardingPath
  snippets: LangSnippets
  steps: OnboardingLaunchStep[]
  mode: TerminalMode
  hasTestKey?: boolean
  runner?: ReactNode
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 14 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: 'easeOut' }}
      className="mx-auto w-full max-w-2xl"
    >
      <div className="overflow-hidden rounded border border-brand-main-700 bg-brand-main-950 shadow-2xl shadow-brand-main-950/70">
        <div className="flex items-center gap-2 border-b border-brand-main-800 bg-brand-main-900/60 px-4 py-2.5">
          <span className="size-2.5 rounded-full bg-brand-main-600" />
          <span className="size-2.5 rounded-full bg-brand-main-600" />
          <span className="size-2.5 rounded-full bg-brand-main-600" />
          <span className="ml-2 text-[11px] text-brand-main-200">
            everstack · first {activePath === 'gateway' ? 'gateway ' : ''}
            request
          </span>
          <span className="ml-auto inline-flex items-center gap-1.5 rounded border border-brand-main-700 px-2 py-0.5 text-[10px] text-brand-main-200">
            {mode === 'received' ? (
              <>
                <span className="size-1.5 rounded-full bg-emerald-400" />
                live
              </>
            ) : mode === 'listening' ? (
              <>
                <span className="size-1.5 animate-pulse rounded-full bg-brand-secondary-400" />
                listening
              </>
            ) : (
              <>
                <span className="size-1.5 rounded-full bg-brand-main-500" />
                idle
              </>
            )}
          </span>
        </div>

        <div className="p-4">
          <CodeHero snippets={snippets} />
        </div>

        <div className="space-y-3 border-t border-brand-main-800 bg-brand-main-900/40 px-4 py-3">
          <TerminalOutput mode={mode} steps={steps} />
          {runner}
        </div>
      </div>
      <p className="mt-3 text-center text-[11px] text-brand-main-300">
        {hasTestKey
          ? 'Replace <YOUR_KEY> with the key from the API key step.'
          : 'Replace <YOUR_KEY> with an API key once you generate one.'}
      </p>
    </motion.div>
  )
}

function CodeHero({ snippets }: { snippets: LangSnippets }) {
  return (
    <Tabs defaultValue="curl">
      <TabsList className="mb-3 inline-flex h-auto gap-1 rounded border border-brand-main-700 bg-brand-main-900/60 p-1">
        {CODE_TABS.map((tab) => (
          <TabsTrigger
            key={tab.value}
            value={tab.value}
            className="rounded px-3 py-1 text-xs text-brand-main-200 transition-colors data-[state=active]:bg-brand-secondary-500/15 data-[state=active]:text-brand-secondary-200"
          >
            {tab.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {CODE_TABS.map((tab) => (
        <TabsContent key={tab.value} value={tab.value} className="mt-0">
          <CodeView code={snippets[tab.value]} language={tab.language} />
        </TabsContent>
      ))}
    </Tabs>
  )
}

function CodeView({ code, language }: { code: string; language: string }) {
  const ext =
    language === 'typescript'
      ? 'ts'
      : language === 'go'
        ? 'go'
        : language === 'bash'
          ? 'sh'
          : 'py'
  const data = [{ language, filename: `example.${ext}`, code }]
  return (
    <CodeBlock
      data={data}
      defaultValue={language}
      className="rounded border-brand-main-800 bg-brand-main-950"
    >
      <CodeBlockBody>
        {(item) => (
          <CodeBlockItem
            key={item.language}
            value={item.language}
            className="relative"
          >
            {/* Fixed height with a dark fill: every language tab is the exact
                same height. The horizontal scrollbar lives on THIS box (pinned
                at the panel's bottom edge) rather than the inner <code>, which
                otherwise only revealed it after scrolling to the last line.
                Overriding the component's [&_code]:overflow-x-auto lets long
                lines overflow into this scroller instead. */}
            <div
              className="h-[24rem] overflow-auto bg-brand-main-950 scrollbar-macos"
              style={{ fontFamily: BRAND_SANS }}
            >
              <CodeBlockContent
                language={language}
                className="!bg-brand-main-950 [&_.shiki]:!bg-transparent [&_pre]:!bg-transparent [&_code]:!overflow-x-visible"
              >
                {item.code}
              </CodeBlockContent>
            </div>
            <div className="absolute right-2 top-2">
              <CodeBlockCopyButton />
            </div>
          </CodeBlockItem>
        )}
      </CodeBlockBody>
    </CodeBlock>
  )
}

function TerminalOutput({
  mode,
  steps,
}: {
  mode: TerminalMode
  steps: OnboardingLaunchStep[]
}) {
  const prereqs = steps.filter((s) => s.id !== 'test')
  const prereqLabel = (id: string, fallback: string) => {
    if (id === 'provider') return 'provider connected'
    if (id === 'api-key') return 'api key generated'
    if (id === 'agent') return 'agent created'
    if (id === 'sandbox') return 'sandbox provisioned'
    return fallback.toLowerCase()
  }
  return (
    <div className="space-y-1.5 text-[11px] leading-5">
      {prereqs.map((step) => (
        <div key={step.id} className="flex items-center gap-2">
          <Iconify.Icon
            icon={step.complete ? 'lucide:check' : 'lucide:circle-dashed'}
            className={cn(
              'size-3.5',
              step.complete
                ? 'text-emerald-400 light:text-emerald-600'
                : 'text-brand-main-400',
            )}
          />
          <span
            className={
              step.complete ? 'text-brand-main-100' : 'text-brand-main-300'
            }
          >
            {prereqLabel(step.id, step.label)}
          </span>
        </div>
      ))}
      <div className="flex items-center gap-2 pt-1">
        {mode === 'received' ? (
          <>
            <Iconify.Icon
              icon="lucide:check"
              className="size-3.5 text-emerald-400 light:text-emerald-600"
            />
            <span className="text-emerald-300 light:text-emerald-600">
              first request received
            </span>
          </>
        ) : mode === 'listening' ? (
          <>
            <span className="size-1.5 animate-pulse rounded-full bg-brand-secondary-400" />
            <span className="text-brand-secondary-200">
              listening for your first request
            </span>
          </>
        ) : (
          <>
            <Iconify.Icon
              icon="lucide:circle-dashed"
              className="size-3.5 text-brand-main-400"
            />
            <span className="text-brand-main-300">awaiting first request</span>
          </>
        )}
      </div>
    </div>
  )
}

// ─── In-UI first request ────────────────────────────────────────────

type ChatTurn = { role: 'user' | 'assistant' | 'error'; content: string }

// InlineFirstRequest lets the user send a real message to their new agent (or
// the gateway) straight from the console and watch the reply land, instead of
// copying a snippet elsewhere. It generates a one-time test key on first send,
// then reuses it. The gateway path sends the running transcript so it feels
// like a conversation; the agent invoke path sends the latest message.
function InlineFirstRequest({
  activePath,
  firstAgentId,
  hasOpenAI,
  testKey,
  onKey,
}: {
  activePath: OnboardingPath
  firstAgentId: string | null
  hasOpenAI: boolean
  testKey: string | null
  onKey: (key: string) => void
}) {
  const createApiKey = useCreateApiKey()
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [draft, setDraft] = useState('Say hello from Everstack')
  const [sending, setSending] = useState(false)

  const send = async () => {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    const history = turns.filter((t) => t.role !== 'error')
    setTurns((prev) => [...prev, { role: 'user', content: text }])
    setDraft('')
    try {
      let key = testKey
      if (!key) {
        const res = await createApiKey.mutateAsync({
          name: 'Onboarding key',
          type: ApiKeyType.USER,
        })
        key = res.apiKey?.hash ?? null
        if (key) onKey(key)
      }
      if (!key) throw new Error('Could not generate a test key')

      const base = gatewayBase()
      const url =
        activePath === 'gateway'
          ? `${base}/v1/chat/completions`
          : `${base}/v1/deploy/${firstAgentId ?? ''}/invoke`
      const body =
        activePath === 'gateway'
          ? {
              model: defaultModel(hasOpenAI),
              messages: [
                ...history.map((t) => ({ role: t.role, content: t.content })),
                { role: 'user', content: text },
              ],
            }
          : { message: text }

      const res = await fetch(url, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${key}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
      })
      const raw = await res.text()
      if (!res.ok) {
        setTurns((prev) => [
          ...prev,
          {
            role: 'error',
            content:
              `${res.status} ${res.statusText} ${raw.slice(0, 200)}`.trim(),
          },
        ])
        return
      }
      let content = raw
      try {
        const json = JSON.parse(raw)
        content =
          json?.choices?.[0]?.message?.content ??
          json?.outputText ??
          json?.message ??
          JSON.stringify(json)
      } catch {
        // keep raw text
      }
      setTurns((prev) => [
        ...prev,
        { role: 'assistant', content: String(content).slice(0, 600) },
      ])
    } catch (err) {
      setTurns((prev) => [
        ...prev,
        {
          role: 'error',
          content: err instanceof Error ? err.message : 'Request failed',
        },
      ])
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="space-y-2.5 border-t border-brand-main-800 pt-3">
      {turns.length > 0 && (
        <div className="max-h-56 space-y-2 overflow-auto pr-1 scrollbar-macos">
          {turns.map((turn, i) => (
            <ChatBubble key={i} turn={turn} />
          ))}
          {sending && (
            <div className="flex items-center gap-1.5 text-[11px] text-brand-main-300">
              <Iconify.Icon
                icon="lucide:loader-2"
                className="size-3.5 animate-spin"
              />
              waiting for reply
            </div>
          )}
        </div>
      )}
      <div className="flex items-end gap-2">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              void send()
            }
          }}
          rows={1}
          placeholder={
            activePath === 'gateway'
              ? 'Send a message to the gateway'
              : 'Send your agent a message'
          }
          className="min-h-9 flex-1 resize-none rounded border border-brand-main-700 bg-brand-main-900 px-3 py-2 text-xs text-brand-main-50 placeholder:text-brand-main-400 focus:border-brand-secondary-500/60 focus:outline-none"
          style={{ fontFamily: BRAND_SANS }}
        />
        <Button
          onClick={() => void send()}
          disabled={sending || !draft.trim()}
          className="gap-2"
        >
          {sending ? (
            <Iconify.Icon
              icon="lucide:loader-2"
              className="size-4 animate-spin"
            />
          ) : (
            <Iconify.Icon icon="lucide:send" className="size-4" />
          )}
          Send
        </Button>
      </div>
      <p className="text-[11px] text-brand-main-300">
        {testKey
          ? 'Sends a real request with your test key.'
          : 'Generates a one-time test key and sends a real request from here.'}
      </p>
    </div>
  )
}

function ChatBubble({ turn }: { turn: ChatTurn }) {
  if (turn.role === 'user') {
    return (
      <div className="ml-auto max-w-[85%] rounded border border-brand-secondary-500/30 bg-brand-secondary-500/10 px-3 py-1.5 text-[11px] leading-5 text-brand-secondary-100">
        {turn.content}
      </div>
    )
  }
  if (turn.role === 'error') {
    return (
      <div className="max-w-[85%] rounded border border-red-500/25 bg-red-500/10 px-3 py-1.5 text-[11px] leading-5 text-red-200 light:text-red-700">
        <span className="mr-1 inline-flex items-center align-[-2px]">
          <Iconify.Icon icon="lucide:triangle-alert" className="size-3.5" />
        </span>
        {turn.content}
      </div>
    )
  }
  return (
    <div className="max-w-[85%] rounded border border-brand-main-700 bg-brand-main-900 px-3 py-1.5 text-[11px] leading-5 text-brand-main-100">
      <p className="whitespace-pre-wrap break-words">{turn.content}</p>
    </div>
  )
}

// ─── Hosted instance ────────────────────────────────────────────────

function HostedInstanceScreen({
  onBack,
  onSkip,
}: {
  onBack: () => void
  onSkip: () => void
}) {
  return (
    <OnboardingFrame
      left={
        <div className="flex min-h-full flex-col">
          <div className="flex flex-1 flex-col justify-center py-10">
            <p className="text-xs font-medium uppercase tracking-[0.2em] text-brand-secondary-300">
              Hosted
            </p>
            <h1 className="mt-4 text-3xl font-semibold leading-tight tracking-tight md:text-4xl">
              Production runs on
              <br />
              Everstack Cloud.
            </h1>
            <p className="mt-4 max-w-sm text-sm leading-6 text-brand-main-200">
              This self-hosted admin runs agents and gateway traffic locally.
              Move to Cloud for managed regions, hosted subdomains, billing, and
              team access.
            </p>
            <div className="mt-7 flex items-center gap-4 text-xs text-brand-main-300">
              <button
                type="button"
                onClick={onBack}
                className="inline-flex items-center gap-1 transition-colors hover:text-brand-main-100"
              >
                <Iconify.Icon icon="lucide:arrow-left" className="size-3" />
                Choose a different path
              </button>
              <span className="text-brand-main-600">·</span>
              <button
                type="button"
                onClick={onSkip}
                className="transition-colors hover:text-brand-main-100"
              >
                Continue self-hosted
              </button>
            </div>
          </div>
        </div>
      }
      right={
        <motion.div
          initial={{ opacity: 0, y: 14 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.35, ease: 'easeOut' }}
          className="mx-auto w-full max-w-md rounded border border-brand-main-700 bg-brand-main-900/60 p-6 text-center"
        >
          <div className="mx-auto mb-4 flex size-14 items-center justify-center rounded border border-brand-secondary-500/30 bg-brand-secondary-500/10 text-brand-secondary-300">
            <Iconify.Icon icon="lucide:cloud" className="size-7" />
          </div>
          <p className="text-sm leading-6 text-brand-main-100">
            Spin up a managed Everstack instance in a click, then come back and
            connect.
          </p>
          <a
            href="https://app.everstack.ai"
            target="_blank"
            rel="noreferrer"
            className="mt-5 inline-flex w-full items-center justify-center gap-2 rounded bg-brand-secondary-500 px-5 py-2.5 text-sm font-medium text-brand-main-950 transition-colors hover:bg-brand-secondary-400"
          >
            Open Everstack Cloud
            <Iconify.Icon icon="lucide:arrow-up-right" className="size-4" />
          </a>
        </motion.div>
      }
    />
  )
}

// ─── Completion ─────────────────────────────────────────────────────

function CompletionScreen({
  activePath,
  firstAgentId,
  hasOpenAI,
  steps,
  markDone,
}: {
  activePath: OnboardingPath
  firstAgentId: string | null
  hasOpenAI: boolean
  steps: OnboardingLaunchStep[]
  markDone: () => void
}) {
  const navigate = useNavigate()
  const snippets = useMemo(
    () => buildSnippets(activePath, firstAgentId, hasOpenAI),
    [activePath, firstAgentId, hasOpenAI],
  )

  const goResult = () => {
    markDone()
    if (activePath === 'agent' && firstAgentId) {
      navigate({
        to: '/deployments/agents/$agentId/chat',
        params: { agentId: firstAgentId },
      })
      return
    }
    navigate({ to: '/observability/logs' })
  }

  const goDashboard = () => {
    markDone()
    navigate({ to: '/chat', search: { session: undefined, model: undefined } })
  }

  return (
    <OnboardingFrame
      left={
        <div className="flex min-h-full flex-col">
          <div className="flex flex-1 flex-col justify-center py-10">
            <motion.div
              initial={{ scale: 0.6, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              transition={{ type: 'spring', stiffness: 240, damping: 18 }}
              className="mb-6 flex size-12 items-center justify-center rounded border border-brand-secondary-500/40 bg-brand-secondary-500/15 text-brand-secondary-200"
            >
              <Iconify.Icon icon="lucide:check" className="size-6" />
            </motion.div>
            <h1 className="text-3xl font-semibold tracking-tight md:text-4xl">
              You are live.
            </h1>
            <p className="mt-4 max-w-sm text-sm leading-6 text-brand-main-200">
              Everstack detected your provider, authentication, and a real
              request. Logs, traces, and sessions are flowing.
            </p>
            <div className="mt-7 flex flex-col gap-3 sm:flex-row sm:items-center">
              <Button onClick={goResult} className="gap-2">
                {activePath === 'agent'
                  ? 'Open agent chat'
                  : 'View observability'}
                <Iconify.Icon icon="lucide:arrow-right" className="size-4" />
              </Button>
              <button
                type="button"
                onClick={goDashboard}
                className="text-sm text-brand-main-200 transition-colors hover:text-brand-main-50"
              >
                Go to dashboard
              </button>
            </div>
          </div>
        </div>
      }
      right={
        <ConsoleTerminal
          activePath={activePath}
          snippets={snippets}
          steps={steps}
          mode="received"
        />
      }
    />
  )
}
