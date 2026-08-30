import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { Iconify } from '@everstack/ui/icons'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { TIME_RANGE_LABELS, calculateTimeRange } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import { useIssues } from '@/hooks/observability/use-issues'
import {
  IssueLevelDot,
  IssueCategoryBadge,
  statusFeed,
} from '@/components/observability/issue-status-badge'
import { IssueSparkline } from '@/components/observability/issue-sparkline'
import { IssueConnectSheet } from '@/components/observability/issue-connect-sheet'
import { ProviderDisplay } from '@/components/providers/provider-icon'
import { IssueStatus } from '@everstack/proto/everstack/issues/v1/issues_pb'
import type { Issue } from '@everstack/proto/everstack/issues/v1/issues_pb'

dayjs.extend(relativeTime)

const issuesSearchSchema = z.object({
  range: z
    .enum(Object.keys(TIME_RANGE_LABELS).filter((k) => k !== 'custom') as [string, ...string[]])
    .optional()
    .default('24h'),
  status: z.enum(['all', 'unresolved', 'regressed', 'resolved', 'ignored']).optional().default('all'),
  q: z.string().optional(),
})

export const Route = createFileRoute('/observability/issues')({
  component: IssuesPage,
  validateSearch: issuesSearchSchema,
})

const STATUS_TO_ENUM: Record<string, IssueStatus | undefined> = {
  all: undefined,
  unresolved: IssueStatus.UNRESOLVED,
  regressed: IssueStatus.REGRESSED,
  resolved: IssueStatus.RESOLVED,
  ignored: IssueStatus.IGNORED,
}

function tsToDate(ts?: { seconds: bigint; nanos: number }): Date | null {
  if (!ts) return null
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000))
}

const EMPTY_ISSUES: Issue[] = []

function IssuesPage() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()

  const { from, to } = useMemo(() => {
    const now = new Date()
    now.setSeconds(0, 0)
    const result = calculateTimeRange(search.range as TimeRangePreset, null)
    return { from: new Date(result.from), to: now }
  }, [search.range])

  const { data, isLoading } = useIssues({
    from,
    to,
    statusFilter: STATUS_TO_ENUM[search.status],
    query: search.q,
    limit: 200,
  })

  const issues = data?.issues ?? EMPTY_ISSUES

  const columns: ColumnConfig<Issue>[] = useMemo(
    () => [
      {
        id: 'title',
        header: 'Issue',
        // Only flexible column (others are maxWidth-capped), so it absorbs all
        // remaining table width, keeping it the dominant ~60%+ column.
        width: 560,
        minWidth: 360,
        render: (row) => (
          <div className="flex min-w-full items-start">
            <span className="mt-1.5">
              <IssueLevelDot status={row.status} />
            </span>
            <div className="min-w-0 leading-tight">
              <div className="truncate text-sm font-semibold text-white/90 light:text-black/90" title={row.title}>
                {row.title || row.signature || '(no message)'}
              </div>
              <div className="mt-1 h-3.5 truncate font-mono text-[11px] text-white/35 light:text-black/35" title={row.signature}>
                {row.signature}
              </div>
              <div className="mt-1.5 flex items-center gap-2">
                <IssueCategoryBadge category={row.category} />
                {row.provider && (
                  <span className="flex min-w-0 items-center gap-1 text-[11px] text-white/40 light:text-black/40">
                    <span className="inline-flex shrink-0 items-center [&_img]:!size-3 [&_svg]:!size-3">
                      <ProviderDisplay providerName={row.provider} isActive size="sm" />
                    </span>
                    <span className="truncate">{[row.provider, row.model].filter(Boolean).join(' · ')}</span>
                  </span>
                )}
              </div>
            </div>
          </div>
        ),
      },
      {
        id: 'last',
        header: 'Last seen',
        width: 110,
        maxWidth: 110,
        render: (row) => {
          const last = tsToDate(row.lastSeen)
          return <span className="text-xs text-white/55 light:text-black/55">{last ? dayjs(last).fromNow() : '--'}</span>
        },
      },
      {
        id: 'age',
        header: 'Age',
        width: 90,
        maxWidth: 90,
        render: (row) => {
          const first = tsToDate(row.firstSeen)
          return <span className="text-xs text-white/45 light:text-black/45">{first ? dayjs(first).fromNow(true) : '--'}</span>
        },
      },
      {
        id: 'trend',
        header: 'Trend',
        width: 200,
        maxWidth: 200,
        render: (row) => {
          const spark = row.sparkline ?? []
          const peak = spark.length ? Math.max(...spark) : 0
          const feed = statusFeed(row.status)
          return (
            <div className="relative w-[168px]">
              {peak > 0 && (
                <span className="absolute -top-1 right-0 font-mono text-[10px] text-white/30 light:text-black/30">{peak}</span>
              )}
              <IssueSparkline data={spark} from={from} to={to} width={168} height={38} className="text-white/30 light:text-black/30" />
              <span className={`block text-[10px] leading-tight ${feed.text}`}>{feed.label}</span>
            </div>
          )
        },
      },
      {
        id: 'count',
        header: 'Events',
        width: 80,
        maxWidth: 80,
        className: 'text-right',
        render: (row) => (
          <div className="text-right font-mono text-sm font-semibold text-white/85 light:text-black/85">{row.count.toString()}</div>
        ),
      },
    ],
    [from, to],
  )

  if (isLoading && issues.length === 0) {
    return (
      <div className="flex h-full w-full flex-col overflow-hidden">
        <div className="divide-y divide-brand-main-800/60">
          {[...Array(10)].map((_, i) => (
            <div key={i} className="flex items-center gap-3 px-3 py-3">
              <div className="flex-1 space-y-2">
                <div className="h-3 w-2/3 animate-pulse rounded bg-brand-main-700/70" />
                <div className="h-2.5 w-1/3 animate-pulse rounded bg-brand-main-800" />
              </div>
              <div className="h-7 w-28 animate-pulse rounded bg-brand-main-800" />
              <div className="h-4 w-10 animate-pulse rounded bg-brand-main-800" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  if (!isLoading && issues.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center px-6">
        <div className="mb-5 rounded-xl border border-brand-main-700 bg-brand-main-800/60 p-4">
          <Iconify.Icon icon="lucide:bug" className="size-7 text-white/40 light:text-black/40" />
        </div>
        <h3 className="mb-2 text-base font-medium text-white/90 light:text-black/90">No issues in this window</h3>
        <p className="mb-5 max-w-md text-center text-sm leading-relaxed text-white/45 light:text-black/45">
          Issues group recurring failures by signature, so one incident becomes a single line you can
          track instead of thousands of identical errors. They appear automatically as your gateway and
          agents emit errors. Nothing matched the selected status and time range.
        </p>
        <IssueConnectSheet />
      </div>
    )
  }

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      <ResponsiveTable
        columns={columns}
        data={issues}
        isLoading={isLoading}
        enableResizing
        minTableWidth="100%"
        emptyMessage="No issues found."
        rowKey={(row: Issue) => row.fingerprint}
        onRowClick={(row: Issue) =>
          navigate({
            to: '/observability/issues/$issueId',
            params: { issueId: row.fingerprint },
            search: { range: search.range },
          })
        }
      />
    </div>
  )
}
