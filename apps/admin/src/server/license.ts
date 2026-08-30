import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { GatewayService } from '@everstack/proto/everstack/gateway/v1/gateway_service_pb'
import { LicenseService } from '@everstack/proto/everstack/license/v1/license_service_pb'
import type {
    ActivateGatewayInstanceRequest,
    ActivateGatewayInstanceResponse,
    GetGatewayInstanceStatusRequest,
    GetGatewayInstanceStatusResponse,
    GetTrialStatusRequest,
    GetTrialStatusResponse,
    GetLicenseMonitorStatusRequest,
    GetLicenseMonitorStatusResponse,
    RefreshLicenseMonitorRequest,
    RefreshLicenseMonitorResponse,
    GetPlansRequest,
    GetPlansResponse,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import {
    ActivateGatewayInstanceRequestSchema,
    GetGatewayInstanceStatusRequestSchema,
    GetTrialStatusRequestSchema,
    GetLicenseMonitorStatusRequestSchema,
    RefreshLicenseMonitorRequestSchema,
    GetPlansRequestSchema,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import type {
    GetSpendLimitsRequest,
    GetSpendLimitsResponse,
    SetSpendLimitRequest,
    SetSpendLimitResponse,
    DeleteSpendLimitRequest,
    DeleteSpendLimitResponse,
    SpendLimitType,
    SpendLimitPeriod,
    SpendLimitAction,
} from '@everstack/proto/everstack/license/v1/license_pb'
import {
    GetSpendLimitsRequestSchema,
    SetSpendLimitRequestSchema,
    DeleteSpendLimitRequestSchema,
} from '@everstack/proto/everstack/license/v1/license_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// Note: Activation endpoints don't require API key (they're bypassed in middleware)
const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})

const gatewayClient = createClientFor(GatewayService)(transport)

// License service client for spend limits
// In development mode: Vite proxies /everstack.license.v1.LicenseService to the license service
// In production mode: The gateway proxies these endpoints to the license service
// Use relative path so it works in both modes
const licenseTransport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const licenseClient = createClientFor(LicenseService)(licenseTransport)

/**
 * Activate the gateway instance with an activation token
 */
export async function activateGateway(
    activationToken: string,
    deviceFingerprintHash?: string,
    instanceId?: string
): Promise<ActivateGatewayInstanceResponse> {
    const req: ActivateGatewayInstanceRequest = create(ActivateGatewayInstanceRequestSchema, {
        activationToken,
        deviceFingerprintHash: deviceFingerprintHash ?? '',
        instanceId: instanceId ?? '',
    })
    return gatewayClient.activateGatewayInstance(req)
}

/**
 * Get the current gateway instance status
 */
export async function getGatewayStatus(): Promise<GetGatewayInstanceStatusResponse> {
    const req: GetGatewayInstanceStatusRequest = create(GetGatewayInstanceStatusRequestSchema, {})
    return gatewayClient.getGatewayInstanceStatus(req)
}

/**
 * Get the current trial mode status
 */
export async function getTrialStatus(): Promise<GetTrialStatusResponse> {
    const req: GetTrialStatusRequest = create(GetTrialStatusRequestSchema, {})
    return gatewayClient.getTrialStatus(req)
}

/**
 * Get detailed license monitor status including usage and feature gates
 */
export async function getLicenseStatus(): Promise<GetLicenseMonitorStatusResponse> {
    const req: GetLicenseMonitorStatusRequest = create(GetLicenseMonitorStatusRequestSchema, {})
    return gatewayClient.getLicenseMonitorStatus(req)
}

/**
 * Force a refresh of the license state from the license service
 */
export async function refreshLicenseStatus(): Promise<RefreshLicenseMonitorResponse> {
    const req: RefreshLicenseMonitorRequest = create(RefreshLicenseMonitorRequestSchema, {})
    return gatewayClient.refreshLicenseMonitor(req)
}

/**
 * Get available license plans
 */
export async function getPlans(): Promise<GetPlansResponse> {
    const req: GetPlansRequest = create(GetPlansRequestSchema, {})
    return gatewayClient.getPlans(req)
}

/**
 * Get spend limits for an organization
 */
export async function getSpendLimits(
    organizationId: string,
    instanceId?: string
): Promise<GetSpendLimitsResponse> {
    const req: GetSpendLimitsRequest = create(GetSpendLimitsRequestSchema, {
        organizationId,
        instanceId: instanceId ?? '',
    })
    return licenseClient.getSpendLimits(req)
}

/**
 * Set a spend limit for an organization
 */
export async function setSpendLimit(params: {
    organizationId: string
    instanceId?: string
    limitType: SpendLimitType
    limitAmount: number
    period: SpendLimitPeriod
    actionOnExceed: SpendLimitAction
    enabled: boolean
}): Promise<SetSpendLimitResponse> {
    const req: SetSpendLimitRequest = create(SetSpendLimitRequestSchema, {
        organizationId: params.organizationId,
        instanceId: params.instanceId ?? '',
        limitType: params.limitType,
        limitAmount: params.limitAmount,
        period: params.period,
        actionOnExceed: params.actionOnExceed,
        enabled: params.enabled,
    })
    return licenseClient.setSpendLimit(req)
}

/**
 * Delete a spend limit
 */
export async function deleteSpendLimit(id: string): Promise<DeleteSpendLimitResponse> {
    const req: DeleteSpendLimitRequest = create(DeleteSpendLimitRequestSchema, {
        id,
    })
    return licenseClient.deleteSpendLimit(req)
}
