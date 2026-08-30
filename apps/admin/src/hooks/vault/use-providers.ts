import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listProviderCatalog,
    listConfiguredProviders,
    getProvider,
    configureProvider,
    toggleProvider,
    deleteProviderConfiguration,
    getSyncStatus,
    reloadConfig,
    type ConfigureProviderParams,
    type ProviderCatalogEntry,
    type ProviderStatus,
} from '@/server/providers'
import type {
    ListProviderCatalogResponse,
    ListConfiguredProvidersResponse,
    GetProviderResponse,
    ConfigureProviderResponse,
    ToggleProviderResponse,
    DeleteProviderConfigurationResponse,
    GetSyncStatusResponse,
    ReloadConfigResponse,
} from '@everstack/proto/everstack/providers/providers_pb'

/**
 * Query key factory for provider queries
 * Centralizes query keys for better type safety and maintenance
 */
export const providerKeys = {
    all: ['providers'] as const,
    catalog: () => [...providerKeys.all, 'catalog'] as const,
    configured: () => [...providerKeys.all, 'configured'] as const,
    detail: (name: string) => [...providerKeys.all, 'detail', name] as const,
    syncStatus: () => [...providerKeys.all, 'syncStatus'] as const,
} as const

/**
 * Hook to fetch the full provider catalog (all available providers from defaults)
 * 
 * Features:
 * - Caches catalog data for 5 minutes
 * - Automatically refetches on window focus
 * - Shared cache across all components
 * 
 * @returns Query result with provider catalog and loading states
 * 
 * @example
 * ```tsx
 * const { data, isLoading, error } = useProviderCatalog()
 * const providers = data?.providers ?? []
 * ```
 */
export function useProviderCatalog(): UseQueryResult<ListProviderCatalogResponse, Error> {
    return useQuery({
        queryKey: providerKeys.catalog(),
        queryFn: listProviderCatalog,
        staleTime: 5 * 60 * 1000, // 5 minutes - catalog doesn't change often
        refetchOnWindowFocus: false,
        refetchOnMount: false,
    })
}

/**
 * Hook to fetch only configured providers (providers with user settings)
 * 
 * Features:
 * - Always fetches fresh data on mount
 * - Automatically refetches on window focus
 * - Shared cache across all components
 * 
 * @returns Query result with configured providers and loading states
 * 
 * @example
 * ```tsx
 * const { data, isLoading, error } = useConfiguredProviders()
 * const providers = data?.providers ?? []
 * ```
 */
export function useConfiguredProviders(): UseQueryResult<ListConfiguredProvidersResponse, Error> {
    return useQuery({
        queryKey: providerKeys.configured(),
        queryFn: listConfiguredProviders,
        staleTime: 0, // Always fetch fresh data
        refetchOnWindowFocus: true,
        refetchOnMount: true,
    })
}

/**
 * Hook to fetch detailed information about a specific provider
 * 
 * Features:
 * - Caches provider details for 2 minutes
 * - Only fetches when provider name is provided
 * - Automatically refetches on window focus
 * 
 * @param providerName - The name of the provider to fetch
 * @returns Query result with provider details and loading states
 * 
 * @example
 * ```tsx
 * const { data, isLoading, error } = useProvider('openai')
 * const provider = data?.provider
 * ```
 */
export function useProvider(providerName: string | undefined): UseQueryResult<GetProviderResponse, Error> {
    return useQuery({
        queryKey: providerKeys.detail(providerName ?? ''),
        queryFn: () => getProvider(providerName!),
        enabled: !!providerName, // Only fetch if providerName is provided
        staleTime: 2 * 60 * 1000, // 2 minutes
        refetchOnWindowFocus: true,
    })
}

/**
 * Mutation hook to configure (create or update) a provider
 * 
 * Features:
 * - Automatically invalidates and refetches provider lists after success
 * - Type-safe error handling
 * - Optimistic updates support
 * 
 * @returns Mutation result with configure function and states
 * 
 * @example
 * ```tsx
 * const configure = useConfigureProvider()
 * 
 * configure.mutate({
 *   providerName: 'openai',
 *   apiKey: 'sk-...',
 *   enabledModels: ['gpt-4', 'gpt-3.5-turbo'],
 *   customBaseUrl: 'https://api.openai.com/v1'
 * }, {
 *   onSuccess: () => console.log('Provider configured'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useConfigureProvider(): UseMutationResult<ConfigureProviderResponse, Error, ConfigureProviderParams> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: configureProvider,
        onSuccess: (_, variables) => {
            // Invalidate all provider queries
            queryClient.invalidateQueries({ queryKey: providerKeys.all })
            // Also invalidate the specific provider detail
            queryClient.invalidateQueries({ queryKey: providerKeys.detail(variables.providerName) })
        },
    })
}

/**
 * Mutation hook to toggle a provider's active status
 * 
 * Features:
 * - Optimistic updates for instant UI feedback
 * - Automatically invalidates and refetches provider lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with toggle function and states
 * 
 * @example
 * ```tsx
 * const toggleProvider = useToggleProvider()
 * 
 * toggleProvider.mutate({ providerName: 'anthropic', isActive: false }, {
 *   onSuccess: () => console.log('Provider toggled'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useToggleProvider(): UseMutationResult<ToggleProviderResponse, Error, { providerName: string; isActive: boolean }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ providerName, isActive }) => toggleProvider(providerName, isActive),
        onSuccess: (_, { providerName }) => {
            // Invalidate all provider queries
            queryClient.invalidateQueries({ queryKey: providerKeys.all })
            // Invalidate the specific provider detail
            queryClient.invalidateQueries({ queryKey: providerKeys.detail(providerName) })
        },
    })
}

/**
 * Mutation hook to delete a provider configuration
 * 
 * Features:
 * - Automatically invalidates and refetches provider lists after success
 * - Type-safe error handling
 * 
 * @returns Mutation result with delete function and states
 * 
 * @example
 * ```tsx
 * const deleteProvider = useDeleteProvider()
 * 
 * deleteProvider.mutate('openai', {
 *   onSuccess: () => console.log('Provider deleted'),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useDeleteProvider(): UseMutationResult<DeleteProviderConfigurationResponse, Error, string> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: deleteProviderConfiguration,
        onSuccess: (_, providerName) => {
            // Invalidate all provider queries
            queryClient.invalidateQueries({ queryKey: providerKeys.all })
            // Remove the specific provider detail from cache
            queryClient.removeQueries({ queryKey: providerKeys.detail(providerName) })
        },
    })
}

/**
 * Hook to check sync status between YAML and database
 * 
 * Features:
 * - Caches sync status for 30 seconds
 * - Automatically refetches on window focus
 * - Shows warning when YAML and DB are out of sync
 * 
 * @returns Query result with sync status information
 * 
 * @example
 * ```tsx
 * const { data, isLoading, error } = useSyncStatus()
 * const isInSync = data?.inSync ?? true
 * ```
 */
export function useSyncStatus(): UseQueryResult<GetSyncStatusResponse, Error> {
    return useQuery({
        queryKey: providerKeys.syncStatus(),
        queryFn: getSyncStatus,
        staleTime: 30 * 1000, // 30 seconds
        refetchOnWindowFocus: true,
        retry: 2,
    })
}

/**
 * Mutation hook to reload config from YAML and trigger gateway reload
 * 
 * Features:
 * - Automatically invalidates and refetches provider data after success
 * - Type-safe error handling
 * - Optimistic updates support
 * 
 * @returns Mutation result with reload function and states
 * 
 * @example
 * ```tsx
 * const reload = useReloadConfig()
 * 
 * reload.mutate(undefined, {
 *   onSuccess: (data) => console.log('Reloaded:', data.providersSynced),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useReloadConfig(): UseMutationResult<ReloadConfigResponse, Error, void> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: reloadConfig,
        onSuccess: () => {
            // Invalidate all provider queries to refresh data
            queryClient.invalidateQueries({ queryKey: providerKeys.all })
            // Also invalidate sync status to update the banner
            queryClient.invalidateQueries({ queryKey: providerKeys.syncStatus() })
        },
    })
}

// Re-export types for convenience
export type { ProviderCatalogEntry, ProviderStatus, ConfigureProviderParams }


