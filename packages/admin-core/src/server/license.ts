import { createClientFor, createConnectTransport } from '@everstack/client'
import { InstanceService } from '@everstack/proto/everstack/license/v1/license_service_pb'

/**
 * Create a license instance service client
 * Used for license management operations (list instances, release license)
 */
export function createLicenseClient(baseUrl?: string) {
  const transport = createConnectTransport({
    baseUrl: baseUrl || (typeof window !== 'undefined' ? window.location.origin : ''),
    // Include cookies for session auth
    fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
  })
  return createClientFor(InstanceService)(transport)
}

/**
 * License client type for external use
 */
export type LicenseClient = ReturnType<typeof createLicenseClient>
