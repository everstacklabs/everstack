import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
  useGitHubInstallations,
  useLinkGitHubInstallation,
  useRemoveGitHubInstallation,
} from '@/hooks/integrations/use-github'
import { getApiBaseUrl } from '@/lib/api-url'
import { useSession } from '@/hooks/auth'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { IntegrationStatus } from './integration-card'

const {
  Button,
  Badge,
  Card,
  CardContent,
  Switch,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} = ui

function parseRedirectInstallationId(): number | null {
  if (typeof window === 'undefined') return null
  const params = new URLSearchParams(window.location.search)
  const raw = params.get('installation_id')
  if (!raw) return null
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed <= 0) return null
  return Math.trunc(parsed)
}

type GitHubIntegrationProps = {
  name?: string
  icon?: string
  category?: string
  status?: IntegrationStatus
  description?: string
  capabilities?: string[]
}

const STATUS_LABELS: Record<IntegrationStatus, string> = {
  live: 'Enabled',
  beta: 'Beta',
  coming_soon: 'Planned',
}

const STATUS_TONE: Record<IntegrationStatus, string> = {
  live: 'border-zinc-500/40 bg-zinc-800/30 light:bg-zinc-200/60 text-zinc-300 light:text-zinc-700',
  beta: 'border-zinc-500/40 bg-zinc-800/30 light:bg-zinc-200/60 text-zinc-300 light:text-zinc-700',
  coming_soon:
    'border-zinc-600/40 bg-zinc-800/20 light:bg-zinc-200/40 text-zinc-400 light:text-zinc-600',
}

function formatDate(value?: string): string {
  if (!value) return 'Not connected'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'Not connected'
  return parsed.toLocaleDateString()
}

export function GitHubIntegration({
  name = 'GitHub',
  icon = 'mdi:github',
  category = 'Source Control',
  status = 'beta',
  description = 'Automate pull request and commit workflows and keep issues synced both ways.',
  capabilities = [
    'Org-level app installation',
    'Repository listing',
    'Branch discovery',
    'Sandbox source import',
  ],
}: GitHubIntegrationProps) {
  const { data: installations, isLoading } = useGitHubInstallations()
  const { data: session } = useSession()
  const {
    mutateAsync: linkInstallationAsync,
    isPending: isLinkingInstallation,
  } = useLinkGitHubInstallation()
  const removeInstallation = useRemoveGitHubInstallation()
  const [confirmRemoveId, setConfirmRemoveId] = useState<number | null>(null)
  const [redirectInstallationId, setRedirectInstallationId] = useState<
    number | null
  >(() => parseRedirectInstallationId())
  const [branchFormat, setBranchFormat] = useState('feature/identifier-title')
  const [openPrsWith, setOpenPrsWith] = useState('github')
  const [privateReposEnabled, setPrivateReposEnabled] = useState(true)
  const [publicReposEnabled, setPublicReposEnabled] = useState(false)
  const [includeDescriptions, setIncludeDescriptions] = useState(true)
  const autoLinkAttemptedRef = useRef<Set<number>>(new Set())

  const organizationId = session?.user?.organizations?.[0]?.id
  const enabledBy =
    session?.user?.user?.name || session?.user?.user?.email || 'Instance admin'
  const connectedDate = formatDate(installations?.[0]?.createdAt)
  const effectiveStatus: IntegrationStatus = installations?.length
    ? 'live'
    : status

  const appInstallUrl = useMemo(() => {
    const explicitUrl = import.meta.env.VITE_GITHUB_APP_INSTALL_URL?.trim()
    if (explicitUrl) return explicitUrl

    const appSlug = import.meta.env.VITE_GITHUB_APP_SLUG?.trim()
    if (appSlug) {
      return `https://github.com/apps/${appSlug}/installations/new`
    }

    return null
  }, [])

  const handleConnect = () => {
    if (!organizationId) {
      toast.error('Organization context is unavailable. Refresh and try again.')
      return
    }

    if (appInstallUrl) {
      const popup = window.open(appInstallUrl, '_blank', 'noopener,noreferrer')
      if (!popup) {
        toast.error(
          'Unable to open GitHub install page. Check popup blocker settings and try again.',
        )
      }
      return
    }

    const startURL = new URL(
      '/integrations/github/manifest/start',
      getApiBaseUrl(),
    )
    startURL.searchParams.set('tenant_id', organizationId)
    startURL.searchParams.set('return_to', '/settings/integrations')
    window.location.assign(startURL.toString())
  }

  const handleRemove = (installationId: number) => {
    removeInstallation.mutate(
      { installationId },
      { onSuccess: () => setConfirmRemoveId(null) },
    )
  }

  useEffect(() => {
    if (!redirectInstallationId || !organizationId) return
    if (autoLinkAttemptedRef.current.has(redirectInstallationId)) return

    let cancelled = false
    autoLinkAttemptedRef.current.add(redirectInstallationId)

    const cleanupSearchParams = () => {
      if (typeof window === 'undefined') return
      const url = new URL(window.location.href)
      url.searchParams.delete('installation_id')
      url.searchParams.delete('setup_action')
      window.history.replaceState(
        {},
        '',
        `${url.pathname}${url.search}${url.hash}`,
      )
    }

    const linkFromRedirect = async () => {
      if (
        installations?.some(
          (inst) => inst.installationId === redirectInstallationId,
        )
      ) {
        cleanupSearchParams()
        setRedirectInstallationId(null)
        return
      }

      try {
        await linkInstallationAsync({ installationId: redirectInstallationId })
        if (!cancelled) {
          toast.success('GitHub installation connected successfully.')
        }
      } catch (error) {
        if (!cancelled) {
          const message =
            error instanceof Error ? error.message : 'Unknown error'
          toast.error(`Failed to connect GitHub installation: ${message}`)
        }
      } finally {
        cleanupSearchParams()
        if (!cancelled) {
          setRedirectInstallationId(null)
        }
      }
    }

    linkFromRedirect()

    return () => {
      cancelled = true
    }
  }, [
    installations,
    linkInstallationAsync,
    organizationId,
    redirectInstallationId,
  ])

  return (
    <div className="space-y-3">
      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-3">
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="flex min-w-0 items-start gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded border border-brand-main-600 bg-brand-main-900/60">
                <Iconify.Icon
                  icon={icon}
                  className="h-5 w-5 text-zinc-100 light:text-zinc-900"
                />
              </div>
              <div className="min-w-0 space-y-1">
                <div className="flex items-center gap-2">
                  <h3 className="text-lg font-semibold text-white light:text-brand-main-50">
                    {name}
                  </h3>
                  <Badge className={STATUS_TONE[effectiveStatus]}>
                    {STATUS_LABELS[effectiveStatus]}
                  </Badge>
                  <Badge
                    variant="outline"
                    className="border-brand-main-600 text-zinc-300 light:text-zinc-700"
                  >
                    {category}
                  </Badge>
                </div>
                <p className="text-sm text-zinc-400 light:text-zinc-600">
                  {description}
                </p>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {capabilities.map((capability) => (
                    <span
                      key={capability}
                      className="rounded border border-brand-main-600 bg-brand-main-900/35 px-2 py-0.5 text-[10px] text-zinc-300 light:text-zinc-700"
                    >
                      {capability}
                    </span>
                  ))}
                </div>
              </div>
            </div>
            <Button
              variant={
                STATUS_LABELS[effectiveStatus].toLocaleLowerCase() === 'enabled'
                  ? 'default'
                  : 'ghost'
              }
              onClick={handleConnect}
              disabled={isLinkingInstallation}
            >
              {isLinkingInstallation
                ? 'Linking...'
                : STATUS_LABELS[effectiveStatus].toLocaleLowerCase() ===
                    'enabled'
                  ? 'Connected'
                  : 'Connect'}
            </Button>
          </div>

          <div className="flex items-center gap-4 text-xs text-zinc-500">
            <span>Enabled by {enabledBy}</span>
            <span>{connectedDate}</span>
          </div>
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-3">
        <CardContent className="space-y-3">
          <div className="flex items-center justify-between">
            <div>
              <h4 className="text-sm font-medium text-white light:text-brand-main-50">
                Connected organizations
              </h4>
              <p className="text-xs text-zinc-500">
                Install GitHub App to link organization repositories.
              </p>
            </div>
            <Button
              size="xs"
              variant="ghost"
              onClick={handleConnect}
              disabled={isLinkingInstallation}
            >
              <Iconify.Icon icon="lucide:plus" className="h-4 w-4" />
            </Button>
          </div>

          {isLoading ? (
            <div className="rounded border border-brand-main-600 bg-brand-main-900/30 p-3 text-sm text-zinc-400 light:text-zinc-600">
              Loading connected organizations...
            </div>
          ) : !installations?.length ? (
            <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/30 p-3">
              <p className="text-sm text-zinc-300 light:text-zinc-700">
                No organizations connected yet.
              </p>
              <p className="mt-1 text-xs text-zinc-500">
                Connect GitHub to import repositories and branches.
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {installations.map((inst) => (
                <div
                  key={inst.installationId}
                  className="rounded border border-brand-main-600 bg-brand-main-900/30 p-2.5"
                >
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <div className="flex h-7 w-7 items-center justify-center rounded border border-brand-main-600 bg-brand-main-900/70">
                        <Iconify.Icon
                          icon={
                            inst.accountType === 'Organization'
                              ? 'mdi:office-building'
                              : 'mdi:account'
                          }
                          className="h-4 w-4 text-zinc-300 light:text-zinc-700"
                        />
                      </div>
                      <div className="min-w-0">
                        <p className="truncate text-sm text-white light:text-brand-main-50">
                          {inst.accountLogin}
                        </p>
                        <p className="text-xs text-zinc-500">
                          Enabled by {enabledBy} {connectedDate}
                        </p>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="inline-flex items-center gap-1 text-xs text-zinc-300 light:text-zinc-700">
                        <span className="h-1.5 w-1.5 rounded-full bg-zinc-400" />
                        Connected
                      </span>
                      {confirmRemoveId === inst.installationId ? (
                        <div className="flex items-center gap-1">
                          <Button
                            variant="destructive"
                            onClick={() => handleRemove(inst.installationId)}
                            disabled={removeInstallation.isPending}
                            className="text-xs"
                          >
                            Confirm
                          </Button>
                          <Button
                            variant="ghost"
                            onClick={() => setConfirmRemoveId(null)}
                            className="text-xs"
                          >
                            Cancel
                          </Button>
                        </div>
                      ) : (
                        <Button
                          size="xs"
                          variant="ghost"
                          onClick={() =>
                            setConfirmRemoveId(inst.installationId)
                          }
                        >
                          <Iconify.Icon
                            icon="lucide:trash-2"
                            className="text-zinc-400 light:text-zinc-600 h-2 w-2"
                          />
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-2">
        <CardContent className="flex items-center justify-between">
          <div>
            <h4 className="text-sm font-medium text-white light:text-brand-main-50">
              Personal GitHub account connected
            </h4>
            <p className="text-xs text-zinc-500">Connected accounts</p>
          </div>
          <Iconify.Icon
            icon="lucide:chevron-right"
            className="h-4 w-4 text-zinc-500"
          />
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-2">
        <CardContent className="flex items-center justify-between">
          <div>
            <h4 className="text-sm font-medium text-white light:text-brand-main-50">
              GitHub Issues
            </h4>
            <p className="text-xs text-zinc-500">
              Create issues and sync properties to GitHub repositories.
            </p>
          </div>
          <Button size="xs" variant="ghost">
            <Iconify.Icon icon="lucide:plus" className="h-4 w-4" />
          </Button>
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-3">
        <CardContent className="space-y-3">
          <div>
            <h4 className="text-sm font-medium text-white light:text-brand-main-50">
              Branch format
            </h4>
            <p className="text-xs text-zinc-500">
              Keep branch names aligned across your organization.
            </p>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs text-zinc-300 light:text-zinc-700">
              Format
            </span>
            <Select value={branchFormat} onValueChange={setBranchFormat}>
              <SelectTrigger className="w-[230px] h-8 bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                <SelectItem value="feature/identifier-title">
                  feature/identifier-title
                </SelectItem>
                <SelectItem value="chore/identifier-title">
                  chore/identifier-title
                </SelectItem>
                <SelectItem value="fix/identifier-title">
                  fix/identifier-title
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-3">
        <CardContent className="space-y-3">
          <div>
            <h4 className="text-sm font-medium text-white light:text-brand-main-50">
              Linkbacks
            </h4>
            <p className="text-xs text-zinc-500">
              Comment in GitHub with a link to related work items.
            </p>
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between rounded border border-brand-main-700 bg-brand-main-900/25 px-3 py-2">
              <span className="text-xs text-zinc-300 light:text-zinc-700">
                Private repositories
              </span>
              <Switch
                checked={privateReposEnabled}
                onCheckedChange={setPrivateReposEnabled}
              />
            </div>
            <div className="flex items-center justify-between rounded border border-brand-main-700 bg-brand-main-900/25 px-3 py-2">
              <span className="text-xs text-zinc-300 light:text-zinc-700">
                Public repositories
              </span>
              <Switch
                checked={publicReposEnabled}
                onCheckedChange={setPublicReposEnabled}
              />
            </div>
            <div className="flex items-center justify-between rounded border border-brand-main-700 bg-brand-main-900/25 px-3 py-2">
              <span className="text-xs text-zinc-300 light:text-zinc-700">
                Include issue descriptions in linkbacks
              </span>
              <Switch
                checked={includeDescriptions}
                onCheckedChange={setIncludeDescriptions}
              />
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="border-brand-main-500/50 bg-brand-main-900/50 py-4 gap-3">
        <CardContent className="flex items-center justify-between gap-3">
          <div>
            <h4 className="text-sm font-medium text-white light:text-brand-main-50">
              Pull request reviews
            </h4>
            <p className="text-xs text-zinc-500">
              Choose where pull requests should open by default.
            </p>
          </div>
          <Select value={openPrsWith} onValueChange={setOpenPrsWith}>
            <SelectTrigger className="w-[150px] h-8 bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
              <SelectItem value="github">GitHub</SelectItem>
              <SelectItem value="everstack">Everstack</SelectItem>
            </SelectContent>
          </Select>
        </CardContent>
      </Card>
    </div>
  )
}
