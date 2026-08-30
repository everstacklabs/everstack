import { useState, useMemo, useEffect, useCallback, useRef } from 'react'
import {
  X,
  ChevronUp,
  ChevronDown,
  Search,
  ChevronRight,
  Loader2,
  BookOpen,
  Sparkles,
  Trash2,
} from 'lucide-react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import {
  useCreateAgent,
  useUpdateAgent,
  useAgents,
  useAgentLinks,
  useCreateAgentLink,
  useDeleteAgentLink,
} from '@/hooks/deployments/use-agents'
import { useFunctions } from '@/hooks/deployments/use-functions'
import { useFederatedTools, useMcpServers } from '@/hooks/gateway/use-mcp'
import { mcpToolName } from './tool-catalog'
import type { McpToolEntry } from './tools-tab-content'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import {
  useGitHubInstallations,
  useGitHubRepositories,
  useGitHubBranches,
} from '@/hooks/integrations/use-github'
import { useCollections } from '@/hooks/deployments/use-memory'
import {
  AgentMode,
  TaskPermissionMode,
  AgentLifecycleMode,
  AgentLinkType,
  type AgentDefinition,
} from '@/server/agents'
import {
  useSandboxTemplates,
  useSandboxInstances,
} from '@/hooks/deployments/use-sandbox'
import type { SandboxTemplate } from '@/server/sandbox'
import { useRuntimeConfigSection } from '@/hooks/gateway/use-runtime-config'
import { useLicenseStatus } from '@/hooks/license/use-license-status'
import {
  SANDBOX_MACHINE_PROFILES,
  estimateDiskHourlyUsd,
  resolveSandboxPricing,
  sandboxMachineProfilesForTier,
} from '@/lib/sandbox-pricing'
import { Iconify } from '@everstack/ui/icons'
import { getApiBaseUrl } from '@/lib/api-url'
import { getCloudBillingUrl, isCloudManaged } from '@/lib/cloud-mode'
import { ConfigureProviderSheet } from '@/components/providers'
import { ToolsTabContent } from './tools-tab-content'
import { McpTabContent } from './mcp-tab-content'

const {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetBody,
  Button,
  Input,
  Label,
  Textarea,
  Switch,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectGroup,
  SelectLabel,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Popover,
  PopoverTrigger,
  PopoverContent,
} = ui

const brandSelectTriggerClass =
  'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200'
const brandSelectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-zinc-200'
const brandInputClass = 'bg-brand-main-900 border-brand-main-600'
const brandTextareaClass =
  'bg-brand-main-900 border-brand-main-600 focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500 focus-visible:ring-[1px]'
const DEFAULT_MAX_STEPS = ''
const DEFAULT_WORKING_DIRECTORY = '/workspace'
const DEFAULT_AGENT_COLOR = '#64748b'

const AGENT_COLORS = [
  { hex: '#64748b', label: 'Slate' },
  { hex: '#6b7280', label: 'Gray' },
  { hex: '#ef4444', label: 'Red' },
  { hex: '#f97316', label: 'Orange' },
  { hex: '#eab308', label: 'Yellow' },
  { hex: '#22c55e', label: 'Green' },
  { hex: '#14b8a6', label: 'Teal' },
  { hex: '#3b82f6', label: 'Blue' },
  { hex: '#6366f1', label: 'Indigo' },
  { hex: '#a855f7', label: 'Purple' },
  { hex: '#ec4899', label: 'Pink' },
  { hex: '#f43f5e', label: 'Rose' },
] as const

// ─── Sandbox templates (fallback when API unavailable) ─────────────
const FALLBACK_TEMPLATES: SandboxTemplate[] = [
  {
    id: 'tpl_node',
    name: 'Node.js',
    slug: 'node',
    description: 'JavaScript/TypeScript runtime',
    icon: 'simple-icons:nodedotjs',
    iconColor: '#5FA04E',
    image: 'ghcr.io/everstacklabs/sandbox:node',
    cpuLimit: 1,
    memoryMb: 512,
    diskMb: 1024,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'tpl_deno',
    name: 'Deno',
    slug: 'deno',
    description: 'Secure TypeScript runtime',
    icon: 'simple-icons:deno',
    iconColor: '#70FFAF',
    image: 'ghcr.io/everstacklabs/sandbox:deno',
    cpuLimit: 1,
    memoryMb: 512,
    diskMb: 1024,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'tpl_python',
    name: 'Python',
    slug: 'python',
    description: 'Python 3 with pip',
    icon: 'simple-icons:python',
    iconColor: '#3776AB',
    image: 'ghcr.io/everstacklabs/sandbox:python',
    cpuLimit: 1,
    memoryMb: 512,
    diskMb: 1024,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'tpl_rust',
    name: 'Rust',
    slug: 'rust',
    description: 'Rust with cargo',
    icon: 'simple-icons:rust',
    iconColor: '#DEA584',
    image: 'ghcr.io/everstacklabs/sandbox:rust',
    cpuLimit: 2,
    memoryMb: 1024,
    diskMb: 2048,
    timeoutSeconds: 600,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'tpl_go',
    name: 'Go',
    slug: 'go',
    description: 'Go with modules',
    icon: 'simple-icons:go',
    iconColor: '#00ADD8',
    image: 'ghcr.io/everstacklabs/sandbox:go',
    cpuLimit: 2,
    memoryMb: 1024,
    diskMb: 2048,
    timeoutSeconds: 600,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
  {
    id: 'tpl_ubuntu',
    name: 'Ubuntu',
    slug: 'ubuntu',
    description: 'General-purpose Linux with Node.js and Python',
    icon: 'simple-icons:ubuntu',
    iconColor: '#E95420',
    image: 'ghcr.io/everstacklabs/sandbox:fullstack',
    cpuLimit: 1,
    memoryMb: 512,
    diskMb: 1024,
    timeoutSeconds: 300,
    networkMode: 'whitelist',
    workDir: '/workspace',
  },
]

const CUSTOM_TEMPLATE_OPTION = {
  id: 'custom',
  name: 'Custom',
  slug: 'custom',
  description: 'Use any Docker image',
  icon: 'heroicons:cube-transparent',
  iconColor: '#a78bfa',
  image: '',
} as const

// ─── Machine profiles ──────────────────────────────────────────────
const DEFAULT_MACHINE_PROFILE = SANDBOX_MACHINE_PROFILES[0]!

function matchMachineProfile(
  cpu: number,
  memoryMb: number,
  diskMb: number,
): string {
  const match = SANDBOX_MACHINE_PROFILES.find(
    (p) => p.cpu === cpu && p.memoryMb === memoryMb && p.diskMb === diskMb,
  )
  return match?.id ?? 'custom'
}

function matchTemplateSlug(
  image: string,
  templates: SandboxTemplate[],
): string {
  if (!image) return 'custom'
  const match = templates.find((t) => t.image === image)
  return match?.slug ?? 'custom'
}

function formatCost(amount: number): string {
  const decimals = Math.abs(amount) >= 1 ? 2 : 4
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(amount)
}

type AgentModeValue = 'primary' | 'subagent'
type TaskPermissionValue = 'ask' | 'always' | 'deny'

interface InstalledSkill {
  name: string
  description: string
  source: string
  content: string
  installed_at: string
}

interface RegistrySkill {
  source: string
  skillId: string
  name: string
  installs: number
}

type RegistryView = 'all-time' | 'trending' | 'hot'

const REGISTRY_VIEWS: { key: RegistryView; label: string }[] = [
  { key: 'all-time', label: 'All Time' },
  { key: 'trending', label: 'Trending' },
  { key: 'hot', label: 'Hot' },
]

function registryInstallSpec(skill: RegistrySkill): string {
  const repoName = skill.source.split('/').pop()
  if (repoName === skill.skillId) return skill.source
  return `${skill.source}/${skill.skillId}`
}

function formatInstalls(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

function parseExistingSkillsConfig(
  agent?: AgentDefinition | null,
): InstalledSkill[] {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const skills = cfg?.skills
    if (!Array.isArray(skills)) return []
    return skills.filter(
      (s: any) => s && typeof s.content === 'string' && s.content.length > 0,
    ) as InstalledSkill[]
  } catch {
    return []
  }
}

function parseExistingDisabledRuntimeTools(
  agent?: AgentDefinition | null,
): string[] {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const tools = cfg?.disabled_runtime_tools
    if (!Array.isArray(tools)) return []
    return tools.filter((tool): tool is string => typeof tool === 'string')
  } catch {
    return []
  }
}

function predictRuntimeToolNames(options: {
  sandboxEnabled: boolean
  browserEnabled: boolean
  memoryEnabled: boolean
  spawnEnabled: boolean
  forkEnabled: boolean
  hasRepo: boolean
  hasSkills: boolean
}) {
  const names = new Set<string>(['create_workflow', 'ask_user'])

  if (options.sandboxEnabled) {
    ;[
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
    ].forEach((name) => names.add(name))
  }

  if (options.sandboxEnabled && options.browserEnabled) {
    ;[
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
    ].forEach((name) => names.add(name))
  }

  if (options.memoryEnabled) {
    ;['memory_store', 'memory_query'].forEach((name) => names.add(name))
  }

  if (options.spawnEnabled) {
    ;['spawn_agent', 'parallel_tasks', 'check_job'].forEach((name) =>
      names.add(name),
    )
  }

  if (options.forkEnabled) names.add('fork')
  if (options.hasRepo) {
    ;['repo_glob', 'repo_read_file'].forEach((name) => names.add(name))
  }
  if (options.hasSkills) names.add('use_skill')

  return Array.from(names)
}

interface AgentFormProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  agent?: AgentDefinition | null
  activeTab?: string
  onActiveTabChange?: (tab: string) => void
}

function parseExistingSpawnConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const spawn = cfg?.spawn as Record<string, any> | undefined
    if (!spawn)
      return {
        enabled: false,
        maxDepth: 3,
        maxTotalSpawns: 10,
        childTimeout: 120,
        tokenBudget: 100000,
        planningMode: 'off' as string,
        plannerModel: 'gpt-4o-mini' as string,
      }
    return {
      enabled: spawn.enabled === true,
      maxDepth: typeof spawn.maxDepth === 'number' ? spawn.maxDepth : 3,
      maxTotalSpawns:
        typeof spawn.maxTotalSpawns === 'number' ? spawn.maxTotalSpawns : 10,
      childTimeout:
        typeof spawn.childTimeout === 'number' ? spawn.childTimeout : 120,
      tokenBudget:
        typeof spawn.totalTokenBudget === 'number'
          ? spawn.totalTokenBudget
          : 100000,
      planningMode:
        typeof spawn.planning_mode === 'string' ? spawn.planning_mode : 'off',
      plannerModel:
        typeof spawn.planner_model === 'string'
          ? spawn.planner_model
          : 'gpt-4o-mini',
    }
  } catch {
    return {
      enabled: false,
      maxDepth: 3,
      maxTotalSpawns: 10,
      childTimeout: 120,
      tokenBudget: 100000,
      planningMode: 'off' as string,
      plannerModel: 'gpt-4o-mini' as string,
    }
  }
}

function parseExistingSpawnAsync(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const spawn = cfg?.spawn as Record<string, any> | undefined
    return spawn?.async === true
  } catch {
    return false
  }
}

function parseExistingForkConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const fork = cfg?.fork as Record<string, any> | undefined
    if (!fork) return { enabled: false, maxConcurrent: 3, timeout: 120 }
    return {
      enabled: fork.enabled === true,
      maxConcurrent:
        typeof fork.max_concurrent === 'number' ? fork.max_concurrent : 3,
      timeout: typeof fork.timeout === 'number' ? fork.timeout : 120,
    }
  } catch {
    return { enabled: false, maxConcurrent: 3, timeout: 120 }
  }
}

function parseExistingMonitorConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const mon = cfg?.monitor as Record<string, any> | undefined
    if (!mon)
      return {
        enabled: false,
        maxContextTokens: 128000,
        summarizationModel: 'gpt-4o-mini',
      }
    return {
      enabled: mon.enabled === true,
      maxContextTokens:
        typeof mon.max_context_tokens === 'number'
          ? mon.max_context_tokens
          : 128000,
      summarizationModel:
        typeof mon.summarization_model === 'string'
          ? mon.summarization_model
          : 'gpt-4o-mini',
    }
  } catch {
    return {
      enabled: false,
      maxContextTokens: 128000,
      summarizationModel: 'gpt-4o-mini',
    }
  }
}

function parseExistingDigestConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const digest = cfg?.digest as Record<string, any> | undefined
    if (!digest)
      return {
        enabled: false,
        refreshInterval: 3600,
        digestModel: 'gpt-4o-mini',
        maxBulletinSize: 4000,
      }
    return {
      enabled: digest.enabled === true,
      refreshInterval:
        typeof digest.refresh_interval === 'number'
          ? digest.refresh_interval
          : 3600,
      digestModel:
        typeof digest.digest_model === 'string'
          ? digest.digest_model
          : 'gpt-4o-mini',
      maxBulletinSize:
        typeof digest.max_bulletin_size === 'number'
          ? digest.max_bulletin_size
          : 4000,
    }
  } catch {
    return {
      enabled: false,
      refreshInterval: 3600,
      digestModel: 'gpt-4o-mini',
      maxBulletinSize: 4000,
    }
  }
}

function parseExistingMemoryConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const mem = cfg?.memory as Record<string, any> | undefined
    if (!mem) {
      return {
        enabled: false,
        scope: 'agent',
        autoRetrieve: true,
        autoExtract: true,
        topK: 10,
        collections: [] as string[],
      }
    }
    const rawCollections = mem.collections
    const collections = Array.isArray(rawCollections)
      ? rawCollections.filter((c: any) => typeof c === 'string')
      : []
    return {
      enabled: mem.enabled === true,
      scope: String(mem.scope ?? mem.scope ?? 'agent'),
      autoRetrieve: mem.auto_retrieve !== false && mem.autoRetrieve !== false,
      autoExtract: mem.auto_extract !== false && mem.autoExtract !== false,
      topK: Number(mem.auto_retrieve_top_k ?? mem.autoRetrieveTopK ?? 10),
      collections: collections as string[],
    }
  } catch {
    return {
      enabled: false,
      scope: 'agent',
      autoRetrieve: true,
      autoExtract: true,
      topK: 10,
      collections: [] as string[],
    }
  }
}

function parseExistingSandboxConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const sb = cfg?.sandbox as Record<string, any> | undefined
    if (!sb) {
      // Default new agents to unrestricted egress. The previous default
      // ('whitelist' with allowed_hosts=[]) was a footgun: the DNS proxy
      // correctly enforces the policy and NXDOMAINs every lookup, but
      // users read the symptom as "the sandbox has no internet" and we
      // burned cycles debugging plumbing that was working perfectly.
      // `allow` matches the "VM I can install packages in" mental model;
      // operators can opt into whitelist + curate the list when they
      // need it.
      return {
        enabled: false,
        image: '',
        cpuLimit: DEFAULT_MACHINE_PROFILE.cpu,
        memoryMb: DEFAULT_MACHINE_PROFILE.memoryMb,
        diskMb: DEFAULT_MACHINE_PROFILE.diskMb,
        timeoutSeconds: 300,
        networkMode: 'allow',
        allowedHosts: [] as string[],
        gitRepoUrl: '',
        gitBranch: '',
        gitInstallationId: 0,
        persistent: false,
        linkedSessionId: '',
      }
    }

    const cpuLimit = Number(sb.cpu_limit ?? sb.cpuLimit)
    const memoryMb = Number(sb.memory_mb ?? sb.memoryMb)
    const diskMb = Number(sb.disk_mb ?? sb.diskMb)
    const timeoutSeconds = Number(sb.timeout_seconds ?? sb.timeoutSeconds)
    const gitInstallationId = Number(
      sb.git_installation_id ?? sb.gitInstallationId,
    )

    return {
      enabled: sb.enabled === true,
      image: typeof sb.image === 'string' ? sb.image : '',
      cpuLimit:
        Number.isFinite(cpuLimit) && cpuLimit > 0
          ? cpuLimit
          : DEFAULT_MACHINE_PROFILE.cpu,
      memoryMb:
        Number.isFinite(memoryMb) && memoryMb > 0
          ? memoryMb
          : DEFAULT_MACHINE_PROFILE.memoryMb,
      diskMb:
        Number.isFinite(diskMb) && diskMb > 0
          ? diskMb
          : DEFAULT_MACHINE_PROFILE.diskMb,
      timeoutSeconds:
        Number.isFinite(timeoutSeconds) && timeoutSeconds > 0
          ? timeoutSeconds
          : 300,
      networkMode:
        typeof sb.network_mode === 'string'
          ? sb.network_mode
          : typeof sb.networkMode === 'string'
            ? sb.networkMode
            : 'deny',
      allowedHosts: Array.isArray(sb.allowed_hosts)
        ? sb.allowed_hosts.filter((h: any) => typeof h === 'string')
        : Array.isArray(sb.allowedHosts)
          ? sb.allowedHosts.filter((h: any) => typeof h === 'string')
          : [],
      gitRepoUrl:
        typeof sb.git_repo_url === 'string'
          ? sb.git_repo_url
          : typeof sb.gitRepoUrl === 'string'
            ? sb.gitRepoUrl
            : '',
      gitBranch:
        typeof sb.git_branch === 'string'
          ? sb.git_branch
          : typeof sb.gitBranch === 'string'
            ? sb.gitBranch
            : '',
      gitInstallationId:
        Number.isFinite(gitInstallationId) && gitInstallationId > 0
          ? Math.trunc(gitInstallationId)
          : 0,
      persistent: sb.persistent === true,
      linkedSessionId:
        typeof sb.linked_session_id === 'string'
          ? sb.linked_session_id
          : typeof sb.linkedSessionId === 'string'
            ? sb.linkedSessionId
            : '',
    }
  } catch {
    return {
      enabled: false,
      image: '',
      cpuLimit: DEFAULT_MACHINE_PROFILE.cpu,
      memoryMb: DEFAULT_MACHINE_PROFILE.memoryMb,
      diskMb: DEFAULT_MACHINE_PROFILE.diskMb,
      timeoutSeconds: 300,
      networkMode: 'deny',
      gitRepoUrl: '',
      gitBranch: '',
      gitInstallationId: 0,
      persistent: false,
      linkedSessionId: '',
    }
  }
}

function parseExistingBrowserConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const br = cfg?.browser as Record<string, any> | undefined
    if (!br) return { enabled: false, headless: true }
    return {
      enabled: br.enabled === true,
      headless: br.headless !== false,
    }
  } catch {
    return { enabled: false, headless: true }
  }
}

function parseExistingFallbackConfig(agent?: AgentDefinition | null) {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    const fb = cfg?.fallback as Record<string, any> | undefined
    if (!fb) return { enabled: false, models: [] as string[] }
    return {
      enabled: fb.enabled === true,
      models: Array.isArray(fb.models)
        ? fb.models.filter((m: any) => typeof m === 'string')
        : [],
    }
  } catch {
    return { enabled: false, models: [] as string[] }
  }
}

function parseExistingAPIType(agent?: AgentDefinition | null): string {
  try {
    const cfg = agent?.config as Record<string, any> | undefined
    if (cfg?.api_type === 'responses') return 'responses'
  } catch {
    /* ignore */
  }
  return 'chat_completions'
}

function modeEnumToValue(mode: AgentMode | undefined): AgentModeValue {
  if (mode === AgentMode.SUBAGENT) return 'subagent'
  return 'primary'
}

function modeValueToEnum(mode: AgentModeValue): AgentMode {
  return mode === 'subagent' ? AgentMode.SUBAGENT : AgentMode.PRIMARY
}

function taskPermissionEnumToValue(
  mode: TaskPermissionMode | undefined,
): TaskPermissionValue {
  if (mode === TaskPermissionMode.ALWAYS) return 'always'
  if (mode === TaskPermissionMode.DENY) return 'deny'
  return 'ask'
}

function taskPermissionValueToEnum(
  mode: TaskPermissionValue,
): TaskPermissionMode {
  if (mode === 'always') return TaskPermissionMode.ALWAYS
  if (mode === 'deny') return TaskPermissionMode.DENY
  return TaskPermissionMode.ASK
}

function parseExistingHidden(agent?: AgentDefinition | null): boolean {
  if (!agent) return false
  if (agent.hidden) return true
  return modeEnumToValue(agent.mode) === 'subagent'
}

function parseMaxStepsInput(agent?: AgentDefinition | null): string {
  if (!agent) return DEFAULT_MAX_STEPS
  if (
    typeof agent.maxSteps === 'number' &&
    Number.isFinite(agent.maxSteps) &&
    agent.maxSteps > 0
  ) {
    return String(agent.maxSteps)
  }
  return DEFAULT_MAX_STEPS
}

function parseGitHubRepo(
  value: string,
): { owner: string; repo: string; fullName: string } | null {
  const raw = value.trim()
  if (!raw) return null

  let normalized = raw
  const githubPrefix = 'https://github.com/'
  if (normalized.startsWith(githubPrefix)) {
    normalized = normalized.slice(githubPrefix.length)
  }
  normalized = normalized.replace(/\.git$/, '').replace(/^\/+|\/+$/g, '')

  const parts = normalized.split('/')
  if (parts.length < 2) return null
  const owner = parts[0]
  const repo = parts[1]
  if (!owner || !repo) return null

  return { owner, repo, fullName: `${owner}/${repo}` }
}

export function AgentFormDialog({
  open,
  onOpenChange,
  agent,
  activeTab: activeTabProp,
  onActiveTabChange,
}: AgentFormProps) {
  const isEditing = !!agent
  const createMutation = useCreateAgent()
  const updateMutation = useUpdateAgent()
  const { data: functions = [] } = useFunctions()
  const { data: mcpServersList = [] } = useMcpServers(true)
  const { data: federatedMcpTools = [] } = useFederatedTools()
  const mcpToolEntries = useMemo<McpToolEntry[]>(() => {
    if (!federatedMcpTools.length) return []
    const serverById = new Map(mcpServersList.map((s) => [s.id, s]))
    return federatedMcpTools.flatMap((tool) => {
      const server = serverById.get(tool.serverId)
      if (!server) return []
      return [
        {
          name: mcpToolName(server.name, tool.name),
          toolName: tool.name,
          serverId: tool.serverId,
          serverName: server.name,
          description: tool.description ?? '',
        },
      ]
    })
  }, [federatedMcpTools, mcpServersList])
  const { data: gatewayModels = [], isLoading: modelsLoading } =
    useGatewayModels()
  const {
    data: gitHubInstallations = [],
    isLoading: gitHubInstallationsLoading,
  } = useGitHubInstallations()

  // Inline provider connect sheet (shown when no models are configured)
  const [connectProviderOpen, setConnectProviderOpen] = useState(false)
  const [connectProviderName, setConnectProviderName] = useState<string | null>(
    null,
  )

  const [name, setName] = useState(agent?.name ?? '')
  const [model, setModel] = useState(agent?.model ?? '')
  const [description, setDescription] = useState(agent?.description ?? '')
  const [systemPrompt, setSystemPrompt] = useState(agent?.systemPrompt ?? '')
  const [maxTurns, setMaxTurns] = useState(agent?.maxTurns ?? 25)
  const [maxToolCallsPerTurn, setMaxToolCallsPerTurn] = useState(
    agent?.maxToolCallsPerTurn ?? 10,
  )
  const [mode, setMode] = useState<AgentModeValue>(modeEnumToValue(agent?.mode))
  const [maxStepsInput, setMaxStepsInput] = useState(parseMaxStepsInput(agent))
  const [taskPermission, setTaskPermission] = useState<TaskPermissionValue>(
    taskPermissionEnumToValue(agent?.taskPermissionMode),
  )
  const [hidden, setHidden] = useState(parseExistingHidden(agent))
  const [color, setColor] = useState(agent?.color ?? '')
  const [workingDirectory, setWorkingDirectory] = useState(
    agent?.workingDirectory ?? DEFAULT_WORKING_DIRECTORY,
  )
  const [mentionAlias, setMentionAlias] = useState(agent?.mentionAlias ?? '')
  const [enabled, setEnabled] = useState(agent?.enabled ?? true)
  const [lifecycleMode, setLifecycleMode] = useState<AgentLifecycleMode>(
    agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
      ? AgentLifecycleMode.PERSISTENT
      : AgentLifecycleMode.EPHEMERAL,
  )
  const [selectedTools, setSelectedTools] = useState<string[]>(
    agent?.tools ?? [],
  )
  const attachedMcpServerCount = useMemo(() => {
    if (!mcpServersList.length || !mcpToolEntries.length) return 0
    const selectedSet = new Set(selectedTools)
    const byServer = new Map<string, McpToolEntry[]>()
    for (const t of mcpToolEntries) {
      const list = byServer.get(t.serverId)
      if (list) list.push(t)
      else byServer.set(t.serverId, [t])
    }
    let count = 0
    for (const server of mcpServersList) {
      const tools = byServer.get(server.id) ?? []
      if (tools.length === 0) continue
      if (tools.every((t) => selectedSet.has(t.name))) count++
    }
    return count
  }, [mcpServersList, mcpToolEntries, selectedTools])
  const [apiType, setApiType] = useState(parseExistingAPIType(agent))

  const existingSpawn = parseExistingSpawnConfig(agent)
  const [spawnEnabled, setSpawnEnabled] = useState(existingSpawn.enabled)
  const [spawnMaxDepth, setSpawnMaxDepth] = useState(existingSpawn.maxDepth)
  const [spawnMaxTotalSpawns, setSpawnMaxTotalSpawns] = useState(
    existingSpawn.maxTotalSpawns,
  )
  const [spawnChildTimeout, setSpawnChildTimeout] = useState(
    existingSpawn.childTimeout,
  )
  const [spawnTokenBudget, setSpawnTokenBudget] = useState(
    existingSpawn.tokenBudget,
  )
  const [spawnPlanningMode, setSpawnPlanningMode] = useState(
    existingSpawn.planningMode,
  )
  const [spawnPlannerModel, setSpawnPlannerModel] = useState(
    existingSpawn.plannerModel,
  )
  const [spawnAsync, setSpawnAsync] = useState(parseExistingSpawnAsync(agent))

  // Fork config state
  const existingFork = parseExistingForkConfig(agent)
  const [forkEnabled, setForkEnabled] = useState(existingFork.enabled)
  const [forkMaxConcurrent, setForkMaxConcurrent] = useState(
    existingFork.maxConcurrent,
  )
  const [forkTimeout, setForkTimeout] = useState(existingFork.timeout)

  // Monitor config state
  const existingMonitor = parseExistingMonitorConfig(agent)
  const [monitorEnabled, setMonitorEnabled] = useState(existingMonitor.enabled)
  const [monitorMaxContextTokens, setMonitorMaxContextTokens] = useState(
    existingMonitor.maxContextTokens,
  )
  const [monitorSummarizationModel, setMonitorSummarizationModel] = useState(
    existingMonitor.summarizationModel,
  )

  // Digest config state
  const existingDigest = parseExistingDigestConfig(agent)
  const [digestEnabled, setDigestEnabled] = useState(existingDigest.enabled)
  const [digestRefreshInterval, setDigestRefreshInterval] = useState(
    existingDigest.refreshInterval,
  )
  const [digestModel, setDigestModel] = useState(existingDigest.digestModel)
  const [digestMaxBulletinSize, setDigestMaxBulletinSize] = useState(
    existingDigest.maxBulletinSize,
  )

  // Identity state (persistent agents only)
  const existingIdentity = (agent as any)?.identity
  const [soulMd, setSoulMd] = useState(existingIdentity?.soulMd ?? '')
  const [identityMd, setIdentityMd] = useState(
    existingIdentity?.identityMd ?? '',
  )
  const [userMd, setUserMd] = useState(existingIdentity?.userMd ?? '')
  const [roleMd, setRoleMd] = useState(existingIdentity?.roleMd ?? '')

  const existingSandbox = parseExistingSandboxConfig(agent)
  const [sandboxEnabled, setSandboxEnabled] = useState(
    agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
      ? true
      : existingSandbox.enabled,
  )
  const [sandboxImage, setSandboxImage] = useState(existingSandbox.image)
  const [sandboxCpuLimit, setSandboxCpuLimit] = useState(
    existingSandbox.cpuLimit,
  )
  const [sandboxMemoryMb, setSandboxMemoryMb] = useState(
    existingSandbox.memoryMb,
  )
  const [sandboxDiskMb, setSandboxDiskMb] = useState(existingSandbox.diskMb)
  const [sandboxTimeoutSeconds, setSandboxTimeoutSeconds] = useState(
    existingSandbox.timeoutSeconds,
  )
  const [sandboxNetworkMode, setSandboxNetworkMode] = useState(
    existingSandbox.networkMode,
  )
  const [sandboxAllowedHosts, setSandboxAllowedHosts] = useState<string[]>(
    existingSandbox.allowedHosts ?? [],
  )
  const [sandboxHostInput, setSandboxHostInput] = useState('')
  const [sandboxGitRepoUrl, setSandboxGitRepoUrl] = useState(
    existingSandbox.gitRepoUrl,
  )
  const [sandboxGitBranch, setSandboxGitBranch] = useState(
    existingSandbox.gitBranch,
  )
  const [sandboxGitInstallationId, setSandboxGitInstallationId] = useState(
    existingSandbox.gitInstallationId > 0
      ? String(existingSandbox.gitInstallationId)
      : '',
  )

  // Browser automation config
  const existingBrowser = parseExistingBrowserConfig(agent)
  const [browserEnabled, setBrowserEnabled] = useState(existingBrowser.enabled)
  const [browserHeadless, setBrowserHeadless] = useState(
    existingBrowser.headless,
  )

  // Sandbox mode: 'new' creates a fresh sandbox, 'existing' links to a running one
  const [sandboxMode, setSandboxMode] = useState<'new' | 'existing'>(
    existingSandbox.linkedSessionId ? 'existing' : 'new',
  )
  const [linkedSessionId, setLinkedSessionId] = useState(
    existingSandbox.linkedSessionId,
  )
  const RUNNING_SANDBOX_OPTS = useMemo(
    () => ({ status: 'running' as const, limit: 100 }),
    [],
  )
  const { data: runningSandboxes } = useSandboxInstances(RUNNING_SANDBOX_OPTS)

  // Sandbox template & machine profile state
  const { data: apiTemplates } = useSandboxTemplates()
  const sandboxTemplates =
    apiTemplates && apiTemplates.length > 0 ? apiTemplates : FALLBACK_TEMPLATES
  const [selectedTemplateSlug, setSelectedTemplateSlug] = useState(() =>
    matchTemplateSlug(existingSandbox.image, sandboxTemplates),
  )
  const [selectedMachineId, setSelectedMachineId] = useState(() =>
    matchMachineProfile(
      existingSandbox.cpuLimit,
      existingSandbox.memoryMb,
      existingSandbox.diskMb,
    ),
  )

  // Pricing
  const { data: licenseData } = useLicenseStatus()
  const { data: featuresSection } = useRuntimeConfigSection('features')
  const tier = licenseData?.license?.tier ?? 'free'
  const managedCloud = useMemo(() => isCloudManaged(), [])
  const sandboxBillingEnabled =
    !managedCloud || (licenseData?.license?.sandbox_billing_enabled ?? false)
  const availableMachineProfiles = useMemo(
    () =>
      managedCloud
        ? sandboxMachineProfilesForTier(tier)
        : SANDBOX_MACHINE_PROFILES,
    [managedCloud, tier],
  )
  const sandboxPricing = useMemo(
    () => resolveSandboxPricing(featuresSection?.config),
    [featuresSection?.config],
  )
  const computeCost = useMemo(() => {
    const cpuHour = sandboxCpuLimit * sandboxPricing.cpuPerHourUsd
    const memHour = (sandboxMemoryMb / 1024) * sandboxPricing.memoryGbPerHourUsd
    const diskHour = estimateDiskHourlyUsd(sandboxDiskMb / 1024, sandboxPricing)
    const perHour =
      (cpuHour + memHour + diskHour + sandboxPricing.platformFeePerHourUsd) *
      (sandboxPricing.tierMultipliers[tier] ?? 1)
    return { perHour, perDay: perHour * 24, perMonth: perHour * 24 * 30 }
  }, [sandboxCpuLimit, sandboxMemoryMb, sandboxDiskMb, sandboxPricing, tier])

  // Memory config state
  const existingMemory = parseExistingMemoryConfig(agent)
  const [memoryEnabled, setMemoryEnabled] = useState(existingMemory.enabled)
  const [memoryScope, setMemoryScope] = useState(existingMemory.scope)
  const [memoryAutoRetrieve, setMemoryAutoRetrieve] = useState(
    existingMemory.autoRetrieve,
  )
  const [memoryAutoExtract, setMemoryAutoExtract] = useState(
    existingMemory.autoExtract,
  )
  const [memoryTopK, setMemoryTopK] = useState(existingMemory.topK)
  const [memoryCollections, setMemoryCollections] = useState<string[]>(
    existingMemory.collections,
  )
  const { data: availableCollections = [] } = useCollections()

  // Skills config state
  const existingSkills = parseExistingSkillsConfig(agent)
  const [skills, setSkills] = useState<InstalledSkill[]>(existingSkills)
  const [disabledRuntimeTools, setDisabledRuntimeTools] = useState<string[]>(
    parseExistingDisabledRuntimeTools(agent),
  )
  const [skillSpecInput, setSkillSpecInput] = useState('')
  const [skillResolving, setSkillResolving] = useState(false)
  const [skillSearch, setSkillSearch] = useState('')
  const [debouncedSkillSearch, setDebouncedSkillSearch] = useState('')
  const [registryView, setRegistryView] = useState<RegistryView>('all-time')
  const [registrySkills, setRegistrySkills] = useState<RegistrySkill[]>([])
  const [registryHasMore, setRegistryHasMore] = useState(false)
  const [registryPage, setRegistryPage] = useState(0)
  const [registryLoading, setRegistryLoading] = useState(false)
  const [searchResults, setSearchResults] = useState<RegistrySkill[]>([])
  const [searchCount, setSearchCount] = useState(0)
  const [searchLoading, setSearchLoading] = useState(false)
  const [customSkillOpen, setCustomSkillOpen] = useState(false)
  const skillsScrollRef = useRef<HTMLDivElement>(null)

  // Agent links (peer communication)
  const { data: agentLinks = [] } = useAgentLinks(agent?.id ?? '')
  const PERSISTENT_AGENTS_OPTS = useMemo(
    () => ({ lifecycleMode: 'persistent' as const }),
    [],
  )
  const { data: allAgents = [] } = useAgents(PERSISTENT_AGENTS_OPTS)
  const createLinkMutation = useCreateAgentLink()
  const deleteLinkMutation = useDeleteAgentLink()
  const [peerAgentId, setPeerAgentId] = useState('')
  const [peerLinkType, setPeerLinkType] = useState<
    'peer' | 'supervisor' | 'subordinate'
  >('peer')
  const linkTypeToEnum = (t: string) => {
    if (t === 'supervisor') return AgentLinkType.SUPERVISOR
    if (t === 'subordinate') return AgentLinkType.SUBORDINATE
    return AgentLinkType.PEER
  }
  // Agents available for linking (exclude self and already-linked agents)
  const linkedTargetIds = useMemo(
    () => new Set(agentLinks.map((l) => l.targetId)),
    [agentLinks],
  )
  const availablePeerAgents = useMemo(
    () =>
      allAgents.filter((a) => a.id !== agent?.id && !linkedTargetIds.has(a.id)),
    [allAgents, agent?.id, linkedTargetIds],
  )

  const existingFallback = parseExistingFallbackConfig(agent)
  const [fallbackEnabled, setFallbackEnabled] = useState(
    existingFallback.enabled,
  )
  const [fallbackModels, setFallbackModels] = useState<string[]>(
    existingFallback.models,
  )
  const [fallbackModelToAdd, setFallbackModelToAdd] = useState('')
  const [activeTabLocal, setActiveTabLocal] = useState('basics')
  const activeTab = activeTabProp ?? activeTabLocal
  const setActiveTab = onActiveTabChange ?? setActiveTabLocal
  const [gitRepoSearch, setGitRepoSearch] = useState('')
  const runtimeToolNames = useMemo(
    () =>
      predictRuntimeToolNames({
        sandboxEnabled,
        browserEnabled,
        memoryEnabled,
        spawnEnabled,
        forkEnabled,
        hasRepo: Boolean(sandboxGitRepoUrl.trim()),
        hasSkills: skills.length > 0,
      }),
    [
      sandboxEnabled,
      browserEnabled,
      memoryEnabled,
      spawnEnabled,
      forkEnabled,
      sandboxGitRepoUrl,
      skills,
    ],
  )
  const explicitSelectedTools = useMemo(
    () => selectedTools.filter((tool) => !runtimeToolNames.includes(tool)),
    [selectedTools, runtimeToolNames],
  )
  // Determine if the selected model is from an OpenAI provider
  const isOpenAIModel = useMemo(() => {
    if (!model || gatewayModels.length === 0) return false
    const openaiGroup = gatewayModels.find((g) =>
      g.provider.toLowerCase().includes('openai'),
    )
    return openaiGroup?.models.includes(model) ?? false
  }, [model, gatewayModels])

  // Auto-reset API type when switching away from OpenAI models.
  // The Responses API is OpenAI-specific; leaving it set for non-OpenAI
  // models would route calls to OpenAI's endpoint and fail.
  useEffect(() => {
    if (!isOpenAIModel && apiType === 'responses') {
      setApiType('chat_completions')
    }
  }, [isOpenAIModel, apiType])

  // Available fallback models (exclude the primary model)
  const availableFallbackModels = useMemo(() => {
    return gatewayModels
      .map((g) => ({
        ...g,
        models: g.models.filter(
          (m) => m !== model && !fallbackModels.includes(m),
        ),
      }))
      .filter((g) => g.models.length > 0)
  }, [gatewayModels, model, fallbackModels])

  const isPending = createMutation.isPending || updateMutation.isPending
  const selectedGitInstallationId = Number(sandboxGitInstallationId)
  const parsedSandboxRepo = useMemo(
    () => parseGitHubRepo(sandboxGitRepoUrl),
    [sandboxGitRepoUrl],
  )
  const repoQueryOptions = useMemo(
    () => ({ query: gitRepoSearch || undefined, page: 1, perPage: 50 }),
    [gitRepoSearch],
  )
  const { data: gitHubReposData, isLoading: gitHubReposLoading } =
    useGitHubRepositories(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      repoQueryOptions,
    )
  const BRANCH_OPTS = useMemo(() => ({ page: 1, perPage: 100 }), [])
  const { data: gitHubBranches = [], isLoading: gitHubBranchesLoading } =
    useGitHubBranches(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      parsedSandboxRepo?.owner ?? '',
      parsedSandboxRepo?.repo ?? '',
      BRANCH_OPTS,
    )
  const gitHubRepos = gitHubReposData?.repositories ?? []
  const selectedRepoIsInList = useMemo(
    () =>
      !!parsedSandboxRepo &&
      gitHubRepos.some((repo) => repo.fullName === parsedSandboxRepo.fullName),
    [gitHubRepos, parsedSandboxRepo],
  )
  const selectedGitInstallationExists = useMemo(
    () =>
      gitHubInstallations.some(
        (inst) => String(inst.installationId) === sandboxGitInstallationId,
      ),
    [gitHubInstallations, sandboxGitInstallationId],
  )

  useEffect(() => {
    if (!open) return

    const spawn = parseExistingSpawnConfig(agent)
    const sb = parseExistingSandboxConfig(agent)
    const fallback = parseExistingFallbackConfig(agent)

    setName(agent?.name ?? '')
    setModel(agent?.model ?? '')
    setDescription(agent?.description ?? '')
    setSystemPrompt(agent?.systemPrompt ?? '')
    setMaxTurns(agent?.maxTurns ?? 25)
    setMaxToolCallsPerTurn(agent?.maxToolCallsPerTurn ?? 10)
    setMode(modeEnumToValue(agent?.mode))
    setMaxStepsInput(parseMaxStepsInput(agent))
    setTaskPermission(taskPermissionEnumToValue(agent?.taskPermissionMode))
    setHidden(parseExistingHidden(agent))
    setColor(agent?.color ?? '')
    setWorkingDirectory(agent?.workingDirectory ?? DEFAULT_WORKING_DIRECTORY)
    setMentionAlias(agent?.mentionAlias ?? '')
    setEnabled(agent?.enabled ?? true)
    setLifecycleMode(
      agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
        ? AgentLifecycleMode.PERSISTENT
        : AgentLifecycleMode.EPHEMERAL,
    )
    setSelectedTools(agent?.tools ?? [])
    setApiType(parseExistingAPIType(agent))

    setSpawnEnabled(spawn.enabled)
    setSpawnMaxDepth(spawn.maxDepth)
    setSpawnMaxTotalSpawns(spawn.maxTotalSpawns)
    setSpawnChildTimeout(spawn.childTimeout)
    setSpawnTokenBudget(spawn.tokenBudget)
    setSpawnPlanningMode(spawn.planningMode)
    setSpawnPlannerModel(spawn.plannerModel)
    setSpawnAsync(parseExistingSpawnAsync(agent))

    const fork = parseExistingForkConfig(agent)
    setForkEnabled(fork.enabled)
    setForkMaxConcurrent(fork.maxConcurrent)
    setForkTimeout(fork.timeout)

    const mon = parseExistingMonitorConfig(agent)
    setMonitorEnabled(mon.enabled)
    setMonitorMaxContextTokens(mon.maxContextTokens)
    setMonitorSummarizationModel(mon.summarizationModel)

    const dig = parseExistingDigestConfig(agent)
    setDigestEnabled(dig.enabled)
    setDigestRefreshInterval(dig.refreshInterval)
    setDigestModel(dig.digestModel)
    setDigestMaxBulletinSize(dig.maxBulletinSize)

    const isPersistent = agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
    setSandboxEnabled(isPersistent ? true : sb.enabled)
    setSandboxImage(sb.image)
    setSandboxCpuLimit(sb.cpuLimit)
    setSandboxMemoryMb(sb.memoryMb)
    setSandboxDiskMb(sb.diskMb)
    setSandboxTimeoutSeconds(sb.timeoutSeconds)
    setSandboxNetworkMode(sb.networkMode)
    setSandboxAllowedHosts(sb.allowedHosts ?? [])
    setSandboxHostInput('')
    setSandboxGitRepoUrl(sb.gitRepoUrl)
    setSandboxGitBranch(sb.gitBranch)
    setSandboxGitInstallationId(
      sb.gitInstallationId > 0 ? String(sb.gitInstallationId) : '',
    )
    setSelectedTemplateSlug(matchTemplateSlug(sb.image, sandboxTemplates))
    setSelectedMachineId(
      matchMachineProfile(sb.cpuLimit, sb.memoryMb, sb.diskMb),
    )

    setFallbackEnabled(fallback.enabled)
    setFallbackModels(fallback.models)
    setFallbackModelToAdd('')
    setDisabledRuntimeTools(parseExistingDisabledRuntimeTools(agent))
    setActiveTab('basics')
    setGitRepoSearch('')

    const identity = (agent as any)?.identity
    setSoulMd(identity?.soulMd ?? '')
    setIdentityMd(identity?.identityMd ?? '')
    setUserMd(identity?.userMd ?? '')
    setRoleMd(identity?.roleMd ?? '')

    setSkills(parseExistingSkillsConfig(agent))
    setSkillSpecInput('')
    setSkillSearch('')
    setDebouncedSkillSearch('')
    setRegistryView('all-time')
    setRegistrySkills([])
    setRegistryPage(0)
    setSearchResults([])
    setCustomSkillOpen(false)
  }, [open, agent?.id])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name || !model) {
      toast.error('Name and model are required')
      return
    }

    const createsNewSandbox = sandboxEnabled && sandboxMode === 'new'
    if (managedCloud && createsNewSandbox && selectedMachineId === 'custom') {
      toast.error('Choose one of the supported fixed sandbox sizes')
      return
    }
    if (
      managedCloud &&
      createsNewSandbox &&
      lifecycleMode === AgentLifecycleMode.PERSISTENT &&
      !sandboxBillingEnabled
    ) {
      toast.error(
        'Your sandbox starter credit is exhausted. Add billing to continue',
      )
      return
    }

    const maxStepsValue = Number(maxStepsInput)
    const maxSteps =
      Number.isFinite(maxStepsValue) && maxStepsValue > 0
        ? Math.trunc(maxStepsValue)
        : undefined
    const workingDirectoryValue = workingDirectory.trim() || undefined
    const mentionAliasValue = mentionAlias.trim() || undefined

    if (mentionAliasValue && !/^[a-z0-9_-]+$/i.test(mentionAliasValue)) {
      toast.error(
        'Mention alias may only contain letters, numbers, "-" and "_"',
      )
      return
    }

    const config: Record<string, any> = {
      ...((agent?.config as Record<string, any>) ?? {}),
    }

    // API type
    if (isOpenAIModel && apiType !== 'chat_completions') {
      config.api_type = apiType
    } else {
      delete config.api_type
    }

    // Fallback
    if (fallbackEnabled && fallbackModels.length > 0) {
      config.fallback = {
        enabled: true,
        models: fallbackModels,
        max_attempts: 1,
        backoff_ms: 100,
      }
    } else {
      delete config.fallback
    }

    if (spawnEnabled) {
      config.spawn = {
        enabled: true,
        maxDepth: spawnMaxDepth,
        maxTotalSpawns: spawnMaxTotalSpawns,
        childTimeout: spawnChildTimeout,
        totalTokenBudget: spawnTokenBudget,
        planning_mode: spawnPlanningMode,
        planner_model: spawnPlannerModel,
        async: spawnAsync,
      }
    } else {
      delete config.spawn
    }

    if (forkEnabled) {
      config.fork = {
        enabled: true,
        max_concurrent: forkMaxConcurrent,
        timeout: forkTimeout,
      }
    } else {
      delete config.fork
    }

    if (monitorEnabled) {
      config.monitor = {
        enabled: true,
        max_context_tokens: monitorMaxContextTokens,
        summarization_model: monitorSummarizationModel,
      }
    } else {
      delete config.monitor
    }

    if (digestEnabled) {
      config.digest = {
        enabled: true,
        refresh_interval: digestRefreshInterval,
        digest_model: digestModel,
        max_bulletin_size: digestMaxBulletinSize,
      }
    } else {
      delete config.digest
    }

    // Build memory config (independent of sandbox)
    if (memoryEnabled) {
      config.memory = {
        enabled: true,
        scope: memoryScope,
        auto_retrieve: memoryAutoRetrieve,
        auto_retrieve_top_k: memoryTopK,
        auto_extract: memoryAutoExtract,
        ...(memoryCollections.length > 0
          ? { collections: memoryCollections }
          : {}),
      }
    } else {
      delete config.memory
    }

    // Skills config
    if (skills.length > 0) {
      config.skills = skills
    } else {
      delete config.skills
    }

    if (sandboxEnabled) {
      if (sandboxMode === 'existing' && linkedSessionId) {
        // Link to existing sandbox — minimal config
        const existingSandboxConfig =
          config.sandbox && typeof config.sandbox === 'object'
            ? (config.sandbox as Record<string, any>)
            : {}

        config.sandbox = {
          ...existingSandboxConfig,
          enabled: true,
          linked_session_id: linkedSessionId,
          persistent: lifecycleMode === AgentLifecycleMode.PERSISTENT,
        }
        // Remove fields that don't apply when linking
        delete config.sandbox.image
        delete config.sandbox.cpu_limit
        delete config.sandbox.memory_mb
        delete config.sandbox.disk_mb
        delete config.sandbox.timeout_seconds
        delete config.sandbox.network_mode
        delete config.sandbox.allowed_hosts
        delete config.sandbox.git_repo_url
        delete config.sandbox.git_branch
        delete config.sandbox.git_installation_id
      } else {
        const gitRepoUrl = sandboxGitRepoUrl.trim()
        const gitBranch = sandboxGitBranch.trim()
        const gitInstallationId = Number(sandboxGitInstallationId)

        if (
          gitRepoUrl &&
          (!Number.isFinite(gitInstallationId) || gitInstallationId <= 0)
        ) {
          toast.error(
            'GitHub installation is required when a repository is set',
          )
          return
        }
        if (!gitRepoUrl && gitBranch) {
          toast.error('Repository is required when a git branch is set')
          return
        }

        const existingSandboxConfig =
          config.sandbox && typeof config.sandbox === 'object'
            ? (config.sandbox as Record<string, any>)
            : {}

        const persistentImage = 'ghcr.io/everstacklabs/sandbox:fullstack'
        config.sandbox = {
          ...existingSandboxConfig,
          enabled: true,
          image:
            lifecycleMode === AgentLifecycleMode.PERSISTENT
              ? persistentImage
              : sandboxImage || undefined,
          cpu_limit: sandboxCpuLimit,
          memory_mb: sandboxMemoryMb,
          disk_mb: sandboxDiskMb,
          timeout_seconds: sandboxTimeoutSeconds,
          network_mode: sandboxNetworkMode,
          persistent: lifecycleMode === AgentLifecycleMode.PERSISTENT,
          ...(sandboxNetworkMode === 'whitelist' &&
          sandboxAllowedHosts.length > 0
            ? { allowed_hosts: sandboxAllowedHosts }
            : {}),
        }
        // Clear linked_session_id when creating new
        delete config.sandbox.linked_session_id

        if (gitRepoUrl) {
          config.sandbox.git_repo_url = gitRepoUrl
        } else {
          delete config.sandbox.git_repo_url
        }

        if (gitBranch) {
          config.sandbox.git_branch = gitBranch
        } else {
          delete config.sandbox.git_branch
        }

        if (Number.isFinite(gitInstallationId) && gitInstallationId > 0) {
          config.sandbox.git_installation_id = Math.trunc(gitInstallationId)
        } else {
          delete config.sandbox.git_installation_id
        }
      }
    } else {
      delete config.sandbox
    }

    // Browser automation config (nested under top-level "browser" key)
    if (sandboxEnabled && browserEnabled) {
      config.browser = {
        enabled: true,
        headless: browserHeadless,
      }
    } else {
      delete config.browser
    }

    if (disabledRuntimeTools.length > 0) {
      config.disabled_runtime_tools = disabledRuntimeTools
    } else {
      delete config.disabled_runtime_tools
    }

    const identityPayload =
      lifecycleMode === AgentLifecycleMode.PERSISTENT &&
      (soulMd.trim() || identityMd.trim() || userMd.trim() || roleMd.trim())
        ? {
            soulMd: soulMd.trim(),
            identityMd: identityMd.trim(),
            userMd: userMd.trim(),
            roleMd: roleMd.trim(),
          }
        : undefined

    try {
      if (isEditing && agent) {
        const updateParams: Parameters<typeof updateMutation.mutateAsync>[0] = {
          id: agent.id,
          name,
          model,
          description,
          systemPrompt,
          tools: explicitSelectedTools,
          config,
          maxTurns,
          maxToolCallsPerTurn,
          mode: modeValueToEnum(mode),
          maxSteps,
          taskPermissionMode: taskPermissionValueToEnum(taskPermission),
          hidden,
          color: color.trim() || undefined,
          workingDirectory: workingDirectoryValue,
          mentionAlias: mentionAliasValue,
          enabled,
          lifecycleMode,
          identity: identityPayload,
        }

        // Send sandboxConfig so the backend can auto-provision if
        // a sandbox is being added to a persistent agent for the first time.
        if (sandboxEnabled && lifecycleMode === AgentLifecycleMode.PERSISTENT) {
          if (sandboxMode === 'existing' && linkedSessionId) {
            updateParams.sandboxConfig = { linkedSessionId }
          } else {
            updateParams.sandboxConfig = {
              image:
                lifecycleMode === AgentLifecycleMode.PERSISTENT
                  ? 'ghcr.io/everstacklabs/sandbox:fullstack'
                  : sandboxImage || undefined,
              cpuLimit: sandboxCpuLimit,
              memoryMb: sandboxMemoryMb,
              diskMb: sandboxDiskMb,
              timeoutSeconds: sandboxTimeoutSeconds,
              networkMode: sandboxNetworkMode,
              ...(sandboxNetworkMode === 'whitelist' &&
              sandboxAllowedHosts.length > 0
                ? { allowedHosts: sandboxAllowedHosts }
                : {}),
              sshEnabled: false,
              gitRepoUrl: sandboxGitRepoUrl.trim() || undefined,
              gitBranch: sandboxGitBranch.trim() || undefined,
            }
          }
        }

        await updateMutation.mutateAsync(updateParams)
        toast.success('Agent updated')
      } else {
        const createParams: Parameters<typeof createMutation.mutateAsync>[0] = {
          name,
          model,
          description,
          systemPrompt,
          tools: explicitSelectedTools,
          config,
          maxTurns,
          maxToolCallsPerTurn,
          mode: modeValueToEnum(mode),
          maxSteps,
          taskPermissionMode: taskPermissionValueToEnum(taskPermission),
          hidden,
          color: color.trim() || undefined,
          workingDirectory: workingDirectoryValue,
          mentionAlias: mentionAliasValue,
          lifecycleMode,
        }

        // When sandbox is enabled and lifecycle is persistent, send
        // sandboxConfig + autoProvision so the backend provisions
        // the sandbox immediately after creation.
        if (sandboxEnabled && lifecycleMode === AgentLifecycleMode.PERSISTENT) {
          if (sandboxMode === 'existing' && linkedSessionId) {
            createParams.sandboxConfig = { linkedSessionId }
          } else {
            createParams.sandboxConfig = {
              image: sandboxImage || undefined,
              cpuLimit: sandboxCpuLimit,
              memoryMb: sandboxMemoryMb,
              diskMb: sandboxDiskMb,
              timeoutSeconds: sandboxTimeoutSeconds,
              networkMode: sandboxNetworkMode,
              ...(sandboxNetworkMode === 'whitelist' &&
              sandboxAllowedHosts.length > 0
                ? { allowedHosts: sandboxAllowedHosts }
                : {}),
              sshEnabled: false,
              gitRepoUrl: sandboxGitRepoUrl.trim() || undefined,
              gitBranch: sandboxGitBranch.trim() || undefined,
            }
          }
          createParams.autoProvision = true
        }

        await createMutation.mutateAsync(createParams)
        toast.success('Agent created')
      }
      onOpenChange(false)
    } catch (err) {
      toast.error(
        isEditing ? 'Failed to update agent' : 'Failed to create agent',
      )
    }
  }

  const toggleTool = (toolName: string) => {
    setSelectedTools((prev) =>
      prev.includes(toolName)
        ? prev.filter((t) => t !== toolName)
        : [...prev, toolName],
    )
  }

  const toggleRuntimeTool = (toolName: string) => {
    setDisabledRuntimeTools((prev) =>
      prev.includes(toolName)
        ? prev.filter((t) => t !== toolName)
        : [...prev, toolName],
    )
    setSelectedTools((prev) => prev.filter((t) => t !== toolName))
  }

  const addFallbackModel = () => {
    if (fallbackModelToAdd && !fallbackModels.includes(fallbackModelToAdd)) {
      setFallbackModels((prev) => [...prev, fallbackModelToAdd])
      setFallbackModelToAdd('')
    }
  }

  const removeFallbackModel = (m: string) => {
    setFallbackModels((prev) => prev.filter((fm) => fm !== m))
  }

  const moveFallbackModel = (index: number, direction: 'up' | 'down') => {
    setFallbackModels((prev) => {
      const next = [...prev]
      const swapIdx = direction === 'up' ? index - 1 : index + 1
      if (swapIdx < 0 || swapIdx >= next.length) return prev
      ;[next[index], next[swapIdx]] = [next[swapIdx], next[index]]
      return next
    })
  }

  const resolveAndInstallSkill = useCallback(async (spec: string) => {
    if (!spec.trim()) return
    setSkillResolving(true)
    try {
      const baseUrl = getApiBaseUrl()
      const res = await fetch(`${baseUrl}/v1/agents/skills/resolve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ spec: spec.trim() }),
      })
      if (!res.ok) {
        const err = await res
          .json()
          .catch(() => ({ error: 'Failed to resolve skill' }))
        toast.error(err.error || 'Failed to resolve skill')
        return
      }
      const data = await res.json()
      const resolved: InstalledSkill[] = data.skills ?? []
      if (resolved.length === 0) {
        toast.error('No skills found')
        return
      }
      setSkills((prev) => {
        const existing = new Set(prev.map((s) => s.source))
        const newSkills = resolved.filter((s) => !existing.has(s.source))
        if (newSkills.length === 0) {
          toast.info('Skill already installed')
          return prev
        }
        toast.success(
          `Installed ${newSkills.length} skill${newSkills.length > 1 ? 's' : ''}`,
        )
        return [...prev, ...newSkills]
      })
      setSkillSpecInput('')
    } catch {
      toast.error('Failed to resolve skill')
    } finally {
      setSkillResolving(false)
    }
  }, [])

  const removeSkill = useCallback((source: string) => {
    setSkills((prev) => prev.filter((s) => s.source !== source))
  }, [])

  const installedNames = useMemo(
    () => new Set(skills.map((s) => s.name.toLowerCase())),
    [skills],
  )

  // Debounce skill search
  useEffect(() => {
    if (skillSearch.length === 0) {
      setDebouncedSkillSearch('')
      return
    }
    if (skillSearch.length < 2) return
    const timer = setTimeout(
      () => setDebouncedSkillSearch(skillSearch),
      Math.max(150, 350 - 50 * skillSearch.length),
    )
    return () => clearTimeout(timer)
  }, [skillSearch])

  // Fetch registry browse results
  const fetchRegistryBrowse = useCallback(
    async (view: RegistryView, page: number, append: boolean) => {
      setRegistryLoading(true)
      try {
        const baseUrl = getApiBaseUrl()
        const res = await fetch(
          `${baseUrl}/v1/agents/skills/registry/browse?view=${view}&page=${page}`,
        )
        if (!res.ok) return
        const data = await res.json()
        const newSkills: RegistrySkill[] = data.skills ?? []
        setRegistrySkills((prev) =>
          append ? [...prev, ...newSkills] : newSkills,
        )
        setRegistryHasMore(data.hasMore ?? false)
        setRegistryPage(page)
      } catch {
        /* noop */
      } finally {
        setRegistryLoading(false)
      }
    },
    [],
  )

  // Fetch search results
  useEffect(() => {
    if (!debouncedSkillSearch || debouncedSkillSearch.length < 2) {
      setSearchResults([])
      return
    }
    let cancelled = false
    setSearchLoading(true)
    const baseUrl = getApiBaseUrl()
    fetch(
      `${baseUrl}/v1/agents/skills/registry/search?q=${encodeURIComponent(debouncedSkillSearch)}&limit=50`,
    )
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (cancelled || !data) return
        setSearchResults(data.skills ?? [])
        setSearchCount(data.count ?? 0)
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setSearchLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [debouncedSkillSearch])

  // Load initial browse on tab open / view change
  useEffect(() => {
    if (activeTab === 'skills' && !debouncedSkillSearch) {
      fetchRegistryBrowse(registryView, 0, false)
    }
  }, [activeTab, registryView, debouncedSkillSearch, fetchRegistryBrowse])

  // Infinite scroll for registry browse — listens on the sheet's scroll container
  const handleSkillsScroll = useCallback(() => {
    if (!registryHasMore || registryLoading || debouncedSkillSearch) return
    // The sheet itself is the scroll container (SheetContent with overflow-y-auto)
    const el = skillsScrollRef.current?.closest(
      '[data-radix-scroll-area-viewport], [class*="overflow-y-auto"]',
    ) as HTMLElement | null
    if (!el) return
    const { scrollTop, scrollHeight, clientHeight } = el
    if (scrollHeight - scrollTop - clientHeight < 300) {
      fetchRegistryBrowse(registryView, registryPage + 1, true)
    }
  }, [
    registryHasMore,
    registryLoading,
    debouncedSkillSearch,
    registryView,
    registryPage,
    fetchRegistryBrowse,
  ])

  useEffect(() => {
    if (activeTab !== 'skills') return
    // Find the sheet scroll container
    const sentinel = skillsScrollRef.current
    const el = sentinel?.closest(
      '[data-radix-scroll-area-viewport], [class*="overflow-y-auto"]',
    ) as HTMLElement | null
    if (!el) return
    el.addEventListener('scroll', handleSkillsScroll)
    return () => el.removeEventListener('scroll', handleSkillsScroll)
  }, [handleSkillsScroll, activeTab])

  const displayedRegistrySkills = debouncedSkillSearch
    ? searchResults
    : registrySkills
  const isRegistryLoading = debouncedSkillSearch
    ? searchLoading
    : registryLoading

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="right"
          className="w-full sm:max-w-[760px] h-[100vh] flex flex-col overflow-hidden"
        >
          <SheetHeader>
            <SheetTitle>{isEditing ? 'Edit Agent' : 'Create Agent'}</SheetTitle>
          </SheetHeader>

          <SheetBody className="py-4 flex-1 min-h-0 overflow-hidden flex flex-col">
            <form
              onSubmit={handleSubmit}
              className="flex flex-col flex-1 min-h-0 space-y-4"
            >
              <Tabs
                value={activeTab}
                onValueChange={setActiveTab}
                className="flex flex-col flex-1 min-h-0 space-y-4"
              >
                <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1 shrink-0">
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="basics"
                  >
                    Basics
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="tools"
                  >
                    Tools
                    {selectedTools.length > 0 && (
                      <span className="ml-1 text-[10px] bg-brand-secondary-600/40 text-brand-secondary-300 rounded-full px-1.5 py-0.5 leading-none tabular-nums">
                        {selectedTools.length}
                      </span>
                    )}
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="mcp"
                  >
                    MCP
                    {attachedMcpServerCount > 0 && (
                      <span className="ml-1 text-[10px] bg-brand-secondary-600/40 text-brand-secondary-300 rounded-full px-1.5 py-0.5 leading-none tabular-nums">
                        {attachedMcpServerCount}
                      </span>
                    )}
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="behavior"
                  >
                    Behavior
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="sandbox"
                  >
                    Sandbox
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="memory"
                  >
                    Memory
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                    value="skills"
                  >
                    Skills
                    {skills.length > 0 && (
                      <span className="ml-1 text-[10px] bg-brand-secondary-600/40 text-brand-secondary-300 rounded-full px-1.5 py-0.5 leading-none tabular-nums">
                        {skills.length}
                      </span>
                    )}
                  </TabsTrigger>
                  {lifecycleMode === AgentLifecycleMode.PERSISTENT && (
                    <TabsTrigger
                      className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                      value="identity"
                    >
                      Identity
                    </TabsTrigger>
                  )}
                  {isEditing && (
                    <TabsTrigger
                      className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 px-3 light:hover:text-brand-main-50"
                      value="peers"
                    >
                      Peers
                      {agentLinks.length > 0 && (
                        <span className="ml-1 text-[10px] bg-brand-secondary-600/40 text-brand-secondary-300 rounded-full px-1.5 py-0.5 leading-none tabular-nums">
                          {agentLinks.length}
                        </span>
                      )}
                    </TabsTrigger>
                  )}
                </TabsList>

                <TabsContent
                  value="basics"
                  forceMount
                  className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  <div className="space-y-2">
                    <Label htmlFor="name">Name</Label>
                    <Input
                      id="name"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="my-agent"
                      className={brandInputClass}
                    />
                  </div>

                  <div className="space-y-2">
                    <div className="flex items-center justify-between">
                      <Label htmlFor="model">Model</Label>
                      {gatewayModels.length > 0 && (
                        <a
                          href="/vault/llm-providers"
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-[10px] text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors"
                        >
                          + Connect provider
                        </a>
                      )}
                    </div>
                    {gatewayModels.length > 0 ? (
                      <Select value={model} onValueChange={setModel}>
                        <SelectTrigger
                          id="model"
                          className={brandSelectTriggerClass}
                        >
                          <SelectValue
                            placeholder={
                              modelsLoading
                                ? 'Loading models...'
                                : 'Select a model'
                            }
                          />
                        </SelectTrigger>
                        <SelectContent className={brandSelectContentClass}>
                          {gatewayModels.map((group) => (
                            <SelectGroup key={group.provider}>
                              <SelectLabel>{group.provider}</SelectLabel>
                              {group.models.map((m) => (
                                <SelectItem key={m} value={m}>
                                  {m}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          ))}
                        </SelectContent>
                      </Select>
                    ) : (
                      <div className="space-y-2">
                        <div className="rounded-lg border border-dashed border-brand-main-500 bg-brand-main-900/40 p-3 space-y-2.5">
                          <p className="text-xs text-white/50 light:text-black/50">
                            No LLM providers configured. Connect a provider to
                            select models.
                          </p>
                          <div className="flex flex-wrap gap-1.5">
                            {[
                              {
                                name: 'openai',
                                label: 'OpenAI',
                                icon: 'simple-icons:openai',
                                color: '#fff',
                              },
                              {
                                name: 'anthropic',
                                label: 'Anthropic',
                                icon: 'simple-icons:anthropic',
                                color: '#D4A574',
                              },
                              {
                                name: 'google',
                                label: 'Google',
                                icon: 'simple-icons:google',
                                color: '#4285F4',
                              },
                              {
                                name: 'azure-openai',
                                label: 'Azure OpenAI',
                                icon: 'simple-icons:microsoftazure',
                                color: '#0078D4',
                              },
                              {
                                name: 'aws-bedrock',
                                label: 'AWS Bedrock',
                                icon: 'simple-icons:amazonaws',
                                color: '#FF9900',
                              },
                              {
                                name: 'vertex-ai',
                                label: 'Vertex AI',
                                icon: 'simple-icons:googlecloud',
                                color: '#4285F4',
                              },
                              {
                                name: 'groq',
                                label: 'Groq',
                                icon: 'simple-icons:groq',
                                color: '#F55036',
                              },
                              {
                                name: 'together',
                                label: 'Together AI',
                                icon: 'simple-icons:togetherai',
                                color: '#6E56CF',
                              },
                              {
                                name: 'fireworks',
                                label: 'Fireworks AI',
                                icon: 'material-icon-theme:fireworks',
                                color: '#FF6A00',
                              },
                              {
                                name: 'xai',
                                label: 'xAI',
                                icon: 'simple-icons:xai',
                                color: '#FFFFFF',
                              },
                              {
                                name: 'perplexity',
                                label: 'Perplexity',
                                icon: 'simple-icons:perplexity',
                                color: '#20B8CD',
                              },
                              {
                                name: 'cerebras',
                                label: 'Cerebras',
                                icon: 'simple-icons:cerebras',
                                color: '#8B5CF6',
                              },
                              {
                                name: 'nvidia-nim',
                                label: 'NVIDIA NIM',
                                icon: 'simple-icons:nvidia',
                                color: '#76B900',
                              },
                              {
                                name: 'ollama',
                                label: 'Ollama',
                                icon: 'simple-icons:ollama',
                                color: '#fff',
                              },
                            ].map((p) => (
                              <button
                                key={p.name}
                                type="button"
                                onClick={() => {
                                  setConnectProviderName(p.name)
                                  setConnectProviderOpen(true)
                                }}
                                className="inline-flex items-center gap-1.5 rounded-md border border-brand-main-600 bg-brand-main-800/80 px-2.5 py-1.5 text-xs text-zinc-300 transition-colors hover:border-brand-secondary-500 hover:text-white light:hover:text-brand-main-50"
                              >
                                <Iconify.Icon
                                  icon={p.icon}
                                  width={13}
                                  style={{ color: p.color }}
                                />
                                {p.label}
                              </button>
                            ))}
                          </div>
                        </div>
                        <Input
                          id="model"
                          value={model}
                          onChange={(e) => setModel(e.target.value)}
                          placeholder="Or type a model name (e.g. gpt-4o)"
                          className={brandInputClass}
                        />
                      </div>
                    )}
                  </div>

                  {isOpenAIModel && (
                    <div className="space-y-2">
                      <Label>API Type</Label>
                      <Select value={apiType} onValueChange={setApiType}>
                        <SelectTrigger className={brandSelectTriggerClass}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent className={brandSelectContentClass}>
                          <SelectItem value="chat_completions">
                            Chat Completions
                          </SelectItem>
                          <SelectItem value="responses">
                            Responses API
                          </SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  )}

                  <div className="space-y-2">
                    <Label htmlFor="description">Description</Label>
                    <Input
                      id="description"
                      value={description}
                      onChange={(e) => setDescription(e.target.value)}
                      placeholder="Optional description"
                      className={brandInputClass}
                    />
                  </div>

                  <div className="space-y-2">
                    <Label>Lifecycle</Label>
                    <Select
                      value={
                        lifecycleMode === AgentLifecycleMode.PERSISTENT
                          ? 'persistent'
                          : 'ephemeral'
                      }
                      onValueChange={(v) => {
                        const next =
                          v === 'persistent'
                            ? AgentLifecycleMode.PERSISTENT
                            : AgentLifecycleMode.EPHEMERAL
                        setLifecycleMode(next)
                        // Persistent agents always require a sandbox
                        if (next === AgentLifecycleMode.PERSISTENT) {
                          setSandboxEnabled(true)
                        }
                      }}
                    >
                      <SelectTrigger className={brandSelectTriggerClass}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className={brandSelectContentClass}>
                        <SelectItem value="ephemeral">Ephemeral</SelectItem>
                        <SelectItem value="persistent">
                          Persistent (always-on)
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <p className="text-[11px] text-white/40 light:text-black/40">
                      {lifecycleMode === AgentLifecycleMode.PERSISTENT
                        ? 'Agent runs 24/7 with persistent sandbox, identity, and channel bindings.'
                        : 'Agent runs only during active sessions. Sandbox is destroyed after each session.'}
                    </p>
                  </div>

                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <Label>Mode</Label>
                      <Select
                        value={mode}
                        onValueChange={(value) => {
                          const nextMode = value as AgentModeValue
                          setMode(nextMode)
                          if (nextMode === 'subagent' && !hidden) {
                            setHidden(true)
                          }
                        }}
                      >
                        <SelectTrigger className={brandSelectTriggerClass}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent className={brandSelectContentClass}>
                          <SelectItem value="primary">Primary</SelectItem>
                          <SelectItem value="subagent">Subagent</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="mentionAlias">Mention Alias</Label>
                      <Input
                        id="mentionAlias"
                        value={mentionAlias}
                        onChange={(e) => setMentionAlias(e.target.value)}
                        placeholder="qa_bot"
                        className={brandInputClass}
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <Label>Color</Label>
                      <Popover>
                        <PopoverTrigger asChild>
                          <button
                            type="button"
                            className="flex h-9 w-full items-center gap-2 rounded-md border border-brand-main-600 bg-brand-main-900 px-3 text-sm text-zinc-200"
                          >
                            <span
                              className="h-4 w-4 shrink-0 rounded"
                              style={{
                                backgroundColor: color || DEFAULT_AGENT_COLOR,
                              }}
                            />
                            <span className="truncate">
                              {AGENT_COLORS.find(
                                (c) => c.hex === (color || DEFAULT_AGENT_COLOR),
                              )?.label ?? 'Select color'}
                            </span>
                          </button>
                        </PopoverTrigger>
                        <PopoverContent className="w-auto p-3" align="start">
                          <div className="grid grid-cols-6 gap-1.5">
                            {AGENT_COLORS.map((c) => (
                              <button
                                key={c.hex}
                                type="button"
                                title={c.label}
                                onClick={() => setColor(c.hex)}
                                className="h-7 w-7 rounded-md border-2 transition-all hover:scale-110"
                                style={{
                                  backgroundColor: c.hex,
                                  borderColor:
                                    (color || DEFAULT_AGENT_COLOR) === c.hex
                                      ? '#fff'
                                      : 'transparent',
                                }}
                              />
                            ))}
                          </div>
                        </PopoverContent>
                      </Popover>
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="workingDirectory">
                        Working Directory
                      </Label>
                      <Input
                        id="workingDirectory"
                        value={workingDirectory}
                        onChange={(e) => setWorkingDirectory(e.target.value)}
                        placeholder="/workspace"
                        className={brandInputClass}
                      />
                    </div>
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="systemPrompt">System Prompt</Label>
                    <Textarea
                      id="systemPrompt"
                      value={systemPrompt}
                      onChange={(e) => setSystemPrompt(e.target.value)}
                      placeholder="You are a helpful assistant..."
                      rows={5}
                      className={brandTextareaClass}
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <Label htmlFor="maxTurns">Max Turns</Label>
                      <Input
                        id="maxTurns"
                        type="number"
                        value={maxTurns}
                        onChange={(e) => setMaxTurns(Number(e.target.value))}
                        min={1}
                        max={100}
                        className={brandInputClass}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="maxToolCalls">
                        Max Tool Calls / Turn
                      </Label>
                      <Input
                        id="maxToolCalls"
                        type="number"
                        value={maxToolCallsPerTurn}
                        onChange={(e) =>
                          setMaxToolCallsPerTurn(Number(e.target.value))
                        }
                        min={1}
                        max={50}
                        className={brandInputClass}
                      />
                    </div>
                  </div>

                  <div className="flex items-center justify-between rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2">
                    <div className="space-y-0.5">
                      <Label className="text-xs text-white/75 light:text-black/75">
                        Hidden in default lists
                      </Label>
                      <p className="text-[11px] text-white/40 light:text-black/40">
                        Hide from primary agent selectors unless include-hidden
                        is enabled.
                      </p>
                    </div>
                    <Switch checked={hidden} onCheckedChange={setHidden} />
                  </div>
                </TabsContent>

                <TabsContent
                  value="tools"
                  forceMount
                  className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  <ToolsTabContent
                    selectedTools={selectedTools}
                    toggleTool={toggleTool}
                    setSelectedTools={setSelectedTools}
                    runtimeToolNames={runtimeToolNames}
                    disabledRuntimeTools={disabledRuntimeTools}
                    toggleRuntimeTool={toggleRuntimeTool}
                    sandboxEnabled={sandboxEnabled}
                    browserEnabled={browserEnabled}
                    memoryEnabled={memoryEnabled}
                    spawnEnabled={spawnEnabled}
                    functions={functions}
                  />
                </TabsContent>

                <TabsContent
                  value="mcp"
                  forceMount
                  className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  <McpTabContent
                    mcpServers={mcpServersList}
                    mcpTools={mcpToolEntries}
                    selectedTools={selectedTools}
                    toggleTool={toggleTool}
                    setSelectedTools={setSelectedTools}
                  />
                </TabsContent>

                <TabsContent
                  value="behavior"
                  forceMount
                  className="space-y-5 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Execution Policy
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div className="space-y-2">
                        <Label htmlFor="maxSteps">Max Steps</Label>
                        <Input
                          id="maxSteps"
                          type="number"
                          min={1}
                          value={maxStepsInput}
                          onChange={(e) => setMaxStepsInput(e.target.value)}
                          placeholder="unbounded"
                          className={brandInputClass}
                        />
                      </div>
                      <div className="space-y-2">
                        <Label>Task Permission Mode</Label>
                        <Select
                          value={taskPermission}
                          onValueChange={(value) =>
                            setTaskPermission(value as TaskPermissionValue)
                          }
                        >
                          <SelectTrigger className={brandSelectTriggerClass}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent className={brandSelectContentClass}>
                            <SelectItem value="ask">Ask</SelectItem>
                            <SelectItem value="always">Always</SelectItem>
                            <SelectItem value="deny">Deny</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                    </div>
                  </div>

                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Model Fallback
                    </div>
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable fallback
                      </Label>
                      <Switch
                        checked={fallbackEnabled}
                        onCheckedChange={setFallbackEnabled}
                      />
                    </div>
                    {fallbackEnabled && (
                      <div className="space-y-3">
                        <div className="flex gap-2">
                          {availableFallbackModels.length > 0 ? (
                            <Select
                              value={fallbackModelToAdd}
                              onValueChange={setFallbackModelToAdd}
                            >
                              <SelectTrigger
                                className={`flex-1 ${brandSelectTriggerClass}`}
                              >
                                <SelectValue placeholder="Select fallback model" />
                              </SelectTrigger>
                              <SelectContent
                                className={brandSelectContentClass}
                              >
                                {availableFallbackModels.map((group) => (
                                  <SelectGroup key={group.provider}>
                                    <SelectLabel>{group.provider}</SelectLabel>
                                    {group.models.map((m) => (
                                      <SelectItem key={m} value={m}>
                                        {m}
                                      </SelectItem>
                                    ))}
                                  </SelectGroup>
                                ))}
                              </SelectContent>
                            </Select>
                          ) : (
                            <Input
                              className={`flex-1 ${brandInputClass}`}
                              value={fallbackModelToAdd}
                              onChange={(e) =>
                                setFallbackModelToAdd(e.target.value)
                              }
                              placeholder="Enter fallback model name"
                            />
                          )}
                          <Button
                            type="button"
                            variant="outline"
                            onClick={addFallbackModel}
                            disabled={!fallbackModelToAdd}
                          >
                            Add
                          </Button>
                        </div>
                        {fallbackModels.length > 0 && (
                          <div className="space-y-1.5">
                            {fallbackModels.map((fm, i) => (
                              <div
                                key={fm}
                                className="flex items-center gap-2 bg-brand-main-800/50 rounded px-2.5 py-1.5"
                              >
                                <span className="text-xs text-white/40 w-4 text-center light:text-black/40">
                                  {i + 1}
                                </span>
                                <span className="text-xs text-white/80 flex-1 truncate light:text-black/80">
                                  {fm}
                                </span>
                                <button
                                  type="button"
                                  onClick={() => moveFallbackModel(i, 'up')}
                                  disabled={i === 0}
                                  className="p-0.5 text-white/30 hover:text-white/60 disabled:opacity-30 disabled:cursor-not-allowed light:text-black/30 light:hover:text-black/60"
                                >
                                  <ChevronUp className="w-3 h-3" />
                                </button>
                                <button
                                  type="button"
                                  onClick={() => moveFallbackModel(i, 'down')}
                                  disabled={i === fallbackModels.length - 1}
                                  className="p-0.5 text-white/30 hover:text-white/60 disabled:opacity-30 disabled:cursor-not-allowed light:text-black/30 light:hover:text-black/60"
                                >
                                  <ChevronDown className="w-3 h-3" />
                                </button>
                                <button
                                  type="button"
                                  onClick={() => removeFallbackModel(fm)}
                                  className="p-0.5 text-white/30 hover:text-red-400 light:text-black/30"
                                >
                                  <X className="w-3 h-3" />
                                </button>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}
                  </div>

                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Sub-Agent Spawning
                    </div>
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable spawning
                      </Label>
                      <Switch
                        checked={spawnEnabled}
                        onCheckedChange={setSpawnEnabled}
                      />
                    </div>
                    {spawnEnabled && (
                      <>
                        <div className="grid grid-cols-2 gap-3">
                          <div className="space-y-1 col-span-2">
                            <Label className="text-xs text-white/60 light:text-black/60">
                              Planning Mode
                            </Label>
                            <Select
                              value={spawnPlanningMode}
                              onValueChange={setSpawnPlanningMode}
                            >
                              <SelectTrigger className={brandInputClass}>
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="off">
                                  Off - Static spawn config
                                </SelectItem>
                                <SelectItem value="on">
                                  On - LLM plans sub-agents dynamically
                                </SelectItem>
                              </SelectContent>
                            </Select>
                            <p className="text-[10px] text-white/40 light:text-black/40">
                              When on, an LLM analyzes each task and dynamically
                              decomposes it into sub-agents before execution.
                            </p>
                          </div>
                          {spawnPlanningMode === 'on' && (
                            <div className="space-y-1 col-span-2">
                              <Label className="text-xs text-white/60 light:text-black/60">
                                Planner Model
                              </Label>
                              <Input
                                value={spawnPlannerModel}
                                onChange={(e) =>
                                  setSpawnPlannerModel(e.target.value)
                                }
                                placeholder="gpt-4o-mini"
                                className={brandInputClass}
                              />
                              <p className="text-[10px] text-white/40 light:text-black/40">
                                A fast, cheap model for task decomposition. The
                                planner only runs once per session.
                              </p>
                            </div>
                          )}
                          <div className="space-y-1">
                            <Label
                              htmlFor="spawnMaxDepth"
                              className="text-xs text-white/60 light:text-black/60"
                            >
                              Max Depth
                            </Label>
                            <Input
                              id="spawnMaxDepth"
                              type="number"
                              value={spawnMaxDepth}
                              onChange={(e) =>
                                setSpawnMaxDepth(Number(e.target.value))
                              }
                              min={1}
                              max={5}
                              className={brandInputClass}
                            />
                          </div>
                          <div className="space-y-1">
                            <Label
                              htmlFor="spawnMaxTotal"
                              className="text-xs text-white/60 light:text-black/60"
                            >
                              Max Total Spawns
                            </Label>
                            <Input
                              id="spawnMaxTotal"
                              type="number"
                              value={spawnMaxTotalSpawns}
                              onChange={(e) =>
                                setSpawnMaxTotalSpawns(Number(e.target.value))
                              }
                              min={1}
                              max={50}
                              className={brandInputClass}
                            />
                          </div>
                          <div className="space-y-1">
                            <Label
                              htmlFor="spawnTimeout"
                              className="text-xs text-white/60 light:text-black/60"
                            >
                              Child Timeout (sec)
                            </Label>
                            <Input
                              id="spawnTimeout"
                              type="number"
                              value={spawnChildTimeout}
                              onChange={(e) =>
                                setSpawnChildTimeout(Number(e.target.value))
                              }
                              min={30}
                              max={600}
                              className={brandInputClass}
                            />
                          </div>
                          <div className="space-y-1">
                            <Label
                              htmlFor="spawnTokenBudget"
                              className="text-xs text-white/60 light:text-black/60"
                            >
                              Token Budget
                            </Label>
                            <Input
                              id="spawnTokenBudget"
                              type="number"
                              value={spawnTokenBudget}
                              onChange={(e) =>
                                setSpawnTokenBudget(Number(e.target.value))
                              }
                              min={1000}
                              max={1000000}
                              step={1000}
                              className={brandInputClass}
                            />
                          </div>
                        </div>
                        <div className="flex items-center justify-between pt-2 border-t border-white/5 light:border-black/5">
                          <div>
                            <Label className="text-xs text-white/60 light:text-black/60">
                              Async Mode
                            </Label>
                            <p className="text-[10px] text-white/40 light:text-black/40">
                              Spawns return immediately and run in the
                              background. Use check_task to poll results.
                            </p>
                          </div>
                          <Switch
                            checked={spawnAsync}
                            onCheckedChange={setSpawnAsync}
                          />
                        </div>
                      </>
                    )}
                  </div>

                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Context Forking
                    </div>
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable forking
                      </Label>
                      <Switch
                        checked={forkEnabled}
                        onCheckedChange={setForkEnabled}
                      />
                    </div>
                    {forkEnabled && (
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1">
                          <Label
                            htmlFor="forkMaxConcurrent"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Max Concurrent
                          </Label>
                          <Input
                            id="forkMaxConcurrent"
                            type="number"
                            value={forkMaxConcurrent}
                            onChange={(e) =>
                              setForkMaxConcurrent(Number(e.target.value))
                            }
                            min={1}
                            max={10}
                            className={brandInputClass}
                          />
                        </div>
                        <div className="space-y-1">
                          <Label
                            htmlFor="forkTimeout"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Timeout (sec)
                          </Label>
                          <Input
                            id="forkTimeout"
                            type="number"
                            value={forkTimeout}
                            onChange={(e) =>
                              setForkTimeout(Number(e.target.value))
                            }
                            min={10}
                            max={600}
                            className={brandInputClass}
                          />
                        </div>
                      </div>
                    )}
                  </div>

                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Context Monitor
                    </div>
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable context compaction
                      </Label>
                      <Switch
                        checked={monitorEnabled}
                        onCheckedChange={setMonitorEnabled}
                      />
                    </div>
                    {monitorEnabled && (
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1">
                          <Label
                            htmlFor="monitorMaxTokens"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Max Context Tokens
                          </Label>
                          <Input
                            id="monitorMaxTokens"
                            type="number"
                            value={monitorMaxContextTokens}
                            onChange={(e) =>
                              setMonitorMaxContextTokens(Number(e.target.value))
                            }
                            min={4000}
                            max={1000000}
                            step={1000}
                            className={brandInputClass}
                          />
                        </div>
                        <div className="space-y-1">
                          <Label
                            htmlFor="monitorModel"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Summarization Model
                          </Label>
                          <Input
                            id="monitorModel"
                            value={monitorSummarizationModel}
                            onChange={(e) =>
                              setMonitorSummarizationModel(e.target.value)
                            }
                            placeholder="gpt-4o-mini"
                            className={brandInputClass}
                          />
                        </div>
                      </div>
                    )}
                  </div>

                  <div className="space-y-3">
                    <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                      Knowledge Digest
                    </div>
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable digest bulletin
                      </Label>
                      <Switch
                        checked={digestEnabled}
                        onCheckedChange={setDigestEnabled}
                      />
                    </div>
                    {digestEnabled && (
                      <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1">
                          <Label
                            htmlFor="digestInterval"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Refresh Interval (sec)
                          </Label>
                          <Input
                            id="digestInterval"
                            type="number"
                            value={digestRefreshInterval}
                            onChange={(e) =>
                              setDigestRefreshInterval(Number(e.target.value))
                            }
                            min={60}
                            max={86400}
                            step={60}
                            className={brandInputClass}
                          />
                        </div>
                        <div className="space-y-1">
                          <Label
                            htmlFor="digestModel"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Digest Model
                          </Label>
                          <Input
                            id="digestModel"
                            value={digestModel}
                            onChange={(e) => setDigestModel(e.target.value)}
                            placeholder="gpt-4o-mini"
                            className={brandInputClass}
                          />
                        </div>
                        <div className="space-y-1 col-span-2">
                          <Label
                            htmlFor="digestMaxSize"
                            className="text-xs text-white/60 light:text-black/60"
                          >
                            Max Bulletin Size (tokens)
                          </Label>
                          <Input
                            id="digestMaxSize"
                            type="number"
                            value={digestMaxBulletinSize}
                            onChange={(e) =>
                              setDigestMaxBulletinSize(Number(e.target.value))
                            }
                            min={500}
                            max={32000}
                            step={500}
                            className={brandInputClass}
                          />
                        </div>
                      </div>
                    )}
                  </div>
                </TabsContent>

                <TabsContent
                  value="sandbox"
                  forceMount
                  className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  {lifecycleMode === AgentLifecycleMode.PERSISTENT ? (
                    <p className="text-[11px] text-white/40 light:text-black/40">
                      Persistent agents always run with a sandbox. Choose which
                      sandbox to use below.
                    </p>
                  ) : (
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Enable sandbox
                      </Label>
                      <Switch
                        checked={sandboxEnabled}
                        onCheckedChange={setSandboxEnabled}
                      />
                    </div>
                  )}

                  {sandboxEnabled && (
                    <div className="space-y-4">
                      {managedCloud && !sandboxBillingEnabled && (
                        <div className="flex items-start justify-between gap-3 rounded border border-amber-500/25 bg-amber-500/5 p-3">
                          <div>
                            <p className="text-xs font-medium text-white/85 light:text-black/85">
                              Sandbox compute is paused
                            </p>
                            <p className="mt-1 text-[11px] text-white/50 light:text-black/55">
                              The organization starter credit is exhausted. Add
                              or restore billing to continue.
                            </p>
                          </div>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() =>
                              window.open(getCloudBillingUrl(), '_blank')
                            }
                          >
                            Open Billing
                          </Button>
                        </div>
                      )}
                      {managedCloud &&
                        sandboxBillingEnabled &&
                        sandboxMode === 'new' && (
                          <div className="rounded border border-brand-secondary-500/20 bg-brand-secondary-500/5 p-3 text-[11px] leading-relaxed text-white/55 light:text-black/60">
                            New organizations start with $5 of sandbox compute
                            credit. The selected VM consumes credit or billed
                            usage at approximately{' '}
                            {formatCost(computeCost.perHour)}/hr.
                          </div>
                        )}

                      {/* Sandbox mode: New vs Existing */}
                      <div className="space-y-1.5">
                        <Label className="text-xs text-white/60 light:text-black/60">
                          Sandbox source
                        </Label>
                        <div className="flex gap-1.5">
                          <button
                            type="button"
                            onClick={() => {
                              setSandboxMode('new')
                              setLinkedSessionId('')
                            }}
                            className={`inline-flex items-center gap-1.5 rounded-md border px-3 py-1 text-xs transition-colors ${
                              sandboxMode === 'new'
                                ? 'border-brand-secondary-500 bg-brand-secondary-500/15 text-brand-secondary-400'
                                : 'border-brand-main-700 bg-brand-main-900/60 text-zinc-400 hover:border-brand-main-500 hover:text-zinc-200'
                            }`}
                          >
                            New sandbox
                          </button>
                          <button
                            type="button"
                            onClick={() => setSandboxMode('existing')}
                            className={`inline-flex items-center gap-1.5 rounded-md border px-3 py-1 text-xs transition-colors ${
                              sandboxMode === 'existing'
                                ? 'border-brand-secondary-500 bg-brand-secondary-500/15 text-brand-secondary-400'
                                : 'border-brand-main-700 bg-brand-main-900/60 text-zinc-400 hover:border-brand-main-500 hover:text-zinc-200'
                            }`}
                          >
                            Use existing
                          </button>
                        </div>
                      </div>

                      {/* Existing sandbox selector */}
                      {sandboxMode === 'existing' && (
                        <div className="space-y-1">
                          <Label className="text-xs text-white/60 light:text-black/60">
                            Running sandbox
                          </Label>
                          <Select
                            value={linkedSessionId}
                            onValueChange={setLinkedSessionId}
                          >
                            <SelectTrigger className={brandSelectTriggerClass}>
                              <SelectValue placeholder="Select a running sandbox..." />
                            </SelectTrigger>
                            <SelectContent className={brandSelectContentClass}>
                              {(runningSandboxes?.instances ?? []).map(
                                (inst) => (
                                  <SelectItem
                                    key={inst.sessionId}
                                    value={inst.sessionId}
                                  >
                                    {inst.name || inst.sessionId} — {inst.image}
                                  </SelectItem>
                                ),
                              )}
                              {(!runningSandboxes?.instances ||
                                runningSandboxes.instances.length === 0) && (
                                // Sentinel value — Radix forbids "" but accepts any non-empty string.
                                <SelectItem value="__none__" disabled>
                                  No running sandboxes
                                </SelectItem>
                              )}
                            </SelectContent>
                          </Select>
                          <p className="text-[10px] text-white/40 mt-1 light:text-black/40">
                            The agent will share this sandbox instead of
                            creating a new one.
                          </p>
                        </div>
                      )}

                      {/* Template selector (only for new sandbox, hidden for persistent agents) */}
                      {sandboxMode === 'new' &&
                        lifecycleMode !== AgentLifecycleMode.PERSISTENT && (
                          <>
                            {/* Template selector */}
                            <div className="space-y-1.5">
                              <Label className="text-xs text-white/60 light:text-black/60">
                                Template
                              </Label>
                              <div className="flex flex-wrap gap-1.5">
                                {sandboxTemplates.map((tpl) => (
                                  <button
                                    key={tpl.slug}
                                    type="button"
                                    onClick={() => {
                                      setSelectedTemplateSlug(tpl.slug)
                                      setSandboxImage(tpl.image)
                                      setSandboxNetworkMode(tpl.networkMode)
                                      if (tpl.slug === 'browser')
                                        setBrowserEnabled(true)
                                    }}
                                    className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors ${
                                      selectedTemplateSlug === tpl.slug
                                        ? 'border-brand-secondary-500 bg-brand-secondary-500/15 text-brand-secondary-400'
                                        : 'border-brand-main-700 bg-brand-main-900/60 text-zinc-400 hover:border-brand-main-500 hover:text-zinc-200'
                                    }`}
                                  >
                                    <Iconify.Icon
                                      icon={tpl.icon}
                                      width={14}
                                      style={{ color: tpl.iconColor }}
                                    />
                                    {tpl.name}
                                  </button>
                                ))}
                                <button
                                  type="button"
                                  onClick={() => {
                                    setSelectedTemplateSlug('custom')
                                    setSandboxImage('')
                                  }}
                                  className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors ${
                                    selectedTemplateSlug === 'custom'
                                      ? 'border-brand-secondary-500 bg-brand-secondary-500/15 text-brand-secondary-400'
                                      : 'border-brand-main-700 bg-brand-main-900/60 text-zinc-400 hover:border-brand-main-500 hover:text-zinc-200'
                                  }`}
                                >
                                  <Iconify.Icon
                                    icon={CUSTOM_TEMPLATE_OPTION.icon}
                                    width={14}
                                    style={{
                                      color: CUSTOM_TEMPLATE_OPTION.iconColor,
                                    }}
                                  />
                                  Custom
                                </button>
                              </div>
                            </div>

                            {/* Custom image input */}
                            {selectedTemplateSlug === 'custom' && (
                              <div className="space-y-1">
                                <Label
                                  htmlFor="sandboxImage"
                                  className="text-xs text-white/60 light:text-black/60"
                                >
                                  Image
                                </Label>
                                <Input
                                  id="sandboxImage"
                                  value={sandboxImage}
                                  onChange={(e) =>
                                    setSandboxImage(e.target.value)
                                  }
                                  placeholder="everstack/sandbox:base"
                                  className={brandInputClass}
                                />
                              </div>
                            )}

                            {/* Machine profile selector */}
                            <div className="space-y-1">
                              <Label className="text-xs text-white/60 light:text-black/60">
                                Machine
                              </Label>
                              <Select
                                value={selectedMachineId}
                                onValueChange={(value) => {
                                  setSelectedMachineId(value)
                                  const profile = SANDBOX_MACHINE_PROFILES.find(
                                    (p) => p.id === value,
                                  )
                                  if (profile) {
                                    setSandboxCpuLimit(profile.cpu)
                                    setSandboxMemoryMb(profile.memoryMb)
                                    setSandboxDiskMb(profile.diskMb)
                                  }
                                }}
                              >
                                <SelectTrigger
                                  className={brandSelectTriggerClass}
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent
                                  className={brandSelectContentClass}
                                >
                                  {availableMachineProfiles.map((p) => (
                                    <SelectItem key={p.id} value={p.id}>
                                      {p.label}
                                    </SelectItem>
                                  ))}
                                  {!managedCloud && (
                                    <SelectItem value="custom">
                                      Custom
                                    </SelectItem>
                                  )}
                                </SelectContent>
                              </Select>
                              {managedCloud && (
                                <p className="text-[11px] text-white/40 light:text-black/45">
                                  Fixed sizes available on the {tier} plan.
                                  Every running second is billed.
                                </p>
                              )}
                            </div>

                            <div className="grid grid-cols-2 gap-3">
                              {!managedCloud && (
                                <>
                                  <div className="space-y-1">
                                    <Label
                                      htmlFor="sandboxCpu"
                                      className="text-xs text-white/60 light:text-black/60"
                                    >
                                      CPU Limit
                                    </Label>
                                    <Input
                                      id="sandboxCpu"
                                      type="number"
                                      value={sandboxCpuLimit}
                                      onChange={(e) => {
                                        const v = Number(e.target.value)
                                        setSandboxCpuLimit(v)
                                        setSelectedMachineId(
                                          matchMachineProfile(
                                            v,
                                            sandboxMemoryMb,
                                            sandboxDiskMb,
                                          ),
                                        )
                                      }}
                                      min={0.5}
                                      max={8}
                                      step={0.5}
                                      className={brandInputClass}
                                    />
                                  </div>
                                  <div className="space-y-1">
                                    <Label
                                      htmlFor="sandboxMemory"
                                      className="text-xs text-white/60 light:text-black/60"
                                    >
                                      Memory (MB)
                                    </Label>
                                    <Input
                                      id="sandboxMemory"
                                      type="number"
                                      value={sandboxMemoryMb}
                                      onChange={(e) => {
                                        const v = Number(e.target.value)
                                        setSandboxMemoryMb(v)
                                        setSelectedMachineId(
                                          matchMachineProfile(
                                            sandboxCpuLimit,
                                            v,
                                            sandboxDiskMb,
                                          ),
                                        )
                                      }}
                                      min={64}
                                      max={8192}
                                      step={64}
                                      className={brandInputClass}
                                    />
                                  </div>
                                  <div className="space-y-1">
                                    <Label
                                      htmlFor="sandboxDisk"
                                      className="text-xs text-white/60 light:text-black/60"
                                    >
                                      Disk (MB)
                                    </Label>
                                    <Input
                                      id="sandboxDisk"
                                      type="number"
                                      value={sandboxDiskMb}
                                      onChange={(e) => {
                                        const v = Number(e.target.value)
                                        setSandboxDiskMb(v)
                                        setSelectedMachineId(
                                          matchMachineProfile(
                                            sandboxCpuLimit,
                                            sandboxMemoryMb,
                                            v,
                                          ),
                                        )
                                      }}
                                      min={64}
                                      max={1048576}
                                      step={64}
                                      className={brandInputClass}
                                    />
                                  </div>
                                </>
                              )}
                              <div className="space-y-1">
                                <Label
                                  htmlFor="sandboxTimeout"
                                  className="text-xs text-white/60 light:text-black/60"
                                >
                                  Timeout (sec)
                                </Label>
                                <Input
                                  id="sandboxTimeout"
                                  type="number"
                                  value={sandboxTimeoutSeconds}
                                  onChange={(e) =>
                                    setSandboxTimeoutSeconds(
                                      Number(e.target.value),
                                    )
                                  }
                                  min={30}
                                  max={3600}
                                  className={brandInputClass}
                                />
                              </div>
                            </div>

                            {/* Compute cost estimate */}
                            {sandboxPricing.enabled && (
                              <p className="text-[11px] text-white/40 light:text-black/40">
                                Est. compute: {formatCost(computeCost.perHour)}
                                /hr · {formatCost(computeCost.perDay)}/day ·{' '}
                                {formatCost(computeCost.perMonth)}/mo
                              </p>
                            )}

                            <div className="space-y-1">
                              <Label
                                htmlFor="sandboxNetwork"
                                className="text-xs text-white/60 light:text-black/60"
                              >
                                Network Mode
                              </Label>
                              <Select
                                value={sandboxNetworkMode}
                                onValueChange={setSandboxNetworkMode}
                              >
                                <SelectTrigger
                                  id="sandboxNetwork"
                                  className={brandSelectTriggerClass}
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent
                                  className={brandSelectContentClass}
                                >
                                  <SelectItem value="deny">Deny</SelectItem>
                                  <SelectItem value="whitelist">
                                    Whitelist
                                  </SelectItem>
                                  <SelectItem value="allow">Allow</SelectItem>
                                </SelectContent>
                              </Select>
                            </div>

                            {sandboxNetworkMode === 'whitelist' && (
                              <div className="space-y-2">
                                <Label className="text-xs text-white/60 light:text-black/60">
                                  Allowed Hosts
                                </Label>
                                <p className="text-[11px] text-white/35 light:text-black/35">
                                  Domains the sandbox can reach. Package
                                  registries (npm, PyPI, cargo, Go) are always
                                  included by default.
                                </p>
                                <div className="flex gap-2">
                                  <Input
                                    value={sandboxHostInput}
                                    onChange={(e) =>
                                      setSandboxHostInput(e.target.value)
                                    }
                                    onKeyDown={(e) => {
                                      if (e.key === 'Enter') {
                                        e.preventDefault()
                                        const host = sandboxHostInput
                                          .trim()
                                          .toLowerCase()
                                        if (
                                          host &&
                                          !sandboxAllowedHosts.includes(host)
                                        ) {
                                          setSandboxAllowedHosts((prev) => [
                                            ...prev,
                                            host,
                                          ])
                                          setSandboxHostInput('')
                                        }
                                      }
                                    }}
                                    placeholder="e.g. api.example.com"
                                    className={`flex-1 ${brandInputClass}`}
                                  />
                                  <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => {
                                      const host = sandboxHostInput
                                        .trim()
                                        .toLowerCase()
                                      if (
                                        host &&
                                        !sandboxAllowedHosts.includes(host)
                                      ) {
                                        setSandboxAllowedHosts((prev) => [
                                          ...prev,
                                          host,
                                        ])
                                        setSandboxHostInput('')
                                      }
                                    }}
                                    disabled={!sandboxHostInput.trim()}
                                  >
                                    Add
                                  </Button>
                                </div>
                                {sandboxAllowedHosts.length > 0 && (
                                  <div className="flex flex-wrap gap-1.5">
                                    {sandboxAllowedHosts.map((host) => (
                                      <span
                                        key={host}
                                        className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-2 py-0.5 text-xs text-white/70 light:text-black/70"
                                      >
                                        {host}
                                        <button
                                          type="button"
                                          onClick={() =>
                                            setSandboxAllowedHosts((prev) =>
                                              prev.filter((h) => h !== host),
                                            )
                                          }
                                          className="text-white/30 hover:text-red-400 light:text-black/30"
                                        >
                                          <X className="w-3 h-3" />
                                        </button>
                                      </span>
                                    ))}
                                  </div>
                                )}
                              </div>
                            )}

                            <div className="space-y-3 rounded-md border border-brand-main-700/80 bg-brand-main-900/25 p-3">
                              <div className="text-[11px] font-medium uppercase tracking-wider text-white/50 light:text-black/50">
                                GitHub Source (optional)
                              </div>

                              <div className="space-y-1">
                                <Label
                                  htmlFor="sandboxGitInstallation"
                                  className="text-xs text-white/60 light:text-black/60"
                                >
                                  GitHub Installation
                                </Label>
                                <Select
                                  value={sandboxGitInstallationId || '__none__'}
                                  onValueChange={(value) => {
                                    setSandboxGitInstallationId(
                                      value === '__none__' ? '' : value,
                                    )
                                    setGitRepoSearch('')
                                  }}
                                >
                                  <SelectTrigger
                                    id="sandboxGitInstallation"
                                    className={brandSelectTriggerClass}
                                  >
                                    <SelectValue
                                      placeholder={
                                        gitHubInstallationsLoading
                                          ? 'Loading installations...'
                                          : 'Select installation'
                                      }
                                    />
                                  </SelectTrigger>
                                  <SelectContent
                                    className={brandSelectContentClass}
                                  >
                                    <SelectItem value="__none__">
                                      None
                                    </SelectItem>
                                    {gitHubInstallations.map((inst) => (
                                      <SelectItem
                                        key={inst.installationId}
                                        value={String(inst.installationId)}
                                      >
                                        {inst.accountLogin} ({inst.accountType})
                                      </SelectItem>
                                    ))}
                                    {sandboxGitInstallationId &&
                                      !selectedGitInstallationExists && (
                                        <SelectItem
                                          value={sandboxGitInstallationId}
                                        >
                                          Installation{' '}
                                          {sandboxGitInstallationId} (not
                                          linked)
                                        </SelectItem>
                                      )}
                                  </SelectContent>
                                </Select>
                                {gitHubInstallations.length === 0 &&
                                  !gitHubInstallationsLoading && (
                                    <p className="text-[11px] text-white/35 light:text-black/35">
                                      No linked GitHub installations found.
                                      Connect GitHub in Settings Integrations.
                                    </p>
                                  )}
                              </div>

                              {selectedGitInstallationId > 0 && (
                                <>
                                  {/* <div className="space-y-1">
                                                        <Label htmlFor="sandboxGitRepoSearch" className="text-xs text-white/60 light:text-black/60">Repository Search</Label>
                                                        <Input
                                                            id="sandboxGitRepoSearch"
                                                            className={brandInputClass}
                                                            value={gitRepoSearch}
                                                            onChange={(e) => setGitRepoSearch(e.target.value)}
                                                            placeholder="Filter repositories"
                                                        />
                                                    </div> */}

                                  <div className="space-y-1">
                                    <Label className="text-xs text-white/60 light:text-black/60">
                                      Repository (dropdown)
                                    </Label>
                                    <Select
                                      value={
                                        selectedRepoIsInList &&
                                        parsedSandboxRepo
                                          ? parsedSandboxRepo.fullName
                                          : '__custom__'
                                      }
                                      onValueChange={(value) => {
                                        if (value === '__custom__') return
                                        setSandboxGitRepoUrl(value)
                                        const selectedRepo = gitHubRepos.find(
                                          (repo) => repo.fullName === value,
                                        )
                                        if (selectedRepo && !sandboxGitBranch) {
                                          setSandboxGitBranch(
                                            selectedRepo.defaultBranch || '',
                                          )
                                        }
                                      }}
                                    >
                                      <SelectTrigger
                                        className={brandSelectTriggerClass}
                                      >
                                        <SelectValue
                                          placeholder={
                                            gitHubReposLoading
                                              ? 'Loading repositories...'
                                              : 'Select repository'
                                          }
                                        />
                                      </SelectTrigger>
                                      <SelectContent
                                        className={brandSelectContentClass}
                                      >
                                        <SelectItem value="__custom__">
                                          Custom / manual input
                                        </SelectItem>
                                        {gitHubRepos.map((repo) => (
                                          <SelectItem
                                            key={repo.id}
                                            value={repo.fullName}
                                          >
                                            {repo.fullName}
                                          </SelectItem>
                                        ))}
                                      </SelectContent>
                                    </Select>
                                  </div>
                                </>
                              )}

                              {/* <div className="space-y-1">
                                                <Label htmlFor="sandboxGitRepo" className="text-xs text-white/60 light:text-black/60">Repository (manual)</Label>
                                                <Input
                                                    id="sandboxGitRepo"
                                                    className={brandInputClass}
                                                    value={sandboxGitRepoUrl}
                                                    onChange={(e) => setSandboxGitRepoUrl(e.target.value)}
                                                    placeholder="owner/repo or https://github.com/owner/repo"
                                                />
                                            </div> */}

                              {selectedGitInstallationId > 0 &&
                                parsedSandboxRepo && (
                                  <div className="space-y-1">
                                    <Label className="text-xs text-white/60 light:text-black/60">
                                      Branch (dropdown)
                                    </Label>
                                    <Select
                                      value={sandboxGitBranch || '__default__'}
                                      onValueChange={(value) =>
                                        setSandboxGitBranch(
                                          value === '__default__' ? '' : value,
                                        )
                                      }
                                    >
                                      <SelectTrigger
                                        className={brandSelectTriggerClass}
                                      >
                                        <SelectValue
                                          placeholder={
                                            gitHubBranchesLoading
                                              ? 'Loading branches...'
                                              : 'Select branch'
                                          }
                                        />
                                      </SelectTrigger>
                                      <SelectContent
                                        className={brandSelectContentClass}
                                      >
                                        <SelectItem value="__default__">
                                          Default branch
                                        </SelectItem>
                                        {gitHubBranches.map((branch) => (
                                          <SelectItem
                                            key={branch.name}
                                            value={branch.name}
                                          >
                                            {branch.name}
                                          </SelectItem>
                                        ))}
                                      </SelectContent>
                                    </Select>
                                  </div>
                                )}

                              {/* <div className="space-y-1">
                                                <Label htmlFor="sandboxGitBranch" className="text-xs text-white/60 light:text-black/60">Branch (manual optional)</Label>
                                                <Input
                                                    id="sandboxGitBranch"
                                                    className={brandInputClass}
                                                    value={sandboxGitBranch}
                                                    onChange={(e) => setSandboxGitBranch(e.target.value)}
                                                    placeholder="main"
                                                />
                                            </div> */}
                            </div>
                          </>
                        )}

                      {/* Browser automation */}
                      <div className="space-y-3 pt-2 border-t border-brand-main-700/40">
                        <div className="flex items-center justify-between p-3 rounded-md bg-brand-main-800/50 border border-brand-main-700/60">
                          <div>
                            <div className="text-sm font-medium text-zinc-200">
                              Browser Automation
                            </div>
                            <div className="text-xs text-zinc-400">
                              Control Chromium via CDP — navigate, click, type,
                              screenshot
                            </div>
                          </div>
                          <Switch
                            checked={browserEnabled}
                            onCheckedChange={setBrowserEnabled}
                          />
                        </div>

                        {browserEnabled && (
                          <div className="flex items-center justify-between">
                            <Label className="text-xs text-white/60 light:text-black/60">
                              Headless mode
                            </Label>
                            <Switch
                              checked={browserHeadless}
                              onCheckedChange={setBrowserHeadless}
                            />
                          </div>
                        )}
                        {browserEnabled && !browserHeadless && (
                          <p className="text-[11px] text-zinc-500">
                            Headed mode enables live browser streaming for
                            real-time viewport viewing.
                          </p>
                        )}
                      </div>
                    </div>
                  )}
                </TabsContent>

                <TabsContent
                  value="memory"
                  forceMount
                  className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                >
                  <div className="flex items-center justify-between p-3 rounded-md bg-brand-main-800/50 border border-brand-main-700/60">
                    <div>
                      <div className="text-sm font-medium text-zinc-200">
                        Persistent Memory
                      </div>
                      <div className="text-xs text-zinc-400">
                        Auto-retrieve past context and extract facts across
                        sessions
                      </div>
                    </div>
                    <Switch
                      checked={memoryEnabled}
                      onCheckedChange={setMemoryEnabled}
                    />
                  </div>

                  {memoryEnabled && (
                    <div className="space-y-4">
                      <div className="space-y-2">
                        <Label>Scope</Label>
                        <Select
                          value={memoryScope}
                          onValueChange={setMemoryScope}
                        >
                          <SelectTrigger className={brandSelectTriggerClass}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent className={brandSelectContentClass}>
                            <SelectItem value="agent">
                              Agent (shared across all sessions)
                            </SelectItem>
                            <SelectItem value="user">
                              User (per end-user)
                            </SelectItem>
                            <SelectItem value="global">
                              Global (shared across all agents)
                            </SelectItem>
                          </SelectContent>
                        </Select>
                      </div>

                      <div className="flex items-center justify-between p-3 rounded-md bg-brand-main-800/50 border border-brand-main-700/60">
                        <div>
                          <div className="text-sm font-medium text-zinc-200">
                            Auto-Retrieve
                          </div>
                          <div className="text-xs text-zinc-400">
                            Inject relevant memories into the system prompt at
                            turn start
                          </div>
                        </div>
                        <Switch
                          checked={memoryAutoRetrieve}
                          onCheckedChange={setMemoryAutoRetrieve}
                        />
                      </div>

                      {memoryAutoRetrieve && (
                        <div className="space-y-2">
                          <Label>Top K (max memories to retrieve)</Label>
                          <Input
                            type="number"
                            value={memoryTopK}
                            onChange={(e) =>
                              setMemoryTopK(Number(e.target.value))
                            }
                            min={1}
                            max={50}
                            className={brandInputClass}
                          />
                        </div>
                      )}

                      <div className="flex items-center justify-between p-3 rounded-md bg-brand-main-800/50 border border-brand-main-700/60">
                        <div>
                          <div className="text-sm font-medium text-zinc-200">
                            Auto-Extract
                          </div>
                          <div className="text-xs text-zinc-400">
                            Extract facts and instructions from conversations at
                            turn end
                          </div>
                        </div>
                        <Switch
                          checked={memoryAutoExtract}
                          onCheckedChange={setMemoryAutoExtract}
                        />
                      </div>

                      {availableCollections.length > 0 && (
                        <div className="space-y-2">
                          <Label>Auto-RAG Collections</Label>
                          <p className="text-[11px] text-zinc-500">
                            Select vector memory collections to automatically
                            search during retrieval.
                          </p>
                          <div className="space-y-1.5">
                            {availableCollections.map((col) => {
                              const isSelected = memoryCollections.includes(
                                col.id,
                              )
                              return (
                                <label
                                  key={col.id}
                                  className={`flex items-center gap-2.5 p-2 rounded-md border cursor-pointer transition-colors ${
                                    isSelected
                                      ? 'bg-brand-secondary-600/15 border-brand-secondary-500/30'
                                      : 'bg-brand-main-800/30 border-brand-main-700/40 hover:border-brand-main-600/60'
                                  }`}
                                >
                                  <input
                                    type="checkbox"
                                    checked={isSelected}
                                    onChange={(e) => {
                                      if (e.target.checked) {
                                        setMemoryCollections((prev) => [
                                          ...prev,
                                          col.id,
                                        ])
                                      } else {
                                        setMemoryCollections((prev) =>
                                          prev.filter((c) => c !== col.id),
                                        )
                                      }
                                    }}
                                    className="accent-brand-secondary-500"
                                  />
                                  <div className="flex-1 min-w-0">
                                    <div className="text-xs text-zinc-200 truncate">
                                      {col.name}
                                    </div>
                                    {col.description && (
                                      <div className="text-[10px] text-zinc-500 truncate">
                                        {col.description}
                                      </div>
                                    )}
                                  </div>
                                  <span className="text-[10px] text-zinc-600 tabular-nums shrink-0">
                                    {col.documentCount} docs
                                  </span>
                                </label>
                              )
                            })}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </TabsContent>

                <TabsContent
                  value="skills"
                  forceMount
                  className="flex flex-col gap-4 mt-0 flex-1 min-h-0 data-[state=inactive]:hidden"
                >
                  {/* Installed skills */}
                  {skills.length > 0 && (
                    <div className="space-y-2">
                      <Label className="text-xs text-zinc-400 uppercase tracking-wide">
                        Installed ({skills.length})
                      </Label>
                      <div className="space-y-1.5">
                        {skills.map((skill) => (
                          <div
                            key={skill.source}
                            className="flex items-center gap-3 bg-brand-main-900/60 border border-brand-main-600 rounded-md px-3 py-2"
                          >
                            <Sparkles className="w-4 h-4 text-brand-secondary-400 shrink-0" />
                            <div className="flex-1 min-w-0">
                              <div className="text-sm text-zinc-200 font-medium truncate">
                                {skill.name}
                              </div>
                              {skill.description && (
                                <div className="text-xs text-zinc-500 truncate">
                                  {skill.description}
                                </div>
                              )}
                              <div className="text-[10px] text-zinc-600 truncate">
                                {skill.source}
                              </div>
                            </div>
                            <button
                              type="button"
                              onClick={() => removeSkill(skill.source)}
                              className="text-zinc-500 hover:text-red-400 transition-colors shrink-0"
                            >
                              <X className="w-3.5 h-3.5" />
                            </button>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Browse registry */}
                  <div className="space-y-3 flex-1 min-h-0 overflow-y-auto scrollbar-macos">
                    <div className="flex items-center justify-between">
                      <Label className="text-xs text-zinc-400 uppercase tracking-wide">
                        Browse Registry
                      </Label>
                      <a
                        href="https://skills.sh"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-[11px] text-zinc-500 hover:text-brand-secondary-400 transition-colors"
                      >
                        skills.sh
                      </a>
                    </div>

                    {/* Search bar + view toggles */}
                    <div className="flex gap-2 items-center">
                      <div className="relative flex-1">
                        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500" />
                        <Input
                          value={skillSearch}
                          onChange={(e) => setSkillSearch(e.target.value)}
                          placeholder="Search skills..."
                          className={`${brandInputClass} pl-8 text-sm`}
                        />
                      </div>
                      {!debouncedSkillSearch && (
                        <div className="flex items-center gap-0.5 shrink-0">
                          {REGISTRY_VIEWS.map((v) => (
                            <button
                              key={v.key}
                              type="button"
                              onClick={() => setRegistryView(v.key)}
                              className={`px-2 py-1 text-[11px] font-medium rounded transition-colors ${
                                registryView === v.key
                                  ? 'bg-brand-main-700/60 text-zinc-200'
                                  : 'text-zinc-500 hover:text-zinc-300'
                              }`}
                            >
                              {v.label}
                            </button>
                          ))}
                        </div>
                      )}
                    </div>

                    {/* Results header */}
                    <div className="flex items-center justify-between">
                      <span className="text-xs text-zinc-500">
                        {debouncedSkillSearch
                          ? `Results for "${debouncedSkillSearch}"`
                          : `${REGISTRY_VIEWS.find((v) => v.key === registryView)?.label ?? ''} Skills`}
                      </span>
                      <span className="text-[10px] text-zinc-600 tabular-nums">
                        {debouncedSkillSearch && searchCount > 0
                          ? `${searchCount} results`
                          : displayedRegistrySkills.length > 0
                            ? `${displayedRegistrySkills.length} skills`
                            : ''}
                      </span>
                    </div>

                    {/* Loading state */}
                    {isRegistryLoading &&
                      displayedRegistrySkills.length === 0 && (
                        <div className="text-center py-6">
                          <Loader2 className="w-4 h-4 animate-spin text-zinc-500 mx-auto" />
                          <p className="text-xs text-zinc-500 mt-2">
                            Loading skills from registry...
                          </p>
                        </div>
                      )}

                    {/* Empty search */}
                    {!isRegistryLoading &&
                      displayedRegistrySkills.length === 0 &&
                      debouncedSkillSearch && (
                        <div className="text-center py-6">
                          <p className="text-xs text-zinc-500">
                            No skills found matching &ldquo;
                            {debouncedSkillSearch}&rdquo;
                          </p>
                        </div>
                      )}

                    {/* Skills grid */}
                    <div
                      ref={skillsScrollRef}
                      className="grid grid-cols-2 gap-1.5"
                    >
                      {displayedRegistrySkills.map((rs) => {
                        const spec = registryInstallSpec(rs)
                        const isInstalled = installedNames.has(
                          rs.name.toLowerCase(),
                        )
                        return (
                          <div
                            key={`${rs.source}/${rs.skillId}`}
                            className="flex flex-col px-3 py-2 rounded-md border bg-brand-main-900/40 border-brand-main-600 hover:border-brand-secondary-500/50 hover:bg-brand-main-800/60 transition-colors"
                          >
                            <div className="flex items-center gap-1.5">
                              <span className="text-xs font-medium text-zinc-200 truncate flex-1">
                                {rs.name}
                              </span>
                              {isInstalled && (
                                <span className="text-[9px] text-green-400 shrink-0">
                                  installed
                                </span>
                              )}
                            </div>
                            <div className="text-[10px] text-zinc-600 truncate mt-0.5">
                              {rs.source}
                            </div>
                            <div className="flex items-center justify-between mt-1.5 pt-1.5 border-t border-brand-main-700/40">
                              <span className="text-[10px] text-zinc-500 tabular-nums">
                                {formatInstalls(rs.installs)} installs
                              </span>
                              <button
                                type="button"
                                disabled={skillResolving || isInstalled}
                                onClick={() => resolveAndInstallSkill(spec)}
                                className={`text-[10px] px-1.5 py-0.5 rounded transition-colors ${
                                  isInstalled
                                    ? 'text-zinc-600 cursor-default'
                                    : 'text-brand-secondary-400 hover:text-brand-secondary-300 hover:bg-brand-secondary-600/20'
                                }`}
                              >
                                {isInstalled ? 'Installed' : 'Install'}
                              </button>
                            </div>
                          </div>
                        )
                      })}
                      {/* Infinite scroll loading indicator */}
                      {!debouncedSkillSearch &&
                        registryLoading &&
                        displayedRegistrySkills.length > 0 && (
                          <div className="col-span-2 text-center py-3">
                            <Loader2 className="w-3.5 h-3.5 animate-spin text-zinc-500 mx-auto" />
                            <span className="text-[10px] text-zinc-600 mt-1 block">
                              Loading more...
                            </span>
                          </div>
                        )}
                    </div>

                    {/* Install from GitHub (collapsible) */}
                    <div className="border-t border-brand-main-700/40 pt-3">
                      <button
                        type="button"
                        onClick={() => setCustomSkillOpen((v) => !v)}
                        className="flex items-center gap-1 text-[11px] text-zinc-500 hover:text-zinc-300 transition-colors"
                      >
                        <BookOpen className="w-3 h-3" />
                        Install from GitHub
                        <ChevronRight
                          className={`w-3 h-3 transition-transform ${customSkillOpen ? 'rotate-90' : ''}`}
                        />
                      </button>
                      {customSkillOpen && (
                        <div className="flex gap-2 mt-2">
                          <Input
                            value={skillSpecInput}
                            onChange={(e) => setSkillSpecInput(e.target.value)}
                            placeholder="owner/repo or owner/repo/skill-name"
                            className={`${brandInputClass} flex-1 text-sm`}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                resolveAndInstallSkill(skillSpecInput)
                              }
                            }}
                            disabled={skillResolving}
                          />
                          <Button
                            type="button"
                            onClick={() =>
                              resolveAndInstallSkill(skillSpecInput)
                            }
                            disabled={skillResolving || !skillSpecInput.trim()}
                          >
                            {skillResolving ? (
                              <Loader2 className="w-3.5 h-3.5 animate-spin" />
                            ) : (
                              'Install'
                            )}
                          </Button>
                        </div>
                      )}
                    </div>
                  </div>
                </TabsContent>

                {lifecycleMode === AgentLifecycleMode.PERSISTENT && (
                  <TabsContent
                    value="identity"
                    forceMount
                    className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                  >
                    <p className="text-xs text-zinc-400">
                      Identity files define who this agent is. These markdown
                      documents shape the agent's personality, role, and
                      behavior.
                    </p>
                    <div className="space-y-2">
                      <Label htmlFor="form-soul">SOUL.md</Label>
                      <Textarea
                        id="form-soul"
                        value={soulMd}
                        onChange={(e) => setSoulMd(e.target.value)}
                        placeholder="Core personality and values..."
                        className={`${brandTextareaClass} h-24 font-mono text-xs`}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="form-identity">IDENTITY.md</Label>
                      <Textarea
                        id="form-identity"
                        value={identityMd}
                        onChange={(e) => setIdentityMd(e.target.value)}
                        placeholder="Who this agent is..."
                        className={`${brandTextareaClass} h-24 font-mono text-xs`}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="form-user">USER.md</Label>
                      <Textarea
                        id="form-user"
                        value={userMd}
                        onChange={(e) => setUserMd(e.target.value)}
                        placeholder="User preferences and context..."
                        className={`${brandTextareaClass} h-24 font-mono text-xs`}
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="form-role">ROLE.md</Label>
                      <Textarea
                        id="form-role"
                        value={roleMd}
                        onChange={(e) => setRoleMd(e.target.value)}
                        placeholder="Role-specific instructions..."
                        className={`${brandTextareaClass} h-24 font-mono text-xs`}
                      />
                    </div>
                  </TabsContent>
                )}

                {isEditing && (
                  <TabsContent
                    value="peers"
                    forceMount
                    className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos data-[state=inactive]:hidden"
                  >
                    <p className="text-xs text-zinc-400">
                      Connect this agent to other persistent agents to enable
                      cross-agent messaging, task delegation, and collaboration.
                    </p>

                    {/* Add new link */}
                    <div className="flex items-end gap-2">
                      <div className="flex-1 space-y-1">
                        <Label className="text-xs">Target Agent</Label>
                        <Select
                          value={peerAgentId}
                          onValueChange={setPeerAgentId}
                        >
                          <SelectTrigger className={brandSelectTriggerClass}>
                            <SelectValue placeholder="Select an agent..." />
                          </SelectTrigger>
                          <SelectContent className={brandSelectContentClass}>
                            {availablePeerAgents.length === 0 ? (
                              <SelectItem value="_none" disabled>
                                No agents available
                              </SelectItem>
                            ) : (
                              availablePeerAgents.map((a) => (
                                <SelectItem key={a.id} value={a.id}>
                                  {a.name}
                                </SelectItem>
                              ))
                            )}
                          </SelectContent>
                        </Select>
                      </div>
                      <div className="w-36 space-y-1">
                        <Label className="text-xs">Relationship</Label>
                        <Select
                          value={peerLinkType}
                          onValueChange={(v) =>
                            setPeerLinkType(
                              v as 'peer' | 'supervisor' | 'subordinate',
                            )
                          }
                        >
                          <SelectTrigger className={brandSelectTriggerClass}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent className={brandSelectContentClass}>
                            <SelectItem value="peer">Peer</SelectItem>
                            <SelectItem value="supervisor">
                              Supervisor
                            </SelectItem>
                            <SelectItem value="subordinate">
                              Subordinate
                            </SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                      <Button
                        type="button"
                        size="sm"
                        disabled={!peerAgentId || createLinkMutation.isPending}
                        onClick={() => {
                          if (!agent?.id || !peerAgentId) return
                          createLinkMutation.mutate(
                            {
                              sourceAgentId: agent.id,
                              targetId: peerAgentId,
                              linkType: linkTypeToEnum(peerLinkType),
                            },
                            {
                              onSuccess: () => {
                                setPeerAgentId('')
                                toast.success('Agent linked')
                              },
                              onError: (err) => toast.error(err.message),
                            },
                          )
                        }}
                      >
                        {createLinkMutation.isPending ? (
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          'Link'
                        )}
                      </Button>
                    </div>

                    {/* Existing links */}
                    {agentLinks.length === 0 ? (
                      <div className="text-center py-8 text-zinc-500 text-sm">
                        No connected agents yet. Add a link above to enable
                        cross-agent communication.
                      </div>
                    ) : (
                      <div className="space-y-2">
                        {agentLinks.map((link) => (
                          <div
                            key={link.id}
                            className="flex items-center justify-between gap-3 px-3 py-2 rounded-md bg-brand-main-800/40 border border-brand-main-600/40"
                          >
                            <div className="flex items-center gap-2 min-w-0">
                              <span className="text-sm text-zinc-200 truncate">
                                {link.targetName || link.targetId}
                              </span>
                              <span className="text-[10px] uppercase tracking-wider text-zinc-500 bg-brand-main-700/60 rounded px-1.5 py-0.5">
                                {link.linkType === AgentLinkType.SUPERVISOR
                                  ? 'supervisor'
                                  : link.linkType === AgentLinkType.SUBORDINATE
                                    ? 'subordinate'
                                    : link.linkType ===
                                        AgentLinkType.COLLABORATOR
                                      ? 'collaborator'
                                      : 'peer'}
                              </span>
                            </div>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 w-7 p-0 text-zinc-500 hover:text-red-400"
                              disabled={deleteLinkMutation.isPending}
                              onClick={() => {
                                deleteLinkMutation.mutate(link.id, {
                                  onSuccess: () =>
                                    toast.success('Link removed'),
                                  onError: (err) => toast.error(err.message),
                                })
                              }}
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </Button>
                          </div>
                        ))}
                      </div>
                    )}
                  </TabsContent>
                )}
              </Tabs>

              {isEditing && (
                <div className="flex items-center gap-2 pt-1">
                  <Switch checked={enabled} onCheckedChange={setEnabled} />
                  <Label>Enabled</Label>
                </div>
              )}

              <div className="flex gap-3 pt-3 pr-4 border-t border-brand-main-700/60 shrink-0 w-full">
                <Button
                  type="button"
                  variant="outline"
                  className="w-1/2"
                  onClick={() => onOpenChange(false)}
                  disabled={isPending}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  className="w-1/2"
                  disabled={
                    isPending ||
                    (managedCloud &&
                      sandboxEnabled &&
                      sandboxMode === 'new' &&
                      lifecycleMode === AgentLifecycleMode.PERSISTENT &&
                      !sandboxBillingEnabled)
                  }
                >
                  {isPending
                    ? isEditing
                      ? 'Updating...'
                      : 'Creating...'
                    : isEditing
                      ? 'Update'
                      : 'Create'}
                </Button>
              </div>
            </form>
          </SheetBody>
        </SheetContent>
      </Sheet>

      {/* Inline provider connect sheet — opened from the model selector empty state */}
      {connectProviderOpen && (
        <ConfigureProviderSheet
          open={connectProviderOpen}
          onOpenChange={(isOpen) => {
            setConnectProviderOpen(isOpen)
            if (!isOpen) setConnectProviderName(null)
          }}
          providerName={connectProviderName}
        />
      )}
    </>
  )
}
