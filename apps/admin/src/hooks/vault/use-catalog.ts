import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
    getCatalogStatus,
    getChangelog,
    getNewModels,
    triggerSync,
    type CatalogStatus,
    type Changelog,
    type NewModels,
    type SyncStatus,
} from '@/server/catalog'

/**
 * Hook to fetch catalog status
 */
export function useCatalogStatus() {
    return useQuery<CatalogStatus>({
        queryKey: ['catalog', 'status'],
        queryFn: getCatalogStatus,
        refetchInterval: 60000, // Refetch every minute
    })
}

/**
 * Hook to fetch changelog
 */
export function useChangelog(fromVersion?: string) {
    return useQuery<Changelog>({
        queryKey: ['catalog', 'changelog', fromVersion],
        queryFn: () => getChangelog(fromVersion),
        refetchInterval: 60000,
    })
}

/**
 * Hook to fetch new models
 */
export function useNewModels(provider?: string) {
    return useQuery<NewModels>({
        queryKey: ['catalog', 'newModels', provider],
        queryFn: () => getNewModels(provider),
        refetchInterval: 60000,
    })
}

/**
 * Hook to trigger manual catalog sync
 */
export function useTriggerCatalogSync() {
    const queryClient = useQueryClient()

    return useMutation<SyncStatus, Error, boolean>({
        mutationFn: (force = false) => triggerSync(force),
        onSuccess: () => {
            // Invalidate catalog queries to refetch latest data
            queryClient.invalidateQueries({ queryKey: ['catalog'] })
        },
    })
}
