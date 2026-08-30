import { useNavigate } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { Plus } from 'lucide-react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { useCreatePlayground } from '@/hooks/evaluations/use-playgrounds'

function NewPlaygroundButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()
  const createMutation = useCreatePlayground()

  if (gate.isBlocked) return null

  const create = async () => {
    try {
      const pg = await createMutation.mutateAsync({
        name: 'Untitled playground',
        config: {},
      })
      if (pg?.id) {
        void navigate({ to: '/evaluations/playground', search: { id: pg.id } })
      } else {
        toast.error('Failed to create playground')
      }
    } catch {
      toast.error('Failed to create playground')
    }
  }

  return (
    <Button
      variant="default"
      onClick={() => void create()}
      disabled={createMutation.isPending}
    >
      <Plus className="h-4 w-4" />
      New Playground
    </Button>
  )
}

export const EvaluationsPlaygroundsActions: ActionGroup[] = [
  {
    title: 'Playgrounds',
    actions: [
      {
        type: 'custom',
        key: 'new-playground',
        label: 'New Playground',
        component: NewPlaygroundButton,
      },
    ],
  },
]
