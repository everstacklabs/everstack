/**
 * Event transformation utilities
 * Converts internal events to user-friendly, sanitized versions
 */

import type { Event } from '@everstack/proto/everstack/events/v1/events_service_pb'
import { truncateString } from '@everstack/utils/functions/common'

/**
 * User-visible event type
 * Contains only information users should see
 */
export interface UserEvent {
    id: string
    type: string
    createdAt: string
    displayData: Record<string, unknown>
    message?: string
}

/**
 * Format plan tier to human-readable string
 */
export function formatPlanTier(tier: string): string {
    const tierMap: Record<string, string> = {
        'LICENSE_TYPE_FREE': 'Free',
        'LICENSE_TYPE_BASIC': 'Basic',
        'LICENSE_TYPE_PRO': 'Pro',
        'LICENSE_TYPE_ENTERPRISE': 'Enterprise',
        'free': 'Free',
        'basic': 'Basic',
        'pro': 'Pro',
        'enterprise': 'Enterprise',
    }
    return tierMap[tier] || tier
}

/**
 * Truncate activation token to show only prefix
 */
export function sanitizeToken(token: string): string {
    if (!token) return 'N/A'
    return token.length > 10 ? token.substring(0, 10) + '...' : token
}

/**
 * Parse event payload from bytes or JSON
 */
export function parsePayload(payload: number[] | Uint8Array | null | undefined): Record<string, unknown> {
    if (!payload || payload.length === 0) {
        return {}
    }

    try {
        // Convert to bytes if needed
        const bytes = payload instanceof Uint8Array ? payload : new Uint8Array(payload)

        // Decode to string
        let decoded = new TextDecoder().decode(bytes)

        // Handle double-encoded JSON (array of numbers)
        const trimmed = decoded.trim()
        if (/^\[\s*\d+(?:\s*,\s*\d+)*\s*\]$/.test(trimmed)) {
            const arr: number[] = JSON.parse(trimmed)
            decoded = new TextDecoder().decode(new Uint8Array(arr))
        }

        // Parse JSON
        return JSON.parse(decoded) as Record<string, unknown>
    } catch (error) {
        console.error('Failed to parse event payload:', error)
        return {}
    }
}

/**
 * Transform instance.activated event for user display
 */
export function transformInstanceActivated(payload: Record<string, unknown>): UserEvent['displayData'] {
    return {
        plan: formatPlanTier(payload.plan_tier as string || ''),
        activated_at: payload.activated_at as string,
        expires_at: payload.expires_at as string || 'Never',
        token: sanitizeToken(payload.activation_token as string || ''),
    }
}

/**
 * Transform instance.activation_failed event for user display
 */
export function transformActivationFailed(payload: Record<string, unknown>): UserEvent['displayData'] {
    return {
        token: sanitizeToken(payload.activation_token as string || ''),
        error: payload.error_reason as string || 'Unknown error',
        retry_count: payload.retry_count as number || 0,
        failed_at: payload.failed_at as string,
    }
}

/**
 * Transform license.refreshed event for user display
 */
export function transformLicenseRefreshed(payload: Record<string, unknown>): UserEvent['displayData'] {
    return {
        plan: formatPlanTier(payload.plan_tier as string || ''),
        status: payload.status as string || 'unknown',
        refreshed_at: payload.refreshed_at as string,
        expires_at: payload.expires_at as string || 'Never',
    }
}

/**
 * Transform api.key.created event for user display
 */
export function transformApiKeyCreated(payload: Record<string, unknown>): UserEvent['displayData'] {
    return {
        name: payload.name as string || 'Unnamed',
        type: payload.type as string || 'Unknown',
        sensitive_id: truncateString(payload.sensitive_id as string || 'N/A'),
        user_id: payload.user_id as string || 'System',
        created_at: payload.created_at as string,
    }
}

/**
 * Transform generic event (fallback)
 */
export function transformGenericEvent(payload: Record<string, unknown>): UserEvent['displayData'] {
    // Remove sensitive fields
    const sanitized = { ...payload }
    delete sanitized.tenant_id
    delete sanitized.correlation_id
    delete sanitized.instance_id
    delete sanitized.device_fingerprint_hash
    delete sanitized.ip_address
    delete sanitized.hash // Remove raw hash from display

    // Sanitize tokens if present
    if (sanitized.activation_token && typeof sanitized.activation_token === 'string') {
        sanitized.activation_token = sanitizeToken(sanitized.activation_token)
    }

    // Sanitize sensitive_id if present (masked API keys)
    if (sanitized.sensitive_id && typeof sanitized.sensitive_id === 'string') {
        // Keep the masked version as-is since it's already safe to display
        sanitized.api_key = sanitized.sensitive_id
        delete sanitized.sensitive_id
    }

    // Format plan tier if present
    if (sanitized.plan_tier && typeof sanitized.plan_tier === 'string') {
        sanitized.plan = formatPlanTier(sanitized.plan_tier)
        delete sanitized.plan_tier
    }

    return sanitized
}

/**
 * Transform internal event to user-visible event
 * Removes sensitive data and formats for display
 */
export function transformEventForUser(event: Event): UserEvent {
    const payload = parsePayload(event.payload)

    let displayData: UserEvent['displayData']
    let message: string | undefined

    // Transform based on event type
    switch (event.type) {
        case 'instance.activated':
            displayData = transformInstanceActivated(payload)
            message = `Gateway activated with ${displayData.plan} plan`
            break

        case 'instance.activation_failed':
            displayData = transformActivationFailed(payload)
            message = `Activation failed: ${displayData.error}`
            break

        case 'license.refreshed':
            displayData = transformLicenseRefreshed(payload)
            message = `License refreshed (${displayData.status})`
            break

        case 'api.key.created':
            displayData = transformApiKeyCreated(payload)
            message = `API key "${displayData.name}" created`
            break

        default:
            displayData = transformGenericEvent(payload)
            break
    }

    return {
        id: event.id,
        type: event.type,
        createdAt: event.createdAt,
        displayData,
        message,
    }
}

/**
 * Check if event should be visible to users
 * Some internal events should never be shown
 */
export function isEventVisibleToUser(eventType: string): boolean {
    const hiddenEventTypes = [
        // Add internal-only event types here
        'internal.telemetry',
        'internal.heartbeat',
    ]

    return !hiddenEventTypes.includes(eventType)
}

/**
 * Format Unix timestamp to human-readable string
 */
export function formatTimestamp(unixTimestamp: bigint | number): string {
    try {
        const timestamp = typeof unixTimestamp === 'bigint'
            ? Number(unixTimestamp)
            : unixTimestamp

        const date = new Date(timestamp * 1000)
        return date.toISOString()
    } catch (error) {
        return 'Invalid date'
    }
}

/**
 * Get user-friendly event type label
 */
export function getEventTypeLabel(eventType: string): string {
    enum labels {
        'instance.activated' = 'Gateway Activated',
        'instance.activation.failed' = 'Activation Failed',
        'license.refreshed' = 'License Refreshed',
        'license.expired' = 'License Expired',
        'license.revoked' = 'License Revoked',
        'api.key.created' = 'API Key Created',
        'api.key.revoked' = 'API Key Revoked',
        'chat.message.processed' = 'Chat Message Processed',
        'chat.session.started' = 'Chat Session Started',
        'chat.session.completed' = 'Chat Session Completed',
        'model.selection.requested' = 'Model Selection Requested',
        'load_balancer.request.completed' = 'Load Balancer Request Completed',
    }

    return labels[eventType as keyof typeof labels] || eventType
}

