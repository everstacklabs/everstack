import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listQueues,
    getQueue,
    createQueue,
    deleteQueue,
    listQueueItems,
    getQueueStats,
    addItemToQueue,
    addItemsToQueueBatch,
    submitAnnotation,
    populateFromTraces,
    type CreateQueueParams,
    type AddItemToQueueParams,
    type SubmitAnnotationParams,
    type PopulateFromTracesParams,
    type AnnotationQueue,
    type AnnotationQueueItem,
    type QueueStats,
} from '@/server/annotations'
import { listDatasetItems } from '@/server/datasets'
import { useSession } from '@/hooks/auth'

const QUEUES_KEY = ['annotation-queues']
const QUEUE_ITEMS_KEY = ['annotation-queue-items']
const QUEUE_STATS_KEY = ['annotation-queue-stats']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useAnnotationQueues(): UseQueryResult<AnnotationQueue[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...QUEUES_KEY, orgId],
        queryFn: async () => {
            const response = await listQueues({ tenantId: orgId })
            return response.queues ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useAnnotationQueue(queueId: string): UseQueryResult<AnnotationQueue | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...QUEUES_KEY, 'detail', orgId, queueId],
        queryFn: async () => {
            const response = await getQueue(queueId, orgId)
            return response.queue
        },
        enabled: !!queueId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useQueueItems(queueId: string, status?: number, limit = 100): UseQueryResult<AnnotationQueueItem[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...QUEUE_ITEMS_KEY, orgId, queueId, status, limit],
        queryFn: async () => {
            const response = await listQueueItems({ tenantId: orgId, queueId, status, limit })
            return response.items ?? []
        },
        enabled: !!queueId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 10_000,
    })
}

export function useQueueStats(queueId: string): UseQueryResult<QueueStats | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...QUEUE_STATS_KEY, orgId, queueId],
        queryFn: async () => {
            const response = await getQueueStats(queueId, orgId)
            return response.stats
        },
        enabled: !!queueId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
    })
}

export function useCreateQueue(): UseMutationResult<unknown, Error, Omit<CreateQueueParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createQueue({ ...params, tenantId: orgId }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: QUEUES_KEY }),
    })
}

export function useDeleteQueue(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteQueue(id, orgId),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: QUEUES_KEY }),
    })
}

export function useAddItemToQueue(): UseMutationResult<unknown, Error, Omit<AddItemToQueueParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => addItemToQueue({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: QUEUE_ITEMS_KEY })
            queryClient.invalidateQueries({ queryKey: QUEUE_STATS_KEY })
        },
    })
}

export function useSubmitAnnotation(): UseMutationResult<unknown, Error, Omit<SubmitAnnotationParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => submitAnnotation({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: QUEUE_ITEMS_KEY })
            queryClient.invalidateQueries({ queryKey: QUEUE_STATS_KEY })
        },
    })
}

export function usePopulateFromTraces(): UseMutationResult<unknown, Error, Omit<PopulateFromTracesParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => populateFromTraces({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: QUEUE_ITEMS_KEY })
            queryClient.invalidateQueries({ queryKey: QUEUE_STATS_KEY })
        },
    })
}

export type PopulateFromDatasetParams = {
    queueId: string
    datasetId: string
    maxItems?: number
}

export function usePopulateFromDataset(): UseMutationResult<{ addedCount: number }, Error, PopulateFromDatasetParams> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: async (params) => {
            const limit = params.maxItems ?? 1000
            const response = await listDatasetItems({ tenantId: orgId, datasetId: params.datasetId, limit })
            const items = (response.items ?? [])
                .filter((item: any) => item.sourceTraceId)
                .map((item: any) => ({
                    traceId: item.sourceTraceId as string,
                    observationId: item.sourceObservationId || undefined,
                }))

            if (items.length === 0) {
                return { addedCount: 0 }
            }

            const result = await addItemsToQueueBatch({
                tenantId: orgId,
                queueId: params.queueId,
                items,
            })
            return { addedCount: (result as any).addedCount ?? items.length }
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: QUEUE_ITEMS_KEY })
            queryClient.invalidateQueries({ queryKey: QUEUE_STATS_KEY })
        },
    })
}
