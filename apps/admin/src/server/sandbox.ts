import { getApiBaseUrl } from '@/lib/api-url'

const env = ((typeof import.meta !== 'undefined'
  ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
  : undefined) ?? {}) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''
const rpcBase = `${baseUrl}${connectBase}/everstack.agents.v1.AgentsService`

// ─── Types ──────────────────────────────────────────────────────────

export interface SandboxOverview {
  totalInstances: number
  runningInstances: number
  maxSandboxes: number
  backend: string
  maxCpu: number
  maxMemoryMb: number
  healthy: boolean
  // Aggregate resource usage across all running instances
  aggregateCpuPercent: number
  aggregateMemoryUsage: number
  aggregateMemoryLimit: number
  aggregateMemoryPercent: number
  aggregateNetworkRxBytes: number
  aggregateNetworkTxBytes: number
  aggregateBlockRead: number
  aggregateBlockWrite: number
  aggregatePids: number
  // Execution metrics
  totalExecutions: number
  avgExecutionDurationMs: number
  // Lifetime billing aggregates from the immutable sandbox_usage_records
  // ledger (the same numbers pushed to Stripe meters). Monotonically
  // increasing across the tenant's history — terminating a sandbox
  // doesn't subtract from these.
  lifetimeCostUsd: number
  lifetimeComputeSeconds: number
  activeCostUsd: number
  activeComputeSeconds: number
}

export interface SandboxInstance {
  id: string
  sessionId: string
  tenantId: string
  backend: string
  containerId: string
  image: string
  status: string
  config?: Record<string, unknown>
  createdAt: string
  /** Start of the current allocated-compute window. */
  billingStartedAt?: string
  /** Pinned backend-confirmed end while the immutable ledger close finalizes. */
  billingEndedAt?: string
  /** Server-calculated current window usage at response time. */
  currentComputeSeconds?: number
  currentComputeCostUsd?: number
  expiresAt: string
  lastUsedAt?: string
  idleRetentionSecs?: number
  destroyReason?: string
  name?: string
  // Git source info (Phase 2)
  gitRepoUrl?: string
  gitBranch?: string
  gitCommitSha?: string
  // Lifecycle state (Phase 3)
  lifecycleState?: string
  revivableUntil?: string
  stoppedAt?: string
  // SSH info (Phase 4)
  sshEnabled?: boolean
  // Persistent trooper fields
  persistent?: boolean
  agentId?: string
  keepWarm?: boolean
  /**
   * Best-known liveness of the in-guest agent at the moment the response
   * was assembled. Backed by the Firecracker backend's HealthMonitor —
   * true means the most recent /health probe returned 204; false means
   * the agent has stopped answering. Other backends (Docker, K8s,
   * Deny-mode Firecracker) report true unconditionally — they don't run
   * the probe, so we surface the strongest signal we have rather than
   * an unhelpful "unknown."
   *
   * The UI renders this as a small dot next to the sandbox label —
   * green when true, red when false. Absence (legacy responses before
   * the backend rollout) is treated as healthy.
   */
  agentHealthy?: boolean
  /**
   * Arbitrary key-value metadata. Agent orchestrators tag sandboxes by
   * run ID, agent ID, repo, PR number, etc. Setting labels via create
   * replaces the full set. Filterable via ListSandboxInstances.
   */
  labels?: Record<string, string>
  /** Days after which a stopped sandbox is auto-archived. 0=disabled. Default: 7. */
  autoArchiveAfterDays?: number
  /** Days after which a stopped/archived sandbox is auto-deleted. -1=never. Default: -1. */
  autoDeleteAfterDays?: number
  /** When the sandbox was archived (ISO 8601). */
  archivedAt?: string
  /** Snapshot this sandbox was created from, if any. */
  snapshotId?: string
  /**
   * Public bitly-style short code (e.g. "xK3p9q2A"). Used as the SSH
   * username and as the preview-URL subdomain on *.evs.run. Stable for
   * the sandbox's lifetime. Empty for legacy rows that predate the
   * backfill.
   */
  shortCode?: string
  /**
   * Public Daytona-style lifecycle label (creating/starting/started/
   * stopping/stopped/archiving/archived/destroying/destroyed/error).
   * Server-derived; prefer this over lifecycleState for display.
   */
  state?: string
  /** What the reconciler is converging toward (running/sleeping/archived/terminated). */
  desiredState?: string
  /** Set when state == "error" (e.g. "vm_not_found"). */
  errorReason?: string
  /** Auto-stop interval in minutes. 0 = disabled. */
  autoStopInterval?: number
  /** Auto-archive interval in minutes. 0 = disabled. */
  autoArchiveInterval?: number
  /** Auto-delete interval in minutes. -1 = never, 0 = ephemeral. */
  autoDeleteInterval?: number
}

export interface SandboxTemplate {
  id: string
  name: string
  slug: string
  description: string
  icon: string
  iconColor: string
  image: string
  cpuLimit: number
  memoryMb: number
  diskMb: number
  timeoutSeconds: number
  networkMode: string
  envVars?: Record<string, string>
  workDir: string
  tags?: string[]
}

export interface SandboxExecution {
  id: string
  sandboxId: string
  sessionId: string
  toolName: string
  toolCallId: string
  language: string
  command: string
  exitCode: number
  stdout: string
  stderr: string
  durationMs: number
  timedOut: boolean
  createdAt: string
}

export interface SandboxStats {
  cpuPercent: number
  memoryUsage: number
  memoryLimit: number
  memoryPercent: number
  networkRxBytes: number
  networkTxBytes: number
  blockRead: number
  blockWrite: number
  pids: number
  timestamp: string
}

// ─── Sandbox File Browsing ───────────────────────────────────────────

export interface SandboxFileInfo {
  name: string
  path: string
  size: number
  isDir: boolean
}

export async function listSandboxFiles(
  sessionId: string,
  path: string,
): Promise<SandboxFileInfo[]> {
  const res = await fetch(
    `${baseUrl}/v1/sandbox/${sessionId}/files?path=${encodeURIComponent(path)}`,
    { credentials: 'include' },
  )
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(
      data?.error?.message ?? `Failed to list files: ${res.status}`,
    )
  }
  const data = await res.json()
  return data.files ?? []
}

export async function searchSandboxFiles(
  sessionId: string,
  query: string,
  rootPath = '/repo',
): Promise<SandboxFileInfo[]> {
  const params = new URLSearchParams({ query, path: rootPath })
  const res = await fetch(
    `${baseUrl}/v1/sandbox/${sessionId}/files/search?${params}`,
    { credentials: 'include' },
  )
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(
      data?.error?.message ?? `Failed to search files: ${res.status}`,
    )
  }
  const data = await res.json()
  return data.files ?? []
}

// ─── snake_case → camelCase (for SSE endpoints that use Go JSON) ────

function snakeToCamelKey(key: string): string {
  return key.replace(/_([a-z])/g, (_, c) => c.toUpperCase())
}

export function snakeToCamel<T>(obj: unknown): T {
  if (Array.isArray(obj)) {
    return obj.map((item) => snakeToCamel(item)) as T
  }
  if (obj !== null && typeof obj === 'object') {
    const result: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(obj as Record<string, unknown>)) {
      result[snakeToCamelKey(key)] = snakeToCamel(value)
    }
    return result as T
  }
  return obj as T
}

// ─── ConnectRPC JSON helper ─────────────────────────────────────────
// ConnectRPC unary calls use POST with JSON body to procedure paths.
// Response JSON uses camelCase (protobuf JSON mapping), no conversion needed.

async function connectRPC<TReq, TResp>(
  method: string,
  body: TReq,
): Promise<TResp> {
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

// ─── Zero-value defaults (proto3 omits zero fields) ─────────────────

function withOverviewDefaults(
  o: Partial<SandboxOverview> | undefined,
): SandboxOverview {
  return {
    totalInstances: o?.totalInstances ?? 0,
    runningInstances: o?.runningInstances ?? 0,
    maxSandboxes: o?.maxSandboxes ?? 0,
    backend: o?.backend ?? '',
    maxCpu: o?.maxCpu ?? 0,
    maxMemoryMb: o?.maxMemoryMb ?? 0,
    healthy: o?.healthy ?? false,
    aggregateCpuPercent: o?.aggregateCpuPercent ?? 0,
    aggregateMemoryUsage: o?.aggregateMemoryUsage ?? 0,
    aggregateMemoryLimit: o?.aggregateMemoryLimit ?? 0,
    aggregateMemoryPercent: o?.aggregateMemoryPercent ?? 0,
    aggregateNetworkRxBytes: o?.aggregateNetworkRxBytes ?? 0,
    aggregateNetworkTxBytes: o?.aggregateNetworkTxBytes ?? 0,
    aggregateBlockRead: o?.aggregateBlockRead ?? 0,
    aggregateBlockWrite: o?.aggregateBlockWrite ?? 0,
    aggregatePids: o?.aggregatePids ?? 0,
    totalExecutions: o?.totalExecutions ?? 0,
    avgExecutionDurationMs: o?.avgExecutionDurationMs ?? 0,
    lifetimeCostUsd: o?.lifetimeCostUsd ?? 0,
    lifetimeComputeSeconds: o?.lifetimeComputeSeconds ?? 0,
    activeCostUsd: o?.activeCostUsd ?? 0,
    activeComputeSeconds: o?.activeComputeSeconds ?? 0,
  }
}

// ─── API Functions ──────────────────────────────────────────────────

export async function getSandboxOverview(
  tenantId: string,
): Promise<SandboxOverview> {
  const resp = await connectRPC<
    { tenantId?: string },
    { overview?: SandboxOverview }
  >('GetSandboxOverview', { tenantId })
  return withOverviewDefaults(resp.overview)
}

// Normalize proto enum status (e.g., "SANDBOX_STATUS_RUNNING") to lowercase (e.g., "running")
//
// Defensive against undefined: proto-JSON omits a zero-valued enum, so
// a sandbox whose status maps to SANDBOX_STATUS_UNSPECIFIED arrives with
// status === undefined. Without this guard the whole instances list
// threw "Cannot read properties of undefined (reading 'startsWith')".
function normalizeStatus(status: string | undefined): string {
  if (!status) return 'unknown'
  if (status.startsWith('SANDBOX_STATUS_')) {
    return status.replace('SANDBOX_STATUS_', '').toLowerCase()
  }
  return status
}

export async function listSandboxInstances(
  tenantId: string,
  opts?: { status?: string; limit?: number; offset?: number },
): Promise<{ instances: SandboxInstance[]; total: number }> {
  const resp = await connectRPC<
    { tenantId?: string; status?: string; limit?: number; offset?: number },
    { instances?: SandboxInstance[]; total?: number }
  >('ListSandboxInstances', { tenantId, ...opts })
  // Normalize status from proto enum format
  const instances = (resp.instances ?? []).map((inst) => ({
    ...inst,
    status: normalizeStatus(inst.status),
  }))
  return {
    instances,
    total: resp.total ?? 0,
  }
}

export async function getSandboxInstance(
  tenantId: string,
  sandboxId: string,
): Promise<SandboxInstance> {
  const resp = await connectRPC<
    { tenantId?: string; sandboxId?: string },
    { instance: SandboxInstance }
  >('GetSandboxInstance', { tenantId, sandboxId })
  return resp.instance
}

export async function destroySandbox(
  tenantId: string,
  sessionId: string,
): Promise<{ success: boolean }> {
  return connectRPC<
    { tenantId?: string; sessionId?: string },
    { success: boolean }
  >('DestroySandbox', { tenantId, sessionId })
}

export async function listSandboxExecutions(
  tenantId: string,
  sandboxId: string,
  opts?: { limit?: number; offset?: number },
): Promise<{ executions: SandboxExecution[]; total: number }> {
  const resp = await connectRPC<
    { tenantId?: string; sandboxId?: string; limit?: number; offset?: number },
    { executions?: SandboxExecution[]; total?: number }
  >('ListSandboxExecutions', { tenantId, sandboxId, ...opts })
  return {
    executions: resp.executions ?? [],
    total: resp.total ?? 0,
  }
}

export async function getSandboxStats(
  tenantId: string,
  sessionId: string,
): Promise<SandboxStats> {
  const resp = await connectRPC<
    { tenantId?: string; sessionId?: string },
    { stats: SandboxStats }
  >('GetSandboxStats', { tenantId, sessionId })
  return resp.stats
}

// ─── CreateSandbox (ConnectRPC) ──────────────────────────────────────

/**
 * Object-storage mount attached to a sandbox at creation time. Mirrors the
 * StorageMount proto message. `type` is one of "s3" | "r2" | "gcs" | "azure".
 */
export interface SandboxStorageMount {
  type: string
  bucket: string
  mountPath: string
  endpoint?: string
  subpath?: string
  readOnly?: boolean
}

export interface CreateSandboxParams {
  tenantId?: string
  sessionId?: string
  image?: string
  cpuLimit?: number
  memoryMb?: number
  diskMb?: number
  timeoutSeconds?: number
  networkMode?: string
  /** Domains allowed when networkMode is 'whitelist'. */
  allowedHosts?: string[]
  /** Idle retention in seconds. -1 = no expiration, 0 = use plan default. */
  idleRetentionSeconds?: number
  /** If set, use template config as base. Individual fields override template defaults. */
  templateId?: string
  /** Optional friendly name for the sandbox. */
  name?: string
  /** Git repo URL to import (e.g. https://github.com/owner/repo). */
  gitRepoUrl?: string
  /** Git branch to clone. */
  gitBranch?: string
  /** GitHub installation ID for private repo access. */
  gitInstallationId?: number
  /** Enable SSH access for this sandbox. */
  sshEnabled?: boolean
  /** Arbitrary key-value labels for this sandbox. */
  labels?: Record<string, string>
  /** Days before auto-archive. 0=disabled. Default: 7. */
  autoArchiveAfterDays?: number
  /** Days before auto-delete. -1=never. Default: -1. */
  autoDeleteAfterDays?: number
  /** Block all outbound egress. Always-allowed: loopback, link-local, DNS. */
  networkBlockAll?: boolean
  /** CIDR blocks to permit when networkBlockAll is true. Max 10. */
  networkAllowCidrs?: string[]
  /** Tailscale auth key to join the sandbox to a tailnet. */
  tailscaleAuthKey?: string
  /** Enable computer-use (desktop/browser automation) support. */
  computerUse?: boolean
  /** Object-storage mounts (S3/R2/GCS/Azure) to attach at creation. */
  mounts?: SandboxStorageMount[]
}

export interface CreateSandboxResponse {
  id: string
  sessionId: string
  tenantId: string
  containerId: string
  status: string
  backend: string
  image: string
  createdAt: string
  expiresAt: string
  name?: string
}

export async function createSandbox(
  params: CreateSandboxParams,
): Promise<CreateSandboxResponse> {
  return connectRPC<CreateSandboxParams, CreateSandboxResponse>(
    'CreateSandbox',
    params,
  )
}

// ─── RecreateSandbox ─────────────────────────────────────────────────

export interface RecreateSandboxParams {
  tenantId?: string
  sandboxId: string
  sessionId?: string
}

export async function recreateSandbox(
  params: RecreateSandboxParams,
): Promise<CreateSandboxResponse> {
  return connectRPC<RecreateSandboxParams, CreateSandboxResponse>(
    'RecreateSandbox',
    params,
  )
}

// ─── Sandbox Templates (read-only catalog) ───────────────────────────

export async function listSandboxTemplates(): Promise<SandboxTemplate[]> {
  const resp = await connectRPC<
    Record<string, never>,
    { templates?: SandboxTemplate[] }
  >('ListSandboxTemplates', {})
  return resp.templates ?? []
}

export async function getSandboxTemplate(
  templateId: string,
): Promise<SandboxTemplate> {
  const resp = await connectRPC<
    { templateId: string },
    { template: SandboxTemplate }
  >('GetSandboxTemplate', { templateId })
  return resp.template
}

// ─── Sandbox Events ──────────────────────────────────────────────────

export interface SandboxEvent {
  id: string
  sandboxId: string
  sessionId: string
  tenantId: string
  eventType: string
  message: string
  metadata: Record<string, unknown>
  durationMs?: number
  error?: string
  createdAt: string
}

export async function listSandboxEvents(
  tenantId: string,
  sandboxId: string,
  opts?: { eventType?: string; limit?: number; offset?: number },
): Promise<{ events: SandboxEvent[]; totalCount: number }> {
  const resp = await connectRPC<
    {
      tenantId?: string
      sandboxId?: string
      eventType?: string
      limit?: number
      offset?: number
    },
    { events?: SandboxEvent[]; totalCount?: number }
  >('ListSandboxEvents', { tenantId, sandboxId, ...opts })
  return {
    events: resp.events ?? [],
    totalCount: resp.totalCount ?? 0,
  }
}

// ─── Idle Timeout Renewal ───────────────────────────────────────────

// renewSandboxExpiration extends the sandbox's idle-retention window by
// `extraSeconds`. The gateway endpoint also resets last_used_at to now,
// so calling this is equivalent to "the user just touched the sandbox"
// plus a hard bump to the retention. Idempotent in the sense that
// repeated calls cumulatively extend; the server enforces no upper
// bound but typical UI use is a single extension at a time (e.g. +30
// minutes when the user clicks Extend timeout).
//
// Returns the {status, extra_seconds} shape the gateway emits. The UI
// doesn't use these fields beyond confirming a 2xx — invalidating the
// sandbox-instance query is what surfaces the new deadline.
export async function renewSandboxExpiration(
  sandboxId: string,
  extraSeconds: number,
): Promise<{ status: string; extra_seconds: number }> {
  const baseUrl = getApiBaseUrl()
  const res = await fetch(
    `${baseUrl}/v1/sandbox/${encodeURIComponent(sandboxId)}/renew-expiration`,
    {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ extra_seconds: extraSeconds }),
    },
  )
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`renew expiration failed (${res.status}): ${text}`)
  }
  return res.json()
}

// ─── Port Exposure ──────────────────────────────────────────────────

export interface SandboxPortMapping {
  sandboxId: string
  port: number
  protocol: string
  subdomain: string
  url: string
  status: string
  createdAt: string
}

export async function exposePort(
  tenantId: string,
  sessionId: string,
  port: number,
  protocol?: string,
): Promise<{ mapping: SandboxPortMapping }> {
  return connectRPC<
    { tenantId?: string; sessionId?: string; port?: number; protocol?: string },
    { mapping: SandboxPortMapping }
  >('ExposePort', { tenantId, sessionId, port, protocol: protocol ?? 'tcp' })
}

export async function unexposePort(
  tenantId: string,
  sessionId: string,
  port: number,
): Promise<{ success: boolean }> {
  return connectRPC<
    { tenantId?: string; sessionId?: string; port?: number },
    { success: boolean }
  >('UnexposePort', { tenantId, sessionId, port })
}

export async function listExposedPorts(
  tenantId: string,
  sessionId: string,
): Promise<{ mappings: SandboxPortMapping[] }> {
  const resp = await connectRPC<
    { tenantId?: string; sessionId?: string },
    { mappings?: SandboxPortMapping[] }
  >('ListExposedPorts', { tenantId, sessionId })
  return { mappings: resp.mappings ?? [] }
}

// ─── Crons ──────────────────────────────────────────────────────────

export interface SandboxCron {
  id: string
  tenantId: string
  sandboxId: string
  sessionId: string
  name: string
  schedule: string
  command: string
  workDir: string
  timeoutSeconds: number
  enabled: boolean
  lastRunAt?: string
  nextRunAt?: string
  runCount: number
  errorCount: number
  lastError?: string
  autoRecreate: boolean
  createdAt: string
  updatedAt: string
}

export interface CreateCronParams {
  tenantId?: string
  sessionId: string
  name: string
  schedule: string
  command: string
  workDir?: string
  timeoutSeconds?: number
  autoRecreate?: boolean
}

export async function createCron(
  params: CreateCronParams,
): Promise<{ cron: SandboxCron }> {
  return connectRPC<CreateCronParams, { cron: SandboxCron }>(
    'CreateCron',
    params,
  )
}

export async function updateCron(params: {
  tenantId?: string
  cronId: string
  name?: string
  schedule?: string
  command?: string
  workDir?: string
  timeoutSeconds?: number
  enabled?: boolean
  autoRecreate?: boolean
}): Promise<{ cron: SandboxCron }> {
  return connectRPC<typeof params, { cron: SandboxCron }>('UpdateCron', params)
}

export async function deleteCron(
  tenantId: string,
  cronId: string,
): Promise<{ success: boolean }> {
  return connectRPC<
    { tenantId?: string; cronId?: string },
    { success: boolean }
  >('DeleteCron', { tenantId, cronId })
}

export async function runCronNow(
  cronId: string,
): Promise<{ success: boolean; message: string; cronId: string }> {
  const res = await fetch(`/v1/agents/crons/${cronId}/run`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`Run cron error ${res.status}: ${text}`)
  }
  return res.json()
}

export async function listCrons(
  tenantId: string,
  sessionId: string,
): Promise<{ crons: SandboxCron[] }> {
  const resp = await connectRPC<
    { tenantId?: string; sessionId?: string },
    { crons?: SandboxCron[] }
  >('ListCrons', { tenantId, sessionId })
  return { crons: resp.crons ?? [] }
}

// ─── Webhooks ───────────────────────────────────────────────────────

export interface SandboxWebhookDef {
  id: string
  tenantId: string
  sandboxId: string
  sessionId: string
  name: string
  path: string
  command: string
  workDir: string
  timeoutSeconds: number
  enabled: boolean
  rateLimitRpm: number
  lastTriggeredAt?: string
  triggerCount: number
  errorCount: number
  lastError?: string
  autoRecreate: boolean
  createdAt: string
  updatedAt: string
}

export interface CreateWebhookParams {
  tenantId?: string
  sessionId: string
  name: string
  path: string
  command: string
  workDir?: string
  timeoutSeconds?: number
  rateLimitRpm?: number
  autoRecreate?: boolean
}

export async function createWebhook(
  params: CreateWebhookParams,
): Promise<{ webhook: SandboxWebhookDef; secret: string }> {
  return connectRPC<
    CreateWebhookParams,
    { webhook: SandboxWebhookDef; secret: string }
  >('CreateWebhook', params)
}

export async function deleteWebhook(
  tenantId: string,
  webhookId: string,
): Promise<{ success: boolean }> {
  return connectRPC<
    { tenantId?: string; webhookId?: string },
    { success: boolean }
  >('DeleteWebhook', { tenantId, webhookId })
}

export async function listWebhooks(
  tenantId: string,
  sessionId: string,
): Promise<{ webhooks: SandboxWebhookDef[] }> {
  const resp = await connectRPC<
    { tenantId?: string; sessionId?: string },
    { webhooks?: SandboxWebhookDef[] }
  >('ListWebhooks', { tenantId, sessionId })
  return { webhooks: resp.webhooks ?? [] }
}

// ─── Triggers ───────────────────────────────────────────────────────

export interface SandboxTrigger {
  id: string
  triggerType: string
  triggerId: string
  sandboxId: string
  executionId?: string
  status: string
  error?: string
  durationMs: number
  webhookMethod?: string
  webhookHeaders?: Record<string, unknown>
  webhookBody?: string
  createdAt: string
}

export async function listTriggers(
  tenantId: string,
  sandboxId: string,
  opts?: { triggerType?: string; limit?: number; offset?: number },
): Promise<{ triggers: SandboxTrigger[]; totalCount: number }> {
  const resp = await connectRPC<
    {
      tenantId?: string
      sandboxId?: string
      triggerType?: string
      limit?: number
      offset?: number
    },
    { triggers?: SandboxTrigger[]; totalCount?: number }
  >('ListTriggers', { tenantId, sandboxId, ...opts })
  return {
    triggers: resp.triggers ?? [],
    totalCount: resp.totalCount ?? 0,
  }
}

// ─── Gateway & Egress ───────────────────────────────────────────────

export interface GatewayStatus {
  listenerAddr: string
  baseDomain: string
  tlsEnabled: boolean
  activeRoutes: number
  sessionRoutingEnabled: boolean
  healthy: boolean
}

export interface EgressEvent {
  id: string
  sandboxId: string
  domain: string
  action: 'allowed' | 'blocked'
  queryType: string
  createdAt: string
}

export interface EgressPolicy {
  mode: 'allow' | 'whitelist' | 'deny'
  allowedHosts: string[]
}

export async function getGatewayStatus(
  tenantId: string,
): Promise<GatewayStatus> {
  const res = await fetch(
    `${baseUrl}/v1/gateway/status?tenant_id=${encodeURIComponent(tenantId)}`,
    {
      credentials: 'include',
    },
  )
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(
      data?.error ?? `Failed to get gateway status: ${res.status}`,
    )
  }
  const s = await res.json()
  return {
    listenerAddr: s.listenerAddr ?? s.listener_addr ?? '',
    baseDomain: s.baseDomain ?? s.base_domain ?? '',
    tlsEnabled: s.tlsEnabled ?? s.tls_enabled ?? false,
    activeRoutes: s.activeRoutes ?? s.active_routes ?? 0,
    sessionRoutingEnabled:
      s.sessionRoutingEnabled ?? s.session_routing_enabled ?? false,
    healthy: s.healthy ?? false,
  }
}

export async function listEgressEvents(
  tenantId: string,
  sandboxId: string,
  opts?: { action?: string; limit?: number; offset?: number },
): Promise<{ events: EgressEvent[]; totalCount: number }> {
  const params = new URLSearchParams({
    tenant_id: tenantId,
    sandbox_id: sandboxId,
  })
  if (opts?.action) params.set('action', opts.action)
  if (opts?.limit) params.set('limit', String(opts.limit))
  if (opts?.offset) params.set('offset', String(opts.offset))
  const res = await fetch(`${baseUrl}/v1/sandbox/egress/events?${params}`, {
    credentials: 'include',
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(
      data?.error ?? `Failed to list egress events: ${res.status}`,
    )
  }
  const data = await res.json()
  return {
    events: (data.events ?? []).map((e: Record<string, unknown>) => ({
      id: e.id ?? '',
      sandboxId: e.sandboxId ?? e.sandbox_id ?? '',
      domain: e.domain ?? '',
      action: e.action ?? 'allowed',
      queryType: e.queryType ?? e.query_type ?? 'A',
      createdAt: e.createdAt ?? e.created_at ?? '',
    })) as EgressEvent[],
    totalCount: data.totalCount ?? data.total_count ?? 0,
  }
}

export async function getEgressPolicy(
  tenantId: string,
  sandboxId: string,
): Promise<EgressPolicy> {
  const params = new URLSearchParams({
    tenant_id: tenantId,
    sandbox_id: sandboxId,
  })
  const res = await fetch(`${baseUrl}/v1/sandbox/egress/policy?${params}`, {
    credentials: 'include',
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data?.error ?? `Failed to get egress policy: ${res.status}`)
  }
  const data = await res.json()
  return {
    mode: data.mode ?? 'allow',
    allowedHosts: data.allowedHosts ?? data.allowed_hosts ?? [],
  }
}

// ─── Sandbox Lifecycle (Phase 3) ────────────────────────────────────

export async function stopSandbox(
  tenantId: string,
  sandboxId: string,
): Promise<void> {
  await connectRPC<
    { tenantId: string; sandboxId: string },
    { success: boolean }
  >('StopSandbox', { tenantId, sandboxId })
}

export async function reviveSandbox(
  tenantId: string,
  sandboxId: string,
): Promise<SandboxInstance> {
  const resp = await connectRPC<
    { tenantId: string; sandboxId: string },
    { instance: SandboxInstance }
  >('ReviveSandbox', { tenantId, sandboxId })
  return resp.instance
}

export async function terminateSandbox(
  tenantId: string,
  sandboxId: string,
): Promise<void> {
  await connectRPC<
    { tenantId: string; sandboxId: string },
    { success: boolean }
  >('TerminateSandbox', { tenantId, sandboxId })
}

// recoverSandbox re-enters convergence for a sandbox in the
// recoverable error state (VM died, or convergence exhausted its
// retries). The reconciler resumes toward the sandbox's desired state.
export async function recoverSandbox(sandboxId: string): Promise<void> {
  const baseUrl = getApiBaseUrl()
  const res = await fetch(
    `${baseUrl}/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/recover`,
    {
      method: 'POST',
      credentials: 'include',
    },
  )
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`recover failed (${res.status}): ${text}`)
  }
}

// archiveSandbox moves a stopped sandbox's workspace to object storage
// and frees the host-disk copy.
export async function archiveSandbox(sandboxId: string): Promise<void> {
  const baseUrl = getApiBaseUrl()
  const res = await fetch(
    `${baseUrl}/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/archive`,
    { method: 'POST', credentials: 'include' },
  )
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(`archive failed (${res.status}): ${text}`)
  }
}

// updateSandboxAutoIntervals changes the Daytona-style auto-lifecycle
// intervals (minutes) on a live sandbox. Only supplied fields change.
export async function updateSandboxAutoIntervals(
  tenantId: string,
  sandboxId: string,
  intervals: {
    autoStopInterval?: number
    autoArchiveInterval?: number
    autoDeleteInterval?: number
  },
): Promise<SandboxInstance> {
  const resp = await connectRPC<
    {
      tenantId: string
      sandboxId: string
      autoStopInterval?: number
      autoArchiveInterval?: number
      autoDeleteInterval?: number
    },
    { instance: SandboxInstance }
  >('UpdateSandboxAutoIntervals', { tenantId, sandboxId, ...intervals })
  return resp.instance
}

// ─── File System API ─────────────────────────────────────────────────

export interface SandboxFsEntry {
  name: string
  path?: string
  size?: number
  is_dir?: boolean
  isDir?: boolean
  mode?: string
  mod_time?: string
  modTime?: string
}

async function sandboxFsFetch(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  const baseUrl = getApiBaseUrl()
  const res = await fetch(`${baseUrl}${path}`, {
    credentials: 'include',
    ...init,
  })
  if (!res.ok) {
    let detail = ''
    try {
      const body = (await res.json()) as { error?: string }
      detail = body.error ?? ''
    } catch {
      detail = await res.text().catch(() => '')
    }
    throw new Error(detail || `request failed (${res.status})`)
  }
  return res
}

export async function listSandboxFs(
  sandboxId: string,
  path: string,
): Promise<{ path: string; files: SandboxFsEntry[] }> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/list?path=${encodeURIComponent(path)}`,
  )
  return res.json()
}

export async function uploadSandboxFile(
  sandboxId: string,
  destPath: string,
  file: Blob,
): Promise<void> {
  await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/upload?path=${encodeURIComponent(destPath)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: file,
    },
  )
}

export async function downloadSandboxFile(
  sandboxId: string,
  path: string,
): Promise<Blob> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/download?path=${encodeURIComponent(path)}`,
  )
  return res.blob()
}

export async function mkdirSandboxFs(
  sandboxId: string,
  path: string,
): Promise<void> {
  await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/mkdir`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    },
  )
}

export async function deleteSandboxFs(
  sandboxId: string,
  path: string,
  recursive: boolean,
): Promise<void> {
  await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/delete`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, recursive }),
    },
  )
}

// ─── Exec Sessions (process API) ─────────────────────────────────────

export interface ExecSessionCommandResult {
  session_id: string
  command_id: string
  exit_code?: number
  timed_out?: boolean
  output?: string
  running?: boolean
}

export async function listExecSessions(sandboxId: string): Promise<string[]> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions`,
  )
  const body = (await res.json()) as { sessions?: string[] }
  return body.sessions ?? []
}

export async function createExecSession(
  sandboxId: string,
  sessionId?: string,
): Promise<string> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(sessionId ? { session_id: sessionId } : {}),
    },
  )
  const body = (await res.json()) as { session_id: string }
  return body.session_id
}

export async function deleteExecSession(
  sandboxId: string,
  sessionId: string,
): Promise<void> {
  await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}`,
    { method: 'DELETE' },
  )
}

export async function executeExecSessionCommand(
  sandboxId: string,
  sessionId: string,
  command: string,
  opts?: { runAsync?: boolean; timeoutSeconds?: number },
): Promise<ExecSessionCommandResult> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/exec`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        command,
        run_async: opts?.runAsync ?? false,
        timeout_seconds: opts?.timeoutSeconds,
      }),
    },
  )
  return res.json()
}

export async function getExecCommandLogs(
  sandboxId: string,
  sessionId: string,
  commandId: string,
): Promise<string> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/commands/${encodeURIComponent(commandId)}/logs`,
  )
  return res.text()
}

export async function getExecCommandStatus(
  sandboxId: string,
  sessionId: string,
  commandId: string,
): Promise<{ running: boolean; exit_code?: string }> {
  const res = await sandboxFsFetch(
    `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/commands/${encodeURIComponent(commandId)}`,
  )
  return res.json()
}

// ─── Lifecycle Webhooks ──────────────────────────────────────────────────────

export interface LCWebhookEndpoint {
  id: string
  tenantId: string
  url: string
  events: string[]
  secret?: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export interface LCWebhookDelivery {
  id: number
  endpointId: string
  tenantId: string
  event: string
  payload: unknown
  statusCode?: number
  error?: string
  durationMs?: number
  createdAt: string
}

const _base = (() =>
  typeof window !== 'undefined' ? window.location.origin : '')()

export async function listLCWebhooks(): Promise<{
  endpoints: LCWebhookEndpoint[]
  total: number
}> {
  const res = await fetch(`${_base}/v1/sandbox-webhooks`, {
    credentials: 'include',
  })
  if (!res.ok) throw new Error(`Failed to list webhooks: ${res.status}`)
  return res.json()
}

export async function createLCWebhook(params: {
  url: string
  events: string[]
  secret?: string
}): Promise<LCWebhookEndpoint> {
  const res = await fetch(`${_base}/v1/sandbox-webhooks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(params),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function deleteLCWebhook(id: string): Promise<void> {
  const res = await fetch(`${_base}/v1/sandbox-webhooks/${id}`, {
    method: 'DELETE',
    credentials: 'include',
  })
  if (!res.ok && res.status !== 204) throw new Error(await res.text())
}

export async function listLCWebhookDeliveries(
  id: string,
): Promise<{ deliveries: LCWebhookDelivery[]; total: number }> {
  const res = await fetch(`${_base}/v1/sandbox-webhooks/${id}/deliveries`, {
    credentials: 'include',
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

// ─── Snapshots ──────────────────────────────────────────────────────────────

export interface Snapshot {
  id: string
  tenantId: string
  name: string
  state: 'pending' | 'building' | 'active' | 'inactive' | 'error'
  baseImage: string
  fromSandboxId?: string
  error?: string
  sizeBytes: number
  createdAt: string
  updatedAt: string
  lastUsedAt?: string
}

export interface CreateSnapshotParams {
  name: string
  image?: string
  fromSandboxId?: string
}

const BASE = (() => {
  if (typeof window !== 'undefined') {
    return window.location.origin
  }
  return ''
})()

export async function listSnapshots(): Promise<{
  snapshots: Snapshot[]
  total: number
}> {
  const res = await fetch(`${BASE}/v1/snapshots`, { credentials: 'include' })
  if (!res.ok) throw new Error(`Failed to list snapshots: ${res.status}`)
  return res.json()
}

export async function createSnapshot(
  params: CreateSnapshotParams,
): Promise<Snapshot> {
  const res = await fetch(`${BASE}/v1/snapshots`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(params),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`Failed to create snapshot: ${text}`)
  }
  return res.json()
}

export async function deleteSnapshot(id: string): Promise<void> {
  const res = await fetch(`${BASE}/v1/snapshots/${id}`, {
    method: 'DELETE',
    credentials: 'include',
  })
  if (!res.ok && res.status !== 204) {
    const text = await res.text()
    throw new Error(`Failed to delete snapshot: ${text}`)
  }
}

// ─── Volumes ──────────────────────────────────────────────────────────────────
// Tenant-scoped persistent volumes (POR-77), served via ConnectRPC. The Connect
// endpoint returns camelCase JSON and omits proto3 zero values, so numeric/
// optional fields may be absent and default to 0 / undefined.

export interface Volume {
  id: string
  tenantId: string
  name: string
  // Optional capacity quota in bytes (0/omitted = unlimited).
  sizeBytes?: number
  // Last measured bytes stored (omitted until first measurement).
  usedBytes?: number
  createdAt: string
  updatedAt: string
  // RFC 3339 timestamp of the last usage measurement (omitted if never run).
  usageMeasuredAt?: string
}

export async function listVolumes(): Promise<{
  volumes: Volume[]
  total: number
}> {
  const res = await connectRPC<
    Record<string, never>,
    { volumes?: Volume[]; total?: number }
  >('ListSandboxVolumes', {})
  return { volumes: res.volumes ?? [], total: res.total ?? 0 }
}

export async function createVolume(params: {
  name: string
  sizeGb?: number
}): Promise<Volume> {
  const res = await connectRPC<
    { name: string; sizeGb?: number },
    { volume: Volume }
  >('CreateSandboxVolume', params)
  return res.volume
}

export async function deleteVolume(id: string): Promise<void> {
  await connectRPC<{ volumeId: string }, Record<string, never>>(
    'DeleteSandboxVolume',
    { volumeId: id },
  )
}

// ─── Signed Preview URLs ──────────────────────────────────────────────────────

export async function getSandboxPreviewUrl(
  tenantId: string,
  sandboxId: string,
  port: number,
  expiresInSeconds = 3600,
): Promise<{ url: string; expiresAt: string }> {
  return connectRPC<
    {
      tenantId: string
      sandboxId: string
      port: number
      expiresInSeconds: number
    },
    { url: string; expiresAt: string }
  >('GetSandboxPreviewUrl', { tenantId, sandboxId, port, expiresInSeconds })
}
