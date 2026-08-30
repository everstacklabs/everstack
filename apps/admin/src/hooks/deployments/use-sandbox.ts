import {
  useQuery,
  useMutation,
  useQueryClient,
  type UseQueryResult,
  type UseMutationResult,
} from '@tanstack/react-query'
import {
  getSandboxOverview,
  getSandboxInstance,
  listSandboxInstances,
  destroySandbox,
  createSandbox,
  recreateSandbox,
  listSandboxExecutions,
  listSandboxTemplates,
  listSandboxEvents,
  exposePort,
  unexposePort,
  listExposedPorts,
  createCron,
  updateCron,
  deleteCron,
  runCronNow,
  listCrons,
  createWebhook,
  deleteWebhook,
  listWebhooks,
  listTriggers,
  stopSandbox,
  reviveSandbox,
  terminateSandbox,
  recoverSandbox,
  archiveSandbox,
  updateSandboxAutoIntervals,
  listSandboxFiles,
  searchSandboxFiles,
  getGatewayStatus,
  listEgressEvents,
  getEgressPolicy,
  snakeToCamel,
  type SandboxOverview,
  type SandboxInstance,
  type SandboxExecution,
  type SandboxStats,
  type SandboxTemplate,
  type SandboxEvent,
  type SandboxPortMapping,
  type SandboxCron,
  type SandboxWebhookDef,
  type SandboxTrigger,
  type SandboxFileInfo,
  type GatewayStatus,
  type EgressEvent,
  type EgressPolicy,
  type CreateSandboxParams,
  type CreateSandboxResponse,
  type RecreateSandboxParams,
  type CreateCronParams,
  type CreateWebhookParams,
  listSnapshots,
  createSnapshot,
  deleteSnapshot,
  type Snapshot,
  type CreateSnapshotParams,
  listVolumes,
  createVolume,
  deleteVolume,
  type Volume,
} from '@/server/sandbox'
import { useSession } from '@/hooks/auth/use-auth'
import { useState, useCallback, useRef, useEffect } from 'react'
import { getApiBaseUrl } from '@/lib/api-url'

const SANDBOX_OVERVIEW_KEY = ['sandbox-overview']
// Exported so the topbar Refresh action can invalidate the same cache
// the instances list reads.
export const SANDBOX_INSTANCES_KEY = ['sandbox-instances']
const SANDBOX_EXECUTIONS_KEY = ['sandbox-executions']
const SANDBOX_TEMPLATES_KEY = ['sandbox-templates']
const SANDBOX_TRIGGERS_KEY = ['sandbox-triggers']
const SANDBOX_SSH_INFO_KEY = ['sandbox-ssh-info']

function useOrganizationId(): string {
  const { data: session } = useSession()
  return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Query Hooks ────────────────────────────────────────────────────

export function useSandboxOverview(): UseQueryResult<SandboxOverview, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...SANDBOX_OVERVIEW_KEY, orgId],
    queryFn: () => getSandboxOverview(orgId),
    refetchInterval: 5000,
  })
}

// Instance is "in flight" — server-side work still pending. While any row is
// in one of these states, poll faster so the UI reflects progress quickly.
const PENDING_LIFECYCLE_STATES = new Set([
  'pending',
  'provisioning',
  'creating',
  'restoring',
  'stopping',
  'archiving',
  'deleting',
])

export function useSandboxInstances(opts?: {
  status?: string
  limit?: number
  offset?: number
}): UseQueryResult<{ instances: SandboxInstance[]; total: number }, Error> {
  const orgId = useOrganizationId()
  const queryClient = useQueryClient()
  const status = opts?.status
  const limit = opts?.limit
  const offset = opts?.offset

  // SSE subscription for tenant-wide lifecycle events. The gateway's
  // /v1/sandboxes/events endpoint is wired only when the reconciler is
  // active (EVS_SANDBOX_RECONCILER_ENABLED). We probe it: if it's
  // available, polling drops to a 30s safety-net; if it returns 503
  // we fall back to the legacy 1.5–5s interval. See
  // docs/design/sandbox-reconciler.md.
  const sseAvailable = useSandboxLifecycleEventsStream(orgId, queryClient, [
    ...SANDBOX_INSTANCES_KEY,
    orgId,
    status ?? null,
    limit ?? null,
    offset ?? null,
  ])

  return useQuery({
    queryKey: [
      ...SANDBOX_INSTANCES_KEY,
      orgId,
      status ?? null,
      limit ?? null,
      offset ?? null,
    ],
    queryFn: () => listSandboxInstances(orgId, opts),
    refetchInterval: (query) => {
      // SSE active → reduce polling to a 30s safety-net so a missed
      // event (slow consumer drop, brief disconnect) reconverges.
      if (sseAvailable) {
        return 30000
      }
      // Legacy fallback when SSE isn't available.
      const data = query.state.data as
        | { instances: SandboxInstance[] }
        | undefined
      const hasPending = data?.instances?.some((i) =>
        PENDING_LIFECYCLE_STATES.has((i.lifecycleState ?? '').trim()),
      )
      return hasPending ? 1500 : 5000
    },
  })
}

// useSandboxInstance fetches a single sandbox by ID. Used by the create
// page when recreating from an existing sandbox (`?from=<id>`), so the
// form can pre-fill resource and network fields from the source.
export function useSandboxInstance(
  sandboxId: string | undefined,
): UseQueryResult<SandboxInstance, Error> {
  const orgId = useOrganizationId()
  return useQuery({
    queryKey: ['sandbox-instance', orgId, sandboxId ?? null],
    queryFn: () => getSandboxInstance(orgId, sandboxId!),
    enabled: !!sandboxId,
    staleTime: 30_000,
  })
}

// useSandboxLifecycleEventsStream subscribes to /v1/sandboxes/events
// (Server-Sent Events) for the active tenant and applies incoming
// lifecycle changes to the cached useSandboxInstances data. Returns
// true when the stream is connected and confirmed live (received the
// 'ready' event); false until then or when the endpoint isn't available.
//
// On a 503 the EventSource silently retries — for the reconciler-off
// case we want to NOT retry forever, so we close after the first error
// and let the caller fall back to polling.
function useSandboxLifecycleEventsStream(
  orgId: string,
  queryClient: ReturnType<typeof useQueryClient>,
  cacheKey: readonly unknown[],
): boolean {
  const [available, setAvailable] = useState(false)
  const cacheKeyRef = useRef(cacheKey)
  cacheKeyRef.current = cacheKey

  useEffect(() => {
    if (!orgId) return

    const baseUrl = getApiBaseUrl()
    const url = `${baseUrl}/v1/sandboxes/events`
    let closedByError = false
    const es = new EventSource(url, { withCredentials: true })

    es.addEventListener('ready', () => {
      setAvailable(true)
    })

    es.addEventListener('lifecycle', (raw) => {
      const e = raw as MessageEvent<string>
      try {
        const evt = JSON.parse(e.data) as {
          id: string
          tenant_id: string
          session_id: string
          lifecycle_state: string
          status: string
          updated_at: number
        }
        applyLifecycleEventToCache(queryClient, cacheKeyRef.current, evt)
      } catch {
        // Malformed payload — ignore; the safety-net poll catches drift.
      }
    })

    es.onerror = () => {
      // Single-shot probe: if the stream errors before becoming ready,
      // the endpoint is probably unconfigured (reconciler off → 503).
      // Close and stop retrying so polling takes over.
      if (!closedByError) {
        closedByError = true
        es.close()
        setAvailable(false)
      }
    }

    return () => {
      es.close()
      setAvailable(false)
    }
  }, [orgId, queryClient])

  return available
}

// applyLifecycleEventToCache patches the cached query data with a
// single lifecycle change. If the sandbox is unknown to the cache it's
// inserted at the top; if known, its lifecycle_state and status are
// updated in place. Terminal-state rows stay in the list — the user
// can dismiss them manually or filter via the FE.
function applyLifecycleEventToCache(
  queryClient: ReturnType<typeof useQueryClient>,
  cacheKey: readonly unknown[],
  evt: {
    id: string
    session_id: string
    lifecycle_state: string
    status: string
  },
) {
  queryClient.setQueryData(
    [...cacheKey],
    (prev: { instances: SandboxInstance[]; total: number } | undefined) => {
      if (!prev) return prev
      const idx = prev.instances.findIndex((i) => i.id === evt.id)
      if (idx === -1) {
        // Not in the cached list. Trigger a one-shot refetch to pull
        // the new row (with full config / image / etc) from the server.
        // Avoids guessing fields the SSE event doesn't carry.
        queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
        return prev
      }
      const next = [...prev.instances]
      next[idx] = {
        ...next[idx],
        lifecycleState: evt.lifecycle_state,
        status: evt.status,
      }
      return { ...prev, instances: next }
    },
  )
}

export function useSandboxExecutions(
  sandboxId: string,
  opts?: { limit?: number; offset?: number },
): UseQueryResult<{ executions: SandboxExecution[]; total: number }, Error> {
  const orgId = useOrganizationId()
  const execLimit = opts?.limit
  const execOffset = opts?.offset

  return useQuery({
    queryKey: [
      ...SANDBOX_EXECUTIONS_KEY,
      orgId,
      sandboxId,
      execLimit ?? null,
      execOffset ?? null,
    ],
    queryFn: () => listSandboxExecutions(orgId, sandboxId, opts),
    enabled: !!orgId && !!sandboxId,
  })
}

// ─── Mutation Hooks ─────────────────────────────────────────────────

export function useDestroySandbox(): UseMutationResult<
  { success: boolean },
  Error,
  string
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (sessionId: string) => destroySandbox(orgId, sessionId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

export function useCreateSandbox(): UseMutationResult<
  CreateSandboxResponse,
  Error,
  CreateSandboxParams
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  // No optimistic placeholder. The CreateSandbox RPC is currently sync
  // server-side (see internal/api/grpc/agents/v1/agents_sandbox_connect.go);
  // injecting a fake "pending" row before the mutation settled was useful when
  // the RPC was async, but with sync it sits there forever if backend.Create
  // hangs (firecracker-agent gRPC, etc.) and gets misread as "the create is
  // stuck". Trust the spinner on the create button and let invalidate-on-
  // settled bring in the real server row.
  return useMutation({
    mutationFn: (params: CreateSandboxParams) =>
      createSandbox({ ...params, tenantId: params.tenantId ?? orgId }),
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

export function useRecreateSandbox(): UseMutationResult<
  CreateSandboxResponse,
  Error,
  RecreateSandboxParams
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (params: RecreateSandboxParams) =>
      recreateSandbox({ ...params, tenantId: params.tenantId ?? orgId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

// ─── Lifecycle Mutation Hooks ───────────────────────────────────────

export function useStopSandbox(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (sandboxId: string) => stopSandbox(orgId, sandboxId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

export function useReviveSandbox(): UseMutationResult<
  SandboxInstance,
  Error,
  string
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (sandboxId: string) => reviveSandbox(orgId, sandboxId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

export function useTerminateSandbox(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (sandboxId: string) => terminateSandbox(orgId, sandboxId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

// useRecoverSandbox re-enters convergence for a sandbox in the
// recoverable error state. The reconciler resumes toward the
// sandbox's desired state (typically recreating a dead VM and
// restoring its workspace snapshot).
export function useRecoverSandbox(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (sandboxId: string) => recoverSandbox(sandboxId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
    },
  })
}

// useArchiveSandbox moves a stopped sandbox's workspace to object
// storage (Daytona archive).
export function useArchiveSandbox(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (sandboxId: string) => archiveSandbox(sandboxId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_OVERVIEW_KEY })
    },
  })
}

// useUpdateSandboxAutoIntervals changes the auto-stop/archive/delete
// intervals (minutes) on a live sandbox.
export function useUpdateSandboxAutoIntervals(): UseMutationResult<
  SandboxInstance,
  Error,
  { sandboxId: string; autoStopInterval?: number; autoArchiveInterval?: number; autoDeleteInterval?: number }
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: ({ sandboxId, ...intervals }) =>
      updateSandboxAutoIntervals(orgId, sandboxId, intervals),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_INSTANCES_KEY })
    },
  })
}

// ─── Template Hooks ─────────────────────────────────────────────────

export function useSandboxTemplates(): UseQueryResult<
  SandboxTemplate[],
  Error
> {
  return useQuery({
    queryKey: SANDBOX_TEMPLATES_KEY,
    queryFn: () => listSandboxTemplates(),
    // Templates are code-defined on the server — rarely change, cache aggressively
    staleTime: 5 * 60 * 1000,
  })
}

// ─── Events Hooks ───────────────────────────────────────────────────

const SANDBOX_EVENTS_KEY = ['sandbox-events']
const SANDBOX_PORTS_KEY = ['sandbox-ports']
const SANDBOX_CRONS_KEY = ['sandbox-crons']
const SANDBOX_WEBHOOKS_KEY = ['sandbox-webhooks']

export function useSandboxEvents(
  sandboxId: string | undefined,
  opts?: { eventType?: string; limit?: number; offset?: number },
): UseQueryResult<{ events: SandboxEvent[]; totalCount: number }, Error> {
  const orgId = useOrganizationId()
  const eventType = opts?.eventType
  const evtLimit = opts?.limit
  const evtOffset = opts?.offset

  return useQuery({
    queryKey: [
      ...SANDBOX_EVENTS_KEY,
      orgId,
      sandboxId,
      eventType ?? null,
      evtLimit ?? null,
      evtOffset ?? null,
    ],
    queryFn: () => listSandboxEvents(orgId, sandboxId!, opts),
    enabled: !!orgId && !!sandboxId,
    refetchInterval: 5000,
  })
}

// ─── Port Exposure Hooks ────────────────────────────────────────────

export function useSandboxPorts(
  sessionId: string | undefined,
): UseQueryResult<{ mappings: SandboxPortMapping[] }, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...SANDBOX_PORTS_KEY, orgId, sessionId],
    queryFn: () => listExposedPorts(orgId, sessionId!),
    enabled: !!orgId && !!sessionId,
    refetchInterval: 5000,
  })
}

export function useExposePort(): UseMutationResult<
  { mapping: SandboxPortMapping },
  Error,
  { sessionId: string; port: number; protocol?: string }
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: ({
      sessionId,
      port,
      protocol,
    }: {
      sessionId: string
      port: number
      protocol?: string
    }) => exposePort(orgId, sessionId, port, protocol),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_PORTS_KEY })
    },
  })
}

export function useUnexposePort(): UseMutationResult<
  { success: boolean },
  Error,
  { sessionId: string; port: number }
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: ({ sessionId, port }: { sessionId: string; port: number }) =>
      unexposePort(orgId, sessionId, port),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_PORTS_KEY })
    },
  })
}

// ─── Cron Hooks ─────────────────────────────────────────────────────

export function useSandboxCrons(
  sessionId: string | undefined,
): UseQueryResult<{ crons: SandboxCron[] }, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...SANDBOX_CRONS_KEY, orgId, sessionId],
    queryFn: () => listCrons(orgId, sessionId!),
    enabled: !!orgId && !!sessionId,
    refetchInterval: 5000,
  })
}

export function useCreateCron(): UseMutationResult<
  { cron: SandboxCron },
  Error,
  CreateCronParams
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (params: CreateCronParams) =>
      createCron({ ...params, tenantId: params.tenantId ?? orgId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_CRONS_KEY })
    },
  })
}

export function useUpdateCron(): UseMutationResult<
  { cron: SandboxCron },
  Error,
  {
    cronId: string
    enabled?: boolean
    name?: string
    schedule?: string
    command?: string
    workDir?: string
    timeoutSeconds?: number
    autoRecreate?: boolean
  }
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (params: {
      cronId: string
      enabled?: boolean
      name?: string
      schedule?: string
      command?: string
      workDir?: string
      timeoutSeconds?: number
      autoRecreate?: boolean
    }) => updateCron({ ...params, tenantId: orgId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_CRONS_KEY })
    },
  })
}

export function useDeleteCron(): UseMutationResult<
  { success: boolean },
  Error,
  string
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (cronId: string) => deleteCron(orgId, cronId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_CRONS_KEY })
    },
  })
}

export function useRunCronNow(): UseMutationResult<
  { success: boolean; message: string; cronId: string },
  Error,
  string
> {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (cronId: string) => runCronNow(cronId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_CRONS_KEY })
      queryClient.invalidateQueries({ queryKey: SANDBOX_TRIGGERS_KEY })
    },
  })
}

// ─── Webhook Hooks ──────────────────────────────────────────────────

export function useSandboxWebhooks(
  sessionId: string | undefined,
): UseQueryResult<{ webhooks: SandboxWebhookDef[] }, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...SANDBOX_WEBHOOKS_KEY, orgId, sessionId],
    queryFn: () => listWebhooks(orgId, sessionId!),
    enabled: !!orgId && !!sessionId,
    refetchInterval: 5000,
  })
}

export function useSandboxTriggers(
  sandboxId: string | undefined,
  opts?: { triggerType?: string; limit?: number; offset?: number },
): UseQueryResult<{ triggers: SandboxTrigger[]; totalCount: number }, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [
      ...SANDBOX_TRIGGERS_KEY,
      orgId,
      sandboxId,
      opts?.triggerType,
      opts?.limit,
      opts?.offset,
    ],
    queryFn: () =>
      listTriggers(orgId, sandboxId!, {
        triggerType: opts?.triggerType,
        limit: opts?.limit,
        offset: opts?.offset,
      }),
    enabled: !!orgId && !!sandboxId,
    refetchInterval: 5000,
  })
}

export function useCreateWebhook(): UseMutationResult<
  { webhook: SandboxWebhookDef; secret: string },
  Error,
  CreateWebhookParams
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (params: CreateWebhookParams) =>
      createWebhook({ ...params, tenantId: params.tenantId ?? orgId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_WEBHOOKS_KEY })
    },
  })
}

export function useDeleteWebhook(): UseMutationResult<
  { success: boolean },
  Error,
  string
> {
  const queryClient = useQueryClient()
  const orgId = useOrganizationId()

  return useMutation({
    mutationFn: (webhookId: string) => deleteWebhook(orgId, webhookId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_WEBHOOKS_KEY })
    },
  })
}

// ─── Gateway & Egress Hooks ─────────────────────────────────────────

const GATEWAY_STATUS_KEY = ['gateway-status']
const EGRESS_EVENTS_KEY = ['egress-events']
const EGRESS_POLICY_KEY = ['egress-policy']

export function useGatewayStatus(): UseQueryResult<GatewayStatus, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...GATEWAY_STATUS_KEY, orgId],
    queryFn: () => getGatewayStatus(orgId),
    refetchInterval: 5000,
  })
}

export function useEgressEvents(
  sandboxId: string | undefined,
  opts?: { action?: string; limit?: number; offset?: number },
): UseQueryResult<{ events: EgressEvent[]; totalCount: number }, Error> {
  const orgId = useOrganizationId()
  const egressAction = opts?.action
  const egressLimit = opts?.limit
  const egressOffset = opts?.offset

  return useQuery({
    queryKey: [
      ...EGRESS_EVENTS_KEY,
      orgId,
      sandboxId,
      egressAction ?? null,
      egressLimit ?? null,
      egressOffset ?? null,
    ],
    queryFn: () => listEgressEvents(orgId, sandboxId!, opts),
    enabled: !!orgId && !!sandboxId,
    refetchInterval: 10000,
  })
}

export function useEgressPolicy(
  sandboxId: string | undefined,
): UseQueryResult<EgressPolicy, Error> {
  const orgId = useOrganizationId()

  return useQuery({
    queryKey: [...EGRESS_POLICY_KEY, orgId, sandboxId],
    queryFn: () => getEgressPolicy(orgId, sandboxId!),
    enabled: !!orgId && !!sandboxId,
    refetchInterval: 30000,
  })
}

// ─── Events SSE Hook ────────────────────────────────────────────────

export function useSandboxEventsStream(sandboxId: string | undefined) {
  const [events, setEvents] = useState<SandboxEvent[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const start = useCallback(() => {
    if (!sandboxId || isStreaming) return

    setIsStreaming(true)
    const baseUrl = getApiBaseUrl()
    const url = `${baseUrl}/v1/sandbox/${sandboxId}/events/stream`
    const controller = new AbortController()
    abortRef.current = controller
    ;(async () => {
      try {
        const response = await fetch(url, { signal: controller.signal })
        if (!response.ok || !response.body) {
          setIsStreaming(false)
          return
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() ?? ''

          let currentEvent = ''
          for (const line of lines) {
            if (line.startsWith('event: ')) {
              currentEvent = line.slice(7).trim()
              continue
            }
            if (!line.startsWith('data: ')) continue
            if (currentEvent === 'error') {
              currentEvent = ''
              continue
            }
            currentEvent = ''
            const jsonStr = line.slice(6).trim()
            if (!jsonStr) continue

            try {
              const parsed = snakeToCamel<SandboxEvent>(JSON.parse(jsonStr))
              if (parsed.eventType) {
                setEvents((prev) => [...prev, parsed])
              }
            } catch {
              // skip malformed
            }
          }
        }
      } catch (err) {
        if ((err as Error).name !== 'AbortError') {
          console.error('Sandbox events stream error:', err)
        }
      } finally {
        setIsStreaming(false)
        abortRef.current = null
      }
    })()
  }, [sandboxId, isStreaming])

  const stop = useCallback(() => {
    abortRef.current?.abort()
  }, [])

  useEffect(() => {
    return () => {
      abortRef.current?.abort()
    }
  }, [])

  return { events, isStreaming, start, stop }
}

// ─── File Browsing ──────────────────────────────────────────────────

const SANDBOX_FILES_KEY = ['sandbox-files']

export function useSandboxFiles(
  sessionId: string | undefined,
  path: string,
  enabled = true,
): UseQueryResult<SandboxFileInfo[], Error> {
  return useQuery({
    queryKey: [...SANDBOX_FILES_KEY, sessionId, path],
    queryFn: () => listSandboxFiles(sessionId!, path),
    enabled: !!sessionId && enabled,
    staleTime: 30_000,
  })
}

/**
 * Prefetch the sandbox root directory so file browsing is instant when the
 * user opens the @ mention dropdown. Tries /repo first, then /workspace.
 */
export function usePrefetchSandboxFiles(sessionId: string | undefined) {
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!sessionId) return

    const prefetch = async () => {
      // Try /repo first (matches the dropdown's default root)
      try {
        await queryClient.fetchQuery({
          queryKey: [...SANDBOX_FILES_KEY, sessionId, '/repo'],
          queryFn: () => listSandboxFiles(sessionId, '/repo'),
          staleTime: 30_000,
        })
      } catch {
        // /repo failed — prefetch /workspace as fallback
        queryClient.prefetchQuery({
          queryKey: [...SANDBOX_FILES_KEY, sessionId, '/workspace'],
          queryFn: () => listSandboxFiles(sessionId, '/workspace'),
          staleTime: 30_000,
        })
      }
    }

    prefetch()
  }, [sessionId, queryClient])
}

const SANDBOX_FILE_SEARCH_KEY = ['sandbox-file-search']

export function useSandboxFileSearch(
  sessionId: string | undefined,
  query: string,
  rootPath = '/repo',
  enabled = true,
): UseQueryResult<SandboxFileInfo[], Error> {
  return useQuery({
    queryKey: [...SANDBOX_FILE_SEARCH_KEY, sessionId, query, rootPath],
    queryFn: () => searchSandboxFiles(sessionId!, query, rootPath),
    enabled: !!sessionId && !!query && enabled,
    staleTime: 30_000,
  })
}

// ─── SSE Hooks ──────────────────────────────────────────────────────

export function useSandboxLogs(sessionId: string | undefined) {
  const [logs, setLogs] = useState<string[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  // Native EventSource over fetch+ReadableStream: half the code, and
  // we get browser-native automatic reconnection on transient drops
  // (gateway pod restart, network hiccup) for free. The manual loop
  // it replaced lost connection state on navigation and never
  // retried, so users had to click Stream again after any blip.
  //
  // Wire format on the gateway side is the same as before:
  //   data: {"line": "<log line>"}\n\n
  //   event: error\n
  //   data: {"error": "..."}\n\n
  // Error events are observed and surfaced via onerror; everything
  // else flows through the default message handler.
  const startStreaming = useCallback(() => {
    if (!sessionId) return
    if (eventSourceRef.current) return // already streaming

    setIsStreaming(true)
    const baseUrl = getApiBaseUrl()
    const url = `${baseUrl}/v1/sandbox/${sessionId}/logs/stream`

    // withCredentials carries the auth cookie through to the gateway
    // — same posture as the old fetch call which used the default
    // include credentials mode.
    const es = new EventSource(url, { withCredentials: true })
    eventSourceRef.current = es

    // The gateway emits NAMED events ("event: log\n...") rather than
    // the default channel — onmessage only catches default-channel
    // events, so we need an explicit addEventListener for "log" or
    // we silently drop every payload. The first iteration of this
    // hook (PR #213) registered only onmessage and that's why logs
    // appeared dead in production.
    const handleLogEvent = (event: MessageEvent) => {
      try {
        const parsed = JSON.parse(event.data) as { line?: string }
        if (parsed.line) setLogs((prev) => [...prev, parsed.line!])
      } catch {
        // malformed payload — skip rather than tearing down the stream
      }
    }
    es.addEventListener('log', handleLogEvent)

    // Keep onmessage too for forward compatibility — if the gateway
    // ever sends default-channel events (heartbeats, schema changes)
    // we won't silently drop them.
    es.onmessage = handleLogEvent

    // Named "error" events: the gateway emits these when the
    // underlying gRPC stream fails (rare but possible). Distinct
    // from the EventSource transport-error event, which is fired on
    // .onerror with no .data. The cast is safe because named-event
    // listeners receive MessageEvent; the EventListener-typed
    // .onerror branch handles the transport case.
    es.addEventListener('error', (event: Event) => {
      const me = event as MessageEvent
      if (typeof me.data !== 'string') return // transport-level error, not a named event
      try {
        const parsed = JSON.parse(me.data) as { error?: string }
        if (parsed.error) {
          console.warn('Sandbox logs stream reported error:', parsed.error)
        }
      } catch {
        // benign — bare string payloads are rare
      }
    })

    es.onerror = () => {
      // EventSource auto-reconnects on transport-level errors. We
      // don't tear it down here; just reflect "reconnecting" in the
      // UI by leaving isStreaming=true and letting onmessage resume
      // when the connection recovers. If reconnection genuinely
      // fails (server is gone), the browser stops retrying and
      // readyState transitions to CLOSED — surface that.
      if (eventSourceRef.current?.readyState === EventSource.CLOSED) {
        setIsStreaming(false)
        eventSourceRef.current = null
      }
    }
  }, [sessionId])

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
      setIsStreaming(false)
    }
  }, [])

  const clearLogs = useCallback(() => {
    setLogs([])
  }, [])

  // Auto-start streaming when a sessionId becomes available so logs
  // appear immediately without requiring the user to click "Stream".
  useEffect(() => {
    if (sessionId && !eventSourceRef.current) {
      startStreaming()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  useEffect(() => {
    return () => {
      eventSourceRef.current?.close()
    }
  }, [])

  return { logs, isStreaming, startStreaming, stopStreaming, clearLogs }
}

export function useSandboxStatsStream(sessionId: string | undefined) {
  const [stats, setStats] = useState<SandboxStats[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  // Use a ref to track streaming state so `start` never reads a stale
  // React state value from its closure (state updates are batched).
  const streamingRef = useRef(false)

  const start = useCallback(() => {
    if (!sessionId || streamingRef.current) return

    streamingRef.current = true
    setIsStreaming(true)
    setStats([])
    const baseUrl = getApiBaseUrl()
    const url = `${baseUrl}/v1/sandbox/${sessionId}/stats/stream`
    const controller = new AbortController()
    abortRef.current = controller
    ;(async () => {
      try {
        const response = await fetch(url, { signal: controller.signal })
        if (!response.ok || !response.body) {
          streamingRef.current = false
          setIsStreaming(false)
          return
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() ?? ''

          let currentEvent = ''
          for (const line of lines) {
            if (line.startsWith('event: ')) {
              currentEvent = line.slice(7).trim()
              continue
            }
            if (!line.startsWith('data: ')) continue
            // Skip error events — only process stats
            if (currentEvent === 'error') {
              currentEvent = ''
              continue
            }
            currentEvent = ''
            const jsonStr = line.slice(6).trim()
            if (!jsonStr) continue

            try {
              const parsed = snakeToCamel<SandboxStats>(JSON.parse(jsonStr))
              // Validate that it's actually stats data (has cpu_percent)
              if (parsed.cpuPercent == null) continue
              setStats((prev) => {
                // Keep a 5-minute rolling window (~150 data points at 2s interval)
                const next = [...prev, parsed]
                return next.length > 150 ? next.slice(-150) : next
              })
            } catch {
              // skip malformed
            }
          }
        }
      } catch (err) {
        if ((err as Error).name !== 'AbortError') {
          console.error('Sandbox stats stream error:', err)
        }
      } finally {
        streamingRef.current = false
        setIsStreaming(false)
        abortRef.current = null
      }
    })()
  }, [sessionId])

  const stop = useCallback(() => {
    abortRef.current?.abort()
    // Synchronously clear the ref so a subsequent start() won't be blocked.
    streamingRef.current = false
  }, [])

  useEffect(() => {
    return () => {
      abortRef.current?.abort()
      streamingRef.current = false
    }
  }, [])

  const latestStats = stats.length > 0 ? stats[stats.length - 1] : undefined

  return { stats, latestStats, isStreaming, start, stop }
}

// ─── WebSocket Hook ─────────────────────────────────────────────────

// Server-control envelope sent as MessageText frames. Anything else
// (binary frames or non-JSON text) is shell stdout passed through to
// xterm verbatim.
type ShellServerEvent = {
  type:
    | 'session'
    | 'session_gone'
    | 'sandbox_gone'
    | 'sandbox_recovering'
    | 'pong'
  session_id?: string
  reattached?: boolean
  // Transport that carried this shell: "vsock" (legacy) or "ws"
  // (Phase 5b HTTP control plane). Empty for non-Firecracker backends.
  // The Shell tab renders this as a small chip so operators rolling
  // out the new transport can see at a glance which path each session
  // landed on.
  transport?: string
  message?: string
}

// Storage key for the persistent shell session ID. Keyed by the concrete
// sandbox instance ID, not the higher-level agent/session ID: multiple
// manual sandboxes can share a session lineage but must never share a tmux
// session.
const shellSessionStorageKey = (sandboxId: string) =>
  `everstack:shell-session:${sandboxId}`

const MAX_RECONNECT_DELAY_MS = 3_000
const INITIAL_RECONNECT_DELAY_MS = 300
const HEARTBEAT_INTERVAL_MS = 25_000
const HEARTBEAT_TIMEOUT_MS = 5_000

export function useSandboxShell(sandboxId: string | undefined) {
  const [isConnected, setIsConnected] = useState(false)
  const [isReconnecting, setIsReconnecting] = useState(false)
  const [reattached, setReattached] = useState(false)
  // transport is the host↔guest channel carrying this shell — "vsock"
  // or "ws" — set on the first "session" event from the gateway.
  // Empty until the event arrives or for backends that don't report.
  const [transport, setTransport] = useState<string>('')
  // Diagnostic state surfaced to the status panel. Lets the user see
  // WHY a connection is in its current state without opening DevTools:
  //   - reconnectAttempt: count of automatic retry attempts in the
  //     current cycle. Reset to 0 on successful open.
  //   - nextRetryAtMs: Date.now()+delay for the pending reconnect, or
  //     null when not scheduled. The panel renders a live countdown
  //     against this so the user sees "next in 4s" instead of just
  //     "reconnecting…".
  //   - lastCloseCode / lastCloseReason: from the most recent WS close
  //     event. Helps distinguish network drops (1006), server kill
  //     (1011), or normal close (1000).
  //   - connectedAtMs: timestamp of the most recent successful onopen.
  //     The panel renders uptime as Date.now() - this. Cleared on close.
  //   - shellSessionId: persistent tmux session id the gateway assigned
  //     us. Exposed so the panel + the future tabs UI can show it.
  const [reconnectAttempt, setReconnectAttempt] = useState(0)
  const [nextRetryAtMs, setNextRetryAtMs] = useState<number | null>(null)
  const [lastCloseCode, setLastCloseCode] = useState<number | null>(null)
  const [lastCloseReason, setLastCloseReason] = useState<string>('')
  const [connectedAtMs, setConnectedAtMs] = useState<number | null>(null)
  const [shellSessionId, setShellSessionId] = useState<string>('')
  // isGone: the gateway told us the sandbox's VM no longer exists on
  // any fcagent (a "sandbox_gone" control event or close reason).
  // Terminal for this sandbox: the reconnect loop stops, and the UI
  // should offer reprovisioning instead of "Reconnecting...". The ref
  // mirrors the state so socket callbacks can read it without stale
  // closures.
  const [isGone, setIsGone] = useState(false)
  const goneRef = useRef(false)
  // isRecovering: the gateway told us the VM died but the platform is
  // auto-restoring it (lifecycle row still desires running — the
  // HealthSweeper marks it error and the RecoveryChecker revives it).
  // NON-terminal, unlike isGone: the reconnect loop keeps running and the
  // terminal reattaches once the new VM answers. Cleared on a successful
  // open.
  const [isRecovering, setIsRecovering] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  // Active connection callbacks. Kept on a ref so the auto-reconnect
  // loop can re-invoke connect() with the same handlers without the
  // caller needing to re-pass them.
  const handlersRef = useRef<{
    onData: (data: string) => void
    onClose?: () => void
  } | null>(null)
  const reconnectTimerRef = useRef<number | null>(null)
  const reconnectDelayRef = useRef(INITIAL_RECONNECT_DELAY_MS)
  const heartbeatIntervalRef = useRef<number | null>(null)
  const heartbeatTimeoutRef = useRef<number | null>(null)
  // Monotonic connection generation. Closing an old WebSocket after a
  // sandbox switch must not schedule a reconnect against its stale
  // sandbox/session identity.
  const connectionGenerationRef = useRef(0)
  // True while the user-facing teardown is in progress so the
  // automatic reconnect loop doesn't fight it.
  const userDisconnectedRef = useRef(false)

  const clearStoredShellSession = useCallback(() => {
    if (!sandboxId) return
    try {
      sessionStorage.removeItem(shellSessionStorageKey(sandboxId))
    } catch {
      // sessionStorage can throw in private modes — non-fatal.
    }
  }, [sandboxId])

  const storeShellSession = useCallback(
    (id: string) => {
      if (!sandboxId || !id) return
      try {
        sessionStorage.setItem(shellSessionStorageKey(sandboxId), id)
      } catch {
        // Same as clearStoredShellSession — best-effort.
      }
    },
    [sandboxId],
  )

  // getOrCreateShellSession returns the persisted shell-session id for
  // this sandbox tab, minting one if none exists yet. Critical for the
  // single-flight contract on the agent: every connect (initial,
  // reconnect, React re-mount, refresh) MUST present the same id so
  // the agent's LoadOrStore converges concurrent attaches onto one
  // tmux session. Without this, the first connect on a fresh sandbox
  // sent no id, the server generated one and reported it back, and
  // every reconnect that fired before that report landed back in the
  // sessionStorage created its own orphan session.
  const getOrCreateShellSession = useCallback(() => {
    if (!sandboxId) return ''
    try {
      const existing = sessionStorage.getItem(shellSessionStorageKey(sandboxId))
      if (existing) return existing
      const minted =
        typeof crypto !== 'undefined' && crypto.randomUUID
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(16).slice(2)}`
      sessionStorage.setItem(shellSessionStorageKey(sandboxId), minted)
      return minted
    } catch {
      // sessionStorage can throw in private modes — degrade to empty.
      // Server will fall back to its own id generation; the only cost
      // is losing reattach across reconnects in this private session.
      return ''
    }
  }, [sandboxId])

  const stopHeartbeat = useCallback(() => {
    if (heartbeatIntervalRef.current !== null) {
      clearInterval(heartbeatIntervalRef.current)
      heartbeatIntervalRef.current = null
    }
    if (heartbeatTimeoutRef.current !== null) {
      clearTimeout(heartbeatTimeoutRef.current)
      heartbeatTimeoutRef.current = null
    }
  }, [])

  const startHeartbeat = useCallback(
    (ws: WebSocket, generation: number) => {
      stopHeartbeat()
      heartbeatIntervalRef.current = window.setInterval(() => {
        if (
          connectionGenerationRef.current !== generation ||
          ws.readyState !== WebSocket.OPEN
        ) {
          stopHeartbeat()
          return
        }
        ws.send(JSON.stringify({ type: 'ping' }))
        heartbeatTimeoutRef.current = window.setTimeout(() => {
          if (
            connectionGenerationRef.current !== generation ||
            wsRef.current !== ws
          )
            return
          ws.close(4000, 'heartbeat timeout')
        }, HEARTBEAT_TIMEOUT_MS)
      }, HEARTBEAT_INTERVAL_MS)
    },
    [stopHeartbeat],
  )

  const openSocket = useCallback(
    (
      onData: (data: string) => void,
      onClose?: () => void,
      shellSessionId?: string,
    ) => {
      if (!sandboxId) return

      const generation = connectionGenerationRef.current + 1
      connectionGenerationRef.current = generation

      const baseUrl = getApiBaseUrl()
      const wsProtocol = baseUrl.startsWith('https') ? 'wss' : 'ws'
      const query = shellSessionId
        ? `?shell_session=${encodeURIComponent(shellSessionId)}`
        : ''
      const wsUrl = `${wsProtocol}://${baseUrl.replace(/^https?:\/\//, '')}/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/shell${query}`

      const ws = new WebSocket(wsUrl)
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      handlersRef.current = { onData, onClose }

      ws.onopen = () => {
        if (
          connectionGenerationRef.current !== generation ||
          wsRef.current !== ws
        )
          return
        setIsConnected(true)
        setIsReconnecting(false)
        setIsRecovering(false)
        setReconnectAttempt(0)
        setNextRetryAtMs(null)
        setConnectedAtMs(Date.now())
        setLastCloseCode(null)
        setLastCloseReason('')
        reconnectDelayRef.current = INITIAL_RECONNECT_DELAY_MS
        startHeartbeat(ws, generation)
      }

      ws.onmessage = (event) => {
        if (
          connectionGenerationRef.current !== generation ||
          wsRef.current !== ws
        )
          return
        if (event.data instanceof ArrayBuffer) {
          onData(new TextDecoder().decode(event.data))
          return
        }
        const text = event.data as string
        // Text frames carry control events from the gateway. Try to
        // parse as JSON; if it doesn't match the event shape, fall
        // through to writing it as shell output. The shell only sends
        // stdout as binary frames in practice, so this branch is
        // mostly for the server-control path.
        try {
          const ev = JSON.parse(text) as ShellServerEvent
          if (ev && typeof ev.type === 'string') {
            if (ev.type === 'pong') {
              if (heartbeatTimeoutRef.current !== null) {
                clearTimeout(heartbeatTimeoutRef.current)
                heartbeatTimeoutRef.current = null
              }
              return
            }
            if (ev.type === 'session') {
              if (ev.session_id) {
                storeShellSession(ev.session_id)
                setShellSessionId(ev.session_id)
              }
              setReattached(Boolean(ev.reattached))
              if (typeof ev.transport === 'string') {
                setTransport(ev.transport)
              }
              return
            }
            if (ev.type === 'sandbox_gone') {
              // The VM behind this sandbox is gone on every fcagent.
              // Unlike session_gone (one dead tmux session, fixable by
              // minting a new one), this is terminal: mark gone so the
              // close handler skips the reconnect loop, and clear the
              // stored session id so a future reprovision starts clean.
              goneRef.current = true
              setIsGone(true)
              clearStoredShellSession()
              setShellSessionId('')
              try {
                ws.close(1000, 'sandbox_gone')
              } catch {
                // best-effort; the server closes its side anyway.
              }
              return
            }
            if (ev.type === 'sandbox_recovering') {
              // The VM died but the platform is auto-restoring it
              // (desired_state=running). Unlike sandbox_gone this is NOT
              // terminal: leave goneRef false so the reconnect loop keeps
              // running and the terminal reattaches once the new VM is up.
              // The old tmux session died with the old VM, so clear the
              // cached id and let reconnect mint a fresh one (same as
              // session_gone, minus the give-up).
              setIsRecovering(true)
              clearStoredShellSession()
              setShellSessionId('')
              try {
                ws.close(1000, 'sandbox_recovering')
              } catch {
                // best-effort; the onclose path schedules the reconnect.
              }
              return
            }
            if (ev.type === 'session_gone') {
              // Server told us our persisted session is dead (reaper
              // got it, fcagent restart cleared it, user killed it
              // elsewhere). Clear our cached id and tear down this
              // WS so the auto-reconnect path mints a FRESH session.
              // Without the explicit close, the WS stays open with no
              // shell attached — the terminal sits at the banner with
              // a cursor and no prompt, and the user has to manually
              // hit "+ New shell" to recover. ws.close() fires the
              // onclose handler, which schedules a reconnect; on the
              // next openSocket call getOrCreateShellSession sees
              // empty storage and mints anew.
              clearStoredShellSession()
              setShellSessionId('')
              try {
                ws.close(1000, 'session_gone')
              } catch {
                // best-effort; reconnect will still take over via
                // the existing onclose path when the socket settles.
              }
              return
            }
          }
        } catch {
          // Not JSON — fall through.
        }
        onData(text)
      }

      const scheduleReconnect = () => {
        if (connectionGenerationRef.current !== generation) return
        if (userDisconnectedRef.current) return
        if (goneRef.current) return
        if (reconnectTimerRef.current !== null) return
        stopHeartbeat()
        const base = reconnectDelayRef.current
        const jitter = base * (0.7 + Math.random() * 0.6)
        const delay = Math.round(jitter)
        setIsReconnecting(true)
        setReconnectAttempt((n) => n + 1)
        setNextRetryAtMs(Date.now() + delay)
        reconnectTimerRef.current = window.setTimeout(() => {
          reconnectTimerRef.current = null
          if (connectionGenerationRef.current !== generation) return
          reconnectDelayRef.current = Math.min(base * 2, MAX_RECONNECT_DELAY_MS)
          setNextRetryAtMs(null)
          if (userDisconnectedRef.current) return
          const handlers = handlersRef.current
          if (!handlers) return
          openSocket(
            handlers.onData,
            handlers.onClose,
            getOrCreateShellSession(),
          )
        }, delay)
      }

      ws.onclose = (event) => {
        if (connectionGenerationRef.current !== generation) return
        stopHeartbeat()
        if (wsRef.current === ws) {
          setIsConnected(false)
          setConnectedAtMs(null)
          // Capture close diagnostics. Code 1006 (abnormal closure) is
          // the most common we'll see — network drop, server-side TCP
          // RST, no clean close frame. 1011 = server "internal error"
          // (gateway intentionally killed the WS). 1000 = normal close,
          // shouldn't trigger reconnect.
          setLastCloseCode(event.code || null)
          setLastCloseReason(event.reason || '')
          wsRef.current = null
        }
        // Server-side close carrying the terminal marker. Covers the
        // case where the close frame arrives without (or before) the
        // sandbox_gone control event.
        if (event.reason === 'sandbox_gone') {
          goneRef.current = true
          setIsGone(true)
        }
        // Fire the user-supplied onClose ONCE per top-level
        // connect() call so the component can show a banner. The
        // auto-reconnect loop replaces the socket; subsequent
        // closes during reconnect attempts shouldn't keep firing
        // the user callback.
        if (!userDisconnectedRef.current && handlersRef.current?.onClose) {
          handlersRef.current.onClose()
          handlersRef.current = { ...handlersRef.current, onClose: undefined }
        }
        if (!userDisconnectedRef.current) {
          scheduleReconnect()
        }
      }

      ws.onerror = () => {
        if (connectionGenerationRef.current !== generation) return
        ws.close()
      }
    },
    [
      sandboxId,
      storeShellSession,
      clearStoredShellSession,
      getOrCreateShellSession,
      startHeartbeat,
      stopHeartbeat,
    ],
  )

  const connect = useCallback(
    (onData: (data: string) => void, onClose?: () => void) => {
      if (!sandboxId || wsRef.current) return
      userDisconnectedRef.current = false
      // An explicit connect() is a fresh start: clear the terminal
      // gone state so a user retrying after a reprovision gets one
      // real attempt (the server re-marks gone if it still is).
      goneRef.current = false
      setIsGone(false)
      reconnectDelayRef.current = INITIAL_RECONNECT_DELAY_MS
      setReattached(false)
      // Always send a client-minted id (or persisted one). The server
      // honours it via LoadOrStore single-flight, so concurrent
      // reconnects converge on a single tmux session.
      openSocket(onData, onClose, getOrCreateShellSession())
    },
    [sandboxId, openSocket, getOrCreateShellSession],
  )

  const send = useCallback((data: string) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify({ type: 'input', data }))
    }
  }, [])

  const resize = useCallback((rows: number, cols: number) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify({ type: 'resize', rows, cols }))
    }
  }, [])

  const disconnect = useCallback(() => {
    userDisconnectedRef.current = true
    connectionGenerationRef.current += 1
    stopHeartbeat()
    if (reconnectTimerRef.current !== null) {
      clearTimeout(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
    const ws = wsRef.current
    wsRef.current = null
    handlersRef.current = null
    setIsConnected(false)
    setIsReconnecting(false)
    setReattached(false)
    setReconnectAttempt(0)
    setNextRetryAtMs(null)
    setConnectedAtMs(null)
    ws?.close()
  }, [stopHeartbeat])

  useEffect(() => {
    return () => {
      userDisconnectedRef.current = true
      connectionGenerationRef.current += 1
      stopHeartbeat()
      if (reconnectTimerRef.current !== null) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      wsRef.current?.close()
    }
  }, [stopHeartbeat])

  return {
    connect,
    send,
    resize,
    disconnect,
    isConnected,
    isReconnecting,
    isGone,
    isRecovering,
    reattached,
    transport,
    reconnectAttempt,
    nextRetryAtMs,
    lastCloseCode,
    lastCloseReason,
    connectedAtMs,
    shellSessionId,
  }
}

// ─── Persistent shell sessions ───────────────────────────────────────

export type ShellSessionRow = {
  id: string
  attached_clients: number
  created_unix: number
  // Negative value means the guest didn't report idle time — the
  // panel renders this as "unknown" rather than treating it as 0.
  idle_seconds: number
}

type ShellSessionsResponse = {
  sessions: ShellSessionRow[]
}

const shellSessionsQueryKey = (sandboxId: string) =>
  ['sandbox', sandboxId, 'shell-sessions'] as const

// useSandboxShellSessions polls the persistent shell sessions for a
// sandbox every 15s while the page is visible — matches the existing
// sandbox-stats cadence so we're not introducing a new noisy poller.
// React Query handles the visibility-pause and dedupes the request
// across multiple consumers (panel + status hints in the shell tab).
// SANDBOX_GONE_ERROR is the typed error thrown by the sessions
// queryFn when the gateway reports the sandbox's VM no longer
// exists on any fcagent (HTTP 410 Gone). Consumers check
// `error.message === SANDBOX_GONE_ERROR` to render a clean
// "sandbox no longer running, reprovision required" state
// instead of treating it as a transient fetch failure.
export const SANDBOX_GONE_ERROR = 'sandbox_gone'

export function useSandboxShellSessions(sandboxId: string | undefined) {
  return useQuery({
    queryKey: sandboxId
      ? shellSessionsQueryKey(sandboxId)
      : ['shell-sessions', 'idle'],
    enabled: Boolean(sandboxId),
    refetchInterval: (query) => {
      // Stop polling once the gateway has confirmed the sandbox is
      // gone — the answer won't change without a reprovision, and
      // hammering the endpoint every 15s would log a fresh stack
      // trace on the gateway for nothing.
      if ((query.state.error as Error | null)?.message === SANDBOX_GONE_ERROR) {
        return false
      }
      return 15_000
    },
    // Don't retry the gone state. Default react-query retry is 3x
    // with backoff, which makes the "sandbox no longer running"
    // UI flicker between "loading" and "error" while it tries.
    retry: (_failureCount, error) =>
      (error as Error).message !== SANDBOX_GONE_ERROR,
    refetchOnWindowFocus: true,
    queryFn: async (): Promise<ShellSessionRow[]> => {
      if (!sandboxId) return []
      const baseUrl = getApiBaseUrl()
      const resp = await fetch(
        `${baseUrl}/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/shell-sessions`,
        { credentials: 'include' },
      )
      // 410 Gone is the gateway's typed signal that the VM is
      // gone on every fcagent — the sandbox can't recover without
      // a reprovision. Surface a stable error string so the UI
      // can branch on it without scraping HTTP status codes.
      if (resp.status === 410) {
        throw new Error(SANDBOX_GONE_ERROR)
      }
      if (!resp.ok) {
        // The session-list endpoint is also load-bearing for the
        // tab's "you have N sessions" hint. Surface the error so
        // react-query can retry, but don't crash the panel — keep
        // any previously-fetched list visible via keepPreviousData
        // (controlled at consumer level via placeholderData when
        // wanted).
        throw new Error(`HTTP ${resp.status}`)
      }
      const body = (await resp.json()) as ShellSessionsResponse
      return body.sessions ?? []
    },
  })
}

// useKillShellSession is the mutation that backs the panel's "Kill"
// button. Optimistically removes the row from the cached list so the
// UI feels instant; rolls back on error.
export function useKillShellSession(
  sandboxId: string | undefined,
): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (shellSessionId: string) => {
      if (!sandboxId) throw new Error('sandboxId is required')
      const baseUrl = getApiBaseUrl()
      const resp = await fetch(
        `${baseUrl}/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/shell-sessions/${encodeURIComponent(shellSessionId)}`,
        {
          method: 'DELETE',
          credentials: 'include',
        },
      )
      if (!resp.ok && resp.status !== 204) {
        throw new Error(`HTTP ${resp.status}`)
      }
    },
    onMutate: async (shellSessionId) => {
      if (!sandboxId)
        return { previous: undefined as ShellSessionRow[] | undefined }
      await queryClient.cancelQueries({
        queryKey: shellSessionsQueryKey(sandboxId),
      })
      const previous = queryClient.getQueryData<ShellSessionRow[]>(
        shellSessionsQueryKey(sandboxId),
      )
      if (previous) {
        queryClient.setQueryData<ShellSessionRow[]>(
          shellSessionsQueryKey(sandboxId),
          previous.filter((s) => s.id !== shellSessionId),
        )
      }
      return { previous }
    },
    onError: (_err, _shellSessionId, ctx) => {
      if (!sandboxId) return
      const previous = (ctx as { previous?: ShellSessionRow[] } | undefined)
        ?.previous
      if (previous) {
        queryClient.setQueryData(shellSessionsQueryKey(sandboxId), previous)
      }
    },
    onSettled: () => {
      if (!sandboxId) return
      queryClient.invalidateQueries({
        queryKey: shellSessionsQueryKey(sandboxId),
      })
    },
  })
}

// useNotifyShellSessionCreated returns a callback the Shell tab
// can fire the moment the gateway confirms a freshly-minted shell
// session id. It (a) optimistically inserts a placeholder row into
// the cached sessions list so the tab strip shows the new tab in
// the same frame, and (b) invalidates the query so the next poll
// confirms the optimistic insert against the server.
//
// Without this, the tab strip waits up to 15 seconds (the default
// shell-sessions refetchInterval) for the new tab to appear after
// "+ New shell" — confusing UX where the terminal works but no
// tab visibly tracks it.
//
// The optimistic row uses the just-resolved id plus sensible
// defaults: attached_clients=1 (it's the current tab), idle=-1
// (unknown, will be filled in on next real fetch), created_unix=
// now. React Query merges the next server response on top of this
// without flicker.
export function useNotifyShellSessionCreated(sandboxId: string | undefined) {
  const queryClient = useQueryClient()
  return useCallback(
    (shellSessionId: string) => {
      if (!sandboxId || !shellSessionId) return
      const key = shellSessionsQueryKey(sandboxId)
      queryClient.setQueryData<ShellSessionRow[]>(key, (prev) => {
        const previous = prev ?? []
        if (previous.some((s) => s.id === shellSessionId)) {
          return previous
        }
        const optimistic: ShellSessionRow = {
          id: shellSessionId,
          attached_clients: 1,
          created_unix: Math.floor(Date.now() / 1000),
          idle_seconds: -1,
        }
        return [optimistic, ...previous]
      })
      // Schedule a fresh fetch so the placeholder is reconciled
      // against the server's authoritative list. Fast invalidate —
      // we want the real row to land within one HTTP round-trip,
      // not at the next 15s poll boundary.
      queryClient.invalidateQueries({ queryKey: key })
    },
    [queryClient, sandboxId],
  )
}

// Helper used by the panel to surface the current tab's shell
// session ID (the one the user's WebSocket attached to). Stored in
// sessionStorage by useSandboxShell — keep both in sync.
export function readCurrentShellSessionId(
  sandboxId: string | undefined,
): string {
  if (!sandboxId) return ''
  try {
    return sessionStorage.getItem(shellSessionStorageKey(sandboxId)) ?? ''
  } catch {
    return ''
  }
}

// ─── Snapshots (POR-75) ─────────────────────────────────────────────────────
// Tenant-scoped resources — no session/org arg needed; the client authenticates
// via the session cookie and the server scopes by the authenticated tenant.

const SANDBOX_SNAPSHOTS_KEY = ['sandbox-snapshots']
const SANDBOX_VOLUMES_KEY = ['sandbox-volumes']

export function useSnapshots(): UseQueryResult<
  { snapshots: Snapshot[]; total: number },
  Error
> {
  return useQuery({
    queryKey: SANDBOX_SNAPSHOTS_KEY,
    queryFn: () => listSnapshots(),
    refetchInterval: 10000,
  })
}

export function useCreateSnapshot(): UseMutationResult<
  Snapshot,
  Error,
  CreateSnapshotParams
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: CreateSnapshotParams) => createSnapshot(params),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_SNAPSHOTS_KEY })
    },
  })
}

export function useDeleteSnapshot(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteSnapshot(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_SNAPSHOTS_KEY })
    },
  })
}

// ─── Volumes (POR-77) ────────────────────────────────────────────────────────

export function useVolumes(): UseQueryResult<
  { volumes: Volume[]; total: number },
  Error
> {
  return useQuery({
    queryKey: SANDBOX_VOLUMES_KEY,
    queryFn: () => listVolumes(),
    refetchInterval: 10000,
  })
}

export function useCreateVolume(): UseMutationResult<
  Volume,
  Error,
  { name: string; sizeGb?: number }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (params: { name: string; sizeGb?: number }) =>
      createVolume(params),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_VOLUMES_KEY })
    },
  })
}

export function useDeleteVolume(): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteVolume(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SANDBOX_VOLUMES_KEY })
    },
  })
}
