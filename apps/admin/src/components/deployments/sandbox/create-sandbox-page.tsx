import { useState, useMemo, useEffect, useRef } from 'react'
import { X } from 'lucide-react'
import { useNavigate } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
  useCreateSandbox,
  useSandboxInstance,
  useSandboxTemplates,
} from '@/hooks/deployments/use-sandbox'
import { useRuntimeConfigSection } from '@/hooks/gateway/use-runtime-config'
import { useLicenseStatus } from '@/hooks/license/use-license-status'
import { getCloudBillingUrl, isCloudManaged } from '@/lib/cloud-mode'
import {
  useGitHubInstallations,
  useGitHubRepositories,
  useGitHubBranches,
} from '@/hooks/integrations/use-github'
import {
  SANDBOX_MACHINE_PROFILES,
  estimateDiskHourlyUsd,
  resolveSandboxPricing,
  sandboxMachineProfilesForTier,
} from '@/lib/sandbox-pricing'
import type {
  CreateSandboxParams,
  SandboxStorageMount,
  SandboxTemplate,
} from '@/server/sandbox'
import { EnvironmentCard } from './environment-card'

const {
  Button,
  Input,
  Label,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} = ui

/** TTL option: value is seconds sent to the API. -1 = no expiration. */
interface TtlOption {
  value: string // string for Select component
  label: string
  seconds: number // actual value: -1 = no expiration, >0 = seconds
}

/** Returns available TTL options based on the user's plan tier. */
function getTtlOptionsForTier(tier: string): TtlOption[] {
  switch (tier) {
    case 'pro':
      return [
        { value: '30d', label: '30 days', seconds: 30 * 24 * 60 * 60 },
        { value: 'never', label: 'No expiration', seconds: -1 },
      ]
    case 'enterprise':
      return [{ value: 'never', label: 'No expiration', seconds: -1 }]
    case 'basic':
      return [{ value: '7d', label: '7 days', seconds: 7 * 24 * 60 * 60 }]
    default: // free
      return [{ value: '1d', label: '1 day', seconds: 1 * 24 * 60 * 60 }]
  }
}

// Fallback templates used when the API is unavailable (e.g. backend not yet rebuilt).
// Mirrors the server-side TemplateCatalog in internal/sandbox/templates.go.
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
    networkMode: 'allow',
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
    networkMode: 'allow',
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
    networkMode: 'allow',
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
    networkMode: 'allow',
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
    networkMode: 'allow',
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
    networkMode: 'allow',
    workDir: '/workspace',
  },
]

// Custom option appended to API templates
const CUSTOM_OPTION = {
  id: 'custom',
  name: 'Custom',
  slug: 'custom',
  description: 'Use any Docker image',
  icon: 'heroicons:cube-transparent',
  iconColor: '#a78bfa',
  image: '',
} as const

const DEFAULT_MACHINE_PROFILE = SANDBOX_MACHINE_PROFILES[0]!

function formatCurrency(amount: number, currency: string): string {
  const decimals = Math.abs(amount) >= 1 ? 2 : 4
  if (currency === 'USD') {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    }).format(amount)
  }
  return `${currency} ${new Intl.NumberFormat('en-US', {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(amount)}`
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min
  return Math.max(min, Math.min(max, value))
}

/** Parse an owner/repo or https://github.com/owner/repo string. */
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

/** A single CIDR is a rough IPv4/prefix shape check; backend re-validates. */
function isLikelyCidr(value: string): boolean {
  return /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/.test(value.trim())
}

const STORAGE_MOUNT_TYPES = [
  { value: 's3', label: 'Amazon S3' },
  { value: 'r2', label: 'Cloudflare R2' },
  { value: 'gcs', label: 'Google Cloud Storage' },
  { value: 'azure', label: 'Azure Blob' },
] as const

/** Empty storage-mount draft used when the user adds a new mount row. */
function emptyMount(): SandboxStorageMount {
  return {
    type: 's3',
    bucket: '',
    mountPath: '',
    endpoint: '',
    subpath: '',
    readOnly: false,
  }
}

export function CreateSandboxPage({
  recreateFromSandboxId,
}: { recreateFromSandboxId?: string } = {}) {
  const navigate = useNavigate()
  const createMutation = useCreateSandbox()
  const { data: licenseData } = useLicenseStatus()
  const { data: apiTemplates } = useSandboxTemplates()
  const { data: featuresSection } = useRuntimeConfigSection('features')
  // When recreating, pull the source sandbox so we can pre-fill the form.
  // Sandboxes have no in-place update RPC, so editing means "create new
  // with these tweaks" — the user can terminate the source from the
  // sandboxes list once the new one is healthy.
  const { data: sourceSandbox } = useSandboxInstance(recreateFromSandboxId)
  const isRecreate = !!recreateFromSandboxId
  const sourceConfig = (sourceSandbox?.config ?? {}) as Record<string, unknown>
  const sourceNum = (key: string): number | undefined => {
    const v = sourceConfig[key]
    return typeof v === 'number' ? v : undefined
  }
  const sourceStr = (key: string): string | undefined => {
    const v = sourceConfig[key]
    return typeof v === 'string' ? v : undefined
  }
  const sourceStrArray = (key: string): string[] | undefined => {
    const v = sourceConfig[key]
    return Array.isArray(v) && v.every((x) => typeof x === 'string')
      ? (v as string[])
      : undefined
  }
  const sourceBool = (key: string): boolean | undefined => {
    const v = sourceConfig[key]
    return typeof v === 'boolean' ? v : undefined
  }
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

  // Use API templates when available, fall back to hardcoded list
  const templates =
    apiTemplates && apiTemplates.length > 0 ? apiTemplates : FALLBACK_TEMPLATES

  const [step, setStep] = useState<1 | 2>(1)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [customImage, setCustomImage] = useState('')

  const [sandboxName, setSandboxName] = useState('')
  const [cpuLimit, setCpuLimit] = useState(DEFAULT_MACHINE_PROFILE.cpu)
  const [memoryMb, setMemoryMb] = useState(DEFAULT_MACHINE_PROFILE.memoryMb)
  const [diskMb, setDiskMb] = useState(DEFAULT_MACHINE_PROFILE.diskMb)
  const [timeoutSeconds, setTimeoutSeconds] = useState(300)
  const [networkMode, setNetworkMode] = useState('allow')
  const [allowedHosts, setAllowedHosts] = useState<string[]>([])
  const [hostInput, setHostInput] = useState('')
  const [sshEnabled, setSshEnabled] = useState(false)
  const [labels, setLabels] = useState<Array<{ key: string; value: string }>>(
    [],
  )
  const [labelKeyInput, setLabelKeyInput] = useState('')
  const [labelValueInput, setLabelValueInput] = useState('')

  // Block-all egress + CIDR allowlist (orthogonal to networkMode). The
  // backend accepts network_allow_cidrs only when network_block_all=true and
  // caps the list at 10 entries.
  const [networkBlockAll, setNetworkBlockAll] = useState(false)
  const [allowCidrs, setAllowCidrs] = useState<string[]>([])
  const [cidrInput, setCidrInput] = useState('')

  // Computer use (desktop/browser automation) + Tailscale tailnet join.
  const [computerUse, setComputerUse] = useState(false)
  const [tailscaleAuthKey, setTailscaleAuthKey] = useState('')

  // Object-storage mounts (S3/R2/GCS/Azure).
  const [mounts, setMounts] = useState<SandboxStorageMount[]>([])

  // Git import (installation → repo → branch).
  const [gitInstallationId, setGitInstallationId] = useState('')
  const [gitRepoUrl, setGitRepoUrl] = useState('')
  const [gitBranch, setGitBranch] = useState('')

  const ttlOptions = useMemo(() => getTtlOptionsForTier(tier), [tier])
  const [selectedTtl, setSelectedTtl] = useState<string>(
    ttlOptions[0]?.value ?? '1d',
  )

  // Sync TTL selection when plan tier changes (e.g., license loads async)
  useEffect(() => {
    setSelectedTtl(ttlOptions[0]?.value ?? '1d')
  }, [ttlOptions])

  const isCustom = selectedId === 'custom'
  const selectedTemplate = templates.find(
    (t) => t.id === selectedId || t.slug === selectedId,
  )

  // Pre-fill resource fields when a template is selected
  useEffect(() => {
    if (selectedTemplate) {
      // Managed compute has a separate fixed-size selector. Templates choose
      // the software image and defaults, never an unpriced resource tuple.
      if (!managedCloud) {
        setCpuLimit(selectedTemplate.cpuLimit || 1)
        setMemoryMb(selectedTemplate.memoryMb || 512)
        setDiskMb(selectedTemplate.diskMb || 1024)
      }
      setTimeoutSeconds(selectedTemplate.timeoutSeconds || 300)
      setNetworkMode(selectedTemplate.networkMode || 'deny')
    }
  }, [managedCloud, selectedTemplate])

  // Pre-fill from a source sandbox when recreating. Runs once the source
  // record arrives. The image-on-config can land under "image" (the column
  // is normalized) or inside the JSONB config map for older rows — try
  // both. Jumps straight to step 2 (the resource form) since the user is
  // editing, not picking a template.
  const recreatePrefillRef = useRef(false)
  useEffect(() => {
    if (!isRecreate || !sourceSandbox || recreatePrefillRef.current) return
    recreatePrefillRef.current = true

    const img = sourceSandbox.image || sourceStr('image') || ''
    if (img) {
      setSelectedId('custom')
      setCustomImage(img)
    }
    if (sourceSandbox.name) {
      setSandboxName(sourceSandbox.name + ' (edited)')
    }
    const cpu = sourceNum('cpu_limit') ?? sourceNum('cpuLimit')
    if (cpu && cpu > 0) setCpuLimit(cpu)
    const mem = sourceNum('memory_mb') ?? sourceNum('memoryMb')
    if (mem && mem > 0) setMemoryMb(mem)
    const disk = sourceNum('disk_mb') ?? sourceNum('diskMb')
    if (disk && disk > 0) setDiskMb(disk)
    const timeout = sourceNum('timeout_seconds') ?? sourceNum('timeoutSeconds')
    if (timeout && timeout > 0) setTimeoutSeconds(timeout)
    const nm = sourceStr('network_mode') ?? sourceStr('networkMode')
    if (nm) setNetworkMode(nm)
    const hosts =
      sourceStrArray('allowed_hosts') ?? sourceStrArray('allowedHosts')
    if (hosts && hosts.length > 0) setAllowedHosts(hosts)
    if (sourceSandbox.sshEnabled != null)
      setSshEnabled(!!sourceSandbox.sshEnabled)

    const blockAll =
      sourceBool('network_block_all') ?? sourceBool('networkBlockAll')
    if (blockAll != null) setNetworkBlockAll(blockAll)
    const cidrs =
      sourceStrArray('network_allow_cidrs') ??
      sourceStrArray('networkAllowCidrs')
    if (cidrs && cidrs.length > 0) setAllowCidrs(cidrs)
    const compUse = sourceBool('computer_use') ?? sourceBool('computerUse')
    if (compUse != null) setComputerUse(compUse)
    const repoUrl = sourceStr('git_repo_url') ?? sourceStr('gitRepoUrl')
    if (repoUrl) setGitRepoUrl(repoUrl)
    const branch = sourceStr('git_branch') ?? sourceStr('gitBranch')
    if (branch) setGitBranch(branch)
    const installId =
      sourceNum('git_installation_id') ?? sourceNum('gitInstallationId')
    if (installId && installId > 0) setGitInstallationId(String(installId))

    setStep(2)
  }, [isRecreate, sourceSandbox])

  const selectedMachineProfile = useMemo(() => {
    const found = SANDBOX_MACHINE_PROFILES.find(
      (profile) =>
        profile.cpu === cpuLimit &&
        profile.memoryMb === memoryMb &&
        profile.diskMb === diskMb,
    )
    return found?.id ?? 'custom'
  }, [cpuLimit, memoryMb, diskMb])

  const hoursPerDay = useMemo(() => {
    const ttlOption = ttlOptions.find((o) => o.value === selectedTtl)
    if (!ttlOption) return 8
    if (ttlOption.seconds === -1) return 24
    return Math.min(24, Math.max(1, Math.ceil(ttlOption.seconds / 3600)))
  }, [selectedTtl, ttlOptions])

  const estimatedPricing = useMemo(() => {
    const memoryGb = memoryMb / 1024
    const diskGb = diskMb / 1024
    const multiplier = sandboxPricing.tierMultipliers[tier] ?? 1

    const hourlyRaw =
      cpuLimit * sandboxPricing.cpuPerHourUsd +
      memoryGb * sandboxPricing.memoryGbPerHourUsd +
      estimateDiskHourlyUsd(diskGb, sandboxPricing) +
      sandboxPricing.platformFeePerHourUsd

    const hourly = (sandboxPricing.enabled ? hourlyRaw : 0) * multiplier
    const daily = hourly * hoursPerDay
    const monthly = daily * 30

    return { hourly, daily, monthly }
  }, [cpuLimit, memoryMb, diskMb, tier, hoursPerDay, sandboxPricing])

  const tierDiscountPercent = useMemo(() => {
    const multiplier = sandboxPricing.tierMultipliers[tier] ?? 1
    if (multiplier >= 1) return 0
    return Math.round((1 - multiplier) * 100)
  }, [tier, sandboxPricing.tierMultipliers])

  const resolvedImage = isCustom ? customImage : (selectedTemplate?.image ?? '')

  const canContinue =
    selectedId !== null && (!isCustom || customImage.trim().length > 0)

  // ─── GitHub source pickers ─────────────────────────────────────────
  const { data: gitHubInstallations = [], isLoading: installationsLoading } =
    useGitHubInstallations()
  const selectedInstallationId = Number(gitInstallationId)
  const parsedRepo = useMemo(() => parseGitHubRepo(gitRepoUrl), [gitRepoUrl])
  const repoQueryOptions = useMemo(() => ({ page: 1, perPage: 50 }), [])
  const { data: gitHubReposData, isLoading: reposLoading } =
    useGitHubRepositories(
      Number.isFinite(selectedInstallationId) ? selectedInstallationId : 0,
      repoQueryOptions,
    )
  const branchOptions = useMemo(() => ({ page: 1, perPage: 100 }), [])
  const { data: gitHubBranches = [], isLoading: branchesLoading } =
    useGitHubBranches(
      Number.isFinite(selectedInstallationId) ? selectedInstallationId : 0,
      parsedRepo?.owner ?? '',
      parsedRepo?.repo ?? '',
      branchOptions,
    )
  const gitHubRepos = gitHubReposData?.repositories ?? []
  const selectedRepoInList = useMemo(
    () =>
      !!parsedRepo &&
      gitHubRepos.some((repo) => repo.fullName === parsedRepo.fullName),
    [gitHubRepos, parsedRepo],
  )

  // A mount only ships if it has both a bucket and an absolute mount path.
  const validMounts = useMemo(
    () =>
      mounts.filter(
        (m) => m.bucket.trim().length > 0 && m.mountPath.trim().length > 0,
      ),
    [mounts],
  )

  const updateMount = (index: number, patch: Partial<SandboxStorageMount>) => {
    setMounts((prev) =>
      prev.map((m, i) => (i === index ? { ...m, ...patch } : m)),
    )
  }

  // Resolve icon/name for header display
  const selectedIcon = isCustom ? CUSTOM_OPTION.icon : selectedTemplate?.icon
  const selectedIconColor = isCustom
    ? CUSTOM_OPTION.iconColor
    : selectedTemplate?.iconColor
  const selectedName = isCustom ? 'Custom' : selectedTemplate?.name

  const handleCreate = () => {
    if (!sandboxBillingEnabled) {
      toast.error(
        'Your sandbox starter credit is exhausted. Add billing to continue',
      )
      return
    }
    if (managedCloud && selectedMachineProfile === 'custom') {
      toast.error('Choose one of the supported fixed sandbox sizes')
      return
    }
    const ttlOption = ttlOptions.find((o) => o.value === selectedTtl)
    // When API templates are available, send templateId so the server resolves config.
    // When using fallback templates, send the image directly.
    const useTemplateId = !isCustom && apiTemplates && apiTemplates.length > 0
    const params: CreateSandboxParams = {
      templateId: useTemplateId ? (selectedId ?? undefined) : undefined,
      image: isCustom ? customImage : selectedTemplate?.image,
      cpuLimit: cpuLimit,
      memoryMb: memoryMb,
      diskMb: diskMb,
      timeoutSeconds: timeoutSeconds,
      networkMode: networkMode,
      allowedHosts:
        networkMode === 'whitelist' && allowedHosts.length > 0
          ? allowedHosts
          : undefined,
      idleRetentionSeconds: ttlOption?.seconds,
      name: sandboxName.trim() || undefined,
      sshEnabled: sshEnabled || undefined,
      labels:
        labels.length > 0
          ? Object.fromEntries(
              labels
                .filter((l) => l.key.trim())
                .map((l) => [l.key.trim(), l.value]),
            )
          : undefined,
      networkBlockAll: networkBlockAll || undefined,
      networkAllowCidrs:
        networkBlockAll && allowCidrs.length > 0 ? allowCidrs : undefined,
      computerUse: computerUse || undefined,
      tailscaleAuthKey: tailscaleAuthKey.trim() || undefined,
      mounts:
        validMounts.length > 0
          ? validMounts.map((m) => ({
              type: m.type,
              bucket: m.bucket.trim(),
              mountPath: m.mountPath.trim(),
              endpoint: m.endpoint?.trim() || undefined,
              subpath: m.subpath?.trim() || undefined,
              readOnly: m.readOnly || undefined,
            }))
          : undefined,
      gitRepoUrl: gitRepoUrl.trim() || undefined,
      gitBranch: gitBranch.trim() || undefined,
      gitInstallationId:
        selectedInstallationId > 0 ? selectedInstallationId : undefined,
    }
    toast.info(`Creating sandbox with ${resolvedImage}...`)
    createMutation.mutate(params, {
      onSuccess: () => {
        toast.success('Sandbox created successfully')
        navigate({ to: '/deployments/sandboxes', search: { tab: 'instances' } })
      },
      onError: (error) =>
        toast.error(
          error instanceof Error ? error.message : 'Failed to create sandbox',
        ),
    })
  }

  return (
    <div className="flex flex-col h-full w-full">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-2 border-b border-brand-main-600">
        <div className="flex items-center gap-4">
          <button
            type="button"
            onClick={() => navigate({ to: '/deployments/sandboxes' })}
            className="flex items-center gap-1.5 text-sm text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50 transition-colors"
          >
            <Iconify.Icon icon="heroicons:arrow-left" className="size-4" />
            <span>Sandboxes</span>
          </button>
          <div className="h-4 w-px bg-brand-main-600" />
          <h2 className="text-sm font-semibold text-white light:text-brand-main-50 flex items-center gap-2">
            {isRecreate ? 'Recreate Sandbox' : 'Create Sandbox'}
            {selectedId && selectedIcon && (
              <>
                <div className="h-4 w-px bg-brand-main-600" />
                <span className="flex items-center gap-1.5 text-white/70 light:text-black/70 font-medium">
                  <Iconify.Icon
                    icon={selectedIcon}
                    className="size-4"
                    style={{ color: selectedIconColor }}
                  />
                  {selectedName}
                </span>
              </>
            )}
          </h2>
        </div>

        {/* Step indicator — matches brand TabsList styling */}
        <div className="flex items-center gap-1 bg-brand-main-800/50 border border-brand-main-600 rounded p-1">
          <button
            type="button"
            onClick={() => setStep(1)}
            className={`flex items-center gap-2 px-3 py-1 rounded text-xs font-medium transition-colors ${
              step === 1
                ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30'
                : 'text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 border border-transparent'
            }`}
          >
            <span
              className={`flex items-center justify-center size-5 rounded-full text-[10px] font-semibold ${
                step === 1
                  ? 'bg-brand-secondary-500/30 text-brand-secondary-200'
                  : 'bg-brand-main-700 text-white/40 light:text-black/40'
              } light:text-black/40`}
            >
              1
            </span>
            Environment
          </button>
          <button
            type="button"
            onClick={() => {
              if (canContinue) setStep(2)
            }}
            disabled={!canContinue}
            className={`flex items-center gap-2 px-3 py-1 rounded text-xs font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
              step === 2
                ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30'
                : 'text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 border border-transparent'
            }`}
          >
            <span
              className={`flex items-center justify-center size-5 rounded-full text-[10px] font-semibold ${
                step === 2
                  ? 'bg-brand-secondary-500/30 text-brand-secondary-200'
                  : 'bg-brand-main-700 text-white/40 light:text-black/40'
              } light:text-black/40`}
            >
              2
            </span>
            Resources
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {managedCloud && !sandboxBillingEnabled && (
          <div className="max-w-3xl mx-auto mb-4 flex flex-wrap items-center justify-between gap-3 rounded border border-amber-500/25 bg-amber-500/5 p-3">
            <div className="flex min-w-0 items-start gap-3">
              <Iconify.Icon
                icon="heroicons:credit-card"
                className="size-4 shrink-0 text-amber-400 light:text-amber-700 mt-0.5"
              />
              <div>
                <p className="text-sm font-medium text-white light:text-brand-main-50">
                  Sandbox compute is paused
                </p>
                <p className="mt-1 text-xs text-white/55 light:text-black/55">
                  The organization starter credit is exhausted. Add or restore
                  billing to continue.
                </p>
              </div>
            </div>
            <Button
              variant="outline"
              onClick={() => window.open(getCloudBillingUrl(), '_blank')}
            >
              Open Billing
              <Iconify.Icon
                icon="heroicons:arrow-top-right-on-square"
                className="ml-1.5 size-4"
              />
            </Button>
          </div>
        )}
        {isRecreate && (
          <div className="max-w-3xl mx-auto mb-4 flex items-start gap-3 rounded border border-amber-500/25 bg-amber-500/5 p-3">
            <Iconify.Icon
              icon="heroicons:exclamation-triangle"
              className="size-4 shrink-0 text-amber-400/80 light:text-amber-700/80 mt-0.5"
            />
            <div className="text-xs leading-relaxed text-white/65 light:text-black/65">
              <p className="font-medium text-white light:text-brand-main-50">
                Recreating{' '}
                {sourceSandbox?.name || sourceSandbox?.id || 'sandbox'}
              </p>
              <p className="mt-1 text-white/45 light:text-black/45">
                Sandboxes can't be edited in place. Adjust the fields below and
                submit to provision a new sandbox with the new config. The
                original keeps running — terminate it from the sandboxes list
                once the new one is healthy.
              </p>
            </div>
          </div>
        )}
        {step === 1 ? (
          <div className="max-w-3xl mx-auto">
            <div className="mb-6">
              <h3 className="text-base font-medium text-white light:text-brand-main-50">
                Select Environment
              </h3>
              <p className="text-sm text-white/40 light:text-black/40 mt-1">
                Choose a runtime environment for your sandbox
              </p>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {templates.map((tpl) => (
                <EnvironmentCard
                  key={tpl.id}
                  name={tpl.name}
                  description={tpl.description}
                  icon={tpl.icon}
                  iconColor={tpl.iconColor}
                  imageTag={tpl.image}
                  selected={selectedId === tpl.id}
                  onClick={() => setSelectedId(tpl.id)}
                />
              ))}
              <EnvironmentCard
                name={CUSTOM_OPTION.name}
                description={CUSTOM_OPTION.description}
                icon={CUSTOM_OPTION.icon}
                iconColor={CUSTOM_OPTION.iconColor}
                imageTag=""
                isCustom
                selected={selectedId === 'custom'}
                onClick={() => setSelectedId('custom')}
              />
            </div>

            {isCustom && (
              <div className="mt-4 max-w-md bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-4">
                <Label
                  htmlFor="customImage"
                  className="text-xs text-white/60 light:text-black/60"
                >
                  Docker Image
                </Label>
                <Input
                  id="customImage"
                  value={customImage}
                  onChange={(e) => setCustomImage(e.target.value)}
                  placeholder="e.g. ubuntu:22.04"
                  className="mt-1.5 w-full"
                />
              </div>
            )}
          </div>
        ) : (
          <div className="max-w-4xl mx-auto flex gap-6">
            {/* Left column — form */}
            <div className="flex-1 min-w-0">
              <div className="mb-6">
                <h3 className="text-base font-medium text-white light:text-brand-main-50 flex items-center gap-2.5">
                  {selectedIcon && (
                    <span
                      className="flex items-center justify-center size-8 rounded-lg"
                      style={{ backgroundColor: `${selectedIconColor}15` }}
                    >
                      <Iconify.Icon
                        icon={selectedIcon}
                        className="size-4"
                        style={{ color: selectedIconColor }}
                      />
                    </span>
                  )}
                  Configure Resources
                </h3>
                <p className="text-sm text-white/40 light:text-black/40 mt-1">
                  Image:{' '}
                  <span className="font-mono text-white/60 light:text-black/60">
                    {resolvedImage}
                  </span>
                </p>
              </div>

              <div className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-5">
                <div className="space-y-1.5 mb-4">
                  <Label
                    htmlFor="sandboxName"
                    className="text-xs text-white/60 light:text-black/60"
                  >
                    Name (optional)
                  </Label>
                  <Input
                    id="sandboxName"
                    value={sandboxName}
                    onChange={(e) => setSandboxName(e.target.value)}
                    placeholder="e.g. My Dev Box"
                    className="w-full"
                  />
                  <p className="text-[11px] text-white/30 light:text-black/30">
                    A friendly name to identify this sandbox
                  </p>
                </div>
                <div className="space-y-1.5 mb-4">
                  <Label
                    htmlFor="machineSize"
                    className="text-xs text-white/60 light:text-black/60"
                  >
                    Machine Size
                  </Label>
                  <Select
                    value={selectedMachineProfile}
                    onValueChange={(value) => {
                      const profile = SANDBOX_MACHINE_PROFILES.find(
                        (p) => p.id === value,
                      )
                      if (!profile) return
                      setCpuLimit(profile.cpu)
                      setMemoryMb(profile.memoryMb)
                      setDiskMb(profile.diskMb)
                    }}
                  >
                    <SelectTrigger id="machineSize" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {availableMachineProfiles.map((profile) => (
                        <SelectItem key={profile.id} value={profile.id}>
                          {profile.label}
                        </SelectItem>
                      ))}
                      {!managedCloud && (
                        <SelectItem value="custom">Custom</SelectItem>
                      )}
                    </SelectContent>
                  </Select>
                  <p className="text-[11px] text-white/30 light:text-black/30">
                    {managedCloud
                      ? `Fixed sizes available on the ${tier} plan. Every running second is billed.`
                      : 'Pick a preset or use custom CPU/Memory/Disk values below'}
                  </p>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  {!managedCloud && (
                    <>
                      <div className="space-y-1.5">
                        <Label
                          htmlFor="cpuLimit"
                          className="text-xs text-white/60 light:text-black/60"
                        >
                          CPU Limit
                        </Label>
                        <Input
                          id="cpuLimit"
                          type="number"
                          value={cpuLimit}
                          onChange={(e) =>
                            setCpuLimit(clamp(Number(e.target.value), 0.5, 16))
                          }
                          min={0.5}
                          max={16}
                          step={0.5}
                          className="w-full"
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label
                          htmlFor="memoryMb"
                          className="text-xs text-white/60 light:text-black/60"
                        >
                          Memory (MB)
                        </Label>
                        <Input
                          id="memoryMb"
                          type="number"
                          value={memoryMb}
                          onChange={(e) =>
                            setMemoryMb(
                              clamp(Number(e.target.value), 128, 32768),
                            )
                          }
                          min={128}
                          max={32768}
                          step={64}
                          className="w-full"
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label
                          htmlFor="diskMb"
                          className="text-xs text-white/60 light:text-black/60"
                        >
                          Disk (MB)
                        </Label>
                        <Input
                          id="diskMb"
                          type="number"
                          value={diskMb}
                          onChange={(e) =>
                            setDiskMb(
                              clamp(Number(e.target.value), 256, 1048576),
                            )
                          }
                          min={256}
                          max={1048576}
                          step={256}
                          className="w-full"
                        />
                        {sandboxPricing.enabled &&
                          sandboxPricing.includedDiskGib > 0 && (
                            <p className="text-[11px] text-white/40 light:text-black/40">
                              First {sandboxPricing.includedDiskGib} GiB is
                              included in the compute rate
                            </p>
                          )}
                      </div>
                    </>
                  )}
                  <div className="space-y-1.5">
                    <Label
                      htmlFor="timeoutSeconds"
                      className="text-xs text-white/60 light:text-black/60"
                    >
                      Timeout (sec)
                    </Label>
                    <Input
                      id="timeoutSeconds"
                      type="number"
                      value={timeoutSeconds}
                      onChange={(e) =>
                        setTimeoutSeconds(
                          clamp(Number(e.target.value), 30, 3600),
                        )
                      }
                      min={30}
                      max={3600}
                      className="w-full"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label
                      htmlFor="networkMode"
                      className="text-xs text-white/60 light:text-black/60"
                    >
                      Network Mode
                    </Label>
                    <Select value={networkMode} onValueChange={setNetworkMode}>
                      <SelectTrigger id="networkMode" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="deny">Deny</SelectItem>
                        <SelectItem value="whitelist">Whitelist</SelectItem>
                        <SelectItem value="allow">Allow</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="space-y-1.5">
                    <Label
                      htmlFor="ttl"
                      className="text-xs text-white/60 light:text-black/60"
                    >
                      TTL (Time-to-Live)
                    </Label>
                    <Select
                      value={selectedTtl}
                      onValueChange={setSelectedTtl}
                      disabled={ttlOptions.length <= 1}
                    >
                      <SelectTrigger id="ttl" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {ttlOptions.map((opt) => (
                          <SelectItem key={opt.value} value={opt.value}>
                            {opt.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <p className="text-[11px] text-white/30 light:text-black/30">
                      {ttlOptions.length <= 1
                        ? `Fixed for ${tier} plan`
                        : 'How long the sandbox stays alive when idle'}
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-xs text-white/60 light:text-black/60">
                      SSH Access
                    </Label>
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        role="switch"
                        aria-checked={sshEnabled}
                        onClick={() => setSshEnabled(!sshEnabled)}
                        className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${sshEnabled ? 'bg-brand-secondary-600' : 'bg-brand-main-600'}`}
                      >
                        <span
                          className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${sshEnabled ? 'translate-x-4' : 'translate-x-0.5'}`}
                        />
                      </button>
                      <span className="text-xs text-white/50 light:text-black/50">
                        {sshEnabled ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                    <p className="text-[11px] text-white/30 light:text-black/30">
                      Enable SSH access to connect via terminal
                    </p>
                  </div>
                </div>

                {/* Labels */}
                <div className="space-y-2 mt-4">
                  <Label className="text-xs text-white/60 light:text-black/60">
                    Labels (optional)
                  </Label>
                  <p className="text-[11px] text-white/30 light:text-black/30">
                    Key-value metadata for filtering and tagging sandboxes.
                  </p>
                  <div className="flex gap-2">
                    <Input
                      className="h-7 text-xs bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 placeholder-white/20 light:placeholder-black/20"
                      placeholder="key"
                      value={labelKeyInput}
                      onChange={(e) => setLabelKeyInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && labelKeyInput.trim()) {
                          setLabels((prev) => [
                            ...prev,
                            {
                              key: labelKeyInput.trim(),
                              value: labelValueInput,
                            },
                          ])
                          setLabelKeyInput('')
                          setLabelValueInput('')
                        }
                      }}
                    />
                    <Input
                      className="h-7 text-xs bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 placeholder-white/20 light:placeholder-black/20"
                      placeholder="value"
                      value={labelValueInput}
                      onChange={(e) => setLabelValueInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && labelKeyInput.trim()) {
                          setLabels((prev) => [
                            ...prev,
                            {
                              key: labelKeyInput.trim(),
                              value: labelValueInput,
                            },
                          ])
                          setLabelKeyInput('')
                          setLabelValueInput('')
                        }
                      }}
                    />
                    <Button
                      type="button"
                      variant="outline"
                      className="h-7 px-2 text-xs border-brand-main-600 text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50"
                      onClick={() => {
                        if (labelKeyInput.trim()) {
                          setLabels((prev) => [
                            ...prev,
                            {
                              key: labelKeyInput.trim(),
                              value: labelValueInput,
                            },
                          ])
                          setLabelKeyInput('')
                          setLabelValueInput('')
                        }
                      }}
                      disabled={!labelKeyInput.trim()}
                    >
                      Add
                    </Button>
                  </div>
                  {labels.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {labels.map((l, i) => (
                        <span
                          key={i}
                          className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs bg-brand-main-700/50 text-brand-secondary-300 border border-brand-main-600/50 font-mono"
                        >
                          {l.key}={l.value}
                          <button
                            type="button"
                            onClick={() =>
                              setLabels((prev) =>
                                prev.filter((_, j) => j !== i),
                              )
                            }
                            className="text-white/40 light:text-black/40 hover:text-white light:hover:text-brand-main-50 ml-0.5"
                          >
                            <X className="size-2.5" />
                          </button>
                        </span>
                      ))}
                    </div>
                  )}
                </div>

                {networkMode === 'whitelist' && (
                  <div className="space-y-2 mt-4">
                    <Label className="text-xs text-white/60 light:text-black/60">
                      Allowed Hosts
                    </Label>
                    <p className="text-[11px] text-white/30 light:text-black/30">
                      Domains the sandbox can reach. Package registries (npm,
                      PyPI, cargo, Go) are always included by default.
                    </p>
                    <div className="flex gap-2">
                      <Input
                        value={hostInput}
                        onChange={(e) => setHostInput(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault()
                            const host = hostInput.trim().toLowerCase()
                            if (host && !allowedHosts.includes(host)) {
                              setAllowedHosts((prev) => [...prev, host])
                              setHostInput('')
                            }
                          }
                        }}
                        placeholder="e.g. api.example.com"
                        className="flex-1"
                      />
                      <Button
                        type="button"
                        variant="outline"
                        onClick={() => {
                          const host = hostInput.trim().toLowerCase()
                          if (host && !allowedHosts.includes(host)) {
                            setAllowedHosts((prev) => [...prev, host])
                            setHostInput('')
                          }
                        }}
                        disabled={!hostInput.trim()}
                      >
                        Add
                      </Button>
                    </div>
                    {allowedHosts.length > 0 && (
                      <div className="flex flex-wrap gap-1.5">
                        {allowedHosts.map((host) => (
                          <span
                            key={host}
                            className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-2 py-0.5 text-xs text-white/70 light:text-black/70"
                          >
                            {host}
                            <button
                              type="button"
                              onClick={() =>
                                setAllowedHosts((prev) =>
                                  prev.filter((h) => h !== host),
                                )
                              }
                              className="text-white/30 light:text-black/30 hover:text-red-400 light:hover:text-red-600"
                            >
                              <X className="w-3 h-3" />
                            </button>
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                )}

                {/* Network egress policy (block-all + CIDR allowlist) */}
                <div className="space-y-2 mt-4 pt-4 border-t border-brand-main-700/40">
                  <div className="flex items-center justify-between">
                    <div>
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Block all egress
                      </Label>
                      <p className="text-[11px] text-white/30 light:text-black/30 mt-0.5">
                        Deny outbound traffic. Loopback, link-local, and DNS
                        stay allowed.
                      </p>
                    </div>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={networkBlockAll}
                      onClick={() => setNetworkBlockAll(!networkBlockAll)}
                      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors ${networkBlockAll ? 'bg-brand-secondary-600' : 'bg-brand-main-600'}`}
                    >
                      <span
                        className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${networkBlockAll ? 'translate-x-4' : 'translate-x-0.5'}`}
                      />
                    </button>
                  </div>
                  {networkBlockAll && (
                    <div className="space-y-2">
                      <p className="text-[11px] text-white/30 light:text-black/30">
                        CIDR blocks to permit (max 10), e.g. 10.0.0.0/8.
                      </p>
                      <div className="flex gap-2">
                        <Input
                          value={cidrInput}
                          onChange={(e) => setCidrInput(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') {
                              e.preventDefault()
                              const cidr = cidrInput.trim()
                              if (
                                isLikelyCidr(cidr) &&
                                !allowCidrs.includes(cidr) &&
                                allowCidrs.length < 10
                              ) {
                                setAllowCidrs((prev) => [...prev, cidr])
                                setCidrInput('')
                              }
                            }
                          }}
                          placeholder="e.g. 10.0.0.0/8"
                          className="flex-1"
                        />
                        <Button
                          type="button"
                          variant="outline"
                          onClick={() => {
                            const cidr = cidrInput.trim()
                            if (
                              isLikelyCidr(cidr) &&
                              !allowCidrs.includes(cidr) &&
                              allowCidrs.length < 10
                            ) {
                              setAllowCidrs((prev) => [...prev, cidr])
                              setCidrInput('')
                            }
                          }}
                          disabled={
                            !isLikelyCidr(cidrInput) ||
                            allowCidrs.includes(cidrInput.trim()) ||
                            allowCidrs.length >= 10
                          }
                        >
                          Add
                        </Button>
                      </div>
                      {allowCidrs.length > 0 && (
                        <div className="flex flex-wrap gap-1.5">
                          {allowCidrs.map((cidr) => (
                            <span
                              key={cidr}
                              className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-2 py-0.5 text-xs text-white/70 light:text-black/70 font-mono"
                            >
                              {cidr}
                              <button
                                type="button"
                                onClick={() =>
                                  setAllowCidrs((prev) =>
                                    prev.filter((c) => c !== cidr),
                                  )
                                }
                                className="text-white/30 light:text-black/30 hover:text-red-400 light:hover:text-red-600"
                              >
                                <X className="w-3 h-3" />
                              </button>
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>

                {/* GitHub source (optional) */}
                <div className="space-y-3 mt-4 pt-4 border-t border-brand-main-700/40">
                  <div>
                    <Label className="text-xs text-white/60 light:text-black/60">
                      GitHub Source (optional)
                    </Label>
                    <p className="text-[11px] text-white/30 light:text-black/30 mt-0.5">
                      Clone a repository into the sandbox at startup.
                    </p>
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-[11px] text-white/50 light:text-black/50">
                      Installation
                    </Label>
                    <Select
                      value={gitInstallationId || '__none__'}
                      onValueChange={(value) => {
                        setGitInstallationId(value === '__none__' ? '' : value)
                        setGitRepoUrl('')
                        setGitBranch('')
                      }}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue
                          placeholder={
                            installationsLoading
                              ? 'Loading installations...'
                              : 'Select installation'
                          }
                        />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">None</SelectItem>
                        {gitHubInstallations.map((inst) => (
                          <SelectItem
                            key={inst.installationId}
                            value={String(inst.installationId)}
                          >
                            {inst.accountLogin} ({inst.accountType})
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {gitHubInstallations.length === 0 &&
                      !installationsLoading && (
                        <p className="text-[11px] text-white/35 light:text-black/35">
                          No linked GitHub installations. Connect GitHub in
                          Settings → Integrations.
                        </p>
                      )}
                  </div>
                  {selectedInstallationId > 0 && (
                    <div className="space-y-1.5">
                      <Label className="text-[11px] text-white/50 light:text-black/50">
                        Repository
                      </Label>
                      <Select
                        value={
                          selectedRepoInList && parsedRepo
                            ? parsedRepo.fullName
                            : '__custom__'
                        }
                        onValueChange={(value) => {
                          if (value === '__custom__') return
                          setGitRepoUrl(value)
                          const repo = gitHubRepos.find(
                            (r) => r.fullName === value,
                          )
                          setGitBranch(repo?.defaultBranch || '')
                        }}
                      >
                        <SelectTrigger className="w-full">
                          <SelectValue
                            placeholder={
                              reposLoading
                                ? 'Loading repositories...'
                                : 'Select repository'
                            }
                          />
                        </SelectTrigger>
                        <SelectContent>
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
                      <Input
                        value={gitRepoUrl}
                        onChange={(e) => setGitRepoUrl(e.target.value)}
                        placeholder="owner/repo or https://github.com/owner/repo"
                        className="w-full"
                      />
                    </div>
                  )}
                  {selectedInstallationId > 0 && parsedRepo && (
                    <div className="space-y-1.5">
                      <Label className="text-[11px] text-white/50 light:text-black/50">
                        Branch
                      </Label>
                      <Select
                        value={gitBranch || '__default__'}
                        onValueChange={(value) =>
                          setGitBranch(value === '__default__' ? '' : value)
                        }
                      >
                        <SelectTrigger className="w-full">
                          <SelectValue
                            placeholder={
                              branchesLoading
                                ? 'Loading branches...'
                                : 'Select branch'
                            }
                          />
                        </SelectTrigger>
                        <SelectContent>
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
                </div>

                {/* Object-storage mounts */}
                <div className="space-y-3 mt-4 pt-4 border-t border-brand-main-700/40">
                  <div className="flex items-center justify-between">
                    <div>
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Storage Mounts (optional)
                      </Label>
                      <p className="text-[11px] text-white/30 light:text-black/30 mt-0.5">
                        Mount S3/R2/GCS/Azure buckets into the sandbox
                        filesystem.
                      </p>
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      className="h-7 px-2 text-xs"
                      onClick={() =>
                        setMounts((prev) => [...prev, emptyMount()])
                      }
                    >
                      Add mount
                    </Button>
                  </div>
                  {mounts.map((mount, idx) => (
                    <div
                      key={idx}
                      className="space-y-2 rounded-md border border-brand-main-700/60 bg-brand-main-900/25 p-3"
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-[11px] font-medium text-white/50 light:text-black/50">
                          Mount {idx + 1}
                        </span>
                        <button
                          type="button"
                          onClick={() =>
                            setMounts((prev) =>
                              prev.filter((_, i) => i !== idx),
                            )
                          }
                          className="text-white/30 light:text-black/30 hover:text-red-400 light:hover:text-red-600"
                        >
                          <X className="w-3.5 h-3.5" />
                        </button>
                      </div>
                      <div className="grid grid-cols-2 gap-2">
                        <div className="space-y-1">
                          <Label className="text-[11px] text-white/50 light:text-black/50">
                            Provider
                          </Label>
                          <Select
                            value={mount.type}
                            onValueChange={(value) =>
                              updateMount(idx, { type: value })
                            }
                          >
                            <SelectTrigger className="w-full">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {STORAGE_MOUNT_TYPES.map((t) => (
                                <SelectItem key={t.value} value={t.value}>
                                  {t.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                        <div className="space-y-1">
                          <Label className="text-[11px] text-white/50 light:text-black/50">
                            {mount.type === 'azure' ? 'Container' : 'Bucket'}
                          </Label>
                          <Input
                            value={mount.bucket}
                            onChange={(e) =>
                              updateMount(idx, { bucket: e.target.value })
                            }
                            placeholder="my-bucket"
                            className="w-full"
                          />
                        </div>
                        <div className="space-y-1">
                          <Label className="text-[11px] text-white/50 light:text-black/50">
                            Mount path
                          </Label>
                          <Input
                            value={mount.mountPath}
                            onChange={(e) =>
                              updateMount(idx, { mountPath: e.target.value })
                            }
                            placeholder="/mnt/data"
                            className="w-full"
                          />
                        </div>
                        <div className="space-y-1">
                          <Label className="text-[11px] text-white/50 light:text-black/50">
                            Subpath (optional)
                          </Label>
                          <Input
                            value={mount.subpath ?? ''}
                            onChange={(e) =>
                              updateMount(idx, { subpath: e.target.value })
                            }
                            placeholder="prefix/within/bucket"
                            className="w-full"
                          />
                        </div>
                        {(mount.type === 'r2' || mount.type === 's3') && (
                          <div className="space-y-1 col-span-2">
                            <Label className="text-[11px] text-white/50 light:text-black/50">
                              Endpoint (optional)
                            </Label>
                            <Input
                              value={mount.endpoint ?? ''}
                              onChange={(e) =>
                                updateMount(idx, { endpoint: e.target.value })
                              }
                              placeholder="https://<account>.r2.cloudflarestorage.com"
                              className="w-full"
                            />
                          </div>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          role="switch"
                          aria-checked={!!mount.readOnly}
                          onClick={() =>
                            updateMount(idx, { readOnly: !mount.readOnly })
                          }
                          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${mount.readOnly ? 'bg-brand-secondary-600' : 'bg-brand-main-600'}`}
                        >
                          <span
                            className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${mount.readOnly ? 'translate-x-4' : 'translate-x-0.5'}`}
                          />
                        </button>
                        <span className="text-xs text-white/50 light:text-black/50">
                          Read-only
                        </span>
                      </div>
                    </div>
                  ))}
                </div>

                {/* Integrations: computer use + Tailscale */}
                <div className="space-y-3 mt-4 pt-4 border-t border-brand-main-700/40">
                  <div className="flex items-center justify-between">
                    <div>
                      <Label className="text-xs text-white/60 light:text-black/60">
                        Computer Use
                      </Label>
                      <p className="text-[11px] text-white/30 light:text-black/30 mt-0.5">
                        Enable desktop/browser automation (screenshot, mouse,
                        keyboard).
                      </p>
                    </div>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={computerUse}
                      onClick={() => setComputerUse(!computerUse)}
                      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors ${computerUse ? 'bg-brand-secondary-600' : 'bg-brand-main-600'}`}
                    >
                      <span
                        className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${computerUse ? 'translate-x-4' : 'translate-x-0.5'}`}
                      />
                    </button>
                  </div>
                  <div className="space-y-1.5">
                    <Label
                      htmlFor="tailscaleAuthKey"
                      className="text-xs text-white/60 light:text-black/60"
                    >
                      Tailscale Auth Key (optional)
                    </Label>
                    <Input
                      id="tailscaleAuthKey"
                      type="password"
                      value={tailscaleAuthKey}
                      onChange={(e) => setTailscaleAuthKey(e.target.value)}
                      placeholder="tskey-auth-..."
                      className="w-full"
                      autoComplete="off"
                    />
                    <p className="text-[11px] text-white/30 light:text-black/30">
                      Joins the sandbox to your tailnet on startup.
                    </p>
                  </div>
                </div>
              </div>
            </div>

            {/* Right column — sticky pricing */}
            <div className="w-72 shrink-0">
              <div className="sticky top-6 rounded-md border border-brand-secondary-600/40 bg-brand-secondary-600/10 p-3">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-brand-secondary-300">
                    Estimated Compute Price
                  </p>
                </div>
                <p className="text-[11px] text-white/50 light:text-black/50 mt-1">
                  Tier: {tier}{' '}
                  {tierDiscountPercent > 0
                    ? `(discount ${tierDiscountPercent}%)`
                    : ''}
                </p>
                <div className="space-y-2 mt-3">
                  <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                    <p className="text-[11px] text-white/45 light:text-black/45">
                      Per hour
                    </p>
                    <p className="text-sm font-semibold text-white light:text-brand-main-50">
                      {formatCurrency(
                        estimatedPricing.hourly,
                        sandboxPricing.currency,
                      )}
                    </p>
                  </div>
                  <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                    <p className="text-[11px] text-white/45 light:text-black/45">
                      Per day
                    </p>
                    <p className="text-sm font-semibold text-white light:text-brand-main-50">
                      {formatCurrency(
                        estimatedPricing.daily,
                        sandboxPricing.currency,
                      )}
                    </p>
                  </div>
                  <div className="rounded bg-brand-main-900/40 px-2.5 py-2">
                    <p className="text-[11px] text-white/45 light:text-black/45">
                      Per month
                    </p>
                    <p className="text-sm font-semibold text-white light:text-brand-main-50">
                      {formatCurrency(
                        estimatedPricing.monthly,
                        sandboxPricing.currency,
                      )}
                    </p>
                  </div>
                </div>
                <p className="text-[11px] text-white/45 light:text-black/45 mt-2">
                  {sandboxPricing.enabled
                    ? `Estimate assumes ${hoursPerDay}h active/day. Billed on actual usage.`
                    : 'Pricing currently disabled in gateway config.'}
                </p>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between px-6 py-4 border-t border-brand-main-600">
        <div>
          {step === 2 && (
            <Button variant="outline" onClick={() => setStep(1)}>
              <Iconify.Icon
                icon="heroicons:arrow-left"
                className="size-4 mr-1.5"
              />
              Back
            </Button>
          )}
        </div>
        <div>
          {step === 1 ? (
            <Button disabled={!canContinue} onClick={() => setStep(2)}>
              Continue
              <Iconify.Icon
                icon="heroicons:arrow-right"
                className="size-4 ml-1.5"
              />
            </Button>
          ) : (
            <Button
              onClick={handleCreate}
              disabled={
                createMutation.isPending ||
                !sandboxBillingEnabled ||
                (managedCloud && selectedMachineProfile === 'custom')
              }
            >
              {createMutation.isPending
                ? isRecreate
                  ? 'Recreating...'
                  : 'Creating...'
                : isRecreate
                  ? 'Create new sandbox'
                  : 'Create Sandbox'}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
