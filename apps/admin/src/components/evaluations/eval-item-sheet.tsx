import { ui } from '@everstack/ui'
import { NavigationButtons } from '../common/navigation-buttons'
import { JsonViewer } from '@/ui/json-viewer'
import { cn } from '@everstack/utils/functions/cn'
import {
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
} from '@/components/evaluations/evaluation-form'

const { Sheet, SheetHeader, SheetBody, SheetContent, SheetTitle, Badge } = ui

const statusMap: Record<string, { className: string; label: string }> = {
  pending: { className: 'bg-yellow-500/20 text-yellow-400', label: 'Pending' },
  running: { className: 'bg-blue-500/20 text-blue-400', label: 'Running' },
  completed: {
    className: 'bg-emerald-500/20 text-emerald-400',
    label: 'Completed',
  },
  failed: { className: 'bg-red-500/20 text-red-400', label: 'Failed' },
}

function isNonEmpty(val: unknown): val is Record<string, unknown> {
  return (
    !!val && typeof val === 'object' && Object.keys(val as object).length > 0
  )
}

interface EvalItemSheetProps {
  item: any | null
  open: boolean
  onClose: () => void
  onPrevious: () => void
  onNext: () => void
  canGoPrevious: boolean
  canGoNext: boolean
}

export function EvalItemSheet({
  item,
  open,
  onClose,
  onPrevious,
  onNext,
  canGoPrevious,
  canGoNext,
}: EvalItemSheetProps) {
  if (!item) return null

  const status = statusMap[item.status?.toLowerCase()] ?? statusMap.pending
  const hasInput = isNonEmpty(item.input)
  const hasOutput = isNonEmpty(item.output)
  const hasExpectedOutput = isNonEmpty(item.expectedOutput)
  const hasScores = isNonEmpty(item.scores)
  const hasTokenUsage = isNonEmpty(item.tokenUsage)

  return (
    <Sheet open={open} onOpenChange={onClose}>
      <SheetContent
        side="right"
        className={`${evaluationSheetContentClass} sm:max-w-[760px]`}
      >
        <SheetHeader className="w-full">
          <SheetTitle className="w-full">
            <div className="flex w-full items-center justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2 py-2.5 text-sm font-semibold text-white light:text-brand-main-50">
                <NavigationButtons
                  canGoPrevious={canGoPrevious}
                  canGoNext={canGoNext}
                  onPrevious={onPrevious}
                  onNext={onNext}
                  previousLabel="Previous Item"
                  nextLabel="Next Item"
                  iconClassName="size-3"
                />
                <span className="text-white/70 text-xs font-mono light:text-black/70">
                  {item.id?.substring(0, 12)}...
                </span>
              </div>
              <Badge variant="secondary" className={status.className}>
                {status.label}
              </Badge>
            </div>
          </SheetTitle>
        </SheetHeader>
        <SheetBody className={evaluationSheetBodyClass}>
          <div className="space-y-4">
            {/* Overview */}
            <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
              <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                Overview
              </h3>
              <div className="grid grid-cols-3 gap-3 text-sm">
                <div className="space-y-1">
                  <div className="text-white/50 text-xs light:text-black/50">
                    Status
                  </div>
                  <Badge variant="secondary" className={status.className}>
                    {status.label}
                  </Badge>
                </div>
                <div className="space-y-1">
                  <div className="text-white/50 text-xs light:text-black/50">
                    Latency
                  </div>
                  <div className="text-white/90 font-mono text-xs light:text-black/90">
                    {item.latencyMs ? `${item.latencyMs}ms` : '-'}
                  </div>
                </div>
                <div className="space-y-1">
                  <div className="text-white/50 text-xs light:text-black/50">
                    Cost
                  </div>
                  <div className="text-white/90 font-mono text-xs light:text-black/90">
                    {item.cost ? `$${Number(item.cost).toFixed(4)}` : '-'}
                  </div>
                </div>
                {item.traceId && (
                  <div className="space-y-1">
                    <div className="text-white/50 text-xs light:text-black/50">
                      Trace ID
                    </div>
                    <div className="text-white/90 font-mono text-xs break-all light:text-black/90">
                      {item.traceId}
                    </div>
                  </div>
                )}
                {item.datasetItemId && (
                  <div className="space-y-1">
                    <div className="text-white/50 text-xs light:text-black/50">
                      Dataset Item
                    </div>
                    <div className="text-white/90 font-mono text-xs break-all light:text-black/90">
                      {item.datasetItemId}
                    </div>
                  </div>
                )}
              </div>
            </div>

            {/* Error */}
            {item.error && (
              <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
                <h3 className="text-sm font-semibold text-red-400">Error</h3>
                <pre className="text-xs text-red-300 bg-red-500/10 p-3 rounded border border-red-500/20 whitespace-pre-wrap max-h-[150px] overflow-y-auto scrollbar-macos">
                  {item.error}
                </pre>
              </div>
            )}

            {/* Scores */}
            {hasScores && (
              <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
                <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                  Scores
                </h3>
                <div className="grid grid-cols-3 gap-3">
                  {Object.entries(item.scores)
                    .filter(
                      ([name]) =>
                        !name.endsWith('_reason') && !name.endsWith('_error'),
                    )
                    .map(([name, value]) => {
                      const reason = item.scores[name + '_reason'] as
                        | string
                        | undefined
                      const error = item.scores[name + '_error'] as
                        | string
                        | undefined
                      return (
                        <div
                          key={name}
                          className="bg-white/5 rounded-md p-3 border border-white/10 light:bg-black/5 light:border-black/10"
                        >
                          <div className="text-white/50 text-xs uppercase tracking-wide light:text-black/50">
                            {name}
                          </div>
                          <div
                            className={cn(
                              'text-lg font-bold mt-1',
                              value === true
                                ? 'text-emerald-400'
                                : value === false
                                  ? 'text-red-400'
                                  : 'text-white light:text-brand-main-50',
                            )}
                          >
                            {value === true
                              ? 'Pass'
                              : value === false
                                ? 'Fail'
                                : typeof value === 'number'
                                  ? value.toFixed(2)
                                  : String(value)}
                          </div>
                          {reason && (
                            <p className="text-[11px] text-white/40 mt-1.5 leading-relaxed light:text-black/40">
                              {reason}
                            </p>
                          )}
                          {error && (
                            <p className="text-[11px] text-red-400/70 mt-1.5 leading-relaxed">
                              {error}
                            </p>
                          )}
                        </div>
                      )
                    })}
                </div>
              </div>
            )}

            {/* Token Usage */}
            {hasTokenUsage && (
              <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
                <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                  Token Usage
                </h3>
                <div className="grid grid-cols-3 gap-3 text-sm">
                  {item.tokenUsage.prompt_tokens != null && (
                    <div className="space-y-1">
                      <div className="text-white/50 text-xs light:text-black/50">
                        Input
                      </div>
                      <div className="text-white/90 font-mono light:text-black/90">
                        {Number(item.tokenUsage.prompt_tokens).toLocaleString()}
                      </div>
                    </div>
                  )}
                  {item.tokenUsage.completion_tokens != null && (
                    <div className="space-y-1">
                      <div className="text-white/50 text-xs light:text-black/50">
                        Output
                      </div>
                      <div className="text-white/90 font-mono light:text-black/90">
                        {Number(
                          item.tokenUsage.completion_tokens,
                        ).toLocaleString()}
                      </div>
                    </div>
                  )}
                  {item.tokenUsage.total_tokens != null && (
                    <div className="space-y-1">
                      <div className="text-white/50 text-xs light:text-black/50">
                        Total
                      </div>
                      <div className="text-white/90 font-mono light:text-black/90">
                        {Number(item.tokenUsage.total_tokens).toLocaleString()}
                      </div>
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* Input */}
            <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
              <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                Input
              </h3>
              {hasInput ? (
                <div className="bg-white/5 p-3 rounded border border-white/10 overflow-x-auto max-h-[300px] overflow-y-auto scrollbar-macos light:bg-black/5 light:border-black/10">
                  <JsonViewer
                    data={item.input}
                    collapsed={false}
                    showControls={true}
                  />
                </div>
              ) : (
                <div className="text-xs text-white/40 italic light:text-black/40">
                  No input data
                </div>
              )}
            </div>

            {/* Output */}
            <div className="space-y-2 border-b border-dotted border-white/10 pb-4 light:border-black/10">
              <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                Output
              </h3>
              {hasOutput ? (
                <div className="bg-white/5 p-3 rounded border border-white/10 overflow-x-auto max-h-[300px] overflow-y-auto scrollbar-macos light:bg-black/5 light:border-black/10">
                  <JsonViewer
                    data={item.output}
                    collapsed={false}
                    showControls={true}
                  />
                </div>
              ) : (
                <div className="text-xs text-white/40 italic light:text-black/40">
                  No output data
                </div>
              )}
            </div>

            {/* Expected Output */}
            {hasExpectedOutput && (
              <div className="space-y-2 pb-4">
                <h3 className="text-sm font-semibold text-white/80 light:text-black/80">
                  Expected Output
                </h3>
                <div className="bg-white/5 p-3 rounded border border-white/10 overflow-x-auto max-h-[300px] overflow-y-auto scrollbar-macos light:bg-black/5 light:border-black/10">
                  <JsonViewer
                    data={item.expectedOutput}
                    collapsed={false}
                    showControls={true}
                  />
                </div>
              </div>
            )}
          </div>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}
