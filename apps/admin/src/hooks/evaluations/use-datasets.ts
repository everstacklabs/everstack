import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listDatasets,
    getDataset,
    createDataset,
    updateDataset,
    deleteDataset,
    listDatasetItems,
    createDatasetItem,
    createDatasetItemBatch,
    deleteDatasetItem,
    type CreateDatasetParams,
    type UpdateDatasetParams,
    type CreateDatasetItemParams,
    type CreateDatasetItemBatchParams,
    type Dataset,
    type DatasetItem,
} from '@/server/datasets'
import { useSession } from '@/hooks/auth'

const DATASETS_KEY = ['datasets']
const DATASET_ITEMS_KEY = ['dataset-items']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useDatasets(): UseQueryResult<Dataset[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DATASETS_KEY, orgId],
        queryFn: async () => {
            const response = await listDatasets({ tenantId: orgId })
            return response.datasets ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useDataset(datasetId: string): UseQueryResult<Dataset | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DATASETS_KEY, 'detail', orgId, datasetId],
        queryFn: async () => {
            const response = await getDataset(datasetId, orgId)
            return response.dataset
        },
        enabled: !!datasetId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useDatasetItems(datasetId: string, limit = 100): UseQueryResult<DatasetItem[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...DATASET_ITEMS_KEY, orgId, datasetId, limit],
        queryFn: async () => {
            const response = await listDatasetItems({ tenantId: orgId, datasetId, limit })
            return response.items ?? []
        },
        enabled: !!datasetId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
    })
}

export function useCreateDataset(): UseMutationResult<unknown, Error, Omit<CreateDatasetParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createDataset({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}

export function useUpdateDataset(): UseMutationResult<unknown, Error, Omit<UpdateDatasetParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateDataset({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}

export function useDeleteDataset(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteDataset(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}

export function useCreateDatasetItem(): UseMutationResult<unknown, Error, Omit<CreateDatasetItemParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createDatasetItem({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASET_ITEMS_KEY })
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}

export function useCreateDatasetItemBatch(): UseMutationResult<unknown, Error, Omit<CreateDatasetItemBatchParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createDatasetItemBatch({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASET_ITEMS_KEY })
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}

export function useDeleteDatasetItem(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteDatasetItem(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: DATASET_ITEMS_KEY })
            await queryClient.invalidateQueries({ queryKey: DATASETS_KEY })
        },
    })
}
