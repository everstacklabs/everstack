import { useQuery, useMutation, useQueryClient, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import {
    listAgents,
    getAgent,
    createAgent,
    updateAgent,
    deleteAgent,
    createSession,
    listSessions,
    getSession,
    cancelSession,
    completeSession,
    steerSession,
    listReviews,
    submitReview,
    getAgentCapabilities,
    listAgentLinks,
    createAgentLink,
    deleteAgentLink,
    type ListAgentsParams,
    type CreateAgentParams,
    type UpdateAgentParams,
    type CreateSessionParams,
    type ListSessionsParams,
    type SteerSessionParams,
    type ListReviewsParams,
    type SubmitReviewParams,
    type CreateAgentLinkParams,
    type AgentDefinition,
    type AgentSession,
    type AgentLink,
    type ApprovalReview,
    type AgentCapabilities,
    ApprovalReviewStatus,
    AgentMode,
    SessionStatus,
} from '@/server/agents'
import type {
    CreateAgentResponse,
    UpdateAgentResponse,
    DeleteAgentResponse,
    CreateSessionResponse,
    CancelSessionResponse,
    CompleteSessionResponse,
    SteerSessionResponse,
    SubmitReviewResponse,
    CreateAgentLinkResponse,
    DeleteAgentLinkResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { useSession } from '@/hooks/auth/use-auth'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useAgentSessionStore, type ToolResultCacheEntry, readPersistedExposedURLs } from '@/stores/agent-session-store'

const AGENTS_QUERY_KEY = ['agents']
const SESSIONS_QUERY_KEY = ['agent-sessions']
const REVIEWS_QUERY_KEY = ['agent-reviews']
const CAPABILITIES_QUERY_KEY = ['agent-capabilities']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Agent Query Hooks ──────────────────────────────────────────────

export function useAgents(params: Omit<ListAgentsParams, 'tenantId'> = {}): UseQueryResult<AgentDefinition[], Error> {
    const orgId = useOrganizationId()
    const enabledFilter = params.enabled
    const limit = params.limit
    const offset = params.offset
    const includeHidden = params.includeHidden ?? false
    const mode = params.mode ?? AgentMode.UNSPECIFIED
    const lifecycleMode = params.lifecycleMode

    return useQuery({
        queryKey: [...AGENTS_QUERY_KEY, orgId, enabledFilter ?? null, limit ?? null, offset ?? null, includeHidden, mode, lifecycleMode ?? null],
        queryFn: async () => {
            const response = await listAgents({
                tenantId: orgId,
                enabled: enabledFilter,
                limit,
                offset,
                includeHidden,
                mode,
                lifecycleMode,
            })
            return response.agents ?? []
        },
        enabled: true,
        refetchOnWindowFocus: false,
        refetchOnMount: true,
        staleTime: 30_000,
        retry: 1,
    })
}

export function useAgent(id: string): UseQueryResult<AgentDefinition | undefined, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...AGENTS_QUERY_KEY, orgId, id],
        queryFn: async () => {
            if (!id) return undefined
            const response = await getAgent(id, orgId)
            return response.agent
        },
        enabled: !!id,
        refetchOnWindowFocus: false,
        refetchOnMount: true,
        staleTime: 30_000,
        retry: false,
    })
}

// ─── Agent Capabilities ─────────────────────────────────────────────

export function useAgentCapabilities(): UseQueryResult<AgentCapabilities, Error> {
    return useQuery({
        queryKey: CAPABILITIES_QUERY_KEY,
        queryFn: getAgentCapabilities,
        staleTime: 5 * 60 * 1000,
        refetchOnWindowFocus: false,
    })
}

// ─── Agent Mutation Hooks ───────────────────────────────────────────

export function useCreateAgent(): UseMutationResult<CreateAgentResponse, Error, Omit<CreateAgentParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createAgent({ ...params, tenantId: orgId }),
        onSuccess: () => {
            // Delay to give the async CQRS projection time to persist
            // the new agent to the read model before re-querying.
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: AGENTS_QUERY_KEY })
            }, 500)
        },
    })
}

export function useUpdateAgent(): UseMutationResult<UpdateAgentResponse, Error, Omit<UpdateAgentParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => updateAgent({ ...params, tenantId: orgId }),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: AGENTS_QUERY_KEY })
            }, 500)
        },
    })
}

export function useDeleteAgent(): UseMutationResult<DeleteAgentResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id) => deleteAgent(id, orgId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: AGENTS_QUERY_KEY })
            }, 500)
        },
    })
}

// ─── Session Query Hooks ────────────────────────────────────────────

export function useSessions(params: Omit<ListSessionsParams, 'tenantId'> = {}): UseQueryResult<AgentSession[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...SESSIONS_QUERY_KEY, orgId, params],
        queryFn: async () => {
            const response = await listSessions({ ...params, tenantId: orgId })
            return response.sessions ?? []
        },
        enabled: true,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function useSession_(id: string): UseQueryResult<AgentSession | undefined, Error> {
    const orgId = useOrganizationId()

    const query = useQuery({
        queryKey: [...SESSIONS_QUERY_KEY, orgId, id],
        queryFn: async () => {
            if (!id) return undefined
            const response = await getSession(id, orgId)
            return response.session
        },
        enabled: !!id,
        refetchOnWindowFocus: false,
        refetchOnMount: true,
        staleTime: 30_000,
        retry: false,
    })

    // When the session is terminal but the loaded turns don't match
    // turnCount, the async CQRS projection hasn't finished yet.
    // Poll until turns are fully projected.
    const session = query.data
    const turnsIncomplete =
        session &&
        session.turnCount > 0 &&
        (session.turns?.length ?? 0) < session.turnCount

    useEffect(() => {
        if (!turnsIncomplete) return
        const timer = setInterval(() => {
            query.refetch()
        }, 1000)
        return () => clearInterval(timer)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [turnsIncomplete])

    // Poll for updates on active sessions (running, waiting_for_input, etc.)
    // that aren't streaming via SSE. This covers channel sessions where the
    // runner goes idle between turns (Discord/Slack messages arrive externally).
    const isActive = session != null && session.status !== SessionStatus.COMPLETED
        && session.status !== SessionStatus.FAILED && session.status !== SessionStatus.CANCELLED
    const storeEntry = useAgentSessionStore((s) => s.sessions[id])
    const isStreaming = storeEntry?.isStreaming ?? false

    useEffect(() => {
        if (!isActive || isStreaming) return
        const timer = setInterval(() => {
            query.refetch()
        }, 3000)
        return () => clearInterval(timer)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isActive, isStreaming])

    return query
}

export function useCancelSession(): UseMutationResult<CancelSessionResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (sessionId) => cancelSession(sessionId, orgId),
        onSuccess: () => {
            // Delay the refetch to give the async projection time to persist
            // the cancelled status to the DB before we re-query.
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
            }, 500)
        },
    })
}

export function useCompleteSession(): UseMutationResult<CompleteSessionResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (sessionId) => completeSession(sessionId, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
        },
    })
}

export function useCreateSession(): UseMutationResult<CreateSessionResponse, Error, Omit<CreateSessionParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createSession({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
        },
    })
}

export function useSteerSession(): UseMutationResult<SteerSessionResponse, Error, Omit<SteerSessionParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => steerSession({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
        },
    })
}

// ─── Review Hooks ───────────────────────────────────────────────────

export function useReviews(params: Omit<ListReviewsParams, 'tenantId'> = {}): UseQueryResult<ApprovalReview[], Error> {
    const orgId = useOrganizationId()
    const { sessionId, status, limit, offset } = params

    return useQuery({
        queryKey: [...REVIEWS_QUERY_KEY, orgId, sessionId ?? null, status ?? null, limit ?? null, offset ?? null],
        queryFn: async () => {
            const response = await listReviews({ ...params, tenantId: orgId })
            return response.reviews ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: true,
        refetchOnMount: true,
        staleTime: 0,
    })
}

export function usePendingReviews(): UseQueryResult<ApprovalReview[], Error> {
    return useReviews({ status: ApprovalReviewStatus.PENDING })
}

export function useSubmitReview(): UseMutationResult<SubmitReviewResponse, Error, Omit<SubmitReviewParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => submitReview({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: REVIEWS_QUERY_KEY })
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
        },
    })
}

// ─── Agent Link Hooks ───────────────────────────────────────────────

const AGENT_LINKS_QUERY_KEY = ['agent-links']

export function useAgentLinks(agentId: string): UseQueryResult<AgentLink[], Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...AGENT_LINKS_QUERY_KEY, orgId, agentId],
        queryFn: async () => {
            if (!agentId) return []
            const response = await listAgentLinks({ tenantId: orgId, agentId })
            return response.links ?? []
        },
        enabled: !!agentId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useCreateAgentLink(): UseMutationResult<CreateAgentLinkResponse, Error, Omit<CreateAgentLinkParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createAgentLink({ ...params, tenantId: orgId }),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: AGENT_LINKS_QUERY_KEY })
            }, 500)
        },
    })
}

export function useDeleteAgentLink(): UseMutationResult<DeleteAgentLinkResponse, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (linkId) => deleteAgentLink(linkId, orgId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: AGENT_LINKS_QUERY_KEY })
            }, 500)
        },
    })
}

// ─── SSE Streaming Hook ─────────────────────────────────────────────

// Re-export types for existing consumers
export type { AgentStreamEvent } from '@/lib/sse-utils'
export type { ToolResultCacheEntry } from '@/stores/agent-session-store'

type AgentStreamEvent = import('@/lib/sse-utils').AgentStreamEvent

// Stable fallbacks to avoid new-reference issues in renders
const EMPTY_EVENTS: AgentStreamEvent[] = []
const EMPTY_CACHE: Record<string, ToolResultCacheEntry> = {}
const EMPTY_URLS: Record<number, string> = {}

/**
 * Manual subscription to a specific session entry in the store.
 * We intentionally avoid Zustand's hook (which wraps useSyncExternalStore)
 * because the per-session selector returns `undefined` before the entry exists,
 * then an object once subscribe() creates it. In Zustand v5, the uncached
 * getSnapshot triggers an infinite "store changed during commit" re-render loop.
 */
function useSessionStoreEntry(sessionId: string) {
    const [entry, setEntry] = useState(
        () => useAgentSessionStore.getState().sessions[sessionId],
    )
    const entryRef = useRef(entry)

    useEffect(() => {
        // Sync in case the store changed between render and effect
        const current = useAgentSessionStore.getState().sessions[sessionId]
        if (current !== entryRef.current) {
            entryRef.current = current
            setEntry(current)
        }

        return useAgentSessionStore.subscribe((state) => {
            const next = state.sessions[sessionId]
            if (next !== entryRef.current) {
                entryRef.current = next
                setEntry(next)
            }
        })
    }, [sessionId])

    return entry
}

import type { AgentSessionTurn } from '@/server/agents'

export function useSessionEvents(sessionId: string, sessionStatus?: number, persistedTurns?: AgentSessionTurn[]) {
    const orgId = useOrganizationId()
    const queryClient = useQueryClient()

    const entry = useSessionStoreEntry(sessionId)
    const events = entry?.events ?? EMPTY_EVENTS
    const isStreaming = entry?.isStreaming ?? false
    const toolResultsCache = entry?.toolResultsCache ?? EMPTY_CACHE
    const persistedExposedURLs = useMemo(
        () => (sessionId ? readPersistedExposedURLs(sessionId) : EMPTY_URLS),
        [sessionId],
    )
    const exposedURLs = entry?.exposedURLs ?? persistedExposedURLs
    const browserStreamActive = entry?.browserStreamActive ?? false
    const browserStreamSessionId = entry?.browserStreamSessionId ?? null
    const browserScreenshotBase64 = entry?.browserScreenshotBase64 ?? null

    // Hydrate from IndexedDB, passing persisted turn numbers so stale data is filtered out.
    // Re-runs when persistedTurns changes (e.g. CQRS projection catches up after page load).
    const persistedTurnNumbers = useMemo(() => {
        const s = new Set<number>()
        if (persistedTurns) {
            for (const t of persistedTurns) s.add(t.turnNumber)
        }
        return s
    }, [persistedTurns])

    useEffect(() => {
        if (!sessionId) return
        void useAgentSessionStore.getState().hydrateSessionFromCache(sessionId, persistedTurnNumbers)
    }, [sessionId, persistedTurnNumbers])

    const hydrationDone = useAgentSessionStore((s) => s.hydrationDone[sessionId] ?? false)

    // Auto-subscribe for active sessions (running or waiting for input).
    // Channel sessions spend most time in WAITING_FOR_INPUT between turns,
    // but a new Discord/Slack message can re-launch them at any time.
    const isSessionActive = sessionStatus === SessionStatus.RUNNING
        || sessionStatus === SessionStatus.WAITING_FOR_INPUT
        || sessionStatus === SessionStatus.WAITING_FOR_APPROVAL
    useEffect(() => {
        if (!isSessionActive || !sessionId) return
        useAgentSessionStore.getState().subscribe(sessionId, sessionStatus!, queryClient)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isSessionActive, sessionId])

    const startTurn = useCallback(async (userInput: string, options?: { enableWebSearch?: boolean; modelOverride?: string }) => {
        if (!sessionId || !orgId) return
        await useAgentSessionStore.getState().startTurn(sessionId, orgId, userInput, queryClient, options)
    }, [sessionId, orgId, queryClient])

    const stopStream = useCallback(() => {
        return useAgentSessionStore.getState().stopStream(sessionId, orgId, queryClient)
    }, [sessionId, orgId, queryClient])

    const clearEvents = useCallback(() => {
        useAgentSessionStore.getState().clearEvents(sessionId)
    }, [sessionId])

    const discardTurnEvents = useCallback((turnNumber: number) => {
        useAgentSessionStore.getState().discardTurnEvents(sessionId, turnNumber)
    }, [sessionId])

    return { events, isStreaming, startTurn, stopStream, clearEvents, discardTurnEvents, toolResultsCache, exposedURLs, browserStreamActive, browserStreamSessionId, browserScreenshotBase64, hydrationDone }
}
