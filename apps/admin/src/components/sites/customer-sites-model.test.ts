import { describe, expect, it } from 'vitest'
import type { Site } from '@/server/sites'
import {
  filterCustomerSites,
  summarizeCustomerSites,
} from './customer-sites-model'

const sites: Site[] = [
  {
    id: 'site-1',
    slug: 'docs',
    status: 'active',
    access: 'public',
    spaFallback: false,
    currentVersion: 2,
    totalBytes: 2048,
    fileCount: 10,
    url: 'https://docs.evs.run',
    claimed: true,
    lastPublishedAt: '2026-07-20T12:00:00Z',
  },
  {
    id: 'site-2',
    slug: 'product-preview',
    status: 'disabled',
    access: 'noindex',
    spaFallback: true,
    currentVersion: 7,
    totalBytes: 4096,
    fileCount: 24,
    url: 'https://product-preview.evs.run',
    claimed: true,
    lastPublishedAt: '2026-07-21T12:00:00Z',
  },
]

describe('customer sites dashboard model', () => {
  it('sorts the tenant projects by the latest production publish', () => {
    expect(
      filterCustomerSites(sites, '', 'all').map((site) => site.slug),
    ).toEqual(['product-preview', 'docs'])
  })

  it('filters by customer-visible project and deployment fields', () => {
    expect(filterCustomerSites(sites, 'DOCS.EVS.RUN', 'active')).toEqual([
      sites[0],
    ])
    expect(filterCustomerSites(sites, '', 'disabled')).toEqual([sites[1]])
  })

  it('summarizes only the caller-owned sites supplied by the tenant API', () => {
    expect(summarizeCustomerSites(sites)).toEqual({
      activeSites: 1,
      totalBytes: 6144,
      latestPublish: '2026-07-21T12:00:00Z',
    })
  })
})
