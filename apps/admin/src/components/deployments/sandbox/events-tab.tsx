import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { useSandboxContext, SandboxPicker } from './sandbox-context'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import { getApiBaseUrl } from '@/lib/api-url'

const { Badge, Collapsible, CollapsibleContent, CollapsibleTrigger } = ui

// ─── Types ──────────────────────────────────────────────────────────

interface SandboxEvent {
    id: string
    sandboxId: string
    type: string
    message: string
    metadata?: Record<string, unknown>
    durationMs?: number
    timestamp: string
}

type EventCategory = 'success' | 'info' | 'warning' | 'error'

// ─── Normalizer ─────────────────────────────────────────────────────

// normalizeEvent maps both SSE (snake_case Go JSON) and REST (camelCase proto JSON)
// into the canonical SandboxEvent shape used by the component.
// SSE sends: { id, sandbox_id, event_type, message, metadata, duration_ms, created_at }
// REST sends: { id, sandboxId, eventType, message, metadata, durationMs, createdAt }
function normalizeEvent(raw: Record<string, unknown>): SandboxEvent | null {
    const type = (raw.event_type ?? raw.eventType ?? raw.type) as string | undefined
    const timestamp = (raw.created_at ?? raw.createdAt ?? raw.timestamp) as string | undefined
    if (!type) return null

    return {
        id: String(raw.id ?? ''),
        sandboxId: (raw.sandbox_id ?? raw.sandboxId ?? '') as string,
        type,
        message: (raw.message ?? '') as string,
        metadata: (raw.metadata ?? undefined) as Record<string, unknown> | undefined,
        durationMs: (raw.duration_ms ?? raw.durationMs ?? undefined) as number | undefined,
        timestamp: timestamp ?? '',
    }
}

// ─── Helpers ────────────────────────────────────────────────────────

const EVENT_CATEGORIES: Record<string, EventCategory> = {
    'created': 'success',
    'ready': 'success',
    'exec.done': 'success',
    'exec.start': 'info',
    'shell.open': 'info',
    'shell.close': 'info',
    'file.write': 'info',
    'file.read': 'info',
    'port.expose': 'info',
    'port.unexpose': 'info',
    'cron.trigger': 'info',
    'webhook.trigger': 'info',
    'image.pull': 'info',
    'container.start': 'info',
    'idle.warning': 'warning',
    'error': 'error',
    'destroy.start': 'error',
    'destroyed': 'error',
}

const EVENT_LABELS: Record<string, string> = {
    'created': 'Sandbox Created',
    'ready': 'Sandbox Ready',
    'exec.start': 'Execution Started',
    'exec.done': 'Execution Completed',
    'shell.open': 'Shell Opened',
    'shell.close': 'Shell Closed',
    'file.write': 'File Written',
    'file.read': 'File Read',
    'port.expose': 'Port Exposed',
    'port.unexpose': 'Port Closed',
    'cron.trigger': 'Cron Triggered',
    'webhook.trigger': 'Webhook Triggered',
    'image.pull': 'Image Pulled',
    'container.start': 'Container Started',
    'idle.warning': 'Idle Warning',
    'error': 'Error',
    'destroy.start': 'Destroying',
    'destroyed': 'Destroyed',
}

function getEventLabel(type: string): string {
    return EVENT_LABELS[type] ?? type
}

const CATEGORY_STYLES: Record<EventCategory, { dot: string; border: string; icon: string }> = {
    success: { dot: 'bg-emerald-400', border: 'border-emerald-400/40', icon: 'heroicons:check-circle' },
    info: { dot: 'bg-brand-secondary-400', border: 'border-brand-secondary-400/40', icon: 'heroicons:information-circle' },
    warning: { dot: 'bg-amber-400', border: 'border-amber-400/40', icon: 'heroicons:exclamation-triangle' },
    error: { dot: 'bg-red-400', border: 'border-red-400/40', icon: 'heroicons:x-circle' },
}

const CATEGORY_TEXT: Record<EventCategory, string> = {
    success: 'text-emerald-300 light:text-emerald-600',
    info: 'text-brand-secondary-300',
    warning: 'text-amber-300 light:text-amber-700',
    error: 'text-red-300 light:text-red-600',
}

function getCategory(type: string): EventCategory {
    return EVENT_CATEGORIES[type] ?? 'info'
}

function formatTimestamp(ts: string): string {
    if (!ts) return '--'
    const d = new Date(ts)
    if (isNaN(d.getTime())) return '--'
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatDate(ts: string): string {
    if (!ts) return ''
    const d = new Date(ts)
    if (isNaN(d.getTime())) return ''
    return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

// ─── Grouping ───────────────────────────────────────────────────────

interface EventGroup {
    type: string
    category: EventCategory
    events: SandboxEvent[]
    firstTimestamp: string
    lastTimestamp: string
    key: string
}

function groupConsecutiveEvents(events: SandboxEvent[]): EventGroup[] {
    if (events.length === 0) return []
    const groups: EventGroup[] = []
    let current: EventGroup = {
        type: events[0].type,
        category: getCategory(events[0].type),
        events: [events[0]],
        firstTimestamp: events[0].timestamp,
        lastTimestamp: events[0].timestamp,
        key: events[0].id || `${events[0].timestamp}-0`,
    }

    for (let i = 1; i < events.length; i++) {
        const event = events[i]
        if (event.type === current.type) {
            current.events.push(event)
            current.lastTimestamp = event.timestamp
        } else {
            groups.push(current)
            current = {
                type: event.type,
                category: getCategory(event.type),
                events: [event],
                firstTimestamp: event.timestamp,
                lastTimestamp: event.timestamp,
                key: event.id || `${event.timestamp}-${i}`,
            }
        }
    }
    groups.push(current)
    return groups
}

const ALL_CATEGORIES: EventCategory[] = ['success', 'info', 'warning', 'error']

const CATEGORY_LABELS: Record<EventCategory, string> = {
    success: 'Success',
    info: 'Info',
    warning: 'Warning',
    error: 'Error',
}

const CATEGORY_BADGE_VARIANT: Record<EventCategory, 'success' | 'default' | 'warning' | 'destructive'> = {
    success: 'success',
    info: 'default',
    warning: 'warning',
    error: 'destructive',
}

// ─── SSE Hook ───────────────────────────────────────────────────────

function useSandboxEventsStream(sandboxId: string | undefined) {
    const [events, setEvents] = useState<SandboxEvent[]>([])
    const [isStreaming, setIsStreaming] = useState(false)
    const abortRef = useRef<AbortController | null>(null)
    const streamingRef = useRef(false)

    const start = useCallback(() => {
        if (!sandboxId || streamingRef.current) return

        streamingRef.current = true
        setIsStreaming(true)
        const baseUrl = getApiBaseUrl()
        const url = `${baseUrl}/v1/sandbox/${sandboxId}/events/stream`
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
                        if (currentEvent === 'error') {
                            currentEvent = ''
                            continue
                        }
                        currentEvent = ''
                        const jsonStr = line.slice(6).trim()
                        if (!jsonStr) continue

                        try {
                            const raw = JSON.parse(jsonStr) as Record<string, unknown>
                            const parsed = normalizeEvent(raw)
                            if (parsed) {
                                setEvents((prev) => {
                                    if (parsed.id && prev.some((e) => e.id === parsed.id)) return prev
                                    return [...prev, parsed]
                                })
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
                streamingRef.current = false
                setIsStreaming(false)
                abortRef.current = null
            }
        })()
    }, [sandboxId])

    const stop = useCallback(() => {
        abortRef.current?.abort()
        streamingRef.current = false
    }, [])

    const clear = useCallback(() => {
        setEvents([])
    }, [])

    useEffect(() => {
        return () => {
            abortRef.current?.abort()
            streamingRef.current = false
        }
    }, [])

    return { events, isStreaming, start, stop, clear }
}

// ─── Query Hook (REST fetch) ────────────────────────────────────────

function useSandboxEvents(sandboxId: string | undefined) {
    const [events, setEvents] = useState<SandboxEvent[]>([])
    const [isLoading, setIsLoading] = useState(false)

    useEffect(() => {
        if (!sandboxId) {
            setEvents([])
            return
        }

        setIsLoading(true)
        const baseUrl = getApiBaseUrl()
        fetch(`${baseUrl}/v1/sandbox/${sandboxId}/events`)
            .then((res) => {
                if (!res.ok) throw new Error(`${res.status}`)
                return res.json()
            })
            .then((data) => {
                const rawEvents = (data.events ?? []) as Record<string, unknown>[]
                setEvents(rawEvents.map(normalizeEvent).filter((e): e is SandboxEvent => e !== null))
            })
            .catch(() => {
                setEvents([])
            })
            .finally(() => setIsLoading(false))
    }, [sandboxId])

    return { events, isLoading }
}

// ─── Event Row Components ────────────────────────────────────────────

function SingleEventRow({ event }: { event: SandboxEvent }) {
    const [expanded, setExpanded] = useState(false)
    const category = getCategory(event.type)
    const style = CATEGORY_STYLES[category]
    const textColor = CATEGORY_TEXT[category]

    return (
        <div className="flex gap-4 group">
            {/* Left: timestamp */}
            <div className="w-20 shrink-0 text-right">
                <p className="text-xs text-white/40 light:text-black/40 font-mono">{formatTimestamp(event.timestamp)}</p>
                <p className="text-[10px] text-white/20 light:text-black/20 font-mono">{formatDate(event.timestamp)}</p>
            </div>

            {/* Timeline line + dot */}
            <div className="flex flex-col items-center">
                <div className={`w-2.5 h-2.5 rounded-full ${style.dot} shrink-0 mt-1`} />
                <div className="w-px flex-1 bg-brand-main-600 min-h-[16px]" />
            </div>

            {/* Right: content */}
            <div className={`flex-1 pb-2 border-l-2 ${style.border} pl-3 -ml-px`}>
                <div className="flex items-center gap-2">
                    <Iconify.Icon icon={style.icon} className={`size-4 ${textColor}`} />
                    <span className={`text-sm font-medium ${textColor}`}>{getEventLabel(event.type)}</span>
                    {event.durationMs != null && event.durationMs > 0 && (
                        <span className="text-[10px] bg-brand-main-700 text-white/50 light:text-black/50 rounded px-1.5 py-0.5">
                            {event.durationMs}ms
                        </span>
                    )}
                </div>
                {event.message && (
                    <p className="text-sm text-white/60 light:text-black/60 mt-0.5">{event.message}</p>
                )}
                {event.metadata && Object.keys(event.metadata).length > 0 && (
                    <button
                        onClick={() => setExpanded(!expanded)}
                        className="text-[11px] text-white/30 light:text-black/30 hover:text-white/50 light:hover:text-black/50 mt-1 flex items-center gap-1"
                    >
                        <Iconify.Icon
                            icon="heroicons:chevron-right"
                            className={`size-3 transition-transform ${expanded ? 'rotate-90' : ''}`}
                        />
                        metadata
                    </button>
                )}
                {expanded && event.metadata && (
                    <pre className="mt-1 text-[11px] text-white/40 light:text-black/40 bg-brand-main-900 border border-brand-main-700 rounded p-2 overflow-x-auto max-w-lg">
                        {JSON.stringify(event.metadata, null, 2)}
                    </pre>
                )}
            </div>
        </div>
    )
}

function InlineEventRow({ event }: { event: SandboxEvent }) {
    const [expanded, setExpanded] = useState(false)

    return (
        <div className="py-1 pl-1">
            <div className="flex items-center gap-2">
                <span className="text-[11px] text-white/30 light:text-black/30 font-mono shrink-0 w-16 text-right">
                    {formatTimestamp(event.timestamp)}
                </span>
                <span className="text-xs text-white/50 light:text-black/50 flex-1 min-w-0 truncate">
                    {event.message || getEventLabel(event.type)}
                </span>
                {event.durationMs != null && event.durationMs > 0 && (
                    <span className="text-[10px] bg-brand-main-700 text-white/40 light:text-black/40 rounded px-1 py-0.5 shrink-0">
                        {event.durationMs}ms
                    </span>
                )}
                {event.metadata && Object.keys(event.metadata).length > 0 && (
                    <button
                        onClick={() => setExpanded(!expanded)}
                        className="text-[10px] text-white/20 light:text-black/20 hover:text-white/40 light:hover:text-black/40 shrink-0"
                    >
                        <Iconify.Icon
                            icon="heroicons:chevron-right"
                            className={`size-3 transition-transform ${expanded ? 'rotate-90' : ''}`}
                        />
                    </button>
                )}
            </div>
            {expanded && event.metadata && (
                <pre className="mt-1 ml-[4.5rem] text-[10px] text-white/30 light:text-black/30 bg-brand-main-900 border border-brand-main-700 rounded p-1.5 overflow-x-auto max-w-sm">
                    {JSON.stringify(event.metadata, null, 2)}
                </pre>
            )}
        </div>
    )
}

function GroupedEventRow({ group }: { group: EventGroup }) {
    const style = CATEGORY_STYLES[group.category]
    const textColor = CATEGORY_TEXT[group.category]
    const firstEvent = group.events[0]

    return (
        <div className="flex gap-4 group">
            {/* Left: time range */}
            <div className="w-20 shrink-0 text-right">
                <p className="text-xs text-white/40 light:text-black/40 font-mono">{formatTimestamp(group.firstTimestamp)}</p>
                {group.firstTimestamp !== group.lastTimestamp && (
                    <p className="text-[10px] text-white/20 light:text-black/20 font-mono">→ {formatTimestamp(group.lastTimestamp)}</p>
                )}
            </div>

            {/* Timeline line + dot */}
            <div className="flex flex-col items-center">
                <div className={`w-2.5 h-2.5 rounded-full ${style.dot} shrink-0 mt-1`} />
                <div className="w-px flex-1 bg-brand-main-600 min-h-[16px]" />
            </div>

            {/* Right: collapsible content */}
            <div className={`flex-1 pb-2 border-l-2 ${style.border} pl-3 -ml-px`}>
                <Collapsible>
                    <CollapsibleTrigger className="w-full text-left cursor-pointer">
                        <div className="flex items-center gap-2">
                            <Iconify.Icon icon={style.icon} className={`size-4 ${textColor}`} />
                            <span className={`text-sm font-medium ${textColor}`}>
                                {group.events.length}x {getEventLabel(group.type)}
                            </span>
                            <span className="text-[10px] bg-brand-main-700 text-white/40 light:text-black/40 rounded-full px-1.5 py-0.5">
                                {group.events.length}
                            </span>
                            <Iconify.Icon
                                icon="heroicons:chevron-down"
                                className="size-3 text-white/30 light:text-black/30 transition-transform [[data-state=open]>&]:rotate-180"
                            />
                        </div>
                        {firstEvent.message && (
                            <p className="text-xs text-white/40 light:text-black/40 mt-0.5 truncate">{firstEvent.message}</p>
                        )}
                    </CollapsibleTrigger>
                    <CollapsibleContent>
                        <div className="mt-1 border-t border-brand-main-700 pt-1">
                            {group.events.map((event, idx) => (
                                <InlineEventRow key={event.id || `${event.timestamp}-${idx}`} event={event} />
                            ))}
                        </div>
                    </CollapsibleContent>
                </Collapsible>
            </div>
        </div>
    )
}

// ─── Main Component ─────────────────────────────────────────────────

export function EventsTab() {
    const { activeSandboxId: sandboxId } = useSandboxContext()

    const { events: fetchedEvents, isLoading } = useSandboxEvents(sandboxId)
    const { events: streamedEvents, isStreaming, start, stop, clear } = useSandboxEventsStream(sandboxId)
    const [activeFilters, setActiveFilters] = useState<Set<EventCategory>>(() => new Set(ALL_CATEGORIES))

    const toggleFilter = useCallback((category: EventCategory) => {
        setActiveFilters((prev) => {
            const next = new Set(prev)
            if (next.has(category)) {
                if (next.size === 1) return prev // prevent deselecting all
                next.delete(category)
            } else {
                next.add(category)
            }
            return next
        })
    }, [])

    const resetFilters = useCallback(() => {
        setActiveFilters(new Set(ALL_CATEGORIES))
    }, [])

    // Auto-start streaming when sandboxId changes
    useEffect(() => {
        if (sandboxId && !isStreaming) {
            start()
        }
        return () => {
            stop()
        }
    }, [sandboxId]) // eslint-disable-line react-hooks/exhaustive-deps

    // Merge fetched and streamed events, deduplicated by id
    const allEvents = useMemo(() => {
        const map = new Map<string, SandboxEvent>()
        for (const e of fetchedEvents) {
            map.set(e.id || `${e.timestamp}-${e.type}`, e)
        }
        for (const e of streamedEvents) {
            map.set(e.id || `${e.timestamp}-${e.type}`, e)
        }
        return Array.from(map.values()).sort(
            (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
        )
    }, [fetchedEvents, streamedEvents])

    // Category counts from unfiltered events
    const categoryCounts = useMemo(() => {
        const counts: Record<EventCategory, number> = { success: 0, info: 0, warning: 0, error: 0 }
        for (const e of allEvents) {
            counts[getCategory(e.type)]++
        }
        return counts
    }, [allEvents])

    // Filter → group pipeline
    const filteredEvents = useMemo(
        () => allEvents.filter((e) => activeFilters.has(getCategory(e.type))),
        [allEvents, activeFilters]
    )

    const eventGroups = useMemo(() => groupConsecutiveEvents(filteredEvents), [filteredEvents])

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxPicker />

                {/* Category filter chips */}
                <div className="flex items-center gap-1.5">
                    {ALL_CATEGORIES.map((cat) => (
                        <button
                            key={cat}
                            onClick={() => toggleFilter(cat)}
                            className={`transition-opacity ${activeFilters.has(cat) ? 'opacity-100' : 'opacity-30'}`}
                        >
                            <Badge variant={CATEGORY_BADGE_VARIANT[cat]} className="text-[10px] px-1.5 py-0 cursor-pointer select-none">
                                {CATEGORY_LABELS[cat]} {categoryCounts[cat]}
                            </Badge>
                        </button>
                    ))}
                </div>

                {isStreaming && (
                    <span className="flex items-center gap-1 text-xs text-brand-secondary-400">
                        <span className="w-1.5 h-1.5 bg-brand-secondary-500 rounded-full animate-pulse" />
                        Live
                    </span>
                )}

                <div className="flex-1" />

                {allEvents.length > 0 && (
                    <span className="text-[10px] text-white/30 light:text-black/30 font-mono">
                        {filteredEvents.length}/{allEvents.length} events, {eventGroups.length} groups
                    </span>
                )}

                {isStreaming ? (
                    <button
                        onClick={stop}
                        className="flex items-center gap-1 text-xs bg-red-500/20 text-red-400 light:text-red-600 border border-red-500/30 rounded px-2 py-1 hover:bg-red-500/30"
                    >
                        <Iconify.Icon icon="heroicons:stop-solid" className="size-3" />
                        Stop
                    </button>
                ) : (
                    <button
                        onClick={start}
                        disabled={!sandboxId}
                        className="flex items-center gap-1 text-xs bg-brand-secondary-500/20 text-brand-secondary-400 border border-brand-secondary-500/30 rounded px-2 py-1 hover:bg-brand-secondary-500/30 disabled:opacity-50"
                    >
                        <Iconify.Icon icon="heroicons:play-solid" className="size-3" />
                        Stream
                    </button>
                )}

                <button
                    onClick={clear}
                    className="text-xs text-white/50 light:text-black/50 hover:text-white/70 light:hover:text-black/70"
                >
                    Clear
                </button>
            </div>

            {/* Timeline */}
            <div className="flex-1 overflow-y-auto bg-brand-main-900 p-4">
                {isLoading ? (
                    <div className="flex items-center justify-center h-full text-white/30 light:text-black/30">
                        <Iconify.Icon icon="heroicons:arrow-path" className="size-5 animate-spin mr-2" />
                        Loading events...
                    </div>
                ) : !sandboxId ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:clock" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sandbox selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a sandbox to view events.
                        </p>
                    </div>
                ) : allEvents.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:clock" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No events recorded yet</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Events will appear as the sandbox runs.
                        </p>
                    </div>
                ) : filteredEvents.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-lg" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3.5">
                                <Iconify.Icon icon="heroicons:funnel" className="size-7 text-brand-secondary-400/70" />
                            </div>
                        </div>
                        <h3 className="text-sm font-medium text-white light:text-brand-main-50 mb-1">No events match filters</h3>
                        <button
                            onClick={resetFilters}
                            className="text-sm text-brand-secondary-400 hover:text-brand-secondary-300 mt-2"
                        >
                            Reset filters
                        </button>
                    </div>
                ) : (
                    <div className="space-y-0">
                        {eventGroups.map((group) =>
                            group.events.length === 1 ? (
                                <SingleEventRow key={group.key} event={group.events[0]} />
                            ) : (
                                <GroupedEventRow key={group.key} group={group} />
                            )
                        )}
                    </div>
                )}
            </div>
        </div>
    )
}
