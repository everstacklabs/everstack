import { getApiBaseUrl } from '@/lib/api-url'
import { getActiveOrgId } from '@/lib/active-org'

export interface OpenAIConnection {
  id: string
  name: string
  status: string
  last_error?: string
  allowed_models?: string[]
}
export interface OpenAISession {
  environment_cleanup_status?: string
  id: string
  connection_id: string
  model: string
  status: string
  readiness: string
  cleanup_status: string
  last_error?: string
  sandbox_id?: string
  environment: { type: string }
  created_at?: string
  updated_at?: string
}
export interface OpenAIEvent {
  cursor: number
  event_id: string
  type: string
  upstream_item_id?: string
  payload: Record<string, unknown>
  tool_failed?: boolean
}
export interface OpenAICapabilities {
  enabled: boolean
  environment_modes: string[]
  executor_version?: string
  can_manage_connections: boolean
  can_run_sessions: boolean
  can_resolve_approvals: boolean
  tool_types: string[]
  file_operations: string[]
}
export interface OpenAIConnectionInput {
  name: string
  api_key: string
  executor_key?: string
  project_id?: string
  organization_id?: string
  principal_id?: string
  allowed_models?: string[]
}
export interface OpenAISessionInput {
  connection_id: string
  idempotency_key: string
  model: string
  instructions?: string
  environment: { type: string }
  input: string
  tools?: OpenAIToolSpec[]
}

export interface OpenAIToolSpec {
  type: 'function' | 'mcp'
  resource_id: string
  name: string
}
export interface OpenAIArtifact {
  kind: 'input' | 'output'
  object_id: string
  filename: string
  content_type: string
  size_bytes: number
  sha256: string
  created_at: string
}
export interface OpenAIApproval {
  id: string
  session_id: string
  turn_id: string
  call_id: string
  tool_name: string
  resource_kind?: string
  resource_id?: string
  requested_by?: string
  arguments?: Record<string, unknown>
  status: string
  requested_at: string
  expires_at: string
}
export interface OpenAIUsage {
  status: 'unavailable' | 'reported'
  input_tokens?: number | null
  output_tokens?: number | null
  total_tokens?: number | null
  updated_at?: string
  source: 'openai'
}

export class OpenAIRequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
  ) {
    super(message)
    this.name = 'OpenAIRequestError'
  }
}

export async function openAIRequest<T>(
  path: string,
  method = 'GET',
  body?: unknown,
): Promise<T> {
  const organizationId = getActiveOrgId()
  const response = await fetch(`${getApiBaseUrl()}/v1/openai-agents${path}`, {
    method,
    credentials: 'include',
    redirect: 'error',
    headers: {
      'Content-Type': 'application/json',
      ...(organizationId ? { 'x-org-id': organizationId } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(60_000),
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    const message =
      typeof payload?.error === 'string'
        ? payload.error
        : (payload?.error?.message ??
          payload?.message ??
          `Request failed (${response.status})`)
    throw new OpenAIRequestError(message, response.status, payload?.error?.code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export function eventText(event: OpenAIEvent): string {
  const payload = event.payload
  if (typeof payload.delta === 'string') return payload.delta
  if (typeof payload.text === 'string') return payload.text
  const item = payload.item as Record<string, unknown> | undefined
  const content = item?.content ?? payload.content
  if (Array.isArray(content))
    return content
      .map((part: unknown) => {
        if (
          part &&
          typeof part === 'object' &&
          'text' in part &&
          typeof part.text === 'string'
        )
          return part.text
        return ''
      })
      .join('')
  return ''
}

export function openAIEventRole(event: OpenAIEvent): string {
  const item = event.payload.item as Record<string, unknown> | undefined
  if (typeof item?.role === 'string') return item.role
  if (typeof event.payload.role === 'string') return event.payload.role
  if (event.tool_failed) return 'tool'
  if (event.type.includes('.output_text.')) return 'assistant'
  return 'unknown'
}

function eventItemId(event: OpenAIEvent): string | undefined {
  const item = event.payload.item as Record<string, unknown> | undefined
  // item.added/item.done carry the identity inside item; streamed text and
  // recovered snapshots expose the same identity in different fields.
  const id = event.upstream_item_id ?? item?.id ?? event.payload.item_id
  return typeof id === 'string' && id ? id : undefined
}

/** A saved complete message replaces its streamed fragments in the visible history. */
export function coalesceOpenAIHistory(events: OpenAIEvent[]): OpenAIEvent[] {
  const rows: OpenAIEvent[] = []
  const messages = new Map<string, { index: number; complete: boolean }>()
  for (const event of events) {
    const text = eventText(event)
    const id = eventItemId(event)
    if (!text || !id || event.tool_failed) {
      rows.push(event)
      continue
    }
    const delta = typeof event.payload.delta === 'string'
    const previous = messages.get(id)
    if (previous) {
      if (previous.complete && delta) continue
      const row = rows[previous.index]!
      rows[previous.index] = {
        ...event,
        cursor: row.cursor,
        payload: {
          ...event.payload,
          role:
            openAIEventRole(event) === 'unknown'
              ? openAIEventRole(row)
              : openAIEventRole(event),
          delta: undefined,
          text: delta ? eventText(row) + text : text,
        },
      }
      previous.complete = !delta
    } else {
      messages.set(id, { index: rows.length, complete: !delta })
      rows.push(event)
    }
  }
  return rows
}
