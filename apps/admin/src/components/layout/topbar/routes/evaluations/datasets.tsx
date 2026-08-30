import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useCreateDataset } from '@/hooks/evaluations/use-datasets'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import {
  EvaluationField,
  evaluationErrorClass,
  evaluationInputClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetFooterClass,
  evaluationTextareaClass,
} from '@/components/evaluations/evaluation-form'

const {
  Input,
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  Textarea,
} = ui

function NewDatasetButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()
  const createMutation = useCreateDataset()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  if (gate.isBlocked) return null

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      const response = (await createMutation.mutateAsync({
        name,
        description: description || undefined,
      })) as { dataset?: { id?: string } }
      setDialogOpen(false)
      setName('')
      setDescription('')
      toast.success('Dataset created successfully')
      if (response.dataset?.id) {
        void navigate({
          to: '/evaluations/datasets/$datasetId',
          params: { datasetId: response.dataset.id },
        })
      }
    } catch {
      toast.error('Failed to create dataset')
    }
  }

  return (
    <>
      <Button variant="default" onClick={() => setDialogOpen(true)}>
        <div className="flex items-center gap-2">New Dataset</div>
      </Button>

      <Sheet open={dialogOpen} onOpenChange={setDialogOpen}>
        <SheetContent
          side="right"
          className={`${evaluationSheetContentClass} sm:max-w-[480px]`}
        >
          <SheetHeader>
            <SheetTitle>Create Dataset</SheetTitle>
          </SheetHeader>

          <form
            onSubmit={handleCreate}
            className="flex min-h-0 flex-1 flex-col"
          >
            <SheetBody className={evaluationSheetBodyClass}>
              {createMutation.error && (
                <div className={evaluationErrorClass}>
                  {(createMutation.error as Error).message}
                </div>
              )}

              <EvaluationField label="Name" htmlFor="topbar-dataset-name">
                <Input
                  id="topbar-dataset-name"
                  placeholder="Support regression set"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className={evaluationInputClass}
                />
              </EvaluationField>
              <EvaluationField
                label="Description"
                htmlFor="topbar-dataset-description"
              >
                <Textarea
                  id="topbar-dataset-description"
                  placeholder="What behavior should this dataset protect?"
                  value={description}
                  onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                    setDescription(e.target.value)
                  }
                  rows={4}
                  className={evaluationTextareaClass}
                />
              </EvaluationField>
            </SheetBody>

            <SheetFooter className={evaluationSheetFooterClass}>
              <Button
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
                disabled={createMutation.isPending}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createMutation.isPending}>
                {createMutation.isPending ? 'Creating...' : 'Create Dataset'}
              </Button>
            </SheetFooter>
          </form>
        </SheetContent>
      </Sheet>
    </>
  )
}

export const EvaluationsDatasetsActions: ActionGroup[] = [
  {
    title: 'Datasets',
    actions: [
      {
        type: 'custom',
        key: 'new-dataset',
        requiredPermission: 'resource:create',
        label: 'New Dataset',
        component: NewDatasetButton,
      },
    ],
  },
]
