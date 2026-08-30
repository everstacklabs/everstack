import { createFileRoute } from '@tanstack/react-router'
import { useAgent } from '@/hooks/deployments/use-agents'
import {
  AgentLifecycleMode,
  AgentLifecycleStatus,
  AgentMode,
  TaskPermissionMode,
  type AgentDefinition,
} from '@/server/agents'
import { LIFECYCLE_STATUS_META } from '@/components/deployments/agents/lifecycle-status-badge'
import { Loader } from '@everstack/ui/components'
import { getSandboxInstance } from '@/server/sandbox'
import { useSession } from '@/hooks/auth/use-auth'
import { formatTimestamp } from '@everstack/utils/functions/index'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  CheckCircle2,
  Bot,
  BrainCircuit,
  Clock3,
  FileText,
  Gauge,
  Hammer,
  LoaderCircle,
  MessageSquareText,
  Network,
  Server,
  Sparkles,
  XCircle,
} from 'lucide-react'
import { TOOL_CATALOG } from '@/components/deployments/agents/tool-catalog'

export const Route = createFileRoute('/deployments/agents/$agentId/overview')({
  component: OverviewRoute,
})

function ConfigField({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex items-start justify-between gap-3 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2.5">
      <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div
        className={`text-sm text-right text-brand-main-100 ${mono ? 'font-mono' : ''}`}
      >
        {value}
      </div>
    </div>
  )
}

function StatCard({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="rounded border border-brand-main-600 bg-brand-main-900/50 px-4 py-3">
      <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div
        className={`mt-2 text-sm text-white light:text-brand-main-50 ${mono ? 'font-mono' : 'font-medium'}`}
      >
        {value}
      </div>
    </div>
  )
}

function SectionCard({
  title,
  description,
  icon: Icon,
  children,
}: {
  title: string
  description: string
  icon: typeof Bot
  children: ReactNode
}) {
  return (
    <section className="rounded border border-brand-main-600 bg-brand-main-900/50 p-4">
      <div className="mb-4 flex items-start gap-3">
        <div className="rounded border border-brand-secondary-500/30 bg-brand-secondary-600/15 p-2 text-brand-secondary-300">
          <Icon className="h-4 w-4" />
        </div>
        <div>
          <div className="text-sm font-medium text-white light:text-brand-main-50">{title}</div>
          <div className="text-xs text-white/50 light:text-black/50">{description}</div>
        </div>
      </div>
      {children}
    </section>
  )
}

function StatusPill({
  className,
  children,
}: {
  className: string
  children: ReactNode
}) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-1 text-[11px] font-medium ${className}`}
    >
      {children}
    </span>
  )
}

function modeLabel(mode: number | undefined) {
  return mode === AgentMode.SUBAGENT ? 'Subagent' : 'Primary'
}

function permissionLabel(mode: number | undefined) {
  if (mode === TaskPermissionMode.ALWAYS) return 'Always allow'
  if (mode === TaskPermissionMode.DENY) return 'Always deny'
  return 'Ask first'
}

const SANDBOX_RUNTIME_TOOLS = [
  'sandbox_execute',
  'sandbox_shell',
  'sandbox_write_file',
  'sandbox_read_file',
  'sandbox_list_files',
  'sandbox_edit',
  'sandbox_grep',
  'sandbox_glob',
  'sandbox_patch',
  'sandbox_expose_port',
  'sandbox_unexpose_port',
  'sandbox_list_ports',
  'sandbox_list_templates',
  'sandbox_set_template',
  'sandbox_git_clone',
  'schedule_cron',
]

const BROWSER_RUNTIME_TOOLS = [
  'browser_navigate',
  'browser_observe',
  'browser_click',
  'browser_type',
  'browser_screenshot',
  'browser_evaluate',
  'browser_wait',
  'browser_scroll',
  'browser_select',
  'browser_tabs',
]

const TOOL_LABELS = new Map(
  TOOL_CATALOG.flatMap((category) =>
    category.tools.map((tool) => [tool.name, tool.label] as const),
  ),
)

const TOOL_CATEGORIES = new Map(
  TOOL_CATALOG.flatMap((category) =>
    category.tools.map((tool) => [tool.name, category] as const),
  ),
)

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function getPredictedRuntimeTools(agent: AgentDefinition | undefined) {
  if (!agent) return []

  const tools = new Set(agent.tools ?? [])
  const config = asRecord(agent.config)
  const sandbox = asRecord(config?.sandbox)
  const browser = asRecord(config?.browser)
  const spawn = asRecord(config?.spawn)
  const fork = asRecord(config?.fork)
  const skills = Array.isArray(config?.skills) ? config.skills : []

  tools.add('create_workflow')
  tools.add('ask_user')

  if (sandbox?.enabled === true) {
    for (const tool of SANDBOX_RUNTIME_TOOLS) tools.add(tool)
  }

  if (sandbox?.enabled === true && browser?.enabled === true) {
    for (const tool of BROWSER_RUNTIME_TOOLS) tools.add(tool)
  }

  if (spawn?.enabled === true) {
    tools.add('spawn_agent')
    tools.add('parallel_tasks')
    if (spawn.async === true) {
      tools.add('check_job')
    }
  }

  if (fork?.enabled === true) {
    tools.add('fork')
  }

  if (sandbox?.enabled === true && skills.length > 0) {
    tools.add('use_skill')
  }

  if (
    typeof sandbox?.git_repo_url === 'string' &&
    sandbox.git_repo_url.trim().length > 0
  ) {
    tools.add('repo_glob')
    tools.add('repo_read_file')
  }

  return Array.from(tools).sort((a, b) => a.localeCompare(b))
}

function toolLabel(toolName: string) {
  return TOOL_LABELS.get(toolName) ?? toolName
}

type StepState = 'complete' | 'current' | 'pending' | 'failed'

function StepIcon({ state }: { state: StepState }) {
  if (state === 'complete') {
    return <CheckCircle2 className="h-4 w-4 text-emerald-300 light:text-emerald-600" />
  }
  if (state === 'current') {
    return (
      <LoaderCircle className="h-4 w-4 animate-spin text-brand-secondary-300" />
    )
  }
  if (state === 'failed') {
    return <XCircle className="h-4 w-4 text-red-300 light:text-red-600" />
  }
  return <Clock3 className="h-4 w-4 text-white/35 light:text-black/35" />
}

function stepCardClass(state: StepState) {
  if (state === 'complete') {
    return 'border-emerald-500/25 bg-emerald-500/10'
  }
  if (state === 'current') {
    return 'border-brand-secondary-500/30 bg-brand-secondary-600/10'
  }
  if (state === 'failed') {
    return 'border-red-500/25 bg-red-500/10'
  }
  return 'border-brand-main-600 bg-brand-main-900/40'
}

function getProvisionStepState(
  agent: AgentDefinition,
  sandboxStatus?: string,
): Array<{
  key: string
  title: string
  description: string
  state: StepState
  icon: typeof Bot
}> {
  const lifecycle = agent.lifecycleStatus
  const normalizedSandboxStatus = sandboxStatus?.toLowerCase()
  const terminalFailure =
    lifecycle === AgentLifecycleStatus.FAILED ||
    lifecycle === AgentLifecycleStatus.TERMINATED
  const sandboxReady =
    normalizedSandboxStatus === 'running' ||
    ((lifecycle === AgentLifecycleStatus.IDLE ||
      lifecycle === AgentLifecycleStatus.RUNNING ||
      lifecycle === AgentLifecycleStatus.SLEEPING ||
      lifecycle === AgentLifecycleStatus.WAKING) &&
      Boolean(agent.sandboxId))
  const sandboxProvisioning =
    !sandboxReady &&
    (normalizedSandboxStatus === 'pending' ||
      lifecycle === AgentLifecycleStatus.CREATED ||
      lifecycle === AgentLifecycleStatus.PROVISIONING ||
      lifecycle === AgentLifecycleStatus.WAKING)

  return [
    {
      key: 'created',
      title: 'Agent created',
      description: 'Definition saved and persistent runtime requested.',
      state: 'complete',
      icon: Bot,
    },
    {
      key: 'sandbox',
      title: 'Sandbox provisioned',
      description: sandboxReady
        ? 'Persistent sandbox is present and reachable.'
        : sandboxProvisioning
          ? 'Creating the long-running sandbox environment.'
          : 'Waiting for the runtime environment to be created.',
      state: terminalFailure
        ? 'failed'
        : sandboxReady
          ? 'complete'
          : sandboxProvisioning
            ? 'current'
            : 'pending',
      icon: Server,
    },
    {
      key: 'networking',
      title: 'Networking ready',
      description: sandboxReady
        ? lifecycle === AgentLifecycleStatus.PROVISIONING
          ? 'Applying ingress and egress rules before activation.'
          : 'Ingress and egress policies are applied.'
        : 'Networking is configured after the sandbox comes up.',
      state: terminalFailure
        ? 'failed'
        : sandboxReady
          ? lifecycle === AgentLifecycleStatus.PROVISIONING
            ? 'current'
            : 'complete'
          : 'pending',
      icon: Network,
    },
  ]
}

function groupToolsByCategory(toolNames: string[]) {
  const groups = new Map<
    string,
    {
      label: string
      description: string
      tools: string[]
    }
  >()

  for (const toolName of toolNames) {
    const category = TOOL_CATEGORIES.get(toolName)
    const key = category?.id ?? 'other'
    const existing = groups.get(key)

    if (existing) {
      existing.tools.push(toolName)
      continue
    }

    groups.set(key, {
      label: category?.label ?? 'Other',
      description:
        category?.description ??
        'Tools inferred at runtime that do not map to a built-in category.',
      tools: [toolName],
    })
  }

  return Array.from(groups.values()).sort((a, b) => {
    if (b.tools.length !== a.tools.length)
      return b.tools.length - a.tools.length
    return a.label.localeCompare(b.label)
  })
}

function OverviewRoute() {
  const { agentId } = Route.useParams()
  const { data: agent } = useAgent(agentId)
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''
  const sandboxId = agent?.sandboxId ?? ''

  const { data: sandboxInstance } = useQuery({
    queryKey: ['agent-overview-sandbox', orgId, sandboxId || null],
    queryFn: () => getSandboxInstance(orgId, sandboxId),
    enabled:
      !!agent &&
      agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT &&
      !!sandboxId &&
      !!orgId,
    refetchInterval:
      agent?.lifecycleStatus === AgentLifecycleStatus.PROVISIONING ||
      agent?.lifecycleStatus === AgentLifecycleStatus.CREATED
        ? 3000
        : false,
    retry: false,
    staleTime: 5000,
  })

  if (!agent) {
    return (
      <div className="flex-1 flex items-center justify-center h-full">
        <Loader loaderText="Loading overview..." />
      </div>
    )
  }

  const isPersistent = agent.lifecycleMode === AgentLifecycleMode.PERSISTENT
  const lifecycleLabel = isPersistent
    ? (LIFECYCLE_STATUS_META[agent.lifecycleStatus]?.label ?? 'Created')
    : 'Ephemeral'
  const promptLineCount = agent.systemPrompt
    ? agent.systemPrompt.split('\n').filter(Boolean).length
    : 0
  const runtimeTools = getPredictedRuntimeTools(agent)
  const runtimeToolGroups = groupToolsByCategory(runtimeTools)
  const configuredTools = agent.tools ?? []
  const provisionSteps = isPersistent
    ? getProvisionStepState(agent, sandboxInstance?.status)
    : []

  return (
    <div className="h-full overflow-y-auto px-4 py-4">
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <section className="relative overflow-hidden rounded border border-brand-main-600 bg-brand-main-900/50 p-5">
          <div className="absolute -right-12 top-0 h-40 w-40 rounded-full bg-brand-secondary-500/10 blur-3xl" />
          <div
            className="absolute bottom-0 left-0 h-24 w-24 rounded-full opacity-20 blur-2xl"
            style={{
              backgroundColor:
                agent.color || 'var(--color-brand-secondary-500)',
            }}
          />

          <div className="relative flex flex-col gap-6 xl:flex-row xl:items-start xl:justify-between">
            <div className="max-w-2xl space-y-4">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/80">
                  <div
                    className="flex h-7 w-7 items-center justify-center rounded opacity-90"
                    style={{
                      backgroundColor:
                        agent.color || 'var(--color-brand-secondary-500)',
                    }}
                  >
                    <Bot className="h-4 w-4 text-white light:text-brand-main-50" />
                  </div>
                </div>
                <div>
                  <div className="flex items-center gap-2 text-[10px] uppercase tracking-wide text-brand-secondary-300">
                    <Sparkles className="h-3.5 w-3.5" />
                    Agent overview
                  </div>
                  <h1 className="mt-1 text-2xl font-semibold tracking-tight text-white light:text-brand-main-50">
                    {agent.name}
                  </h1>
                </div>
              </div>

              <p className="max-w-xl text-sm leading-6 text-white/50 light:text-black/50">
                {agent.description ||
                  'A focused execution profile with its runtime, prompting, and tool configuration collected in one place.'}
              </p>

              <div className="flex flex-wrap gap-2">
                <StatusPill
                  className={
                    agent.enabled
                      ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600 border border-emerald-500/25'
                      : 'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25'
                  }
                >
                  {agent.enabled ? 'Enabled' : 'Disabled'}
                </StatusPill>
                <StatusPill
                  className={
                    agent.mode === AgentMode.SUBAGENT
                      ? 'bg-amber-500/15 text-amber-300 light:text-amber-700 border border-amber-500/25'
                      : 'bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/25'
                  }
                >
                  {modeLabel(agent.mode)}
                </StatusPill>
                <StatusPill
                  className={
                    isPersistent
                      ? 'bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/25'
                      : 'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25'
                  }
                >
                  {isPersistent ? 'Persistent runtime' : 'Ephemeral runtime'}
                </StatusPill>
                {isPersistent && (
                  <StatusPill
                    className={`${LIFECYCLE_STATUS_META[agent.lifecycleStatus]?.colors ?? 'bg-brand-main-500/30 text-brand-main-200'}`}
                  >
                    {lifecycleLabel}
                  </StatusPill>
                )}
                {agent.hidden && (
                  <StatusPill className="bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25">
                    Hidden
                  </StatusPill>
                )}
                {agent.mentionAlias && (
                  <StatusPill className="bg-brand-main-500/30 text-brand-main-100 border border-brand-main-500/25">
                    @{agent.mentionAlias}
                  </StatusPill>
                )}
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2 xl:min-w-[340px] xl:max-w-[380px]">
              <StatCard label="Model" value={agent.model} mono />
              <StatCard label="Tools" value={String(runtimeTools.length)} />
              <StatCard label="Max turns" value={String(agent.maxTurns)} />
              <StatCard
                label="Tool budget"
                value={String(agent.maxToolCallsPerTurn)}
              />
            </div>
          </div>
        </section>

        <div className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
          <SectionCard
            title="Runtime profile"
            description="Operational limits and execution defaults for this agent."
            icon={Gauge}
          >
            <div className="grid gap-3 md:grid-cols-2">
              <ConfigField
                label="Lifecycle"
                value={isPersistent ? lifecycleLabel : 'Ephemeral'}
              />
              <ConfigField
                label="Permission mode"
                value={permissionLabel(agent.taskPermissionMode)}
              />
              <ConfigField
                label="Working directory"
                value={agent.workingDirectory || '-'}
                mono
              />
              <ConfigField
                label="Max steps"
                value={agent.maxSteps ? String(agent.maxSteps) : '-'}
              />
              <ConfigField
                label="Created"
                value={formatTimestamp(agent.createdAt)}
              />
              <ConfigField
                label="Updated"
                value={formatTimestamp(agent.updatedAt)}
              />
            </div>
          </SectionCard>

          <SectionCard
            title="Prompting"
            description="How the agent is primed before it starts reasoning."
            icon={MessageSquareText}
          >
            {agent.systemPrompt ? (
              <div className="rounded border border-brand-main-600 bg-brand-main-900/50 p-4">
                <div className="mb-3 flex items-center justify-between gap-3 text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                  <span className="inline-flex items-center gap-2">
                    <FileText className="h-3.5 w-3.5" />
                    System prompt
                  </span>
                  <span>{promptLineCount} lines</span>
                </div>
                <div className="max-h-72 overflow-y-auto whitespace-pre-wrap text-sm leading-6 text-brand-main-100">
                  {agent.systemPrompt}
                </div>
              </div>
            ) : (
              <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/30 px-4 py-8 text-center">
                <BrainCircuit className="mx-auto h-5 w-5 text-brand-secondary-400" />
                <div className="mt-3 text-sm font-medium text-white light:text-brand-main-50">
                  No system prompt configured
                </div>
                <div className="mt-1 text-xs text-white/50 light:text-black/50">
                  This agent currently relies on the model defaults and any
                  runtime context passed during execution.
                </div>
              </div>
            )}
          </SectionCard>
        </div>

        {isPersistent && (
          <SectionCard
            title="Provisioning"
            description="Persistent agents come online in three phases: record creation, sandbox boot, and networking setup."
            icon={Server}
          >
            <div className="grid gap-3 md:grid-cols-3">
              {provisionSteps.map((step, index) => {
                const Icon = step.icon
                return (
                  <div
                    key={step.key}
                    className={`rounded border p-3 ${stepCardClass(step.state)}`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex items-center gap-2 text-[11px] uppercase tracking-wide text-white/45 light:text-black/45">
                        <span>{index + 1}</span>
                        <Icon className="h-3.5 w-3.5" />
                      </div>
                      <StepIcon state={step.state} />
                    </div>
                    <div className="mt-3 text-sm font-medium text-white light:text-brand-main-50">
                      {step.title}
                    </div>
                    <div className="mt-1 text-xs leading-5 text-white/55 light:text-black/55">
                      {step.description}
                    </div>
                  </div>
                )
              })}
            </div>
          </SectionCard>
        )}

        <SectionCard
          title={`Runtime tools${runtimeTools.length ? ` (${runtimeTools.length})` : ''}`}
          description="Predicted effective tools for a normal agent run, inferred from this agent's current configuration."
          icon={Hammer}
        >
          {runtimeTools.length > 0 ? (
            <>
              <div className="mb-4 text-xs text-white/50 light:text-black/50">
                Environment-specific tools like web search, memory, storage,
                triggers, and messaging may appear during a live session if the
                backend enables them.
              </div>
              <div className="grid gap-3 md:grid-cols-2">
                {runtimeToolGroups.map((group) => (
                  <div
                    key={group.label}
                    className="rounded border border-brand-main-600 bg-brand-main-900/40 p-3"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="text-sm font-medium text-white light:text-brand-main-50">
                        {group.label}
                      </div>
                      <span className="rounded-full border border-brand-main-500/25 bg-brand-main-800/80 px-2 py-0.5 text-[10px] uppercase tracking-wide text-white/50 light:text-black/50">
                        {group.tools.length}
                      </span>
                    </div>
                    <div className="mt-1 text-[11px] leading-5 text-white/45 light:text-black/45">
                      {group.description}
                    </div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      {group.tools.map((tool) => (
                        <span
                          key={tool}
                          className="inline-flex items-center rounded border border-brand-secondary-500/25 bg-brand-secondary-600/10 px-2.5 py-1 text-xs text-brand-secondary-100"
                          title={tool}
                        >
                          {toolLabel(tool)}
                        </span>
                      ))}
                    </div>
                  </div>
                ))}
              </div>

              <div className="mt-4 rounded border border-brand-main-600 bg-brand-main-900/40 px-3 py-2.5">
                <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                  Explicitly configured
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  {configuredTools.length > 0 ? (
                    configuredTools.map((tool) => (
                      <span
                        key={tool}
                        className="inline-flex items-center rounded border border-brand-main-500/25 bg-brand-main-800/70 px-2.5 py-1 text-xs font-mono text-brand-main-100"
                      >
                        {tool}
                      </span>
                    ))
                  ) : (
                    <span className="text-xs text-white/50 light:text-black/50">
                      No explicit allowlist saved; runtime tools are being
                      inferred.
                    </span>
                  )}
                </div>
              </div>
            </>
          ) : (
            <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/30 px-4 py-8 text-center">
              <Clock3 className="mx-auto h-5 w-5 text-brand-secondary-400" />
              <div className="mt-3 text-sm font-medium text-white light:text-brand-main-50">
                No runtime tools detected
              </div>
              <div className="mt-1 text-xs text-white/50 light:text-black/50">
                This usually means the agent has no explicit tools and no
                runtime capabilities inferred from its current config.
              </div>
            </div>
          )}
        </SectionCard>
      </div>
    </div>
  )
}
