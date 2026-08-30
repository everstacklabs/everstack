import { beforeEach, describe, expect, it, vi } from 'vitest'

const rpc = vi.hoisted(() => ({
  listSites: vi.fn(),
  getSite: vi.fn(),
  updateSite: vi.fn(),
  deleteSite: vi.fn(),
}))

vi.mock('@/server', () => ({
  createServerTransport: vi.fn(() => ({})),
}))

vi.mock('@everstack/client', () => ({
  createClientFor: vi.fn(() => () => rpc),
  create: vi.fn((_schema, value) => value),
  toDate: vi.fn((timestamp) => timestamp),
}))

vi.mock('@everstack/proto/everstack/hosting/v1/hosting_service_pb', () => ({
  SitesService: {},
}))

vi.mock('@everstack/proto/everstack/hosting/v1/hosting_pb', () => ({
  DeleteSiteRequestSchema: {},
  GetSiteRequestSchema: {},
  ListSitesRequestSchema: {},
  UpdateSiteRequestSchema: {},
  SiteAccess: {
    PUBLIC: 1,
    NOINDEX: 2,
  },
  SiteStatus: {
    ACTIVE: 1,
    EXPIRED: 2,
    DISABLED: 3,
  },
}))

import { listAllSites, listSites } from './sites'

describe('sites authenticated client', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              code: 16,
              message: 'API key is required',
            }),
            { status: 401 },
          ),
      ),
    )
  })

  it('uses the cookie-backed Connect transport instead of the REST API-key hop', async () => {
    rpc.listSites.mockResolvedValue({
      sites: [
        {
          id: 'site-1',
          slug: 'release-notes',
          status: 1,
          access: 1,
          spaFallback: true,
          currentVersion: 3,
          totalBytes: 4096n,
          fileCount: 12,
          url: 'https://release-notes.evs.run',
          claimed: true,
        },
      ],
      nextPageToken: 'next-page',
    })

    await expect(listSites({ pageSize: 25 })).resolves.toMatchObject({
      sites: [
        {
          slug: 'release-notes',
          status: 'active',
          access: 'public',
          totalBytes: 4096,
        },
      ],
      nextPageToken: 'next-page',
    })
    expect(rpc.listSites).toHaveBeenCalledWith({ pageSize: 25, pageToken: '' })
    expect(fetch).not.toHaveBeenCalled()
  })

  it('loads every customer site page for the project dashboard', async () => {
    rpc.listSites
      .mockResolvedValueOnce({
        sites: [
          {
            id: 'site-1',
            slug: 'docs',
            status: 1,
            access: 1,
            currentVersion: 1,
            totalBytes: 1024n,
            fileCount: 4,
            url: 'https://docs.evs.run',
            claimed: true,
          },
        ],
        nextPageToken: '200',
      })
      .mockResolvedValueOnce({
        sites: [
          {
            id: 'site-2',
            slug: 'status',
            status: 1,
            access: 1,
            currentVersion: 2,
            totalBytes: 2048n,
            fileCount: 8,
            url: 'https://status.evs.run',
            claimed: true,
          },
        ],
        nextPageToken: '',
      })

    await expect(listAllSites()).resolves.toMatchObject([
      { slug: 'docs' },
      { slug: 'status' },
    ])
    expect(rpc.listSites).toHaveBeenNthCalledWith(1, {
      pageSize: 200,
      pageToken: '',
    })
    expect(rpc.listSites).toHaveBeenNthCalledWith(2, {
      pageSize: 200,
      pageToken: '200',
    })
  })
})
