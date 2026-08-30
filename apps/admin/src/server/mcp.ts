import { getApiBaseUrl } from '@/lib/api-url'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const rpcBase = `${baseUrl}${connectBase}/everstack.mcp.v1.McpService`

// ─── ConnectRPC JSON helper ─────────────────────────────────────────

async function connectRPC<TReq, TResp>(method: string, body: TReq): Promise<TResp> {
    const res = await fetch(`${rpcBase}/${method}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        credentials: 'include',
    })
    if (!res.ok) {
        const text = await res.text()
        throw new Error(`RPC error ${res.status}: ${text}`)
    }
    return res.json()
}

// ─── Types ──────────────────────────────────────────────────────────

export type McpTransportType = 'MCP_TRANSPORT_TYPE_SSE' | 'MCP_TRANSPORT_TYPE_STREAMABLE_HTTP' | 'MCP_TRANSPORT_TYPE_STDIO'
export type McpAuthType = 'MCP_AUTH_TYPE_NONE' | 'MCP_AUTH_TYPE_API_KEY' | 'MCP_AUTH_TYPE_BEARER' | 'MCP_AUTH_TYPE_OAUTH2'
export type McpHealthStatus = 'MCP_SERVER_HEALTH_STATUS_HEALTHY' | 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY' | 'MCP_SERVER_HEALTH_STATUS_UNKNOWN'

export interface McpStdioConfig {
    command: string
    args?: string[]
    env?: Record<string, string>
    workingDir?: string
}

export interface McpToolFilter {
    type: string
    tools: string[]
}

export interface McpServer {
    id: string
    tenantId: string
    name: string
    url: string
    transportType: McpTransportType
    authType: McpAuthType
    authConfig?: Record<string, unknown>
    toolFilter?: McpToolFilter
    headers?: Record<string, string>
    stdioConfig?: McpStdioConfig
    enabled: boolean
    healthStatus: McpHealthStatus
    healthLastCheck?: string
    createdAt: string
    updatedAt: string
    toolCount: number
}

export interface McpTool {
    id: string
    serverId: string
    name: string
    description: string
    inputSchema?: Record<string, unknown>
    annotations?: Record<string, unknown>
    discoveredAt?: string
    lastSeenAt?: string
}

export interface McpToolCallResult {
    id: string
    serverId: string
    toolName: string
    tenantId: string
    status: string
    inputArgs?: Record<string, unknown>
    output: string
    error: string
    latencyMs: number
    createdAt: string
}

// ─── Zero-value defaults ────────────────────────────────────────────

function withServerDefaults(s: Partial<McpServer> | undefined): McpServer {
    return {
        id: s?.id ?? '',
        tenantId: s?.tenantId ?? '',
        name: s?.name ?? '',
        url: s?.url ?? '',
        transportType: s?.transportType ?? 'MCP_TRANSPORT_TYPE_SSE',
        authType: s?.authType ?? 'MCP_AUTH_TYPE_NONE',
        authConfig: s?.authConfig,
        toolFilter: s?.toolFilter,
        headers: s?.headers,
        stdioConfig: s?.stdioConfig,
        enabled: s?.enabled ?? true,
        healthStatus: s?.healthStatus ?? 'MCP_SERVER_HEALTH_STATUS_UNKNOWN',
        healthLastCheck: s?.healthLastCheck,
        createdAt: s?.createdAt ?? '',
        updatedAt: s?.updatedAt ?? '',
        toolCount: s?.toolCount ?? 0,
    }
}

// ─── Server CRUD ────────────────────────────────────────────────────

export interface RegisterMcpServerParams {
    tenantId?: string
    name: string
    url?: string
    transportType: McpTransportType
    authType?: McpAuthType
    authConfig?: Record<string, unknown>
    toolFilter?: McpToolFilter
    headers?: Record<string, string>
    stdioConfig?: McpStdioConfig
    enabled?: boolean
}

export async function registerMcpServer(params: RegisterMcpServerParams): Promise<{ server: McpServer; tools: McpTool[] }> {
    const resp = await connectRPC<RegisterMcpServerParams, { server?: Partial<McpServer>; tools?: McpTool[] }>(
        'RegisterMcpServer',
        params
    )
    return {
        server: withServerDefaults(resp.server),
        tools: resp.tools ?? [],
    }
}

export async function getMcpServer(id: string, tenantId: string): Promise<McpServer> {
    const resp = await connectRPC<{ id: string; tenantId: string }, { server?: Partial<McpServer> }>(
        'GetMcpServer',
        { id, tenantId }
    )
    return withServerDefaults(resp.server)
}

export async function listMcpServers(tenantId: string, enabledOnly?: boolean): Promise<McpServer[]> {
    const resp = await connectRPC<{ tenantId: string; enabledOnly?: boolean }, { servers?: Partial<McpServer>[] }>(
        'ListMcpServers',
        { tenantId, enabledOnly }
    )
    return (resp.servers ?? []).map(withServerDefaults)
}

export interface UpdateMcpServerParams {
    id: string
    tenantId: string
    name?: string
    url?: string
    transportType?: McpTransportType
    authType?: McpAuthType
    authConfig?: Record<string, unknown>
    toolFilter?: McpToolFilter
    headers?: Record<string, string>
    stdioConfig?: McpStdioConfig
    enabled?: boolean
}

export async function updateMcpServer(params: UpdateMcpServerParams): Promise<McpServer> {
    const resp = await connectRPC<UpdateMcpServerParams, { server?: Partial<McpServer> }>(
        'UpdateMcpServer',
        params
    )
    return withServerDefaults(resp.server)
}

export async function deleteMcpServer(id: string, tenantId: string): Promise<void> {
    await connectRPC<{ id: string; tenantId: string }, Record<string, never>>(
        'DeleteMcpServer',
        { id, tenantId }
    )
}

// ─── Tool Discovery ─────────────────────────────────────────────────

export async function discoverTools(serverId: string, tenantId: string): Promise<McpTool[]> {
    const resp = await connectRPC<{ serverId: string; tenantId: string }, { tools?: McpTool[] }>(
        'DiscoverTools',
        { serverId, tenantId }
    )
    return resp.tools ?? []
}

export async function listFederatedTools(
    tenantId: string,
    opts?: { serverIds?: string[]; namePrefix?: string }
): Promise<McpTool[]> {
    const resp = await connectRPC<
        { tenantId: string; serverIds?: string[]; namePrefix?: string },
        { tools?: McpTool[] }
    >('ListFederatedTools', { tenantId, ...opts })
    return resp.tools ?? []
}

// ─── Tool Execution ─────────────────────────────────────────────────

export async function callTool(
    tenantId: string,
    serverId: string,
    toolName: string,
    args?: Record<string, unknown>
): Promise<McpToolCallResult> {
    const resp = await connectRPC<
        { tenantId: string; serverId: string; toolName: string; arguments?: Record<string, unknown> },
        { result: McpToolCallResult }
    >('CallTool', { tenantId, serverId, toolName, arguments: args })
    return resp.result
}

// ─── Health ─────────────────────────────────────────────────────────

export async function getMcpServerHealth(id: string, tenantId: string): Promise<{ status: McpHealthStatus; error?: string }> {
    return connectRPC<
        { id: string; tenantId: string },
        { status: McpHealthStatus; error?: string }
    >('GetMcpServerHealth', { id, tenantId })
}

// ─── OAuth ──────────────────────────────────────────────────────────

export interface InitiateMcpOAuthParams {
    tenantId: string
    serverId: string
    callbackBaseUrl?: string
}

export interface InitiateMcpOAuthResponse {
    authorization_url: string
}

export async function initiateMcpOAuth(params: InitiateMcpOAuthParams): Promise<InitiateMcpOAuthResponse> {
    const res = await fetch(`${baseUrl}/mcp/oauth/initiate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            tenant_id: params.tenantId,
            server_id: params.serverId,
            callback_base_url: params.callbackBaseUrl ?? window.location.origin,
        }),
        credentials: 'include',
    })
    if (!res.ok) {
        const text = await res.text()
        throw new Error(`OAuth initiate failed (${res.status}): ${text}`)
    }
    return res.json()
}
