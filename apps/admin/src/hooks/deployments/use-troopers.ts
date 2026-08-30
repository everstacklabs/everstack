import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult, type QueryClient } from '@tanstack/react-query'
import {
    listTroopers,
    getTrooper,
    createTrooper,
    updateTrooper,
    deleteTrooper,
    provisionTrooper,
    sleepTrooper,
    wakeTrooper,
    createTrooperLink,
    listTrooperLinks,
    deleteTrooperLink,
    bindChannel,
    unbindChannel,
    listChannelBindings,
    type Trooper,
    type TrooperLink,
    type TrooperChannelBinding,
    type CreateTrooperParams,
    type UpdateTrooperParams,
} from '@/server/troopers'
import { listSessions } from '@/server/agents'
import { useSession } from '@/hooks/auth/use-auth'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import type { AgentStreamEvent } from '@/lib/sse-utils'
import React from 'react'

const TROOPERS_KEY = ['troopers']
const TROOPER_LINKS_KEY = ['trooper-links']
const TROOPER_CHANNELS_KEY = ['trooper-channels']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

// ─── Query Hooks ────────────────────────────────────────────────────

export function useTroopers(
    opts?: { status?: string; limit?: number; offset?: number }
): UseQueryResult<{ troopers: Trooper[]; total: number }, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TROOPERS_KEY, orgId, opts],
        queryFn: () => listTroopers(orgId, opts),
        enabled: !!orgId,
        refetchInterval: 5000,
        refetchOnWindowFocus: 'always',
        refetchOnReconnect: true,
    })
}

export function useTrooper(id: string | undefined): UseQueryResult<Trooper, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TROOPERS_KEY, orgId, id],
        queryFn: () => getTrooper(orgId, id!),
        enabled: !!orgId && !!id,
        refetchInterval: 5000,
        refetchOnWindowFocus: 'always',
        refetchOnReconnect: true,
    })
}

export function useTrooperLinks(trooperId: string | undefined): UseQueryResult<{ links: TrooperLink[]; total: number }, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TROOPER_LINKS_KEY, orgId, trooperId],
        queryFn: () => listTrooperLinks(orgId, trooperId!),
        enabled: !!orgId && !!trooperId,
    })
}

export function useTrooperChannelBindings(trooperId: string | undefined): UseQueryResult<{ bindings: TrooperChannelBinding[]; total: number }, Error> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...TROOPER_CHANNELS_KEY, orgId, trooperId],
        queryFn: () => listChannelBindings(orgId, trooperId!),
        enabled: !!orgId && !!trooperId,
    })
}

// ─── Mutation Hooks ─────────────────────────────────────────────────

export function useCreateTrooper(): UseMutationResult<Trooper, Error, CreateTrooperParams> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params: CreateTrooperParams) =>
            createTrooper({ ...params, tenantId: params.tenantId ?? orgId }),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useUpdateTrooper(): UseMutationResult<Trooper, Error, UpdateTrooperParams> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params: UpdateTrooperParams) =>
            updateTrooper({ ...params, tenantId: params.tenantId ?? orgId }),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useDeleteTrooper(): UseMutationResult<{ success: boolean; message: string }, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id: string) => deleteTrooper(orgId, id),
        onSuccess: (_data, deletedId) => {
            // Clear stale session cache for the deleted trooper
            trooperSessionMap.delete(deletedId)
            clearTrooperLastSessionId(orgId, deletedId)
            queryClient.removeQueries({ queryKey: [...TROOPER_SESSIONS_KEY, orgId, deletedId] })
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useProvisionTrooper(): UseMutationResult<Trooper, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (trooperId: string) => provisionTrooper(orgId, trooperId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useSleepTrooper(): UseMutationResult<{ success: boolean; message: string }, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (trooperId: string) => sleepTrooper(orgId, trooperId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useWakeTrooper(): UseMutationResult<Trooper, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (trooperId: string) => wakeTrooper(orgId, trooperId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
            }, 500)
        },
    })
}

export function useCreateTrooperLink(): UseMutationResult<TrooperLink, Error, { sourceTrooperId: string; targetType: string; targetId: string; targetName?: string; linkType?: string; protocol?: string }> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params) => createTrooperLink(orgId, params),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPER_LINKS_KEY })
            }, 500)
        },
    })
}

export function useDeleteTrooperLink(): UseMutationResult<{ success: boolean; message: string }, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (linkId: string) => deleteTrooperLink(orgId, linkId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPER_LINKS_KEY })
            }, 500)
        },
    })
}

export function useBindChannel(): UseMutationResult<TrooperChannelBinding, Error, { trooperId: string; channelConfigId: string }> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: ({ trooperId, channelConfigId }) => bindChannel(orgId, trooperId, channelConfigId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPER_CHANNELS_KEY })
            }, 500)
        },
    })
}

export function useUnbindChannel(): UseMutationResult<{ success: boolean; message: string }, Error, { trooperId: string; channelConfigId: string }> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: ({ trooperId, channelConfigId }) => unbindChannel(orgId, trooperId, channelConfigId),
        onSuccess: () => {
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: TROOPER_CHANNELS_KEY })
            }, 500)
        },
    })
}

// ─── Trooper Session Hook ─────────────────────────────────────────

const TROOPER_SESSIONS_KEY = ['trooper-sessions']
const EMPTY_EVENTS: AgentStreamEvent[] = []
const TROOPER_LAST_SESSION_STORAGE_PREFIX = 'trooper:last-session'

function getTrooperLastSessionStorageKey(orgId: string, trooperId: string): string {
    return `${TROOPER_LAST_SESSION_STORAGE_PREFIX}:${orgId}:${trooperId}`
}

function readTrooperLastSessionId(orgId: string, trooperId: string): string | null {
    if (!orgId || !trooperId || typeof window === 'undefined') return null
    try {
        const value = window.localStorage.getItem(getTrooperLastSessionStorageKey(orgId, trooperId))
        return value && value.trim() ? value : null
    } catch {
        return null
    }
}

function writeTrooperLastSessionId(orgId: string, trooperId: string, sessionId: string): void {
    if (!orgId || !trooperId || !sessionId || typeof window === 'undefined') return
    try {
        window.localStorage.setItem(getTrooperLastSessionStorageKey(orgId, trooperId), sessionId)
    } catch {
        // ignore storage errors
    }
}

function clearTrooperLastSessionId(orgId: string, trooperId: string): void {
    if (!orgId || !trooperId || typeof window === 'undefined') return
    try {
        window.localStorage.removeItem(getTrooperLastSessionStorageKey(orgId, trooperId))
    } catch {
        // ignore storage errors
    }
}

function setTrooperStatusInCache(queryClient: QueryClient, trooperId: string, status: string) {
    queryClient.setQueriesData({ queryKey: TROOPERS_KEY }, (old: unknown) => {
        if (!old) return old
        if (typeof old !== 'object') return old
        const data = old as { id?: string; status?: string; troopers?: Trooper[]; total?: number }

        // Detail query shape: Trooper
        if (data.id === trooperId) {
            return { ...data, status }
        }

        // List query shape: { troopers, total }
        if (Array.isArray(data.troopers)) {
            return {
                ...data,
                troopers: data.troopers.map((t) => (t.id === trooperId ? { ...t, status } : t)),
            }
        }

        return old
    })
}

/**
 * Module-level map: trooperId → resolved sessionId.
 * Survives React component unmounts (navigation) without Zustand or React Query.
 * This is the source of truth for "which session is active for this trooper"
 * and avoids races where the DB query returns null during CQRS eventual consistency.
 */
const trooperSessionMap = new Map<string, string>()

/**
 * Loads the latest session for a trooper from the DB on mount,
 * and creates new sessions when the user sends the first message.
 * The sessionId is persisted across navigations via:
 * 1. Module-level trooperSessionMap (instant, never stale)
 * 2. React Query cache (survives GC, used as fallback)
 * 3. DB query (ground truth, eventual consistency)
 */
export function useTrooperSession(trooperId: string) {
    const orgId = useOrganizationId()
    const queryClient = useQueryClient()

    const startTrooperSession = useAgentSessionStore((s) => s.startTrooperSession)
    const sessions = useAgentSessionStore((s) => s.sessions)
    const persistedSessionId = React.useMemo(
        () => readTrooperLastSessionId(orgId, trooperId),
        [orgId, trooperId],
    )

    // Load the latest session for this trooper from the DB.
    // This survives navigation — React Query caches the result.
    const { data: latestSession } = useQuery({
        queryKey: [...TROOPER_SESSIONS_KEY, orgId, trooperId],
        queryFn: async () => {
            const resp = await listSessions({
                tenantId: orgId,
                trooperId,
                limit: 20,
            })
            const list = resp.sessions ?? []
            if (list.length === 0) return null
            const persisted = persistedSessionId
                ? list.find((s) => s.id === persistedSessionId)
                : undefined
            const withTurns = list.find((s) => (s.turnCount ?? 0) > 0)
            const first = persisted ?? withTurns ?? list[0]
            if (!first?.id) return null
            return { id: first.id, status: first.status }
        },
        enabled: !!orgId && !!trooperId,
        staleTime: 60_000, // 60s — module-level map handles fast lookups
    })

    // Local override: set when a new session is created during this mount.
    const [overrideSessionId, setOverrideSessionId] = React.useState<string | null>(null)

    // Reset override when trooper ID changes (navigating to different trooper)
    const prevTrooperId = React.useRef(trooperId)
    React.useEffect(() => {
        if (prevTrooperId.current !== trooperId) {
            prevTrooperId.current = trooperId
            setOverrideSessionId(null)
        }
    }, [trooperId])

    // Resolve session ID: override > module map > DB query.
    // Use persisted fallback only before the first DB fetch resolves.
    const mapSessionId = trooperSessionMap.get(trooperId) ?? null
    const hasResolvedLatestSession = latestSession !== undefined
    const sessionId = overrideSessionId
        ?? mapSessionId
        ?? latestSession?.id
        ?? (!hasResolvedLatestSession ? persistedSessionId : null)

    // Keep module map + local storage in sync with DB ground truth.
    React.useEffect(() => {
        if (latestSession?.id) {
            trooperSessionMap.set(trooperId, latestSession.id)
            writeTrooperLastSessionId(orgId, trooperId, latestSession.id)
            return
        }
        if (latestSession === null) {
            trooperSessionMap.delete(trooperId)
            clearTrooperLastSessionId(orgId, trooperId)
            setOverrideSessionId(null)
        }
    }, [orgId, trooperId, latestSession])

    React.useEffect(() => {
        if (!sessionId) return
        trooperSessionMap.set(trooperId, sessionId)
        writeTrooperLastSessionId(orgId, trooperId, sessionId)
    }, [orgId, trooperId, sessionId])

    const entry = sessionId ? sessions[sessionId] : undefined

    // Optimistic UI: as soon as a turn starts streaming, reflect running state
    // immediately in trooper badge/actions instead of waiting for poll cadence.
    React.useEffect(() => {
        if (!entry?.isStreaming) return
        setTrooperStatusInCache(queryClient, trooperId, 'running')
    }, [entry?.isStreaming, queryClient, trooperId])

    const startSession = React.useCallback(
        async (userInput: string) => {
            // Point the hook at the temp session ID IMMEDIATELY so the component
            // can see isStreaming/events while the backend processes the request.
            const tempId = `ws-pending-${trooperId}`
            setOverrideSessionId(tempId)

            // Immediate UI feedback while backend wakes/starts stream.
            setTrooperStatusInCache(queryClient, trooperId, 'waking')
            // Fetch latest status before send (sleeping/running may have changed).
            await queryClient.invalidateQueries({ queryKey: [...TROOPERS_KEY, orgId, trooperId] })
            await startTrooperSession(trooperId, orgId, userInput, queryClient, (resolvedId) => {
                setTrooperStatusInCache(queryClient, trooperId, 'running')
                // Persist in module-level map (survives navigation)
                trooperSessionMap.set(trooperId, resolvedId)
                writeTrooperLastSessionId(orgId, trooperId, resolvedId)
                setOverrideSessionId(resolvedId)
                // Update the React Query cache so navigating away and back picks up this session
                queryClient.setQueryData(
                    [...TROOPER_SESSIONS_KEY, orgId, trooperId],
                    { id: resolvedId, status: 2 /* RUNNING */ },
                )
            }).catch(console.error)
            // Refresh status/icon after backend wake/dispatch.
            await queryClient.invalidateQueries({ queryKey: [...TROOPERS_KEY, orgId, trooperId] })
            await queryClient.invalidateQueries({ queryKey: TROOPERS_KEY })
        },
        [trooperId, orgId, queryClient, startTrooperSession],
    )

    const resetSessionPointer = React.useCallback(() => {
        trooperSessionMap.delete(trooperId)
        clearTrooperLastSessionId(orgId, trooperId)
        setOverrideSessionId(null)
        queryClient.removeQueries({ queryKey: [...TROOPER_SESSIONS_KEY, orgId, trooperId] })
        void queryClient.invalidateQueries({ queryKey: [...TROOPER_SESSIONS_KEY, orgId, trooperId] })
    }, [orgId, queryClient, trooperId])

    return {
        sessionId,
        setSessionId: setOverrideSessionId,
        events: entry?.events ?? EMPTY_EVENTS,
        isStreaming: entry?.isStreaming ?? false,
        startSession,
        resetSessionPointer,
    }
}
