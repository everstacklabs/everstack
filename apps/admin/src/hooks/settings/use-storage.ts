import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listStorageConfigs,
    configureStorage,
    updateStorageConfig,
    deleteStorageConfig,
    getStorageUsage,
    listObjects,
    deleteObject,
    getPresignedDownloadURL,
    ObjectPurpose,
    type ConfigureStorageParams,
    type UpdateStorageConfigParams,
    type StorageConfig,
    type StorageObject,
} from '@/server/storage'
import { useSession } from '@/hooks/auth'
import { useLicenseStatus } from '@/hooks/license/use-license-status'
import { getApiBaseUrl } from '@/lib/api-url'

const STORAGE_CONFIGS_KEY = ['storage-configs']
const STORAGE_USAGE_KEY = ['storage-usage']
const STORAGE_OBJECTS_KEY = ['storage-objects']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useStorageConfigs(): UseQueryResult<StorageConfig[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...STORAGE_CONFIGS_KEY, orgId],
        queryFn: async () => {
            const response = await listStorageConfigs({ tenantId: orgId })
            return response.configs ?? []
        },
        enabled: !!orgId,
    })
}

export function useStorageUsage(): UseQueryResult<{ bytesUsed: bigint; bytesQuota: bigint; objectCount: number }, Error> {
    const orgId = useOrganizationId()
    const { data: licenseData } = useLicenseStatus()

    // Resolve quota from the plan's STORAGE_BYTES usage limit
    const storageLimit = licenseData?.license?.usage_limits?.find(
        (l) => l.type === 'STORAGE_BYTES',
    )
    // -1 means unlimited, 0 or undefined means no limit configured
    const quotaBytes = storageLimit?.limit ?? 0

    return useQuery({
        queryKey: [...STORAGE_USAGE_KEY, orgId, quotaBytes],
        queryFn: async () => {
            const response = await getStorageUsage(orgId)
            return {
                bytesUsed: BigInt(response.usage?.totalBytes ?? 0),
                bytesQuota: quotaBytes > 0 ? BigInt(quotaBytes) : BigInt(0),
                objectCount: Number(response.usage?.objectCount ?? 0),
            }
        },
        enabled: !!orgId,
    })
}

export function useConfigureStorage(): UseMutationResult<unknown, Error, Omit<ConfigureStorageParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => configureStorage({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: STORAGE_CONFIGS_KEY })
            queryClient.invalidateQueries({ queryKey: STORAGE_USAGE_KEY })
        },
    })
}

export function useUpdateStorageConfig(): UseMutationResult<unknown, Error, Omit<UpdateStorageConfigParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateStorageConfig({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: STORAGE_CONFIGS_KEY })
        },
    })
}

export function useDeleteStorageConfig(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteStorageConfig(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: STORAGE_CONFIGS_KEY })
            queryClient.invalidateQueries({ queryKey: STORAGE_USAGE_KEY })
        },
    })
}

export function useStorageObjects(purpose?: ObjectPurpose): UseQueryResult<StorageObject[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...STORAGE_OBJECTS_KEY, orgId, purpose ?? 'all'],
        queryFn: async () => {
            const response = await listObjects({
                tenantId: orgId,
                purpose: purpose || undefined,
            })
            return (response.objects ?? []) as StorageObject[]
        },
        enabled: !!orgId,
    })
}

export function useStorageObjectsByReference(
    referenceType: string,
    referenceId: string,
): UseQueryResult<StorageObject[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...STORAGE_OBJECTS_KEY, orgId, 'ref', referenceType, referenceId],
        queryFn: async () => {
            const response = await listObjects({
                tenantId: orgId,
                referenceType,
                referenceId,
            })
            return (response.objects ?? []) as StorageObject[]
        },
        enabled: !!orgId && !!referenceType && !!referenceId,
    })
}

export function useDeleteObject(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (objectId: string) => deleteObject(objectId, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: STORAGE_OBJECTS_KEY })
            queryClient.invalidateQueries({ queryKey: STORAGE_USAGE_KEY })
        },
    })
}

export function useDownloadObject() {
    const orgId = useOrganizationId()
    return async (objectId: string) => {
        const response = await getPresignedDownloadURL({ tenantId: orgId, objectId })
        if (response.downloadUrl) {
            window.open(response.downloadUrl, '_blank')
        }
    }
}

export function useSyncObjects(): UseMutationResult<{ synced: number; total: number }, Error, string | undefined> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: async (configId?: string) => {
            const baseUrl = getApiBaseUrl()
            const res = await fetch(`${baseUrl}/api/v1/storage/sync`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'include',
                body: JSON.stringify({ tenant_id: orgId, ...(configId ? { config_id: configId } : {}) }),
            })
            if (!res.ok) {
                const text = await res.text()
                throw new Error(text || 'Sync failed')
            }
            return res.json()
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: STORAGE_OBJECTS_KEY })
            queryClient.invalidateQueries({ queryKey: STORAGE_USAGE_KEY })
        },
    })
}
