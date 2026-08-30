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
    if (!res.ok) throw new Error(`request failed: ${res.status}`)
    return (await res.json()) as T
}

async function apiSend<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(`${getApiBaseUrl()}${path}`, {
        method,
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
    })
    if (!res.ok) throw new Error(`request failed: ${res.status}`)
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
    { name: string; endpoint: string; auth_token?: string }
> {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (body) => apiSend('POST', '/api/interop/remotes', body),
        onSuccess: () => qc.invalidateQueries({ queryKey: REMOTES_KEY }),
        onError: () => toast.error('Failed to save remote agent'),
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
