import { useNavigate } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { Plus } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
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
      <Plus className="h-4 w-4" />
      New Eval Run
    </Button>
  )
}

export const EvaluationsHomeActions: ActionGroup[] = [
  {
    title: 'Evaluations',
    actions: [
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
