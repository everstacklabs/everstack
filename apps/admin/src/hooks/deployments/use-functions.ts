import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listFunctions,
    getFunction,
    createFunction,
    updateFunction,
    deleteFunction,
    getIsolationStatus,
    type CreateFunctionParams,
    type UpdateFunctionParams,
    type ListFunctionsParams,
    type Function,
    type GetIsolationStatusResponse,
} from '@/server/functions'
import type {
    CreateFunctionResponse,
    UpdateFunctionResponse,
    DeleteFunctionResponse,
} from '@everstack/proto/everstack/functions/v1/functions_pb'
import { useSession } from '@/hooks/auth/use-auth'

const FUNCTIONS_QUERY_KEY = ['functions']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useFunctions(params: Omit<ListFunctionsParams, 'tenantId'> = {}): UseQueryResult<Function[], Error> {
    const orgId = useOrganizationId()
    const { limit, offset } = params

    return useQuery({
        queryKey: [...FUNCTIONS_QUERY_KEY, orgId, limit ?? null, offset ?? null],
        queryFn: async () => {
            const response = await listFunctions({ ...params, tenantId: orgId })
            return response.functions ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function useFunction(id: string): UseQueryResult<Function | undefined, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...FUNCTIONS_QUERY_KEY, orgId, id],
        queryFn: async () => {
            if (!id) return undefined
            const response = await getFunction(id, orgId)
            return response.function
        },
        enabled: !!id && !!orgId,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function useCreateFunction(): UseMutationResult<CreateFunctionResponse, Error, Omit<CreateFunctionParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createFunction({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FUNCTIONS_QUERY_KEY })
        },
    })
}

export function useUpdateFunction(): UseMutationResult<UpdateFunctionResponse, Error, Omit<UpdateFunctionParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateFunction({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FUNCTIONS_QUERY_KEY })
        },
    })
}

export function useDeleteFunction(): UseMutationResult<DeleteFunctionResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => deleteFunction(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FUNCTIONS_QUERY_KEY })
        },
    })
}

const ISOLATION_STATUS_QUERY_KEY = ['isolation-status']

export function useIsolationStatus(): UseQueryResult<GetIsolationStatusResponse, Error> {
    return useQuery({
        queryKey: ISOLATION_STATUS_QUERY_KEY,
        queryFn: () => getIsolationStatus(),
        staleTime: 30_000, // 30 seconds
        refetchOnWindowFocus: false,
    })
}
