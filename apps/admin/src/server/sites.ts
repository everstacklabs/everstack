import { createServerTransport } from '@/server'
import { create, createClientFor, toDate } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { SitesService } from '@everstack/proto/everstack/hosting/v1/hosting_service_pb'
import {
  DeleteSiteRequestSchema,
  GetSiteRequestSchema,
  ListSitesRequestSchema,
  SiteAccess as ProtoSiteAccess,
  UpdateSiteRequestSchema,
  type Site as ProtoSite,
} from '@everstack/proto/everstack/hosting/v1/hosting_pb'

// ─── evs.run hosted sites (everstack.hosting.v1.SitesService) ───────────────
// The Admin UI uses the same cookie-backed Connect transport as the rest of
// the application. Do not route these calls through the grpc-gateway JSON
// mux: that adds an internal proxy hop where browser session context can be
// lost and the inner interceptor consequently asks the UI for an API key.
// The server scopes every result to the authenticated tenant.

export type SiteStatus = 'active' | 'expired' | 'disabled' | 'unknown'
export type SiteAccess = 'public' | 'noindex'

export interface Site {
  id: string
  slug: string
  status: SiteStatus
  access: SiteAccess
  spaFallback: boolean
  currentVersion: number
  totalBytes: number
  fileCount: number
  /** Live URL, e.g. https://{slug}.evs.run */
  url: string
  claimed: boolean
  /** RFC 3339 timestamp; undefined = permanent (never expires). */
  expiresAt?: string
  createdAt?: string
  lastPublishedAt?: string
}

// Wire shape as emitted by the grpc-gateway (see marshaler notes above).
export interface WireSite {
  id?: string
  slug?: string
  status?: number | string
  access?: number | string
  spa_fallback?: boolean
  current_version?: number
  total_bytes?: number | string
  file_count?: number
  url?: string
  claimed?: boolean
  expires_at?: string
  created_at?: string
  last_published_at?: string
}

const STATUS_FROM_WIRE: Record<string, SiteStatus> = {
  1: 'active',
  2: 'expired',
  3: 'disabled',
  SITE_STATUS_ACTIVE: 'active',
  SITE_STATUS_EXPIRED: 'expired',
  SITE_STATUS_DISABLED: 'disabled',
}

const ACCESS_FROM_WIRE: Record<string, SiteAccess> = {
  1: 'public',
  2: 'noindex',
  SITE_ACCESS_PUBLIC: 'public',
  SITE_ACCESS_NOINDEX: 'noindex',
}

const ACCESS_TO_PROTO: Record<SiteAccess, ProtoSiteAccess> = {
  public: ProtoSiteAccess.PUBLIC,
  noindex: ProtoSiteAccess.NOINDEX,
}

type SiteSource = WireSite | ProtoSite

function timestampToISOString(
  value: ProtoSite['createdAt'] | string | undefined,
): string | undefined {
  if (!value) return undefined
  if (typeof value === 'string') return value
  return toDate(value)?.toISOString()
}

export function normalizeSite(raw: SiteSource): Site {
  const proto = raw as Partial<ProtoSite>
  const wire = raw as WireSite
  return {
    id: raw.id ?? '',
    slug: raw.slug ?? '',
    status: STATUS_FROM_WIRE[String(raw.status)] ?? 'unknown',
    access: ACCESS_FROM_WIRE[String(raw.access)] ?? 'public',
    spaFallback: proto.spaFallback ?? wire.spa_fallback ?? false,
    currentVersion: proto.currentVersion ?? wire.current_version ?? 0,
    totalBytes: Number(proto.totalBytes ?? wire.total_bytes ?? 0),
    fileCount: proto.fileCount ?? wire.file_count ?? 0,
    url: raw.url ?? '',
    claimed: raw.claimed ?? false,
    expiresAt: timestampToISOString(proto.expiresAt ?? wire.expires_at),
    createdAt: timestampToISOString(proto.createdAt ?? wire.created_at),
    lastPublishedAt: timestampToISOString(
      proto.lastPublishedAt ?? wire.last_published_at,
    ),
  }
}

const baseUrl = getApiBaseUrl()
const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>
const connectBase = env.VITE_CONNECT_BASE_PATH ?? ''
const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})
const sitesClient = createClientFor(SitesService)(transport)

export type ListSitesParams = {
  pageSize?: number
  pageToken?: string
}

export async function listSites(
  params?: ListSitesParams,
): Promise<{ sites: Site[]; nextPageToken: string }> {
  const response = await sitesClient.listSites(
    create(ListSitesRequestSchema, {
      pageSize: params?.pageSize ?? 0,
      pageToken: params?.pageToken ?? '',
    }),
  )
  return {
    sites: response.sites.map(normalizeSite),
    nextPageToken: response.nextPageToken,
  }
}

export async function listAllSites(): Promise<Site[]> {
  const sites: Site[] = []
  let pageToken = ''

  do {
    const response = await listSites({
      pageSize: 200,
      pageToken: pageToken || undefined,
    })
    sites.push(...response.sites)
    if (response.nextPageToken === pageToken) break
    pageToken = response.nextPageToken
  } while (pageToken)

  return sites
}

export async function getSite(slug: string): Promise<Site> {
  const response = await sitesClient.getSite(
    create(GetSiteRequestSchema, { slug }),
  )
  return normalizeSite(response.site ?? {})
}

export type UpdateSiteParams = {
  slug: string
  spaFallback?: boolean
  access?: SiteAccess
}

export async function updateSite(params: UpdateSiteParams): Promise<Site> {
  const response = await sitesClient.updateSite(
    create(UpdateSiteRequestSchema, {
      slug: params.slug,
      spaFallback: params.spaFallback,
      access:
        params.access === undefined
          ? undefined
          : ACCESS_TO_PROTO[params.access],
    }),
  )
  return normalizeSite(response.site ?? {})
}

export async function deleteSite(slug: string): Promise<void> {
  await sitesClient.deleteSite(create(DeleteSiteRequestSchema, { slug }))
}
