import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ObjectStorageService } from '@everstack/proto/everstack/storage/v1/storage_service_pb'
import {
    StorageProvider,
    ObjectPurpose,
    type ConfigureStorageRequest,
    type ConfigureStorageResponse,
    type GetStorageConfigRequest,
    type GetStorageConfigResponse,
    type ListStorageConfigsRequest,
    type ListStorageConfigsResponse,
    type UpdateStorageConfigRequest,
    type UpdateStorageConfigResponse,
    type DeleteStorageConfigRequest,
    type DeleteStorageConfigResponse,
    type GetPresignedUploadURLRequest,
    type GetPresignedUploadURLResponse,
    type CompleteUploadRequest,
    type CompleteUploadResponse,
    type GetPresignedDownloadURLRequest,
    type GetPresignedDownloadURLResponse,
    type DeleteObjectRequest,
    type DeleteObjectResponse,
    type ListObjectsRequest,
    type ListObjectsResponse,
    type GetStorageUsageRequest,
    type GetStorageUsageResponse,
    type StorageConfig,
    type StorageObject,
} from '@everstack/proto/everstack/storage/v1/storage_pb'
import {
    ConfigureStorageRequestSchema,
    GetStorageConfigRequestSchema,
    ListStorageConfigsRequestSchema,
    UpdateStorageConfigRequestSchema,
    DeleteStorageConfigRequestSchema,
    GetPresignedUploadURLRequestSchema,
    CompleteUploadRequestSchema,
    GetPresignedDownloadURLRequestSchema,
    DeleteObjectRequestSchema,
    ListObjectsRequestSchema,
    GetStorageUsageRequestSchema,
} from '@everstack/proto/everstack/storage/v1/storage_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const storageClient = createClientFor(ObjectStorageService)(transport)

// ─── Storage Config CRUD ─────────────────────────────────────────────

export type ConfigureStorageParams = {
    tenantId: string
    provider: StorageProvider
    bucket: string
    region?: string
    endpoint?: string
    accessKeyId?: string
    secretAccessKey?: string
    pathPrefix?: string
}

export async function configureStorage(params: ConfigureStorageParams): Promise<ConfigureStorageResponse> {
    const req: ConfigureStorageRequest = create(ConfigureStorageRequestSchema, {
        tenantId: params.tenantId,
        provider: params.provider,
        bucket: params.bucket,
        region: params.region,
        endpoint: params.endpoint,
        accessKeyId: params.accessKeyId,
        secretAccessKey: params.secretAccessKey,
        pathPrefix: params.pathPrefix,
    })
    return storageClient.configureStorage(req)
}

export async function getStorageConfig(id: string, tenantId: string): Promise<GetStorageConfigResponse> {
    const req: GetStorageConfigRequest = create(GetStorageConfigRequestSchema, { tenantId, configId: id })
    return storageClient.getStorageConfig(req)
}

export type ListStorageConfigsParams = {
    tenantId?: string
}

export async function listStorageConfigs(params: ListStorageConfigsParams = {}): Promise<ListStorageConfigsResponse> {
    const req: ListStorageConfigsRequest = create(ListStorageConfigsRequestSchema, {
        tenantId: params.tenantId ?? '',
    })
    return storageClient.listStorageConfigs(req)
}

export type UpdateStorageConfigParams = {
    tenantId: string
    id: string
    bucket?: string
    region?: string
    endpoint?: string
    accessKeyId?: string
    secretAccessKey?: string
    pathPrefix?: string
    enabled?: boolean
}

export async function updateStorageConfig(params: UpdateStorageConfigParams): Promise<UpdateStorageConfigResponse> {
    const req: UpdateStorageConfigRequest = create(UpdateStorageConfigRequestSchema, {
        tenantId: params.tenantId,
        configId: params.id,
        bucket: params.bucket,
        region: params.region,
        endpoint: params.endpoint,
        accessKeyId: params.accessKeyId,
        secretAccessKey: params.secretAccessKey,
        pathPrefix: params.pathPrefix,
        enabled: params.enabled,
    })
    return storageClient.updateStorageConfig(req)
}

export async function deleteStorageConfig(id: string, tenantId: string): Promise<DeleteStorageConfigResponse> {
    const req: DeleteStorageConfigRequest = create(DeleteStorageConfigRequestSchema, { tenantId, configId: id })
    return storageClient.deleteStorageConfig(req)
}

// ─── Object Operations ───────────────────────────────────────────────

export type GetPresignedUploadURLParams = {
    tenantId: string
    purpose: ObjectPurpose
    filename: string
    contentType: string
    referenceId?: string
    referenceType?: string
}

export async function getPresignedUploadURL(params: GetPresignedUploadURLParams): Promise<GetPresignedUploadURLResponse> {
    const req: GetPresignedUploadURLRequest = create(GetPresignedUploadURLRequestSchema, {
        tenantId: params.tenantId,
        purpose: params.purpose,
        filename: params.filename,
        contentType: params.contentType,
        referenceId: params.referenceId,
        referenceType: params.referenceType,
    })
    return storageClient.getPresignedUploadURL(req)
}

export type CompleteUploadParams = {
    tenantId: string
    objectId: string
    checksumSha256?: string
    sizeBytes?: bigint
}

export async function completeUpload(params: CompleteUploadParams): Promise<CompleteUploadResponse> {
    const req: CompleteUploadRequest = create(CompleteUploadRequestSchema, {
        tenantId: params.tenantId,
        objectId: params.objectId,
        checksumSha256: params.checksumSha256,
        sizeBytes: params.sizeBytes,
    })
    return storageClient.completeUpload(req)
}

export type GetPresignedDownloadURLParams = {
    tenantId: string
    objectId: string
}

export async function getPresignedDownloadURL(params: GetPresignedDownloadURLParams): Promise<GetPresignedDownloadURLResponse> {
    const req: GetPresignedDownloadURLRequest = create(GetPresignedDownloadURLRequestSchema, {
        tenantId: params.tenantId,
        objectId: params.objectId,
    })
    return storageClient.getPresignedDownloadURL(req)
}

export async function deleteObject(objectId: string, tenantId: string): Promise<DeleteObjectResponse> {
    const req: DeleteObjectRequest = create(DeleteObjectRequestSchema, { tenantId, objectId })
    return storageClient.deleteObject(req)
}

export type ListObjectsParams = {
    tenantId?: string
    purpose?: ObjectPurpose
    referenceId?: string
    referenceType?: string
    pageSize?: number
    pageToken?: string
}

export async function listObjects(params: ListObjectsParams = {}): Promise<ListObjectsResponse> {
    const req: ListObjectsRequest = create(ListObjectsRequestSchema, {
        tenantId: params.tenantId ?? '',
        purpose: params.purpose,
        referenceId: params.referenceId,
        referenceType: params.referenceType,
        pageSize: params.pageSize,
        pageToken: params.pageToken,
    })
    return storageClient.listObjects(req)
}

export async function getStorageUsage(tenantId: string): Promise<GetStorageUsageResponse> {
    const req: GetStorageUsageRequest = create(GetStorageUsageRequestSchema, { tenantId })
    return storageClient.getStorageUsage(req)
}

export { StorageProvider, ObjectPurpose }
export type { StorageConfig, StorageObject }
