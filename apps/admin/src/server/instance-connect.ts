import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { AuthService } from '@everstack/proto/everstack/auth/v1/auth_service_pb'
import type { CreateInstanceConnectSessionResponse } from '@everstack/proto/everstack/auth/v1/auth_pb'
import { CreateInstanceConnectSessionRequestSchema } from '@everstack/proto/everstack/auth/v1/auth_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const cloudBaseUrl = env.VITE_CLOUD_URL || 'https://app.everstack.ai'

// Use relative URL so requests go through the vite dev proxy (or production reverse proxy)
// The proxy forwards to the auth service (localhost:8092 in dev, auth.everstack.ai in prod)
const transport = createServerTransport(undefined, {
    baseUrl: '',
    interceptors: [],
})

const authClient = createClientFor(AuthService)(transport)

export async function createInstanceConnectSession(params: {
    instanceName: string
    instanceUrl: string
    instanceId?: string
    ownerEmail?: string
    fingerprint?: string
}): Promise<CreateInstanceConnectSessionResponse> {
    const req = create(CreateInstanceConnectSessionRequestSchema, {
        instanceName: params.instanceName,
        instanceUrl: params.instanceUrl,
        instanceId: params.instanceId || undefined,
        ownerEmail: params.ownerEmail || undefined,
        fingerprint: params.fingerprint || undefined,
    })
    return authClient.createInstanceConnectSession(req)
}

export function buildInstanceConnectUrl(sessionId: string): string {
    return `${cloudBaseUrl}/instance/connect?session_id=${encodeURIComponent(sessionId)}`
}
