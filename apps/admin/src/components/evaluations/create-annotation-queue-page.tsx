import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { ClipboardCheck, ListChecks } from 'lucide-react'
import { useCreateQueue } from '@/hooks/evaluations/use-annotations'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import {
  EvaluationField,
  EvaluationInlineAction,
  evaluationErrorClass,
  evaluationInputClass,
  evaluationPanelClass,
  evaluationTextareaClass,
} from './evaluation-form'

const { Input, Textarea } = ui

export function CreateAnnotationQueuePage() {
  const navigate = useNavigate()
  const createMutation = useCreateQueue()
  const { data: scoreConfigs } = useScoreConfigs()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [selectedScoreConfigIds, setSelectedScoreConfigIds] = useState<
    string[]
  >([])

  const selectedScoreConfigs =
    scoreConfigs?.filter((scoreConfig: any) =>
      selectedScoreConfigIds.includes(scoreConfig.id),
    ) ?? []

  const toggleScoreConfig = (id: string) => {
    setSelectedScoreConfigIds((current) =>
      current.includes(id)
        ? current.filter((scoreId) => scoreId !== id)
        : [...current, id],
    )
  }

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault()

    try {
      const result = await createMutation.mutateAsync({
        name,
        description: description || undefined,
        scoreConfigIds: selectedScoreConfigIds,
      })
      toast.success('Annotation queue created')
      const queueId = (result as any)?.queue?.id
      if (queueId) {
        void navigate({
          to: '/evaluations/annotation-queues/$queueId',
          params: { queueId },
        })
        return
      }
      void navigate({ to: '/evaluations/annotation-queues' })
    } catch {
      toast.error('Failed to create annotation queue')
    }
  }

  return (
    <div className="flex h-full w-full justify-center overflow-y-auto px-6 py-8 scrollbar-macos">
      <div className="grid w-full max-w-5xl gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
        <section className="rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50">
          <div className="border-b border-brand-main-700 px-5 py-4 light:border-brand-main-200">
            <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
              Create annotation queue
            </h3>
          </div>

          <form onSubmit={handleCreate} className="space-y-5 p-5">
            {createMutation.error && (
              <div className={evaluationErrorClass}>
                {(createMutation.error as Error).message}
              </div>
            )}

            <EvaluationField label="Name" htmlFor="annotation-queue-name">
              <Input
                id="annotation-queue-name"
                placeholder="Customer support QA"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                className={evaluationInputClass}
              />
            </EvaluationField>

            <EvaluationField
              label="Description"
              htmlFor="annotation-queue-description"
            >
              <Textarea
                id="annotation-queue-description"
                placeholder="Describe the review work for this queue"
                value={description}
                onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) =>
                  setDescription(event.target.value)
                }
                rows={4}
                className={evaluationTextareaClass}
              />
            </EvaluationField>

            <EvaluationField
              label="Score configs"
              action={
                <EvaluationInlineAction
                  onClick={() =>
                    void navigate({ to: '/evaluations/score-configs/new' })
                  }
                >
                  Create scorer
                </EvaluationInlineAction>
              }
            >
              <div
                className={`${evaluationPanelClass} max-h-56 space-y-1 overflow-y-auto p-3`}
              >
                {!scoreConfigs || scoreConfigs.length === 0 ? (
                  <p className="text-sm text-white/45 light:text-black/45">
                    No score configs available.
                  </p>
                ) : (
                  scoreConfigs.map((scoreConfig: any) => (
                    <label
                      key={scoreConfig.id}
                      className="flex cursor-pointer items-center gap-2 py-1 text-sm text-zinc-300 hover:text-white light:text-zinc-700 light:hover:text-brand-main-50"
                    >
                      <input
                        type="checkbox"
                        checked={selectedScoreConfigIds.includes(
                          scoreConfig.id,
                        )}
                        onChange={() => toggleScoreConfig(scoreConfig.id)}
                        className="rounded border-brand-main-700"
                      />
                      <span className="min-w-0 flex-1 truncate">
                        {scoreConfig.name}
                      </span>
                      <span className="shrink-0 text-xs text-white/35 light:text-black/40">
                        {scoreConfig.dataType}
                      </span>
                    </label>
                  ))
                )}
              </div>
            </EvaluationField>

            <div className="flex items-center justify-between gap-3 border-t border-brand-main-700 pt-5 light:border-brand-main-200">
              <Button
                type="button"
                variant="outline"
                onClick={() =>
                  void navigate({ to: '/evaluations/annotation-queues' })
                }
                disabled={createMutation.isPending}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createMutation.isPending}>
                {createMutation.isPending ? 'Creating...' : 'Create queue'}
              </Button>
            </div>
          </form>
        </section>

        <aside className="flex flex-col gap-4">
          <section className="rounded border border-brand-main-700 bg-brand-main-950 p-4 light:border-brand-main-200 light:bg-brand-main-50">
            <div className="mb-4 flex items-center gap-2">
              <ClipboardCheck className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
              <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
                Queue preview
              </h3>
            </div>
            <div className="space-y-3 text-xs">
              <div className="rounded border border-brand-main-700 bg-brand-main-900 p-3 light:border-brand-main-200 light:bg-white">
                <div className="mb-1 text-white/35 light:text-black/40">
                  Name
                </div>
                <div className="truncate text-white light:text-brand-main-50">
                  {name || 'Untitled queue'}
                </div>
              </div>
              <div className="rounded border border-brand-main-700 bg-brand-main-900 p-3 light:border-brand-main-200 light:bg-white">
                <div className="mb-2 text-white/35 light:text-black/40">
                  Scorers
                </div>
                {selectedScoreConfigs.length > 0 ? (
                  <div className="flex flex-wrap gap-1.5">
                    {selectedScoreConfigs.map((scoreConfig: any) => (
                      <span
                        key={scoreConfig.id}
                        className="rounded bg-brand-main-700 px-2 py-1 text-white/70 light:bg-brand-main-100 light:text-brand-main-800"
                      >
                        {scoreConfig.name}
                      </span>
                    ))}
                  </div>
                ) : (
                  <div className="text-white/45 light:text-black/45">
                    No scorers selected
                  </div>
                )}
              </div>
            </div>
          </section>

          <section className="rounded border border-brand-main-700 bg-brand-main-950 p-4 light:border-brand-main-200 light:bg-brand-main-50">
            <div className="mb-3 flex items-center gap-2">
              <ListChecks className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
              <h3 className="text-xs font-medium text-white light:text-brand-main-50">
                Review stages
              </h3>
            </div>
            <div className="grid gap-2">
              {['Populate', 'Annotate', 'Export signal'].map((step) => (
                <div
                  key={step}
                  className="rounded border border-brand-main-700 bg-brand-main-900 px-3 py-2 text-xs text-white/60 light:border-brand-main-200 light:bg-white light:text-black/60"
                >
                  {step}
                </div>
              ))}
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}
