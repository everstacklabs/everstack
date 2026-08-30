import { getApiBaseUrl } from '@/lib/api-url'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const rpcBase = `${baseUrl}${connectBase}/everstack.agents.v1.AgentsService`

// ─── Types ──────────────────────────────────────────────────────────

export interface SSHKey {
    id: number
    name: string
    fingerprint: string
    keyType: string
    createdAt?: string
    lastUsedAt?: string
}

export interface SSHInfo {
    enabled: boolean
    host?: string
    port?: number
    connectionString?: string
    hostFingerprint?: string
    sandboxId?: string
    sessionId?: string
    name?: string
    status?: string
    lifecycleState?: string
    disabledReason?: string
    /** Region slug this gateway pod serves (e.g. "eu-gra-1"). */
    region?: string
    /** Public bitly-style short code; the SSH username and preview-URL subdomain. */
    shortCode?: string
}

export interface SandboxSSHToken {
    id: string
    sandboxId: string
    tenantId: string
    tokenPrefix: string
    createdBy: string
    createdAt?: string
    expiresAt?: string
    revokedAt?: string
    lastUsedAt?: string
    lastUsedIp?: string
}

export interface CreateSandboxSSHTokenResponse {
    token: SandboxSSHToken
    rawToken: string
    connectionString: string
}

// ─── Key Management ─────────────────────────────────────────────────

export async function addSSHKey(name: string, publicKey: string): Promise<{ key: SSHKey }> {
    const res = await fetch(`${rpcBase}/AddSSHKey`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ name, publicKey }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to add SSH key: ${res.status}`)
    }
    return res.json()
}

export async function listSSHKeys(): Promise<{ keys: SSHKey[] }> {
    const res = await fetch(`${rpcBase}/ListSSHKeys`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({}),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to list SSH keys: ${res.status}`)
    }
    return res.json()
}

export async function deleteSSHKey(keyId: number): Promise<{ success: boolean }> {
    const res = await fetch(`${rpcBase}/DeleteSSHKey`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ keyId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to delete SSH key: ${res.status}`)
    }
    return res.json()
}

// ─── Access Control ─────────────────────────────────────────────────

export async function grantSandboxSSHAccess(sandboxId: string, userId: string): Promise<{ success: boolean }> {
    const res = await fetch(`${rpcBase}/GrantSandboxSSHAccess`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId, userId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to grant SSH access: ${res.status}`)
    }
    return res.json()
}

export async function revokeSandboxSSHAccess(sandboxId: string, userId: string): Promise<{ success: boolean }> {
    const res = await fetch(`${rpcBase}/RevokeSandboxSSHAccess`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId, userId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to revoke SSH access: ${res.status}`)
    }
    return res.json()
}

// ─── SSH Info ───────────────────────────────────────────────────────

export async function getSandboxSSHInfo(sandboxId: string): Promise<SSHInfo> {
    const res = await fetch(`${rpcBase}/GetSandboxSSHInfo`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to get SSH info: ${res.status}`)
    }
    return res.json()
}

// ─── Temporary SSH Tokens ────────────────────────────────────────────

export async function createSandboxSSHToken(
    sandboxId: string,
    expiresInMinutes: number,
): Promise<CreateSandboxSSHTokenResponse> {
    const res = await fetch(`${rpcBase}/CreateSandboxSSHToken`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId, expiresInMinutes }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to create SSH token: ${res.status}`)
    }
    return res.json()
}

export async function listSandboxSSHTokens(sandboxId: string): Promise<{ tokens: SandboxSSHToken[] }> {
    const res = await fetch(`${rpcBase}/ListSandboxSSHTokens`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to list SSH tokens: ${res.status}`)
    }
    return res.json()
}

export async function revokeSandboxSSHToken(sandboxId: string, tokenId: string): Promise<{ success: boolean }> {
    const res = await fetch(`${rpcBase}/RevokeSandboxSSHToken`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ sandboxId, tokenId }),
    })
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.message || `Failed to revoke SSH token: ${res.status}`)
    }
    return res.json()
}
