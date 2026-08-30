import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listCollections,
    getCollection,
    createCollection,
    deleteCollection,
    addDocuments,
    queryCollection,
    getMemoryAnalytics,
    type MemoryCollection,
    type MemorySearchResult,
    type MemoryAnalytics,
} from '@/server/memory'
import { useSession } from '@/hooks/auth/use-auth'

const COLLECTIONS_QUERY_KEY = ['memory-collections']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Query Hooks ────────────────────────────────────────────────────

export function useCollections(): UseQueryResult<MemoryCollection[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...COLLECTIONS_QUERY_KEY, orgId],
        queryFn: () => listCollections(orgId),
        enabled: !!orgId,
    })
}

export function useCollection(name: string): UseQueryResult<MemoryCollection, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...COLLECTIONS_QUERY_KEY, orgId, name],
        queryFn: () => getCollection(orgId, name),
        enabled: !!orgId && !!name,
    })
}

// ─── Mutation Hooks ─────────────────────────────────────────────────

export function useCreateCollection(): UseMutationResult<
    MemoryCollection,
    Error,
    { name: string; description?: string; embeddingModel?: string; embeddingDimension?: number; distanceMetric?: string }
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createCollection({ tenantId: orgId, ...params }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: COLLECTIONS_QUERY_KEY })
        },
    })
}

export function useDeleteCollection(): UseMutationResult<void, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (name: string) => deleteCollection(orgId, name),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: COLLECTIONS_QUERY_KEY })
        },
    })
}

export function useAddDocuments(): UseMutationResult<
    { documentIds: string[]; chunksCreated: number },
    Error,
    { collectionName: string; content: string; source?: string; chunkSize?: number }
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => addDocuments({ tenantId: orgId, ...params }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: COLLECTIONS_QUERY_KEY })
        },
    })
}

export function useQueryCollection(): UseMutationResult<
    MemorySearchResult[],
    Error,
    { collectionName: string; query: string; topK?: number; minScore?: number }
> {
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => queryCollection({ tenantId: orgId, ...params }),
    })
}

// ─── Analytics Hook ─────────────────────────────────────────────────

export function useMemoryAnalytics(from: string, to: string): UseQueryResult<MemoryAnalytics, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: ['memory-analytics', orgId, from, to],
        queryFn: () => getMemoryAnalytics(orgId, from, to),
        enabled: !!orgId,
        refetchInterval: 30_000,
    })
}
