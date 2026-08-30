import { useNavigate } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { CompareRunsButton } from '@/components/evaluations/eval-run-select-sheet'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

function NewEvalRunButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()

  if (gate.isBlocked) return null

  return (
    <Button
      variant="default"
      onClick={() => void navigate({ to: '/evaluations/runs/new' })}
    >
      Create Eval Run
    </Button>
  )
}

export const EvaluationsRunsActions: ActionGroup[] = [
  {
    title: 'Eval Runs',
    actions: [
      {
        type: 'custom',
        key: 'compare-runs',
        label: 'Compare',
        component: CompareRunsButton,
      },
      {
        type: 'custom',
        key: 'new-eval-run',
        requiredPermission: 'resource:create',
        label: 'New Eval Run',
        component: NewEvalRunButton,
      },
    ],
  },
]

export const EvaluationsRunsNewActions: ActionGroup[] = [
  {
    title: 'New Eval Run',
    actions: [],
  },
]
