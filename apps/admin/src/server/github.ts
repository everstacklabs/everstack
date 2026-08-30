import { getApiBaseUrl } from '@/lib/api-url'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const rpcBase = `${baseUrl}${connectBase}/everstack.agents.v1.AgentsService`

type RpcErrorPayload = {
    code?: string
    message?: string
}

async function throwRpcError(operation: string, resp: Response): Promise<never> {
    let details = `${operation}: ${resp.status}`
    try {
        const payload = (await resp.json()) as RpcErrorPayload
        if (payload?.message) {
            details = `${operation}: ${payload.message}`
        } else if (payload?.code) {
            details = `${operation}: ${payload.code}`
        }
    } catch {
        // ignore JSON parsing failures and keep status-based fallback
    }
    throw new Error(details)
}

function tenantHeaders(tenantId: string): Record<string, string> {
    return {
        'Content-Type': 'application/json',
        ...(tenantId ? { 'x-tenant-id': tenantId } : {}),
    }
}

// ─── Types ──────────────────────────────────────────────────────────

export interface GitHubInstallation {
    id: number
    tenantId: string
    installationId: number
    accountLogin: string
    accountType: string
    appId: number
    repositorySelection: string
    status: string
    installedBy?: string
    createdAt: string
    updatedAt: string
}

export interface GitHubRepository {
    id: number
    name: string
    fullName: string
    description: string
    private: boolean
    defaultBranch: string
    language: string
    sizeKb: number
    htmlUrl: string
}

export interface GitHubBranch {
    name: string
    protected: boolean
    commitSha: string
}

// ─── RPC Functions ──────────────────────────────────────────────────

export async function listGitHubInstallations(tenantId: string): Promise<GitHubInstallation[]> {
    const resp = await fetch(`${rpcBase}/ListGitHubInstallations`, {
        method: 'POST',
        headers: tenantHeaders(tenantId),
        credentials: 'include',
        body: JSON.stringify({ tenantId }),
    })
    if (!resp.ok) return throwRpcError('listGitHubInstallations', resp)
    const data = await resp.json()
    return data.installations ?? []
}

export async function linkGitHubInstallation(
    tenantId: string,
    installationId: number
): Promise<GitHubInstallation> {
    const resp = await fetch(`${rpcBase}/LinkGitHubInstallation`, {
        method: 'POST',
        headers: tenantHeaders(tenantId),
        credentials: 'include',
        body: JSON.stringify({ tenantId, installationId }),
    })
    if (!resp.ok) return throwRpcError('linkGitHubInstallation', resp)
    const data = await resp.json()
    return data.installation
}

export async function removeGitHubInstallation(
    tenantId: string,
    installationId: number
): Promise<void> {
    const resp = await fetch(`${rpcBase}/RemoveGitHubInstallation`, {
        method: 'POST',
        headers: tenantHeaders(tenantId),
        credentials: 'include',
        body: JSON.stringify({ tenantId, installationId }),
    })
    if (!resp.ok) return throwRpcError('removeGitHubInstallation', resp)
}

export async function listGitHubRepositories(
    tenantId: string,
    installationId: number,
    opts?: { query?: string; page?: number; perPage?: number }
): Promise<{ repositories: GitHubRepository[]; total: number }> {
    const resp = await fetch(`${rpcBase}/ListGitHubRepositories`, {
        method: 'POST',
        headers: tenantHeaders(tenantId),
        credentials: 'include',
        body: JSON.stringify({
            tenantId,
            installationId,
            query: opts?.query,
            page: opts?.page,
            perPage: opts?.perPage,
        }),
    })
    if (!resp.ok) return throwRpcError('listGitHubRepositories', resp)
    const data = await resp.json()
    return { repositories: data.repositories ?? [], total: data.total ?? 0 }
}

export async function listGitHubBranches(
    tenantId: string,
    installationId: number,
    owner: string,
    repo: string,
    opts?: { page?: number; perPage?: number }
): Promise<GitHubBranch[]> {
    const resp = await fetch(`${rpcBase}/ListGitHubBranches`, {
        method: 'POST',
        headers: tenantHeaders(tenantId),
        credentials: 'include',
        body: JSON.stringify({
            tenantId,
            installationId,
            owner,
            repo,
            page: opts?.page,
            perPage: opts?.perPage,
        }),
    })
    if (!resp.ok) return throwRpcError('listGitHubBranches', resp)
    const data = await resp.json()
    return data.branches ?? []
}

// ─── Repo File Tree ─────────────────────────────────────────────────

export interface GitHubTreeFile {
    name: string
    path: string
    size: number
    isDir: boolean
}

export async function listGitHubRepoTree(
    tenantId: string,
    installationId: number,
    owner: string,
    repo: string,
    opts?: { ref?: string; path?: string; search?: string }
): Promise<GitHubTreeFile[]> {
    const params = new URLSearchParams({
        installation_id: String(installationId),
        owner,
        repo,
    })
    if (opts?.ref) params.set('ref', opts.ref)
    if (opts?.path) params.set('path', opts.path)
    if (opts?.search) params.set('search', opts.search)

    const resp = await fetch(`${baseUrl}/v1/integrations/github/tree?${params}`, {
        headers: tenantHeaders(tenantId),
        credentials: 'include',
    })
    if (!resp.ok) {
        throw new Error(`listGitHubRepoTree: ${resp.status}`)
    }
    const data = await resp.json()
    return data.files ?? []
}
