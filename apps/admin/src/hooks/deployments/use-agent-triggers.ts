import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listAgentTriggers,
    getAgentTrigger,
    createAgentTrigger,
    updateAgentTrigger,
    deleteAgentTrigger,
    testAgentTrigger,
    listAgentTriggerExecutions,
    type CreateTriggerParams,
    type UpdateTriggerParams,
    type AgentTrigger,
    type AgentTriggerExecution,
} from '@/server/agent-triggers'
import type {
    CreateAgentTriggerResponse,
    UpdateAgentTriggerResponse,
    DeleteAgentTriggerResponse,
    TestAgentTriggerResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { useSession } from '@/hooks/auth/use-auth'

const TRIGGERS_QUERY_KEY = ['agent-triggers']
const EXECUTIONS_QUERY_KEY = ['agent-trigger-executions']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Query Hooks ─────────────────────────────────────────────────────

export function useAgentTriggers(agentId: string): UseQueryResult<AgentTrigger[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TRIGGERS_QUERY_KEY, orgId, agentId],
        queryFn: async () => {
            const response = await listAgentTriggers(agentId, orgId)
            return response.triggers ?? []
        },
        enabled: !!agentId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useAgentTrigger(id: string): UseQueryResult<AgentTrigger | undefined, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TRIGGERS_QUERY_KEY, orgId, 'detail', id],
        queryFn: async () => {
            if (!id) return undefined
            const response = await getAgentTrigger(id, orgId)
            return response.trigger
        },
        enabled: !!id && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useTriggerExecutions(triggerId: string, limit?: number): UseQueryResult<{ executions: AgentTriggerExecution[]; total: number }, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...EXECUTIONS_QUERY_KEY, orgId, triggerId, limit],
        queryFn: async () => {
            const response = await listAgentTriggerExecutions({
                tenantId: orgId,
                triggerId,
                limit,
            })
            return {
                executions: response.executions ?? [],
                total: response.total,
            }
        },
        enabled: !!triggerId && !!orgId,
        refetchOnWindowFocus: true,
        staleTime: 10_000,
    })
}

// ─── Mutation Hooks ──────────────────────────────────────────────────

export function useCreateTrigger(): UseMutationResult<CreateAgentTriggerResponse, Error, Omit<CreateTriggerParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createAgentTrigger({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: TRIGGERS_QUERY_KEY })
        },
    })
}

export function useUpdateTrigger(): UseMutationResult<UpdateAgentTriggerResponse, Error, Omit<UpdateTriggerParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateAgentTrigger({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: TRIGGERS_QUERY_KEY })
        },
    })
}

export function useDeleteTrigger(): UseMutationResult<DeleteAgentTriggerResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => deleteAgentTrigger(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: TRIGGERS_QUERY_KEY })
        },
    })
}

export function useTestTrigger(): UseMutationResult<TestAgentTriggerResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => testAgentTrigger(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: EXECUTIONS_QUERY_KEY })
        },
    })
}
