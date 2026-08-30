import { Info } from 'lucide-react'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import { ProviderDisplay } from '../../providers/provider-icon'
import { formatDelta } from './format'

const { Card, CardContent, Badge, Tooltip } = ui

export type TopNRow = {
  key: string
  value: number
  requestCount?: bigint | number
  previousValue?: number
  provider?: string
}

export function TopNCard({
  title,
  info,
  rows,
  totalGroups,
  valueFormatter,
  labelFormatter,
  compare = false,
  onRowClick,
}: {
  title: string
  info?: string
  rows: TopNRow[]
  totalGroups?: bigint | number
  valueFormatter: (value: number) => string
  // Maps a row key to its display label (e.g. provider-key id -> human name).
  // Defaults to showing the raw key.
  labelFormatter?: (key: string) => string
  compare?: boolean
  onRowClick?: (key: string) => void
}) {
  const max = Math.max(...rows.map((r) => r.value), 0)
  return (
    <Card className="border-brand-main-700 bg-brand-main-950/80 rounded">
      <CardContent className="px-3">
        <div className="mb-2 flex items-center justify-between gap-3">
          <div className="flex items-center gap-1">
            <span className="text-[11px] uppercase text-white/45 light:text-black/45">
              {title}
            </span>
            {info && (
              <Tooltip
                content={
                  <span className="block w-max max-w-[240px] px-2 py-1 text-left text-xs text-white light:text-brand-main-50">
                    {info}
                  </span>
                }
              >
                <Info className="size-3 shrink-0 text-white/30 transition-colors hover:text-white/60 light:text-black/30 light:hover:text-black/60" />
              </Tooltip>
            )}
          </div>
          {totalGroups !== undefined && Number(totalGroups) > rows.length && (
            <span className="text-[10px] text-white/35 light:text-black/35">
              {Number(totalGroups)} groups
            </span>
          )}
        </div>

        {rows.length === 0 ? (
          <div className="flex h-[168px] items-center justify-center text-xs text-white/25 light:text-black/25">
            No data
          </div>
        ) : (
          <div className="space-y-1.5">
            {rows.map((row, index) => {
              const width = max > 0 ? Math.max((row.value / max) * 100, 4) : 0
              const delta =
                compare && row.previousValue
                  ? (row.value - row.previousValue) / row.previousValue
                  : undefined
              return (
                <button
                  key={row.key}
                  type="button"
                  onClick={() => onRowClick?.(row.key)}
                  className={cn(
                    'group relative w-full overflow-hidden rounded border border-white/5 bg-white/[0.025] px-2 py-2 text-left light:border-black/5 light:bg-black/[0.025]',
                    onRowClick && 'hover:border-brand-secondary-500/40',
                  )}
                >
                  <div
                    className="absolute inset-y-0 left-0 bg-brand-secondary-500/12"
                    style={{ width: `${width}%` }}
                  />
                  <div className="relative flex items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="w-4 shrink-0 text-[10px] text-white/30 light:text-black/30">
                        {index + 1}
                      </span>
                      {row.provider && (
                        <span className="shrink-0">
                          <ProviderDisplay
                            providerName={row.provider}
                            isActive={false}
                            size="sm"
                          />
                        </span>
                      )}
                      <span className="truncate font-mono text-xs text-white/85 light:text-black/85">
                        {labelFormatter ? labelFormatter(row.key) : row.key}
                      </span>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      {delta !== undefined && (
                        <Badge
                          variant="outline"
                          className={cn(
                            'rounded px-1 py-0 text-[10px] font-mono',
                            delta > 0 &&
                              'border-emerald-500/30 bg-emerald-500/10 text-emerald-400 light:text-emerald-600',
                            delta < 0 &&
                              'border-rose-500/30 bg-rose-500/10 text-rose-400 light:text-rose-600',
                            delta === 0 &&
                              'border-white/10 bg-white/5 text-white/45 light:border-black/10 light:bg-black/5 light:text-black/45',
                          )}
                        >
                          {formatDelta(delta)}
                        </Badge>
                      )}
                      <span className="font-mono text-xs text-white light:text-brand-main-50">
                        {valueFormatter(row.value)}
                      </span>
                    </div>
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
