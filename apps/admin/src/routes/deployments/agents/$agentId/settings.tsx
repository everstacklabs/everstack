import { createFileRoute, Link } from '@tanstack/react-router'
import { z } from 'zod'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  AlertTriangle,
  Brain,
  CheckCircle2,
  Cpu,
  Link2,
  Loader2,
  Pencil,
  RotateCcw,
  ScrollText,
  ShieldCheck,
  SlidersHorizontal,
  Stethoscope,
  Trash2,
} from 'lucide-react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import {
  AgentLifecycleMode,
  AgentLifecycleStatus,
  AgentLinkType,
  TaskPermissionMode,
  type AgentDefinition,
  type AgentLink,
} from '@/server/agents'
import {
  useAgent,
  useAgentCapabilities,
  useAgentLinks,
  useAgents,
  useCreateAgentLink,
  useDeleteAgent,
  useDeleteAgentLink,
  useSessions,
  useUpdateAgent,
} from '@/hooks/deployments/use-agents'
import {
  useAgentMemories,
  useDeactivateAgentMemory,
} from '@/hooks/deployments/use-agent-memories'
import { useSandboxInstances } from '@/hooks/deployments/use-sandbox'
import {
  IdentityEditor,
  type IdentityKey,
} from '@/components/deployments/agents/identity-editor'
import { AgentFormDialog } from '@/components/deployments/agents'
import { LifecycleStatusBadge } from '@/components/deployments/agents/lifecycle-status-badge'
import { Loader } from '@everstack/ui/components'

const {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Badge,
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} = ui

const settingsSearchSchema = z.object({
  section: z
    .enum([
      'identity',
      'runtime',
      'memory',
      'environment',
      'collaboration',
      'governance',
      'diagnostics',
      'danger',
    ] as const)
    .optional(),
  file: z
    .enum(['soulMd', 'identityMd', 'userMd', 'roleMd'] as const)
    .optional(),
})

export const Route = createFileRoute('/deployments/agents/$agentId/settings')({
  component: SettingsRoute,
  validateSearch: settingsSearchSchema,
})

type SettingsSection = NonNullable<
  z.infer<typeof settingsSearchSchema>['section']
>

type JsonObject = { [key: string]: JsonValue }
type JsonValue = number | string | boolean | null | JsonObject | JsonValue[]
type AgentConfig = JsonObject

const SECTIONS: {
  value: SettingsSection
  label: string
  description: string
  icon: typeof ScrollText
}[] = [
  {
    value: 'identity',
    label: 'Identity',
    description: 'Long-form operating identity',
    icon: ScrollText,
  },
  {
    value: 'runtime',
    label: 'Runtime Policy',
    description: 'Autonomy, limits, and working posture',
    icon: SlidersHorizontal,
  },
  {
    value: 'memory',
    label: 'Memory Policy',
    description: 'Recall, extraction, and memory hygiene',
    icon: Brain,
  },
  {
    value: 'environment',
    label: 'Environment',
    description: 'Sandbox, repo, resources, and network',
    icon: Cpu,
  },
  {
    value: 'collaboration',
    label: 'Collaboration',
    description: 'Peer links and delegation posture',
    icon: Link2,
  },
  {
    value: 'governance',
    label: 'Governance',
    description: 'Approval posture and risk controls',
    icon: ShieldCheck,
  },
  {
    value: 'diagnostics',
    label: 'Diagnostics',
    description: 'Effective prompt and live capability checks',
    icon: Stethoscope,
  },
  {
    value: 'danger',
    label: 'Danger Zone',
    description: 'Disable, delete, or clear active memory',
    icon: AlertTriangle,
  },
]

const panelClass =
  'rounded border border-brand-main-600 bg-brand-main-900/50'
const inputClass = 'bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const selectTriggerClass =
  'bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const selectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandActionButtonClass =
  'h-8 gap-1.5 px-3 text-xs border-brand-main-600 text-zinc-400 light:text-zinc-600 hover:text-white light:hover:text-brand-main-50'
const brandPrimaryButtonClass = 'h-8 gap-1.5 px-3 text-xs'

function SettingsRoute() {
  const { agentId } = Route.useParams()
  const { section = 'identity', file } = Route.useSearch()
  const navigate = Route.useNavigate()
  const { data: agent } = useAgent(agentId)
  const [editOpen, setEditOpen] = useState(false)

  if (!agent) {
    return (
      <div className="flex-1 flex items-center justify-center h-full">
        <Loader loaderText="Loading settings..." />
      </div>
    )
  }

  const activeSection = section as SettingsSection

  return (
    <div className="h-full min-h-0 overflow-hidden px-3 pb-3">
      <div className="grid h-full min-h-0 grid-cols-1 gap-3 lg:grid-cols-[236px_minmax(0,1fr)]">
        <aside className="min-h-0 max-h-64 overflow-y-auto rounded border border-brand-main-600 bg-brand-main-900/50 p-2 scrollbar-macos lg:max-h-none">
          <div className="px-2 pb-2 pt-1">
            <div className="flex items-center justify-between gap-2">
              <div>
                <h2 className="text-sm font-semibold text-white light:text-brand-main-50">
                  Operational Settings
                </h2>
                <p className="mt-1 text-[11px] leading-relaxed text-white/50 light:text-black/50">
                  Tune the live agent without duplicating basic setup.
                </p>
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setEditOpen(true)}
              className={`${brandActionButtonClass} mt-3 w-full justify-center bg-brand-main-900/60 text-brand-main-100 hover:bg-brand-main-800/80`}
            >
              <Pencil className="size-3.5" />
              Edit basic details
            </Button>
          </div>
          <nav className="space-y-1">
            {SECTIONS.map((item) => {
              const Icon = item.icon
              const isActive = item.value === activeSection
              return (
                <button
                  key={item.value}
                  type="button"
                  onClick={() =>
                    navigate({
                      search: {
                        section: item.value,
                        ...(item.value === 'identity'
                          ? { file: file ?? 'soulMd' }
                          : {}),
                      },
                    })
                  }
                  className={`w-full rounded px-2.5 py-2 text-left transition-colors ${
                    isActive
                      ? 'border border-brand-secondary-500/35 bg-brand-secondary-600/15 text-brand-secondary-200'
                      : 'border border-transparent text-brand-main-200 hover:border-brand-main-600 hover:bg-brand-main-800/60 hover:text-white light:hover:text-brand-main-50'
                  } light:hover:text-brand-main-50`}
                >
                  <span className="flex items-center gap-2 text-xs font-medium">
                    <Icon className="size-3.5 shrink-0" />
                    {item.label}
                  </span>
                  <span className="mt-1 block pl-5 text-[10px] leading-snug text-white/40 light:text-black/40">
                    {item.description}
                  </span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="min-h-0 overflow-hidden">
          {activeSection === 'identity' && (
            <IdentityPanel
              agent={agent}
              activeFile={(file ?? 'soulMd') as IdentityKey}
              onFileChange={(nextFile) =>
                navigate({ search: { section: 'identity', file: nextFile } })
              }
            />
          )}
          {activeSection === 'runtime' && <RuntimePolicyPanel agent={agent} />}
          {activeSection === 'memory' && <MemoryPolicyPanel agent={agent} />}
          {activeSection === 'environment' && (
            <EnvironmentPanel agent={agent} onEdit={() => setEditOpen(true)} />
          )}
          {activeSection === 'collaboration' && (
            <CollaborationPanel agent={agent} />
          )}
          {activeSection === 'governance' && <GovernancePanel agent={agent} />}
          {activeSection === 'diagnostics' && (
            <DiagnosticsPanel agent={agent} />
          )}
          {activeSection === 'danger' && <DangerZonePanel agent={agent} />}
        </main>
      </div>

      <AgentFormDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        agent={agent}
      />
    </div>
  )
}

function IdentityPanel({
  agent,
  activeFile,
  onFileChange,
}: {
  agent: AgentDefinition
  activeFile: IdentityKey
  onFileChange: (file: IdentityKey) => void
}) {
  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <PanelHeader
        title="Identity"
        description="A full-page workspace for the agent's long-form self, role, and user context."
      />
      <div className="min-h-0 flex-1">
        <IdentityEditor
          agentId={agent.id}
          identity={agent.identity}
          activeFile={activeFile}
          onFileChange={onFileChange}
        />
      </div>
    </div>
  )
}

function RuntimePolicyPanel({ agent }: { agent: AgentDefinition }) {
  const updateMutation = useUpdateAgent()
  const config = useMemo(() => cloneConfig(agent.config), [agent.config])
  const [permissionMode, setPermissionMode] = useState(
    taskPermissionEnumToValue(agent.taskPermissionMode),
  )
  const [maxSteps, setMaxSteps] = useState(
    agent.maxSteps != null && agent.maxSteps > 0 ? String(agent.maxSteps) : '',
  )
  const [maxTurns, setMaxTurns] = useState(
    agent.maxTurns > 0 ? String(agent.maxTurns) : '',
  )
  const [maxToolCalls, setMaxToolCalls] = useState(
    agent.maxToolCallsPerTurn > 0 ? String(agent.maxToolCallsPerTurn) : '',
  )
  const [workingDirectory, setWorkingDirectory] = useState(
    agent.workingDirectory ?? '',
  )
  const [disabledRuntimeTools, setDisabledRuntimeTools] = useState(
    Array.isArray(config.disabled_runtime_tools)
      ? (config.disabled_runtime_tools as unknown[]).filter(isString).join(', ')
      : '',
  )

  const handleSave = async () => {
    const nextConfig = cloneConfig(agent.config)
    const disabledTools = disabledRuntimeTools
      .split(',')
      .map((tool) => tool.trim())
      .filter(Boolean)
    if (disabledTools.length > 0) {
      nextConfig.disabled_runtime_tools = disabledTools
    } else {
      delete nextConfig.disabled_runtime_tools
    }

    try {
      await updateMutation.mutateAsync({
        id: agent.id,
        tools: agent.tools ?? [],
        config: nextConfig,
        taskPermissionMode: taskPermissionValueToEnum(permissionMode),
        maxSteps: parseOptionalInt(maxSteps),
        maxTurns: parseOptionalInt(maxTurns),
        maxToolCallsPerTurn: parseOptionalInt(maxToolCalls),
        workingDirectory: workingDirectory.trim() || undefined,
      })
      toast.success('Runtime policy updated')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to update runtime policy: ${error.message}`
          : 'Failed to update runtime policy',
      )
    }
  }

  return (
    <SettingsScroll>
      <PanelHeader
        title="Runtime Policy"
        description="Set how much autonomy this agent has when it is actively working."
      />
      <div className="grid gap-3 lg:grid-cols-2">
        <SettingsCard
          title="Autonomy"
          description="Approval posture for task execution and tool usage."
        >
          <div className="space-y-2">
            <Label className="text-xs text-white/60 light:text-black/60">
              Task permission mode
            </Label>
            <Select value={permissionMode} onValueChange={setPermissionMode}>
              <SelectTrigger className={selectTriggerClass}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent className={selectContentClass}>
                <SelectItem value="ask">Ask before risky task work</SelectItem>
                <SelectItem value="always">
                  Always allow configured tasks
                </SelectItem>
                <SelectItem value="deny">Deny task execution</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <FieldGrid>
            <NumberField
              label="Max steps"
              value={maxSteps}
              onChange={setMaxSteps}
              placeholder="Default"
            />
            <NumberField
              label="Max turns"
              value={maxTurns}
              onChange={setMaxTurns}
              placeholder="Default"
            />
            <NumberField
              label="Tool calls / turn"
              value={maxToolCalls}
              onChange={setMaxToolCalls}
              placeholder="Default"
            />
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Working directory</Label>
              <Input
                value={workingDirectory}
                onChange={(event) => setWorkingDirectory(event.target.value)}
                placeholder="/workspace"
                className={inputClass}
              />
            </div>
          </FieldGrid>
        </SettingsCard>

        <SettingsCard
          title="Runtime tool overrides"
          description="Disable platform-injected runtime tools without changing the agent's basic tool list."
        >
          <div className="space-y-2">
            <Label className="text-xs text-white/60 light:text-black/60">
              Disabled runtime tools
            </Label>
            <Input
              value={disabledRuntimeTools}
              onChange={(event) => setDisabledRuntimeTools(event.target.value)}
              placeholder="shell, browser, memory_store"
              className={inputClass}
            />
            <p className="text-[11px] leading-relaxed text-white/40 light:text-black/40">
              Comma-separated tool names. The basic enabled tools still live in
              the Edit panel.
            </p>
          </div>
          <SummaryList
            rows={[
              ['Enabled tools', String(agent.tools?.length ?? 0)],
              ['Model', agent.model || 'Not configured'],
              ['Lifecycle', lifecycleModeLabel(agent.lifecycleMode)],
            ]}
          />
        </SettingsCard>
      </div>
      <SaveBar
        label="Save runtime policy"
        isSaving={updateMutation.isPending}
        onSave={handleSave}
      />
    </SettingsScroll>
  )
}

function MemoryPolicyPanel({ agent }: { agent: AgentDefinition }) {
  const updateMutation = useUpdateAgent()
  const deactivateMemory = useDeactivateAgentMemory()
  const { data: memories = [] } = useAgentMemories(agent.id, {
    activeOnly: true,
    limit: 50,
  })
  const memoryConfig = agent.memoryConfig
  const [enabled, setEnabled] = useState(memoryConfig?.enabled ?? false)
  const [scope, setScope] = useState(memoryConfig?.scope || 'agent')
  const [autoRetrieve, setAutoRetrieve] = useState(
    memoryConfig?.autoRetrieve ?? true,
  )
  const [autoExtract, setAutoExtract] = useState(
    memoryConfig?.autoExtract ?? false,
  )
  const [topK, setTopK] = useState(
    memoryConfig?.autoRetrieveTopK
      ? String(memoryConfig.autoRetrieveTopK)
      : '10',
  )

  const memoryCounts = useMemo(() => {
    return memories.reduce<Record<string, number>>((acc, memory) => {
      acc[memory.memoryType] = (acc[memory.memoryType] ?? 0) + 1
      return acc
    }, {})
  }, [memories])

  const handleSave = async () => {
    try {
      await updateMutation.mutateAsync({
        id: agent.id,
        tools: agent.tools ?? [],
        memoryConfig: {
          enabled,
          scope,
          autoRetrieve,
          autoRetrieveTopK: parseOptionalInt(topK) ?? 10,
          autoExtract,
        },
      })
      toast.success('Memory policy updated')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to update memory policy: ${error.message}`
          : 'Failed to update memory policy',
      )
    }
  }

  const handleDeactivateMemories = async () => {
    if (memories.length === 0) return
    const confirmed = window.confirm(
      `Deactivate ${memories.length} active memory entries for this agent?`,
    )
    if (!confirmed) return
    try {
      await Promise.all(
        memories.map((memory) =>
          deactivateMemory.mutateAsync({ memoryId: memory.id }),
        ),
      )
      toast.success('Active memories deactivated')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to deactivate memories: ${error.message}`
          : 'Failed to deactivate memories',
      )
    }
  }

  return (
    <SettingsScroll>
      <PanelHeader
        title="Memory Policy"
        description="Control what the agent recalls automatically and how memory is curated over time."
      />
      <div className="grid gap-3 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,0.9fr)]">
        <SettingsCard
          title="Automatic memory"
          description="These controls use the existing persistent memory configuration."
        >
          <ToggleRow
            title="Enable persistent memory"
            description="Allow this agent to use long-term memory entries."
            checked={enabled}
            onCheckedChange={setEnabled}
          />
          <ToggleRow
            title="Auto-retrieve context"
            description="Inject relevant memories into the prompt before a turn."
            checked={autoRetrieve}
            onCheckedChange={setAutoRetrieve}
          />
          <ToggleRow
            title="Auto-extract memories"
            description="Let completed sessions write useful facts and instructions."
            checked={autoExtract}
            onCheckedChange={setAutoExtract}
          />
          <FieldGrid>
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Scope</Label>
              <Select value={scope} onValueChange={setScope}>
                <SelectTrigger className={selectTriggerClass}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                  <SelectItem value="agent">Agent</SelectItem>
                  <SelectItem value="user">User</SelectItem>
                  <SelectItem value="global">Global</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <NumberField
              label="Auto-retrieve top K"
              value={topK}
              onChange={setTopK}
              placeholder="10"
            />
          </FieldGrid>
        </SettingsCard>

        <SettingsCard
          title="Memory hygiene"
          description="Inspect and prune memory without hiding the dedicated Memory tab."
        >
          <SummaryList
            rows={[
              ['Active entries', String(memories.length)],
              ['Facts', String(memoryCounts.fact ?? 0)],
              ['Instructions', String(memoryCounts.instruction ?? 0)],
              ['Summaries', String(memoryCounts.session_summary ?? 0)],
              ['Documents', String(memoryCounts.document ?? 0)],
            ]}
          />
          <div className="flex flex-wrap gap-2 pt-2">
            <Button asChild variant="outline" size="sm">
              <Link
                to="/deployments/agents/$agentId/memory"
                params={{ agentId: agent.id }}
                className={brandActionButtonClass}
              >
                Inspect memory entries
              </Link>
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={memories.length === 0 || deactivateMemory.isPending}
              onClick={handleDeactivateMemories}
              className={`${brandActionButtonClass} border-red-500/25 text-red-300 light:text-red-600 hover:bg-red-500/10`}
            >
              {deactivateMemory.isPending ? 'Clearing...' : 'Deactivate active'}
            </Button>
          </div>
        </SettingsCard>
      </div>
      <SaveBar
        label="Save memory policy"
        isSaving={updateMutation.isPending}
        onSave={handleSave}
      />
    </SettingsScroll>
  )
}

type SandboxMode = 'new' | 'existing'

function EnvironmentPanel({
  agent,
  onEdit,
}: {
  agent: AgentDefinition
  onEdit: () => void
}) {
  const sandbox = agent.sandboxConfig
  const envVarCount = Object.keys(sandbox?.envVars ?? {}).length
  const updateMutation = useUpdateAgent()
  const runningSandboxesOpts = useMemo(
    () => ({ status: 'running' as const, limit: 100 }),
    [],
  )
  const { data: runningSandboxes } = useSandboxInstances(runningSandboxesOpts)

  // Seed staged state from the agent's current sandbox config. Edits stay
  // local until the user confirms via the Apply & restart flow.
  const initialMode: SandboxMode = sandbox?.linkedSessionId
    ? 'existing'
    : 'new'
  const [mode, setMode] = useState<SandboxMode>(initialMode)
  const [linkedSessionId, setLinkedSessionId] = useState(
    sandbox?.linkedSessionId ?? '',
  )
  const [image, setImage] = useState(sandbox?.image ?? '')
  const [cpuLimit, setCpuLimit] = useState(
    sandbox?.cpuLimit ? String(sandbox.cpuLimit) : '',
  )
  const [memoryMb, setMemoryMb] = useState(
    sandbox?.memoryMb ? sandbox.memoryMb.toString() : '',
  )
  const [diskMb, setDiskMb] = useState(
    sandbox?.diskMb ? sandbox.diskMb.toString() : '',
  )
  const [timeoutSeconds, setTimeoutSeconds] = useState(
    sandbox?.timeoutSeconds ? String(sandbox.timeoutSeconds) : '',
  )
  const [networkMode, setNetworkMode] = useState(
    sandbox?.networkMode || 'whitelist',
  )
  const [allowedHosts, setAllowedHosts] = useState(
    (sandbox?.allowedHosts ?? []).join('\n'),
  )
  const [gitRepoUrl, setGitRepoUrl] = useState(sandbox?.gitRepoUrl ?? '')
  const [gitBranch, setGitBranch] = useState(sandbox?.gitBranch ?? '')
  const [sshEnabled, setSshEnabled] = useState(sandbox?.sshEnabled ?? false)
  const [confirmOpen, setConfirmOpen] = useState(false)

  // Re-seed when the underlying agent record changes (e.g. read-model
  // refetch after a successful save). Without this the staged fields keep
  // their pre-save snapshot and look "dirty" forever.
  useEffect(() => {
    setMode(sandbox?.linkedSessionId ? 'existing' : 'new')
    setLinkedSessionId(sandbox?.linkedSessionId ?? '')
    setImage(sandbox?.image ?? '')
    setCpuLimit(sandbox?.cpuLimit ? String(sandbox.cpuLimit) : '')
    setMemoryMb(sandbox?.memoryMb ? sandbox.memoryMb.toString() : '')
    setDiskMb(sandbox?.diskMb ? sandbox.diskMb.toString() : '')
    setTimeoutSeconds(
      sandbox?.timeoutSeconds ? String(sandbox.timeoutSeconds) : '',
    )
    setNetworkMode(sandbox?.networkMode || 'whitelist')
    setAllowedHosts((sandbox?.allowedHosts ?? []).join('\n'))
    setGitRepoUrl(sandbox?.gitRepoUrl ?? '')
    setGitBranch(sandbox?.gitBranch ?? '')
    setSshEnabled(sandbox?.sshEnabled ?? false)
  }, [
    sandbox?.linkedSessionId,
    sandbox?.image,
    sandbox?.cpuLimit,
    sandbox?.memoryMb,
    sandbox?.diskMb,
    sandbox?.timeoutSeconds,
    sandbox?.networkMode,
    sandbox?.allowedHosts,
    sandbox?.gitRepoUrl,
    sandbox?.gitBranch,
    sandbox?.sshEnabled,
  ])

  const parsedAllowedHosts = useMemo(
    () =>
      allowedHosts
        .split(/[\n,]/)
        .map((h) => h.trim())
        .filter(Boolean),
    [allowedHosts],
  )

  const dirty = useMemo(() => {
    if (mode !== initialMode) return true
    if (mode === 'existing') {
      return linkedSessionId !== (sandbox?.linkedSessionId ?? '')
    }
    return (
      image !== (sandbox?.image ?? '') ||
      cpuLimit !== (sandbox?.cpuLimit ? String(sandbox.cpuLimit) : '') ||
      memoryMb !== (sandbox?.memoryMb ? sandbox.memoryMb.toString() : '') ||
      diskMb !== (sandbox?.diskMb ? sandbox.diskMb.toString() : '') ||
      timeoutSeconds !==
        (sandbox?.timeoutSeconds ? String(sandbox.timeoutSeconds) : '') ||
      networkMode !== (sandbox?.networkMode || 'whitelist') ||
      parsedAllowedHosts.join(',') !==
        (sandbox?.allowedHosts ?? []).join(',') ||
      gitRepoUrl !== (sandbox?.gitRepoUrl ?? '') ||
      gitBranch !== (sandbox?.gitBranch ?? '') ||
      sshEnabled !== (sandbox?.sshEnabled ?? false)
    )
  }, [
    mode,
    initialMode,
    linkedSessionId,
    image,
    cpuLimit,
    memoryMb,
    diskMb,
    timeoutSeconds,
    networkMode,
    parsedAllowedHosts,
    gitRepoUrl,
    gitBranch,
    sshEnabled,
    sandbox,
  ])

  const handleConfirm = async () => {
    setConfirmOpen(false)
    const params: Parameters<typeof updateMutation.mutateAsync>[0] =
      mode === 'existing' && linkedSessionId
        ? { id: agent.id, sandboxConfig: { linkedSessionId } }
        : {
            id: agent.id,
            sandboxConfig: {
              image: image.trim() || undefined,
              cpuLimit: parseNumber(cpuLimit),
              memoryMb: parseNumber(memoryMb),
              diskMb: parseNumber(diskMb),
              timeoutSeconds: parseNumber(timeoutSeconds),
              networkMode,
              allowedHosts:
                networkMode === 'whitelist' && parsedAllowedHosts.length > 0
                  ? parsedAllowedHosts
                  : [],
              gitRepoUrl: gitRepoUrl.trim() || undefined,
              gitBranch: gitBranch.trim() || undefined,
              sshEnabled,
            },
          }

    try {
      await updateMutation.mutateAsync(params)
      toast.success(
        'Sandbox config saved — the runtime will restart the sandbox if infra fields changed.',
      )
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to update sandbox config: ${error.message}`
          : 'Failed to update sandbox config',
      )
    }
  }

  return (
    <SettingsScroll>
      <PanelHeader
        title="Environment"
        description="Edit the sandbox shape, source binding, and egress posture inline. Changes are staged until you click Apply & restart."
      />
      <div className="grid gap-3 lg:grid-cols-2">
        <SettingsCard
          title="Sandbox status"
          description="Live persistent runtime"
        >
          <div className="flex items-center gap-2">
            <Badge
              variant="outline"
              className="border-brand-main-600 bg-brand-main-800/70 text-brand-main-100"
            >
              {agent.sandboxId ? agent.sandboxId : 'No sandbox id'}
            </Badge>
            {agent.lifecycleStatus != null && (
              <LifecycleStatusBadge status={agent.lifecycleStatus} />
            )}
          </div>
          <SummaryList
            rows={[
              ['Lifecycle', lifecycleStatusLabel(agent.lifecycleStatus)],
              ['Mode', lifecycleModeLabel(agent.lifecycleMode)],
              ['Primary session', agent.primarySessionId || 'Not assigned'],
              ['Env vars', `${envVarCount} configured`],
            ]}
          />
        </SettingsCard>

        <SettingsCard
          title="Sandbox source"
          description="Provision a new sandbox or share an existing one"
        >
          <div className="flex gap-1.5">
            <button
              type="button"
              onClick={() => {
                setMode('new')
                setLinkedSessionId('')
              }}
              className={modeButtonClass(mode === 'new')}
            >
              New sandbox
            </button>
            <button
              type="button"
              onClick={() => setMode('existing')}
              className={modeButtonClass(mode === 'existing')}
            >
              Use existing
            </button>
          </div>
          {mode === 'existing' ? (
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Running sandbox</Label>
              <Select
                value={linkedSessionId}
                onValueChange={setLinkedSessionId}
              >
                <SelectTrigger className={selectTriggerClass}>
                  <SelectValue placeholder="Select a running sandbox…" />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                  {(runningSandboxes?.instances ?? []).map((inst) => (
                    <SelectItem key={inst.sessionId} value={inst.sessionId}>
                      {inst.name || inst.sessionId} — {inst.image}
                    </SelectItem>
                  ))}
                  {(!runningSandboxes?.instances ||
                    runningSandboxes.instances.length === 0) && (
                    <SelectItem value="__none__" disabled>
                      No running sandboxes
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
              <p className="text-[11px] leading-relaxed text-white/40 light:text-black/40">
                The agent will share this sandbox instead of creating its own.
                Resource fields are ignored while linked.
              </p>
            </div>
          ) : (
            <p className="text-[11px] leading-relaxed text-white/40 light:text-black/40">
              The agent gets a dedicated sandbox with the resource and network
              shape you configure below.
            </p>
          )}
        </SettingsCard>

        {mode ==='new' && (
          <SettingsCard
            title="Resources"
            description="Configured sandbox envelope"
          >
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Image</Label>
              <Input
                value={image}
                onChange={(event) => setImage(event.target.value)}
                placeholder="ghcr.io/everstacklabs/sandbox:base"
                className={inputClass}
              />
            </div>
            <FieldGrid>
              <NumberField
                label="CPU (vCPU)"
                value={cpuLimit}
                onChange={setCpuLimit}
                placeholder="1"
              />
              <NumberField
                label="Memory (MB)"
                value={memoryMb}
                onChange={setMemoryMb}
                placeholder="512"
              />
              <NumberField
                label="Disk (MB)"
                value={diskMb}
                onChange={setDiskMb}
                placeholder="1024"
              />
              <NumberField
                label="Timeout (sec)"
                value={timeoutSeconds}
                onChange={setTimeoutSeconds}
                placeholder="300"
              />
            </FieldGrid>
          </SettingsCard>
        )}

        {mode ==='new' && (
          <SettingsCard
            title="Source control"
            description="Optional repository pre-mounted at /repo"
          >
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Repo URL</Label>
              <Input
                value={gitRepoUrl}
                onChange={(event) => setGitRepoUrl(event.target.value)}
                placeholder="https://github.com/owner/repo.git"
                className={inputClass}
              />
            </div>
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Branch</Label>
              <Input
                value={gitBranch}
                onChange={(event) => setGitBranch(event.target.value)}
                placeholder="main"
                className={inputClass}
              />
            </div>
            <ToggleRow
              title="SSH access"
              description="Allow operators to SSH into the sandbox for diagnostics."
              checked={sshEnabled}
              onCheckedChange={setSshEnabled}
            />
          </SettingsCard>
        )}

        {mode ==='new' && (
          <SettingsCard
            title="Network"
            description="Egress mode and allowed hosts"
          >
            <div className="space-y-2">
              <Label className="text-xs text-white/60 light:text-black/60">Network mode</Label>
              <Select value={networkMode} onValueChange={setNetworkMode}>
                <SelectTrigger className={selectTriggerClass}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className={selectContentClass}>
                  <SelectItem value="allow">
                    Allow — unrestricted egress
                  </SelectItem>
                  <SelectItem value="whitelist">
                    Whitelist — gated via DNS allowlist
                  </SelectItem>
                  <SelectItem value="deny">Deny — no outbound</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {networkMode ==='whitelist' && (
              <div className="space-y-2">
                <Label className="text-xs text-white/60 light:text-black/60">Allowed hosts</Label>
                <Textarea
                  value={allowedHosts}
                  onChange={(event) => setAllowedHosts(event.target.value)}
                  placeholder={'registry.npmjs.org\n*.pypi.org\ngithub.com'
                  }
                  rows={5}
                  className={`${inputClass} font-mono text-xs`}
                />
                <p className="text-[11px] leading-relaxed text-white/40 light:text-black/40">
                  One host per line. Wildcards like <code>*.example.com</code>{' '}
                  are matched at the DNS proxy.
                </p>
              </div>
            )}
          </SettingsCard>
        )}
      </div>

      <div
        className={`${panelClass} mt-3 flex flex-wrap items-center justify-between gap-3 p-3`}
      >
        <div className="flex items-start gap-2">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-400/80 light:text-amber-700/80" />
          <div>
            <p className="text-sm font-medium text-white light:text-brand-main-50">
              Changes restart the sandbox
            </p>
            <p className="mt-1 text-xs text-white/50 light:text-black/50">
              Applying a new resource shape, network mode, repo binding, or
              source toggle recreates the sandbox. In-flight session work may
              be interrupted. Environment variables are still managed in the
              full Edit panel.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onEdit}
            className={brandActionButtonClass}
          >
            <Pencil className="size-3.5" />
            Env vars & advanced
          </Button>
          <Button
            type="button"
            size="sm"
            disabled={
              !dirty ||
              updateMutation.isPending ||
              (mode ==='existing' && !linkedSessionId)
            }
            onClick={() => setConfirmOpen(true)}
            className={brandPrimaryButtonClass}
          >
            {updateMutation.isPending && (
              <Loader2 className="size-3.5 animate-spin" />
            )}
            Apply & restart
          </Button>
        </div>
      </div>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent className="bg-brand-main-900 border border-brand-main-700 text-brand-main-100 p-0 gap-0 sm:max-w-md shadow-2xl shadow-black/60">
          <AlertDialogHeader className="p-5 pb-4 sm:text-left">
            <div className="flex items-start gap-3">
              <div className="shrink-0 size-9 rounded-md bg-amber-500/10 border border-amber-500/25 flex items-center justify-center">
                <AlertTriangle size={18} className="text-amber-400 light:text-amber-700" />
              </div>
              <div className="flex-1 min-w-0">
                <AlertDialogTitle className="text-white light:text-brand-main-50 text-[15px] font-semibold leading-tight">
                  Restart the sandbox?
                </AlertDialogTitle>
                <AlertDialogDescription className="mt-1.5 text-sm text-white/55 light:text-black/55 leading-relaxed">
                  Saving these changes recreates the agent's sandbox with the
                  new shape. Any in-flight session work running inside the
                  sandbox will be lost. Linked sandboxes are re-attached
                  without restart.
                </AlertDialogDescription>
              </div>
            </div>
          </AlertDialogHeader>
          <AlertDialogFooter className="px-5 py-3 border-t border-brand-main-700 bg-brand-main-950/40 sm:justify-end gap-2">
            <AlertDialogCancel
              disabled={updateMutation.isPending}
              className="mt-0 h-8 px-3.5 text-sm bg-transparent border border-brand-main-600 text-white/75 light:text-black/75 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50"
            >
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={updateMutation.isPending}
              onClick={(event) => {
                event.preventDefault()
                handleConfirm()
              }}
              className="h-8 px-3.5 text-sm bg-amber-500/90 hover:bg-amber-500 text-white border-0 inline-flex items-center gap-1.5 shadow-sm shadow-amber-900/30"
            >
              <RotateCcw size={14} />
              {updateMutation.isPending ? 'Applying…' : 'Apply & restart'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsScroll>
  )
}

function modeButtonClass(active: boolean) {
  return `inline-flex items-center gap-1.5 rounded-md border px-3 py-1 text-xs transition-colors ${
    active
      ? 'border-brand-secondary-500 bg-brand-secondary-500/15 text-brand-secondary-400'
      : 'border-brand-main-700 bg-brand-main-900/60 text-zinc-400 light:text-zinc-600 hover:border-brand-main-500 hover:text-zinc-200 light:hover:text-zinc-800'
  }`
}

function parseNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (trimmed === '') return undefined
  const n = Number(trimmed)
  return Number.isFinite(n) && n > 0 ? n : undefined
}

function CollaborationPanel({ agent }: { agent: AgentDefinition }) {
  const { data: links = [] } = useAgentLinks(agent.id)
  const { data: agents = [] } = useAgents({
    lifecycleMode: 'persistent',
    includeHidden: true,
  })
  const createLink = useCreateAgentLink()
  const deleteLink = useDeleteAgentLink()
  const [targetId, setTargetId] = useState('')
  const candidates = agents.filter((candidate) => candidate.id !== agent.id)

  const handleCreate = async () => {
    const target = candidates.find((candidate) => candidate.id === targetId)
    if (!target) return
    try {
      await createLink.mutateAsync({
        sourceAgentId: agent.id,
        targetId: target.id,
        targetName: target.name,
        targetType: 'agent',
        linkType: AgentLinkType.PEER,
      })
      setTargetId('')
      toast.success('Agent link created')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to create link: ${error.message}`
          : 'Failed to create link',
      )
    }
  }

  return (
    <SettingsScroll>
      <PanelHeader
        title="Collaboration"
        description="Manage the live agent graph for messaging, delegation, and shared work."
      />
      <div className="grid gap-3 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <SettingsCard
          title="Add peer link"
          description="Connect this agent to another persistent agent."
        >
          <div className="space-y-2">
            <Label className="text-xs text-white/60 light:text-black/60">Target agent</Label>
            <Select value={targetId} onValueChange={setTargetId}>
              <SelectTrigger className={selectTriggerClass}>
                <SelectValue placeholder="Select an agent" />
              </SelectTrigger>
              <SelectContent className={selectContentClass}>
                {candidates.length === 0 ? (
                  <SelectItem value="_none" disabled>
                    No other persistent agents
                  </SelectItem>
                ) : (
                  candidates.map((candidate) => (
                    <SelectItem key={candidate.id} value={candidate.id}>
                      {candidate.name}
                    </SelectItem>
                  ))
                )}
              </SelectContent>
            </Select>
          </div>
          <Button
            type="button"
            size="sm"
            disabled={!targetId || createLink.isPending}
            onClick={handleCreate}
            className={brandPrimaryButtonClass}
          >
            {createLink.isPending ? 'Linking...' : 'Create peer link'}
          </Button>
        </SettingsCard>

        <SettingsCard
          title="Linked agents"
          description="Existing connections available to the runtime."
        >
          {links.length === 0 ? (
            <EmptyState label="No linked agents yet." />
          ) : (
            <div className="space-y-2">
              {links.map((link) => (
                <LinkedAgentRow
                  key={link.id}
                  link={link}
                  onDelete={() => deleteLink.mutate(link.id)}
                  isDeleting={deleteLink.isPending}
                />
              ))}
            </div>
          )}
        </SettingsCard>
      </div>
    </SettingsScroll>
  )
}

function GovernancePanel({ agent }: { agent: AgentDefinition }) {
  const disabledRuntimeTools = Array.isArray(
    cloneConfig(agent.config).disabled_runtime_tools,
  )
    ? (cloneConfig(agent.config).disabled_runtime_tools as unknown[]).filter(
        isString,
      )
    : []
  const hasSandboxTools =
    agent.tools?.some((tool) =>
      ['shell', 'sandbox', 'browser', 'file_write', 'apply_patch'].some(
        (risk) => tool.includes(risk),
      ),
    ) ?? false

  return (
    <SettingsScroll>
      <PanelHeader
        title="Governance"
        description="A readable control posture for approvals, risky capabilities, and audit-facing defaults."
      />
      <div className="grid gap-3 lg:grid-cols-3">
        <PostureCard
          title="Approval posture"
          value={permissionLabel(agent.taskPermissionMode)}
          tone={
            agent.taskPermissionMode === TaskPermissionMode.ALWAYS
              ? 'warn'
              : agent.taskPermissionMode === TaskPermissionMode.DENY
                ? 'muted'
                : 'good'
          }
          description="Configured through Runtime Policy."
        />
        <PostureCard
          title="Tool risk"
          value={hasSandboxTools ? 'High-capability tools' : 'Limited tools'}
          tone={hasSandboxTools ? 'warn' : 'good'}
          description={`${agent.tools?.length ?? 0} basic tools enabled.`}
        />
        <PostureCard
          title="Runtime overrides"
          value={
            disabledRuntimeTools.length
              ? `${disabledRuntimeTools.length} disabled`
              : 'No overrides'
          }
          tone={disabledRuntimeTools.length ? 'good' : 'muted'}
          description="Platform-injected tools can be restricted separately."
        />
      </div>
      <SettingsCard
        title="Policy notes"
        description="Settings summarizes current posture; advanced policy engines can be layered in later."
        className="mt-3"
      >
        <div className="grid gap-2 md:grid-cols-2">
          <CheckItem label="Basic identity edits stay in the Edit panel or Identity workspace." />
          <CheckItem label="Approval posture is saved through the existing execution policy fields." />
          <CheckItem label="Dangerous destructive actions require confirmation in this UI." />
          <CheckItem label="Audit and trace inspection links remain in dedicated observability surfaces." />
        </div>
      </SettingsCard>
    </SettingsScroll>
  )
}

function DiagnosticsPanel({ agent }: { agent: AgentDefinition }) {
  const { data: capabilities } = useAgentCapabilities()
  const { data: sessions = [] } = useSessions({ agentId: agent.id, limit: 5 })
  const config = cloneConfig(agent.config)
  const promptPreview = buildEffectivePromptPreview(agent)

  return (
    <SettingsScroll>
      <PanelHeader
        title="Diagnostics"
        description="Inspect what the runtime will see without turning Settings into a basic edit form."
      />
      <div className="grid gap-3 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
        <SettingsCard
          title="Effective prompt preview"
          description="System prompt plus persistent identity files."
        >
          <pre className="max-h-[360px] overflow-auto whitespace-pre-wrap rounded border border-brand-main-600 bg-brand-main-950/70 p-3 text-xs leading-relaxed text-brand-main-100 scrollbar-macos">
            {promptPreview || 'No prompt or identity content configured.'}
          </pre>
        </SettingsCard>
        <div className="space-y-3">
          <SettingsCard title="Capability summary">
            <SummaryList
              rows={[
                ['Model', agent.model || 'Not configured'],
                ['Tools', String(agent.tools?.length ?? 0)],
                [
                  'Web search available',
                  capabilities?.web_search_available ? 'Yes' : 'No',
                ],
                ['Config keys', String(Object.keys(config).length)],
              ]}
            />
          </SettingsCard>
          <SettingsCard title="Recent sessions">
            {sessions.length === 0 ? (
              <EmptyState label="No recent sessions found." />
            ) : (
              <div className="space-y-2">
                {sessions.slice(0, 5).map((session) => (
                  <div
                    key={session.id}
                    className="rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-mono text-[11px] text-white/70 light:text-black/70">
                        {session.id}
                      </span>
                      <Badge
                        variant="outline"
                        className="border-brand-main-600 text-[10px] text-white/55 light:text-black/55"
                      >
                        {sessionStatusLabel(session.status)}
                      </Badge>
                    </div>
                    <p className="mt-1 text-[11px] text-white/40 light:text-black/40">
                      {session.turnCount} turns
                    </p>
                  </div>
                ))}
              </div>
            )}
          </SettingsCard>
        </div>
      </div>
    </SettingsScroll>
  )
}

function DangerZonePanel({ agent }: { agent: AgentDefinition }) {
  const navigate = Route.useNavigate()
  const updateMutation = useUpdateAgent()
  const deleteMutation = useDeleteAgent()
  const { data: memories = [] } = useAgentMemories(agent.id, {
    activeOnly: true,
    limit: 50,
  })
  const deactivateMemory = useDeactivateAgentMemory()

  const handleToggleEnabled = async () => {
    const nextEnabled = !agent.enabled
    const confirmed = window.confirm(
      `${nextEnabled ? 'Enable' : 'Disable'} ${agent.name}?`,
    )
    if (!confirmed) return
    try {
      await updateMutation.mutateAsync({
        id: agent.id,
        tools: agent.tools ?? [],
        enabled: nextEnabled,
      })
      toast.success(nextEnabled ? 'Agent enabled' : 'Agent disabled')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to update agent: ${error.message}`
          : 'Failed to update agent',
      )
    }
  }

  const handleClearMemory = async () => {
    if (memories.length === 0) return
    const confirmed = window.confirm(
      `Deactivate ${memories.length} active memory entries for ${agent.name}?`,
    )
    if (!confirmed) return
    try {
      await Promise.all(
        memories.map((memory) =>
          deactivateMemory.mutateAsync({ memoryId: memory.id }),
        ),
      )
      toast.success('Active memories deactivated')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to clear memory: ${error.message}`
          : 'Failed to clear memory',
      )
    }
  }

  const handleDelete = async () => {
    const confirmed = window.confirm(
      `Delete ${agent.name}? This cannot be undone.`,
    )
    if (!confirmed) return
    try {
      await deleteMutation.mutateAsync(agent.id)
      toast.success('Agent deleted')
      navigate({ to: '/deployments/agents' })
    } catch (error) {
      toast.error(
        error instanceof Error
          ? `Failed to delete agent: ${error.message}`
          : 'Failed to delete agent',
      )
    }
  }

  return (
    <SettingsScroll>
      <PanelHeader
        title="Danger Zone"
        description="Destructive actions that affect the live agent or its retained context."
      />
      <div className="space-y-3">
        <DangerAction
          title={agent.enabled ? 'Disable agent' : 'Enable agent'}
          description={
            agent.enabled
              ? 'Stop new invocations without deleting configuration or history.'
              : 'Allow the agent to receive new invocations again.'
          }
          action={agent.enabled ? 'Disable' : 'Enable'}
          onClick={handleToggleEnabled}
          isPending={updateMutation.isPending}
        />
        <DangerAction
          title="Deactivate active memory"
          description="Soft-clear active memory entries while preserving the Memory tab audit trail."
          action="Deactivate active"
          onClick={handleClearMemory}
          isPending={deactivateMemory.isPending}
          disabled={memories.length === 0}
        />
        <DangerAction
          title="Delete agent"
          description="Remove this agent definition. Existing sessions may remain as historical records."
          action="Delete"
          onClick={handleDelete}
          isPending={deleteMutation.isPending}
          destructive
        />
      </div>
    </SettingsScroll>
  )
}

function PanelHeader({
  title,
  description,
}: {
  title: string
  description: string
}) {
  return (
    <div className="mb-3 shrink-0 rounded border border-brand-main-600 bg-brand-main-900/50 px-4 py-3">
      <h1 className="text-base font-semibold text-white light:text-brand-main-50">{title}</h1>
      <p className="mt-1 max-w-3xl text-xs leading-relaxed text-white/50 light:text-black/50">
        {description}
      </p>
    </div>
  )
}

function SettingsScroll({ children }: { children: ReactNode }) {
  return (
    <div className="h-full min-h-0 overflow-y-auto pr-1 scrollbar-macos">
      {children}
    </div>
  )
}

function SettingsCard({
  title,
  description,
  children,
  className = '',
}: {
  title: string
  description?: string
  children?: ReactNode
  className?: string
}) {
  return (
    <section className={`${panelClass} p-4 ${className}`}>
      <div className="mb-3">
        <h2 className="text-sm font-medium text-white light:text-brand-main-50">{title}</h2>
        {description && (
          <p className="mt-1 text-xs leading-relaxed text-white/50 light:text-black/50">
            {description}
          </p>
        )}
      </div>
      <div className="space-y-3">{children}</div>
    </section>
  )
}

function FieldGrid({ children }: { children: ReactNode }) {
  return <div className="grid gap-3 sm:grid-cols-2">{children}</div>
}

function NumberField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder: string
}) {
  return (
    <div className="space-y-2">
      <Label className="text-xs text-white/60 light:text-black/60">{label}</Label>
      <Input
        type="number"
        min={0}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className={inputClass}
      />
    </div>
  )
}

function ToggleRow({
  title,
  description,
  checked,
  onCheckedChange,
}: {
  title: string
  description: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2.5">
      <div>
        <p className="text-sm text-white/80 light:text-black/80">{title}</p>
        <p className="mt-1 text-[11px] leading-relaxed text-white/40 light:text-black/40">
          {description}
        </p>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  )
}

function SaveBar({
  label,
  isSaving,
  onSave,
}: {
  label: string
  isSaving: boolean
  onSave: () => void
}) {
  return (
    <div className="sticky bottom-0 mt-4 rounded border border-brand-main-600 bg-brand-main-900/95 px-4 py-3 backdrop-blur">
      <div className="flex justify-end">
        <Button
          type="button"
          size="sm"
          onClick={onSave}
          disabled={isSaving}
          className={brandPrimaryButtonClass}
        >
          {isSaving && <Loader2 className="size-3.5 animate-spin" />}
          {label}
        </Button>
      </div>
    </div>
  )
}

function SummaryList({ rows }: { rows: [string, string][] }) {
  return (
    <dl className="divide-y divide-brand-main-700/50 rounded border border-brand-main-600 bg-brand-main-900/50">
      {rows.map(([label, value]) => (
        <div
          key={label}
          className="grid grid-cols-[132px_minmax(0,1fr)] gap-3 px-3 py-2"
        >
          <dt className="text-[11px] text-white/40 light:text-black/40">{label}</dt>
          <dd className="min-w-0 truncate text-xs text-white/70 light:text-black/70">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function PostureCard({
  title,
  value,
  tone,
  description,
}: {
  title: string
  value: string
  tone: 'good' | 'warn' | 'muted'
  description: string
}) {
  const toneClass =
    tone === 'good'
      ? 'border-green-500/25 bg-green-500/10 text-green-300 light:text-green-600'
      : tone === 'warn'
        ? 'border-yellow-500/25 bg-yellow-500/10 text-yellow-300 light:text-yellow-700'
        : 'border-brand-main-600 bg-brand-main-900/60 text-white/60 light:text-black/60'
  return (
    <div className={`${panelClass} p-4`}>
      <p className="text-xs text-white/50 light:text-black/50">{title}</p>
      <div className={`mt-3 rounded border px-3 py-2 text-sm ${toneClass}`}>
        {value}
      </div>
      <p className="mt-3 text-[11px] leading-relaxed text-white/40 light:text-black/40">
        {description}
      </p>
    </div>
  )
}

function CheckItem({ label }: { label: string }) {
  return (
    <div className="flex items-start gap-2 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2">
      <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-green-400/80 light:text-green-600/80" />
      <span className="text-xs leading-relaxed text-white/60 light:text-black/60">{label}</span>
    </div>
  )
}

function LinkedAgentRow({
  link,
  onDelete,
  isDeleting,
}: {
  link: AgentLink
  onDelete: () => void
  isDeleting: boolean
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2.5">
      <div className="min-w-0">
        <p className="truncate text-sm text-white/80 light:text-black/80">
          {link.targetName || link.targetId}
        </p>
        <p className="mt-1 flex items-center gap-2 text-[11px] text-white/35 light:text-black/35">
          <span>{linkTypeLabel(link.linkType)}</span>
          <span>/</span>
          <span>{linkProtocolLabel(link.protocol)}</span>
        </p>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={isDeleting}
        onClick={onDelete}
        className="h-8 px-2 text-white/45 light:text-black/45 hover:text-red-300 light:hover:text-red-600"
      >
        <Trash2 className="size-3.5" />
      </Button>
    </div>
  )
}

function DangerAction({
  title,
  description,
  action,
  onClick,
  isPending,
  disabled,
  destructive,
}: {
  title: string
  description: string
  action: string
  onClick: () => void
  isPending: boolean
  disabled?: boolean
  destructive?: boolean
}) {
  return (
    <div
      className={`${panelClass} flex items-center justify-between gap-4 p-4`}
    >
      <div>
        <h2 className="text-sm font-medium text-white light:text-brand-main-50">{title}</h2>
        <p className="mt-1 text-xs leading-relaxed text-white/50 light:text-black/50">
          {description}
        </p>
      </div>
      <Button
        type="button"
        size="sm"
        variant={destructive ? 'destructive' : 'outline'}
        disabled={disabled || isPending}
        onClick={onClick}
        className={
          destructive
            ? brandPrimaryButtonClass
            : `${brandActionButtonClass} shrink-0`
        }
      >
        {destructive && <Trash2 className="size-3.5" />}
        {isPending ? 'Working...' : action}
      </Button>
    </div>
  )
}

function EmptyState({ label }: { label: string }) {
  return (
    <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/40 px-3 py-6 text-center text-xs text-white/40 light:text-black/40">
      {label}
    </div>
  )
}

function cloneConfig(config: unknown): AgentConfig {
  if (!config || typeof config !== 'object') return {}
  return JSON.parse(JSON.stringify(config)) as AgentConfig
}

function parseOptionalInt(value: string): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return undefined
  return Math.max(0, Math.trunc(parsed))
}

function taskPermissionEnumToValue(mode: TaskPermissionMode | undefined) {
  if (mode === TaskPermissionMode.ALWAYS) return 'always'
  if (mode === TaskPermissionMode.DENY) return 'deny'
  return 'ask'
}

function taskPermissionValueToEnum(value: string): TaskPermissionMode {
  if (value === 'always') return TaskPermissionMode.ALWAYS
  if (value === 'deny') return TaskPermissionMode.DENY
  return TaskPermissionMode.ASK
}

function permissionLabel(mode: TaskPermissionMode | undefined) {
  if (mode === TaskPermissionMode.ALWAYS) return 'Always allow'
  if (mode === TaskPermissionMode.DENY) return 'Always deny'
  return 'Ask first'
}

function lifecycleModeLabel(mode: AgentLifecycleMode | undefined) {
  if (mode === AgentLifecycleMode.PERSISTENT) return 'Persistent'
  if (mode === AgentLifecycleMode.EPHEMERAL) return 'Ephemeral'
  return 'Unspecified'
}

function lifecycleStatusLabel(status: AgentLifecycleStatus | undefined) {
  if (status === AgentLifecycleStatus.CREATED) return 'Created'
  if (status === AgentLifecycleStatus.PROVISIONING) return 'Provisioning'
  if (status === AgentLifecycleStatus.RUNNING) return 'Running'
  if (status === AgentLifecycleStatus.IDLE) return 'Idle'
  if (status === AgentLifecycleStatus.SLEEPING) return 'Sleeping'
  if (status === AgentLifecycleStatus.WAKING) return 'Waking'
  if (status === AgentLifecycleStatus.FAILED) return 'Failed'
  if (status === AgentLifecycleStatus.TERMINATED) return 'Terminated'
  return 'Unknown'
}

function linkTypeLabel(type: AgentLinkType | undefined) {
  if (type === AgentLinkType.SUPERVISOR) return 'Supervisor'
  if (type === AgentLinkType.SUBORDINATE) return 'Subordinate'
  if (type === AgentLinkType.COLLABORATOR) return 'Collaborator'
  return 'Peer'
}

function linkProtocolLabel(protocol: number | undefined) {
  if (protocol === 2) return 'Channel'
  if (protocol === 3) return 'Webhook'
  return 'Internal'
}

function sessionStatusLabel(status: number | undefined) {
  if (status === 1) return 'Created'
  if (status === 2) return 'Running'
  if (status === 3) return 'Waiting input'
  if (status === 4) return 'Waiting approval'
  if (status === 5) return 'Completed'
  if (status === 6) return 'Failed'
  if (status === 7) return 'Cancelled'
  return 'Unknown'
}

function buildEffectivePromptPreview(agent: AgentDefinition) {
  const parts = [
    agent.systemPrompt ? `# System Prompt\n${agent.systemPrompt}` : '',
    agent.identity?.soulMd ? `# SOUL.md\n${agent.identity.soulMd}` : '',
    agent.identity?.identityMd
      ? `# IDENTITY.md\n${agent.identity.identityMd}`
      : '',
    agent.identity?.userMd ? `# USER.md\n${agent.identity.userMd}` : '',
    agent.identity?.roleMd ? `# ROLE.md\n${agent.identity.roleMd}` : '',
  ]
  return parts.filter(Boolean).join('\n\n')
}

function isString(value: unknown): value is string {
  return typeof value === 'string'
}
