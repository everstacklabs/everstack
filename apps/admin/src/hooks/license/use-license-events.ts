import { useEffect, useRef, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { streamEvents, type ListEventsParams } from '@/server/events'
import type { Event } from '@everstack/proto/everstack/events/v1/events_service_pb'
import { licenseKeys } from './use-license-status'
import { gatewayLicenseKeys } from './use-license-observer'

/**
 * License-related event types that we care about
 */
export const LICENSE_EVENT_TYPES = [
    'instance.activated',
    'instance.activation_failed',
    'license.activated',
    'license.upgraded',
    'license.downgraded',
    'license.refreshed',
    'subscription.canceled',
    'subscription.resumed',
    'subscription.updated',
    'subscription.plan_changed',
] as const

export type LicenseEventType = typeof LICENSE_EVENT_TYPES[number]

export interface UseLicenseEventsOptions {
    /** Called when any license event is received */
    onEvent?: (event: Event) => void
    /** Called specifically when activation succeeds */
    onActivated?: (event: Event) => void
    /** Called specifically when activation fails */
    onActivationFailed?: (event: Event) => void
    /** Called when subscription status changes (canceled, resumed, etc.) */
    onSubscriptionStatusChanged?: (event: Event) => void
    /** Whether to auto-refresh queries when license events are received */
    autoRefreshQueries?: boolean
    /** Whether the hook is enabled */
    enabled?: boolean
}

/**
 * Hook to subscribe to license-related events via SSE streaming
 * 
 * This hook:
 * - Establishes an SSE connection to receive real-time events
 * - Filters for license-related events (activation, upgrade, etc.)
 * - Automatically refreshes license queries when relevant events arrive
 * - Provides callbacks for specific event types
 * 
 * @example
 * ```tsx
 * useLicenseEvents({
 *   enabled: isWaitingForActivation,
 *   onActivated: (event) => {
 *     toast.success('Gateway activated!')
 *     setIsWaitingForActivation(false)
 *   },
 *   onActivationFailed: (event) => {
 *     toast.error('Activation failed')
 *   }
 * })
 * ```
 */
export function useLicenseEvents({
    onEvent,
    onActivated,
    onActivationFailed,
    onSubscriptionStatusChanged,
    autoRefreshQueries = true,
    enabled = true,
}: UseLicenseEventsOptions = {}) {
    const queryClient = useQueryClient()
    const abortControllerRef = useRef<AbortController | null>(null)
    const seenEventIdsRef = useRef<Set<string>>(new Set())

    // Stable callback refs to avoid re-subscribing on callback changes
    const onEventRef = useRef(onEvent)
    const onActivatedRef = useRef(onActivated)
    const onActivationFailedRef = useRef(onActivationFailed)
    const onSubscriptionStatusChangedRef = useRef(onSubscriptionStatusChanged)

    useEffect(() => {
        onEventRef.current = onEvent
        onActivatedRef.current = onActivated
        onActivationFailedRef.current = onActivationFailed
        onSubscriptionStatusChangedRef.current = onSubscriptionStatusChanged
    }, [onEvent, onActivated, onActivationFailed, onSubscriptionStatusChanged])

    const refreshLicenseQueries = useCallback(async () => {
        if (!autoRefreshQueries) return

        await Promise.all([
            queryClient.refetchQueries({ queryKey: licenseKeys.status() }),
            queryClient.refetchQueries({ queryKey: gatewayLicenseKeys.status() }),
        ])
    }, [queryClient, autoRefreshQueries])

    const isLicenseEvent = useCallback((eventType: string): eventType is LicenseEventType => {
        return LICENSE_EVENT_TYPES.includes(eventType as LicenseEventType)
    }, [])

    useEffect(() => {
        if (!enabled) {
            // Clean up if disabled
            if (abortControllerRef.current) {
                abortControllerRef.current.abort()
                abortControllerRef.current = null
            }
            return
        }

        // Create abort controller for this subscription
        const abortController = new AbortController()
        abortControllerRef.current = abortController

        const subscribeToEvents = async () => {
            try {
                // Look back 10 seconds to catch any events we might have missed
                const from = new Date(Date.now() - 10000).toISOString()

                const params: ListEventsParams = {
                    from,
                    pageSize: 50,
                }

                for await (const event of streamEvents(params, { signal: abortController.signal })) {
                    // Skip events we've already seen
                    if (event.id && seenEventIdsRef.current.has(event.id)) {
                        continue
                    }
                    if (event.id) {
                        seenEventIdsRef.current.add(event.id)
                        // Keep the set from growing too large
                        if (seenEventIdsRef.current.size > 1000) {
                            const entries = Array.from(seenEventIdsRef.current)
                            seenEventIdsRef.current = new Set(entries.slice(-500))
                        }
                    }

                    // Check if this is a license-related event
                    if (!event.type || !isLicenseEvent(event.type)) {
                        continue
                    }

                    // Call general event handler
                    onEventRef.current?.(event)

                    // Call specific handlers based on event type
                    switch (event.type) {
                        case 'instance.activated':
                        case 'license.activated':
                        case 'license.upgraded':
                            onActivatedRef.current?.(event)
                            await refreshLicenseQueries()
                            break
                        case 'instance.activation_failed':
                            onActivationFailedRef.current?.(event)
                            break
                        case 'license.refreshed':
                        case 'license.downgraded':
                            await refreshLicenseQueries()
                            break
                        case 'subscription.canceled':
                        case 'subscription.resumed':
                        case 'subscription.updated':
                        case 'subscription.plan_changed':
                            onSubscriptionStatusChangedRef.current?.(event)
                            await refreshLicenseQueries()
                            break
                    }
                }
            } catch (error) {
                // AbortError and canceled ConnectError are expected when we clean up
                if (error instanceof Error) {
                    if (error.name === 'AbortError') {
                        return
                    }
                    // ConnectError with 'canceled' code is also expected on cleanup
                    if ('code' in error && (error as { code?: string }).code === 'canceled') {
                        return
                    }
                    // Check for canceled in message as fallback
                    if (error.message?.includes('canceled') || error.message?.includes('aborted')) {
                        return
                    }
                }
                console.error('License events subscription error:', error)
            }
        }

        subscribeToEvents()

        return () => {
            abortController.abort()
            abortControllerRef.current = null
        }
    }, [enabled, isLicenseEvent, refreshLicenseQueries])
}

/**
 * Activation details extracted from the SSE event payload
 */
export interface ActivationDetails {
    planTier?: string
    instanceId?: string
    tenantId?: string
    expiresAt?: string
    activatedAt?: string
}

/**
 * Parse activation details from an event payload
 */
function parseActivationPayload(payload: unknown): ActivationDetails {
    try {
        if (!payload) return {}

        // Payload could be a Uint8Array (protobuf bytes) or already parsed
        const payloadStr = typeof payload === 'string'
            ? payload
            : new TextDecoder().decode(payload as Uint8Array)
        const parsed = JSON.parse(payloadStr)

        return {
            planTier: parsed.plan_tier,
            instanceId: parsed.instance_id,
            tenantId: parsed.tenant_id,
            expiresAt: parsed.expires_at,
            activatedAt: parsed.activated_at,
        }
    } catch {
        return {}
    }
}

/**
 * Hook specifically for waiting for activation after payment
 * 
 * This is a convenience wrapper around useLicenseEvents that:
 * - Only activates when explicitly enabled (e.g., after payment success)
 * - Provides success/failure callbacks with full activation details
 * - Auto-refreshes queries on success
 * 
 * @example
 * ```tsx
 * useWaitForActivation({
 *   enabled: isWaitingForActivation,
 *   onSuccess: (details) => {
 *     toast.success(`Activated with ${details.planTier} plan!`)
 *     setActivatedPlan(details.planTier)
 *   }
 * })
 * ```
 */
export function useWaitForActivation({
    enabled,
    onSuccess,
    onError,
}: {
    enabled: boolean
    onSuccess?: (details: ActivationDetails) => void
    onError?: (reason?: string) => void
}) {
    useLicenseEvents({
        enabled,
        onActivated: (event) => {
            const details = parseActivationPayload(event.payload)
            onSuccess?.(details)
        },
        onActivationFailed: (event) => {
            let reason: string | undefined
            try {
                if (event.payload) {
                    const payloadStr = typeof event.payload === 'string'
                        ? event.payload
                        : new TextDecoder().decode(event.payload as Uint8Array)
                    const payload = JSON.parse(payloadStr)
                    reason = payload.error_reason
                }
            } catch {
                // Ignore parse errors
            }
            onError?.(reason)
        },
        autoRefreshQueries: true,
    })
}

/**
 * Subscription status details extracted from the SSE event payload
 */
export interface SubscriptionStatusDetails {
    organizationId?: string
    instanceId?: string
    planTier?: string
    status?: string
    cancelAtPeriodEnd?: boolean
    currentPeriodEnd?: string
    eventType?: string
    changedAt?: string
}

/**
 * Parse subscription status details from an event payload
 */
function parseSubscriptionStatusPayload(payload: unknown): SubscriptionStatusDetails {
    try {
        if (!payload) return {}

        // Payload could be a Uint8Array (protobuf bytes) or already parsed
        const payloadStr = typeof payload === 'string'
            ? payload
            : new TextDecoder().decode(payload as Uint8Array)
        const parsed = JSON.parse(payloadStr)

        return {
            organizationId: parsed.organization_id,
            instanceId: parsed.instance_id,
            planTier: parsed.plan_tier,
            status: parsed.status,
            cancelAtPeriodEnd: parsed.cancel_at_period_end,
            currentPeriodEnd: parsed.current_period_end,
            eventType: parsed.event_type,
            changedAt: parsed.changed_at,
        }
    } catch {
        return {}
    }
}

/**
 * Hook to listen for subscription status changes (canceled, resumed, etc.)
 * 
 * This is useful for the billing page to show real-time updates when
 * a user cancels or resumes their subscription from Stripe's billing portal.
 * 
 * @example
 * ```tsx
 * useSubscriptionStatusEvents({
 *   enabled: true,
 *   onCanceled: (details) => {
 *     toast.info('Your subscription will be canceled at the end of the billing period.')
 *     setPendingCancellation({ active: true, cancelAt: details.currentPeriodEnd })
 *   },
 *   onResumed: () => {
 *     toast.success('Your subscription has been resumed!')
 *     setPendingCancellation({ active: false })
 *   }
 * })
 * ```
 */
export function useSubscriptionStatusEvents({
    enabled = true,
    onCanceled,
    onResumed,
    onPlanChanged,
    onStatusChanged,
}: {
    enabled?: boolean
    onCanceled?: (details: SubscriptionStatusDetails) => void
    onResumed?: (details: SubscriptionStatusDetails) => void
    onPlanChanged?: (details: SubscriptionStatusDetails) => void
    onStatusChanged?: (details: SubscriptionStatusDetails) => void
}) {
    useLicenseEvents({
        enabled,
        onSubscriptionStatusChanged: (event) => {
            const details = parseSubscriptionStatusPayload(event.payload)

            // Call general handler
            onStatusChanged?.(details)

            // Call specific handlers
            if (event.type === 'subscription.canceled') {
                onCanceled?.(details)
            } else if (event.type === 'subscription.resumed') {
                onResumed?.(details)
            } else if (event.type === 'subscription.plan_changed') {
                onPlanChanged?.(details)
            }
        },
        autoRefreshQueries: true,
    })
}