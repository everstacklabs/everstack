import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listAgentMemories,
    createAgentMemory,
    deleteAgentMemory,
    updateAgentMemory,
    deactivateAgentMemory,
    type ListAgentMemoriesParams,
    type CreateAgentMemoryParams,
    type DeleteAgentMemoryParams,
    type UpdateAgentMemoryParams,
    type DeactivateAgentMemoryParams,
    type AgentMemoryEntry,
} from '@/server/agents'
import type {
    CreateAgentMemoryResponse,
    DeleteAgentMemoryResponse,
    UpdateAgentMemoryResponse,
    DeactivateAgentMemoryResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { useSession } from '@/hooks/auth/use-auth'

const AGENT_MEMORIES_QUERY_KEY = ['agent-memories']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useAgentMemories(
    agentId: string,
    options?: Pick<ListAgentMemoriesParams, 'memoryType' | 'scope' | 'activeOnly' | 'limit' | 'offset'>,
): UseQueryResult<AgentMemoryEntry[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...AGENT_MEMORIES_QUERY_KEY, orgId, agentId, options?.memoryType, options?.scope, options?.activeOnly],
        queryFn: async () => {
            const response = await listAgentMemories({
                tenantId: orgId,
                agentId,
                ...options,
            })
            return (response.memories ?? []) as AgentMemoryEntry[]
        },
        enabled: !!orgId && !!agentId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useCreateAgentMemory(): UseMutationResult<
    CreateAgentMemoryResponse,
    Error,
    Omit<CreateAgentMemoryParams, 'tenantId'>
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createAgentMemory({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: AGENT_MEMORIES_QUERY_KEY })
        },
    })
}

export function useUpdateAgentMemory(): UseMutationResult<
    UpdateAgentMemoryResponse,
    Error,
    Omit<UpdateAgentMemoryParams, 'tenantId'>
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateAgentMemory({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: AGENT_MEMORIES_QUERY_KEY })
        },
    })
}

export function useDeactivateAgentMemory(): UseMutationResult<
    DeactivateAgentMemoryResponse,
    Error,
    Omit<DeactivateAgentMemoryParams, 'tenantId'>
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => deactivateAgentMemory({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: AGENT_MEMORIES_QUERY_KEY })
        },
    })
}

export function useDeleteAgentMemory(): UseMutationResult<
    DeleteAgentMemoryResponse,
    Error,
    Omit<DeleteAgentMemoryParams, 'tenantId'>
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => deleteAgentMemory({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: AGENT_MEMORIES_QUERY_KEY })
        },
    })
}
