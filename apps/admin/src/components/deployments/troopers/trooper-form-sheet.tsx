import { useState, useMemo, useEffect, useCallback, useRef } from 'react'
import {
  X,
  Search,
  ChevronRight,
  Loader2,
  BookOpen,
  Sparkles,
} from 'lucide-react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useCreateTrooper } from '@/hooks/deployments/use-troopers'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { useFunctions } from '@/hooks/deployments/use-functions'
import {
  useGitHubInstallations,
  useGitHubRepositories,
  useGitHubBranches,
} from '@/hooks/integrations/use-github'
import { getApiBaseUrl } from '@/lib/api-url'
import { resolveSandboxPricing } from '@/lib/sandbox-pricing'

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
  'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandSelectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandInputClass = 'bg-brand-main-900 border-brand-main-600'
const brandTextareaClass =
  'bg-brand-main-900 border-brand-main-600 focus-visible:border-brand-secondary-500 focus-visible:ring-brand-secondary-500 focus-visible:ring-[1px]'

const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3'

const TROOPER_VM_IMAGE = 'ubuntu:22.04'

const sandboxPricing = resolveSandboxPricing(undefined)

function formatCurrency(amount: number, currency = 'USD'): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(amount)
}

const DEFAULT_TROOPER_COLOR = '#64748b'

const TROOPER_COLORS = [
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

function parseGitHubRepo(
  value: string,
): { owner: string; repo: string; fullName: string } | null {
  const raw = value.trim()
  if (!raw) return null
  let normalized = raw
  const githubPrefix = 'https://github.com/'
  if (normalized.startsWith(githubPrefix))
    normalized = normalized.slice(githubPrefix.length)
  normalized = normalized.replace(/\.git$/, '').replace(/^\/+|\/+$/g, '')
  const parts = normalized.split('/')
  if (parts.length < 2) return null
  const owner = parts[0]
  const repo = parts[1]
  if (!owner || !repo) return null
  return { owner, repo, fullName: `${owner}/${repo}` }
}

interface TrooperFormSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function TrooperFormSheet({
  open,
  onOpenChange,
}: TrooperFormSheetProps) {
  const createMutation = useCreateTrooper()
  const { data: gatewayModels = [], isLoading: modelsLoading } =
    useGatewayModels()
  const { data: functions = [] } = useFunctions()
  const {
    data: gitHubInstallations = [],
    isLoading: gitHubInstallationsLoading,
  } = useGitHubInstallations()
  const [activeTab, setActiveTab] = useState('general')

  // General
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [model, setModel] = useState('')
  const [color, setColor] = useState(DEFAULT_TROOPER_COLOR)
  const [icon, setIcon] = useState('')

  // Identity
  const [soulMd, setSoulMd] = useState('')
  const [identityMd, setIdentityMd] = useState('')
  const [userMd, setUserMd] = useState('')
  const [roleMd, setRoleMd] = useState('')

  // Agent
  const [systemPrompt, setSystemPrompt] = useState('')
  const [selectedTools, setSelectedTools] = useState<string[]>([])
  const [maxTurns, setMaxTurns] = useState(0)
  const [maxToolCallsPerTurn, setMaxToolCallsPerTurn] = useState(10)

  // Skills
  const [skills, setSkills] = useState<InstalledSkill[]>([])
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

  // Sandbox
  const [cpuLimit, setCpuLimit] = useState(1.0)
  const [memoryMb, setMemoryMb] = useState(512)
  const [diskMb, setDiskMb] = useState(2048)
  const [networkMode, setNetworkMode] = useState('allow')
  const [sshEnabled, setSshEnabled] = useState(false)
  const [gitRepoUrl, setGitRepoUrl] = useState('')
  const [gitBranch, setGitBranch] = useState('')
  const [gitInstallationId, setGitInstallationId] = useState('')

  // Advanced
  const [maxConcurrentWorkers, setMaxConcurrentWorkers] = useState(3)
  const [autoProvision, setAutoProvision] = useState(false)

  const isPending = createMutation.isPending
  const installedNames = useMemo(
    () => new Set(skills.map((s) => s.name.toLowerCase())),
    [skills],
  )

  const estimatedPricing = useMemo(() => {
    const memoryGb = memoryMb / 1024
    const diskGb = diskMb / 1024
    const hourlyRaw =
      cpuLimit * sandboxPricing.cpuPerHourUsd +
      memoryGb * sandboxPricing.memoryGbPerHourUsd +
      diskGb * sandboxPricing.diskGbPerHourUsd +
      sandboxPricing.platformFeePerHourUsd
    const hourly = sandboxPricing.enabled ? hourlyRaw : 0
    const daily = hourly * 24
    const monthly = daily * 30
    return { hourly, daily, monthly }
  }, [cpuLimit, memoryMb, diskMb])

  const selectedGitInstallationId = Number(gitInstallationId)
  const parsedRepo = useMemo(() => parseGitHubRepo(gitRepoUrl), [gitRepoUrl])
  const { data: gitHubReposData, isLoading: gitHubReposLoading } =
    useGitHubRepositories(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      { page: 1, perPage: 50 },
    )
  const { data: gitHubBranches = [], isLoading: gitHubBranchesLoading } =
    useGitHubBranches(
      Number.isFinite(selectedGitInstallationId)
        ? selectedGitInstallationId
        : 0,
      parsedRepo?.owner ?? '',
      parsedRepo?.repo ?? '',
      { page: 1, perPage: 100 },
    )
  const gitHubRepos = gitHubReposData?.repositories ?? []
  const selectedRepoIsInList = useMemo(
    () =>
      !!parsedRepo &&
      gitHubRepos.some((repo) => repo.fullName === parsedRepo.fullName),
    [gitHubRepos, parsedRepo],
  )
  const selectedGitInstallationExists = useMemo(
    () =>
      gitHubInstallations.some(
        (inst) => String(inst.installationId) === gitInstallationId,
      ),
    [gitHubInstallations, gitInstallationId],
  )

  const toggleTool = (toolName: string) => {
    setSelectedTools((prev) =>
      prev.includes(toolName)
        ? prev.filter((t) => t !== toolName)
        : [...prev, toolName],
    )
  }

  // ─── Skills logic ────────────────────────────────────────────────
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

  // Fetch registry browse
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

  // Infinite scroll
  const handleSkillsScroll = useCallback(() => {
    if (!registryHasMore || registryLoading || debouncedSkillSearch) return
    const el = skillsScrollRef.current?.closest(
      '[data-radix-scroll-area-viewport], [class*="overflow-y-auto"]',
    ) as HTMLElement | null
    if (!el) return
    const { scrollTop, scrollHeight, clientHeight } = el
    if (scrollHeight - scrollTop - clientHeight < 300)
      fetchRegistryBrowse(registryView, registryPage + 1, true)
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

  // ─── Reset ───────────────────────────────────────────────────────
  useEffect(() => {
    if (!open) return
    setActiveTab('general')
    setName('')
    setDescription('')
    setModel('')
    setColor(DEFAULT_TROOPER_COLOR)
    setIcon('')
    setSoulMd('')
    setIdentityMd('')
    setUserMd('')
    setRoleMd('')
    setSystemPrompt('')
    setSelectedTools([])
    setMaxTurns(0)
    setMaxToolCallsPerTurn(10)
    setSkills([])
    setSkillSpecInput('')
    setSkillSearch('')
    setDebouncedSkillSearch('')
    setRegistryView('all-time')
    setRegistrySkills([])
    setRegistryPage(0)
    setSearchResults([])
    setCustomSkillOpen(false)
    setCpuLimit(1.0)
    setMemoryMb(512)
    setDiskMb(2048)
    setNetworkMode('allow')
    setSshEnabled(false)
    setGitRepoUrl('')
    setGitBranch('')
    setGitInstallationId('')
    setMaxConcurrentWorkers(3)
    setAutoProvision(false)
  }, [open])

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim() || !model.trim()) {
      toast.error('Name and model are required')
      return
    }

    // Build skills config
    const config: Record<string, any> = {}
    if (skills.length > 0) {
      config.skills = skills
    }

    createMutation.mutate(
      {
        name: name.trim(),
        description: description.trim() || undefined,
        model: model.trim(),
        systemPrompt: systemPrompt.trim() || undefined,
        tools: selectedTools.length > 0 ? selectedTools : undefined,
        maxTurns: maxTurns || undefined,
        maxToolCallsPerTurn: maxToolCallsPerTurn || undefined,
        identity: {
          soulMd: soulMd.trim(),
          identityMd: identityMd.trim(),
          userMd: userMd.trim(),
          roleMd: roleMd.trim(),
        },
        sandbox: {
          image: TROOPER_VM_IMAGE,
          cpuLimit,
          memoryMb,
          diskMb,
          networkMode,
          sshEnabled,
          gitRepoUrl: gitRepoUrl.trim() || undefined,
          gitBranch: gitBranch.trim() || undefined,
          ...(selectedGitInstallationId > 0
            ? { gitInstallationId: selectedGitInstallationId }
            : {}),
        } as any,
        workers: { maxConcurrentWorkers } as any,
        color: color.trim() || undefined,
        icon: icon.trim() || undefined,
        autoProvision,
        ...(Object.keys(config).length > 0 ? { config } : {}),
      } as any,
      {
        onSuccess: () => {
          toast.success('Instance created')
          onOpenChange(false)
        },
        onError: (e) => toast.error(`Failed: ${e.message}`),
      },
    )
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-[600px] h-[100vh] flex flex-col overflow-hidden"
      >
        <SheetHeader>
          <SheetTitle>Create Instance</SheetTitle>
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
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="general">
                  General
                </TabsTrigger>
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="identity">
                  Identity
                </TabsTrigger>
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="agent">
                  Agent
                </TabsTrigger>
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="skills">
                  Skills
                  {skills.length > 0 && (
                    <span className="ml-1 text-[10px] bg-brand-secondary-600/40 text-brand-secondary-300 rounded-full px-1.5 py-0.5 leading-none tabular-nums">
                      {skills.length}
                    </span>
                  )}
                </TabsTrigger>
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="sandbox">
                  Sandbox
                </TabsTrigger>
                <TabsTrigger className={TAB_TRIGGER_CLASS} value="advanced">
                  Advanced
                </TabsTrigger>
              </TabsList>

              {/* ── General ──────────────────────────────────── */}
              <TabsContent
                value="general"
                className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos"
              >
                <div className="space-y-2">
                  <Label htmlFor="ws-name">Name</Label>
                  <Input
                    id="ws-name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="my-instance"
                    className={brandInputClass}
                  />
                </div>

                <div className="space-y-2">
                  <Label htmlFor="ws-model">Model</Label>
                  {gatewayModels.length > 0 ? (
                    <Select value={model} onValueChange={setModel}>
                      <SelectTrigger
                        id="ws-model"
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
                    <Input
                      id="ws-model"
                      value={model}
                      onChange={(e) => setModel(e.target.value)}
                      placeholder="anthropic/claude-sonnet-4-20250514"
                      className={brandInputClass}
                    />
                  )}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="ws-description">Description</Label>
                  <Input
                    id="ws-description"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    placeholder="What does this instance do?"
                    className={brandInputClass}
                  />
                </div>

                <div className="space-y-2">
                  <Label>Color</Label>
                  <Popover>
                    <PopoverTrigger asChild>
                      <button
                        type="button"
                        className="flex h-9 w-full items-center gap-2 rounded-md border border-brand-main-600 bg-brand-main-900 px-3 text-sm text-zinc-200 light:text-zinc-800"
                      >
                        <span
                          className="h-4 w-4 shrink-0 rounded"
                          style={{
                            backgroundColor: color || DEFAULT_TROOPER_COLOR,
                          }}
                        />
                        <span className="truncate">
                          {TROOPER_COLORS.find(
                            (c) => c.hex === (color || DEFAULT_TROOPER_COLOR),
                          )?.label ?? 'Select color'}
                        </span>
                      </button>
                    </PopoverTrigger>
                    <PopoverContent className="w-auto p-3" align="start">
                      <div className="grid grid-cols-6 gap-1.5">
                        {TROOPER_COLORS.map((c) => (
                          <button
                            key={c.hex}
                            type="button"
                            title={c.label}
                            onClick={() => setColor(c.hex)}
                            className="h-7 w-7 rounded-md border-2 transition-all hover:scale-110"
                            style={{
                              backgroundColor: c.hex,
                              borderColor:
                                (color || DEFAULT_TROOPER_COLOR) === c.hex
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
                  <Label htmlFor="ws-icon">Icon</Label>
                  <Input
                    id="ws-icon"
                    value={icon}
                    onChange={(e) => setIcon(e.target.value)}
                    placeholder="brain"
                    className={brandInputClass}
                  />
                </div>
              </TabsContent>

              {/* ── Identity ─────────────────────────────────── */}
              <TabsContent
                value="identity"
                className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos"
              >
                <div className="space-y-2">
                  <Label htmlFor="ws-soul">SOUL.md</Label>
                  <Textarea
                    id="ws-soul"
                    value={soulMd}
                    onChange={(e) => setSoulMd(e.target.value)}
                    placeholder="Core personality and values..."
                    className={`${brandTextareaClass} h-24 font-mono text-xs`}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="ws-identity">IDENTITY.md</Label>
                  <Textarea
                    id="ws-identity"
                    value={identityMd}
                    onChange={(e) => setIdentityMd(e.target.value)}
                    placeholder="Who this agent is..."
                    className={`${brandTextareaClass} h-24 font-mono text-xs`}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="ws-user">USER.md</Label>
                  <Textarea
                    id="ws-user"
                    value={userMd}
                    onChange={(e) => setUserMd(e.target.value)}
                    placeholder="User preferences and context..."
                    className={`${brandTextareaClass} h-24 font-mono text-xs`}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="ws-role">ROLE.md</Label>
                  <Textarea
                    id="ws-role"
                    value={roleMd}
                    onChange={(e) => setRoleMd(e.target.value)}
                    placeholder="Role-specific instructions..."
                    className={`${brandTextareaClass} h-24 font-mono text-xs`}
                  />
                </div>
              </TabsContent>

              {/* ── Agent ────────────────────────────────────── */}
              <TabsContent
                value="agent"
                className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos"
              >
                <div className="space-y-2">
                  <Label htmlFor="ws-prompt">System Prompt</Label>
                  <Textarea
                    id="ws-prompt"
                    value={systemPrompt}
                    onChange={(e) => setSystemPrompt(e.target.value)}
                    placeholder="You are a helpful assistant..."
                    className={`${brandTextareaClass} h-32 font-mono text-xs`}
                  />
                </div>

                {functions.length > 0 && (
                  <div className="space-y-2">
                    <Label>
                      Tools
                      {selectedTools.length > 0 && (
                        <span className="ml-1 text-[10px] text-brand-secondary-300">
                          ({selectedTools.length})
                        </span>
                      )}
                    </Label>
                    <div className="flex flex-wrap gap-2 max-h-36 overflow-y-auto p-2 rounded-md border border-brand-main-800">
                      {functions.map((fn) => (
                        <button
                          key={fn.name}
                          type="button"
                          onClick={() => toggleTool(fn.name)}
                          className={`px-2 py-1 rounded text-xs transition-colors ${
                            selectedTools.includes(fn.name)
                              ? 'bg-brand-secondary-600 text-white'
                              : 'bg-brand-main-800 text-white/60 light:text-black/60 hover:text-white/80 light:hover:text-black/80'
                          } light:hover:text-black/80`}
                        >
                          {fn.name}
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label htmlFor="ws-turns">Max Turns</Label>
                    <Input
                      id="ws-turns"
                      type="number"
                      value={maxTurns}
                      onChange={(e) => setMaxTurns(Number(e.target.value))}
                      className={brandInputClass}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="ws-tcpt">Max Tool Calls/Turn</Label>
                    <Input
                      id="ws-tcpt"
                      type="number"
                      value={maxToolCallsPerTurn}
                      onChange={(e) =>
                        setMaxToolCallsPerTurn(Number(e.target.value))
                      }
                      className={brandInputClass}
                    />
                  </div>
                </div>
              </TabsContent>

              {/* ── Skills ───────────────────────────────────── */}
              <TabsContent
                value="skills"
                className="flex flex-col gap-4 mt-0 flex-1 min-h-0"
              >
                {skills.length > 0 && (
                  <div className="space-y-2">
                    <Label className="text-xs text-zinc-400 light:text-zinc-600 uppercase tracking-wide">
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
                            <div className="text-sm text-zinc-200 light:text-zinc-800 font-medium truncate">
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
                            className="text-zinc-500 hover:text-red-400 light:hover:text-red-600 transition-colors shrink-0"
                          >
                            <X className="w-3.5 h-3.5" />
                          </button>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                <div className="space-y-3 flex-1 min-h-0 overflow-y-auto scrollbar-macos">
                  <div className="flex items-center justify-between">
                    <Label className="text-xs text-zinc-400 light:text-zinc-600 uppercase tracking-wide">
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
                            className={`px-2 py-1 text-[11px] font-medium rounded transition-colors ${registryView === v.key ? 'bg-brand-main-700/60 text-zinc-200 light:text-zinc-800' : 'text-zinc-500 hover:text-zinc-300 light:hover:text-zinc-700'}`}
                          >
                            {v.label}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>

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

                  {isRegistryLoading &&
                    displayedRegistrySkills.length === 0 && (
                      <div className="text-center py-6">
                        <Loader2 className="w-4 h-4 animate-spin text-zinc-500 mx-auto" />
                        <p className="text-xs text-zinc-500 mt-2">
                          Loading skills from registry...
                        </p>
                      </div>
                    )}

                  {!isRegistryLoading &&
                    displayedRegistrySkills.length === 0 &&
                    debouncedSkillSearch && (
                      <div className="text-center py-6">
                        <p className="text-xs text-zinc-500">
                          No skills found matching &ldquo;{debouncedSkillSearch}
                          &rdquo;
                        </p>
                      </div>
                    )}

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
                            <span className="text-xs font-medium text-zinc-200 light:text-zinc-800 truncate flex-1">
                              {rs.name}
                            </span>
                            {isInstalled && (
                              <span className="text-[9px] text-green-400 light:text-green-600 shrink-0">
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
                              className={`text-[10px] px-1.5 py-0.5 rounded transition-colors ${isInstalled ? 'text-zinc-600 cursor-default' : 'text-brand-secondary-400 hover:text-brand-secondary-300 hover:bg-brand-secondary-600/20'}`}
                            >
                              {isInstalled ? 'Installed' : 'Install'}
                            </button>
                          </div>
                        </div>
                      )
                    })}
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

                  <div className="border-t border-brand-main-700/40 pt-3">
                    <button
                      type="button"
                      onClick={() => setCustomSkillOpen((v) => !v)}
                      className="flex items-center gap-1 text-[11px] text-zinc-500 hover:text-zinc-300 light:hover:text-zinc-700 transition-colors"
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
                          onClick={() => resolveAndInstallSkill(skillSpecInput)}
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

              {/* ── Sandbox ──────────────────────────────────── */}
              <TabsContent
                value="sandbox"
                className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos"
              >
                <div className="flex items-center gap-2 rounded-md bg-brand-main-900/40 border border-brand-main-700/60 px-3 py-2">
                  <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Sandbox Image
                  </span>
                  <span className="text-xs text-zinc-300 light:text-zinc-700 font-mono">
                    {TROOPER_VM_IMAGE}
                  </span>
                </div>

                <div className="grid grid-cols-3 gap-3">
                  <div className="space-y-2">
                    <Label htmlFor="ws-cpu">CPU</Label>
                    <Input
                      id="ws-cpu"
                      type="number"
                      step="0.5"
                      value={cpuLimit}
                      onChange={(e) => setCpuLimit(Number(e.target.value))}
                      className={brandInputClass}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="ws-mem">Memory (MB)</Label>
                    <Input
                      id="ws-mem"
                      type="number"
                      value={memoryMb}
                      onChange={(e) => setMemoryMb(Number(e.target.value))}
                      className={brandInputClass}
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="ws-disk">Disk (MB)</Label>
                    <Input
                      id="ws-disk"
                      type="number"
                      value={diskMb}
                      onChange={(e) => setDiskMb(Number(e.target.value))}
                      className={brandInputClass}
                    />
                  </div>
                </div>
                <div className="space-y-2">
                  <Label>Network Mode</Label>
                  <Select value={networkMode} onValueChange={setNetworkMode}>
                    <SelectTrigger className={brandSelectTriggerClass}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className={brandSelectContentClass}>
                      <SelectItem value="allow">Allow</SelectItem>
                      <SelectItem value="deny">Deny</SelectItem>
                      <SelectItem value="egress-only">Egress Only</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex items-center justify-between rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2">
                  <div className="space-y-0.5">
                    <Label className="text-xs text-white/75 light:text-black/75">
                      SSH Enabled
                    </Label>
                    <p className="text-[11px] text-white/40 light:text-black/40">
                      Allow SSH connections into the instance sandbox
                    </p>
                  </div>
                  <Switch
                    checked={sshEnabled}
                    onCheckedChange={setSshEnabled}
                  />
                </div>

                {/* Pricing Estimate */}
                <div className="rounded-md border border-brand-main-700/60 bg-brand-main-900/30 p-3 space-y-2">
                  <div className="text-[11px] font-medium uppercase tracking-wider text-white/50 light:text-black/50">
                    Estimated Cost
                  </div>
                  <div className="grid grid-cols-3 gap-2">
                    <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                      <p className="text-[10px] text-white/45 light:text-black/45">
                        Per hour
                      </p>
                      <p className="text-sm font-semibold text-white light:text-brand-main-50">
                        {formatCurrency(estimatedPricing.hourly)}
                      </p>
                    </div>
                    <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                      <p className="text-[10px] text-white/45 light:text-black/45">
                        Per day
                      </p>
                      <p className="text-sm font-semibold text-white light:text-brand-main-50">
                        {formatCurrency(estimatedPricing.daily)}
                      </p>
                    </div>
                    <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                      <p className="text-[10px] text-white/45 light:text-black/45">
                        Per month
                      </p>
                      <p className="text-sm font-semibold text-white light:text-brand-main-50">
                        {formatCurrency(estimatedPricing.monthly)}
                      </p>
                    </div>
                  </div>
                </div>

                {/* GitHub Source */}
                <div className="space-y-3 rounded-md border border-brand-main-700/80 bg-brand-main-900/25 p-3">
                  <div className="text-[11px] font-medium uppercase tracking-wider text-white/50 light:text-black/50">
                    GitHub Source (optional)
                  </div>

                  <div className="space-y-1">
                    <Label
                      htmlFor="ws-git-installation"
                      className="text-xs text-white/60 light:text-black/60"
                    >
                      GitHub Installation
                    </Label>
                    <Select
                      value={gitInstallationId || '__none__'}
                      onValueChange={(value) => {
                        setGitInstallationId(value === '__none__' ? '' : value)
                      }}
                    >
                      <SelectTrigger
                        id="ws-git-installation"
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
                      <SelectContent className={brandSelectContentClass}>
                        <SelectItem value="__none__">None</SelectItem>
                        {gitHubInstallations.map((inst) => (
                          <SelectItem
                            key={inst.installationId}
                            value={String(inst.installationId)}
                          >
                            {inst.accountLogin} ({inst.accountType})
                          </SelectItem>
                        ))}
                        {gitInstallationId &&
                          !selectedGitInstallationExists && (
                            <SelectItem value={gitInstallationId}>
                              Installation {gitInstallationId} (not linked)
                            </SelectItem>
                          )}
                      </SelectContent>
                    </Select>
                    {gitHubInstallations.length === 0 &&
                      !gitHubInstallationsLoading && (
                        <p className="text-[11px] text-white/35 light:text-black/35">
                          No linked GitHub installations found. Connect GitHub
                          in Settings → Integrations.
                        </p>
                      )}
                  </div>

                  {selectedGitInstallationId > 0 && (
                    <>
                      <div className="space-y-1">
                        <Label className="text-xs text-white/60 light:text-black/60">
                          Repository
                        </Label>
                        <Select
                          value={
                            selectedRepoIsInList && parsedRepo
                              ? parsedRepo.fullName
                              : '__custom__'
                          }
                          onValueChange={(value) => {
                            if (value === '__custom__') return
                            setGitRepoUrl(value)
                            const selectedRepo = gitHubRepos.find(
                              (repo) => repo.fullName === value,
                            )
                            if (selectedRepo && !gitBranch)
                              setGitBranch(selectedRepo.defaultBranch || '')
                          }}
                        >
                          <SelectTrigger className={brandSelectTriggerClass}>
                            <SelectValue
                              placeholder={
                                gitHubReposLoading
                                  ? 'Loading repositories...'
                                  : 'Select repository'
                              }
                            />
                          </SelectTrigger>
                          <SelectContent className={brandSelectContentClass}>
                            <SelectItem value="__custom__">
                              Custom / manual input
                            </SelectItem>
                            {gitHubRepos.map((repo) => (
                              <SelectItem key={repo.id} value={repo.fullName}>
                                {repo.fullName}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>
                    </>
                  )}

                  {selectedGitInstallationId > 0 && parsedRepo && (
                    <div className="space-y-1">
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Branch
                      </Label>
                      <Select
                        value={gitBranch || '__default__'}
                        onValueChange={(value) =>
                          setGitBranch(value === '__default__' ? '' : value)
                        }
                      >
                        <SelectTrigger className={brandSelectTriggerClass}>
                          <SelectValue
                            placeholder={
                              gitHubBranchesLoading
                                ? 'Loading branches...'
                                : 'Select branch'
                            }
                          />
                        </SelectTrigger>
                        <SelectContent className={brandSelectContentClass}>
                          <SelectItem value="__default__">
                            Default branch
                          </SelectItem>
                          {gitHubBranches.map((branch) => (
                            <SelectItem key={branch.name} value={branch.name}>
                              {branch.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  )}

                  {!selectedGitInstallationId && (
                    <>
                      <div className="space-y-1">
                        <Label
                          htmlFor="ws-git-url"
                          className="text-xs text-white/60 light:text-black/60"
                        >
                          Git Repo URL
                        </Label>
                        <Input
                          id="ws-git-url"
                          value={gitRepoUrl}
                          onChange={(e) => setGitRepoUrl(e.target.value)}
                          placeholder="https://github.com/org/repo"
                          className={brandInputClass}
                        />
                      </div>
                      <div className="space-y-1">
                        <Label
                          htmlFor="ws-git-branch"
                          className="text-xs text-white/60 light:text-black/60"
                        >
                          Git Branch
                        </Label>
                        <Input
                          id="ws-git-branch"
                          value={gitBranch}
                          onChange={(e) => setGitBranch(e.target.value)}
                          placeholder="main"
                          className={brandInputClass}
                        />
                      </div>
                    </>
                  )}
                </div>
              </TabsContent>

              {/* ── Advanced ─────────────────────────────────── */}
              <TabsContent
                value="advanced"
                className="space-y-4 mt-0 flex-1 min-h-0 overflow-y-auto scrollbar-macos"
              >
                <div className="space-y-2">
                  <Label htmlFor="ws-workers">Max Concurrent Workers</Label>
                  <Input
                    id="ws-workers"
                    type="number"
                    value={maxConcurrentWorkers}
                    onChange={(e) =>
                      setMaxConcurrentWorkers(Number(e.target.value))
                    }
                    className={brandInputClass}
                  />
                </div>
                <div className="flex items-center justify-between rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2">
                  <div className="space-y-0.5">
                    <Label className="text-xs text-white/75 light:text-black/75">
                      Auto-Provision
                    </Label>
                    <p className="text-[11px] text-white/40 light:text-black/40">
                      Create sandbox immediately after instance creation
                    </p>
                  </div>
                  <Switch
                    checked={autoProvision}
                    onCheckedChange={setAutoProvision}
                  />
                </div>
              </TabsContent>
            </Tabs>

            <div className="flex gap-3 pt-3 border-t border-brand-main-700/60 shrink-0 w-full">
              <Button
                type="button"
                variant="outline"
                className="w-1/2"
                onClick={() => onOpenChange(false)}
                disabled={isPending}
              >
                Cancel
              </Button>
              <Button type="submit" className="w-1/2" disabled={isPending}>
                {isPending ? 'Creating...' : 'Create Instance'}
              </Button>
            </div>
          </form>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}
