import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { Code2, Copy, Database, ExternalLink, Flag, Play } from 'lucide-react'
import { useGatewayModels } from '@/hooks/deployments/use-gateway-models'
import { useDatasets } from '@/hooks/evaluations/use-datasets'
import { useCreateEvalRun } from '@/hooks/evaluations/use-evals'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import {
  EvaluationDisclosure,
  EvaluationField,
  EvaluationInlineAction,
  evaluationErrorClass,
  evaluationInputClass,
  evaluationPanelClass,
  evaluationSelectContentClass,
  evaluationSelectTriggerClass,
  evaluationTextareaClass,
} from './evaluation-form'

const {
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} = ui

const EVAL_RUNS_DOCS_URL =
  'https://docs.everstack.ai/getting-started/evaluations/runs'

type EvalRunSdkExample = {
  id: 'node' | 'python' | 'go'
  label: string
  language: string
  code: string
}

function quotedList(values: string[]) {
  return values.map((value) => JSON.stringify(value)).join(', ')
}

function makeEvalRunSdkExamples({
  datasetId,
  runName,
  scorerConfigIds,
}: {
  datasetId?: string
  runName: string
  scorerConfigIds: string[]
}): EvalRunSdkExample[] {
  const safeRunName = runName.trim() || 'support-regression-run'
  const safeDatasetId = datasetId || 'dataset-id'
  const scorers = quotedList(scorerConfigIds)

  return [
    {
      id: 'node',
      label: 'Node',
      language: 'typescript',
      code: `await client.evaluations.runs.create({
  name: ${JSON.stringify(safeRunName)},
  datasetId: ${JSON.stringify(safeDatasetId)},
  evalTargetType: "model",
  scorerConfigIds: [${scorers}],
});`,
    },
    {
      id: 'python',
      label: 'Python',
      language: 'python',
      code: `client.evaluations.runs.create(
    name=${JSON.stringify(safeRunName)},
    dataset_id=${JSON.stringify(safeDatasetId)},
    eval_target_type="model",
    scorer_config_ids=[${scorers}],
)`,
    },
    {
      id: 'go',
      label: 'Go',
      language: 'go',
      code: `_, err := client.Evaluations.Runs.Create(ctx, map[string]any{
  "name": ${JSON.stringify(safeRunName)},
  "datasetId": ${JSON.stringify(safeDatasetId)},
  "evalTargetType": "model",
  "scorerConfigIds": []string{${scorers}},
})`,
    },
  ]
}

export function CreateEvalRunPage() {
  const navigate = useNavigate()
  const { data: datasets } = useDatasets()
  const { data: scoreConfigs } = useScoreConfigs()
  const { data: gatewayModels } = useGatewayModels()
  const createMutation = useCreateEvalRun()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [datasetId, setDatasetId] = useState('')
  const [targetType, setTargetType] = useState('model')
  const [modelId, setModelId] = useState('')
  const [selectedScorers, setSelectedScorers] = useState<string[]>([])
  const [retrievalEnabled, setRetrievalEnabled] = useState(false)
  const [retrievalCollections, setRetrievalCollections] = useState('')
  const [retrievalTopK, setRetrievalTopK] = useState('5')
  const [retrievalIncludeTrace, setRetrievalIncludeTrace] = useState(true)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [selectedSdk, setSelectedSdk] =
    useState<EvalRunSdkExample['id']>('node')

  const selectedDataset = datasets?.find((dataset: any) => dataset.id === datasetId)
  const selectedScorerConfigs = useMemo(
    () =>
      (scoreConfigs ?? []).filter((scoreConfig: any) =>
        selectedScorers.includes(scoreConfig.id),
      ),
    [scoreConfigs, selectedScorers],
  )

  const sdkExamples = makeEvalRunSdkExamples({
    datasetId,
    runName: name,
    scorerConfigIds: selectedScorers,
  })
  const selectedExample =
    sdkExamples.find((example) => example.id === selectedSdk) ?? sdkExamples[0]

  const toggleScorer = (id: string) => {
    setSelectedScorers((current) =>
      current.includes(id)
        ? current.filter((scoreId) => scoreId !== id)
        : [...current, id],
    )
  }

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault()

    try {
      const evalConfig: Record<string, any> = {}
      if (retrievalEnabled) {
        evalConfig.retrieval = {
          enabled: true,
          vector_collections: retrievalCollections
            ? retrievalCollections
                .split(',')
                .map((collection) => collection.trim())
                .filter(Boolean)
            : [],
          top_k: Number(retrievalTopK) || 5,
          include_trace_context: retrievalIncludeTrace,
        }
      }

      const result = await createMutation.mutateAsync({
        name,
        datasetId,
        description: description || undefined,
        evalTargetType: targetType,
        evalTargetId: targetType === 'model' ? modelId || undefined : undefined,
        scorerConfigIds: selectedScorers,
        evalConfig: Object.keys(evalConfig).length > 0 ? evalConfig : undefined,
      })

      toast.success('Evaluation run started successfully')
      const runId = (result as any)?.evalRun?.id
      if (runId) {
        void navigate({
          to: '/evaluations/runs/$runId',
          params: { runId },
        })
        return
      }
      void navigate({ to: '/evaluations/runs' })
    } catch {
      toast.error('Failed to start evaluation run')
    }
  }

  const copySdkExample = async () => {
    await navigator.clipboard.writeText(selectedExample.code)
    toast.success(`Copied ${selectedExample.label} example`)
  }

  return (
    <div className="flex h-full w-full justify-center overflow-y-auto px-6 py-8 scrollbar-macos">
      <div className="grid w-full max-w-6xl gap-4 lg:grid-cols-[minmax(0,1.08fr)_minmax(360px,0.92fr)]">
        <section className="rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50">
          <div className="border-b border-brand-main-700 px-5 py-4 light:border-brand-main-200">
            <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
              Create evaluation run
            </h3>
          </div>

          <form onSubmit={handleCreate} className="space-y-5 p-5">
            {createMutation.error && (
              <div className={evaluationErrorClass}>
                {(createMutation.error as Error).message}
              </div>
            )}

            <EvaluationField label="Run name">
              <Input
                placeholder="Support regression run"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                className={evaluationInputClass}
              />
            </EvaluationField>

            <EvaluationField
              label="Dataset"
              action={
                <EvaluationInlineAction
                  onClick={() => void navigate({ to: '/evaluations/datasets' })}
                >
                  Create or import dataset
                </EvaluationInlineAction>
              }
            >
              <Select value={datasetId} onValueChange={setDatasetId}>
                <SelectTrigger className={evaluationSelectTriggerClass}>
                  <SelectValue placeholder="Select a dataset" />
                </SelectTrigger>
                <SelectContent className={evaluationSelectContentClass}>
                  {datasets?.map((dataset: any) => (
                    <SelectItem key={dataset.id} value={dataset.id}>
                      {dataset.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </EvaluationField>

            <EvaluationField
              label="Scorers"
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
                className={`${evaluationPanelClass} max-h-40 space-y-1 overflow-y-auto p-3`}
              >
                {!scoreConfigs || scoreConfigs.length === 0 ? (
                  <p className="text-sm text-white/45 light:text-black/45">
                    No score configs available. Create some first.
                  </p>
                ) : (
                  scoreConfigs.map((scoreConfig: any) => (
                    <label
                      key={scoreConfig.id}
                      className="flex cursor-pointer items-center gap-2 py-1 text-sm text-zinc-300 hover:text-white light:text-zinc-700 light:hover:text-brand-main-50"
                    >
                      <input
                        type="checkbox"
                        checked={selectedScorers.includes(scoreConfig.id)}
                        onChange={() => toggleScorer(scoreConfig.id)}
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

            <EvaluationDisclosure
              label="Advanced"
              open={advancedOpen}
              onOpenChange={setAdvancedOpen}
            >
              <EvaluationField label="Description">
                <Textarea
                  placeholder="What are you evaluating?"
                  value={description}
                  onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) =>
                    setDescription(event.target.value)
                  }
                  rows={3}
                  className={evaluationTextareaClass}
                />
              </EvaluationField>

              <EvaluationField label="Target type">
                <Select value={targetType} onValueChange={setTargetType}>
                  <SelectTrigger className={evaluationSelectTriggerClass}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className={evaluationSelectContentClass}>
                    <SelectItem value="model">Model</SelectItem>
                    <SelectItem value="agent">Agent</SelectItem>
                    <SelectItem value="workflow">Workflow</SelectItem>
                    <SelectItem value="function">Function</SelectItem>
                  </SelectContent>
                </Select>
              </EvaluationField>

              {targetType === 'model' && (
                <EvaluationField label="Model">
                  <Select value={modelId} onValueChange={setModelId}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue placeholder="Select a model (optional)" />
                    </SelectTrigger>
                    <SelectContent
                      className={`${evaluationSelectContentClass} max-h-64`}
                    >
                      {gatewayModels && gatewayModels.length > 0 ? (
                        gatewayModels.flatMap((provider) =>
                          (provider.models ?? []).map((model) => (
                            <SelectItem
                              key={`${provider.provider}-${model}`}
                              value={model}
                            >
                              {model} ({provider.provider})
                            </SelectItem>
                          )),
                        )
                      ) : (
                        <SelectItem value="__none__" disabled>
                          No models available
                        </SelectItem>
                      )}
                    </SelectContent>
                  </Select>
                </EvaluationField>
              )}

              <div className="space-y-3">
                <label className="flex items-center gap-2 text-sm text-white light:text-brand-main-50">
                  <input
                    type="checkbox"
                    checked={retrievalEnabled}
                    onChange={(event) =>
                      setRetrievalEnabled(event.target.checked)
                    }
                    className="rounded border-brand-main-600 bg-brand-main-900"
                  />
                  Context retrieval
                </label>
                {retrievalEnabled && (
                  <div className="space-y-4 border-l border-brand-main-700/70 pl-4 light:border-black/10">
                    <EvaluationField label="Vector collection IDs">
                      <Input
                        placeholder="collection-id-1, collection-id-2"
                        value={retrievalCollections}
                        onChange={(event) =>
                          setRetrievalCollections(event.target.value)
                        }
                        className={evaluationInputClass}
                      />
                    </EvaluationField>
                    <EvaluationField label="Top K">
                      <Input
                        type="number"
                        placeholder="5"
                        value={retrievalTopK}
                        onChange={(event) => setRetrievalTopK(event.target.value)}
                        className={`${evaluationInputClass} w-24`}
                      />
                    </EvaluationField>
                    <label className="flex items-center gap-2 text-sm text-white/80 light:text-black/80">
                      <input
                        type="checkbox"
                        checked={retrievalIncludeTrace}
                        onChange={(event) =>
                          setRetrievalIncludeTrace(event.target.checked)
                        }
                        className="rounded border-brand-main-600 bg-brand-main-900"
                      />
                      Include trace context
                    </label>
                  </div>
                )}
              </div>
            </EvaluationDisclosure>

            <div className="flex items-center justify-between gap-3 border-t border-brand-main-700 pt-5 light:border-brand-main-200">
              <Button
                type="button"
                variant="outline"
                onClick={() => void navigate({ to: '/evaluations/runs' })}
                disabled={createMutation.isPending}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={createMutation.isPending || !datasetId}
              >
                <Play className="h-4 w-4" />
                {createMutation.isPending ? 'Starting...' : 'Start run'}
              </Button>
            </div>
          </form>
        </section>

        <aside className="flex flex-col gap-4">
          <section className="rounded border border-brand-main-700 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
            <div className="mb-4 flex items-center gap-2">
              <Flag className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
              <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
                Run preview
              </h3>
            </div>
            <div className="grid gap-3 text-xs">
              <div className="rounded border border-brand-main-700 bg-brand-main-900 p-3 light:border-brand-main-200 light:bg-white">
                <div className="mb-1 text-white/35 light:text-black/40">
                  Dataset
                </div>
                <div className="flex min-w-0 items-center gap-2 text-white light:text-brand-main-50">
                  <Database className="h-3.5 w-3.5 shrink-0 text-brand-secondary-300 light:text-brand-secondary-700" />
                  <span className="truncate">
                    {selectedDataset?.name ?? 'No dataset selected'}
                  </span>
                </div>
              </div>
              <div className="rounded border border-brand-main-700 bg-brand-main-900 p-3 light:border-brand-main-200 light:bg-white">
                <div className="mb-2 text-white/35 light:text-black/40">
                  Scorers
                </div>
                {selectedScorerConfigs.length > 0 ? (
                  <div className="flex flex-wrap gap-1.5">
                    {selectedScorerConfigs.map((scoreConfig: any) => (
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

          <section className="flex min-h-0 flex-col rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50">
            <div className="flex items-center justify-between gap-3 border-b border-brand-main-700 px-4 py-3 light:border-brand-main-200">
              <div className="flex min-w-0 items-center gap-2">
                <Code2 className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
                <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
                  SDK example
                </span>
              </div>
              <div className="flex items-center gap-2">
                <a
                  href={EVAL_RUNS_DOCS_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex h-7 items-center gap-1 rounded border border-brand-main-700 px-2 text-xs text-white/65 transition-colors hover:border-brand-main-500 hover:text-white light:border-brand-main-200 light:text-black/60 light:hover:text-black"
                >
                  <ExternalLink className="h-3 w-3" />
                  Docs
                </a>
                <Button variant="outline" onClick={copySdkExample}>
                  <Copy className="h-3 w-3" />
                  Copy
                </Button>
              </div>
            </div>
            <div className="space-y-4 p-4">
              <div className="flex rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
                {sdkExamples.map((example) => (
                  <button
                    key={example.id}
                    type="button"
                    onClick={() => setSelectedSdk(example.id)}
                    className={`flex-1 rounded px-2 py-1 text-xs font-medium transition-colors ${
                      selectedSdk === example.id
                        ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                        : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                    }`}
                  >
                    {example.label}
                  </button>
                ))}
              </div>
              <pre className="max-h-[260px] overflow-auto rounded border border-brand-main-700 bg-brand-main-900 p-4 text-xs leading-relaxed text-white/65 light:border-brand-main-200 light:bg-white light:text-black/65">
                <code className={`language-${selectedExample.language}`}>
                  {selectedExample.code}
                </code>
              </pre>
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}
