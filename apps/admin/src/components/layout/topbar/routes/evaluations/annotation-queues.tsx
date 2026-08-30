import { useNavigate } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

function NewQueueButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()

  if (gate.isBlocked) return null

  return (
    <Button
      variant="default"
      onClick={() => void navigate({ to: '/evaluations/annotation-queues/new' })}
    >
      New Queue
    </Button>
  )
}

export const EvaluationsAnnotationQueuesActions: ActionGroup[] = [
  {
    title: 'Annotation Queues',
    actions: [
      {
        type: 'custom',
        key: 'new-queue',
        requiredPermission: 'resource:create',
        label: 'New Queue',
        component: NewQueueButton,
      },
    ],
  },
]

export const EvaluationsAnnotationQueuesNewActions: ActionGroup[] = [
  {
    title: 'New Queue',
    actions: [],
  },
]
