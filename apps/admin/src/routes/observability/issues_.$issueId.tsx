import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { z } from 'zod'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { ChevronRight, ChevronDown } from 'lucide-react'
import { TIME_RANGE_LABELS, calculateTimeRange } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import { useIssue, useUpdateIssueStatus } from '@/hooks/observability/use-issues'
import { useSession } from '@/hooks/auth/use-auth'
import { statusFeed } from '@/components/observability/issue-status-badge'
import { ProviderDisplay } from '@/components/providers/provider-icon'
import type { EventDetail, SpanCrumb, TagDistribution, IssueActivity } from '@everstack/proto/everstack/issues/v1/issues_pb'

dayjs.extend(relativeTime)

const {
  Button,
  EChart,
  brandTooltip,
  categoryAxis,
  valueAxis,
  baseGrid,
  areaGradient,
  BRAND_PALETTE,
  useChartMode,
} = ui

const detailSearchSchema = z.object({
  range: z
    .enum(Object.keys(TIME_RANGE_LABELS).filter((k) => k !== 'custom') as [string, ...string[]])
    .optional()
    .default('24h'),
})

export const Route = createFileRoute('/observability/issues_/$issueId')({
  component: IssueDetailPage,
  validateSearch: detailSearchSchema,
})

function tsToDate(ts?: { seconds: bigint; nanos: number }): Date | null {
  if (!ts) return null
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000))
}

function fmtNum(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`
  return n.toString()
}

// ─── Primitives ──────────────────────────────────────────────────────

function Section({
  id,
  icon,
  title,
  right,
  children,
  defaultOpen = true,
}: {
  id?: string
  icon: string
  title: string
  right?: React.ReactNode
  children: React.ReactNode
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div id={id} className="overflow-hidden rounded border border-brand-main-700 bg-brand-main-900/40">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 border-b border-brand-main-800 px-3 py-2 text-left"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5 text-white/30 light:text-black/30" /> : <ChevronRight className="h-3.5 w-3.5 text-white/30 light:text-black/30" />}
        <Iconify.Icon icon={icon} className="h-3.5 w-3.5 text-white/40 light:text-black/40" />
        <span className="text-xs font-medium text-white/80 light:text-black/80">{title}</span>
        {right && <div className="ml-auto">{right}</div>}
      </button>
      {open && children}
    </div>
  )
}

function KV({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[128px_1fr] items-start gap-3 border-b border-brand-main-800/50 px-3 py-1.5 last:border-0">
      <span className="truncate text-[11px] text-white/40 light:text-black/40">{label}</span>
      <span className={`min-w-0 break-all text-[11px] text-white/80 light:text-black/80 ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  )
}

// ─── Header band ─────────────────────────────────────────────────────

function HeaderBand({
  title,
  culprit,
  status,
  events,
  users,
  provider,
  model,
}: {
  title: string
  culprit?: string
  status: number
  events: number
  users: number
  provider?: string
  model?: string
}) {
  const feed = statusFeed(status)
  return (
    <div className="border-b border-brand-main-700 px-4 py-3">
      <div className="flex items-start justify-between gap-6">
        <div className="min-w-0">
          <h1 className="truncate text-base font-semibold text-white light:text-brand-main-50" title={title}>
            {title}
          </h1>
          {culprit && <div className="mt-0.5 truncate font-mono text-[11px] text-white/45 light:text-black/45">{culprit}</div>}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <span className={`inline-flex items-center gap-1.5 text-[11px] ${feed.text}`}>
              <span className={`h-1.5 w-1.5 rounded ${feed.dot}`} />
              {feed.label}
            </span>
            {provider && (
              <span className="flex items-center gap-1 text-[11px] text-white/40 light:text-black/40">
                <span className="inline-flex shrink-0 items-center [&_img]:!size-3 [&_svg]:!size-3">
                  <ProviderDisplay providerName={provider} isActive size="sm" />
                </span>
                {[provider, model].filter(Boolean).join(' · ')}
              </span>
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-6">
          <Stat big label="Events" value={fmtNum(events)} />
          <Stat big label="Users" value={fmtNum(users)} />
        </div>
      </div>
    </div>
  )
}

function Stat({ label, value, big }: { label: string; value: string; big?: boolean }) {
  return (
    <div className="text-right">
      <div className="text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">{label}</div>
      <div className={`mt-0.5 font-semibold text-white light:text-brand-main-50 ${big ? 'text-xl' : 'text-sm'}`} title={value}>
        {value}
      </div>
    </div>
  )
}

// ─── Tag summary (overview side panel) ───────────────────────────────

function TagSummary({ tags }: { tags: TagDistribution[] }) {
  if (!tags.length) return <div className="text-[11px] text-white/35 light:text-black/35">No tags.</div>
  return (
    <div className="space-y-2">
      {tags.map((t) => {
        const top = t.values[0]
        const pct = top && Number(t.total) > 0 ? (Number(top.count) / Number(t.total)) * 100 : 0
        return (
          <div key={t.key} className="grid grid-cols-[100px_1fr_auto] items-center gap-2">
            <span className="truncate font-mono text-[10px] text-white/40 light:text-black/40" title={t.key}>{t.key}</span>
            <div className="relative h-3.5 overflow-hidden rounded bg-brand-main-800/60">
              <div className="absolute inset-y-0 left-0 rounded bg-white/15 light:bg-black/15" style={{ width: `${pct}%` }} />
              <span className="absolute inset-0 flex items-center truncate px-1.5 text-[10px] text-white/70 light:text-black/70" title={top?.value}>
                {top?.value}
              </span>
            </div>
            <span className="w-9 text-right font-mono text-[10px] text-white/40 light:text-black/40">{pct.toFixed(0)}%</span>
          </div>
        )
      })}
    </div>
  )
}

// ─── Breadcrumbs (rich) ──────────────────────────────────────────────

function Breadcrumbs({ crumbs }: { crumbs: SpanCrumb[] }) {
  if (!crumbs.length) return <div className="px-3 py-2 text-[11px] text-white/35 light:text-black/35">No breadcrumbs.</div>
  return (
    <div className="divide-y divide-brand-main-800/50">
      {crumbs.map((c, i) => {
        const isErr = c.statusCode?.toLowerCase().includes('error')
        const ok = c.statusCode?.toLowerCase() === 'ok'
        const d = tsToDate(c.timestamp)
        return (
          <div key={`${c.spanId}-${i}`} className="flex items-start gap-2.5 px-3 py-2">
            <Iconify.Icon
              icon={isErr ? 'lucide:circle-x' : ok ? 'lucide:circle-check' : 'lucide:circle-dot'}
              className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${isErr ? 'text-rose-400/70 light:text-rose-600/70' : ok ? 'text-emerald-400/50 light:text-emerald-600/50' : 'text-white/30 light:text-black/30'}`}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate font-mono text-[11px] text-white/80 light:text-black/80">{c.name}</span>
                {isErr && <span className="rounded bg-rose-500/10 px-1 text-[10px] uppercase text-rose-300/80 light:text-rose-600/80">error</span>}
              </div>
              {c.statusMessage && <div className="mt-0.5 truncate text-[11px] text-white/45 light:text-black/45" title={c.statusMessage}>{c.statusMessage}</div>}
            </div>
            <div className="shrink-0 text-right">
              {c.durationMs > 0 && <div className="font-mono text-[10px] text-white/40 light:text-black/40">{c.durationMs.toFixed(1)}ms</div>}
              <div className="text-[10px] text-white/25 light:text-black/25">{d ? dayjs(d).format('HH:mm:ss.SSS') : ''}</div>
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ─── Trace preview (mini waterfall) ──────────────────────────────────

function TracePreview({ crumbs, onOpen }: { crumbs: SpanCrumb[]; onOpen: () => void }) {
  const rows = useMemo(() => {
    const spans = crumbs
      .map((c) => {
        const start = tsToDate(c.timestamp)?.getTime() ?? 0
        return { name: c.name, start, dur: Math.max(c.durationMs, 0.5), err: !!c.statusCode?.toLowerCase().includes('error') }
      })
      .filter((s) => s.start > 0)
    if (!spans.length) return []
    const t0 = Math.min(...spans.map((s) => s.start))
    const t1 = Math.max(...spans.map((s) => s.start + s.dur))
    const span = Math.max(t1 - t0, 1)
    return spans.map((s) => ({ ...s, left: ((s.start - t0) / span) * 100, width: Math.max((s.dur / span) * 100, 1.5) }))
  }, [crumbs])

  if (!rows.length) return <div className="px-3 py-2 text-[11px] text-white/35 light:text-black/35">No trace data.</div>
  return (
    <div className="space-y-1 px-3 py-2.5">
      {rows.map((r, i) => (
        <div key={i} className="grid grid-cols-[170px_1fr] items-center gap-2">
          <span className="truncate font-mono text-[10px] text-white/55 light:text-black/55" title={r.name}>{r.name}</span>
          <div className="relative h-3 rounded bg-brand-main-800/40">
            <div
              className={`absolute inset-y-0 rounded ${r.err ? 'bg-rose-400/60' : 'bg-white/25 light:bg-black/25'}`}
              style={{ left: `${r.left}%`, width: `${r.width}%` }}
            />
          </div>
        </div>
      ))}
      <button type="button" onClick={onOpen} className="mt-1 text-[11px] text-brand-secondary-300 hover:text-brand-secondary-200">
        View full trace
      </button>
    </div>
  )
}

// ─── Contexts (grouped attributes) ───────────────────────────────────

const CONTEXT_GROUPS: { title: string; prefix: string }[] = [
  { title: 'LLM', prefix: 'llm.' },
  { title: 'HTTP', prefix: 'http.' },
  { title: 'Error', prefix: 'error' },
  { title: 'Rate limit', prefix: 'ratelimit.' },
  { title: 'Agent', prefix: 'agent.' },
]

function groupContexts(attrs: Record<string, string>) {
  const used = new Set<string>()
  const groups = CONTEXT_GROUPS.map((g) => {
    const rows = Object.entries(attrs)
      .filter(([k]) => k.startsWith(g.prefix))
      .sort(([a], [b]) => a.localeCompare(b))
    rows.forEach(([k]) => used.add(k))
    return { title: g.title, rows }
  }).filter((g) => g.rows.length > 0)
  const other = Object.entries(attrs).filter(([k]) => !used.has(k) && k !== 'tenant.id').sort(([a], [b]) => a.localeCompare(b))
  if (other.length) groups.push({ title: 'Other', rows: other })
  return groups
}

// ─── Activity ────────────────────────────────────────────────────────

const ACTIVITY_ICON: Record<string, string> = {
  resolved: 'lucide:check',
  ignored: 'lucide:bell-off',
  reopened: 'lucide:rotate-ccw',
  assigned: 'lucide:user',
  unassigned: 'lucide:user-x',
}

function Activity({ activity }: { activity: IssueActivity[] }) {
  return (
    <div className="space-y-3">
      <textarea
        rows={2}
        disabled
        placeholder="Leave a comment (coming soon)"
        className="w-full resize-none rounded border border-brand-main-700 bg-brand-main-800/40 px-2 py-1.5 text-[11px] text-white/70 light:text-black/70 placeholder:text-white/25 light:placeholder:text-black/25 focus:outline-none"
      />
      {activity.length === 0 ? (
        <div className="text-[11px] text-white/35 light:text-black/35">No triage activity yet.</div>
      ) : (
        <div className="space-y-2.5">
          {activity.map((a, i) => {
            const d = tsToDate(a.createdAt)
            return (
              <div key={i} className="flex items-start gap-2 text-[11px]">
                <Iconify.Icon icon={ACTIVITY_ICON[a.action] ?? 'lucide:dot'} className="mt-0.5 h-3 w-3 shrink-0 text-white/40 light:text-black/40" />
                <div className="min-w-0">
                  <span className="text-white/70 light:text-black/70">{a.actor || 'system'}</span> <span className="text-white/45 light:text-black/45">{a.action}</span>
                  {a.note && <span className="text-white/45 light:text-black/45"> {a.note}</span>}
                  <div className="text-[10px] text-white/25 light:text-black/25">{d ? dayjs(d).fromNow() : ''}</div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ─── Page ────────────────────────────────────────────────────────────

const HIGHLIGHT_KEYS = [
  'http.status_code',
  'http.latency_ms',
  'error.type',
  'error.retryable',
  'llm.request.model_parameters',
  'ratelimit.remaining_tokens',
  'http.url',
]
const HIGHLIGHT_LABEL: Record<string, string> = {
  'http.status_code': 'Status code',
  'http.latency_ms': 'Latency',
  'error.type': 'Error type',
  'error.retryable': 'Retryable',
  'llm.request.model_parameters': 'Parameters',
  'ratelimit.remaining_tokens': 'Tokens left',
  'http.url': 'URL',
}

function IssueDetailPage() {
  const { issueId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()
  const update = useUpdateIssueStatus()
  const session = useSession()
  const me = (session.data as any)?.user?.user?.email as string | undefined
  const mode = useChartMode()

  const { from, to } = useMemo(() => {
    const now = new Date()
    now.setSeconds(0, 0)
    const result = calculateTimeRange(search.range as TimeRangePreset, null)
    return { from: new Date(result.from), to: now }
  }, [search.range])

  const { data, isLoading } = useIssue(issueId, { from, to })
  const issue = data?.issue
  const occurrences = data?.occurrences ?? []
  const tags = data?.tags ?? []
  const breadcrumbs = data?.breadcrumbs ?? []
  const activity = data?.activity ?? []
  const ev = data?.latestEvent as EventDetail | undefined
  const attrs = ev?.attributes ?? {}

  const chartData = useMemo(
    () => (data?.trend ?? []).map((p) => ({ t: tsToDate(p.timestamp)?.getTime() ?? 0, count: Number(p.count) })),
    [data?.trend],
  )

  const trendOption = useMemo(
    () => ({
      grid: baseGrid({ left: 0, right: 4, top: 8, bottom: 0 }),
      tooltip: brandTooltip({
        trigger: 'item',
        headerFormatter: (v) => dayjs(Number(v)).format('MMM D, HH:mm'),
        valueFormatter: (val) => `${val} events`,
      }),
      xAxis: categoryAxis(
        chartData.map((d) => d.t),
        { axisLabel: { hideOverlap: true, formatter: (v: number) => dayjs(Number(v)).format('MMM D') } },
      ),
      yAxis: valueAxis(undefined, { position: 'left', show: false }),
      series: [
        {
          name: 'Events',
          type: 'bar',
          data: chartData.map((d) => d.count),
          itemStyle: { ...areaGradient(BRAND_PALETTE[0], 0.55, 0.15), borderRadius: [2, 2, 0, 0] },
        },
      ],
    }),
    // `mode` rebuilds the option when the theme toggles (BRAND_PALETTE/axis getters are theme-aware)
    [chartData, mode],
  )

  const openTrace = (traceId: string) =>
    (navigate as (opts: unknown) => void)({
      to: '/observability/traces',
      search: (prev: Record<string, unknown>) => ({ ...prev, live: 'false', range: search.range, trace: traceId }),
    })

  const assign = (assignee: string) => {
    if (!issue) return
    update.mutate({ fingerprint: issue.fingerprint, status: issue.status, signature: issue.signature, title: issue.title, assignee })
  }

  if (isLoading) {
    return (
      <div className="space-y-3 p-4">
        <div className="h-6 w-2/3 animate-pulse rounded bg-brand-main-800/60" />
        <div className="h-4 w-1/3 animate-pulse rounded bg-brand-main-800/40" />
        <div className="h-44 animate-pulse rounded bg-brand-main-800/30" />
      </div>
    )
  }
  if (!issue) {
    return <div className="flex h-full flex-col items-center justify-center text-white/50 light:text-black/50">Issue not found in this window.</div>
  }

  const firstSeen = tsToDate(issue.firstSeen)
  const lastSeen = tsToDate(issue.lastSeen)
  const assignee = issue.assignee
  const culprit = attrs['http.url'] || (attrs['http.method'] ? `${attrs['http.method']} request` : undefined)
  const highlights = HIGHLIGHT_KEYS.filter((k) => attrs[k])
  const contexts = groupContexts(attrs)
  const traceId = ev?.traceId

  return (
    <div className="h-full overflow-auto">
      <HeaderBand
        title={issue.title || issue.signature || '(no message)'}
        culprit={culprit}
        status={issue.status}
        events={Number(issue.count)}
        users={Number(data?.users ?? 0)}
        provider={issue.provider}
        model={issue.model}
      />

      <div className="flex flex-col gap-4 p-4 lg:flex-row">
        {/* Main column */}
        <div className="min-w-0 flex-1 space-y-3">
          {/* Overview: chart + tag summary */}
          <div className="grid gap-3 lg:grid-cols-[1fr_280px]">
            <div className="rounded border border-brand-main-700 bg-brand-main-900/40 p-3">
              <div className="mb-2 text-[11px] font-medium text-white/60 light:text-black/60">Events over time</div>
              {chartData.length > 0 ? (
                <EChart option={trendOption} height={140} />
              ) : (
                <div className="flex h-[140px] items-center justify-center text-xs text-white/40 light:text-black/40">No trend data</div>
              )}
            </div>
            <div className="rounded border border-brand-main-700 bg-brand-main-900/40 p-3">
              <div className="mb-2 text-[11px] font-medium text-white/60 light:text-black/60">Tag summary</div>
              <TagSummary tags={tags} />
            </div>
          </div>

          {/* Event strip */}
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded border border-brand-main-700 bg-brand-main-900/40 px-3 py-2 text-[11px]">
            <span className="font-medium text-white/70 light:text-black/70">Latest event</span>
            {ev && <span className="font-mono text-white/45 light:text-black/45">{ev.spanId?.slice(0, 12)}</span>}
            {ev && <span className="text-white/35 light:text-black/35">{dayjs(tsToDate(ev.timestamp) ?? undefined).fromNow()}</span>}
            <div className="ml-auto flex items-center gap-3 text-white/35 light:text-black/35">
              <span>Jump to:</span>
              {['Highlights', 'Breadcrumbs', 'Trace', 'Tags', 'Contexts'].map((a) => (
                <a key={a} href={`#sec-${a.toLowerCase()}`} className="hover:text-white/70 light:hover:text-black/70">{a}</a>
              ))}
            </div>
          </div>

          {highlights.length > 0 && (
            <Section id="sec-highlights" icon="lucide:sparkles" title="Highlights">
              <div className="grid sm:grid-cols-2">
                {highlights.map((k) => (
                  <KV key={k} label={HIGHLIGHT_LABEL[k] ?? k} value={k === 'http.latency_ms' ? `${attrs[k]} ms` : attrs[k]} mono={k === 'http.url' || k === 'llm.request.model_parameters'} />
                ))}
              </div>
            </Section>
          )}

          <Section icon="lucide:message-square" title="Message">
            <div className="px-3 py-2 font-mono text-[11px] break-all text-white/70 light:text-black/70">{issue.title || issue.signature}</div>
          </Section>

          <Section id="sec-breadcrumbs" icon="lucide:footprints" title="Breadcrumbs">
            <Breadcrumbs crumbs={breadcrumbs} />
          </Section>

          <Section id="sec-trace" icon="lucide:git-fork" title="Trace preview">
            <TracePreview crumbs={breadcrumbs} onOpen={() => traceId && openTrace(traceId)} />
          </Section>

          <Section id="sec-tags" icon="lucide:tags" title="Tags">
            <div className="grid gap-x-6 gap-y-3 p-3 sm:grid-cols-2">
              {tags.map((t) => (
                <div key={t.key}>
                  <div className="mb-1 font-mono text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">{t.key}</div>
                  <div className="space-y-1">
                    {t.values.map((v) => {
                      const pct = Number(t.total) > 0 ? (Number(v.count) / Number(t.total)) * 100 : 0
                      return (
                        <div key={v.value} className="flex items-center gap-2">
                          <div className="relative h-4 flex-1 overflow-hidden rounded bg-brand-main-800/60">
                            <div className="absolute inset-y-0 left-0 rounded bg-white/15 light:bg-black/15" style={{ width: `${pct}%` }} />
                            <span className="absolute inset-0 flex items-center truncate px-1.5 text-[11px] text-white/70 light:text-black/70" title={v.value}>{v.value}</span>
                          </div>
                          <span className="w-8 shrink-0 text-right font-mono text-[11px] text-white/45 light:text-black/45">{v.count.toString()}</span>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
              {tags.length === 0 && <div className="text-[11px] text-white/35 light:text-black/35">No tags.</div>}
            </div>
          </Section>

          {contexts.length > 0 && (
            <Section id="sec-contexts" icon="lucide:box" title="Contexts">
              <div className="grid gap-3 p-3 sm:grid-cols-2">
                {contexts.map((g) => (
                  <div key={g.title} className="overflow-hidden rounded border border-brand-main-800">
                    <div className="border-b border-brand-main-800 bg-brand-main-800/30 px-3 py-1.5 text-[11px] font-medium text-white/60 light:text-black/60">{g.title}</div>
                    <div>
                      {g.rows.map(([k, v]) => (
                        <KV key={k} label={k} value={v} mono />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </Section>
          )}

          <Section icon="lucide:list" title="Recent occurrences">
            <div className="divide-y divide-brand-main-800/50">
              {occurrences.map((o, i) => {
                const d = tsToDate(o.timestamp)
                return (
                  <button key={`${o.traceId}-${i}`} type="button" onClick={() => openTrace(o.traceId)} className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-brand-secondary-500/10">
                    <span className="font-mono text-[11px] text-brand-secondary-300">{o.traceId.slice(0, 8)}</span>
                    <span className="truncate text-xs text-white/70 light:text-black/70">{o.spanName || o.message}</span>
                    <span className="ml-auto shrink-0 text-[11px] text-white/35 light:text-black/35">{d ? dayjs(d).fromNow() : ''}</span>
                    <ChevronRight className="h-3.5 w-3.5 shrink-0 text-white/30 light:text-black/30" />
                  </button>
                )
              })}
              {occurrences.length === 0 && <div className="px-3 py-2 text-xs text-white/40 light:text-black/40">No occurrences in this window.</div>}
            </div>
          </Section>
        </div>

        {/* Sidebar */}
        <div className="w-full shrink-0 space-y-3 lg:w-72">
          <div className="rounded border border-brand-main-700 bg-brand-main-900/40 p-3">
            <KVrow label="Last seen" value={lastSeen ? dayjs(lastSeen).fromNow() : '--'} />
            <KVrow label="First seen" value={firstSeen ? dayjs(firstSeen).fromNow() : '--'} />
            <KVrow label="Events" value={fmtNum(Number(issue.count))} />
            <KVrow label="Users" value={fmtNum(Number(data?.users ?? 0))} />
          </div>

          <div className="rounded border border-brand-main-700 bg-brand-main-900/40 p-3">
            <div className="mb-2 flex items-center gap-2 text-[11px] font-medium text-white/70 light:text-black/70">
              <Iconify.Icon icon="lucide:user" className="h-3.5 w-3.5 text-white/40 light:text-black/40" />
              Assignee
            </div>
            {assignee ? (
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-xs text-white/80 light:text-black/80" title={assignee}>{assignee}</span>
                <Button variant="ghost" size="sm" className="h-6 px-2 text-[11px] text-white/50 light:text-black/50" disabled={update.isPending} onClick={() => assign('')}>Unassign</Button>
              </div>
            ) : (
              <Button variant="outline" size="sm" className="h-7 w-full border-brand-main-600 bg-brand-main-800 text-xs text-brand-main-100 hover:bg-brand-main-700" disabled={update.isPending || !me} onClick={() => me && assign(me)}>Assign to me</Button>
            )}
          </div>

          <div className="rounded border border-brand-main-700 bg-brand-main-900/40 p-3">
            <div className="mb-2 flex items-center gap-2 text-[11px] font-medium text-white/70 light:text-black/70">
              <Iconify.Icon icon="lucide:history" className="h-3.5 w-3.5 text-white/40 light:text-black/40" />
              Activity
            </div>
            <Activity activity={activity} />
          </div>

          <div className="rounded border border-dashed border-brand-main-700/60 bg-brand-main-900/20 p-3">
            <div className="text-[11px] font-medium text-white/45 light:text-black/45">Issue tracking</div>
            <div className="mt-1 text-[10px] text-white/30 light:text-black/30">GitHub linking, similar &amp; merged issues coming soon.</div>
          </div>
        </div>
      </div>
    </div>
  )
}

function KVrow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between border-b border-brand-main-800/50 py-1.5 text-[11px] last:border-0">
      <span className="text-white/40 light:text-black/40">{label}</span>
      <span className="font-medium text-white/80 light:text-black/80">{value}</span>
    </div>
  )
}
