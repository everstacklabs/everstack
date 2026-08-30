import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { consumeSSEStream, type AgentStreamEvent } from '@/lib/sse-utils'
import { getApiBaseUrl } from '@/lib/api-url'
import type { QueryClient } from '@tanstack/react-query'

const SESSIONS_QUERY_KEY = ['agent-sessions']

const MAX_EVENTS_PER_SESSION = 2000
const EXPOSED_URLS_STORAGE_PREFIX = 'agent:exposed-urls'
const SESSION_CACHE_DB_NAME = 'everstack-admin'
const SESSION_CACHE_STORE_NAME = 'agent-session-cache'
const SESSION_CACHE_VERSION = 2

function getExposedURLsStorageKey(sessionId: string): string {
  return `${EXPOSED_URLS_STORAGE_PREFIX}:${sessionId}`
}

export function readPersistedExposedURLs(
  sessionId: string,
): Record<number, string> {
  if (!sessionId || typeof window === 'undefined') return {}
  try {
    const raw = window.localStorage.getItem(getExposedURLsStorageKey(sessionId))
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Record<number, string> = {}
    for (const [k, v] of Object.entries(parsed)) {
      const port = Number(k)
      if (!Number.isFinite(port) || typeof v !== 'string' || !v) continue
      out[port] = v
    }
    return out
  } catch {
    return {}
  }
}

function persistExposedURLs(
  sessionId: string,
  urls: Record<number, string>,
): void {
  if (!sessionId || typeof window === 'undefined') return
  try {
    window.localStorage.setItem(
      getExposedURLsStorageKey(sessionId),
      JSON.stringify(urls),
    )
  } catch {
    // ignore storage errors
  }
}

type PersistedSessionSnapshot = {
  version: 2
  sessionId: string
  turnNumber: number
  events: AgentStreamEvent[]
  toolResultsCache: Record<string, ToolResultCacheEntry>
  exposedURLs: Record<number, string>
  updatedAt: number
}

let sessionCacheDbPromise: Promise<IDBDatabase> | null = null
const persistTimers = new Map<string, number>()

function openSessionCacheDb(): Promise<IDBDatabase> {
  if (typeof indexedDB === 'undefined') {
    return Promise.reject(new Error('indexedDB unavailable'))
  }
  if (sessionCacheDbPromise) return sessionCacheDbPromise

  sessionCacheDbPromise = new Promise((resolve, reject) => {
    const request = indexedDB.open(SESSION_CACHE_DB_NAME, SESSION_CACHE_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(SESSION_CACHE_STORE_NAME)) {
        db.createObjectStore(SESSION_CACHE_STORE_NAME, { keyPath: 'sessionId' })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () =>
      reject(request.error ?? new Error('failed to open indexeddb'))
  })

  return sessionCacheDbPromise
}

function writeSessionSnapshotToIndexedDB(
  snapshot: PersistedSessionSnapshot,
): void {
  if (typeof window === 'undefined') return
  void openSessionCacheDb()
    .then((db) => {
      const tx = db.transaction(SESSION_CACHE_STORE_NAME, 'readwrite')
      tx.objectStore(SESSION_CACHE_STORE_NAME).put(snapshot)
    })
    .catch(() => {
      // ignore browser storage failures
    })
}

function buildSnapshot(
  sessionId: string,
  entry: SessionEntry,
): PersistedSessionSnapshot {
  let turnNumber = 0
  for (const e of entry.events) {
    if (e.turnNumber > turnNumber) turnNumber = e.turnNumber
  }
  return {
    version: 2,
    sessionId,
    turnNumber,
    events: entry.events,
    toolResultsCache: entry.toolResultsCache,
    exposedURLs: entry.exposedURLs,
    updatedAt: Date.now(),
  }
}

function scheduleSessionSnapshotPersist(
  sessionId: string,
  entry: SessionEntry,
): void {
  if (!sessionId || typeof window === 'undefined') return
  const existing = persistTimers.get(sessionId)
  if (existing) {
    window.clearTimeout(existing)
  }
  const timer = window.setTimeout(() => {
    persistTimers.delete(sessionId)
    writeSessionSnapshotToIndexedDB(buildSnapshot(sessionId, entry))
  }, 250)
  persistTimers.set(sessionId, timer)
}

/** Immediately persist to IndexedDB — use for critical events like turn.start
 *  (which contains the user's input) and error events so they survive crashes. */
function immediateSessionSnapshotPersist(
  sessionId: string,
  entry: SessionEntry,
): void {
  if (!sessionId || typeof window === 'undefined') return
  // Cancel any pending debounced write — we're writing now.
  const existing = persistTimers.get(sessionId)
  if (existing) {
    window.clearTimeout(existing)
    persistTimers.delete(sessionId)
  }
  writeSessionSnapshotToIndexedDB(buildSnapshot(sessionId, entry))
}

/** Flush any pending debounced persist by writing the given entry immediately. */
function flushSessionSnapshotPersist(
  sessionId: string,
  entry: SessionEntry,
): void {
  if (!sessionId || typeof window === 'undefined') return
  const timer = persistTimers.get(sessionId)
  if (timer) {
    window.clearTimeout(timer)
    persistTimers.delete(sessionId)
  }
  writeSessionSnapshotToIndexedDB(buildSnapshot(sessionId, entry))
}

function readSessionSnapshotFromIndexedDB(
  sessionId: string,
): Promise<PersistedSessionSnapshot | null> {
  if (!sessionId || typeof window === 'undefined') return Promise.resolve(null)
  return openSessionCacheDb()
    .then(
      (db) =>
        new Promise<PersistedSessionSnapshot | null>((resolve, reject) => {
          const tx = db.transaction(SESSION_CACHE_STORE_NAME, 'readonly')
          const req = tx.objectStore(SESSION_CACHE_STORE_NAME).get(sessionId)
          req.onsuccess = () => {
            const value = req.result as PersistedSessionSnapshot | undefined
            resolve(value && value.version === 2 ? value : null)
          }
          req.onerror = () =>
            reject(req.error ?? new Error('failed to read indexeddb snapshot'))
        }),
    )
    .catch(() => null)
}

function deleteSessionSnapshotFromIndexedDB(sessionId: string): void {
  if (!sessionId || typeof window === 'undefined') return
  const existing = persistTimers.get(sessionId)
  if (existing) {
    window.clearTimeout(existing)
    persistTimers.delete(sessionId)
  }
  void openSessionCacheDb()
    .then((db) => {
      const tx = db.transaction(SESSION_CACHE_STORE_NAME, 'readwrite')
      tx.objectStore(SESSION_CACHE_STORE_NAME).delete(sessionId)
    })
    .catch(() => {
      // ignore browser storage failures
    })
}

// Wipe every cached session snapshot from IndexedDB. Called when the active
// organisation changes, so a stale snapshot from the previous tenant cannot
// be served to the new one. Session ids are UUIDs and unlikely to collide,
// but this is a tenant-isolation defense — we don't want any cached payload
// to outlive the org switch.
function clearAllSessionSnapshotsFromIndexedDB(): void {
  if (typeof window === 'undefined') return
  for (const t of persistTimers.values()) {
    window.clearTimeout(t)
  }
  persistTimers.clear()
  void openSessionCacheDb()
    .then((db) => {
      const tx = db.transaction(SESSION_CACHE_STORE_NAME, 'readwrite')
      tx.objectStore(SESSION_CACHE_STORE_NAME).clear()
    })
    .catch(() => {
      // ignore browser storage failures
    })
}

export interface ToolResultCacheEntry {
  toolResult?: string
  toolSuccess?: boolean
  toolDurationMs?: number
  sandboxId?: string
  sandboxExitCode?: number
  sandboxDurationMs?: number
  sandboxParentDurationMs?: number
}

interface SessionEntry {
  events: AgentStreamEvent[]
  toolResultsCache: Record<string, ToolResultCacheEntry>
  exposedURLs: Record<number, string>
  isStreaming: boolean
  subscribeRetries: number
  subscribeAbort: AbortController | null
  turnAbort: AbortController | null
  stoppedByUser: boolean
  lastActiveAt: number
  /** Highest turn number ever seen — survives event discards so nextTurn is always correct. */
  lastTurnNumber: number
  // Browser automation state
  browserStreamActive: boolean
  /** Sandbox session ID for the browser stream (e.g. "trp-{trooperId}") — differs from the agent session ID. */
  browserStreamSessionId: string | null
  /** Last screenshot base64 from browser.screenshot event — used as fallback when stream isn't working. */
  browserScreenshotBase64: string | null
  // Audio controls state
  ttsEnabled: boolean
  sttRecording: boolean
  selectedVoiceProfileId: string | null
}

/** Mutable dedup Sets stored outside Zustand to avoid O(N) cloning per event. */
const seenEventsMap = new Map<string, Set<string>>()

/** Build a deduplication key for an event.
 *  llm.chunk events are NEVER deduped — they are append-only text fragments.
 *  Using textDelta for dedup was fragile: short repeated chunks like "\n\n",
 *  ".", or common words would collide and be silently dropped, causing text
 *  corruption that looks like "jumbled" output. The SSE subscribe stream only
 *  sends live events (not replays), so dedup is unnecessary for chunks. */
function eventDedupeKey(e: AgentStreamEvent): string | null {
  if (e.type === 'llm.chunk') {
    // Never dedup chunks — each one is a unique text fragment.
    // Returning null tells appendEvent to skip the dedup check.
    return null
  }
  if (e.type === 'llm.start' || e.type === 'llm.end') {
    // Never dedup — multiple llm.start/end events occur per turn in the
    // tool loop (one per iteration). They share the same turnNumber so the
    // generic key would collapse them, breaking narration boundary detection.
    return null
  }
  if (e.type === 'tool_call.start' || e.type === 'tool_call.end') {
    return `${e.type}:${e.turnNumber}:${e.toolCallId}:${e.toolName}`
  }
  // Generic: combine type + turn + toolCallId + toolName + key data fields
  return `${e.type}:${e.turnNumber}:${e.toolCallId}:${e.toolName}:${e.finishReason}:${e.reviewId}:${e.userInputId}`
}

interface AgentSessionState {
  sessions: Record<string, SessionEntry>
  /** Tracks which sessions have completed IndexedDB hydration. */
  hydrationDone: Record<string, boolean>

  subscribe: (
    sessionId: string,
    sessionStatus: number,
    queryClient: QueryClient,
  ) => void
  startTurn: (
    sessionId: string,
    orgId: string,
    userInput: string,
    queryClient: QueryClient,
    options?: { enableWebSearch?: boolean; modelOverride?: string },
  ) => Promise<void>
  stopStream: (
    sessionId: string,
    orgId: string,
    queryClient: QueryClient,
  ) => Promise<void>
  clearEvents: (sessionId: string) => void
  /** Discard events for a specific turn number. Called when the turn is fully persisted. */
  discardTurnEvents: (sessionId: string, turnNumber: number) => void
  updateToolResultsCache: (
    sessionId: string,
    entries: Record<string, ToolResultCacheEntry>,
  ) => void
  removeSession: (sessionId: string) => void
  /** Reset every in-memory session and wipe the IndexedDB snapshot store.
   *  Called when the active organisation changes so cached state from the
   *  previous tenant cannot leak into the new one. */
  clearAll: () => void
  getActiveSessionIds: () => string[]
  toggleTTS: (sessionId: string) => void
  setVoiceProfile: (sessionId: string, profileId: string | null) => void
  /** Hydrate session from IndexedDB cache, filtering out events for already-persisted turns. */
  hydrateSessionFromCache: (
    sessionId: string,
    persistedTurnNumbers: Set<number>,
  ) => Promise<void>

  /**
   * @deprecated Phase 3 unified persistent agents into the standard steer flow.
   * Use startTurn instead. Will be removed in Phase 6.
   */
  startTrooperSession: (
    trooperId: string,
    tenantId: string,
    userInput: string,
    queryClient: QueryClient,
    onSessionResolved?: (sessionId: string) => void,
  ) => Promise<string | null>
  /**
   * @deprecated Phase 3 unified persistent agents into the standard steer flow.
   * Use runTurn instead. Will be removed in Phase 6.
   */
  runTrooperTurn: (
    sessionId: string,
    tenantId: string,
    userInput: string,
    queryClient: QueryClient,
  ) => Promise<void>
}

function createDefaultEntry(): SessionEntry {
  return {
    events: [],
    toolResultsCache: {},
    exposedURLs: {},
    isStreaming: false,
    subscribeRetries: 0,
    subscribeAbort: null,
    turnAbort: null,
    stoppedByUser: false,
    lastActiveAt: 0,
    lastTurnNumber: 0,
    browserStreamActive: false,
    browserStreamSessionId: null,
    browserScreenshotBase64: null,
    ttsEnabled: false,
    sttRecording: false,
    selectedVoiceProfileId: null,
  }
}

function ensureEntry(
  sessions: Record<string, SessionEntry>,
  id: string,
): Record<string, SessionEntry> {
  if (sessions[id]) return sessions
  return { ...sessions, [id]: createDefaultEntry() }
}

/** Append an event to the entry, skipping duplicates and pruning if needed. Returns a new entry (immutable).
 *  Dedup Set is stored in the module-level seenEventsMap to avoid cloning it into every Zustand snapshot. */
function appendEvent(
  sessionId: string,
  entry: SessionEntry,
  event: AgentStreamEvent,
): SessionEntry {
  const key = eventDedupeKey(event)
  // null key = no dedup (e.g. llm.chunk — always append)
  if (key !== null) {
    let seen = seenEventsMap.get(sessionId)
    if (!seen) {
      seen = new Set()
      seenEventsMap.set(sessionId, seen)
    }
    if (seen.has(key)) {
      return entry // duplicate — skip
    }
    seen.add(key)
  }
  const newEvents = pruneEvents([...entry.events, event])
  return { ...entry, events: newEvents }
}

/** Prune events to MAX_EVENTS_PER_SESSION, dropping older llm.chunk events first. */
function pruneEvents(events: AgentStreamEvent[]): AgentStreamEvent[] {
  if (events.length <= MAX_EVENTS_PER_SESSION) return events

  // Separate chunks from non-chunks
  const chunks: AgentStreamEvent[] = []
  const nonChunks: AgentStreamEvent[] = []
  for (const e of events) {
    if (e.type === 'llm.chunk') chunks.push(e)
    else nonChunks.push(e)
  }

  // If dropping all old chunks is enough, keep the newest chunks to fill remaining capacity
  const capacity = MAX_EVENTS_PER_SESSION - nonChunks.length
  if (capacity <= 0) {
    // Extremely unlikely — just keep the tail
    return events.slice(-MAX_EVENTS_PER_SESSION)
  }

  // Keep only the most recent chunks
  const keptChunks = chunks.slice(-capacity)

  // Re-interleave by original order (non-chunks retain position, chunks fill in)
  const keptChunkSet = new Set(keptChunks)
  const result: AgentStreamEvent[] = []
  for (const e of events) {
    if (e.type === 'llm.chunk') {
      if (keptChunkSet.has(e)) result.push(e)
    } else {
      result.push(e)
    }
  }
  return result
}

/**
 * Flush a batch of events into the Zustand store in a single set() call.
 * Shared by both the immediate and drip-feed paths.
 */
function flushEventBatch(
  sessionId: string,
  batch: AgentStreamEvent[],
  set: (fn: (s: AgentSessionState) => Partial<AgentSessionState>) => void,
  isLive = true,
) {
  if (batch.length === 0) return

  set((s) => {
    let entry = s.sessions[sessionId] ?? createDefaultEntry()
    let changed = false
    let exposedURLs = entry.exposedURLs

    let maxTurn = entry.lastTurnNumber
    let browserStreamActive = entry.browserStreamActive
    let browserStreamSessionId = entry.browserStreamSessionId
    let browserScreenshotBase64 = entry.browserScreenshotBase64
    for (const evt of batch) {
      const updated = appendEvent(sessionId, entry, evt)
      if (updated !== entry) {
        entry = updated
        changed = true
        if (evt.turnNumber > maxTurn) maxTurn = evt.turnNumber
        if (
          evt.type === 'sandbox.port.expose' &&
          evt.data?.url &&
          evt.data?.port
        ) {
          exposedURLs = {
            ...exposedURLs,
            [evt.data.port as number]: evt.data.url as string,
          }
        }
        // Track browser stream availability from live events only — cached/hydrated
        // events should not activate the browser panel since the sandbox may be stopped.
        if (isLive) {
          if (
            evt.type === 'browser.ready' &&
            !evt.data?.headless &&
            evt.data?.stream_available !== false
          ) {
            // Only activate the browser stream panel for headed mode.
            // Headless browsers have no streamer sidecar — the panel would
            // just show a perpetual "Connecting..." spinner.
            browserStreamActive = true
            browserStreamSessionId = evt.sessionId
          }
          if (evt.type === 'browser.close') {
            browserStreamActive = false
            browserStreamSessionId = null
          }
          // Capture screenshot base64 for fallback display in browser panel
          if (evt.type === 'browser.screenshot' && evt.data?.image_base64) {
            browserScreenshotBase64 = evt.data.image_base64 as string
          }
        }
      }
    }

    if (!changed) return s
    if (Object.keys(exposedURLs).length > 0) {
      persistExposedURLs(sessionId, exposedURLs)
    }
    const updatedEntry = {
      ...entry,
      exposedURLs,
      browserStreamActive,
      browserStreamSessionId,
      browserScreenshotBase64,
      lastActiveAt: Date.now(),
      lastTurnNumber: maxTurn,
    }
    scheduleSessionSnapshotPersist(sessionId, updatedEntry)

    return {
      sessions: {
        ...s.sessions,
        [sessionId]: updatedEntry,
      },
    }
  })
}

/**
 * RAF-based event batcher for smooth streaming text.
 *
 * Problem: The network delivers SSE chunks in bursts (many chunks in one
 * `reader.read()`). If we flush them all at once, text jumps in large blocks
 * instead of flowing smoothly.
 *
 * Solution: Split events into two lanes:
 *  - **Control events** (turn.start, tool_call.*, sandbox.*, etc.) are flushed
 *    immediately via queueMicrotask — they drive UI state transitions.
 *  - **llm.chunk events** are queued and drip-fed via requestAnimationFrame,
 *    releasing a small batch per frame (~60fps) for smooth text flow.
 *
 * The drip rate is tuned so that:
 *  - At typical LLM speeds (~30-80 tokens/sec), chunks flow 1-2 per frame
 *  - During bursts (reconnects, fast models), chunks are spread across frames
 *    instead of dumped all at once
 */
function createEventBatcher(
  sessionId: string,
  set: (fn: (s: AgentSessionState) => Partial<AgentSessionState>) => void,
) {
  // Immediate lane: control events batched per microtask (fast flush)
  let pendingControl: AgentStreamEvent[] = []
  let controlScheduled = false

  // Drip lane: chunk events released via RAF
  const chunkQueue: AgentStreamEvent[] = []
  let rafId: number | null = null

  // How many chunks to release per animation frame.
  // 2 chunks/frame at 60fps = ~120 tokens/sec visual throughput —
  // faster than any current LLM, so no visual backlog builds up.
  const CHUNKS_PER_FRAME = 2

  function flushControlEvents() {
    const batch = pendingControl
    pendingControl = []
    controlScheduled = false
    flushEventBatch(sessionId, batch, set)
  }

  function drainChunkQueue() {
    rafId = null
    if (chunkQueue.length === 0) return

    // Release up to CHUNKS_PER_FRAME chunks this frame
    const batch = chunkQueue.splice(0, CHUNKS_PER_FRAME)
    flushEventBatch(sessionId, batch, set)

    // If more chunks remain, schedule next frame
    if (chunkQueue.length > 0) {
      rafId = requestAnimationFrame(drainChunkQueue)
    }
  }

  function batch(event: AgentStreamEvent) {
    if (event.type === 'llm.chunk') {
      // Drip lane: queue and schedule RAF drain
      chunkQueue.push(event)
      if (rafId === null) {
        rafId = requestAnimationFrame(drainChunkQueue)
      }
    } else {
      // Preserve chronology: if there are pending chunk events that arrived
      // before this control event, flush them first so control events do not
      // visually jump ahead of earlier text deltas.
      if (chunkQueue.length > 0) {
        if (rafId !== null) {
          cancelAnimationFrame(rafId)
          rafId = null
        }
        const remaining = chunkQueue.splice(0)
        flushEventBatch(sessionId, remaining, set)
      }

      // On turn boundaries, flush any remaining chunks immediately
      // so they appear before the new turn starts. This prevents
      // stale chunks from turn N bleeding into turn N+1's display.
      if (
        (event.type === 'turn.start' || event.type === 'turn.end') &&
        chunkQueue.length > 0
      ) {
        if (rafId !== null) {
          cancelAnimationFrame(rafId)
          rafId = null
        }
        const remaining = chunkQueue.splice(0)
        flushEventBatch(sessionId, remaining, set)
      }

      // Control lane: flush immediately via microtask
      pendingControl.push(event)
      if (!controlScheduled) {
        controlScheduled = true
        queueMicrotask(flushControlEvents)
      }
    }
  }

  /**
   * Flush all pending events (both control and chunk queues) synchronously.
   * Call this when the stream ends abnormally (connection drop, error) to
   * ensure no events are lost in the RAF drip queue.
   */
  function flush() {
    // Flush pending control events
    if (pendingControl.length > 0) {
      const batch = pendingControl
      pendingControl = []
      controlScheduled = false
      flushEventBatch(sessionId, batch, set)
    }
    // Flush pending chunk events
    if (chunkQueue.length > 0) {
      if (rafId !== null) {
        cancelAnimationFrame(rafId)
        rafId = null
      }
      const remaining = chunkQueue.splice(0)
      flushEventBatch(sessionId, remaining, set)
    }
  }

  return Object.assign(batch, { flush })
}

// SessionStatus.RUNNING from the proto enum
const SESSION_STATUS_RUNNING = 2
const SESSION_STATUS_WAITING_FOR_INPUT = 3
const SESSION_STATUS_WAITING_FOR_APPROVAL = 4

export const useAgentSessionStore = create<AgentSessionState>()(
  devtools(
    (set, get) => ({
      sessions: {},
      hydrationDone: {},

      subscribe: (sessionId, sessionStatus, queryClient) => {
        // Allow subscribing for any active session status — channel
        // sessions spend most time in WAITING_FOR_INPUT between turns.
        const isActiveStatus =
          sessionStatus === SESSION_STATUS_RUNNING ||
          sessionStatus === SESSION_STATUS_WAITING_FOR_INPUT ||
          sessionStatus === SESSION_STATUS_WAITING_FOR_APPROVAL
        if (!isActiveStatus) return

        const state = get()
        const existing = state.sessions[sessionId]

        // Idempotent: already streaming or already has an active subscribe connection → skip
        if (
          existing?.isStreaming ||
          existing?.subscribeAbort ||
          existing?.turnAbort
        )
          return

        const controller = new AbortController()

        // Store the abort controller but do NOT set isStreaming yet.
        // We only set isStreaming: true once we confirm the SSE
        // connection is active (non-204 response). This prevents
        // UI flicker during polling retries.
        set((s) => {
          const sessions = ensureEntry(s.sessions, sessionId)
          return {
            sessions: {
              ...sessions,
              [sessionId]: {
                ...sessions[sessionId],
                subscribeAbort: controller,
                lastActiveAt: Date.now(),
              },
            },
          }
        })

        const baseUrl = getApiBaseUrl()
        const url = `${baseUrl}/v1/agents/sessions/${sessionId}/events/subscribe`

        ;(async () => {
          try {
            const response = await fetch(url, {
              method: 'GET',
              signal: controller.signal,
            })

            if (response.status === 204) {
              // Session runner not active — may be idle between turns
              // (e.g. channel session). Refetch data and retry subscribe
              // in case a new turn starts shortly.
              queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })

              // Retry subscribe after a delay — handles channel sessions
              // where a Discord/Slack message may re-launch the runner.
              const current = get().sessions[sessionId]
              if (current?.subscribeAbort !== controller) return
              const retryCount = current.subscribeRetries
              if (retryCount < 20 && !controller.signal.aborted) {
                set((s) => ({
                  sessions: {
                    ...s.sessions,
                    [sessionId]: {
                      ...(s.sessions[sessionId] ?? createDefaultEntry()),
                      isStreaming: false,
                      // Clear transient browser state — sandbox may be stopped
                      browserStreamActive: false,
                      browserStreamSessionId: null,
                      browserScreenshotBase64: null,
                      // Keep controller reference but track retries
                      subscribeAbort: controller,
                      subscribeRetries: retryCount + 1,
                    },
                  },
                }))
                await new Promise((r) => setTimeout(r, 3000))
                if (!controller.signal.aborted) {
                  const currentAfterDelay = get().sessions[sessionId]
                  if (currentAfterDelay?.subscribeAbort !== controller) return
                  // Clear subscribeAbort before re-invoking so
                  // the idempotency check allows re-entry.
                  set((s) => ({
                    sessions: {
                      ...s.sessions,
                      [sessionId]: {
                        ...(s.sessions[sessionId] ?? createDefaultEntry()),
                        subscribeAbort: null,
                      },
                    },
                  }))
                  get().subscribe(sessionId, sessionStatus, queryClient)
                }
              } else {
                const currentAfterRetry = get().sessions[sessionId]
                if (currentAfterRetry?.subscribeAbort !== controller) return
                set((s) => ({
                  sessions: {
                    ...s.sessions,
                    [sessionId]: {
                      ...(s.sessions[sessionId] ?? createDefaultEntry()),
                      isStreaming: false,
                      subscribeAbort: null,
                    },
                  },
                }))
              }
              return
            }

            if (!response.ok || !response.body) {
              const current = get().sessions[sessionId]
              if (current?.subscribeAbort !== controller) return
              set((s) => ({
                sessions: {
                  ...s.sessions,
                  [sessionId]: {
                    ...(s.sessions[sessionId] ?? createDefaultEntry()),
                    isStreaming: false,
                    subscribeAbort: null,
                  },
                },
              }))
              return
            }

            // Now we know the stream is active — mark isStreaming
            const current = get().sessions[sessionId]
            if (current?.subscribeAbort !== controller) return
            set((s) => ({
              sessions: {
                ...s.sessions,
                [sessionId]: {
                  ...(s.sessions[sessionId] ?? createDefaultEntry()),
                  isStreaming: true,
                  lastActiveAt: Date.now(),
                },
              },
            }))

            const batchEvent = createEventBatcher(sessionId, set)
            await consumeSSEStream(
              response,
              (event) => {
                const current = get().sessions[sessionId]
                if (current?.subscribeAbort !== controller) return
                batchEvent(event)
              },
              controller.signal,
              'subscribe',
            )
            // Normal completion — flush remaining events
            batchEvent.flush()
          } catch (err) {
            if ((err as Error).name !== 'AbortError') {
              console.error('[agent-session-store] Subscribe error:', err)
            }
          } finally {
            const entry = get().sessions[sessionId]
            const isCurrentSubscribe = entry?.subscribeAbort === controller
            if (!isCurrentSubscribe) return
            const stoppedByUser = entry?.stoppedByUser ?? false

            // Flush any pending debounced persist before clearing streaming state
            if (entry) flushSessionSnapshotPersist(sessionId, entry)

            set((s) => ({
              sessions: {
                ...s.sessions,
                [sessionId]: {
                  ...(s.sessions[sessionId] ?? createDefaultEntry()),
                  isStreaming: false,
                  subscribeAbort: null,
                  stoppedByUser: false,
                  subscribeRetries: 0,
                },
              },
            }))

            if (!stoppedByUser) {
              // Delay invalidation to give the async CQRS projection
              // time to persist turns to the read-model table before
              // the refetch happens.
              setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
              }, 1000)

              // Re-subscribe after a short delay — handles channel
              // sessions that may get a new turn from Discord/Slack.
              setTimeout(() => {
                const current = get().sessions[sessionId]
                if (
                  !current?.isStreaming &&
                  !current?.subscribeAbort &&
                  !current?.turnAbort &&
                  !current?.stoppedByUser
                ) {
                  get().subscribe(sessionId, sessionStatus, queryClient)
                }
              }, 2000)
            }
          }
        })()
      },

      startTurn: async (sessionId, orgId, userInput, queryClient, options) => {
        if (!sessionId || !orgId) return

        const state = get()
        const existing = state.sessions[sessionId]

        // Abort any active subscribe stream before starting a new turn
        existing?.subscribeAbort?.abort()

        const turnController = new AbortController()

        // Preserve existing events — never wipe history.
        // Reset dedup set so new turn events are not filtered as duplicates.
        seenEventsMap.delete(sessionId)

        // Compute the next turn number from existing events AND the persisted
        // lastTurnNumber counter. Events may have been discarded after a turn
        // was fully persisted, so relying solely on events would produce a
        // stale maxTurn (often 0), causing turnNumber collisions with previous
        // persisted turns and breaking currentTurnPersisted checks.
        const existingEvents = existing?.events ?? []
        let maxTurn = existing?.lastTurnNumber ?? 0
        for (const e of existingEvents) {
          if (e.turnNumber > maxTurn) maxTurn = e.turnNumber
        }
        const nextTurn = maxTurn + 1

        // Inject a synthetic turn.start event with the user's input immediately,
        // so it's visible in the timeline even if the backend never responds.
        const syntheticTurnStart: AgentStreamEvent = {
          type: 'turn.start',
          sessionId,
          turnNumber: nextTurn,
          textDelta: '',
          toolCallId: '',
          toolName: '',
          toolArgs: '',
          toolResult: '',
          toolSuccess: false,
          toolDurationMs: 0,
          finishReason: '',
          error: '',
          promptTokens: 0,
          completionTokens: 0,
          totalTokens: 0,
          reviewId: '',
          approvalAction: '',
          pendingToolCalls: [],
          sandboxId: '',
          sandboxExitCode: 0,
          sandboxDurationMs: 0,
          fallbackFromModel: '',
          fallbackToModel: '',
          fallbackAttempt: 0,
          userInputId: '',
          data: { user_input: userInput },
        }

        set((s) => {
          const sessions = ensureEntry(s.sessions, sessionId)
          const updatedEntry = {
            ...sessions[sessionId],
            events: [...existingEvents, syntheticTurnStart],
            isStreaming: true,
            subscribeAbort: null,
            turnAbort: turnController,
            stoppedByUser: false,
            lastActiveAt: Date.now(),
            lastTurnNumber: nextTurn,
          }
          // Immediately persist the user's message so it survives crashes/drops
          immediateSessionSnapshotPersist(sessionId, updatedEntry)
          return {
            sessions: {
              ...sessions,
              [sessionId]: updatedEntry,
            },
          }
        })

        const baseUrl = getApiBaseUrl()
        const url = `${baseUrl}/v1/agents/sessions/${sessionId}/turns/stream`

        // Hoist batcher so flush() is accessible in catch/finally
        let batchEvent: ReturnType<typeof createEventBatcher> | null = null

        try {
          const response = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              tenant_id: orgId,
              user_input: userInput,
              enable_streaming: true,
              enable_web_search: options?.enableWebSearch ?? false,
              ...(options?.modelOverride
                ? { model_override: options.modelOverride }
                : {}),
            }),
            signal: turnController.signal,
          })

          if (!response.ok || !response.body) {
            // Try to extract the actual error message from the response body
            // Connect-RPC returns JSON like { "code": "resource_exhausted", "message": "maximum turns exceeded" }
            let errorMessage: string
            try {
              const errorBody = await response.json()
              errorMessage =
                errorBody.message ||
                response.statusText ||
                `HTTP ${response.status}`
            } catch {
              errorMessage = response.statusText || `HTTP ${response.status}`
            }
            // Inject error event so the user sees what went wrong
            const errorEvent: AgentStreamEvent = {
              ...syntheticTurnStart,
              type: 'session.error',
              error: errorMessage,
            }
            set((s) => {
              const updatedEntry = {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                events: [...(s.sessions[sessionId]?.events ?? []), errorEvent],
                isStreaming: false,
                turnAbort: null,
              }
              immediateSessionSnapshotPersist(sessionId, updatedEntry)
              return {
                sessions: {
                  ...s.sessions,
                  [sessionId]: updatedEntry,
                },
              }
            })
            return
          }

          // Refresh agent data now that the backend has accepted the turn
          // (auto-wake may have changed lifecycle_status from sleeping → idle)
          queryClient.invalidateQueries({ queryKey: ['agents'] })

          batchEvent = createEventBatcher(sessionId, set)
          await consumeSSEStream(
            response,
            (event) => {
              const current = get().sessions[sessionId]
              if (current?.turnAbort !== turnController) return
              batchEvent!(event)
            },
            turnController.signal,
            'startTurn',
          )
        } catch (err) {
          if ((err as Error).name !== 'AbortError') {
            // Flush any pending events from the RAF drip queue before
            // injecting the error event. This ensures partial text chunks
            // streamed before the connection drop are preserved in the store.
            batchEvent?.flush()

            // Inject error event for network failures
            const errorEvent: AgentStreamEvent = {
              ...syntheticTurnStart,
              type: 'session.error',
              error: `Connection error: ${(err as Error).message}`,
            }
            set((s) => {
              const updatedEntry = {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                events: [...(s.sessions[sessionId]?.events ?? []), errorEvent],
                isStreaming: false,
                turnAbort: null,
              }
              immediateSessionSnapshotPersist(sessionId, updatedEntry)
              return {
                sessions: {
                  ...s.sessions,
                  [sessionId]: updatedEntry,
                },
              }
            })
          }
        } finally {
          // Flush any remaining events from the batcher's RAF queue
          batchEvent?.flush()

          const entry = get().sessions[sessionId]
          const isCurrentTurn = entry?.turnAbort === turnController
          if (!isCurrentTurn) return
          const stoppedByUser = entry?.stoppedByUser ?? false

          // Flush any pending debounced persist before updating state
          if (entry) flushSessionSnapshotPersist(sessionId, entry)

          set((s) => ({
            sessions: {
              ...s.sessions,
              [sessionId]: {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                isStreaming: false,
                turnAbort: null,
                stoppedByUser: false,
              },
            },
          }))

          if (!stoppedByUser) {
            // Delay invalidation to give the async CQRS projection
            // time to persist turns to the read-model table before
            // the refetch happens.
            setTimeout(() => {
              queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
              // Refresh agent data so lifecycle status (sleeping→idle) updates
              queryClient.invalidateQueries({ queryKey: ['agents'] })
            }, 1000)
          }
        }
      },

      stopStream: async (sessionId, orgId, queryClient) => {
        set((s) => {
          const entry = s.sessions[sessionId]
          if (!entry) return s

          entry.subscribeAbort?.abort()
          entry.turnAbort?.abort()

          return {
            sessions: {
              ...s.sessions,
              [sessionId]: {
                ...entry,
                stoppedByUser: true,
              },
            },
          }
        })

        if (orgId) {
          const baseUrl = getApiBaseUrl()
          const url = `${baseUrl}/v1/agents/sessions/${sessionId}/turns/stop`
          try {
            await fetch(url, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ tenant_id: orgId }),
            })
          } catch (err) {
            console.error(
              '[agent-session-store] stopStream interrupt error:',
              err,
            )
          }
        }

        // The turn projection is async; give it a moment, then refresh.
        setTimeout(() => {
          queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
        }, 1000)
      },

      clearEvents: (sessionId) => {
        seenEventsMap.delete(sessionId)
        deleteSessionSnapshotFromIndexedDB(sessionId)

        set((s) => {
          const entry = s.sessions[sessionId]
          if (!entry) return s
          return {
            sessions: {
              ...s.sessions,
              [sessionId]: { ...entry, events: [] },
            },
          }
        })
      },

      discardTurnEvents: (sessionId, turnNumber) => {
        set((s) => {
          const entry = s.sessions[sessionId]
          if (!entry) return s
          const kept = entry.events.filter(
            (e) => !e.turnNumber || e.turnNumber !== turnNumber,
          )
          if (kept.length === entry.events.length) return s
          if (kept.length === 0) {
            deleteSessionSnapshotFromIndexedDB(sessionId)
          } else {
            scheduleSessionSnapshotPersist(sessionId, {
              ...entry,
              events: kept,
            })
          }
          return {
            sessions: {
              ...s.sessions,
              [sessionId]: { ...entry, events: kept },
            },
          }
        })
      },

      updateToolResultsCache: (sessionId, entries) => {
        set((s) => {
          const entry = s.sessions[sessionId]
          if (!entry) return s
          const nextEntry = {
            ...entry,
            toolResultsCache: { ...entry.toolResultsCache, ...entries },
          }
          scheduleSessionSnapshotPersist(sessionId, nextEntry)
          return {
            sessions: {
              ...s.sessions,
              [sessionId]: nextEntry,
            },
          }
        })
      },

      removeSession: (sessionId) => {
        seenEventsMap.delete(sessionId)

        deleteSessionSnapshotFromIndexedDB(sessionId)
        set((s) => {
          const entry = s.sessions[sessionId]
          if (entry) {
            entry.subscribeAbort?.abort()
            entry.turnAbort?.abort()
          }
          const { [sessionId]: _, ...rest } = s.sessions
          return { sessions: rest }
        })
      },

      clearAll: () => {
        const sessions = get().sessions
        for (const entry of Object.values(sessions)) {
          entry.subscribeAbort?.abort()
          entry.turnAbort?.abort()
        }
        seenEventsMap.clear()
        clearAllSessionSnapshotsFromIndexedDB()
        set({ sessions: {}, hydrationDone: {} })
      },

      getActiveSessionIds: () => {
        const sessions = get().sessions
        return Object.keys(sessions).filter((id) => sessions[id].isStreaming)
      },

      toggleTTS: (sessionId) => {
        set((state) => {
          const sessions = ensureEntry(state.sessions, sessionId)
          const entry = sessions[sessionId]
          return {
            sessions: {
              ...sessions,
              [sessionId]: { ...entry, ttsEnabled: !entry.ttsEnabled },
            },
          }
        })
      },

      setVoiceProfile: (sessionId, profileId) => {
        set((state) => {
          const sessions = ensureEntry(state.sessions, sessionId)
          const entry = sessions[sessionId]
          return {
            sessions: {
              ...sessions,
              [sessionId]: { ...entry, selectedVoiceProfileId: profileId },
            },
          }
        })
      },

      hydrateSessionFromCache: async (sessionId, persistedTurnNumbers) => {
        if (!sessionId) {
          set((s) => ({
            hydrationDone: { ...s.hydrationDone, [sessionId]: true },
          }))
          return
        }

        const snapshot = await readSessionSnapshotFromIndexedDB(sessionId)

        set((s) => {
          const existing = s.sessions[sessionId]

          // Never clobber an active/streaming session entry
          if (existing?.isStreaming || existing?.turnAbort) {
            return { hydrationDone: { ...s.hydrationDone, [sessionId]: true } }
          }

          // No cached data — just mark done
          if (!snapshot || !snapshot.events?.length) {
            return { hydrationDone: { ...s.hydrationDone, [sessionId]: true } }
          }

          // If the cached turn is already persisted by the backend, discard entirely
          if (
            snapshot.turnNumber > 0 &&
            persistedTurnNumbers.has(snapshot.turnNumber)
          ) {
            deleteSessionSnapshotFromIndexedDB(sessionId)
            return { hydrationDone: { ...s.hydrationDone, [sessionId]: true } }
          }

          // Filter out any events for turns that are already persisted
          const freshEvents = snapshot.events.filter(
            (e) => !e.turnNumber || !persistedTurnNumbers.has(e.turnNumber),
          )

          if (freshEvents.length === 0) {
            deleteSessionSnapshotFromIndexedDB(sessionId)
            return { hydrationDone: { ...s.hydrationDone, [sessionId]: true } }
          }

          // In-memory events take precedence if they exist
          if (existing && existing.events.length >= freshEvents.length) {
            return { hydrationDone: { ...s.hydrationDone, [sessionId]: true } }
          }

          const next = {
            ...(existing ?? createDefaultEntry()),
            events: freshEvents,
            toolResultsCache: {
              ...(existing?.toolResultsCache ?? {}),
              ...(snapshot.toolResultsCache ?? {}),
            },
            exposedURLs: {
              ...(existing?.exposedURLs ?? {}),
              ...(snapshot.exposedURLs ?? {}),
            },
            lastActiveAt: snapshot.updatedAt ?? Date.now(),
          }

          return {
            sessions: { ...s.sessions, [sessionId]: next },
            hydrationDone: { ...s.hydrationDone, [sessionId]: true },
          }
        })
      },

      // @deprecated Phase 3 unified persistent agents into the standard steer flow.
      // Use startTurn instead. Will be removed in Phase 6.
      startTrooperSession: async (
        trooperId,
        tenantId,
        userInput,
        queryClient,
        onSessionResolved,
      ) => {
        const { createTrooperSessionStream } = await import('@/server/troopers')
        const turnController = new AbortController()

        // We'll use a temp session ID until we parse it from SSE
        const tempId = `ws-pending-${trooperId}`
        seenEventsMap.delete(tempId)

        // Inject a synthetic turn.start so the user's message is visible
        // immediately in the timeline (same pattern as startTurn).
        const syntheticTurnStart: AgentStreamEvent = {
          type: 'turn.start',
          sessionId: tempId,
          turnNumber: 1,
          textDelta: '',
          toolCallId: '',
          toolName: '',
          toolArgs: '',
          toolResult: '',
          toolSuccess: false,
          toolDurationMs: 0,
          finishReason: '',
          error: '',
          promptTokens: 0,
          completionTokens: 0,
          totalTokens: 0,
          reviewId: '',
          approvalAction: '',
          pendingToolCalls: [],
          sandboxId: '',
          sandboxExitCode: 0,
          sandboxDurationMs: 0,
          fallbackFromModel: '',
          fallbackToModel: '',
          fallbackAttempt: 0,
          userInputId: '',
          data: { user_input: userInput },
        }

        set((s) => {
          const sessions = ensureEntry(s.sessions, tempId)
          const updatedEntry = {
            ...createDefaultEntry(),
            events: [syntheticTurnStart],
            isStreaming: true,
            turnAbort: turnController,
            lastActiveAt: Date.now(),
          }
          immediateSessionSnapshotPersist(tempId, updatedEntry)
          return {
            sessions: {
              ...sessions,
              [tempId]: updatedEntry,
            },
          }
        })

        let resolvedSessionId: string | null = null
        let activeBatcher: ReturnType<typeof createEventBatcher> | null = null

        try {
          const response = await createTrooperSessionStream(
            tenantId,
            trooperId,
            userInput,
          )
          if (!response.ok || !response.body) {
            set((s) => {
              const { [tempId]: _, ...rest } = s.sessions
              return { sessions: rest }
            })
            return null
          }

          activeBatcher = createEventBatcher(tempId, set)
          await consumeSSEStream(
            response,
            (event) => {
              // Extract session ID from session.start event
              if (event.type === 'session.start' && event.sessionId) {
                resolvedSessionId = event.sessionId
                // Migrate temp entry to real session ID
                set((s) => {
                  const entry = s.sessions[tempId]
                  if (!entry) return s
                  const { [tempId]: _, ...rest } = s.sessions
                  seenEventsMap.set(
                    resolvedSessionId!,
                    seenEventsMap.get(tempId) ?? new Set(),
                  )
                  seenEventsMap.delete(tempId)

                  return { sessions: { ...rest, [resolvedSessionId!]: entry } }
                })
                // Switch batcher to target the real session ID
                activeBatcher = createEventBatcher(resolvedSessionId, set)
                // Notify caller immediately so UI can mount SessionTimeline
                // while streaming is still in progress.
                onSessionResolved?.(resolvedSessionId)
              }
              activeBatcher!(event)
            },
            turnController.signal,
            'startTrooperSession',
          )
          activeBatcher?.flush()
        } catch (err) {
          activeBatcher?.flush()
          if ((err as Error).name !== 'AbortError') {
            console.error('startTrooperSession error:', err)
          }
        } finally {
          activeBatcher?.flush()
          const finalId = resolvedSessionId ?? tempId
          set((s) => {
            const entry = s.sessions[finalId]
            if (!entry) return s
            return {
              sessions: {
                ...s.sessions,
                [finalId]: { ...entry, isStreaming: false, turnAbort: null },
              },
            }
          })
          // Clean up temp entry if still around
          if (resolvedSessionId && resolvedSessionId !== tempId) {
            set((s) => {
              const { [tempId]: _, ...rest } = s.sessions
              return { sessions: rest }
            })
          }
          setTimeout(() => {
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
          }, 1000)
        }
        return resolvedSessionId
      },

      // @deprecated Phase 3 unified persistent agents into the standard steer flow.
      // Use runTurn instead. Will be removed in Phase 6.
      runTrooperTurn: async (sessionId, tenantId, userInput, queryClient) => {
        if (!sessionId || !tenantId) return

        const existing = get().sessions[sessionId]
        existing?.subscribeAbort?.abort()

        const turnController = new AbortController()
        // Reset dedup set for new turn, but preserve existing events
        seenEventsMap.delete(sessionId)

        const existingEvents = existing?.events ?? []
        let maxTurn = 0
        for (const e of existingEvents) {
          if (e.turnNumber > maxTurn) maxTurn = e.turnNumber
        }
        const nextTurn = maxTurn + 1

        const syntheticTurnStart: AgentStreamEvent = {
          type: 'turn.start',
          sessionId,
          turnNumber: nextTurn,
          textDelta: '',
          toolCallId: '',
          toolName: '',
          toolArgs: '',
          toolResult: '',
          toolSuccess: false,
          toolDurationMs: 0,
          finishReason: '',
          error: '',
          promptTokens: 0,
          completionTokens: 0,
          totalTokens: 0,
          reviewId: '',
          approvalAction: '',
          pendingToolCalls: [],
          sandboxId: '',
          sandboxExitCode: 0,
          sandboxDurationMs: 0,
          fallbackFromModel: '',
          fallbackToModel: '',
          fallbackAttempt: 0,
          userInputId: '',
          data: { user_input: userInput },
        }

        set((s) => {
          const sessions = ensureEntry(s.sessions, sessionId)
          const updatedEntry = {
            ...(sessions[sessionId] ?? createDefaultEntry()),
            events: [...existingEvents, syntheticTurnStart],
            isStreaming: true,
            subscribeAbort: null,
            turnAbort: turnController,
            stoppedByUser: false,
            lastActiveAt: Date.now(),
          }
          immediateSessionSnapshotPersist(sessionId, updatedEntry)
          return {
            sessions: {
              ...sessions,
              [sessionId]: updatedEntry,
            },
          }
        })

        let batchEvent: ReturnType<typeof createEventBatcher> | null = null

        try {
          const { steerTrooperSessionStream } =
            await import('@/server/troopers')
          const response = await steerTrooperSessionStream(
            tenantId,
            sessionId,
            userInput,
          )
          if (!response.ok || !response.body) {
            const statusText = response.statusText || `HTTP ${response.status}`
            console.error(
              '[agent-session-store] trooper steer bad response:',
              response.status,
            )
            const errorEvent: AgentStreamEvent = {
              ...syntheticTurnStart,
              type: 'session.error',
              error: `Request failed: ${statusText}`,
            }
            set((s) => {
              const updatedEntry = {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                events: [...(s.sessions[sessionId]?.events ?? []), errorEvent],
                isStreaming: false,
                turnAbort: null,
              }
              immediateSessionSnapshotPersist(sessionId, updatedEntry)
              return {
                sessions: {
                  ...s.sessions,
                  [sessionId]: updatedEntry,
                },
              }
            })
            return
          }

          batchEvent = createEventBatcher(sessionId, set)
          await consumeSSEStream(
            response,
            (event) => {
              const current = get().sessions[sessionId]
              if (current?.turnAbort !== turnController) return
              batchEvent!(event)
            },
            turnController.signal,
            'runTrooperTurn',
          )
        } catch (err) {
          batchEvent?.flush()
          if ((err as Error).name !== 'AbortError') {
            console.error('[agent-session-store] runTrooperTurn error:', err)
            const errorEvent: AgentStreamEvent = {
              ...syntheticTurnStart,
              type: 'session.error',
              error: `Connection error: ${(err as Error).message}`,
            }
            set((s) => {
              const updatedEntry = {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                events: [...(s.sessions[sessionId]?.events ?? []), errorEvent],
                isStreaming: false,
                turnAbort: null,
              }
              immediateSessionSnapshotPersist(sessionId, updatedEntry)
              return {
                sessions: {
                  ...s.sessions,
                  [sessionId]: updatedEntry,
                },
              }
            })
          }
        } finally {
          batchEvent?.flush()
          const entry = get().sessions[sessionId]
          if (entry?.turnAbort !== turnController) return

          // Flush any pending debounced persist before clearing streaming state
          if (entry) flushSessionSnapshotPersist(sessionId, entry)

          set((s) => ({
            sessions: {
              ...s.sessions,
              [sessionId]: {
                ...(s.sessions[sessionId] ?? createDefaultEntry()),
                isStreaming: false,
                turnAbort: null,
                stoppedByUser: false,
              },
            },
          }))
          setTimeout(() => {
            queryClient.invalidateQueries({ queryKey: SESSIONS_QUERY_KEY })
          }, 1000)
        }
      },
    }),
    { name: 'agent-session-store' },
  ),
)
