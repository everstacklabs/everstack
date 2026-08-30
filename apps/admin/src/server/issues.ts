import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { IssuesService } from '@everstack/proto/everstack/issues/v1/issues_service_pb'
import type {
  ListIssuesResponse,
  GetIssueResponse,
  UpdateIssueStatusResponse,
  IssueStatus,
} from '@everstack/proto/everstack/issues/v1/issues_pb'
import {
  ListIssuesRequestSchema,
  GetIssueRequestSchema,
  UpdateIssueStatusRequestSchema,
} from '@everstack/proto/everstack/issues/v1/issues_pb'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

function timestampFromDate(date: Date) {
  const ms = date.getTime()
  return {
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1_000_000,
  }
}

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})
const issuesClient = createClientFor(IssuesService)(transport)

export interface ListIssuesParams {
  from?: Date
  to?: Date
  statusFilter?: IssueStatus
  query?: string
  limit?: number
  offset?: number
}

export async function listIssues(params: ListIssuesParams = {}): Promise<ListIssuesResponse> {
  const req = create(ListIssuesRequestSchema, {
    from: params.from ? timestampFromDate(params.from) : undefined,
    to: params.to ? timestampFromDate(params.to) : undefined,
    statusFilter: params.statusFilter,
    query: params.query ?? '',
    limit: params.limit ?? 100,
    offset: params.offset ?? 0,
  })
  return issuesClient.listIssues(req)
}

export async function getIssue(
  fingerprint: string,
  params: { from?: Date; to?: Date; interval?: string } = {},
): Promise<GetIssueResponse> {
  const req = create(GetIssueRequestSchema, {
    fingerprint,
    from: params.from ? timestampFromDate(params.from) : undefined,
    to: params.to ? timestampFromDate(params.to) : undefined,
    interval: params.interval ?? '',
  })
  return issuesClient.getIssue(req)
}

export async function updateIssueStatus(params: {
  fingerprint: string
  status: IssueStatus
  signature?: string
  title?: string
  assignee?: string
}): Promise<UpdateIssueStatusResponse> {
  const req = create(UpdateIssueStatusRequestSchema, {
    fingerprint: params.fingerprint,
    status: params.status,
    signature: params.signature ?? '',
    title: params.title ?? '',
    // undefined = leave unchanged; a string (incl. "") sets/clears the assignee.
    assignee: params.assignee,
  })
  return issuesClient.updateIssueStatus(req)
}
