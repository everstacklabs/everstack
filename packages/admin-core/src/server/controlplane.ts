import { createClientFor, createConnectTransport, timestampDate } from '@everstack/client'
import { ControlPlaneService } from '@everstack/proto/everstack/controlplane/v1/controlplane_service_pb'
import type {
    GatewayInstance as ProtoInstance,
    InstanceStatus as ProtoInstanceStatus,
} from '@everstack/proto/everstack/controlplane/v1/controlplane_pb'
import type { Timestamp } from '@everstack/client'

/**
 * Instance status
 */
export type InstanceStatus =
    | 'provisioning'
    | 'starting'
    | 'running'
    | 'stopping'
    | 'stopped'
    | 'degraded'
    | 'failed'
    | 'deleting'
    | 'unknown'

/**
 * Gateway instance
 */
export type CustomDomainStatus =
    | 'pending_dns'
    | 'verifying'
    | 'active'
    | 'failed'

export interface GatewayInstance {
    id: string
    workspaceId: string
    organizationId: string
    slug: string
    gatewayUrl: string
    status: InstanceStatus
    version: string
    environment: string
    schemaName: string
    errorReason?: string
    createdAt: Date
    updatedAt: Date
    stoppedAt?: Date
    customDomain?: string
    customDomainStatus?: CustomDomainStatus
    region?: string
}

/**
 * Create a control plane service client
 */
export function createControlPlaneClient(baseUrl?: string) {
    const transport = createConnectTransport({
        baseUrl: baseUrl || (typeof window !== 'undefined' ? window.location.origin : ''),
        fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
    })
    return createClientFor(ControlPlaneService)(transport)
}

export type ControlPlaneClient = ReturnType<typeof createControlPlaneClient>

// ========== Converters ==========

function safeTimestampToDate(ts: Timestamp | undefined): Date {
    if (!ts) return new Date()
    return timestampDate(ts)
}

function protoStatusToStatus(status: ProtoInstanceStatus): InstanceStatus {
    // Proto enum: 0=unspecified, 1=provisioning, 2=starting, 3=running, 4=stopping,
    // 5=stopped, 6=degraded, 7=failed, 8=deleting
    switch (status) {
        case 1: return 'provisioning'
        case 2: return 'starting'
        case 3: return 'running'
        case 4: return 'stopping'
        case 5: return 'stopped'
        case 6: return 'degraded'
        case 7: return 'failed'
        case 8: return 'deleting'
        default: return 'unknown'
    }
}

function protoToInstance(proto: ProtoInstance): GatewayInstance {
    return {
        id: proto.id,
        workspaceId: proto.workspaceId,
        organizationId: proto.organizationId,
        slug: proto.slug,
        gatewayUrl: proto.gatewayUrl,
        status: protoStatusToStatus(proto.status),
        version: proto.version,
        environment: proto.environment,
        schemaName: (proto as any).schemaName ?? '',
        errorReason: proto.errorReason || undefined,
        createdAt: safeTimestampToDate(proto.createdAt),
        updatedAt: safeTimestampToDate(proto.updatedAt),
        stoppedAt: proto.stoppedAt ? safeTimestampToDate(proto.stoppedAt) : undefined,
        customDomain: (proto as any).customDomain || undefined,
        customDomainStatus: ((proto as any).customDomainStatus || undefined) as
            | CustomDomainStatus
            | undefined,
        region: (proto as any).region || undefined,
    }
}

// ========== API Functions ==========

/**
 * Provision a new gateway instance
 */
export async function provisionInstance(
    client: ControlPlaneClient,
    workspaceId: string,
    organizationId: string,
    slug: string,
    orgSlug: string,
    environment: string = 'development',
    version?: string,
    region?: string,
): Promise<GatewayInstance> {
    const response = await client.provisionInstance({
        workspaceId,
        organizationId,
        slug,
        orgSlug,
        environment,
        version: version ?? '',
        region: region ?? '',
    })
    return protoToInstance(response.instance!)
}

// ========== Regions ==========

export interface GatewayRegion {
    id: string
    displayName: string
    flagEmoji: string
    available: boolean
}

export interface RegionCatalogue {
    regions: GatewayRegion[]
    defaultRegion: string
}

export async function listRegions(
    client: ControlPlaneClient,
): Promise<RegionCatalogue> {
    const response = await client.listRegions({})
    return {
        regions: response.regions.map((r) => ({
            id: r.id,
            displayName: r.displayName,
            flagEmoji: r.flagEmoji,
            available: r.available,
        })),
        defaultRegion: response.defaultRegion,
    }
}

/**
 * Get an instance by ID
 */
export async function getInstanceById(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<GatewayInstance> {
    const response = await client.getInstance({
        identifier: { case: 'instanceId', value: instanceId },
    })
    return protoToInstance(response.instance!)
}

/**
 * Get an instance by workspace ID
 */
export async function getInstanceByWorkspace(
    client: ControlPlaneClient,
    workspaceId: string,
): Promise<GatewayInstance | null> {
    try {
        const response = await client.getInstance({
            identifier: { case: 'workspaceId', value: workspaceId },
        })
        return protoToInstance(response.instance!)
    } catch {
        return null
    }
}

/**
 * Result from listing instances, includes plan limit info
 */
export interface ListInstancesResult {
    instances: GatewayInstance[]
    instanceCount: number
    instanceLimit: number // 0 = unlimited
}

/**
 * List all instances in an organization
 */
export async function listInstances(
    client: ControlPlaneClient,
    organizationId: string,
): Promise<GatewayInstance[]> {
    const response = await client.listInstances({ organizationId })
    return response.instances.map(protoToInstance)
}

/**
 * List all instances in an organization with limit info
 */
export async function listInstancesWithLimits(
    client: ControlPlaneClient,
    organizationId: string,
): Promise<ListInstancesResult> {
    const response = await client.listInstances({ organizationId })
    return {
        instances: response.instances.map(protoToInstance),
        instanceCount: response.instanceCount ?? 0,
        instanceLimit: response.instanceLimit ?? 0,
    }
}

/**
 * Start an instance
 */
export async function startInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<GatewayInstance> {
    const response = await client.startInstance({ instanceId })
    return protoToInstance(response.instance!)
}

/**
 * Stop an instance
 */
export async function stopInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<GatewayInstance> {
    const response = await client.stopInstance({ instanceId })
    return protoToInstance(response.instance!)
}

/**
 * Restart an instance
 */
export async function restartInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<GatewayInstance> {
    const response = await client.restartInstance({ instanceId })
    return protoToInstance(response.instance!)
}

/**
 * Delete an instance
 */
export async function deleteInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<boolean> {
    const response = await client.deleteInstance({ instanceId })
    return response.success
}

// --- Custom Domain ---

export interface CustomDomainSetup {
    domain: string
    status: string
    verificationTxtName: string
    verificationTxtValue: string
    cnameTarget: string
}

export interface CustomDomainVerification {
    status: string
    message: string
}

export async function setCustomDomain(
    client: ControlPlaneClient,
    instanceId: string,
    domain: string,
): Promise<CustomDomainSetup> {
    const response = await client.setCustomDomain({ instanceId, domain })
    return {
        domain: response.domain,
        status: response.status,
        verificationTxtName: response.verificationTxtName,
        verificationTxtValue: response.verificationTxtValue,
        cnameTarget: response.cnameTarget,
    }
}

export async function verifyCustomDomain(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<CustomDomainVerification> {
    const response = await client.verifyCustomDomain({ instanceId })
    return { status: response.status, message: response.message }
}

export async function removeCustomDomain(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<boolean> {
    const response = await client.removeCustomDomain({ instanceId })
    return response.success
}
