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

export interface TrooperIdentity {
    soulMd: string
    identityMd: string
    userMd: string
    roleMd: string
}

export interface TrooperSandboxConfig {
    image: string
    cpuLimit: number
    memoryMb: number
    diskMb: number
    timeoutSeconds: number
    networkMode: string
    allowedHosts: string[]
    envVars: Record<string, string>
    sshEnabled: boolean
    gitRepoUrl: string
    gitBranch: string
}

export interface TrooperDatabaseConfig {
    sqlitePath: string
    lancedbPath: string
    redbPath: string
}

export interface TrooperWorkersConfig {
    maxConcurrentWorkers: number
    poolConfig: Record<string, unknown>
}

export interface Trooper {
    id: string
    tenantId: string
    name: string
    description: string
    status: string
    model: string
    systemPrompt: string
    tools: string[]
    agentConfig: Record<string, unknown>
    maxTurns: number
    maxToolCallsPerTurn: number
    maxSteps?: number
    identity: TrooperIdentity
    sandbox: TrooperSandboxConfig
    sandboxId: string
    databases: TrooperDatabaseConfig
    workers: TrooperWorkersConfig
    color?: string
    icon?: string
    createdAt: string
    updatedAt: string
}

export interface TrooperLink {
    id: string
    tenantId: string
    sourceTrooperId: string
    targetType: string
    targetId: string
    targetName: string
    linkType: string
    protocol: string
    status: string
    config: Record<string, unknown>
    createdAt: string
    updatedAt: string
}

export interface TrooperChannelBinding {
    id: string
    trooperId: string
    channelConfigId: string
    enabled: boolean
    createdAt: string
}

export interface CreateTrooperParams {
    tenantId?: string
    name: string
    description?: string
    model: string
    systemPrompt?: string
    tools?: string[]
    agentConfig?: Record<string, unknown>
    maxTurns?: number
    maxToolCallsPerTurn?: number
    maxSteps?: number
    identity?: Partial<TrooperIdentity>
    sandbox?: Partial<TrooperSandboxConfig>
    databases?: Partial<TrooperDatabaseConfig>
    workers?: Partial<TrooperWorkersConfig>
    color?: string
    icon?: string
    autoProvision?: boolean
}

export interface UpdateTrooperParams {
    tenantId?: string
    id: string
    name?: string
    description?: string
    model?: string
    systemPrompt?: string
    tools?: string[]
    agentConfig?: Record<string, unknown>
    maxTurns?: number
    maxToolCallsPerTurn?: number
    maxSteps?: number
    identity?: Partial<TrooperIdentity>
    sandbox?: Partial<TrooperSandboxConfig>
    databases?: Partial<TrooperDatabaseConfig>
    workers?: Partial<TrooperWorkersConfig>
    color?: string
    icon?: string
}

// ─── RPC Client ─────────────────────────────────────────────────────

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

// ─── Trooper defaults ─────────────────────────────────────────────

function withTrooperDefaults(w: Partial<Trooper> | undefined): Trooper {
    return {
        id: w?.id ?? '',
        tenantId: w?.tenantId ?? '',
        name: w?.name ?? '',
        description: w?.description ?? '',
        status: normalizeStatus(w?.status),
        model: w?.model ?? '',
        systemPrompt: w?.systemPrompt ?? '',
        tools: w?.tools ?? [],
        agentConfig: w?.agentConfig ?? {},
        maxTurns: w?.maxTurns ?? 0,
        maxToolCallsPerTurn: w?.maxToolCallsPerTurn ?? 10,
        maxSteps: w?.maxSteps,
        identity: {
            soulMd: w?.identity?.soulMd ?? '',
            identityMd: w?.identity?.identityMd ?? '',
            userMd: w?.identity?.userMd ?? '',
            roleMd: w?.identity?.roleMd ?? '',
        },
        sandbox: {
            image: w?.sandbox?.image ?? 'ubuntu:22.04',
            cpuLimit: w?.sandbox?.cpuLimit ?? 1.0,
            memoryMb: w?.sandbox?.memoryMb ?? 512,
            diskMb: w?.sandbox?.diskMb ?? 2048,
            timeoutSeconds: w?.sandbox?.timeoutSeconds ?? 0,
            networkMode: w?.sandbox?.networkMode ?? 'allow',
            allowedHosts: w?.sandbox?.allowedHosts ?? [],
            envVars: w?.sandbox?.envVars ?? {},
            sshEnabled: w?.sandbox?.sshEnabled ?? false,
            gitRepoUrl: w?.sandbox?.gitRepoUrl ?? '',
            gitBranch: w?.sandbox?.gitBranch ?? '',
        },
        sandboxId: w?.sandboxId ?? '',
        databases: {
            sqlitePath: w?.databases?.sqlitePath ?? '/workspace/data/workspace.db',
            lancedbPath: w?.databases?.lancedbPath ?? '/workspace/data/lancedb',
            redbPath: w?.databases?.redbPath ?? '/workspace/data/workspace.redb',
        },
        workers: {
            maxConcurrentWorkers: w?.workers?.maxConcurrentWorkers ?? 3,
            poolConfig: w?.workers?.poolConfig ?? {},
        },
        color: w?.color,
        icon: w?.icon,
        createdAt: w?.createdAt ?? '',
        updatedAt: w?.updatedAt ?? '',
    }
}

function normalizeStatus(s: string | undefined): string {
    if (!s) return 'created'
    // Proto enum format: TROOPER_STATUS_RUNNING → running
    const lower = s.toLowerCase()
    if (lower.startsWith('trooper_status_')) return lower.replace('trooper_status_', '')
    return lower
}

// ─── API Functions ──────────────────────────────────────────────────

export async function listTroopers(tenantId: string, opts?: { status?: string; limit?: number; offset?: number }): Promise<{ troopers: Trooper[]; total: number }> {
    const resp = await connectRPC<Record<string, unknown>, { troopers?: Partial<Trooper>[]; total?: number }>(
        'ListTroopers',
        { tenantId, ...opts }
    )
    return {
        troopers: (resp.troopers ?? []).map(withTrooperDefaults),
        total: resp.total ?? 0,
    }
}

export async function getTrooper(tenantId: string, id: string): Promise<Trooper> {
    const resp = await connectRPC<{ tenantId: string; id: string }, { trooper?: Partial<Trooper> }>(
        'GetTrooper',
        { tenantId, id }
    )
    return withTrooperDefaults(resp.trooper)
}

export async function createTrooper(params: CreateTrooperParams): Promise<Trooper> {
    const resp = await connectRPC<CreateTrooperParams, { trooper?: Partial<Trooper> }>(
        'CreateTrooper',
        params
    )
    return withTrooperDefaults(resp.trooper)
}

export async function updateTrooper(params: UpdateTrooperParams): Promise<Trooper> {
    const resp = await connectRPC<UpdateTrooperParams, { trooper?: Partial<Trooper> }>(
        'UpdateTrooper',
        params
    )
    return withTrooperDefaults(resp.trooper)
}

export async function deleteTrooper(tenantId: string, id: string): Promise<{ success: boolean; message: string }> {
    return connectRPC('DeleteTrooper', { tenantId, id })
}

export async function provisionTrooper(tenantId: string, trooperId: string): Promise<Trooper> {
    const resp = await connectRPC<{ tenantId: string; trooperId: string }, { trooper?: Partial<Trooper> }>(
        'ProvisionTrooper',
        { tenantId, trooperId }
    )
    return withTrooperDefaults(resp.trooper)
}

export async function sleepTrooper(tenantId: string, trooperId: string): Promise<{ success: boolean; message: string }> {
    return connectRPC('SleepTrooper', { tenantId, trooperId })
}

export async function wakeTrooper(tenantId: string, trooperId: string): Promise<Trooper> {
    const resp = await connectRPC<{ tenantId: string; trooperId: string }, { trooper?: Partial<Trooper> }>(
        'WakeTrooper',
        { tenantId, trooperId }
    )
    return withTrooperDefaults(resp.trooper)
}

export async function createTrooperLink(tenantId: string, params: {
    sourceTrooperId: string
    targetType: string
    targetId: string
    targetName?: string
    linkType?: string
    protocol?: string
    config?: Record<string, unknown>
}): Promise<TrooperLink> {
    const resp = await connectRPC<Record<string, unknown>, { link?: TrooperLink }>(
        'CreateTrooperLink',
        { tenantId, ...params }
    )
    return resp.link!
}

export async function listTrooperLinks(tenantId: string, trooperId: string): Promise<{ links: TrooperLink[]; total: number }> {
    const resp = await connectRPC<{ tenantId: string; trooperId: string }, { links?: TrooperLink[]; total?: number }>(
        'ListTrooperLinks',
        { tenantId, trooperId }
    )
    return { links: resp.links ?? [], total: resp.total ?? 0 }
}

export async function deleteTrooperLink(tenantId: string, linkId: string): Promise<{ success: boolean; message: string }> {
    return connectRPC('DeleteTrooperLink', { tenantId, linkId })
}

export async function bindChannel(tenantId: string, trooperId: string, channelConfigId: string): Promise<TrooperChannelBinding> {
    const resp = await connectRPC<Record<string, unknown>, { binding?: TrooperChannelBinding }>(
        'BindChannelToTrooper',
        { tenantId, trooperId, channelConfigId }
    )
    return resp.binding!
}

export async function unbindChannel(tenantId: string, trooperId: string, channelConfigId: string): Promise<{ success: boolean; message: string }> {
    return connectRPC('UnbindChannelFromTrooper', { tenantId, trooperId, channelConfigId })
}

export async function listChannelBindings(tenantId: string, trooperId: string): Promise<{ bindings: TrooperChannelBinding[]; total: number }> {
    const resp = await connectRPC<{ tenantId: string; trooperId: string }, { bindings?: TrooperChannelBinding[]; total?: number }>(
        'ListTrooperChannelBindings',
        { tenantId, trooperId }
    )
    return { bindings: resp.bindings ?? [], total: resp.total ?? 0 }
}

// ─── Trooper Session Streaming ────────────────────────────────────

const restBase = `${baseUrl}/v1`

/** POST /v1/troopers/{trooperId}/sessions/stream — returns raw Response for SSE consumption */
export function createTrooperSessionStream(
    tenantId: string,
    trooperId: string,
    userInput: string,
): Promise<Response> {
    return fetch(`${restBase}/troopers/${trooperId}/sessions/stream`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tenant_id: tenantId, user_input: userInput, enable_streaming: true }),
        credentials: 'include',
    })
}

/** POST /v1/troopers/sessions/{sessionId}/turns/stream — returns raw Response for SSE consumption */
export function steerTrooperSessionStream(
    tenantId: string,
    sessionId: string,
    userInput: string,
): Promise<Response> {
    return fetch(`${restBase}/troopers/sessions/${sessionId}/turns/stream`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tenant_id: tenantId, user_input: userInput }),
        credentials: 'include',
    })
}
