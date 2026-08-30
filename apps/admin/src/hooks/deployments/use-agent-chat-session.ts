import * as React from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth/use-auth'
import { createSession, listSessions } from '@/server/agents'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import type { AgentStreamEvent } from '@/hooks/deployments/use-agents'

const AGENT_CHAT_SESSIONS_KEY = ['agent-chat-sessions']
const EMPTY_EVENTS: AgentStreamEvent[] = []

// Sentinel: when set as override, means "user wants a blank new chat"
const NEW_SESSION_SENTINEL = '__new__'

// ── localStorage helpers ────────────────────────────────────────────

const STORAGE_PREFIX = 'agent:last-session'

function storageKey(orgId: string, agentId: string): string {
    return `${STORAGE_PREFIX}:${orgId}:${agentId}`
}

function readPersistedSessionId(orgId: string, agentId: string): string | null {
    if (!orgId || !agentId || typeof window === 'undefined') return null
    try {
        const v = window.localStorage.getItem(storageKey(orgId, agentId))
        return v && v.trim() ? v : null
    } catch {
        return null
    }
}

function writePersistedSessionId(orgId: string, agentId: string, sessionId: string): void {
    if (!orgId || !agentId || !sessionId || typeof window === 'undefined') return
    try {
        window.localStorage.setItem(storageKey(orgId, agentId), sessionId)
    } catch {
        // ignore
    }
}

function clearPersistedSessionId(orgId: string, agentId: string): void {
    if (!orgId || !agentId || typeof window === 'undefined') return
    try {
        window.localStorage.removeItem(storageKey(orgId, agentId))
    } catch {
        // ignore
    }
}

// ── Shared session override (shared across all hook instances) ──────

/**
 * Module-level map storing the current session override per agent.
 * Values can be:
 *   - a real session ID (after create/switch)
 *   - NEW_SESSION_SENTINEL (user wants blank chat)
 *   - absent (no override, use resolution chain)
 */
const agentSessionOverrides = new Map<string, string>()

/** Module-level session map for navigation persistence */
const agentChatSessionMap = new Map<string, string>()

/** Listeners that get notified when overrides change */
const overrideListeners = new Set<() => void>()

function notifyOverrideChange() {
    for (const listener of overrideListeners) {
        listener()
    }
}

function setOverride(agentId: string, value: string | null) {
    if (value === null) {
        agentSessionOverrides.delete(agentId)
    } else {
        agentSessionOverrides.set(agentId, value)
    }
    notifyOverrideChange()
}

/** Subscribe to override changes — returns unsubscribe function */
function useOverrideVersion(): number {
    const [version, setVersion] = React.useState(0)
    React.useEffect(() => {
        const listener = () => setVersion((v) => v + 1)
        overrideListeners.add(listener)
        return () => { overrideListeners.delete(listener) }
    }, [])
    return version
}

// ── Hook ────────────────────────────────────────────────────────────

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

/**
 * Manages which chat session is active for a given persistent agent.
 *
 * Session resolution priority:
 *  1. Shared override (set when user creates/switches/resets session)
 *  2. Module-level agentChatSessionMap (survives navigation)
 *  3. agent.primarySessionId (from API — persistent agents only)
 *  4. Most recent session with turns (from listSessions query)
 *  5. localStorage fallback (before first DB fetch resolves)
 *
 * The override is shared across all hook instances via a module-level map
 * with a listener pattern, so both the layout and chat route stay in sync.
 */
export function useAgentChatSession(agentId: string, primarySessionId?: string) {
    const orgId = useOrganizationId()
    const queryClient = useQueryClient()

    // Subscribe to shared override changes
    useOverrideVersion()

    const startTurn = useAgentSessionStore((s) => s.startTurn)
    const sessions = useAgentSessionStore((s) => s.sessions)
    const persistedSessionId = React.useMemo(
        () => readPersistedSessionId(orgId, agentId),
        [orgId, agentId],
    )

    // Load the latest session for this agent from DB.
    const { data: latestSession } = useQuery({
        queryKey: [...AGENT_CHAT_SESSIONS_KEY, orgId, agentId],
        queryFn: async () => {
            const resp = await listSessions({
                tenantId: orgId,
                agentId,
                limit: 20,
            })
            const list = resp.sessions ?? []
            if (list.length === 0) return null

            // Resolution: persisted > most recent with turns > primary > first
            // Sessions are sorted by updated_at desc from the API, so the first
            // session with turns is the most recently active one.
            const persisted = persistedSessionId
                ? list.find((s) => s.id === persistedSessionId)
                : undefined
            const withTurns = list.find((s) => (s.turnCount ?? 0) > 0)
            const primary = primarySessionId
                ? list.find((s) => s.id === primarySessionId)
                : undefined
            const first = persisted ?? withTurns ?? primary ?? list[0]
            if (!first?.id) return null
            return { id: first.id, status: first.status }
        },
        enabled: !!orgId && !!agentId,
        staleTime: 60_000,
    })

    // Reset override when agent ID changes (navigating to different agent).
    // Skip when transitioning from empty → real (initial load / lazy resolution)
    // to avoid clearing overrides set by switchSession/resetSessionPointer
    // before the agentId was resolved.
    const prevAgentId = React.useRef(agentId)
    React.useEffect(() => {
        const prev = prevAgentId.current
        if (prev !== agentId) {
            prevAgentId.current = agentId
            // Only reset when switching between two real agent IDs
            if (prev && agentId) {
                setOverride(agentId, null)
            }
        }
    }, [agentId])

    // Read shared override
    const overrideSessionId = agentSessionOverrides.get(agentId) ?? null

    // Resolve session ID: override > module map > localStorage > DB query > primarySessionId.
    // NEW_SESSION_SENTINEL means "user clicked New Session — show blank chat."
    // primarySessionId is the initial session created at provision time — it's only used as a
    // last resort since the user may have created newer sessions that should take priority.
    const isNewSessionMode = overrideSessionId === NEW_SESSION_SENTINEL
    const mapSessionId = agentChatSessionMap.get(agentId) ?? null
    const hasResolvedLatestSession = latestSession !== undefined
    const sessionId = isNewSessionMode
        ? null
        : (overrideSessionId
            ?? mapSessionId
            ?? latestSession?.id
            ?? (!hasResolvedLatestSession ? persistedSessionId : null)
            ?? (primarySessionId || null))

    // Keep module map + localStorage in sync with DB ground truth.
    // Skip when user explicitly requested a new blank chat.
    React.useEffect(() => {
        if (isNewSessionMode) return
        if (latestSession?.id) {
            agentChatSessionMap.set(agentId, latestSession.id)
            writePersistedSessionId(orgId, agentId, latestSession.id)
            return
        }
        if (latestSession === null) {
            agentChatSessionMap.delete(agentId)
            clearPersistedSessionId(orgId, agentId)
        }
    }, [orgId, agentId, latestSession, isNewSessionMode])

    React.useEffect(() => {
        if (!sessionId || isNewSessionMode) return
        agentChatSessionMap.set(agentId, sessionId)
        writePersistedSessionId(orgId, agentId, sessionId)
    }, [orgId, agentId, sessionId, isNewSessionMode])

    const entry = sessionId ? sessions[sessionId] : undefined

    /**
     * Start a new session (or reuse existing) and send the first message.
     *
     * Agent sessions use two-step creation:
     *  1. createSession({ agentId }) → returns sessionId
     *  2. store.startTurn(sessionId, orgId, userInput) → starts SSE stream
     */
    const startSession = React.useCallback(
        async (userInput: string, options?: { modelOverride?: string }) => {
            // Step 1: Create a new session via RPC.
            const resp = await createSession({ tenantId: orgId, agentId })
            const newSessionId = resp.session?.id
            if (!newSessionId) return

            // Point all hook instances at the new session immediately.
            setOverride(agentId, newSessionId)
            agentChatSessionMap.set(agentId, newSessionId)
            writePersistedSessionId(orgId, agentId, newSessionId)

            // Update React Query cache so navigating away and back picks this up.
            queryClient.setQueryData(
                [...AGENT_CHAT_SESSIONS_KEY, orgId, agentId],
                { id: newSessionId, status: 2 /* RUNNING */ },
            )

            // Step 2: Start the turn (injects synthetic turn.start + streams SSE).
            await startTurn(newSessionId, orgId, userInput, queryClient, options)
        },
        [agentId, orgId, queryClient, startTurn],
    )

    /**
     * Send a message in the current session (new turn, not new session).
     */
    const sendMessage = React.useCallback(
        async (userInput: string, options?: { modelOverride?: string }) => {
            if (!sessionId || !orgId) return
            await startTurn(sessionId, orgId, userInput, queryClient, options)
        },
        [sessionId, orgId, queryClient, startTurn],
    )

    const switchSession = React.useCallback(
        (newSessionId: string) => {
            setOverride(agentId, newSessionId)
            agentChatSessionMap.set(agentId, newSessionId)
            writePersistedSessionId(orgId, agentId, newSessionId)
        },
        [orgId, agentId],
    )

    const resetSessionPointer = React.useCallback(() => {
        agentChatSessionMap.delete(agentId)
        clearPersistedSessionId(orgId, agentId)
        setOverride(agentId, NEW_SESSION_SENTINEL)
    }, [orgId, agentId])

    return {
        sessionId,
        startSession,
        sendMessage,
        switchSession,
        resetSessionPointer,
        isStreaming: entry?.isStreaming ?? false,
        events: entry?.events ?? EMPTY_EVENTS,
    }
}
