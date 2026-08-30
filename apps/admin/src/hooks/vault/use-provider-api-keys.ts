import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listProviderAPIKeys,
    addProviderAPIKey,
    updateAPIKeyWeight,
    toggleAPIKey,
    deleteProviderAPIKey,
    type AddProviderAPIKeyParams,
    type UpdateAPIKeyWeightParams,
    type ToggleAPIKeyParams,
} from '@/server/provider-api-keys'
import type {
    ListProviderAPIKeysResponse,
    AddProviderAPIKeyResponse,
    UpdateAPIKeyWeightResponse,
    ToggleAPIKeyResponse,
    DeleteProviderAPIKeyResponse,
    ProviderAPIKey,
} from '@everstack/proto/everstack/providers/providers_pb'

/**
 * Query key factory for provider API key queries
 * Centralizes query keys for better type safety and maintenance
 */
export const providerAPIKeyKeys = {
    all: ['provider-api-keys'] as const,
    lists: () => [...providerAPIKeyKeys.all, 'list'] as const,
    list: (providerConfigId: string) => [...providerAPIKeyKeys.all, 'list', providerConfigId] as const,
} as const

/**
 * Hook to fetch all API keys for a specific provider configuration
 * 
 * Features:
 * - Always fetches fresh data on mount
 * - Automatically refetches on window focus
 * - Shared cache across all components
 * 
 * @param providerConfigId - The ID of the provider configuration
 * @returns Query result with API keys and loading states
 * 
 * @example
 * ```tsx
 * const { data, isLoading, error } = useProviderAPIKeys('provider-123')
 * const keys = data?.keys ?? []
 * ```
 */
export function useProviderAPIKeys(providerConfigId: string | undefined): UseQueryResult<ListProviderAPIKeysResponse, Error> {
    return useQuery({
        queryKey: providerAPIKeyKeys.list(providerConfigId ?? ''),
        queryFn: () => listProviderAPIKeys(providerConfigId!),
        enabled: !!providerConfigId, // Only fetch if providerConfigId is provided
        staleTime: 0, // Always fetch fresh data
        refetchOnWindowFocus: true,
        refetchOnMount: true,
    })
}

/**
 * Mutation hook to add a new API key to a provider
 * 
 * Features:
 * - Automatically invalidates and refetches API key lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with add function and states
 * 
 * @example
 * ```tsx
 * const addKey = useAddProviderAPIKey()
 * 
 * addKey.mutate({
 *   providerConfigId: 'provider-123',
 *   keyName: 'Production Key 1',
 *   apiKey: 'sk-...',
 *   weight: 3
 * }, {
 *   onSuccess: () => console.log('API key added'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useAddProviderAPIKey(): UseMutationResult<AddProviderAPIKeyResponse, Error, AddProviderAPIKeyParams> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: addProviderAPIKey,
        onSuccess: (_, variables) => {
            // Invalidate the specific provider's API keys
            queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.list(variables.providerConfigId) })
            // Also invalidate all lists
            queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.lists() })
        },
    })
}

/**
 * Mutation hook to update an API key's weight for load balancing
 * 
 * Features:
 * - Automatically invalidates and refetches API key lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with update function and states
 * 
 * @example
 * ```tsx
 * const updateWeight = useUpdateAPIKeyWeight()
 * 
 * updateWeight.mutate({
 *   keyId: 'key-123',
 *   weight: 5,
 *   providerConfigId: 'provider-123' // for cache invalidation
 * }, {
 *   onSuccess: () => console.log('Weight updated'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useUpdateAPIKeyWeight(): UseMutationResult<UpdateAPIKeyWeightResponse, Error, UpdateAPIKeyWeightParams> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: updateAPIKeyWeight,
        onSuccess: (_, variables) => {
            // Invalidate the specific provider's API keys
            if (variables.providerConfigId) {
                queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.list(variables.providerConfigId) })
            }
            // Also invalidate all lists
            queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.lists() })
        },
    })
}

/**
 * Mutation hook to toggle (activate/deactivate) an API key
 * 
 * Features:
 * - Automatically invalidates and refetches API key lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with toggle function and states
 * 
 * @example
 * ```tsx
 * const toggleKey = useToggleAPIKey()
 * 
 * toggleKey.mutate({
 *   keyId: 'key-123',
 *   isActive: false,
 *   providerConfigId: 'provider-123' // for cache invalidation
 * }, {
 *   onSuccess: () => console.log('API key toggled'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useToggleAPIKey(): UseMutationResult<ToggleAPIKeyResponse, Error, ToggleAPIKeyParams> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: toggleAPIKey,
        onSuccess: (_, variables) => {
            // Invalidate the specific provider's API keys
            if (variables.providerConfigId) {
                queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.list(variables.providerConfigId) })
            }
            // Also invalidate all lists
            queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.lists() })
        },
    })
}

/**
 * Mutation hook to delete an API key
 * 
 * Features:
 * - Automatically invalidates and refetches API key lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with delete function and states
 * 
 * @example
 * ```tsx
 * const deleteKey = useDeleteProviderAPIKey()
 * 
 * deleteKey.mutate({
 *   keyId: 'key-123',
 *   providerConfigId: 'provider-123' // for cache invalidation
 * }, {
 *   onSuccess: () => console.log('API key deleted'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useDeleteProviderAPIKey(): UseMutationResult<DeleteProviderAPIKeyResponse, Error, { keyId: string; providerConfigId?: string }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ keyId }) => deleteProviderAPIKey(keyId),
        onSuccess: (_, variables) => {
            // Invalidate the specific provider's API keys
            if (variables.providerConfigId) {
                queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.list(variables.providerConfigId) })
            }
            // Also invalidate all lists
            queryClient.invalidateQueries({ queryKey: providerAPIKeyKeys.lists() })
        },
    })
}

// Re-export types for convenience
export type { ProviderAPIKey, AddProviderAPIKeyParams, UpdateAPIKeyWeightParams, ToggleAPIKeyParams }

