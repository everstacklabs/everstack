import { createClientFor, createConnectTransport } from '@everstack/client'
import { BillingService } from '@everstack/proto/everstack/billing/v1/billing_service_pb'

/**
 * Create a billing service client
 */
export function createBillingClient(baseUrl?: string) {
    const transport = createConnectTransport({
        baseUrl: baseUrl || (typeof window !== 'undefined' ? window.location.origin : ''),
        // Include cookies for session auth
        fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
    })
    return createClientFor(BillingService)(transport)
}

/**
 * Billing client type for external use
 */
export type BillingClient = ReturnType<typeof createBillingClient>


