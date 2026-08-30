import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import {
  useSamplingEvalRules,
  useCreateSamplingEvalRule,
  useUpdateSamplingEvalRule,
  useDeleteSamplingEvalRule,
  useRunSamplingEvalRuleNow,
} from '@/hooks/evaluations/use-evals'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Button, Loader, Slider, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { Activity, Pause, Play, Plus, Search, Trash2, Zap } from 'lucide-react'
import {
  EvaluationField,
  EvaluationDisclosure,
  EvaluationInlineAction,
  evaluationErrorClass,
  evaluationInputClass,
  evaluationPanelClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetFooterClass,
  evaluationTextareaClass,
} from '@/components/evaluations/evaluation-form'

const {
  Input,
  Textarea,
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetBody,
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} = ui

export const Route = createFileRoute('/evaluations/online-evals')({
  component: OnlineEvalsPage,
})

function OnlineEvalsPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Online Evals"
        description="Continuously score a sample of your production traces so quality is measured where it actually happens."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <OnlineEvalsPageContent />
}

function OnlineEvalsPageContent() {
  const { data: rules, isLoading } = useSamplingEvalRules()
  const { data: scoreConfigs } = useScoreConfigs()
  const createMutation = useCreateSamplingEvalRule()
  const updateMutation = useUpdateSamplingEvalRule()
  const deleteMutation = useDeleteSamplingEvalRule()
  const runNowMutation = useRunSamplingEvalRuleNow()

  const [search, setSearch] = useState('')
  const [sheetOpen, setSheetOpen] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)
  const [runningId, setRunningId] = useState<string | null>(null)

  // Create form state
  const [name, setName] = useState('')
  const [selectedScorers, setSelectedScorers] = useState<string[]>([])
  const [sampleRate, setSampleRate] = useState(0.1)
  const [intervalSeconds, setIntervalSeconds] = useState('300')
  const [lookbackSeconds, setLookbackSeconds] = useState('3600')
  const [description, setDescription] = useState('')
  const [filterJson, setFilterJson] = useState('')
  const [filterError, setFilterError] = useState<string | null>(null)
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const allRules = (rules ?? []) as any[]
  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return allRules
    return allRules.filter((r) =>
      [r.name, r.description].filter(Boolean).join(' ').toLowerCase().includes(query),
    )
  }, [allRules, search])

  const toggleScorer = (id: string) =>
    setSelectedScorers((current) =>
      current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
    )

  const resetForm = () => {
    setName('')
    setSelectedScorers([])
    setSampleRate(0.1)
    setIntervalSeconds('300')
    setLookbackSeconds('3600')
    setDescription('')
    setFilterJson('')
    setFilterError(null)
    setAdvancedOpen(false)
  }

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault()
    let filterPredicate: Record<string, any> | undefined
    if (filterJson.trim()) {
      try {
        filterPredicate = JSON.parse(filterJson)
        setFilterError(null)
      } catch {
        setFilterError('Filter must be valid JSON')
        return
      }
    }
    try {
      await createMutation.mutateAsync({
        name,
        description: description || undefined,
        filterPredicate,
        sampleRate,
        scorerConfigIds: selectedScorers,
        lookbackSeconds: Number(lookbackSeconds) || undefined,
        intervalSeconds: Number(intervalSeconds) || 0,
        enabled: true,
      })
      toast.success('Online eval rule created')
      resetForm()
      setSheetOpen(false)
    } catch {
      toast.error('Failed to create rule')
    }
  }

  const handleToggle = async (row: any) => {
    try {
      await updateMutation.mutateAsync({
        id: row.id,
        enabled: !row.enabled,
        scorerConfigIds: row.scorerConfigIds ?? [],
      })
      toast.success(row.enabled ? 'Rule paused' : 'Rule resumed')
    } catch {
      toast.error('Failed to update rule')
    }
  }

  const handleRunNow = async (row: any) => {
    setRunningId(row.id)
    try {
      const res = await runNowMutation.mutateAsync(row.id)
      if (res.error) {
        toast.error(res.error)
      } else {
        toast.success(
          `Matched ${res.tracesMatched}, sampled ${res.tracesSampled}, scored ${res.scoresRecorded}`,
        )
      }
    } catch {
      toast.error('Failed to run rule')
    } finally {
      setRunningId(null)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      toast.success('Rule deleted')
      setDeleteTarget(null)
    } catch {
      toast.error('Failed to delete rule')
    }
  }

  const columns: ColumnConfig<any>[] = useMemo(
    () => [
      {
        id: 'name',
        header: 'Name',
        width: 220,
        minWidth: 160,
        render: (row) => (
          <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
            {row.name}
          </span>
        ),
      },
      {
        id: 'status',
        header: 'Status',
        width: 110,
        minWidth: 90,
        render: (row) => (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
              row.enabled
                ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600'
                : 'bg-white/10 text-white/45 light:bg-black/10 light:text-black/45'
            }`}
          >
            {row.enabled ? 'Active' : 'Paused'}
          </span>
        ),
      },
      {
        id: 'sampleRate',
        header: 'Sample',
        width: 90,
        minWidth: 70,
        render: (row) => (
          <span className="font-mono text-xs tabular-nums text-white/70 light:text-black/70">
            {Math.round((Number(row.sampleRate) || 0) * 100)}%
          </span>
        ),
      },
      {
        id: 'interval',
        header: 'Interval',
        width: 110,
        minWidth: 90,
        render: (row) => (
          <span className="text-xs text-white/60 light:text-black/60">
            {Number(row.intervalSeconds) > 0 ? `${row.intervalSeconds}s` : 'Manual'}
          </span>
        ),
      },
      {
        id: 'scorers',
        header: 'Scorers',
        width: 90,
        minWidth: 70,
        render: (row) => (
          <span className="text-xs text-white/55 light:text-black/55">
            {row.scorerConfigIds?.length ?? 0}
          </span>
        ),
      },
      {
        id: 'lastRun',
        header: 'Last run',
        width: 160,
        minWidth: 120,
        render: (row) => (
          <span className="text-xs text-white/50 light:text-black/55">
            {Number(row.lastRunTraceCount) > 0
              ? `${row.lastRunTraceCount} traces`
              : row.lastRunError
                ? 'Error'
                : '-'}
          </span>
        ),
      },
    ],
    [],
  )

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      <div className="shrink-0 border-b border-brand-main-700/70 bg-brand-main-950 light:border-brand-main-200 light:bg-white">
        <div className="flex flex-wrap items-center gap-3 px-4 py-2">
          <div className="relative min-w-[220px] flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/35" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search rules..."
              className={`${evaluationInputClass} pl-7`}
            />
          </div>
          <Button size="sm" onClick={() => setSheetOpen(true)}>
            <Plus className="h-4 w-4" />
            New
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
          <Loader loaderText="Loading rules..." />
        </div>
      ) : allRules.length === 0 ? (
        <div className="flex flex-1 items-center justify-center px-6 py-8">
          <div className="mx-auto max-w-md text-center">
            <div className="mx-auto mb-4 flex h-9 w-9 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70 light:border-brand-main-200 light:bg-white">
              <Activity className="size-4 text-brand-secondary-300 light:text-brand-secondary-700" />
            </div>
            <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
              Score production, continuously
            </h3>
            <p className="mb-5 text-sm leading-relaxed text-white/45 light:text-black/50">
              Sample a slice of live traces and run your scorers on them
              automatically. Quality trends show up where users actually are.
            </p>
            <Button onClick={() => setSheetOpen(true)}>
              <Plus className="h-4 w-4" />
              New Rule
            </Button>
          </div>
        </div>
      ) : (
        <ResponsiveTable
          tableId="evaluations-online-evals"
          columns={columns}
          data={filtered}
          rowKey={(row) => row.id}
          minTableWidth="100%"
          emptyMessage="No rules match this view."
          rowActions={[
            {
              label: 'Run now',
              icon: <Zap className="h-4 w-4" />,
              disabled: (row: any) => runningId === row.id,
              onClick: (row) => void handleRunNow(row),
            },
            {
              label: 'Toggle',
              icon: <Pause className="h-4 w-4" />,
              onClick: (row) => void handleToggle(row),
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-4 w-4" />,
              variant: 'destructive',
              onClick: (row) => setDeleteTarget({ id: row.id, name: row.name }),
            },
          ]}
        />
      )}

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent side="right" className={evaluationSheetContentClass}>
          <SheetHeader>
            <SheetTitle>New Online Eval Rule</SheetTitle>
          </SheetHeader>
          <form onSubmit={handleCreate} className="flex min-h-0 flex-1 flex-col">
            <SheetBody className={evaluationSheetBodyClass}>
              {createMutation.error && (
                <div className={evaluationErrorClass}>
                  {(createMutation.error as Error).message}
                </div>
              )}

              <EvaluationField label="Name">
                <Input
                  placeholder="Production faithfulness sampling"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                  className={evaluationInputClass}
                />
              </EvaluationField>

              <EvaluationField
                label="Sample rate"
                action={
                  <span className="font-mono text-xs tabular-nums text-brand-secondary-300 light:text-brand-secondary-700">
                    {Math.round(sampleRate * 100)}%
                  </span>
                }
              >
                <Slider
                  value={[sampleRate]}
                  onValueChange={([value]) => setSampleRate(value ?? 0)}
                  min={0}
                  max={1}
                  step={0.05}
                />
                <p className="text-[11px] text-white/35 light:text-black/40">
                  1–10% for high volume, 50–100% for critical paths.
                </p>
              </EvaluationField>

              <EvaluationField
                label="Scorers"
                action={
                  <EvaluationInlineAction>{`${selectedScorers.length} selected`}</EvaluationInlineAction>
                }
              >
                <div className={`${evaluationPanelClass} max-h-40 space-y-1 overflow-y-auto p-3`}>
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
                        <span className="min-w-0 flex-1 truncate">{scoreConfig.name}</span>
                        <span className="shrink-0 text-xs text-white/35 light:text-black/40">
                          {scoreConfig.dataType}
                        </span>
                      </label>
                    ))
                  )}
                </div>
              </EvaluationField>

              <div className="grid grid-cols-2 gap-3">
                <EvaluationField label="Interval (seconds)">
                  <Input
                    type="number"
                    value={intervalSeconds}
                    onChange={(event) => setIntervalSeconds(event.target.value)}
                    placeholder="300"
                    className={evaluationInputClass}
                  />
                </EvaluationField>
                <EvaluationField label="Lookback (seconds)">
                  <Input
                    type="number"
                    value={lookbackSeconds}
                    onChange={(event) => setLookbackSeconds(event.target.value)}
                    placeholder="3600"
                    className={evaluationInputClass}
                  />
                </EvaluationField>
              </div>
              <p className="-mt-2 text-[11px] text-white/35 light:text-black/40">
                Interval 0 = manual only (run with {'"Run now"'}). Lookback bounds how far back each poll scans.
              </p>

              <EvaluationDisclosure label="Advanced" open={advancedOpen} onOpenChange={setAdvancedOpen}>
                <EvaluationField label="Description">
                  <Textarea
                    placeholder="What does this rule monitor?"
                    value={description}
                    onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) =>
                      setDescription(event.target.value)
                    }
                    rows={3}
                    className={evaluationTextareaClass}
                  />
                </EvaluationField>

                <EvaluationField label="Trace filter (JSON, optional)">
                  <Textarea
                    placeholder='{"environment": "production", "model": "gpt-4o"}'
                    value={filterJson}
                    onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) => {
                      setFilterJson(event.target.value)
                      setFilterError(null)
                    }}
                    rows={4}
                    className={`${evaluationTextareaClass} font-mono`}
                  />
                  {filterError && (
                    <p className="text-[11px] text-red-400 light:text-red-600">{filterError}</p>
                  )}
                  <p className="text-[11px] text-white/35 light:text-black/40">
                    Restrict which traces are eligible. Leave blank to sample all.
                  </p>
                </EvaluationField>
              </EvaluationDisclosure>
            </SheetBody>
            <SheetFooter className={evaluationSheetFooterClass}>
              <Button
                type="button"
                variant="outline"
                onClick={() => setSheetOpen(false)}
                disabled={createMutation.isPending}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={createMutation.isPending || !name || selectedScorers.length === 0}
              >
                <Play className="h-4 w-4" />
                {createMutation.isPending ? 'Creating...' : 'Create rule'}
              </Button>
            </SheetFooter>
          </form>
        </SheetContent>
      </Sheet>

      <AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Rule</AlertDialogTitle>
            <AlertDialogDescription>
              Delete "{deleteTarget?.name}"? This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteMutation.isPending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-red-600 hover:bg-red-700"
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
