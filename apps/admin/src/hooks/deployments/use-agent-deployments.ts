import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    deployAgent,
    listDeployments,
    getDeployment,
    updateDeployment,
    createDeploymentKey,
    listDeploymentKeys,
    revokeDeploymentKey,
    listDeploymentInvocations,
    type DeployAgentParams,
    type UpdateDeploymentParams,
    type CreateDeploymentKeyParams,
    type AgentDeployment,
    type DeploymentKey,
    type DeploymentInvocation,
} from '@/server/agent-deployments'
import type {
    DeployAgentResponse,
    UpdateDeploymentResponse,
    CreateDeploymentKeyResponse,
    RevokeDeploymentKeyResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { useSession } from '@/hooks/auth/use-auth'
import { useMemo } from 'react'

const DEPLOYMENTS_QUERY_KEY = ['agent-deployments']
const DEPLOYMENT_KEYS_QUERY_KEY = ['deployment-keys']
const DEPLOYMENT_INVOCATIONS_QUERY_KEY = ['deployment-invocations']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Deployment Queries ──────────────────────────────────────────────

export function useAgentDeployments(agentId: string): UseQueryResult<AgentDeployment[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DEPLOYMENTS_QUERY_KEY, orgId, agentId],
        queryFn: async () => {
            const response = await listDeployments({ tenantId: orgId, agentId })
            return response.deployments ?? []
        },
        enabled: !!agentId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

/**
 * Set of agent ids that have an *active* deployment for the current tenant.
 * One bulk query (listDeployments with no agentId returns the whole tenant),
 * used to gate A2A/MCP publish controls: an agent is only runnable over those
 * protocols when it has an active deployment (see agentrun.Runner).
 */
export function useActiveDeploymentAgentIds(): Set<string> {
    const orgId = useOrganizationId()
    const { data } = useQuery({
        queryKey: [...DEPLOYMENTS_QUERY_KEY, 'tenant-active', orgId],
        queryFn: async () => {
            const response = await listDeployments({ tenantId: orgId, agentId: '' })
            return response.deployments ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
    return useMemo(() => {
        const ids = new Set<string>()
        for (const d of data ?? []) {
            if (d.status === 'active') ids.add(d.agentId)
        }
        return ids
    }, [data])
}

export function useDeployment(deploymentId: string): UseQueryResult<AgentDeployment | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DEPLOYMENTS_QUERY_KEY, 'detail', orgId, deploymentId],
        queryFn: async () => {
            const response = await getDeployment(orgId, deploymentId)
            return response.deployment
        },
        enabled: !!deploymentId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

// ─── Deployment Mutations ────────────────────────────────────────────

export function useDeployAgent(): UseMutationResult<DeployAgentResponse, Error, Omit<DeployAgentParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => deployAgent({ ...params, tenantId: orgId }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: DEPLOYMENTS_QUERY_KEY }),
    })
}

export function useUpdateDeployment(): UseMutationResult<UpdateDeploymentResponse, Error, Omit<UpdateDeploymentParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateDeployment({ ...params, tenantId: orgId }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: DEPLOYMENTS_QUERY_KEY }),
    })
}

// ─── Deployment Keys ─────────────────────────────────────────────────

export function useDeploymentKeys(deploymentId: string): UseQueryResult<DeploymentKey[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DEPLOYMENT_KEYS_QUERY_KEY, orgId, deploymentId],
        queryFn: async () => {
            const response = await listDeploymentKeys({ tenantId: orgId, deploymentId })
            return response.keys ?? []
        },
        enabled: !!deploymentId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
    })
}

export function useCreateDeploymentKey(): UseMutationResult<CreateDeploymentKeyResponse, Error, Omit<CreateDeploymentKeyParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createDeploymentKey({ ...params, tenantId: orgId }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: DEPLOYMENT_KEYS_QUERY_KEY }),
    })
}

export function useRevokeDeploymentKey(): UseMutationResult<RevokeDeploymentKeyResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (keyId: string) => revokeDeploymentKey(orgId, keyId),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: DEPLOYMENT_KEYS_QUERY_KEY }),
    })
}

// ─── Deployment Invocations ──────────────────────────────────────────

export function useDeploymentInvocations(deploymentId: string, limit = 50): UseQueryResult<{ invocations: DeploymentInvocation[]; total: number }, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DEPLOYMENT_INVOCATIONS_QUERY_KEY, orgId, deploymentId, limit],
        queryFn: async () => {
            const response = await listDeploymentInvocations({ tenantId: orgId, deploymentId, limit })
            return { invocations: response.invocations ?? [], total: response.total }
        },
        enabled: !!deploymentId,
        refetchOnWindowFocus: false,
        staleTime: 10_000,
        refetchInterval: 30_000,
    })
}
