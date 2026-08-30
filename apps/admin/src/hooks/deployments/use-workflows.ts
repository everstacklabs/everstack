import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listWorkflows,
    getWorkflow,
    createWorkflow,
    updateWorkflow,
    deleteWorkflow,
    getWorkflowVersionHistory,
    getWorkflowAtVersion,
    saveWorkflowDraft,
    publishWorkflow,
    unpublishWorkflow,
    type CreateWorkflowParams,
    type UpdateWorkflowParams,
    type SaveWorkflowDraftParams,
    type ListWorkflowsParams,
    type Workflow,
    type WorkflowVersionEntry,
    type GetWorkflowAtVersionResponse,
    type SaveWorkflowDraftResponse,
    type PublishWorkflowResponse,
    type UnpublishWorkflowResponse,
} from '@/server/workflows'
import type {
    CreateWorkflowResponse,
    UpdateWorkflowResponse,
    DeleteWorkflowResponse,
} from '@everstack/proto/everstack/workflows/v1/workflows_pb'
import { useSession } from '@/hooks/auth/use-auth'

const WORKFLOWS_QUERY_KEY = ['workflows']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useWorkflows(params: Omit<ListWorkflowsParams, 'tenantId'> = {}): UseQueryResult<Workflow[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...WORKFLOWS_QUERY_KEY, orgId, params],
        queryFn: async () => {
            const response = await listWorkflows({ ...params, tenantId: orgId })
            return response.workflows ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function useWorkflow(id: string): UseQueryResult<Workflow | undefined, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...WORKFLOWS_QUERY_KEY, orgId, id],
        queryFn: async () => {
            if (!id) return undefined
            const response = await getWorkflow(id, orgId)
            return response.workflow
        },
        enabled: !!id && !!orgId,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
        retry: 5,
        retryDelay: (attempt) => Math.min(300 * 2 ** attempt, 3000),
    })
}

export function useCreateWorkflow(): UseMutationResult<CreateWorkflowResponse, Error, Omit<CreateWorkflowParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createWorkflow({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: WORKFLOWS_QUERY_KEY })
        },
    })
}

export function useUpdateWorkflow(): UseMutationResult<UpdateWorkflowResponse, Error, Omit<UpdateWorkflowParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateWorkflow({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: WORKFLOWS_QUERY_KEY })
        },
    })
}

export function useDeleteWorkflow(): UseMutationResult<DeleteWorkflowResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => deleteWorkflow(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: WORKFLOWS_QUERY_KEY })
        },
    })
}

export function useWorkflowVersionHistory(workflowId: string | null): UseQueryResult<WorkflowVersionEntry[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...WORKFLOWS_QUERY_KEY, orgId, workflowId, 'versions'],
        queryFn: async () => {
            const response = await getWorkflowVersionHistory(workflowId!, orgId)
            return response.versions ?? []
        },
        enabled: !!workflowId && !!orgId,
        staleTime: 30_000,
    })
}

export function useWorkflowAtVersion(
    workflowId: string | null,
    version: number | null,
): UseQueryResult<GetWorkflowAtVersionResponse, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...WORKFLOWS_QUERY_KEY, orgId, workflowId, 'version', version],
        queryFn: () => getWorkflowAtVersion(workflowId!, orgId, version!),
        enabled: !!workflowId && !!orgId && version !== null && version >= 1,
        staleTime: 60_000,
    })
}

export function useSaveWorkflowDraft(): UseMutationResult<SaveWorkflowDraftResponse, Error, Omit<SaveWorkflowDraftParams, 'tenantId'>> {
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => saveWorkflowDraft({ ...params, tenantId: orgId }),
        // No query invalidation — draft saves are frequent and don't change list state
    })
}

export function usePublishWorkflow(): UseMutationResult<PublishWorkflowResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => publishWorkflow(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: WORKFLOWS_QUERY_KEY })
        },
    })
}

export function useUnpublishWorkflow(): UseMutationResult<UnpublishWorkflowResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => unpublishWorkflow(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: WORKFLOWS_QUERY_KEY })
        },
    })
}
