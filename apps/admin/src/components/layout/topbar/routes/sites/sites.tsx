import { Link, useParams } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { SiteStatusBadge } from '@/components/sites/site-status-badge'
import { PublishSiteDialog } from '@/components/sites/publish-site-dialog'
import { useSite } from '@/hooks/sites/use-sites'

function useDetailSlug(): string {
  const params = useParams({ strict: false }) as { slug?: string }
  return params.slug ?? ''
}

function SiteDetailBreadcrumb() {
  const slug = useDetailSlug()
  const { data: site } = useSite(slug)

  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <Link
        to="/sites"
        className="text-sm font-normal text-brand-main-300 transition-colors hover:text-white/80 light:hover:text-black/80"
      >
        Sites
      </Link>
      <span className="text-sm text-brand-main-400">/</span>
      <span className="max-w-[280px] truncate text-sm font-normal text-white light:text-brand-main-50">
        {site?.slug || slug}
      </span>
      {site && <SiteStatusBadge status={site.status} />}
    </div>
  )
}

function SiteDetailActions() {
  const slug = useDetailSlug()
  const { data: site } = useSite(slug)

  if (!site) return null

  const copyURL = async () => {
    try {
      await copyToClipboard(site.url)
      toast.success('URL copied to clipboard')
    } catch {
      toast.error('Failed to copy URL')
    }
  }

  return (
    <div className="flex items-center gap-2">
      <Button variant="outline" onClick={copyURL}>
        <Iconify.Icon icon="lucide:copy" className="size-4" />
        Copy URL
      </Button>
      <Button
        variant="default"
        onClick={() => window.open(site.url, '_blank', 'noopener,noreferrer')}
      >
        <Iconify.Icon icon="lucide:external-link" className="size-4" />
        Open site
      </Button>
    </div>
  )
}

export const SitesActions: ActionGroup[] = [
  {
    title: 'Sites',
    actions: [
      {
        type: 'search',
        key: 'search-sites',
        label: 'Search',
        searchParam: 'search',
        placeholder: 'Search sites or domains...',
        debounceMs: 200,
      },
    ],
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'publish-site',
        label: 'Publish site',
        component: PublishSiteDialog,
      },
    ],
  },
]

export const SitesDetailTopbarActions: ActionGroup[] = [
  { title: <SiteDetailBreadcrumb />, actions: [] },
  {
    actions: [
      {
        type: 'custom',
        key: 'site-actions',
        label: 'Site actions',
        component: SiteDetailActions,
      },
    ],
  },
]
