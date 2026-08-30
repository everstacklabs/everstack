import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ui, Iconify } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import type { TraceLogRecord } from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import { getTraceLogs } from '@/server/traces'

const { Badge } = ui

interface TraceLogsProps {
    traceId: string
    sessionId?: string
    selectedSpanId?: string
}

// Severity -> muted chip styling (matches the trace-tree badge palette).
function severityStyle(severity: string): { bg: string; text: string; border: string } {
    switch (severity.toUpperCase()) {
        case 'FATAL':
        case 'ERROR':
            return { bg: 'bg-rose-500/15', text: 'text-rose-300', border: 'border-rose-500/25' }
        case 'WARN':
        case 'WARNING':
            return { bg: 'bg-amber-500/15', text: 'text-amber-300', border: 'border-amber-500/25' }
        case 'INFO':
            return { bg: 'bg-sky-500/15', text: 'text-sky-300', border: 'border-sky-500/25' }
        case 'DEBUG':
            return { bg: 'bg-slate-500/15', text: 'text-slate-300', border: 'border-slate-500/25' }
        default:
            return { bg: 'bg-zinc-500/15', text: 'text-zinc-300', border: 'border-zinc-500/25' }
    }
}

function tsToDate(ts?: { seconds: bigint; nanos: number }): Date | null {
    if (!ts) return null
    return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000))
}

function formatTime(d: Date | null): string {
    if (!d) return '—'
    const hh = String(d.getHours()).padStart(2, '0')
    const mm = String(d.getMinutes()).padStart(2, '0')
    const ss = String(d.getSeconds()).padStart(2, '0')
    const ms = String(d.getMilliseconds()).padStart(3, '0')
    return `${hh}:${mm}:${ss}.${ms}`
}

// The event identity isn't a single canonical field across SDKs — coding agents
// put it in an attribute, the gateway in `event`, OTel in the scope. Coalesce.
function eventLabel(r: TraceLogRecord): string {
    const a = r.attributes ?? {}
    const fromAttr = a['event'] || a['log_event'] || a['event.name'] || a['claude_code.event']
    if (fromAttr) return fromAttr
    if (r.scopeName) {
        const seg = r.scopeName.split('.').filter(Boolean).pop()
        if (seg) return seg
    }
    return r.severityText || 'log'
}

export function TraceLogs({ traceId, sessionId, selectedSpanId }: TraceLogsProps) {
    const { data, isLoading, isError } = useQuery({
        queryKey: ['trace-logs', traceId, sessionId ?? ''],
        queryFn: () => getTraceLogs(traceId, sessionId),
        enabled: !!traceId,
    })

    const [onlySelected, setOnlySelected] = useState(true)
    const [expanded, setExpanded] = useState<Set<number>>(new Set())

    const records = useMemo(() => data ?? [], [data])
    const visible = useMemo(() => {
        if (selectedSpanId && onlySelected) {
            return records.filter((r) => r.spanId === selectedSpanId)
        }
        return records
    }, [records, selectedSpanId, onlySelected])

    const toggle = (i: number) =>
        setExpanded((prev) => {
            const next = new Set(prev)
            if (next.has(i)) next.delete(i)
            else next.add(i)
            return next
        })

    return (
        <div className="flex h-full flex-col px-3 py-2">
            <div className="mb-2 flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <Iconify.Icon icon="ph:scroll-duotone" className="size-4 text-brand-main-50 light:text-black" />
                    <span className="text-sm font-semibold text-brand-main-50 light:text-brand-main-50">Logs</span>
                    <span className="text-xs text-brand-main-50 light:text-black">{visible.length}</span>
                </div>
                {selectedSpanId && (
                    <button
                        type="button"
                        onClick={() => setOnlySelected((v) => !v)}
                        className="rounded border border-brand-main-500 px-2 py-0.5 text-[11px] text-brand-main-50 hover:bg-brand-main-600 light:text-black"
                    >
                        {onlySelected ?'Showing selected span — show all' : 'Show selected span only'}
                    </button>
                )}
            </div>

            {isLoading && <div className="py-8 text-center text-xs text-brand-main-50 light:text-black">Loading logs…</div>}
            {isError && (
                <div className="py-8 text-center text-xs text-rose-300">Failed to load logs for this trace.</div>
            )}
            {!isLoading && !isError && visible.length === 0 && (
                <div className="py-8 text-center text-xs text-brand-main-50 light:text-black">
                    No correlated logs for this trace.
                </div>
            )}

            <div className="min-h-0 flex-1 space-y-0.5 overflow-auto text-xs">
                {visible.map((r, i) => {
                    const sev = severityStyle(r.severityText)
                    const when = formatTime(tsToDate(r.timestamp))
                    const label = eventLabel(r)
                    const isOpen = expanded.has(i)
                    const attrs = r.attributes ?? {}
                    const attrKeys = Object.keys(attrs).sort()
                    return (
                        <div key={i} className="rounded border border-transparent hover:border-brand-main-600">
                            <button
                                type="button"
                                onClick={() => toggle(i)}
                                className="flex w-full items-start gap-2 px-1.5 py-1 text-left"
                            >
                                <Iconify.Icon
                                    icon={isOpen ? 'ph:caret-down' : 'ph:caret-right'}
                                    className="mt-0.5 size-3 shrink-0 text-brand-main-50 light:text-black"
                                />
                                <span className="shrink-0 text-brand-main-50 light:text-black">{when}</span>
                                <Badge
                                    className={cn(
                                        'h-4 shrink-0 border px-1 py-0 text-[10px] font-normal uppercase',
                                        sev.bg,
                                        sev.text,
                                        sev.border,
                                    )}
                                >
                                    {r.severityText || 'LOG'}
                                </Badge>
                                <span className="shrink-0 text-brand-secondary-200">{label}</span>
                                {r.body && (
                                    <span className={cn('truncate text-brand-main-50 light:text-black', isOpen && 'whitespace-pre-wrap break-words')}>
                                        {r.body}
                                    </span>
                                )}
                            </button>

                            {isOpen && (
                                <div className="space-y-2 px-3 pb-2 pl-7">
                                    {r.body && (
                                        <pre className="whitespace-pre-wrap break-words rounded bg-brand-main-600/40 p-2 text-brand-main-50 light:text-black">
                                            {r.body}
                                        </pre>
                                    )}
                                    {attrKeys.length > 0 && (
                                        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
                                            {attrKeys.map((k) => (
                                                <div key={k} className="contents">
                                                    <dt className="text-brand-main-50 light:text-black">{k}</dt>
                                                    <dd className="break-words text-brand-main-50 light:text-black">{attrs[k]}</dd>
                                                </div>
                                            ))}
                                        </dl>
                                    )}
                                    {r.spanId && (
                                        <div className="text-[10px] text-brand-main-50 light:text-black">span {r.spanId}</div>
                                    )}
                                </div>
                            )}
                        </div>
                    )
                })}
            </div>
        </div>
    )
}
