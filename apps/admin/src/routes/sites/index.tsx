import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { Button, Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatRelativeTime } from '@everstack/utils/functions/index'
import { useSites } from '@/hooks/sites/use-sites'
import type { Site } from '@/server/sites'
import { SiteStatusBadge } from '@/components/sites/site-status-badge'
import { PublishSiteDialog } from '@/components/sites/publish-site-dialog'
import {
  filterCustomerSites,
  type CustomerSiteStatusFilter,
} from '@/components/sites/customer-sites-model'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { formatBytes } from '@/utils/trace-formatters'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

const sitesSearchSchema = z.object({
  search: z.string().optional().default(''),
})

export const Route = createFileRoute('/sites/')({
  component: SitesPage,
  validateSearch: sitesSearchSchema,
})

function SitesPage() {
  const { search } = Route.useSearch()
  const navigate = useNavigate()
  const { data: sites = [], isLoading, isFetching, error, refetch } = useSites()
  const [status, setStatus] = useState<CustomerSiteStatusFilter>('all')

  const filteredSites = useMemo(
    () => filterCustomerSites(sites, search, status),
    [search, sites, status],
  )

  const columns = useMemo<ColumnConfig<Site>[]>(
    () => [
      {
        id: 'site',
        header: 'Site',
        width: 260,
        minWidth: 190,
        render: (site) => (
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex size-7 shrink-0 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70 text-brand-secondary-300">
              <Iconify.Icon icon="lucide:globe-2" className="size-3.5" />
            </div>
            <div className="min-w-0">
              <div className="truncate text-xs font-medium text-brand-secondary-100">
                {site.slug}
              </div>
              <div className="mt-0.5 truncate font-mono text-[10px] text-brand-main-300">
                {site.url.replace(/^https?:\/\//, '')}
              </div>
            </div>
          </div>
        ),
      },
      {
        id: 'status',
        header: 'Status',
        width: 105,
        minWidth: 90,
        render: (site) => <SiteStatusBadge status={site.status} />,
      },
      {
        id: 'production',
        header: 'Production',
        width: 125,
        minWidth: 110,
        render: (site) => (
          <div className="flex items-center gap-2">
            <span
              className={`size-1.5 shrink-0 rounded-full ${
                site.status === 'active'
                  ? 'bg-emerald-400'
                  : site.status === 'disabled'
                    ? 'bg-red-400'
                    : 'bg-amber-400'
              }`}
            />
            <span className="font-mono text-xs text-brand-main-100">
              v{site.currentVersion}
            </span>
          </div>
        ),
      },
      {
        id: 'routing',
        header: 'Routing',
        width: 125,
        minWidth: 105,
        render: (site) => (
          <span className="text-xs text-brand-main-100">
            {site.spaFallback ? 'SPA fallback' : 'Static'}
          </span>
        ),
      },
      {
        id: 'access',
        header: 'Indexing',
        width: 115,
        minWidth: 95,
        render: (site) => (
          <span className="inline-flex items-center gap-1.5 text-xs text-brand-main-100">
            <Iconify.Icon
              icon={
                site.access === 'noindex' ? 'lucide:eye-off' : 'lucide:search'
              }
              className="size-3 text-brand-main-300"
            />
            {site.access === 'noindex' ? 'No index' : 'Indexable'}
          </span>
        ),
      },
      {
        id: 'files',
        header: 'Files',
        width: 85,
        minWidth: 70,
        align: 'right',
        render: (site) => (
          <span className="font-mono text-xs tabular-nums text-brand-main-100">
            {site.fileCount.toLocaleString()}
          </span>
        ),
      },
      {
        id: 'storage',
        header: 'Storage',
        width: 110,
        minWidth: 90,
        align: 'right',
        render: (site) => (
          <span className="font-mono text-xs tabular-nums text-brand-main-100">
            {formatBytes(site.totalBytes)}
          </span>
        ),
      },
      {
        id: 'published',
        header: 'Last published',
        width: 155,
        minWidth: 130,
        render: (site) => (
          <span className="text-xs text-brand-main-100">
            {site.lastPublishedAt
              ? formatRelativeTime(site.lastPublishedAt)
              : 'Not published'}
          </span>
        ),
      },
    ],
    [],
  )

  if (isLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading sites..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center gap-3 px-6 text-center">
        <div className="relative mb-2">
          <div className="absolute inset-0 rounded-full bg-red-500/20 blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Iconify.Icon
              icon="lucide:triangle-alert"
              className="size-8 text-red-400 light:text-red-600"
            />
          </div>
        </div>
        <div>
          <p className="text-base font-medium text-white light:text-brand-main-50">
            Sites could not be loaded
          </p>
          <p className="mt-1 max-w-lg text-sm text-white/50 light:text-black/50">
            {error.message}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => refetch()}>
          Try again
        </Button>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col overflow-hidden">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-brand-main-800/40 bg-brand-main-900/20 px-3 py-2">
        <div className="flex items-center gap-2">
          <span className="text-[11px] uppercase tracking-wider text-white/45 light:text-black/45">
            Status
          </span>
          <Select
            value={status}
            onValueChange={(value) =>
              setStatus(value as CustomerSiteStatusFilter)
            }
          >
            <SelectTrigger className="h-8 w-[150px] border-brand-main-700 bg-brand-main-900/60 text-xs text-zinc-200">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="border-brand-main-600 bg-brand-main-900 text-zinc-200">
              <SelectItem value="all">All sites</SelectItem>
              <SelectItem value="active">Active</SelectItem>
              <SelectItem value="disabled">Disabled</SelectItem>
              <SelectItem value="expired">Expired</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="flex items-center gap-2 text-xs text-white/45 light:text-black/45">
          {isFetching && (
            <Iconify.Icon
              icon="lucide:refresh-cw"
              className="size-3 animate-spin"
            />
          )}
          <span className="font-mono text-[10px] uppercase tracking-wider">
            {filteredSites.length}{' '}
            {filteredSites.length === 1 ? 'site' : 'sites'}
          </span>
        </div>
      </div>

      <ResponsiveTable
        tableId="customer-sites"
        columns={columns}
        data={filteredSites}
        enableResizing
        enableColumnPersistence
        minTableWidth="100%"
        emptyMessage={
          sites.length === 0 ? (
            <div className="flex flex-col items-center justify-center">
              <div className="relative mb-6">
                <div className="absolute inset-0 rounded-full bg-brand-secondary-500/20 blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                  <Iconify.Icon
                    icon="lucide:panel-top"
                    className="size-8 text-brand-secondary-400"
                  />
                </div>
              </div>
              <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
                No sites yet
              </h3>
              <p className="mb-4 max-w-md text-center text-sm leading-relaxed text-white/50 light:text-black/50">
                Publish a static build to get a production evs.run domain and
                manage its deployment here.
              </p>
              <PublishSiteDialog />
            </div>
          ) : (
            'No sites match your search or status filter.'
          )
        }
        onRowClick={(site) =>
          navigate({ to: '/sites/$slug', params: { slug: site.slug } })
        }
        rowKey={(site) => site.id || site.slug}
      />
    </div>
  )
}
