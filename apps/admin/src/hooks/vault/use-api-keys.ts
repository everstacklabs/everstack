import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listApiKeys,
    createApiKey,
    deleteApiKey,
    type CreateApiKeyParams,
    type ListApiKeysParams,
    type ApiKey,
} from '@/server/api-keys'
import type { CreateApiKeyResponse, DeleteApiKeyResponse } from '@everstack/proto/everstack/api_key/v1/api_key_pb'

const API_KEYS_QUERY_KEY = ['api-keys']

export function useApiKeys(params: ListApiKeysParams = {}): UseQueryResult<ApiKey[], Error> {
    return useQuery({
        queryKey: [...API_KEYS_QUERY_KEY, params],
        queryFn: async () => {
            const response = await listApiKeys(params)
            return response.apiKeys ?? []
        },
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function useCreateApiKey(): UseMutationResult<CreateApiKeyResponse, Error, CreateApiKeyParams> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: createApiKey,
        onSuccess: () => {
            // Invalidate and refetch all api keys queries
            queryClient.invalidateQueries({ queryKey: API_KEYS_QUERY_KEY })
        },
    })
}

export function useDeleteApiKey(): UseMutationResult<DeleteApiKeyResponse, Error, string> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: deleteApiKey,
        onSuccess: () => {
            // Invalidate and refetch all api keys queries
            queryClient.invalidateQueries({ queryKey: API_KEYS_QUERY_KEY })
        },
    })
}

