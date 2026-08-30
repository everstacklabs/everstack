import { Fragment } from 'react'
import { Iconify } from '@everstack/ui'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { categoryColors, categoryIcons, categoryLabels } from '@/utils/span-display-helpers'
import type { SpanCategory } from '@/utils/traces-common'
import { spanTypeSummary } from '@/utils/span-type-summary'

/**
 * SpanTypeSummaryCard renders the per-type "what happened" fields for a span
 * (M4-T1) — the command that ran, the URL navigated to, the query and result
 * count — as a compact, category-tinted card. Returns null for categories
 * without a dedicated summary so callers can drop it in unconditionally.
 */
export function SpanTypeSummaryCard({ span, category }: { span: Span; category: SpanCategory }) {
  const fields = spanTypeSummary(span, category)
  if (fields.length === 0) return null

  const colors = categoryColors[category]

  return (
    <div className={`rounded border ${colors.border} ${colors.bg} p-3`}>
      <div className="mb-2 flex items-center gap-2">
        <Iconify.Icon icon={categoryIcons[category]} className={`h-3.5 w-3.5 ${colors.text}`} />
        <span className={`text-xs font-medium ${colors.text}`}>{categoryLabels[category]}</span>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        {fields.map((f) => (
          <Fragment key={f.label}>
            <dt className="text-xs text-zinc-500">{f.label}</dt>
            <dd className="truncate text-xs text-zinc-300 light:text-zinc-800" title={f.value}>
              {f.value}
            </dd>
          </Fragment>
        ))}
      </dl>
    </div>
  )
}
