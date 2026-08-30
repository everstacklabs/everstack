import { useEffect, useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useEvalRuns, useDeleteEvalRun } from '@/hooks/evaluations/use-evals'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader, toast } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import {
  Activity,
  Clock,
  Check,
  X,
  Trash2,
  Flag,
  TrendingDown,
  Code2,
  Copy,
  ExternalLink,
  Plus,
  Search,
} from 'lucide-react'
import { ui } from '@everstack/ui'
import { evaluationInputClass } from '@/components/evaluations/evaluation-form'
import { highlightCode } from '@/lib/shiki'

const {
  Input,
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} = ui

export const Route = createFileRoute('/evaluations/runs')({
  component: EvalRunsPage,
})

const EVAL_RUNS_DOCS_URL =
  'https://docs.everstack.ai/getting-started/evaluations/runs'

type EvalRunSdkExample = {
  id: 'node' | 'python' | 'go'
  label: string
  language: string
  docsUrl: string
  code: string
}

const runStatusFilters = [
  { value: 'all', label: 'All' },
  { value: 'running', label: 'Running' },
  { value: 'completed', label: 'Completed' },
  { value: 'failed', label: 'Failed' },
  { value: 'cancelled', label: 'Cancelled' },
]

function StatusIcon({ status }: { status?: string }) {
  const s = status?.toLowerCase()
  if (s === 'completed')
    return <Check className="h-4 w-4 text-emerald-400 light:text-emerald-600" />
  if (s === 'running')
    return (
      <Activity className="h-4 w-4 text-blue-400 light:text-blue-600 animate-pulse" />
    )
  if (s === 'failed')
    return <X className="h-4 w-4 text-red-400 light:text-red-600" />
  if (s === 'cancelled')
    return <X className="h-4 w-4 text-white/40 light:text-black/40" />
  return <Clock className="h-4 w-4 text-yellow-400 light:text-yellow-700" />
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
      docsUrl: EVAL_RUNS_DOCS_URL,
      code: `import Everstack from "@everstack/node";

const client = new Everstack({
  apiKey: process.env.EVERSTACK_API_KEY!,
  baseUrl: process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089",
});

const run = await client.evaluations.runs.create({
  name: ${JSON.stringify(safeRunName)},
  datasetId: ${JSON.stringify(safeDatasetId)},
  evalTargetType: "model",
  scorerConfigIds: [${scorers}],
});

console.log(run.evalRun?.id);`,
    },
    {
      id: 'python',
      label: 'Python',
      language: 'python',
      docsUrl: EVAL_RUNS_DOCS_URL,
      code: `import os
from everstack import Everstack

client = Everstack(
    api_key=os.environ["EVERSTACK_API_KEY"],
    base_url=os.environ.get("EVERSTACK_GATEWAY_URL", "http://localhost:8089"),
)

run = client.evaluations.runs.create(
    name=${JSON.stringify(safeRunName)},
    dataset_id=${JSON.stringify(safeDatasetId)},
    eval_target_type="model",
    scorer_config_ids=[${scorers}],
)

print(run["evalRun"]["id"])`,
    },
    {
      id: 'go',
      label: 'Go',
      language: 'go',
      docsUrl: EVAL_RUNS_DOCS_URL,
      code: `run, err := client.Evaluations.Runs.Create(ctx, map[string]any{
  "name": ${JSON.stringify(safeRunName)},
  "datasetId": ${JSON.stringify(safeDatasetId)},
  "evalTargetType": "model",
  "scorerConfigIds": []string{${scorers}},
})
if err != nil {
  return err
}

fmt.Println(run["evalRun"].(map[string]any)["id"])`,
    },
  ]
}

function ShikiCodeBlock({
  code,
  language,
}: {
  code: string
  language: string
}) {
  const [highlighted, setHighlighted] = useState<{
    innerHtml: string
    codeClass: string
  } | null>(null)

  useEffect(() => {
    let cancelled = false
    setHighlighted(null)
    void highlightCode(code, language).then((result) => {
      if (!cancelled) setHighlighted(result)
    })
    return () => {
      cancelled = true
    }
  }, [code, language])

  return (
    <pre className="max-h-[340px] overflow-auto rounded border border-brand-main-700 bg-brand-main-900 p-4 text-xs leading-relaxed text-white/65 light:border-brand-main-200 light:bg-white light:text-black/65">
      {highlighted ? (
        <code
          className={highlighted.codeClass}
          dangerouslySetInnerHTML={{ __html: highlighted.innerHtml }}
        />
      ) : (
        <code className={`language-${language}`}>{code}</code>
      )}
    </pre>
  )
}

function EvalRunsPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Evaluation Runs"
        description="Run evaluations with scheduled runs, regression detection, and scoring."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <EvalRunsPageContent />
}

function EvalRunsPageContent() {
  const navigate = useNavigate()
  const { data: runs, isLoading } = useEvalRuns()
  const deleteMutation = useDeleteEvalRun()

  const [deleteTarget, setDeleteTarget] = useState<{
    id: string
    name: string
  } | null>(null)
  const [selectedSdk, setSelectedSdk] =
    useState<EvalRunSdkExample['id']>('node')
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      toast.success('Evaluation run deleted')
    } catch {
      toast.error('Failed to delete evaluation run')
    } finally {
      setDeleteTarget(null)
    }
  }

  const allRuns = runs ?? []
  const filteredRuns = useMemo(() => {
    const query = search.trim().toLowerCase()
    return allRuns.filter((run: any) => {
      const status = run.status?.toLowerCase()
      if (statusFilter !== 'all' && status !== statusFilter) return false
      if (!query) return true
      return [
        run.name,
        run.datasetName,
        run.datasetId,
        run.evalTargetType,
        run.status,
        run.id,
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
        .includes(query)
    })
  }, [allRuns, search, statusFilter])

  const columns: ColumnConfig<any>[] = [
    {
      id: 'name',
      header: 'Name',
      width: 220,
      minWidth: 140,
      render: (run: any) => (
        <span className="truncate font-medium text-brand-secondary-100 text-xs flex items-center gap-1.5">
          {run.name}
          {run.isBaseline && (
            <span className="inline-flex items-center gap-0.5 px-1 py-0.5 rounded text-[10px] font-medium bg-amber-500/20 text-amber-400 light:text-amber-700 border border-amber-500/30 flex-shrink-0">
              <Flag className="h-2.5 w-2.5" /> Baseline
            </span>
          )}
        </span>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      width: 80,
      minWidth: 60,
      render: (run: any) => (
        <span className="flex items-center gap-1.5">
          <StatusIcon status={run.status} />
          {run.regressionResult?.hasRegression && (
            <span title="Regression detected">
              <TrendingDown className="h-3.5 w-3.5 text-red-400 light:text-red-600" />
            </span>
          )}
        </span>
      ),
    },
    {
      id: 'dataset',
      header: 'Dataset',
      width: 180,
      minWidth: 120,
      render: (run: any) => (
        <span className="truncate text-xs text-brand-main-100">
          {run.datasetName ?? run.datasetId}
        </span>
      ),
    },
    {
      id: 'target',
      header: 'Target',
      width: 120,
      minWidth: 80,
      render: (run: any) => (
        <span className="truncate text-xs text-brand-main-100 capitalize">
          {run.evalTargetType}
        </span>
      ),
    },
    {
      id: 'progress',
      header: 'Progress',
      width: 120,
      minWidth: 80,
      render: (run: any) => (
        <span className="text-xs text-brand-main-100 font-mono">
          {run.completedItems ?? 0}/{run.totalItems ?? 0}
        </span>
      ),
    },
    {
      id: 'createdAt',
      header: 'Created',
      width: 160,
      minWidth: 140,
      render: (run: any) => (
        <span className="truncate text-xs text-brand-main-100">
          {run.createdAt ? formatTimestamp(run.createdAt) : '-'}
        </span>
      ),
    },
  ]

  const evalRunExamples = makeEvalRunSdkExamples({
    datasetId: undefined,
    runName: '',
    scorerConfigIds: [],
  })
  const selectedExample =
    evalRunExamples.find((example) => example.id === selectedSdk) ??
    evalRunExamples[0]

  const copySdkExample = async () => {
    await navigator.clipboard.writeText(selectedExample.code)
    toast.success(`Copied ${selectedExample.label} example`)
  }

  return (
    <div className="flex flex-col h-full w-full overflow-hidden">
      {!isLoading && allRuns.length > 0 && (
        <div className="shrink-0 border-b border-brand-main-700/70 bg-brand-main-950 light:border-brand-main-200 light:bg-white">
          <div className="flex flex-wrap items-center gap-3 px-4 py-2">
            <div className="relative min-w-[220px] flex-1">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/35" />
              <Input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search evaluation runs..."
                className={`${evaluationInputClass} pl-7`}
              />
            </div>
            <div className="flex rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
              {runStatusFilters.map((filter) => (
                <button
                  key={filter.value}
                  type="button"
                  onClick={() => setStatusFilter(filter.value)}
                  className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                    statusFilter === filter.value
                      ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                      : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                  }`}
                >
                  {filter.label}
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      {isLoading ? (
        <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
          <Loader loaderText="Loading eval runs..." />
        </div>
      ) : allRuns.length === 0 ? (
        <div className="flex flex-1 items-center justify-center overflow-y-auto px-6 py-8 scrollbar-macos">
          <div className="mx-auto flex max-w-6xl flex-col gap-6">
            <div className="mx-auto max-w-xl text-center">
              <div className="mx-auto mb-4 flex h-9 w-9 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70">
                <Iconify.Icon
                  icon="lucide:flask-conical"
                  className="size-4 text-brand-secondary-300"
                />
              </div>
              <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
                Get started with evaluations
              </h3>
              <p className="text-sm leading-relaxed text-white/45 light:text-black/50">
                Create a run from a dataset, attach scorers, and compare model
                or agent behavior over time.
              </p>
            </div>

            <div className="grid gap-4 lg:grid-cols-[minmax(0,1.15fr)_minmax(420px,0.85fr)]">
              <section className="rounded border border-brand-main-700/70 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
                <h4 className="mb-4 text-base font-semibold text-white light:text-brand-main-50">
                  Create evaluation
                </h4>
                <p className="mb-5 text-sm leading-relaxed text-white/45 light:text-black/50">
                  Start a run from a dedicated setup page where dataset,
                  scorers, target, and retrieval settings can be reviewed
                  together.
                </p>
                <div className="mb-5 grid gap-2 sm:grid-cols-3">
                  {['Dataset', 'Scorers', 'Target'].map((step) => (
                    <div
                      key={step}
                      className="rounded border border-brand-main-700 bg-brand-main-900 px-3 py-2 text-xs text-white/60 light:border-brand-main-200 light:bg-white light:text-black/60"
                    >
                      {step}
                    </div>
                  ))}
                </div>
                <Button
                  type="button"
                  onClick={() => void navigate({ to: '/evaluations/runs/new' })}
                >
                  <Plus className="h-4 w-4" />
                  Create Evaluation
                </Button>
              </section>

              <section className="flex flex-col rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50">
                <div className="flex items-center justify-between gap-3 border-b border-brand-main-700 px-4 py-3 light:border-brand-main-200">
                  <div className="flex min-w-0 items-center gap-2">
                    <Code2 className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
                    <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
                      SDK examples
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <a
                      href={selectedExample.docsUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex h-7 items-center gap-1 rounded border border-brand-main-700 px-2 text-xs text-white/65 transition-colors hover:border-brand-main-500 hover:text-white light:border-brand-main-200 light:text-black/60 light:hover:text-black"
                    >
                      <ExternalLink className="h-3 w-3" />
                      Docs
                    </a>
                    <Button
                      variant="outline"
                      onClick={copySdkExample}
                      className="h-7 px-2 text-xs"
                    >
                      <Copy className="h-3 w-3" />
                      Copy
                    </Button>
                  </div>
                </div>
                <div className="space-y-4 p-4">
                  <div className="flex rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
                    {evalRunExamples.map((example) => (
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
                  <ShikiCodeBlock
                    code={selectedExample.code}
                    language={selectedExample.language}
                  />
                  <Button
                    variant="default"
                    className="w-full"
                    onClick={() => void navigate({ to: '/evaluations/runs/new' })}
                  >
                    <Plus className="h-4 w-4" />
                    Start Evaluation Run
                  </Button>
                </div>
              </section>
            </div>
          </div>
        </div>
      ) : (
        <ResponsiveTable
          columns={columns}
          data={filteredRuns}
          enableResizing={true}
          minTableWidth="100%"
          emptyMessage="No evaluation runs match this view."
          onRowClick={(run: any) =>
            navigate({
              to: '/evaluations/runs/$runId',
              params: { runId: run.id },
            })
          }
          rowKey={(run: any) => run.id}
          rowActions={[
            {
              label: 'Delete',
              icon: <Trash2 className="w-4 h-4" />,
              variant: 'destructive',
              onClick: (run: any) => {
                setDeleteTarget({ id: run.id, name: run.name })
              },
            },
          ]}
        />
      )}

      {/* Delete Confirmation */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Evaluation Run</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete "{deleteTarget?.name}"? This will
              permanently remove the run and all its items. This action cannot
              be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-red-600 hover:bg-red-700"
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
