import type { Site, SiteStatus } from '@/server/sites'

export type CustomerSiteStatusFilter = 'all' | Exclude<SiteStatus, 'unknown'>

export type CustomerSitesSummary = {
  activeSites: number
  totalBytes: number
  latestPublish?: string
}

export function filterCustomerSites(
  sites: Site[],
  search: string,
  status: CustomerSiteStatusFilter,
): Site[] {
  const query = search.trim().toLowerCase()
  return [...sites]
    .filter((site) => {
      if (status !== 'all' && site.status !== status) return false
      if (!query) return true
      return (
        site.slug.toLowerCase().includes(query) ||
        site.url.toLowerCase().includes(query)
      )
    })
    .sort((a, b) => {
      const aTime = a.lastPublishedAt
        ? new Date(a.lastPublishedAt).getTime()
        : 0
      const bTime = b.lastPublishedAt
        ? new Date(b.lastPublishedAt).getTime()
        : 0
      return bTime - aTime
    })
}

export function summarizeCustomerSites(sites: Site[]): CustomerSitesSummary {
  return sites.reduce<CustomerSitesSummary>(
    (summary, site) => {
      if (site.status === 'active') summary.activeSites += 1
      summary.totalBytes += site.totalBytes
      if (
        site.lastPublishedAt &&
        (!summary.latestPublish ||
          new Date(site.lastPublishedAt) > new Date(summary.latestPublish))
      ) {
        summary.latestPublish = site.lastPublishedAt
      }
      return summary
    },
    { activeSites: 0, totalBytes: 0 },
  )
}
