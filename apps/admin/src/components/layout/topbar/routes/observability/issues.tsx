import { useNavigate, useSearch } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { TIME_RANGE_LABELS } from '@/lib/time-ranges'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { IssueConnectSheet } from '@/components/observability/issue-connect-sheet'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

const STATUS_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'unresolved', label: 'Unresolved' },
  { value: 'regressed', label: 'Regressed' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'ignored', label: 'Ignored' },
]

// Topbar filters for the Issues page. They drive the route's URL search params
// (range, status) so the page body stays content-only, per the topbar pattern.

function IssuesStatusFilter() {
  const navigate = useNavigate() as (opts: unknown) => void
  const search = useSearch({ strict: false }) as { status?: string }
  return (
    <Select
      value={search.status ?? 'all'}
      onValueChange={(value) =>
        navigate({ to: '.', search: (prev: Record<string, unknown>) => ({ ...prev, status: value }), replace: true })
      }
    >
      <SelectTrigger className="h-8 w-[150px] border-brand-main-600 bg-brand-main-800 text-sm text-white light:text-brand-main-50">
        <SelectValue placeholder="Status" />
      </SelectTrigger>
      <SelectContent className="border-brand-main-600 bg-brand-main-800">
        {STATUS_OPTIONS.map((o) => (
          <SelectItem key={o.value} value={o.value} className="text-white light:text-brand-main-50 focus:bg-brand-main-700">
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function IssuesRangeFilter() {
  const navigate = useNavigate() as (opts: unknown) => void
  const search = useSearch({ strict: false }) as { range?: string }
  return (
    <Select
      value={search.range ?? '24h'}
      onValueChange={(value) =>
        navigate({ to: '.', search: (prev: Record<string, unknown>) => ({ ...prev, range: value }), replace: true })
      }
    >
      <SelectTrigger className="h-8 w-[150px] border-brand-main-600 bg-brand-main-800 text-sm text-white light:text-brand-main-50">
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="border-brand-main-600 bg-brand-main-800">
        {Object.entries(TIME_RANGE_LABELS)
          .filter(([key]) => key !=='custom')
          .map(([key, label]) => (
            <SelectItem key={key} value={key} className="text-white light:text-brand-main-50 focus:bg-brand-main-700">
              {label}
            </SelectItem>
          ))}
      </SelectContent>
    </Select>
  )
}

function IssuesConnectAction() {
  return <IssueConnectSheet variant="topbar" />
}

export const ObservabilityIssuesActions: ActionGroup[] = [
  {
    title: 'Issues',
    actions: [
      { type: 'custom', key: 'issues-status', label: 'Status', component: IssuesStatusFilter },
      { type: 'custom', key: 'issues-range', label: 'Range', component: IssuesRangeFilter },
      { type: 'custom', key: 'issues-connect', label: 'Connect SDK', component: IssuesConnectAction },
    ],
  },
]
