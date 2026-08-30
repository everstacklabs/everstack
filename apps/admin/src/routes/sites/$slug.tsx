import { createFileRoute, Link } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { formatRelativeTime } from '@everstack/utils/functions/index'
import { useDeleteSite, useSite, useUpdateSite } from '@/hooks/sites/use-sites'
import type { Site, SiteAccess } from '@/server/sites'
import { SiteStatusBadge } from '@/components/sites/site-status-badge'
import { formatBytes } from '@/utils/trace-formatters'

const {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} = ui

const siteDetailSearchSchema = z.object({
  tab: z.enum(['overview', 'settings']).optional().default('overview'),
})

export const Route = createFileRoute('/sites/$slug')({
  component: SiteDetailPage,
  validateSearch: siteDetailSearchSchema,
})

const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

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
        className={`mt-2 text-sm text-white light:text-brand-main-50 ${
          mono ? 'font-mono' : 'font-medium'
        }`}
      >
        {value}
      </div>
    </div>
  )
}

function SectionCard({
  title,
  description,
  icon,
  children,
  tone = 'default',
}: {
  title: string
  description: string
  icon: string
  children: ReactNode
  tone?: 'default' | 'danger'
}) {
  return (
    <section
      className={`rounded border bg-brand-main-900/50 p-4 ${
        tone === 'danger' ? 'border-red-500/25' : 'border-brand-main-600'
      }`}
    >
      <div className="mb-4 flex items-start gap-3">
        <div
          className={`rounded border p-2 ${
            tone === 'danger'
              ? 'border-red-500/25 bg-red-500/10 text-red-300 light:text-red-600'
              : 'border-brand-secondary-500/30 bg-brand-secondary-600/15 text-brand-secondary-300'
          }`}
        >
          <Iconify.Icon icon={icon} className="size-4" />
        </div>
        <div>
          <div
            className={`text-sm font-medium ${
              tone === 'danger'
                ? 'text-red-300 light:text-red-600'
                : 'text-white light:text-brand-main-50'
            }`}
          >
            {title}
          </div>
          <div className="text-xs text-white/50 light:text-black/50">
            {description}
          </div>
        </div>
      </div>
      {children}
    </section>
  )
}

function ConfigField({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="flex min-w-0 items-start justify-between gap-3 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2.5">
      <div className="shrink-0 text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div className="min-w-0 text-right text-sm text-brand-main-100">
        {children}
      </div>
    </div>
  )
}

function StatusPill({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center rounded-full border border-brand-main-500/25 bg-brand-main-800/70 px-2.5 py-1 text-[11px] font-medium text-brand-main-100">
      {children}
    </span>
  )
}

function SiteOverview({
  site,
  publishCommand,
  onCopyPublishCommand,
}: {
  site: Site
  publishCommand: string
  onCopyPublishCommand: () => void
}) {
  return (
    <div className="h-full overflow-y-auto px-4 py-4">
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <section className="relative overflow-hidden rounded border border-brand-main-600 bg-brand-main-900/50 p-5">
          <div className="absolute -right-12 top-0 h-40 w-40 rounded-full bg-brand-secondary-500/10 blur-3xl" />
          <div className="relative flex flex-col gap-6 xl:flex-row xl:items-start xl:justify-between">
            <div className="min-w-0 max-w-2xl space-y-4">
              <div className="flex items-center gap-3">
                <div className="flex size-12 shrink-0 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/80">
                  <div className="flex size-7 items-center justify-center rounded bg-brand-secondary-600/30 text-brand-secondary-200">
                    <Iconify.Icon icon="lucide:globe-2" className="size-4" />
                  </div>
                </div>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-[10px] uppercase tracking-wide text-brand-secondary-300">
                    <Iconify.Icon icon="lucide:rocket" className="size-3.5" />
                    Site overview
                  </div>
                  <h1 className="mt-1 truncate text-2xl font-semibold tracking-tight text-white light:text-brand-main-50">
                    {site.slug}
                  </h1>
                </div>
              </div>

              <a
                href={site.url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex max-w-full items-center gap-1.5 font-mono text-sm text-white/50 transition-colors hover:text-white/80 light:text-black/50 light:hover:text-black/80"
              >
                <span className="truncate">{site.url}</span>
                <Iconify.Icon
                  icon="lucide:external-link"
                  className="size-3.5 shrink-0"
                />
              </a>

              <div className="flex flex-wrap gap-2">
                <SiteStatusBadge status={site.status} />
                <StatusPill>Production</StatusPill>
                <StatusPill>
                  {site.spaFallback ? 'SPA fallback' : 'Static routing'}
                </StatusPill>
                <StatusPill>
                  {site.access === 'noindex' ? 'No index' : 'Indexable'}
                </StatusPill>
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2 xl:min-w-[340px] xl:max-w-[380px]">
              <StatCard
                label="Live version"
                value={`v${site.currentVersion}`}
                mono
              />
              <StatCard label="Files" value={site.fileCount.toLocaleString()} />
              <StatCard label="Storage" value={formatBytes(site.totalBytes)} />
              <StatCard
                label="Published"
                value={
                  site.lastPublishedAt
                    ? formatRelativeTime(site.lastPublishedAt)
                    : 'Never'
                }
              />
            </div>
          </div>
        </section>

        <div className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
          <SectionCard
            title="Production deployment"
            description="The immutable version currently receiving traffic."
            icon="lucide:rocket"
          >
            <div className="grid gap-3 md:grid-cols-2">
              <ConfigField label="Version">
                <span className="font-mono">v{site.currentVersion}</span>
              </ConfigField>
              <ConfigField label="Status">
                <SiteStatusBadge status={site.status} />
              </ConfigField>
              <ConfigField label="Domain">
                <a
                  href={site.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex max-w-full items-center gap-1 font-mono text-xs text-brand-secondary-200 hover:text-white light:text-brand-secondary-700"
                >
                  <span className="truncate">
                    {site.url.replace(/^https?:\/\//, '')}
                  </span>
                  <Iconify.Icon
                    icon="lucide:external-link"
                    className="size-3 shrink-0"
                  />
                </a>
              </ConfigField>
              <ConfigField label="Published">
                {site.lastPublishedAt
                  ? new Date(site.lastPublishedAt).toLocaleString()
                  : 'Never'}
              </ConfigField>
            </div>
          </SectionCard>

          <SectionCard
            title="Delivery profile"
            description="How this site is served and retained."
            icon="lucide:network"
          >
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-1">
              <ConfigField label="Routing">
                {site.spaFallback ? 'SPA fallback' : 'Static'}
              </ConfigField>
              <ConfigField label="Indexing">
                {site.access === 'noindex' ? 'No index' : 'Public'}
              </ConfigField>
              <ConfigField label="Ownership">
                {site.claimed ? 'Organization' : 'Anonymous'}
              </ConfigField>
              <ConfigField label="Retention">
                {site.expiresAt
                  ? `Expires ${formatRelativeTime(site.expiresAt)}`
                  : 'Permanent'}
              </ConfigField>
            </div>
          </SectionCard>
        </div>

        <SectionCard
          title="Publish a new version"
          description="Deploy this project again without changing its production domain."
          icon="lucide:upload-cloud"
        >
          <div className="flex items-center gap-2 rounded border border-brand-main-600 bg-brand-main-950/70 p-1.5 pl-3">
            <code className="min-w-0 flex-1 truncate font-mono text-xs text-brand-secondary-200 light:text-brand-secondary-700">
              {publishCommand}
            </code>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 shrink-0 px-2"
              onClick={onCopyPublishCommand}
            >
              <Iconify.Icon icon="lucide:copy" className="size-3.5" />
              Copy
            </Button>
          </div>
          <p className="mt-2 text-xs leading-relaxed text-white/45 light:text-black/45">
            The new version is activated atomically after every uploaded file
            has been verified.
          </p>
        </SectionCard>
      </div>
    </div>
  )
}

function SiteSettings({
  site,
  updatePending,
  onUpdate,
  onDelete,
}: {
  site: Site
  updatePending: boolean
  onUpdate: (changes: { spaFallback?: boolean; access?: SiteAccess }) => void
  onDelete: () => void
}) {
  return (
    <div className="h-full overflow-y-auto px-4 py-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <SectionCard
          title="Serving"
          description="Control routing and crawler behavior at the serving edge."
          icon="lucide:settings-2"
        >
          <div className="divide-y divide-brand-main-700/60 rounded border border-brand-main-600 bg-brand-main-950/40">
            <div className="flex items-center justify-between gap-4 px-3 py-3">
              <div>
                <p className="text-sm text-white/85 light:text-black/85">
                  SPA fallback
                </p>
                <p className="mt-0.5 text-xs text-white/45 light:text-black/45">
                  Serve index.html for unknown document routes. Missing assets
                  still return 404.
                </p>
              </div>
              <Switch
                checked={site.spaFallback}
                disabled={updatePending}
                onCheckedChange={(checked) =>
                  onUpdate({ spaFallback: checked })
                }
              />
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-3">
              <div>
                <p className="text-sm text-white/85 light:text-black/85">
                  Search indexing
                </p>
                <p className="mt-0.5 text-xs text-white/45 light:text-black/45">
                  No index adds a crawler directive while keeping the URL
                  public.
                </p>
              </div>
              <Select
                value={site.access}
                disabled={updatePending}
                onValueChange={(value) =>
                  onUpdate({ access: value as SiteAccess })
                }
              >
                <SelectTrigger className="h-8 w-[140px] bg-brand-main-900/60 text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="public">Public</SelectItem>
                  <SelectItem value="noindex">No index</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </SectionCard>

        <SectionCard
          title="Lifecycle"
          description="Ownership and retention for this project."
          icon="lucide:clock-3"
        >
          <div className="grid gap-3 sm:grid-cols-3">
            <ConfigField label="Ownership">
              {site.claimed ? 'Organization' : 'Anonymous'}
            </ConfigField>
            <ConfigField label="Created">
              {site.createdAt
                ? new Date(site.createdAt).toLocaleDateString()
                : 'Unknown'}
            </ConfigField>
            <ConfigField label="Retention">
              {site.expiresAt
                ? `Expires ${formatRelativeTime(site.expiresAt)}`
                : 'Permanent'}
            </ConfigField>
          </div>
        </SectionCard>

        <SectionCard
          title="Delete site"
          description="Take the domain offline and remove every uploaded version."
          icon="lucide:trash-2"
          tone="danger"
        >
          <Button
            variant="destructive"
            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
            onClick={onDelete}
          >
            Delete site
          </Button>
        </SectionCard>
      </div>
    </div>
  )
}

function SiteDetailPage() {
  const { slug } = Route.useParams()
  const { tab } = Route.useSearch()
  const navigate = Route.useNavigate()
  const { data: site, isLoading, error } = useSite(slug)
  const updateSiteMutation = useUpdateSite()
  const deleteSiteMutation = useDeleteSite()
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)

  if (isLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading site..." />
      </div>
    )
  }

  if (error || !site) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center">
        <div className="relative mb-6">
          <div className="absolute inset-0 rounded-full bg-red-500/20 blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Iconify.Icon
              icon="lucide:triangle-alert"
              className="size-8 text-red-400 light:text-red-600"
            />
          </div>
        </div>
        <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
          {error?.message ?? 'Site not found'}
        </h3>
        <p className="mb-4 max-w-sm text-center text-sm leading-relaxed text-white/50 light:text-black/50">
          The site may have been deleted or you may not have access.
        </p>
        <Link
          to="/sites"
          className="text-sm text-brand-secondary-400 hover:text-brand-secondary-300"
        >
          Back to sites
        </Link>
      </div>
    )
  }

  const handleUpdate = (changes: {
    spaFallback?: boolean
    access?: SiteAccess
  }) => {
    updateSiteMutation.mutate(
      { slug: site.slug, ...changes },
      {
        onSuccess: () => toast.success('Site settings updated'),
        onError: (updateError) =>
          toast.error(`Update failed: ${updateError.message}`),
      },
    )
  }

  const handleDelete = async () => {
    try {
      await deleteSiteMutation.mutateAsync(site.slug)
      setDeleteConfirmOpen(false)
      toast.success('Site deleted')
      navigate({ to: '/sites' })
    } catch (deleteError) {
      console.error('Failed to delete site:', deleteError)
      toast.error('Failed to delete site')
    }
  }

  const publishCommand = `evs sites publish ./dist --slug ${site.slug}${
    site.spaFallback ? ' --spa' : ''
  }${site.access === 'noindex' ? ' --noindex' : ''}`

  const copyPublishCommand = async () => {
    try {
      await copyToClipboard(publishCommand)
      toast.success('Publish command copied')
    } catch {
      toast.error('Could not copy the command')
    }
  }

  return (
    <div className="flex h-full w-full flex-col">
      <Tabs
        value={tab}
        onValueChange={(value) =>
          navigate({
            search: { tab: value as 'overview' | 'settings' },
            replace: true,
          })
        }
        className="flex flex-1 flex-col overflow-hidden"
      >
        <div className="shrink-0 px-3 pt-2">
          <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
            <TabsTrigger className={TAB_TRIGGER_CLASS} value="overview">
              Overview
            </TabsTrigger>
            <TabsTrigger className={TAB_TRIGGER_CLASS} value="settings">
              Settings
            </TabsTrigger>
          </TabsList>
        </div>

        <div className="min-h-0 flex-1 overflow-hidden">
          <TabsContent
            value="overview"
            className="flex h-full flex-col overflow-hidden"
          >
            <SiteOverview
              site={site}
              publishCommand={publishCommand}
              onCopyPublishCommand={copyPublishCommand}
            />
          </TabsContent>
          <TabsContent
            value="settings"
            className="flex h-full flex-col overflow-hidden"
          >
            <SiteSettings
              site={site}
              updatePending={updateSiteMutation.isPending}
              onUpdate={handleUpdate}
              onDelete={() => setDeleteConfirmOpen(true)}
            />
          </TabsContent>
        </div>
      </Tabs>

      <Dialog open={deleteConfirmOpen} onOpenChange={setDeleteConfirmOpen}>
        <DialogContent className="w-[500px]">
          <DialogTitle>Delete site</DialogTitle>
          <DialogDescription className="text-brand-main-100">
            Delete <strong className="text-brand-main-100">{site.slug}</strong>?
            The domain goes offline immediately and every uploaded version is
            removed. This action cannot be undone.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-3">
            <Button
              variant="outline"
              onClick={() => setDeleteConfirmOpen(false)}
              disabled={deleteSiteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
              onClick={handleDelete}
              disabled={deleteSiteMutation.isPending}
            >
              {deleteSiteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
