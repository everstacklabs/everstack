import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
    searchModels,
    addCustomModel,
    listCustomModels,
    deleteCustomModel,
    updateCustomModel,
    getCustomModel,
} from '../server/model-discovery'
import { toast } from '@everstack/ui/components'

// Query keys
export const modelDiscoveryKeys = {
    all: ['model-discovery'] as const,
    search: (provider: string, query: string, customBaseUrl?: string) =>
        [...modelDiscoveryKeys.all, 'search', provider, query, customBaseUrl] as const,
    customModels: (provider?: string) => [...modelDiscoveryKeys.all, 'custom-models', provider] as const,
    customModel: (id: string) => [...modelDiscoveryKeys.all, 'custom-model', id] as const,
}

// Helper to clean up gRPC error messages
function cleanErrorMessage(error: unknown): string {
    if (!error) return 'An unknown error occurred'

    const message = error instanceof Error ? error.message : String(error)

    // Remove gRPC error codes like "[unknown]", "[internal]", etc.
    const cleaned = message
        .replace(/^\[[\w\s]+\]\s*/gi, '')  // Remove [unknown], [internal], etc.
        .replace(/^(unknown|internal|unavailable|unauthenticated):\s*/gi, '')  // Remove error type prefix
        .trim()

    return cleaned || 'An error occurred'
}

/**
 * Hook to search for models from a meta-provider
 */
export function useSearchModels(
    providerName: string,
    query: string = '',
    options?: {
        enabled?: boolean
        limit?: number
        useProviderApiKey?: boolean
        customBaseUrl?: string
        refetchInterval?: number
    }
) {
    return useQuery({
        queryKey: modelDiscoveryKeys.search(providerName, query, options?.customBaseUrl),
        queryFn: async () => {
            try {
                return await searchModels({
                    providerName,
                    query,
                    limit: options?.limit,
                    useProviderApiKey: options?.useProviderApiKey,
                    customBaseUrl: options?.customBaseUrl,
                })
            } catch (error) {
                // Clean up the error message
                const cleanMessage = cleanErrorMessage(error)
                throw new Error(cleanMessage)
            }
        },
        enabled: options?.enabled !== false && providerName !== '',
        staleTime: options?.refetchInterval ? 0 : 5 * 60 * 1000, // No stale time if polling
        refetchInterval: options?.refetchInterval, // Enable polling if specified
    })
}

/**
 * Hook to list custom models
 */
export function useCustomModels(providerName?: string, activeOnly: boolean = true) {
    return useQuery({
        queryKey: modelDiscoveryKeys.customModels(providerName),
        queryFn: () => listCustomModels({ providerName, activeOnly }),
        staleTime: 30 * 1000, // 30 seconds
    })
}

/**
 * Hook to get a specific custom model
 */
export function useCustomModel(id: string, enabled: boolean = true) {
    return useQuery({
        queryKey: modelDiscoveryKeys.customModel(id),
        queryFn: () => getCustomModel(id),
        enabled: enabled && id !== '',
    })
}

/**
 * Hook to add a custom model
 */
export function useAddCustomModel() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: addCustomModel,
        onSuccess: (data) => {
            // Invalidate custom models list
            queryClient.invalidateQueries({ queryKey: modelDiscoveryKeys.all })
            toast.success(`Added custom model: ${data.model?.displayName}`)
        },
        onError: (error: Error) => {
            toast.error(`Failed to add custom model: ${error.message}`)
        },
    })
}

/**
 * Hook to update a custom model
 */
export function useUpdateCustomModel() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: updateCustomModel,
        onSuccess: (data) => {
            // Invalidate custom models list and specific model
            queryClient.invalidateQueries({ queryKey: modelDiscoveryKeys.all })
            toast.success(`Updated custom model: ${data.model?.displayName}`)
        },
        onError: (error: Error) => {
            toast.error(`Failed to update custom model: ${error.message}`)
        },
    })
}

/**
 * Hook to delete a custom model
 */
export function useDeleteCustomModel() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: deleteCustomModel,
        onSuccess: () => {
            // Invalidate custom models list
            queryClient.invalidateQueries({ queryKey: modelDiscoveryKeys.all })
            toast.success('Custom model deleted successfully')
        },
        onError: (error: Error) => {
            toast.error(`Failed to delete custom model: ${error.message}`)
        },
    })
}

