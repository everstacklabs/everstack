import { createClientFor, createConnectTransport, timestampDate } from '@everstack/client'
import { ControlPlaneService } from '@everstack/proto/everstack/controlplane/v1/controlplane_service_pb'
import type {
    GatewayInstance as ProtoInstance,
    GetOrgActivityResponse as ProtoOrgActivity,
    InstanceLimitStatus as ProtoLimitStatus,
    InstanceStatus as ProtoInstanceStatus,
    OrgActivityDelta as ProtoActivityDelta,
    OrgActivityTotals as ProtoActivityTotals,
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
    | 'paused'
    | 'unknown'

/**
 * Why an instance is paused (docs/design/instance-over-limit.md).
 */
export type InstancePauseReason = 'over_limit_choice' | 'over_limit_auto' | 'extra_billing_unpaid'

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
    /** Set when status is 'paused'. */
    pauseReason?: InstancePauseReason
    pausedAt?: Date
    /** Last accepted API request (throttled); undefined = never. */
    lastUsedAt?: Date
    /** Owner/admin opted to pay the monthly extra-instance fee. */
    extraBilling: boolean
    /** The fee covers until here; undefined = unpaid. */
    extraBillingPaidThrough?: Date
    /**
     * Holds a slot the plan does not have: paused when the over-limit grace
     * period ends. Set by the server's own slot rule.
     */
    overLimit?: boolean
}

/**
 * An organization's plan instance limit and its open over-limit episode.
 */
export interface InstanceLimitStatus {
    /** 0 = unlimited */
    instanceLimit: number
    slotsUsed: number
    overLimit: boolean
    overLimitSince?: Date
    /** Least recently used instances are paused after this. */
    graceEndsAt?: Date
    extraBillingAvailable: boolean
    extraInstanceUsdPerMonth: number
    planTier: string
    /** Spendable credit (balance minus holds) in USD; undefined when unknown. */
    availableCreditUsd?: number
    /** Customers can buy credit (prepaid v2 slice S3 is live). */
    creditPurchaseAvailable: boolean
}

export interface InstanceLimitReport {
    status: InstanceLimitStatus
    /** Most recently used first. */
    instances: GatewayInstance[]
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
        case 9: return 'paused'
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
        pauseReason: (proto.pauseReason || undefined) as InstancePauseReason | undefined,
        pausedAt: proto.pausedAt ? safeTimestampToDate(proto.pausedAt) : undefined,
        lastUsedAt: proto.lastUsedAt ? safeTimestampToDate(proto.lastUsedAt) : undefined,
        extraBilling: !!proto.extraBilling,
        extraBillingPaidThrough: proto.extraBillingPaidThrough
            ? safeTimestampToDate(proto.extraBillingPaidThrough)
            : undefined,
        overLimit: !!proto.overLimit,
    }
}

function protoToLimitStatus(st: ProtoLimitStatus | undefined): InstanceLimitStatus {
    return {
        instanceLimit: st?.instanceLimit ?? 0,
        slotsUsed: st?.slotsUsed ?? 0,
        overLimit: !!st?.overLimit,
        overLimitSince: st?.overLimitSince ? safeTimestampToDate(st.overLimitSince) : undefined,
        graceEndsAt: st?.graceEndsAt ? safeTimestampToDate(st.graceEndsAt) : undefined,
        extraBillingAvailable: !!st?.extraBillingAvailable,
        extraInstanceUsdPerMonth: st?.extraInstanceUsdPerMonth ?? 0,
        planTier: st?.planTier ?? '',
        availableCreditUsd: st?.availableCreditKnown ? st.availableCreditUsd : undefined,
        creditPurchaseAvailable: !!st?.creditPurchaseAvailable,
    }
}

// ========== Instance limit (docs/design/instance-over-limit.md) ==========

/**
 * The org's limit, slots and open over-limit episode, with its instances
 * ordered most recently used first.
 */
export async function getInstanceLimitStatus(
    client: ControlPlaneClient,
    organizationId: string,
): Promise<InstanceLimitReport> {
    const response = await client.getInstanceLimitStatus({ organizationId })
    return {
        status: protoToLimitStatus(response.status),
        instances: response.instances.map(protoToInstance),
    }
}

export interface InstanceLimitMutationResult {
    instance: GatewayInstance
    status?: InstanceLimitStatus
}

/**
 * Pause an instance: traffic refused, everything kept. Owner/admin only.
 */
export async function pauseInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<InstanceLimitMutationResult> {
    const response = await client.pauseInstance({ instanceId })
    return {
        instance: protoToInstance(response.instance!),
        status: response.status ? protoToLimitStatus(response.status) : undefined,
    }
}

/**
 * Resume a paused instance. Fails with ResourceExhausted when the org has
 * no free slot. Owner/admin only.
 */
export async function resumeInstance(
    client: ControlPlaneClient,
    instanceId: string,
): Promise<InstanceLimitMutationResult> {
    const response = await client.resumeInstance({ instanceId })
    return {
        instance: protoToInstance(response.instance!),
        status: response.status ? protoToLimitStatus(response.status) : undefined,
    }
}

/**
 * Opt an instance into (or out of) the monthly extra-instance fee debited
 * from the prepaid wallet. Owner/admin only.
 */
export async function setInstanceExtraBilling(
    client: ControlPlaneClient,
    instanceId: string,
    enabled: boolean,
): Promise<InstanceLimitMutationResult> {
    const response = await client.setInstanceExtraBilling({
        instanceId,
        enabled,
    })
    return {
        instance: protoToInstance(response.instance!),
        status: response.status ? protoToLimitStatus(response.status) : undefined,
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

// ========== Org activity ==========

export type OrgActivityRange = '24h' | '7d' | '30d'

export interface OrgActivityTotals {
    requests: number
    tokens: number
    errors: number
    /** errors / requests, 0..1. */
    errorRate: number
    p95LatencyMs: number
    p95Available: boolean
}

/** Change versus the previous window; see the proto for units. */
export interface OrgActivityDelta {
    hasValue: boolean
    change: number
}

export interface OrgActivityBucket {
    start: Date
    /** Mapped to an instance name by the caller; never displayed. */
    instanceId: string
    requests: number
    tokens: number
    errors: number
}

/** p95 of one bucket across all of the org's instances. */
export interface OrgActivityLatencyBucket {
    start: Date
    p95LatencyMs: number
    samples: number
}

export interface OrgActivityModel {
    model: string
    requests: number
    tokens: number
}

/** Model-call activity across an organization's own instances. */
export interface OrgActivity {
    windowStart: Date
    windowEnd: Date
    bucketSeconds: number
    /** Sparse: only non-empty (bucket, instance) cells. */
    buckets: OrgActivityBucket[]
    current: OrgActivityTotals
    previous: OrgActivityTotals
    previousAvailable: boolean
    requestsDelta: OrgActivityDelta
    tokensDelta: OrgActivityDelta
    errorRateDelta: OrgActivityDelta
    p95LatencyDelta: OrgActivityDelta
    topModels: OrgActivityModel[]
    /**
     * Per-bucket p95 over the current window. Sparse: a bucket with no timed
     * calls is absent. Empty from a server that predates the field.
     */
    latencyBuckets: OrgActivityLatencyBucket[]
    /** False when the deployment has no telemetry store. */
    available: boolean
}

function activityTotals(t: ProtoActivityTotals | undefined): OrgActivityTotals {
    return {
        requests: Number(t?.requests ?? 0n),
        tokens: Number(t?.tokens ?? 0n),
        errors: Number(t?.errors ?? 0n),
        errorRate: t?.errorRate ?? 0,
        p95LatencyMs: t?.p95LatencyMs ?? 0,
        p95Available: !!t?.p95Available,
    }
}

function activityDelta(d: ProtoActivityDelta | undefined): OrgActivityDelta {
    return { hasValue: !!d?.hasValue, change: d?.change ?? 0 }
}

export function protoToOrgActivity(r: ProtoOrgActivity): OrgActivity {
    return {
        windowStart: safeTimestampToDate(r.windowStart),
        windowEnd: safeTimestampToDate(r.windowEnd),
        bucketSeconds: Number(r.bucketSeconds),
        buckets: r.buckets.map((b) => ({
            start: safeTimestampToDate(b.start),
            instanceId: b.instanceId,
            requests: Number(b.requests),
            tokens: Number(b.tokens),
            errors: Number(b.errors),
        })),
        current: activityTotals(r.current),
        previous: activityTotals(r.previous),
        previousAvailable: r.previousAvailable,
        requestsDelta: activityDelta(r.requestsDelta),
        tokensDelta: activityDelta(r.tokensDelta),
        errorRateDelta: activityDelta(r.errorRateDelta),
        p95LatencyDelta: activityDelta(r.p95LatencyDelta),
        topModels: r.topModels.map((m) => ({
            model: m.model,
            requests: Number(m.requests),
            tokens: Number(m.tokens),
        })),
        latencyBuckets: (r.latencyBuckets ?? []).map((b) => ({
            start: safeTimestampToDate(b.start),
            p95LatencyMs: b.p95LatencyMs,
            samples: Number(b.samples),
        })),
        available: r.available,
    }
}

/**
 * Requests, tokens, errors and latency across the organization's instances.
 * The server resolves the instances from the organization; nothing else is
 * sent.
 */
export async function getOrgActivity(
    client: ControlPlaneClient,
    organizationId: string,
    range: OrgActivityRange,
): Promise<OrgActivity> {
    return protoToOrgActivity(await client.getOrgActivity({ organizationId, range }))
}
