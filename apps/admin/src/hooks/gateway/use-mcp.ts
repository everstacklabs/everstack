import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listMcpServers, getMcpServer, registerMcpServer, updateMcpServer, deleteMcpServer,
    listFederatedTools, discoverTools, getMcpServerHealth, initiateMcpOAuth,
    type McpServer, type McpTool, type McpHealthStatus,
    type RegisterMcpServerParams, type UpdateMcpServerParams,
} from '@/server/mcp'
import { useSession } from '@/hooks/auth/use-auth'

const MCP_SERVERS_KEY = ['mcp-servers']
const MCP_TOOLS_KEY = ['mcp-tools']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Server Query Hooks ─────────────────────────────────────────────

export function useMcpServers(enabledOnly?: boolean): UseQueryResult<McpServer[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...MCP_SERVERS_KEY, orgId, enabledOnly],
        queryFn: () => listMcpServers(orgId, enabledOnly),
        enabled: !!orgId,
        refetchOnWindowFocus: true,
        staleTime: 0,
    })
}

export function useMcpServer(id: string): UseQueryResult<McpServer | undefined, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...MCP_SERVERS_KEY, orgId, id],
        queryFn: async () => {
            if (!id) return undefined
            return getMcpServer(id, orgId)
        },
        enabled: !!orgId && !!id,
    })
}

// ─── Server Mutation Hooks ──────────────────────────────────────────

export function useRegisterMcpServer(): UseMutationResult<
    { server: McpServer; tools: McpTool[] },
    Error,
    Omit<RegisterMcpServerParams, 'tenantId'>
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => registerMcpServer({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: MCP_SERVERS_KEY })
            queryClient.invalidateQueries({ queryKey: MCP_TOOLS_KEY })
        },
    })
}

export function useUpdateMcpServer(): UseMutationResult<McpServer, Error, Omit<UpdateMcpServerParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateMcpServer({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: MCP_SERVERS_KEY })
        },
    })
}

export function useDeleteMcpServer(): UseMutationResult<void, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => deleteMcpServer(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: MCP_SERVERS_KEY })
            queryClient.invalidateQueries({ queryKey: MCP_TOOLS_KEY })
        },
    })
}

// ─── Tool Query Hooks ───────────────────────────────────────────────

export function useFederatedTools(opts?: { serverIds?: string[]; namePrefix?: string }): UseQueryResult<McpTool[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...MCP_TOOLS_KEY, orgId, opts],
        queryFn: () => listFederatedTools(orgId, opts),
        enabled: !!orgId,
        refetchOnWindowFocus: true,
        staleTime: 0,
    })
}

export function useDiscoverTools(): UseMutationResult<McpTool[], Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (serverId) => discoverTools(serverId, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: MCP_TOOLS_KEY })
        },
    })
}

// ─── Health Check Hook ──────────────────────────────────────────────

export function useMcpServerHealth(serverId: string): UseQueryResult<{ status: McpHealthStatus; error?: string }, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...MCP_SERVERS_KEY, 'health', orgId, serverId],
        queryFn: () => getMcpServerHealth(serverId, orgId),
        enabled: !!orgId && !!serverId,
        refetchInterval: 30000,
    })
}

// ─── OAuth Hook ─────────────────────────────────────────────────────

export function useInitiateMcpOAuth(): UseMutationResult<{ authorization_url: string }, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (serverId: string) => initiateMcpOAuth({ tenantId: orgId, serverId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: MCP_SERVERS_KEY })
        },
    })
}
