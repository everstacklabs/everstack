// React Query hooks for bot connections (/api/bots). Same conventions as the
// interop API: the session cookie carries the tenant, writes are JSON, and
// failures are {"error": "<code>"}.
//
// A bot key is returned only by create and rotate. It never enters the query
// or mutation cache: the mutation hands it to the caller's onKey callback and
// resolves with the bot alone.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth'
import { getApiBaseUrl } from '@/lib/api-url'

export interface Bot {
    id: string
    name: string
    kind: string
    surfaces: string[]
    /** The bot key's id. OTLP data sent with the key carries it as the
     *  resource attribute `everstack.api_key_id`. Changes on a new key. */
    api_key_id: string
    key_hint: string
    remote_agent: string | null
    /** Requests per minute across everything the key does; null means no
     *  bot-specific limit. */
    rate_limit_rpm: number | null
    usage: BotUsage
    created_at: string
    last_seen_at: string | null
}

/** Calls in the last 30 UTC days, counted for the current key only. */
export interface BotUsage {
    calls_30d: number
    by_surface: Partial<Record<'inference' | 'mcp' | 'telemetry' | 'a2a', number>>
}

export class BotsApiError extends Error {
    constructor(
        readonly code: string,
        readonly status: number,
    ) {
        super(code)
        this.name = 'BotsApiError'
    }
}

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

const REQUEST_TIMEOUT_MS = 30_000

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(`${getApiBaseUrl()}${path}`, {
        method,
        credentials: 'include',
        headers:
            method === 'GET'
                ? { Accept: 'application/json' }
                : { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        // A gateway that accepts the connection and never answers must not leave
        // a dialog stuck on "Working…" with every way out disabled.
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    })
    if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new BotsApiError(typeof data.error === 'string' ? data.error : `http_${res.status}`, res.status)
    }
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
}

const BOTS_KEY = ['bots']

export function useBots() {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...BOTS_KEY, orgId],
        queryFn: async () => (await request<{ bots: Bot[] }>('GET', '/api/bots')).bots ?? [],
        // A missing or expired session will not fix itself on retry.
        retry: (count, error) => !isSessionError(error) && count < 2,
    })
}

export interface CreateBotInput {
    name: string
    kind: string
    surfaces: string[]
    remote_agent?: string
    /** Omit for no bot-specific limit; the gateway refuses null on create. */
    rate_limit_rpm?: number
    /** Receives the new key. It is not kept anywhere else. */
    onKey: (key: string) => void
}

export function useCreateBot() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: async ({ onKey, ...body }: CreateBotInput) => {
            const res = await request<{ bot: Bot; api_key: string }>('POST', '/api/bots', body)
            onKey(res.api_key)
            return res.bot
        },
        retry: false,
        gcTime: 0,
        onSuccess: () => qc.invalidateQueries({ queryKey: BOTS_KEY }),
    })
}

export interface UpdateBotInput {
    id: string
    name?: string
    surfaces?: string[]
    remote_agent?: string | null
    /** null clears the limit. */
    rate_limit_rpm?: number | null
}

export function useUpdateBot() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: async ({ id, ...body }: UpdateBotInput) =>
            (await request<{ bot: Bot }>('PATCH', `/api/bots/${encodeURIComponent(id)}`, body)).bot,
        retry: false,
        onSuccess: () => qc.invalidateQueries({ queryKey: BOTS_KEY }),
    })
}

export function useRotateBotKey() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: async ({ id, onKey }: { id: string; onKey: (key: string) => void }) => {
            const res = await request<{ bot: Bot; api_key: string }>(
                'POST',
                `/api/bots/${encodeURIComponent(id)}/rotate-key`,
                {},
            )
            onKey(res.api_key)
            return res.bot
        },
        retry: false,
        gcTime: 0,
        onSuccess: () => qc.invalidateQueries({ queryKey: BOTS_KEY }),
    })
}

export function useDeleteBot() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: ({ id }: { id: string }) => request<void>('DELETE', `/api/bots/${encodeURIComponent(id)}`),
        retry: false,
        onSuccess: () => qc.invalidateQueries({ queryKey: BOTS_KEY }),
    })
}

// ─── Error copy ─────────────────────────────────────────────────────

const BOT_ERRORS: Record<string, string> = {
    invalid_name:
        'Use 1 to 64 characters: letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit.',
    invalid_kind: 'This gateway does not support that bot type.',
    invalid_surfaces: 'This bot type cannot use one of the selected surfaces.',
    name_taken: 'A bot with this name already exists.',
    invalid_rate_limit: 'Set the rate limit to a whole number from 1 to 100,000 requests per minute, or leave it empty.',
    remote_not_found: 'That remote agent no longer exists. Choose another one or none.',
    not_found: 'This bot no longer exists. It may have been removed.',
    invalid_body: 'The gateway could not read the request.',
    json_required: 'The gateway expected a JSON request.',
    unauthorized: 'Your session has ended. Sign in again to manage bots.',
    tenant_required: 'Your session is not linked to an organization. Sign in again.',
    internal_error: 'The gateway could not complete the request. Try again.',
    gateway_unreachable: 'Could not reach the gateway.',
    gateway_timeout: 'The gateway did not answer in time. Check the bot list before trying again.',
}

export function botErrorCode(error: unknown): string {
    if (error instanceof BotsApiError) {
        if (error.status === 401 && !BOT_ERRORS[error.code]) return 'unauthorized'
        return error.code
    }
    if (error instanceof TypeError) return 'gateway_unreachable'
    if (error instanceof DOMException && (error.name === 'TimeoutError' || error.name === 'AbortError')) return 'gateway_timeout'
    return 'internal_error'
}

/** True when the dashboard session is missing or not allowed to manage bots. */
export function isSessionError(error: unknown): boolean {
    return error instanceof BotsApiError && (error.status === 401 || error.status === 403)
}

export function describeBotError(error: unknown): string {
    if (error instanceof BotsApiError && error.status === 403 && !BOT_ERRORS[error.code]) {
        return 'Your account is not allowed to manage bots.'
    }
    const code = botErrorCode(error)
    const known = BOT_ERRORS[code]
    if (known) return known
    const status = /^http_(\d{3})$/.exec(code)
    if (status) return `The gateway answered with HTTP ${status[1]}.`
    return `The request failed (${code}).`
}
