import { IssueStatus, IssueCategory } from '@everstack/proto/everstack/issues/v1/issues_pb'

// Issues follow Sentry's restraint: the feed is near-monochrome. Colour is
// reserved for one small status dot/label; everything else is neutral gray so
// the data, not the palette, carries the page.

type StatusFeed = { label: string; dot: string; text: string }

const STATUS_FEED: Record<number, StatusFeed> = {
  [IssueStatus.UNRESOLVED]: { label: 'Ongoing', dot: 'bg-white/30 light:bg-black/30', text: 'text-white/45 light:text-black/45' },
  [IssueStatus.REGRESSED]: { label: 'Regressed', dot: 'bg-amber-400/60', text: 'text-amber-300/75' },
  [IssueStatus.RESOLVED]: { label: 'Resolved', dot: 'bg-emerald-400/50', text: 'text-emerald-300/70' },
  [IssueStatus.IGNORED]: { label: 'Ignored', dot: 'bg-white/15 light:bg-black/15', text: 'text-white/30 light:text-black/30' },
}

export function statusFeed(status: IssueStatus): StatusFeed {
  return STATUS_FEED[status] ?? STATUS_FEED[IssueStatus.UNRESOLVED]
}

// Small filled level dot — the one bit of colour in a feed row.
export function IssueLevelDot({ status }: { status: IssueStatus }) {
  return <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${statusFeed(status).dot}`} aria-hidden />
}

// Status badge for the detail header — neutral pill with a muted status dot.
export function IssueStatusBadge({ status, className = '' }: { status: IssueStatus; className?: string }) {
  const meta = statusFeed(status)
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-800/50 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${meta.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${meta.dot}`} />
      {meta.label}
    </span>
  )
}

const CATEGORY_LABEL: Record<number, string> = {
  [IssueCategory.RATE_LIMIT]: 'Rate limit',
  [IssueCategory.CONTEXT_LENGTH]: 'Context length',
  [IssueCategory.PROVIDER_5XX]: 'Provider 5xx',
  [IssueCategory.GUARDRAIL_BLOCK]: 'Guardrail',
  [IssueCategory.TOOL_ERROR]: 'Tool error',
  [IssueCategory.TIMEOUT]: 'Timeout',
  [IssueCategory.AUTH]: 'Auth',
  [IssueCategory.PARSE_ERROR]: 'Parse error',
  [IssueCategory.OTHER]: 'Other',
}

export function categoryLabel(category: IssueCategory): string {
  return CATEGORY_LABEL[category] ?? 'Other'
}

// Neutral category tag (Sentry-style project/tag chip) — no per-category colour.
export function IssueCategoryBadge({ category, className = '' }: { category: IssueCategory; className?: string }) {
  return (
    <span
      className={`inline-flex shrink-0 items-center whitespace-nowrap rounded border border-brand-main-700 bg-brand-main-800/40 px-1.5 py-0.5 text-[10px] font-medium text-white/50 light:text-black/50${className} light:text-black/50`}
    >
      {categoryLabel(category)}
    </span>
  )
}
