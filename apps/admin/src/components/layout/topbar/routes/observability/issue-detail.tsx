import { useMemo } from 'react'
import { Link, useLocation, useSearch } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { Check, BellOff, RotateCcw } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { calculateTimeRange, TIME_RANGE_LABELS } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import { useIssue, useUpdateIssueStatus } from '@/hooks/observability/use-issues'
import { IssueStatus } from '@everstack/proto/everstack/issues/v1/issues_pb'

// Resolve the fingerprint + current time window from the URL so these topbar
// widgets stay in sync with the detail page without prop threading. The window
// is memoized on [fingerprint, range] — recomputing it every render would drift
// by milliseconds and thrash the query key, leaving the breadcrumb stuck
// loading.
function useIssueContext() {
  const { pathname } = useLocation()
  const fingerprint = pathname.split('/').filter(Boolean).pop() ?? ''
  const search = useSearch({ strict: false }) as { range?: string }
  const range = (search.range ?? '24h') as TimeRangePreset
  return useMemo(() => {
    const now = new Date()
    now.setSeconds(0, 0)
    const r = calculateTimeRange(TIME_RANGE_LABELS[range] ? range : '24h', null)
    return { fingerprint, from: new Date(r.from), to: now }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fingerprint, range])
}

function IssueBreadcrumb() {
  const { fingerprint, from, to } = useIssueContext()
  const { data, isLoading } = useIssue(fingerprint, { from, to })
  const title = data?.issue?.title || data?.issue?.signature || fingerprint.slice(0, 12)
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <Link to="/observability/issues" search={{ range: '24h', status: 'all' }} className="text-sm font-normal text-brand-main-300 transition-colors hover:text-white/80 light:hover:text-black/80">
        Issues
      </Link>
      <span className="text-sm text-brand-main-400">/</span>
      {isLoading ? (
        <span className="inline-block h-4 w-32 animate-pulse rounded bg-white/10 light:bg-black/10" />
      ) : (
        <span className="max-w-[420px] truncate text-sm font-normal text-white light:text-brand-main-50" title={title}>
          {title}
        </span>
      )}
    </div>
  )
}

function IssueActions() {
  const { fingerprint, from, to } = useIssueContext()
  const { data } = useIssue(fingerprint, { from, to })
  const issue = data?.issue
  const update = useUpdateIssueStatus()

  if (!issue) return null

  const apply = (status: IssueStatus) =>
    update.mutate({ fingerprint: issue.fingerprint, status, signature: issue.signature, title: issue.title })

  if (issue.status === IssueStatus.RESOLVED) {
    return (
      <Button
        variant="outline"
        className="gap-1 border-brand-main-600 bg-brand-main-800 text-brand-main-100 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50"
        disabled={update.isPending}
        onClick={() => apply(IssueStatus.UNRESOLVED)}
      >
        <RotateCcw className="h-3.5 w-3.5" />
        Reopen
      </Button>
    )
  }

  return (
    <div className="flex items-center gap-2">
      <Button
        variant="outline"
        className="gap-1 border-brand-main-600 bg-brand-main-800 text-brand-main-100 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50"
        disabled={update.isPending}
        onClick={() => apply(IssueStatus.RESOLVED)}
      >
        <Check className="h-3.5 w-3.5" />
        Resolve
      </Button>
      <Button
        variant="ghost"
        className="gap-1 text-white/70 light:text-black/70"
        disabled={update.isPending}
        onClick={() => apply(IssueStatus.IGNORED)}
      >
        <BellOff className="h-3.5 w-3.5" />
        Ignore
      </Button>
    </div>
  )
}

export const ObservabilityIssueDetailActions: ActionGroup[] = [
  { title: <IssueBreadcrumb /> },
  {
    actions: [{ type: 'custom', key: 'issue-actions', label: 'Issue actions', component: IssueActions }],
  },
]
