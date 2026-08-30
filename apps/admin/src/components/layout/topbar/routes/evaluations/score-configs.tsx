import { useNavigate } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { Sparkles } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

function BuiltinMetricButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()

  if (gate.isBlocked) return null

  return (
    <Button
      variant="outline"
      onClick={() => void navigate({ to: '/evaluations/score-configs/new' })}
    >
      <Sparkles className="h-4 w-4" />
      Add Built-in Metric
    </Button>
  )
}

function NewScoreConfigButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()

  if (gate.isBlocked) return null

  return (
    <Button
      variant="default"
      onClick={() => void navigate({ to: '/evaluations/score-configs/new' })}
    >
      New Score Config
    </Button>
  )
}

export const EvaluationsScoreConfigsActions: ActionGroup[] = [
  {
    title: 'Score Configs',
    actions: [
      {
        type: 'custom',
        key: 'builtin-metric',
        label: 'Add Built-in Metric',
        component: BuiltinMetricButton,
      },
      {
        type: 'custom',
        key: 'new-score-config',
        requiredPermission: 'resource:create',
        label: 'New Score Config',
        component: NewScoreConfigButton,
      },
    ],
  },
]

export const EvaluationsScoreConfigsNewActions: ActionGroup[] = [
  {
    title: 'New Score Config',
    actions: [],
  },
]
