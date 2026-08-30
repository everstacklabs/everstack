import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { GitCompare } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useEvalRuns } from '@/hooks/evaluations/use-evals'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import {
  evaluationPanelClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetFooterClass,
} from '@/components/evaluations/evaluation-form'

const { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } =
  ui

type CompareRunsButtonProps = {
  /** Pre-checked run ids when the sheet opens (e.g. the current comparison). */
  initialRunIds?: string[]
  label?: string
  variant?: string
}

/**
 * Topbar control: pick two or more eval runs and open the comparison view.
 * Shared by the runs list ("Compare") and the compare page ("Edit selection").
 */
export function CompareRunsButton({
  initialRunIds = [],
  label = 'Compare',
  variant = 'outline',
}: CompareRunsButtonProps) {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()
  const { data: runs } = useEvalRuns()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string[]>(initialRunIds)

  if (gate.isBlocked) return null

  const toggle = (id: string) =>
    setSelected((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    )

  const go = () => {
    if (selected.length < 2) {
      toast.error('Pick at least two runs to compare')
      return
    }
    void navigate({
      to: '/evaluations/runs/compare',
      search: { runs: selected.join(',') },
    })
    setOpen(false)
  }

  return (
    <>
      <Button
        variant={variant}
        onClick={() => {
          setSelected(initialRunIds)
          setOpen(true)
        }}
      >
        <GitCompare className="h-3.5 w-3.5 mr-1.5" />
        {label}
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="right"
          className={`${evaluationSheetContentClass} sm:max-w-[460px]`}
        >
          <SheetHeader>
            <SheetTitle>Compare Eval Runs</SheetTitle>
          </SheetHeader>
          <SheetBody className={evaluationSheetBodyClass}>
            {!runs || runs.length === 0 ? (
              <p className="text-xs text-white/50 light:text-black/50">
                No eval runs yet.
              </p>
            ) : (
              <div className="space-y-1.5">
                {runs.map((run) => {
                  const checked = selected.includes(run.id)
                  return (
                    <button
                      key={run.id}
                      type="button"
                      onClick={() => toggle(run.id)}
                      className={cn(
                        `${evaluationPanelClass} flex w-full items-center gap-3 px-3 py-2 text-left transition-colors`,
                        checked
                          ? 'border-brand-secondary-500/50 bg-brand-secondary-500/5'
                          : 'hover:border-brand-main-500 hover:bg-brand-main-900/70',
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        readOnly
                        className="rounded border-brand-main-600 pointer-events-none"
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block text-xs font-medium text-brand-secondary-100 truncate">
                          {run.name}
                        </span>
                        <span className="block text-[10px] text-white/40 mt-0.5 light:text-black/40">
                          {(run as { status?: string }).status ?? 'unknown'}
                          {run.createdAt
                            ? ` · ${formatTimestamp(run.createdAt)}`
                            : ''}
                        </span>
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
          </SheetBody>
          <SheetFooter className={evaluationSheetFooterClass}>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={go} disabled={selected.length < 2}>
              Compare {selected.length > 0 ? `(${selected.length})` : ''}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </>
  )
}
