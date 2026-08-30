import { useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  useSandboxInstances,
  useRecreateSandbox,
  useStopSandbox,
  useReviveSandbox,
  useTerminateSandbox,
  useDestroySandbox,
  useRecoverSandbox,
} from '@/hooks/deployments/use-sandbox'
import {
  useCreateSandboxSSHToken,
  useRevokeSandboxSSHToken,
  useSandboxSSHInfo,
  useSandboxSSHTokens,
} from '@/hooks/deployments/use-sandbox-ssh'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import type { SandboxInstance } from '@/server/sandbox'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { useNavigate, type NavigateOptions } from '@tanstack/react-router'
import {
  isSandboxIntermediate,
  isSandboxRunning,
  isSandboxStopped,
  sandboxLifecycle,
  sandboxStatusLabel,
} from './lifecycle'

const {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
  Input,
  Label,
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} = ui

function SSHAccessPanel({
  sandboxId,
  enabled,
}: {
  sandboxId: string
  enabled: boolean
}) {
  const { data: sshInfo, isLoading } = useSandboxSSHInfo(
    enabled ? sandboxId : undefined,
  )
  const { data: tokenData, isLoading: tokensLoading } = useSandboxSSHTokens(
    enabled ? sandboxId : undefined,
  )
  const createToken = useCreateSandboxSSHToken()
  const revokeToken = useRevokeSandboxSSHToken()
  const [expiresInMinutes, setExpiresInMinutes] = useState(60)
  const [createdCommand, setCreatedCommand] = useState('')
  const [createdToken, setCreatedToken] = useState('')
  const [createdTokenId, setCreatedTokenId] = useState('')
  const [createdTokenExpiresAt, setCreatedTokenExpiresAt] = useState('')
  const [copied, setCopied] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const nextExpiry = (tokenData?.tokens ?? [])
      .filter((token) => !token.revokedAt)
      .map((token) => Date.parse(token.expiresAt ?? ''))
      .filter((expiresAt) => Number.isFinite(expiresAt) && expiresAt > now)
      .sort((a, b) => a - b)[0]
    if (!nextExpiry) return

    const timeout = window.setTimeout(
      () => setNow(Date.now()),
      Math.max(0, nextExpiry - now + 1000),
    )
    return () => window.clearTimeout(timeout)
  }, [tokenData?.tokens, now])

  useEffect(() => {
    if (!createdTokenId) return
    const listedToken = tokenData?.tokens?.find(
      (token) => token.id === createdTokenId,
    )
    const createdTokenInactive =
      isTokenExpired(createdTokenExpiresAt, now) ||
      (listedToken
        ? !!listedToken.revokedAt || isTokenExpired(listedToken.expiresAt, now)
        : !!tokenData?.tokens)
    if (!createdTokenInactive) return
    setCreatedCommand('')
    setCreatedToken('')
    setCreatedTokenId('')
    setCreatedTokenExpiresAt('')
  }, [createdTokenExpiresAt, createdTokenId, now, tokenData?.tokens])

  if (!enabled) {
    return (
      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2.5">
        <div className="flex items-center gap-2 text-sm text-white/55 light:text-black/55">
          <Iconify.Icon
            icon="heroicons:key"
            className="size-4 text-white/30 light:text-black/30"
          />
          <span>SSH unlocks when the sandbox is running.</span>
        </div>
      </section>
    )
  }

  if (isLoading) {
    return (
      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2.5 text-sm text-white/40 light:text-black/40">
        Loading SSH access...
      </section>
    )
  }

  if (!sshInfo) {
    return (
      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 px-3 py-2.5">
        <p className="text-sm text-white/45 light:text-black/45">
          SSH is unavailable for this sandbox.
        </p>
      </section>
    )
  }

  const keyBasedEnabled = sshInfo.enabled
  const temporaryTokensEnabled =
    sshInfo.enabled || isKeyBasedOnlySSHBlock(sshInfo.disabledReason)
  const connStr = keyBasedEnabled ? formatSafeSSHCommand(sshInfo) : ''
  const parsed = parseSSHCommand(connStr)
  const user = parsed?.user ?? ''
  const host = parsed?.host ?? sshInfo.host ?? ''
  const shortLabel = user && host ? `${user}@${host}` : connStr

  const copyText = (text: string, message = 'Copied to clipboard') => {
    navigator.clipboard.writeText(text)
    setCopied(true)
    toast.success(message)
    setTimeout(() => setCopied(false), 2000)
  }

  const handleCreateToken = () => {
    createToken.mutate(
      { sandboxId, expiresInMinutes },
      {
        onSuccess: (data) => {
          setCreatedCommand(data.connectionString)
          setCreatedToken(data.rawToken)
          setCreatedTokenId(data.token.id)
          setCreatedTokenExpiresAt(data.token.expiresAt ?? '')
          copyText(data.connectionString, 'Temporary SSH command copied')
        },
        onError: (err) =>
          toast.error(`Failed to create SSH token: ${err.message}`),
      },
    )
  }

  const activeTokens =
    tokenData?.tokens?.filter(
      (token) => !token.revokedAt && !isTokenExpired(token.expiresAt, now),
    ) ?? []

  return (
    <div className="space-y-3">
      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <Label className="text-sm text-white light:text-brand-main-50">
              Key-based SSH
            </Label>
            <p className="mt-1 text-xs text-white/45 light:text-black/45">
              Uses your registered SSH keys for this sandbox.
            </p>
          </div>
          {keyBasedEnabled && (
            <button
              type="button"
              onClick={() => copyText(connStr, 'SSH command copied')}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-md border border-brand-main-600 bg-brand-main-800 px-2.5 py-1.5 text-xs text-white/65 light:text-black/65 transition-colors hover:border-brand-secondary-500/40 hover:text-white light:hover:text-brand-main-50"
            >
              <Iconify.Icon
                icon={
                  copied ? 'heroicons:check' : 'heroicons:clipboard-document'
                }
                className="size-3.5"
              />
              Copy
            </button>
          )}
        </div>
        {keyBasedEnabled ? (
          <code className="block overflow-x-auto whitespace-nowrap rounded-md border border-brand-main-700 bg-black/35 px-3 py-2.5 font-mono text-xs text-brand-secondary-200 scrollbar-macos">
            {connStr}
          </code>
        ) : (
          <div className="rounded-md border border-dashed border-brand-main-700 bg-black/15 px-3 py-2.5 text-xs text-white/40 light:text-black/40">
            {sshInfo.disabledReason ||
              'Add an SSH key to use persistent key-based SSH.'}
          </div>
        )}
        {keyBasedEnabled && shortLabel && (
          <div className="mt-3 flex items-center gap-2 text-[11px] text-white/35 light:text-black/35">
            <Iconify.Icon
              icon="heroicons:identification"
              className="size-3.5"
            />
            <span className="font-mono">{shortLabel}</span>
          </div>
        )}
      </section>

      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3">
        <div className="mb-3">
          <Label className="text-sm text-white light:text-brand-main-50">
            Temporary token
          </Label>
          <p className="mt-1 text-xs text-white/45 light:text-black/45">
            Generate a one-time command for short-lived debugging access. No
            registered SSH key required.
          </p>
        </div>
        <div className="flex items-end gap-3">
          <div className="min-w-0 flex-1 space-y-1.5">
            <Label
              htmlFor={`ssh-token-expiry-${sandboxId}`}
              className="text-xs text-white/55 light:text-black/55"
            >
              Expires in
            </Label>
            <div className="relative">
              <Input
                id={`ssh-token-expiry-${sandboxId}`}
                type="number"
                min={1}
                max={1440}
                value={expiresInMinutes}
                onChange={(e) =>
                  setExpiresInMinutes(clampTokenMinutes(Number(e.target.value)))
                }
                className="h-9 pr-12 font-mono"
              />
              <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-white/35 light:text-black/35">
                min
              </span>
            </div>
          </div>
          <Button
            onClick={handleCreateToken}
            disabled={!temporaryTokensEnabled || createToken.isPending}
            className="h-9 shrink-0"
          >
            {createToken.isPending ? 'Creating...' : 'Create'}
          </Button>
        </div>
        {!temporaryTokensEnabled && (
          <div className="mt-3 rounded-md border border-dashed border-brand-main-700 bg-black/15 px-3 py-2 text-xs text-white/40 light:text-black/40">
            {sshInfo.disabledReason ||
              'Temporary SSH tokens are unavailable for this sandbox.'}
          </div>
        )}

        {createdCommand && (
          <div className="mt-4 rounded-lg border border-brand-secondary-500/25 bg-brand-secondary-600/10 p-3">
            <div className="mb-2 flex items-start gap-2 text-xs text-brand-secondary-100/80">
              <Iconify.Icon
                icon="heroicons:exclamation-circle"
                className="mt-0.5 size-4 shrink-0 text-brand-secondary-300"
              />
              Run the command, then paste the token when SSH prompts for it. The
              token cannot be viewed again.
            </div>
            <button
              type="button"
              onClick={() =>
                copyText(createdCommand, 'Temporary SSH command copied')
              }
              className="group flex w-full items-center gap-2 rounded-md border border-brand-secondary-500/20 bg-brand-main-950/35 px-3 py-2 text-left font-mono text-xs text-brand-secondary-50/90 transition-colors hover:border-brand-secondary-300/40"
            >
              <span className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap scrollbar-macos">
                {createdCommand}
              </span>
              <Iconify.Icon
                icon="heroicons:clipboard-document"
                className="size-4 shrink-0 text-brand-secondary-200/60 group-hover:text-brand-secondary-100"
              />
            </button>
            {createdToken && (
              <button
                type="button"
                onClick={() =>
                  copyText(createdToken, 'Temporary SSH token copied')
                }
                className="group mt-2 flex w-full items-center gap-2 rounded-md border border-brand-secondary-500/20 bg-brand-main-950/35 px-3 py-2 text-left font-mono text-xs text-brand-secondary-50/90 transition-colors hover:border-brand-secondary-300/40"
              >
                <span className="text-brand-secondary-200/55">Token</span>
                <span className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap scrollbar-macos">
                  {createdToken}
                </span>
                <Iconify.Icon
                  icon="heroicons:clipboard-document"
                  className="size-4 shrink-0 text-brand-secondary-200/60 group-hover:text-brand-secondary-100"
                />
              </button>
            )}
          </div>
        )}
      </section>

      <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <Label className="text-sm text-white light:text-brand-main-50">
              Active tokens
            </Label>
            <p className="mt-1 text-xs text-white/45 light:text-black/45">
              Revoke tokens when a handoff is complete.
            </p>
          </div>
          <span className="text-xs text-white/30 light:text-black/30">
            {activeTokens.length} active
          </span>
        </div>
        <div className="max-h-52 space-y-2 overflow-auto pr-1 scrollbar-macos">
          {tokensLoading ? (
            <div className="rounded-md border border-brand-main-700 bg-brand-main-900/40 px-3 py-3 text-xs text-white/35 light:text-black/35">
              Loading tokens...
            </div>
          ) : activeTokens.length === 0 ? (
            <div className="rounded-md border border-dashed border-brand-main-700 bg-black/15 px-3 py-4 text-center text-xs text-white/35 light:text-black/35">
              No temporary SSH tokens are active.
            </div>
          ) : (
            activeTokens.map((token) => (
              <div
                key={token.id}
                className="flex items-center gap-3 rounded-md border border-brand-main-700 bg-black/20 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="font-mono text-xs text-white/70 light:text-black/70">
                    {token.tokenPrefix}...
                  </div>
                  <div className="mt-0.5 text-[11px] text-white/30 light:text-black/30">
                    Expires {formatTokenTime(token.expiresAt)}
                    {token.lastUsedAt
                      ? ` · used ${formatTokenTime(token.lastUsedAt)}`
                      : ''}
                  </div>
                </div>
                <button
                  type="button"
                  disabled={revokeToken.isPending}
                  onClick={() =>
                    revokeToken.mutate(
                      { sandboxId, tokenId: token.id },
                      {
                        onSuccess: () => {
                          if (createdTokenId === token.id) {
                            setCreatedCommand('')
                            setCreatedToken('')
                            setCreatedTokenId('')
                            setCreatedTokenExpiresAt('')
                          }
                          toast.success('SSH token revoked')
                        },
                        onError: (err) =>
                          toast.error(`Failed to revoke token: ${err.message}`),
                      },
                    )
                  }
                  className="rounded border border-red-500/20 px-2 py-1 text-xs text-red-300 light:text-red-600 hover:bg-red-500/10 disabled:opacity-50"
                >
                  Revoke
                </button>
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}

function isKeyBasedOnlySSHBlock(reason?: string): boolean {
  const normalized = reason?.toLowerCase() ?? ''
  return (
    normalized.includes('no ssh key') ||
    normalized.includes('ssh access has not been granted')
  )
}

function isTokenExpired(expiresAt: string | undefined, now: number): boolean {
  if (!expiresAt) return false
  const expiresAtMs = Date.parse(expiresAt)
  return Number.isFinite(expiresAtMs) && expiresAtMs <= now
}

function clampTokenMinutes(value: number): number {
  if (!Number.isFinite(value)) return 60
  return Math.min(1440, Math.max(1, Math.round(value)))
}

function formatTokenTime(value?: string): string {
  if (!value) return 'unknown'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'unknown'
  return date.toLocaleString()
}

function simplifySSHCommand(value: string): string {
  return value
    .replace(/\s+-o\s+StrictHostKeyChecking=no/g, '')
    .replace(/\s+-o\s+UserKnownHostsFile=\/dev\/null/g, '')
    .trim()
}

function isValidSSHUser(value?: string): boolean {
  return !!value && /^[A-Za-z0-9._-]+$/.test(value)
}

function parseSSHCommand(
  value: string,
): { user: string; host: string; port?: string } | null {
  const match = value.match(/^ssh\s+([^\s@]+)@([^\s]+)(?:\s+-p\s+(\d+))?$/)
  if (!match) return null
  return { user: match[1], host: match[2], port: match[3] }
}

function formatSafeSSHCommand(sshInfo: {
  connectionString?: string
  shortCode?: string
  sandboxId?: string
  host?: string
  port?: number
}): string {
  const existing = simplifySSHCommand(sshInfo.connectionString ?? '')
  const parsed = parseSSHCommand(existing)
  if (parsed && isValidSSHUser(parsed.user)) return existing
  const user = isValidSSHUser(sshInfo.shortCode)
    ? sshInfo.shortCode
    : isValidSSHUser(sshInfo.sandboxId)
      ? sshInfo.sandboxId
      : undefined
  if (!user || !sshInfo.host) return existing
  return formatSSHCommand(user, sshInfo.host, sshInfo.port)
}

function formatSSHCommand(user: string, host: string, port?: number): string {
  return `ssh ${user}@${host}${port && port !== 22 ? ` -p ${port}` : ''}`
}

// All sandbox state badges live inside the brand palette. Distinction is by
// fill intensity (active states pop, terminal states fade), not by hue. We
// avoid hard-coded greens/yellows/reds to keep the surface coherent.
const STATUS_STYLES: Record<string, string> = {
  pending:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  running:
    'bg-brand-secondary-400/20 text-brand-secondary-200 border-brand-secondary-400/40',
  stopped:
    'bg-brand-main-600/40 text-white/55 light:text-black/55 border-brand-main-500/40',
  failed:
    'bg-brand-main-600/40 text-white/70 light:text-black/70 border-brand-main-500/50',
}

const LIFECYCLE_STYLES: Record<string, string> = {
  pending:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  provisioning:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  creating:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  running:
    'bg-brand-secondary-400/20 text-brand-secondary-200 border-brand-secondary-400/40',
  stopping:
    'bg-brand-secondary-400/10 text-brand-secondary-300 border-brand-secondary-400/25',
  stopped:
    'bg-brand-main-600/40 text-white/55 light:text-black/55 border-brand-main-500/40',
  restoring:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  archiving:
    'bg-brand-main-700/50 text-white/40 light:text-black/40 border-brand-main-600/40',
  archived:
    'bg-brand-main-700/60 text-white/35 light:text-black/35 border-brand-main-600/35',
  deleting:
    'bg-brand-main-600/50 text-white/65 light:text-black/65 border-brand-main-500/50',
  deleted:
    'bg-brand-main-600/40 text-white/45 light:text-black/45 border-brand-main-500/40',
  failed:
    'bg-brand-main-600/40 text-white/70 light:text-black/70 border-brand-main-500/50',
  // Reconciler vocabulary (sleeping = stopped; reviving/terminating
  // mirror restoring/deleting) plus the recoverable error state, which
  // pops harder than terminal states while staying in the palette.
  sleeping:
    'bg-brand-main-600/40 text-white/55 light:text-black/55 border-brand-main-500/40',
  reviving:
    'bg-brand-secondary-400/15 text-brand-secondary-200 border-brand-secondary-400/30',
  terminating:
    'bg-brand-main-600/50 text-white/65 light:text-black/65 border-brand-main-500/50',
  terminated:
    'bg-brand-main-600/40 text-white/45 light:text-black/45 border-brand-main-500/40',
  error:
    'bg-brand-secondary-500/15 text-brand-secondary-100 border-brand-secondary-500/50',
}

function formatTimeUntil(dateStr: string): string {
  const target = new Date(dateStr).getTime()
  const now = Date.now()
  const diff = target - now
  if (diff <= 0) return 'expired'
  const days = Math.floor(diff / (1000 * 60 * 60 * 24))
  const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60))
  if (days > 0) return `${days}d ${hours}h`
  const mins = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60))
  return hours > 0 ? `${hours}h ${mins}m` : `${mins}m`
}

function formatDateTime(value?: string): string {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--'
  if (date.getFullYear() < 2000) return '--'
  return date.toLocaleString()
}

function sortSandboxInstancesNewestFirst(
  instances: SandboxInstance[],
): SandboxInstance[] {
  return [...instances].sort((a, b) => {
    const aCreated = Date.parse(a.createdAt || '')
    const bCreated = Date.parse(b.createdAt || '')
    const aTime = Number.isFinite(aCreated) ? aCreated : 0
    const bTime = Number.isFinite(bCreated) ? bCreated : 0
    if (aTime !== bTime) return bTime - aTime
    return b.id.localeCompare(a.id)
  })
}

function sandboxDisplayName(inst: SandboxInstance): string {
  return inst.name?.trim() || inst.shortCode || inst.sessionId || inst.id
}

function configNumber(
  config: Record<string, unknown> | undefined,
  keys: string[],
): number | undefined {
  if (!config) return undefined
  for (const key of keys) {
    const value = config[key]
    if (typeof value === 'number' && Number.isFinite(value)) return value
    if (typeof value === 'string') {
      const parsed = Number(value)
      if (Number.isFinite(parsed)) return parsed
    }
  }
  return undefined
}

function configString(
  config: Record<string, unknown> | undefined,
  keys: string[],
): string | undefined {
  if (!config) return undefined
  for (const key of keys) {
    const value = config[key]
    if (typeof value === 'string' && value.trim()) return value
    if (typeof value === 'number' && Number.isFinite(value))
      return String(value)
  }
  return undefined
}

function formatResourceMb(value?: number): string | undefined {
  if (!value) return undefined
  if (value >= 1024) {
    const gib = value / 1024
    return `${Number.isInteger(gib) ? gib : gib.toFixed(1)} GiB`
  }
  return `${value} MiB`
}

function sandboxResourceParts(inst: SandboxInstance): string[] {
  const cpu = configNumber(inst.config, [
    'cpuLimit',
    'cpu_limit',
    'cpu',
    'cpus',
  ])
  const memory = configNumber(inst.config, [
    'memoryMb',
    'memory_mb',
    'memory',
    'memoryLimitMb',
    'memory_limit_mb',
  ])
  const disk = configNumber(inst.config, [
    'diskMb',
    'disk_mb',
    'disk',
    'diskSizeMb',
    'disk_size_mb',
  ])
  return [
    cpu ? `${cpu} vCPU` : undefined,
    formatResourceMb(memory),
    formatResourceMb(disk),
  ].filter(Boolean) as string[]
}

function ResourceSegments({ parts }: { parts: string[] }) {
  if (parts.length === 0)
    return <span className="text-xs text-white/20 light:text-black/20">--</span>
  return (
    <div
      className="flex min-w-0 items-center overflow-hidden font-mono text-xs text-white/65 light:text-black/65"
      title={parts.join(' | ')}
    >
      {parts.map((part, index) => (
        <span key={part} className="flex min-w-0 items-center">
          {index > 0 && (
            <span className="mx-2 h-4 w-px shrink-0 bg-white/10 light:bg-black/10" />
          )}
          <span className="truncate">{part}</span>
        </span>
      ))}
    </div>
  )
}

function ResourcePills({ parts }: { parts: string[] }) {
  const icons = [
    'heroicons:cpu-chip',
    'heroicons:server-stack',
    'heroicons:archive-box',
  ]
  if (parts.length === 0)
    return <span className="text-sm text-white/35 light:text-black/35">--</span>
  return (
    <div className="flex flex-wrap gap-2">
      {parts.map((part, index) => (
        <span
          key={part}
          className="inline-flex items-center gap-1.5 rounded-md border border-brand-main-700/50 bg-brand-main-900/60 px-2.5 py-1 text-xs text-brand-main-100"
        >
          <Iconify.Icon
            icon={icons[index] ?? 'heroicons:cube'}
            className="size-3.5 text-brand-secondary-300"
          />
          {part}
        </span>
      ))}
    </div>
  )
}

function formatDurationSeconds(value?: number): string {
  if (!value || value <= 0) return 'Disabled'
  if (value < 60) return `${value}s`
  if (value < 3600) return `${Math.round(value / 60)}m`
  if (value < 86400) return `${Math.round(value / 3600)}h`
  return `${Math.round(value / 86400)}d`
}

function formatComputeSeconds(value = 0): string {
  if (value < 60) return `${Math.max(0, value)}s`
  const hours = Math.floor(value / 3600)
  const minutes = Math.floor((value % 3600) / 60)
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`
}

function formatComputeCost(value = 0): string {
  if (value <= 0) return '$0.00'
  if (value < 0.01) return '<$0.01'
  return value < 1 ? `$${value.toFixed(3)}` : `$${value.toFixed(2)}`
}

function formatDays(value?: number): string {
  if (value === undefined || value < 0) return 'Disabled'
  if (value === 0) return 'Disabled'
  return `${value}d`
}

function formatRelativeTime(value?: string): string {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return '--'
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

function InfoLine({
  label,
  value,
  mono,
  copyValue,
}: {
  label: string
  value?: string
  mono?: boolean
  copyValue?: string
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-1">
      <span className="text-xs text-brand-main-300">{label}</span>
      <div className="flex min-w-0 items-center gap-2 text-right">
        <span
          className={`min-w-0 truncate text-sm text-brand-main-100 ${mono ? 'font-mono text-xs' : ''}`}
          title={value}
        >
          {value || '--'}
        </span>
        {copyValue && (
          <button
            type="button"
            onClick={() => {
              navigator.clipboard.writeText(copyValue)
              toast.success('Copied to clipboard')
            }}
            className="shrink-0 text-brand-main-300 transition-colors hover:text-white light:hover:text-brand-main-50"
          >
            <Iconify.Icon
              icon="heroicons:clipboard-document"
              className="size-4"
            />
          </button>
        )}
      </div>
    </div>
  )
}

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <div className="text-[11px] uppercase tracking-wider text-white/45 light:text-black/45">
      {children}
    </div>
  )
}

function SandboxStatusBadge({ instance }: { instance: SandboxInstance }) {
  const lifecycle = sandboxLifecycle(instance)
  if (lifecycle && lifecycle !== 'running') {
    return (
      <span
        className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${LIFECYCLE_STYLES[lifecycle] ?? LIFECYCLE_STYLES.running}`}
      >
        {sandboxStatusLabel(instance)}
      </span>
    )
  }
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${STATUS_STYLES[instance.status] ?? STATUS_STYLES.stopped}`}
    >
      {instance.status}
    </span>
  )
}

function DetailRow({ label, value }: { label: string; value?: string }) {
  return (
    <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-3 border-b border-white/5 light:border-black/5 py-2 last:border-b-0">
      <div className="text-xs text-white/35 light:text-black/35">{label}</div>
      <div
        className="min-w-0 truncate font-mono text-xs text-white/65 light:text-black/65"
        title={value}
      >
        {value || '--'}
      </div>
    </div>
  )
}

function SandboxDetailsSheet({
  instance,
  open,
  onOpenChange,
  onOpenShell,
  onViewLogs,
  onEditConfig,
}: {
  instance: SandboxInstance | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onOpenShell: (instance: SandboxInstance) => void
  onViewLogs: (instance: SandboxInstance) => void
  onEditConfig: (instance: SandboxInstance) => void
}) {
  const [activeTab, setActiveTab] = useState('overview')
  const labelEntries = Object.entries(instance?.labels ?? {})
  const configEntries = Object.entries(instance?.config ?? {})
  const isRunning = instance ? isSandboxRunning(instance) : false
  const lifecycle = instance ? sandboxLifecycle(instance) : undefined
  const isStopped = instance ? isSandboxStopped(instance) : false
  const isFailed = instance?.lifecycleState
    ? lifecycle === 'failed'
    : instance?.status === 'failed'
  const isDeleted = lifecycle === 'deleted'
  const title = instance ? sandboxDisplayName(instance) : 'Sandbox details'
  const resourceParts = instance ? sandboxResourceParts(instance) : []
  const stateLabel = lifecycle || instance?.status || '--'
  const region =
    configString(instance?.config, ['region', 'regionSlug', 'region_slug']) ??
    '--'

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-[620px] h-[100vh] flex flex-col overflow-hidden"
      >
        {instance && (
          <>
            <SheetHeader className="min-w-0 flex-1 items-center justify-between gap-3 pr-2">
              <div className="min-w-0 flex-1 py-2">
                <SheetTitle className="truncate text-base leading-5">
                  {title}
                </SheetTitle>
                <SheetDescription className="mt-1 truncate text-xs text-white/45 light:text-black/45">
                  {instance.shortCode ? `${instance.shortCode} · ` : ''}
                  {instance.image}
                </SheetDescription>
              </div>
              <div className="shrink-0">
                <SandboxStatusBadge instance={instance} />
              </div>
            </SheetHeader>

            <SheetBody className="py-4 flex-1 min-h-0 overflow-hidden flex flex-col">
              <Tabs
                value={activeTab}
                onValueChange={setActiveTab}
                className="flex flex-col flex-1 min-h-0 space-y-4"
              >
                <TabsList className="h-auto w-fit shrink-0 gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
                  <TabsTrigger
                    className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                    value="overview"
                  >
                    Overview
                  </TabsTrigger>
                  <TabsTrigger
                    className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                    value="access"
                  >
                    SSH
                  </TabsTrigger>
                </TabsList>

                <TabsContent
                  value="overview"
                  className="mt-0 flex-1 min-h-0 space-y-4 overflow-y-auto pb-4 scrollbar-macos data-[state=inactive]:hidden"
                >
                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-3">
                    <InfoLine
                      label="Name / UUID"
                      value={instance.id}
                      mono
                      copyValue={instance.id}
                    />
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex items-center gap-2 text-sm text-brand-main-100">
                        <span
                          className={`size-2 rounded-full ${isRunning ? 'bg-brand-secondary-300' : 'bg-white/35 light:bg-black/35'}`}
                        />
                        <span className="capitalize">{stateLabel}</span>
                      </div>
                      <div className="flex overflow-hidden rounded-md border border-brand-main-600">
                        <button
                          type="button"
                          disabled={!isRunning}
                          onClick={() => onOpenShell(instance)}
                          className="inline-flex h-8 items-center gap-1.5 border-r border-brand-main-600 px-2.5 text-xs text-brand-main-100 hover:bg-brand-main-800 disabled:cursor-not-allowed disabled:opacity-40"
                        >
                          <Iconify.Icon
                            icon="heroicons:command-line"
                            className="size-3.5"
                          />
                          Shell
                        </button>
                        <button
                          type="button"
                          disabled={
                            !(isRunning || isStopped || isFailed || isDeleted)
                          }
                          onClick={() => onViewLogs(instance)}
                          className="inline-flex h-8 items-center justify-center border-r border-brand-main-600 px-2.5 text-brand-main-100 hover:bg-brand-main-800 disabled:cursor-not-allowed disabled:opacity-40"
                          title="View logs"
                        >
                          <Iconify.Icon
                            icon="heroicons:document-text"
                            className="size-3.5"
                          />
                        </button>
                        <button
                          type="button"
                          disabled={isDeleted}
                          onClick={() => onEditConfig(instance)}
                          className="inline-flex h-8 items-center justify-center px-2.5 text-brand-main-100 hover:bg-brand-main-800 disabled:cursor-not-allowed disabled:opacity-40"
                          title="Edit config"
                        >
                          <Iconify.Icon
                            icon="heroicons:cog-6-tooth"
                            className="size-3.5"
                          />
                        </button>
                      </div>
                    </div>
                  </section>

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-1">
                    <InfoLine
                      label="Region"
                      value={region}
                      copyValue={region !== '--' ? region : undefined}
                    />
                    <InfoLine label="Class" value="Container" />
                    <InfoLine
                      label="Snapshot"
                      value={instance.snapshotId || '--'}
                      mono
                    />
                    <InfoLine label="Preview access" value="Private" />
                  </section>

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-3">
                    <SectionTitle>Resources</SectionTitle>
                    <ResourcePills parts={resourceParts} />
                  </section>

                  <section className="rounded border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-2">
                    <SectionTitle>Compute usage</SectionTitle>
                    <InfoLine
                      label="Billing"
                      value={
                        instance.billingStartedAt
                          ? instance.billingEndedAt
                            ? 'Finalizing ledger · compute stopped'
                            : 'Accruing while VM is allocated'
                          : 'Not accruing'
                      }
                    />
                    {instance.billingStartedAt && (
                      <>
                        <InfoLine
                          label="Current window"
                          value={`${formatComputeSeconds(instance.currentComputeSeconds)} · ${formatComputeCost(instance.currentComputeCostUsd)}`}
                        />
                        <InfoLine
                          label={instance.billingEndedAt ? 'Ended' : 'Opened'}
                          value={formatDateTime(
                            instance.billingEndedAt ??
                              instance.billingStartedAt,
                          )}
                        />
                      </>
                    )}
                    <p className="pt-1 text-xs leading-relaxed text-white/50 light:text-black/55">
                      Idle time is included while the VM runs. Compute stops
                      when the sandbox sleeps; retained storage is separate.
                    </p>
                  </section>

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-2">
                    <SectionTitle>Lifecycle</SectionTitle>
                    <InfoLine
                      label="Auto-stop"
                      value={formatDurationSeconds(instance.idleRetentionSecs)}
                    />
                    <InfoLine
                      label="Auto-archive"
                      value={formatDays(instance.autoArchiveAfterDays)}
                    />
                    <InfoLine
                      label="Auto-delete"
                      value={formatDays(instance.autoDeleteAfterDays)}
                    />
                    {isStopped && instance.revivableUntil && (
                      <InfoLine
                        label="Revivable"
                        value={formatTimeUntil(instance.revivableUntil)}
                      />
                    )}
                  </section>

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 flex items-center justify-between gap-3">
                    <SectionTitle>SSH Access</SectionTitle>
                    <div className="flex overflow-hidden rounded-md border border-brand-main-600">
                      <button
                        type="button"
                        onClick={() => setActiveTab('access')}
                        className="inline-flex h-8 items-center gap-1.5 border-r border-brand-main-600 px-2.5 text-xs font-medium text-brand-main-100 hover:bg-brand-main-800"
                      >
                        <Iconify.Icon
                          icon="heroicons:key"
                          className="size-3.5"
                        />
                        Create
                      </button>
                      <button
                        type="button"
                        onClick={() => setActiveTab('access')}
                        className="inline-flex h-8 items-center gap-1.5 px-2.5 text-xs font-medium text-brand-main-100 hover:bg-brand-main-800"
                      >
                        <Iconify.Icon
                          icon="heroicons:user-minus"
                          className="size-3.5"
                        />
                        Revoke
                      </button>
                    </div>
                  </section>

                  {labelEntries.length > 0 && (
                    <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-3">
                      <SectionTitle>Labels</SectionTitle>
                      <div className="flex flex-wrap gap-1.5">
                        {labelEntries.map(([key, value]) => (
                          <span
                            key={key}
                            className="max-w-full overflow-hidden rounded border border-brand-main-600 bg-brand-main-800/70 font-mono text-xs text-white/70 light:text-black/70"
                            title={`${key}=${value}`}
                          >
                            <span className="inline-block border-r border-brand-main-600 px-2 py-1 text-white/40 light:text-black/40">
                              {key}
                            </span>
                            <span className="inline-block px-2 py-1 text-white/85 light:text-black/85">
                              {value}
                            </span>
                          </span>
                        ))}
                      </div>
                    </section>
                  )}

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 flex items-center justify-between gap-3">
                    <SectionTitle>Recordings</SectionTitle>
                    <button
                      type="button"
                      className="inline-flex items-center gap-1.5 text-xs font-medium text-brand-main-100 transition-colors hover:text-white light:hover:text-brand-main-50"
                    >
                      View
                      <Iconify.Icon
                        icon="heroicons:arrow-up-right"
                        className="size-3.5"
                      />
                    </button>
                  </section>

                  <section className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3 space-y-2">
                    <SectionTitle>Activity</SectionTitle>
                    <InfoLine
                      label="Created"
                      value={formatRelativeTime(instance.createdAt)}
                    />
                    <InfoLine
                      label="Last event"
                      value={formatRelativeTime(instance.lastUsedAt)}
                    />
                  </section>

                  <details className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3">
                    <summary className="cursor-pointer text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                      Technical details
                    </summary>
                    <div className="mt-3">
                      <DetailRow label="Sandbox" value={instance.id} />
                      <DetailRow label="Session" value={instance.sessionId} />
                      <DetailRow label="Tenant" value={instance.tenantId} />
                      <DetailRow
                        label="Container"
                        value={instance.containerId}
                      />
                      <DetailRow label="Agent" value={instance.agentId} />
                      <DetailRow
                        label="Expires"
                        value={formatDateTime(instance.expiresAt)}
                      />
                    </div>
                  </details>
                </TabsContent>

                <TabsContent
                  value="access"
                  className="mt-0 flex-1 min-h-0 space-y-4 overflow-y-auto pb-4 scrollbar-macos data-[state=inactive]:hidden"
                >
                  <SSHAccessPanel sandboxId={instance.id} enabled={isRunning} />
                  {configEntries.length > 0 && (
                    <details className="rounded-md border border-brand-main-800/70 bg-brand-main-900/30 p-3">
                      <summary className="cursor-pointer text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                        Sandbox config
                      </summary>
                      <div className="mt-3 max-h-52 overflow-auto rounded border border-brand-main-700 bg-black/25 p-3 scrollbar-macos">
                        <pre className="text-xs leading-relaxed text-white/55 light:text-black/55">
                          {JSON.stringify(instance.config, null, 2)}
                        </pre>
                      </div>
                    </details>
                  )}
                </TabsContent>
              </Tabs>
            </SheetBody>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

export function InstancesTab() {
  const { data, isLoading, error } = useSandboxInstances()
  const recreateMutation = useRecreateSandbox()
  const stopMutation = useStopSandbox()
  const reviveMutation = useReviveSandbox()
  const terminateMutation = useTerminateSandbox()
  const recoverMutation = useRecoverSandbox()
  const destroyMutation = useDestroySandbox()
  const navigate = useNavigate()
  // Holds the row queued for permanent deletion. Set when the user picks
  // Delete from the dropdown; the AlertDialog opens until confirmed/cancelled.
  const [deleteTarget, setDeleteTarget] = useState<SandboxInstance | null>(null)
  // Same pattern for Terminate — needs confirmation because it stops the
  // container/VM AND cascades to terminate any associated persistent agent.
  const [terminateTarget, setTerminateTarget] =
    useState<SandboxInstance | null>(null)
  const [selectedSandboxId, setSelectedSandboxId] = useState<string | null>(
    null,
  )
  const instances = useMemo(
    () => sortSandboxInstancesNewestFirst(data?.instances ?? []),
    [data?.instances],
  )
  const selectedSandbox =
    instances.find((inst) => inst.id === selectedSandboxId) ?? null

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading instances..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
        Error loading instances: {error.message}
      </div>
    )
  }

  const columns: ColumnConfig<SandboxInstance>[] = [
    {
      id: 'sandbox',
      header: 'Sandbox',
      width: 240,
      minWidth: 190,
      render: (inst) => (
        <div className="flex min-w-0 items-center gap-2">
          <span
            className={`size-2 shrink-0 rounded-full ${inst.agentHealthy === false ? 'bg-red-400' : isSandboxRunning(inst) ? 'bg-brand-secondary-300' : 'bg-white/25 light:bg-black/25'}`}
            title={
              inst.agentHealthy === false
                ? 'Agent unhealthy'
                : isSandboxRunning(inst)
                  ? 'Running'
                  : 'Not running'
            }
          />
          <span
            className="truncate text-sm font-medium text-white/85 light:text-black/85"
            title={sandboxDisplayName(inst)}
          >
            {sandboxDisplayName(inst)}
          </span>
          {inst.persistent && (
            <span className="shrink-0 rounded border border-brand-main-600 px-1.5 py-0.5 text-[10px] text-white/35 light:text-black/35">
              persistent
            </span>
          )}
        </div>
      ),
    },
    {
      id: 'status',
      header: 'State',
      width: 115,
      minWidth: 100,
      render: (inst) => <SandboxStatusBadge instance={inst} />,
    },
    {
      id: 'image',
      header: 'Image',
      width: 230,
      minWidth: 170,
      render: (inst) => (
        <span
          className="truncate font-mono text-xs text-white/70 light:text-black/70"
          title={inst.image}
        >
          {inst.image}
        </span>
      ),
    },
    {
      id: 'resources',
      header: 'Resources',
      width: 180,
      minWidth: 150,
      render: (inst) => {
        const parts = sandboxResourceParts(inst)
        return <ResourceSegments parts={parts} />
      },
    },
    {
      id: 'access',
      header: 'Access',
      width: 130,
      minWidth: 110,
      render: (inst) => {
        if (!isSandboxRunning(inst))
          return (
            <span className="text-xs text-white/20 light:text-black/20">
              --
            </span>
          )
        return (
          <span
            className="inline-flex max-w-full items-center gap-1.5 rounded border border-brand-secondary-400/20 bg-brand-secondary-400/10 px-2 py-1 font-mono text-xs text-brand-secondary-200"
            title="Open details for SSH commands"
          >
            <Iconify.Icon icon="heroicons:key" className="size-3.5 shrink-0" />
            <span className="truncate">{inst.shortCode || 'ssh ready'}</span>
          </span>
        )
      },
    },
    {
      id: 'compute',
      header: 'Compute now',
      width: 145,
      minWidth: 125,
      render: (inst) =>
        inst.billingStartedAt ? (
          <div className="min-w-0">
            <div className="text-sm text-brand-secondary-200">
              {formatComputeSeconds(inst.currentComputeSeconds)}
            </div>
            <div className="text-xs text-white/50 light:text-black/55">
              {formatComputeCost(inst.currentComputeCostUsd)} ·{' '}
              {inst.billingEndedAt ? 'finalizing' : 'accruing'}
            </div>
          </div>
        ) : (
          <span className="text-xs text-white/45 light:text-black/50">
            Not accruing
          </span>
        ),
    },
    {
      id: 'createdAt',
      header: 'Created',
      width: 150,
      minWidth: 120,
      render: (inst) => (
        <span className="text-sm text-white/50 light:text-black/50">
          {formatDateTime(inst.createdAt)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      width: 56,
      minWidth: 56,
      maxWidth: 56,
      resizable: false,
      render: (inst) => {
        const lifecycle = sandboxLifecycle(inst)
        const isRunning = isSandboxRunning(inst)
        const isStopped = isSandboxStopped(inst)
        // The backend writes 'terminated'; 'deleted' is tolerated for
        // forward compatibility with the public-label vocabulary.
        const isDeleted = lifecycle === 'terminated' || lifecycle === 'deleted'
        const isIntermediate = isSandboxIntermediate(inst)
        // Lifecycle is authoritative when present — falling back to
        // inst.status with an OR was the bug behind "I can delete
        // an already-deleted sandbox": the status column isn't
        // overwritten on delete, so a row that was failed→deleted
        // kept status='failed' and slipped past the showTerminate gate.
        const isFailed = inst.lifecycleState
          ? lifecycle === 'failed' || lifecycle === 'error'
          : inst.status === 'failed'

        // Intermediate states (creating/deleting/etc.) have no actions
        // — the Status column already conveys the in-flight state.
        if (isIntermediate) {
          return <div aria-hidden className="h-full w-full" />
        }

        const goShell: NavigateOptions = {
          to: '/deployments/sandboxes/$sandboxId',
          params: { sandboxId: inst.id },
          search: { tab: 'terminal' },
        }
        const goLogs: NavigateOptions = {
          to: '/deployments/sandboxes/$sandboxId',
          params: { sandboxId: inst.id },
          search: { tab: 'logs' },
        }
        // Sandbox config lives on the owning agent's read model for
        // agent-owned sandboxes (single source of truth), but orphan
        // sandboxes have no agent to route to. There's no in-place
        // update RPC either, so for orphans we send the user to the
        // create page in "recreate from" mode — it pre-fills the form
        // from this sandbox's config. Either way, "Edit configuration"
        // always lands the user somewhere they can change the shape.
        const goEditConfig: NavigateOptions = inst.agentId
          ? {
              to: '/deployments/agents/$agentId/settings',
              params: { agentId: inst.agentId },
              search: { section: 'environment' },
            }
          : { to: '/deployments/sandboxes/new', search: { from: inst.id } }

        const showShell = isRunning
        // Logs work live for running and historically for stopped/failed/deleted
        // (stream may be empty for deleted; the events tab covers history).
        const showLogs = isRunning || isStopped || isFailed || isDeleted
        // Edit configuration only makes sense for sandboxes that still
        // exist server-side — deleted rows have no live config to edit.
        const showEditConfig = !isDeleted
        const isArchived =
          inst.lifecycleState === 'archived' ||
          inst.lifecycleState === 'archiving'
        const showStop = isRunning
        const showRevive = isStopped && !isArchived
        const showRestore = isArchived
        // Recover re-enters convergence for error rows: the VM died
        // or convergence exhausted retries; desired_state preserved.
        const showRecover = lifecycle === 'error' || lifecycle === 'failed'
        // Recreate spawns a new sandbox from the stored config — works for any
        // row whose DB record still exists (legacy stopped/failed + deleted).
        const showRecreate =
          isDeleted ||
          (!inst.lifecycleState &&
            (inst.status === 'stopped' || inst.status === 'failed'))
        // Terminate acts on live rows; the same endpoint, called on a deleted
        // row, purges the DB record (sandbox_instances + child rows). Surface
        // that as "Delete" so the action label matches the effect.
        const showTerminate = isRunning || isStopped || isFailed
        const showDelete = isDeleted

        const hasViewActions = showShell || showLogs || showEditConfig
        const hasLifecycleActions =
          showStop || showRevive || showRecreate || showRestore || showRecover
        const hasDestructive = showTerminate || showDelete
        const hasAny = hasViewActions || hasLifecycleActions || hasDestructive

        if (!hasAny) {
          return <div className="flex items-center justify-end pr-1" />
        }

        const pending =
          stopMutation.isPending ||
          reviveMutation.isPending ||
          terminateMutation.isPending ||
          recreateMutation.isPending

        return (
          <div className="flex items-center justify-end" data-row-actions>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  aria-label="Sandbox actions"
                  disabled={pending}
                  className="p-1 rounded text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/60 transition-colors disabled:opacity-50 focus:outline-none focus:ring-1 focus:ring-brand-secondary-400/40"
                >
                  <Iconify.Icon
                    icon="heroicons:ellipsis-vertical"
                    className="size-4"
                  />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                sideOffset={4}
                data-row-actions
                onClick={(event) => event.stopPropagation()}
                className="w-44 bg-brand-main-800 border border-brand-main-600 text-brand-main-100 p-1 shadow-xl shadow-black/40"
              >
                <DropdownMenuItem
                  onSelect={() => setSelectedSandboxId(inst.id)}
                  className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                >
                  <Iconify.Icon icon="heroicons:eye" className="size-4" />
                  Quick view
                </DropdownMenuItem>
                {showShell && (
                  <DropdownMenuItem
                    onSelect={() => navigate(goShell)}
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:command-line"
                      className="size-4"
                    />
                    Open shell
                  </DropdownMenuItem>
                )}
                {showLogs && (
                  <DropdownMenuItem
                    onSelect={() => navigate(goLogs)}
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:document-text"
                      className="size-4"
                    />
                    View logs
                  </DropdownMenuItem>
                )}
                {showEditConfig && (
                  <DropdownMenuItem
                    onSelect={() => navigate(goEditConfig)}
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:cog-6-tooth"
                      className="size-4"
                    />
                    Edit configuration
                  </DropdownMenuItem>
                )}
                {hasViewActions && hasLifecycleActions && (
                  <DropdownMenuSeparator className="bg-brand-main-600" />
                )}
                {showStop && (
                  <DropdownMenuItem
                    onSelect={() =>
                      stopMutation.mutate(inst.id, {
                        onSuccess: () => toast.success('Sandbox stopped'),
                        onError: (e) =>
                          toast.error(`Failed to stop: ${e.message}`),
                      })
                    }
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon icon="heroicons:pause" className="size-4" />
                    Stop
                  </DropdownMenuItem>
                )}
                {showRevive && (
                  <DropdownMenuItem
                    onSelect={() =>
                      reviveMutation.mutate(inst.id, {
                        onSuccess: () => toast.success('Sandbox revived'),
                        onError: (e) =>
                          toast.error(`Failed to revive: ${e.message}`),
                      })
                    }
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon icon="heroicons:play" className="size-4" />
                    Revive
                  </DropdownMenuItem>
                )}
                {showRecover && (
                  <DropdownMenuItem
                    onSelect={() =>
                      recoverMutation.mutate(inst.id, {
                        onSuccess: () => toast.success('Recovery started'),
                        onError: (e) =>
                          toast.error(`Failed to recover: ${e.message}`),
                      })
                    }
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:arrow-path"
                      className="size-4"
                    />
                    Recover
                  </DropdownMenuItem>
                )}
                {showRestore && (
                  <DropdownMenuItem
                    onSelect={async () => {
                      try {
                        const { getApiBaseUrl } = await import('@/lib/api-url')
                        const res = await fetch(
                          `${getApiBaseUrl()}/v1/sandbox/instances/${inst.id}/restore`,
                          {
                            method: 'POST',
                            credentials: 'include',
                          },
                        )
                        if (!res.ok) throw new Error(await res.text())
                        toast.success('Sandbox restore initiated')
                      } catch (e) {
                        toast.error(
                          `Failed to restore: ${e instanceof Error ? e.message : 'unknown'}`,
                        )
                      }
                    }}
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:archive-box-arrow-down"
                      className="size-4"
                    />
                    Restore from Archive
                  </DropdownMenuItem>
                )}
                {showRecreate && (
                  <DropdownMenuItem
                    onSelect={() =>
                      recreateMutation.mutate(
                        { sandboxId: inst.id },
                        {
                          onSuccess: () => toast.success('Sandbox recreated'),
                          onError: () =>
                            toast.error('Failed to recreate sandbox'),
                        },
                      )
                    }
                    className="rounded text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 focus:bg-brand-main-700 focus:text-white light:focus:text-brand-main-50 cursor-pointer [&_svg]:text-brand-secondary-400"
                  >
                    <Iconify.Icon
                      icon="heroicons:arrow-path"
                      className="size-4"
                    />
                    Recreate
                  </DropdownMenuItem>
                )}
                {hasDestructive && (hasViewActions || hasLifecycleActions) && (
                  <DropdownMenuSeparator className="bg-brand-main-600" />
                )}
                {showTerminate && (
                  <DropdownMenuItem
                    onSelect={() => setTerminateTarget(inst)}
                    className="rounded text-red-400 light:text-red-600 hover:bg-red-500/10 hover:text-red-300 light:hover:text-red-600 focus:bg-red-500/10 focus:text-red-300 light:focus:text-red-600 cursor-pointer [&_svg]:text-red-400 light:[&_svg]:text-red-600 hover:[&_svg]:text-red-300 light:hover:[&_svg]:text-red-600"
                  >
                    <Iconify.Icon
                      icon="heroicons:x-circle"
                      className="size-4"
                    />
                    Terminate
                  </DropdownMenuItem>
                )}
                {showDelete && (
                  <DropdownMenuItem
                    onSelect={() => setDeleteTarget(inst)}
                    className="rounded text-red-400 light:text-red-600 hover:bg-red-500/10 hover:text-red-300 light:hover:text-red-600 focus:bg-red-500/10 focus:text-red-300 light:focus:text-red-600 cursor-pointer [&_svg]:text-red-400 light:[&_svg]:text-red-600 hover:[&_svg]:text-red-300 light:hover:[&_svg]:text-red-600"
                  >
                    <Iconify.Icon icon="heroicons:trash" className="size-4" />
                    Delete
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        )
      },
    },
  ]

  return (
    <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
      {/* Refresh + Create live in the topbar (see topbar/routes/
          deployments/sandboxes.tsx), like every other route. */}
      <ResponsiveTable
        columns={columns}
        data={instances}
        enableResizing={true}
        minTableWidth="100%"
        emptyMessage={
          <div className="flex flex-col items-center justify-center py-12">
            <div className="relative mb-6">
              <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
              <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                <Iconify.Icon
                  icon="heroicons:cube-transparent"
                  className="size-8 text-brand-secondary-400"
                />
              </div>
            </div>
            <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
              No sandbox instances
            </h3>
            <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
              Instances are created when agents with sandbox enabled run.
            </p>
          </div>
        }
        rowKey={(inst) => inst.id}
        onRowClick={(inst) =>
          navigate({
            to: '/deployments/sandboxes/$sandboxId',
            params: { sandboxId: inst.id },
          })
        }
      />

      <SandboxDetailsSheet
        instance={selectedSandbox}
        open={!!selectedSandbox}
        onOpenChange={(open) => {
          if (!open) setSelectedSandboxId(null)
        }}
        onOpenShell={(inst) => {
          setSelectedSandboxId(null)
          navigate({
            to: '/deployments/sandboxes/$sandboxId',
            params: { sandboxId: inst.id },
            search: { tab: 'terminal' },
          })
        }}
        onViewLogs={(inst) => {
          setSelectedSandboxId(null)
          navigate({
            to: '/deployments/sandboxes/$sandboxId',
            params: { sandboxId: inst.id },
            search: { tab: 'logs' },
          })
        }}
        onEditConfig={(inst) => {
          setSelectedSandboxId(null)
          navigate(
            inst.agentId
              ? {
                  to: '/deployments/agents/$agentId/settings',
                  params: { agentId: inst.agentId },
                  search: { section: 'environment' },
                }
              : { to: '/deployments/sandboxes/new', search: { from: inst.id } },
          )
        }}
      />

      <AlertDialog
        open={!!terminateTarget}
        onOpenChange={(open) => {
          if (!open && !terminateMutation.isPending) setTerminateTarget(null)
        }}
      >
        <AlertDialogContent className="bg-brand-main-900 border border-brand-main-700 text-brand-main-100 p-0 gap-0 sm:max-w-md shadow-2xl shadow-black/60">
          <AlertDialogHeader className="p-5 pb-4 sm:text-left">
            <div className="flex items-start gap-3">
              <div className="shrink-0 size-9 rounded-md bg-red-500/10 border border-red-500/25 flex items-center justify-center">
                <Iconify.Icon
                  icon="heroicons:exclamation-triangle"
                  className="size-5 text-red-400 light:text-red-600"
                />
              </div>
              <div className="flex-1 min-w-0">
                <AlertDialogTitle className="text-white light:text-brand-main-50 text-[15px] font-semibold leading-tight">
                  Terminate sandbox?
                </AlertDialogTitle>
                <AlertDialogDescription className="mt-1.5 text-sm text-white/55 light:text-black/55 leading-relaxed">
                  Stops{' '}
                  <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-brand-main-800 text-brand-secondary-300 border border-brand-main-700">
                    {terminateTarget?.name?.trim() ||
                      terminateTarget?.sessionId ||
                      terminateTarget?.id}
                  </span>{' '}
                  and releases its compute resources back to the host. The
                  record stays in this list with status{' '}
                  <span className="text-white/75 light:text-black/75">
                    deleted
                  </span>
                  , so you can recreate or purge it later.
                </AlertDialogDescription>
              </div>
            </div>
            {terminateTarget?.agentId && (
              <div className="mt-4 rounded-md border border-brand-main-700 bg-brand-main-800/60 px-3 py-2.5 flex items-start gap-2">
                <Iconify.Icon
                  icon="heroicons:link"
                  className="size-4 text-brand-secondary-400 shrink-0 mt-0.5"
                />
                <p className="text-xs text-white/65 light:text-black/65 leading-relaxed">
                  Bound to a persistent agent. The agent will also be marked as{' '}
                  <span className="text-white/85 light:text-black/85 font-medium">
                    terminated
                  </span>{' '}
                  and stop running. The agent definition itself stays in place.
                </p>
              </div>
            )}
          </AlertDialogHeader>
          <AlertDialogFooter className="px-5 py-3 border-t border-brand-main-700 bg-brand-main-950/40 sm:justify-end gap-2">
            <AlertDialogCancel
              disabled={terminateMutation.isPending}
              className="mt-0 h-8 px-3.5 text-sm bg-transparent border border-brand-main-600 text-white/75 light:text-black/75 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50"
            >
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={terminateMutation.isPending}
              onClick={(e) => {
                e.preventDefault()
                if (!terminateTarget) return
                terminateMutation.mutate(terminateTarget.id, {
                  onSuccess: () => {
                    toast.success(
                      terminateTarget.agentId
                        ? 'Sandbox deletion requested; agent terminated'
                        : 'Sandbox deletion requested',
                    )
                    setTerminateTarget(null)
                  },
                  onError: (err) =>
                    toast.error(`Failed to terminate: ${err.message}`),
                })
              }}
              className="h-8 px-3.5 text-sm bg-red-500/90 hover:bg-red-500 text-white border-0 inline-flex items-center gap-1.5 shadow-sm shadow-red-900/30"
            >
              {terminateMutation.isPending ? (
                <>
                  <Iconify.Icon
                    icon="heroicons:arrow-path"
                    className="size-3.5 animate-spin"
                  />
                  Terminating
                </>
              ) : (
                <>
                  <Iconify.Icon
                    icon="heroicons:x-circle"
                    className="size-3.5"
                  />
                  Terminate
                </>
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open && !destroyMutation.isPending) setDeleteTarget(null)
        }}
      >
        <AlertDialogContent className="bg-brand-main-900 border border-brand-main-700 text-brand-main-100 p-0 gap-0 sm:max-w-md shadow-2xl shadow-black/60">
          <AlertDialogHeader className="p-5 pb-4 sm:text-left">
            <div className="flex items-start gap-3">
              <div className="shrink-0 size-9 rounded-md bg-red-500/10 border border-red-500/25 flex items-center justify-center">
                <Iconify.Icon
                  icon="heroicons:trash"
                  className="size-5 text-red-400 light:text-red-600"
                />
              </div>
              <div className="flex-1 min-w-0">
                <AlertDialogTitle className="text-white light:text-brand-main-50 text-[15px] font-semibold leading-tight">
                  Delete sandbox permanently?
                </AlertDialogTitle>
                <AlertDialogDescription className="mt-1.5 text-sm text-white/55 light:text-black/55 leading-relaxed">
                  Removes{' '}
                  <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-brand-main-800 text-brand-secondary-300 border border-brand-main-700">
                    {deleteTarget?.name?.trim() ||
                      deleteTarget?.sessionId ||
                      deleteTarget?.id}
                  </span>{' '}
                  from this list.
                </AlertDialogDescription>
              </div>
            </div>
            <div className="mt-4 rounded-md border border-brand-main-700 bg-brand-main-800/60 px-3 py-2.5 flex items-start gap-2">
              <Iconify.Icon
                icon="heroicons:exclamation-circle"
                className="size-4 text-red-400 light:text-red-600 shrink-0 mt-0.5"
              />
              <p className="text-xs text-white/65 light:text-black/65 leading-relaxed">
                Workspace data, snapshots, logs, and event history will be
                erased.
                <span className="text-white/85 light:text-black/85">
                  {' '}
                  This cannot be undone.
                </span>
              </p>
            </div>
          </AlertDialogHeader>
          <AlertDialogFooter className="px-5 py-3 border-t border-brand-main-700 bg-brand-main-950/40 sm:justify-end gap-2">
            <AlertDialogCancel
              disabled={destroyMutation.isPending}
              className="mt-0 h-8 px-3.5 text-sm bg-transparent border border-brand-main-600 text-white/75 light:text-black/75 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50"
            >
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={destroyMutation.isPending || !deleteTarget?.sessionId}
              onClick={(e) => {
                e.preventDefault()
                if (!deleteTarget?.sessionId) return
                destroyMutation.mutate(deleteTarget.sessionId, {
                  onSuccess: () => {
                    toast.success('Sandbox deleted')
                    setDeleteTarget(null)
                  },
                  onError: (err) =>
                    toast.error(`Failed to delete: ${err.message}`),
                })
              }}
              className="h-8 px-3.5 text-sm bg-red-500/90 hover:bg-red-500 text-white border-0 inline-flex items-center gap-1.5 shadow-sm shadow-red-900/30"
            >
              {destroyMutation.isPending ? (
                <>
                  <Iconify.Icon
                    icon="heroicons:arrow-path"
                    className="size-3.5 animate-spin"
                  />
                  Deleting
                </>
              ) : (
                <>
                  <Iconify.Icon icon="heroicons:trash" className="size-3.5" />
                  Delete
                </>
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
