import { useCallback, useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useEvalRun,
  useEvalRunItems,
  useEvalRunSummary,
  useSetBaseline,
} from '@/hooks/evaluations/use-evals'
import { ui } from '@everstack/ui'
import { Button, Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
  Activity,
  CheckCircle,
  XCircle,
  Clock,
  BarChart3,
  Target,
  Check,
  X,
  Download,
  Flag,
  TrendingDown,
  ArrowDown,
} from 'lucide-react'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { EvalItemSheet } from '@/components/evaluations/eval-item-sheet'
import { useFileUpload } from '@/hooks/storage/use-file-upload'
import { ObjectPurpose } from '@/server/storage'

const {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
  Progress,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} = ui

export const Route = createFileRoute('/evaluations/runs_/$runId')({
  component: EvalRunDetailPage,
})

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

function MetricCard({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode
  label: string
  value: React.ReactNode
}) {
  return (
    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
      <CardContent>
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
              <div className="text-brand-secondary-300">{icon}</div>
            </div>
            <div className="min-w-0">
              <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide truncate">
                {label}
              </div>
            </div>
          </div>
          <div className="text-sm font-semibold text-white light:text-brand-main-50">
            {value}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function EvalRunDetailPage() {
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

  return <EvalRunDetailPageContent />
}

function EvalRunDetailPageContent() {
  const { runId } = Route.useParams()
  const { data: run, isLoading: runLoading } = useEvalRun(runId)
  const runStatus = (run as any)?.status?.toLowerCase()
  const isActive = runStatus === 'pending' || runStatus === 'running'
  const { data: items } = useEvalRunItems(runId, isActive)
  const { data: summary } = useEvalRunSummary(runId, isActive)

  const { upload: uploadToStorage, isUploading: isExportingToStorage } =
    useFileUpload()
  const setBaselineMutation = useSetBaseline()

  const exportResults = useCallback(
    (mode: 'csv' | 'json' | 'storage') => {
      if (!items || items.length === 0) return

      const rows = items.map((item: any) => ({
        input: item.input,
        output: item.output,
        status: item.status,
        error: item.error,
        scores: item.scores,
        latencyMs: item.latencyMs,
      }))

      if (mode === 'storage') {
        const content = JSON.stringify(rows, null, 2)
        const blob = new Blob([content], { type: 'application/json' })
        const file = new File([blob], `eval-run-${runId}.json`, {
          type: 'application/json',
        })
        uploadToStorage(file, ObjectPurpose.EVAL_RESULT, 'eval_run', runId)
          .then(() => toast.success('Results exported to storage'))
          .catch(() => toast.error('Failed to export to storage'))
        return
      }

      let content: string
      let mime: string
      let ext: string

      if (mode === 'json') {
        content = JSON.stringify(rows, null, 2)
        mime = 'application/json'
        ext = 'json'
      } else {
        const header = 'input,output,status,error,scores,latencyMs'
        const csvRows = rows.map(
          (r: any) =>
            `${JSON.stringify(JSON.stringify(r.input))},${JSON.stringify(JSON.stringify(r.output))},${r.status ?? ''},${r.error ?? ''},${JSON.stringify(JSON.stringify(r.scores ?? {}))},${r.latencyMs ?? ''}`,
        )
        content = `${header}\n${csvRows.join('\n')}`
        mime = 'text/csv'
        ext = 'csv'
      }

      const blob = new Blob([content], { type: mime })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `eval-run-${runId}.${ext}`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    },
    [items, runId, uploadToStorage],
  )

  const [selectedItemId, setSelectedItemId] = useState<string | null>(null)
  const itemsList = items ?? []
  const selectedItem = useMemo(
    () =>
      itemsList.find(
        (i: any) => (i.id ?? i.datasetItemId) === selectedItemId,
      ) ?? null,
    [itemsList, selectedItemId],
  )
  const selectedIndex = useMemo(
    () =>
      itemsList.findIndex(
        (i: any) => (i.id ?? i.datasetItemId) === selectedItemId,
      ),
    [itemsList, selectedItemId],
  )

  const handleRowClick = useCallback((item: any) => {
    setSelectedItemId(item.id ?? item.datasetItemId)
  }, [])

  const handlePrevious = useCallback(() => {
    if (selectedIndex > 0) {
      const prev = itemsList[selectedIndex - 1]
      setSelectedItemId(prev.id ?? prev.datasetItemId)
    }
  }, [itemsList, selectedIndex])

  const handleNext = useCallback(() => {
    if (selectedIndex < itemsList.length - 1) {
      const next = itemsList[selectedIndex + 1]
      setSelectedItemId(next.id ?? next.datasetItemId)
    }
  }, [itemsList, selectedIndex])

  const columns: ColumnConfig<any>[] = useMemo(
    () => [
      {
        id: 'input',
        header: 'Input',
        width: 280,
        minWidth: 150,
        render: (item: any) => {
          const val =
            item.input && Object.keys(item.input).length > 0
              ? JSON.stringify(item.input)
              : null
          return (
            <span className="truncate font-mono text-xs text-white light:text-brand-main-50">
              {val ?? '-'}
            </span>
          )
        },
      },
      {
        id: 'output',
        header: 'Output',
        width: 280,
        minWidth: 150,
        render: (item: any) => {
          const val =
            item.output && Object.keys(item.output).length > 0
              ? JSON.stringify(item.output)
              : null
          return (
            <span className="truncate font-mono text-xs text-white/60 light:text-black/60">
              {val ?? '-'}
            </span>
          )
        },
      },
      {
        id: 'status',
        header: 'Status',
        width: 60,
        minWidth: 50,
        render: (item: any) => <StatusIcon status={item.status} />,
      },
      {
        id: 'error',
        header: 'Error',
        width: 200,
        minWidth: 120,
        render: (item: any) => (
          <span className="truncate text-xs text-red-400 light:text-red-600">
            {item.error || '-'}
          </span>
        ),
      },
      {
        id: 'scores',
        header: 'Scores',
        width: 240,
        minWidth: 140,
        render: (item: any) => {
          if (!item.scores || Object.keys(item.scores).length === 0) {
            return (
              <span className="text-xs text-white/60 light:text-black/60">
                -
              </span>
            )
          }
          const entries = Object.entries(item.scores).filter(
            ([k]) => !k.endsWith('_reason') && !k.endsWith('_error'),
          )
          if (entries.length === 0) {
            return (
              <span className="text-xs text-white/60 light:text-black/60">
                -
              </span>
            )
          }
          return (
            <span className="text-xs text-white/60 light:text-black/60">
              {entries
                .map(([k, v]) => {
                  const display =
                    v === true
                      ? 'Pass'
                      : v === false
                        ? 'Fail'
                        : typeof v === 'number'
                          ? (v as number).toFixed(2)
                          : String(v)
                  return `${k}: ${display}`
                })
                .join(', ')}
            </span>
          )
        },
      },
      {
        id: 'latency',
        header: 'Latency',
        width: 100,
        minWidth: 80,
        render: (item: any) => (
          <span className="p-0.5 text-xs text-white/60 light:text-black/60">
            {item.latencyMs ? `${item.latencyMs}ms` : '-'}
          </span>
        ),
      },
      {
        id: 'trace',
        header: 'Trace',
        width: 110,
        minWidth: 80,
        render: (item: any) => {
          const tid: string | undefined = item.traceId || item.trace_id
          if (!tid)
            return (
              <span className="text-xs text-white/30 light:text-black/30">
                -
              </span>
            )
          return (
            <a
              href={`/observability/traces?trace=${encodeURIComponent(tid)}`}
              onClick={(e) => e.stopPropagation()}
              className="text-[11px] font-mono text-brand-secondary-300 hover:text-brand-secondary-200 hover:underline"
              title={`Open trace ${tid}`}
            >
              {tid.slice(0, 8)}…
            </a>
          )
        },
      },
    ],
    [],
  )

  if (runLoading) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <Loader loaderText="Loading eval run..." />
      </div>
    )
  }

  if (!run) {
    return (
      <div className="flex flex-col flex-1 items-center justify-center text-white/70 light:text-black/70 gap-4">
        <div className="text-center flex flex-col justify-center items-center space-y-2">
          <span className="bg-brand-secondary-200 rounded-md p-2 inline-block mb-4">
            <Iconify.Icon
              icon="heroicons:beaker"
              className="size-10 text-brand-secondary-700"
            />
          </span>
          <h3 className="text-lg font-medium text-white light:text-brand-main-50">
            Eval run not found
          </h3>
          <p className="text-sm w-2/3 mb-4 text-center text-white/60 light:text-black/60">
            The evaluation run you're looking for doesn't exist or has been
            deleted.
          </p>
        </div>
      </div>
    )
  }

  const totalItems = (run as any).totalItems ?? 0
  const completedItems = (run as any).completedItems ?? 0
  const failedItems = (run as any).failedItems ?? 0
  const processedItems = completedItems + failedItems
  const progressPercent =
    totalItems > 0 ? (processedItems / totalItems) * 100 : 0
  const isRunning = (run as any).status?.toLowerCase() === 'running'
  const isPending = (run as any).status?.toLowerCase() === 'pending'
  const isCompleted = (run as any).status?.toLowerCase() === 'completed'
  const isBaseline = (run as any)?.isBaseline === true
  const regressionResult = (run as any)?.regressionResult
  const hasRegression = regressionResult?.hasRegression === true
  const regressedScores =
    (regressionResult?.scores as any[])?.filter((s: any) => s.regressed) ?? []

  return (
    <div className="flex flex-col h-full w-full overflow-hidden">
      {/* Fixed header section */}
      <div className="flex-shrink-0 p-3 space-y-3">
        {/* Actions */}
        <div className="flex items-center justify-end gap-2">
          {isBaseline && (
            <span className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium bg-amber-500/20 text-amber-400 light:text-amber-700 border border-amber-500/30">
              <Flag className="h-3 w-3" /> Baseline
            </span>
          )}
          {isCompleted && !isBaseline && (
            <Button
              variant="outline"
              disabled={setBaselineMutation.isPending}
              onClick={() => {
                setBaselineMutation.mutate(runId, {
                  onSuccess: () => toast.success('Run set as baseline'),
                  onError: () => toast.error('Failed to set baseline'),
                })
              }}
            >
              <Flag className="w-3.5 h-3.5 mr-1.5" />
              {setBaselineMutation.isPending ? 'Setting...' : 'Set as Baseline'}
            </Button>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                disabled={!items?.length || isExportingToStorage}
              >
                <Download className="w-3.5 h-3.5 mr-1.5" />
                {isExportingToStorage ? 'Exporting...' : 'Export Results'}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onClick={() => exportResults('csv')}>
                Download as CSV
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => exportResults('json')}>
                Download as JSON
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => exportResults('storage')}>
                Export to Storage
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {/* Metric cards */}
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-2">
          <MetricCard
            icon={<Activity className="h-4 w-4" />}
            label="Status"
            value={<StatusIcon status={(run as any).status} />}
          />
          <MetricCard
            icon={<Target className="h-4 w-4" />}
            label="Total Items"
            value={totalItems}
          />
          <MetricCard
            icon={<CheckCircle className="h-4 w-4" />}
            label="Completed"
            value={completedItems}
          />
          <MetricCard
            icon={<XCircle className="h-4 w-4" />}
            label="Failed"
            value={failedItems}
          />
          <MetricCard
            icon={<Clock className="h-4 w-4" />}
            label="Progress"
            value={`${Math.round(progressPercent)}%`}
          />
          <MetricCard
            icon={<BarChart3 className="h-4 w-4" />}
            label="Evaluated"
            value={items?.length ?? 0}
          />
        </div>

        {/* Progress bar */}
        {(isRunning || isPending) && (
          <Card className="border-brand-main-600 bg-brand-main-900/50">
            <CardContent className="!py-3">
              <div className="space-y-2">
                <div className="flex justify-between text-xs">
                  <span className="text-white/40 light:text-black/40">
                    {processedItems} of {totalItems} items processed (
                    {completedItems} passed, {failedItems} failed)
                  </span>
                  <span className="text-white/40 light:text-black/40">
                    {Math.round(progressPercent)}%
                  </span>
                </div>
                <Progress value={progressPercent} className="h-1.5" />
              </div>
            </CardContent>
          </Card>
        )}

        {/* Score Summary */}
        {summary && (summary as any).scores?.length > 0 && (
          <Card className="border-brand-main-600 bg-brand-main-900/50">
            <CardHeader className="!pb-2">
              <CardTitle className="text-white light:text-brand-main-50 text-sm font-medium">
                Score Summary
              </CardTitle>
            </CardHeader>
            <CardContent className="!pt-0">
              <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                {(summary as any).scores.map((score: any) => (
                  <div
                    key={score.name}
                    className="bg-brand-main-800/50 rounded-md p-3 border border-brand-main-600"
                  >
                    <div className="text-white/40 light:text-black/40 text-xs uppercase tracking-wide">
                      {score.name}
                    </div>
                    <div className="text-xl font-bold text-white light:text-brand-main-50 mt-1">
                      {typeof score.avgScore === 'number'
                        ? score.avgScore.toFixed(2)
                        : (score.avgScore ?? '-')}
                    </div>
                    {score.minScore !== undefined && (
                      <div className="text-[10px] text-white/30 light:text-black/30 mt-1">
                        min: {score.minScore?.toFixed?.(2) ?? score.minScore} /
                        max: {score.maxScore?.toFixed?.(2) ?? score.maxScore}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        )}

        {/* Regression Result */}
        {hasRegression && regressedScores.length > 0 && (
          <Card className="border-red-500/30 bg-red-950/20 light:bg-red-50">
            <CardHeader className="!pb-2">
              <CardTitle className="text-red-400 light:text-red-600 text-sm font-medium flex items-center gap-2">
                <TrendingDown className="h-4 w-4" /> Regression Detected
              </CardTitle>
            </CardHeader>
            <CardContent className="!pt-0">
              <p className="text-xs text-white/50 light:text-black/50 mb-3">
                {regressedScores.length} score
                {regressedScores.length > 1 ? 's' : ''} regressed compared to
                baseline run
              </p>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                {regressedScores.map((score: any) => (
                  <div
                    key={score.scoreName}
                    className="bg-red-900/20 light:bg-red-100 rounded-md p-3 border border-red-500/20"
                  >
                    <div className="text-white/40 light:text-black/40 text-xs uppercase tracking-wide">
                      {score.scoreName}
                    </div>
                    <div className="flex items-center gap-2 mt-1">
                      <span className="text-white/50 light:text-black/50 text-sm">
                        {score.baselineAvg?.toFixed(2)}
                      </span>
                      <ArrowDown className="h-3 w-3 text-red-400 light:text-red-600" />
                      <span className="text-red-400 light:text-red-600 text-sm font-bold">
                        {score.currentAvg?.toFixed(2)}
                      </span>
                    </div>
                    <div className="text-[10px] text-red-400/60 light:text-red-600/60 mt-1">
                      {(score.deltaPercent * -100)?.toFixed(1)}% drop
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      {/* Table */}
      <ResponsiveTable
        columns={columns}
        data={itemsList}
        enableResizing={true}
        minTableWidth="100%"
        rowKey={(item: any) => item.id ?? item.datasetItemId}
        onRowClick={handleRowClick}
        rowClassName={(item: any) =>
          (item.id ?? item.datasetItemId) === selectedItemId
            ? 'bg-brand-secondary-500/20'
            : ''
        }
        emptyMessage={
          <div className="flex flex-col items-center justify-center gap-3">
            <span className="bg-brand-secondary-200 rounded-md p-2 inline-block">
              <Iconify.Icon
                icon="heroicons:beaker"
                className="size-8 text-brand-secondary-700"
              />
            </span>
            <p className="text-sm text-white/40 light:text-black/40">
              {isRunning || isPending
                ? 'Evaluation in progress...'
                : 'No items evaluated.'}
            </p>
          </div>
        }
      />

      <EvalItemSheet
        item={selectedItem}
        open={!!selectedItem}
        onClose={() => setSelectedItemId(null)}
        onPrevious={handlePrevious}
        onNext={handleNext}
        canGoPrevious={selectedIndex > 0}
        canGoNext={selectedIndex < itemsList.length - 1}
      />
    </div>
  )
}
