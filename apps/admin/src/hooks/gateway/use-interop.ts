// React Query hooks for the interop control plane (/api/interop/*): A2A publish
// flags, MCP tool exposure overrides, the saved-remote registry, and ADK
// runtime status. The backend resolves tenant from the session cookie, so these
// just fetch with credentials included.
import {
    useQuery,
    useMutation,
    useQueryClient,
    type UseQueryResult,
    type UseMutationResult,
} from '@tanstack/react-query'
import { toast } from '@everstack/ui/components'
import { useSession } from '@/hooks/auth'
import { getApiBaseUrl } from '@/lib/api-url'

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

async function apiGet<T>(path: string): Promise<T> {
    const res = await fetch(`${getApiBaseUrl()}${path}`, {
        credentials: 'include',
        headers: { Accept: 'application/json' },
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(typeof body.error === 'string' ? body.error : `request failed: ${res.status}`)
    }
    return (await res.json()) as T
}

async function apiSend<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(`${getApiBaseUrl()}${path}`, {
        method,
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(typeof body.error === 'string' ? body.error : `request failed: ${res.status}`)
    }
    return (await res.json()) as T
}

// ─── A2A publish ────────────────────────────────────────────────────

const A2A_PUBLISHED_KEY = ['interop', 'a2a-published']

export function useA2aPublished(): UseQueryResult<Record<string, boolean>, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...A2A_PUBLISHED_KEY, orgId],
        queryFn: async () =>
            (await apiGet<{ published: Record<string, boolean> }>('/api/interop/a2a/published')).published ?? {},
    })
}

type FlagSnapshot = { previous: [readonly unknown[], unknown][] }

export function useSetA2aPublished(): UseMutationResult<
    unknown,
    Error,
    { agentId: string; enabled: boolean },
    FlagSnapshot
> {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: ({ agentId, enabled }) =>
            apiSend('PUT', `/api/interop/a2a/published/${encodeURIComponent(agentId)}`, { enabled }),
        // Optimistically flip the toggle so it responds instantly; the switch is
        // a controlled component, so without this it wouldn't move until the
        // round-trip + refetch completed (which reads as "the switch doesn't work").
        onMutate: async ({ agentId, enabled }) => {
            await qc.cancelQueries({ queryKey: A2A_PUBLISHED_KEY })
            const previous = qc.getQueriesData({ queryKey: A2A_PUBLISHED_KEY })
            qc.setQueriesData<Record<string, boolean>>({ queryKey: A2A_PUBLISHED_KEY }, (old) => ({
                ...(old ?? {}),
                [agentId]: enabled,
            }))
            return { previous }
        },
        onError: (_err, _vars, ctx) => {
            ctx?.previous.forEach(([key, data]) => qc.setQueryData(key, data))
            toast.error('Failed to update A2A publish setting')
        },
        onSettled: () => qc.invalidateQueries({ queryKey: A2A_PUBLISHED_KEY }),
    })
}

// ─── MCP tool settings ──────────────────────────────────────────────

const MCP_TOOLS_KEY = ['interop', 'mcp-tools']

export function useMcpToolSettings(): UseQueryResult<Record<string, boolean>, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...MCP_TOOLS_KEY, orgId],
        queryFn: async () => (await apiGet<{ tools: Record<string, boolean> }>('/api/interop/mcp/tools')).tools ?? {},
    })
}

export function useSetMcpTool(): UseMutationResult<
    unknown,
    Error,
    { tool: string; enabled: boolean },
    FlagSnapshot
> {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: ({ tool, enabled }) =>
            apiSend('PUT', `/api/interop/mcp/tools/${encodeURIComponent(tool)}`, { enabled }),
        onMutate: async ({ tool, enabled }) => {
            await qc.cancelQueries({ queryKey: MCP_TOOLS_KEY })
            const previous = qc.getQueriesData({ queryKey: MCP_TOOLS_KEY })
            qc.setQueriesData<Record<string, boolean>>({ queryKey: MCP_TOOLS_KEY }, (old) => ({
                ...(old ?? {}),
                [tool]: enabled,
            }))
            return { previous }
        },
        onError: (_err, _vars, ctx) => {
            ctx?.previous.forEach(([key, data]) => qc.setQueryData(key, data))
            toast.error('Failed to update tool exposure')
        },
        onSettled: () => qc.invalidateQueries({ queryKey: MCP_TOOLS_KEY }),
    })
}

// ─── Remote agents ──────────────────────────────────────────────────

export interface RemoteAgent {
    id: string
    name: string
    endpoint: string
    protocol: 'a2a-legacy' | 'a2a-1.0'
    routing_tenant?: string
    has_credential: boolean
    created_at: string
    updated_at: string
}

const REMOTES_KEY = ['interop', 'remotes']

export function useRemoteAgents(): UseQueryResult<RemoteAgent[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...REMOTES_KEY, orgId],
        queryFn: async () => (await apiGet<{ remotes: RemoteAgent[] }>('/api/interop/remotes')).remotes ?? [],
    })
}

export function useUpsertRemoteAgent(): UseMutationResult<
    unknown,
    Error,
    { name: string; endpoint: string; auth_token?: string; protocol?: 'a2a-legacy' | 'a2a-1.0'; routing_tenant?: string }
> {
    const qc = useQueryClient()
    // No error toast: the remote agent dialog shows the reason inline.
    return useMutation({
        mutationFn: (body) => apiSend('POST', '/api/interop/remotes', body),
        onSuccess: () => qc.invalidateQueries({ queryKey: REMOTES_KEY }),
    })
}

export function useDeleteRemoteAgent(): UseMutationResult<unknown, Error, { id: string }> {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: ({ id }) => apiSend('DELETE', `/api/interop/remotes/${encodeURIComponent(id)}`),
        onSuccess: () => qc.invalidateQueries({ queryKey: REMOTES_KEY }),
        onError: () => toast.error('Failed to remove remote agent'),
    })
}

// ─── ADK runtime status ─────────────────────────────────────────────

export interface AdkStatus {
    enabled: boolean
    instance_capable: boolean
    tenant_enabled: boolean
    shared_mode: boolean
    network_mode: string
}

const ADK_STATUS_KEY = ['interop', 'adk-status']

export function useAdkStatus(): UseQueryResult<AdkStatus, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...ADK_STATUS_KEY, orgId],
        queryFn: async () => apiGet<AdkStatus>('/api/interop/adk/status'),
    })
}


export interface ExternalRun {
    id: string
    remote_id: string
    remote_name: string
    protocol: string
    state: 'dispatching' | 'submitted' | 'working' | 'input-required' | 'auth-required' | 'completed' | 'failed' | 'canceled' | 'rejected' | 'unknown'
    remote_task_id?: string
    remote_context_id?: string
    output: string
    last_error?: string
    cancel_requested_at?: string
    created_at: string
    updated_at: string
}
const RUNS_KEY = ['interop', 'external-runs']
export function useExternalRuns() {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...RUNS_KEY, orgId],
        queryFn: async () => (await apiGet<{ runs: ExternalRun[] }>('/api/interop/runs')).runs,
        refetchInterval: 5000,
    })
}
export function useExternalRun(id: string) {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...RUNS_KEY, orgId, id],
        queryFn: () => apiGet<ExternalRun>(`/api/interop/runs/${encodeURIComponent(id)}`),
        enabled: Boolean(id),
        refetchInterval: id ? 5000 : false,
    })
}
export function useStartExternalRun() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (body: { remote: string; message: string; idempotency_key: string }) =>
            apiSend<ExternalRun>('POST', '/api/interop/runs', body),
        retry: false,
        onSuccess: () => qc.invalidateQueries({ queryKey: RUNS_KEY }),
    })
}
export function useExternalRunAction() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: ({ id, action }: { id: string; action: 'refresh' | 'cancel' }) =>
            apiSend<ExternalRun>('POST', `/api/interop/runs/${encodeURIComponent(id)}/${action}`),
        retry: false,
        onSuccess: () => qc.invalidateQueries({ queryKey: RUNS_KEY }),
    })
}

// ─── Error copy ─────────────────────────────────────────────────────

// The interop API answers failures with a fixed code (`{"error": "<code>"}`),
// which apiGet/apiSend rethrow as the Error message; runs keep the code of
// their last remote call in `last_error`. `http_<n>` and `rpc_<n>` describe the
// remote agent's answer, not the gateway's. These helpers turn any of them into
// a sentence and decide whether resending the same submission makes sense.

const PROTOCOL_HINT = 'Check the protocol setting for this connection.'

const INTEROP_ERRORS: Record<string, string> = {
    // Client-side outcomes with no code from the gateway.
    gateway_unreachable: 'Could not reach the gateway, so it is unclear whether the request arrived.',
    gateway_bad_response: 'The gateway response could not be read, so it is unclear whether the request arrived.',
    // Gateway codes.
    tenant_required: 'Your session is not linked to an organization. Sign in again.',
    not_found: 'Not found. It may have been removed.',
    invalid_request: 'The gateway rejected the request as invalid. Check the fields and try again.',
    idempotency_conflict:
        'This submission was already used for a different task, so it was not sent. Submit again to send it as new work.',
    operation_in_progress: 'Another operation on this run is still in progress. Try again in a moment.',
    no_remote_task_handle:
        'The remote agent never returned a task handle, so this run cannot be checked or canceled from here.',
    remote_connection_changed:
        'This connection was changed or removed after the run started, so the run can no longer be checked or canceled from here.',
    credential_replacement_required:
        'Changing the endpoint, protocol or routing tenant requires replacing or removing the saved credential.',
    credential_encryption_unavailable:
        'The server has no credential encryption key configured, so tokens cannot be saved. Ask your operator to configure one.',
    json_required: 'The gateway expected a JSON request.',
    operation_failed: 'The gateway could not complete the operation.',
    // Connection checks.
    invalid_endpoint: 'The endpoint must be a full http:// or https:// URL, without a user name or fragment.',
    https_required: 'A bearer token can only be sent to an https:// endpoint.',
    unsupported_protocol: 'Choose a supported protocol.',
    invalid_credential: 'The bearer token cannot contain line breaks.',
    invalid_routing_tenant: 'A routing tenant can only be set for A2A 1.0 connections (up to 1024 characters).',
    invalid_message: 'The task is empty or over the 65,536 byte limit.',
    // Remote agent answers.
    transport_error: 'Could not connect to the remote agent.',
    read_failed: 'The connection to the remote agent dropped while its response was being read.',
    response_too_large: 'The remote agent response was over the 8 MB limit.',
    invalid_content_type: 'The remote agent did not answer with JSON. Check the endpoint URL.',
    invalid_envelope: `The remote agent did not send a valid JSON-RPC response. ${PROTOCOL_HINT}`,
    invalid_rpc_error: `The remote agent sent a malformed JSON-RPC error. ${PROTOCOL_HINT}`,
    remote_rpc_error: 'The remote agent reported an error.',
    remote_unavailable: 'The remote agent could not be reached.',
    unsupported_content: 'The remote agent answered with content other than text, which cannot be shown here.',
    invalid_task_state: 'The remote agent reported a task state that is not recognized.',
    invalid_task_id: 'The remote agent returned an unusable task ID.',
    task_id_mismatch: 'The remote agent answered about a different task.',
    task_identity_changed: 'The remote agent returned a different task than the one on record.',
    task_state_regressed: 'The remote agent reported an earlier state than before, so the update was ignored.',
    dispatch_interrupted: 'The gateway stopped while sending this task, so its outcome is unknown.',
    http_401: 'The remote agent did not accept the credential (HTTP 401). Check the saved bearer token.',
    http_403: 'The remote agent denied access (HTTP 403). Check the saved bearer token.',
    http_404: 'The remote agent endpoint was not found (HTTP 404). Check the endpoint URL.',
    http_405: 'The remote agent endpoint does not accept this request (HTTP 405). Check the endpoint URL.',
    http_429: 'The remote agent is rate limiting requests (HTTP 429). Try again later.',
    'rpc_-32600': `The remote agent rejected the request format (JSON-RPC -32600). ${PROTOCOL_HINT}`,
    'rpc_-32601': `The remote agent does not support this method (JSON-RPC -32601). ${PROTOCOL_HINT}`,
    'rpc_-32602': `The remote agent rejected the request parameters (JSON-RPC -32602). ${PROTOCOL_HINT}`,
    'rpc_-32603': 'The remote agent hit an internal error (JSON-RPC -32603).',
    'rpc_-32001': 'The remote agent does not know this task (A2A -32001).',
    'rpc_-32002': 'The task can no longer be canceled (A2A -32002).',
    'rpc_-32004': 'The remote agent does not support this operation (A2A -32004).',
    'rpc_-32005': 'The remote agent does not accept text messages (A2A -32005).',
    'rpc_-32006': 'The remote agent ran but returned an invalid response (A2A -32006).',
}

// Failures where the request may have reached its target, so resending the
// unchanged submission (same idempotency key) is the right recovery.
const RETRYABLE_ERRORS = new Set([
    'gateway_unreachable',
    'gateway_bad_response',
    'transport_error',
    'read_failed',
    'operation_failed',
    'operation_in_progress',
])

/** Normalizes a thrown interop error or a stored `last_error` to one code. */
export function interopErrorCode(error: unknown): string {
    if (typeof error === 'string') return error
    // fetch rejects with a TypeError when no response arrived at all.
    if (error instanceof TypeError) return 'gateway_unreachable'
    if (error instanceof SyntaxError) return 'gateway_bad_response'
    if (error instanceof Error) {
        // apiGet/apiSend fall back to this when the gateway sent no code.
        const status = /^request failed: (\d{3})$/.exec(error.message)
        return status ? `gateway_http_${status[1]}` : error.message
    }
    return 'operation_failed'
}

export function describeInteropError(error: unknown): string {
    const code = interopErrorCode(error)
    const known = INTEROP_ERRORS[code]
    if (known) return known
    let match = /^http_(\d{3})$/.exec(code)
    if (match) {
        return Number(match[1]) >= 500
            ? `The remote agent had a server error (HTTP ${match[1]}).`
            : `The remote agent answered with HTTP ${match[1]}.`
    }
    match = /^gateway_http_(\d{3})$/.exec(code)
    if (match) return `The gateway answered with HTTP ${match[1]}.`
    match = /^rpc_(-?\d+)$/.exec(code)
    if (match) return `The remote agent returned JSON-RPC error ${match[1]}.`
    if (/^[a-z0-9_-]+$/.test(code)) return `The operation failed (${code}).`
    // Some handlers still answer with a short sentence instead of a code.
    const sentence = code.trim()
    if (!sentence) return INTEROP_ERRORS.operation_failed
    const capitalized = sentence.charAt(0).toUpperCase() + sentence.slice(1)
    return /[.!?]$/.test(capitalized) ? capitalized : `${capitalized}.`
}

export function isRetryableInteropError(error: unknown): boolean {
    const code = interopErrorCode(error)
    return RETRYABLE_ERRORS.has(code) || /^(gateway_)?http_5\d\d$/.test(code)
}
