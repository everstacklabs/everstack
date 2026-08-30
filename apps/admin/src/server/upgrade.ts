import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { GatewayService } from '@everstack/proto/everstack/gateway/v1/gateway_service_pb'
import type {
    StoreUpgradeCallbackSecretRequest,
    StoreUpgradeCallbackSecretResponse,
    GetUpgradeCallbackSecretRequest,
    GetUpgradeCallbackSecretResponse,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import {
    StoreUpgradeCallbackSecretRequestSchema,
    GetUpgradeCallbackSecretRequestSchema,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// Note: Upgrade callback endpoints don't require API key
const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})

const gatewayClient = createClientFor(GatewayService)(transport)

/**
 * Store a callback secret for the upgrade flow
 * This secret is used to verify the callback from cloud after payment
 */
export async function storeUpgradeCallbackSecret(
    callbackSecret: string,
    planTier: string,
    billingPeriod: string
): Promise<StoreUpgradeCallbackSecretResponse> {
    const req: StoreUpgradeCallbackSecretRequest = create(StoreUpgradeCallbackSecretRequestSchema, {
        callbackSecret,
        planTier,
        billingPeriod,
    })
    return gatewayClient.storeUpgradeCallbackSecret(req)
}

/**
 * Get the stored callback secret (for verification during callback)
 */
export async function getUpgradeCallbackSecret(
    callbackSecret: string
): Promise<GetUpgradeCallbackSecretResponse> {
    const req: GetUpgradeCallbackSecretRequest = create(GetUpgradeCallbackSecretRequestSchema, {
        callbackSecret,
    })
    return gatewayClient.getUpgradeCallbackSecret(req)
}

// Response from upgrade session creation
interface UpgradeSessionResponse {
    session_id: string
    expires_at: string
    expires_in_seconds: number
}

/**
 * Create a secure upgrade session through the billing service
 * This validates the callback secret and returns a time-limited session ID
 */
export async function createUpgradeSession(params: {
    instanceId: string
    gatewayUrl: string
    callbackSecret: string
    planTier: string
    billingPeriod: string
    currentTier?: string
}): Promise<UpgradeSessionResponse> {
    const billingServiceUrl = env.VITE_BILLING_SERVICE_URL || 'https://billing.everstack.ai'

    const response = await fetch(`${billingServiceUrl}/api/billing/upgrade-session`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            instance_id: params.instanceId,
            gateway_url: params.gatewayUrl,
            callback_secret: params.callbackSecret,
            plan_tier: params.planTier,
            billing_period: params.billingPeriod,
            current_tier: params.currentTier || 'free',
        }),
    })

    if (!response.ok) {
        const errorData = await response.json().catch(() => ({ error: 'Failed to create upgrade session' }))
        throw new Error(errorData.error || `Failed to create upgrade session: ${response.status}`)
    }

    return response.json()
}

/**
 * Generate the cloud upgrade URL with a secure session ID
 * This is the new secure flow that uses time-limited sessions
 * Falls back to legacy direct params if billing service is unavailable
 */
export async function buildSecureCloudUpgradeUrl(params: {
    instanceId: string
    gatewayUrl: string
    callbackSecret: string
    planTier: string
    billingPeriod: string
    currentTier?: string
}): Promise<string> {
    const cloudBaseUrl = env.VITE_CLOUD_URL || 'https://app.everstack.ai'

    try {
        // Try to create a secure session first
        const session = await createUpgradeSession(params)

        // Return URL with only the session ID - no sensitive params exposed
        return `${cloudBaseUrl}/license/upgrade?session_id=${encodeURIComponent(session.session_id)}`
    } catch (err) {
        // Fallback to legacy direct params if billing service is unavailable
        // This ensures upgrade still works in local/self-hosted environments
        console.warn('Failed to create upgrade session, falling back to legacy flow:', err)
        return buildCloudUpgradeUrl(params)
    }
}

/**
 * Generate the cloud upgrade URL with all necessary parameters
 * @deprecated Use buildSecureCloudUpgradeUrl instead for better security
 */
export function buildCloudUpgradeUrl(params: {
    instanceId: string
    gatewayUrl: string
    callbackSecret: string
    planTier: string
    billingPeriod: string
    currentTier?: string
}): string {
    const cloudBaseUrl = env.VITE_CLOUD_URL || 'https://app.everstack.ai'

    const searchParams = new URLSearchParams({
        intent: 'upgrade',
        plan: params.planTier,
        billing_period: params.billingPeriod,
        instance_id: params.instanceId,
        gateway_url: params.gatewayUrl,
        callback_secret: params.callbackSecret,
        current_tier: params.currentTier || 'free',
    })

    return `${cloudBaseUrl}/license/upgrade?${searchParams.toString()}`
}


