import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useDeleteScoreConfig,
  useScoreConfigs,
} from '@/hooks/evaluations/use-score-configs'
import { Button, Loader, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Pencil, Plus, Search, Sparkles, Trash2 } from 'lucide-react'
import { evaluationInputClass } from '@/components/evaluations/evaluation-form'

const {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Input,
} = ui

export const Route = createFileRoute('/evaluations/score-configs')({
  component: ScoreConfigsPage,
})

const typeFilters = [
  { value: 'all', label: 'All' },
  { value: 'numeric', label: 'Numeric' },
  { value: 'boolean', label: 'Boolean' },
  { value: 'llm_judge', label: 'LLM Judge' },
  { value: 'code_scorer', label: 'Code' },
]

function ScoreConfigsPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Score Configurations"
        description="Configure scoring criteria for evaluations and annotations."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <ScoreConfigsPageContent />
}

function dataTypeBadge(type: string) {
  const normalized = type?.toLowerCase()
  const map: Record<string, string> = {
    numeric: 'bg-blue-500/15 text-blue-300 light:text-blue-600',
    boolean: 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600',
    categorical: 'bg-fuchsia-500/15 text-fuchsia-300 light:text-fuchsia-700',
    llm_judge: 'bg-amber-500/15 text-amber-300 light:text-amber-700',
    code_scorer: 'bg-cyan-500/15 text-cyan-300 light:text-cyan-700',
  }

  return (
    <span
      className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${map[normalized] ?? 'bg-white/10 text-white/45 light:bg-black/10 light:text-black/45'}`}
    >
      {type}
    </span>
  )
}

function rangeLabel(row: any) {
  if (row.minValue !== undefined && row.maxValue !== undefined) {
    return `${row.minValue} - ${row.maxValue}`
  }
  if (row.minValue !== undefined) return `>= ${row.minValue}`
  if (row.maxValue !== undefined) return `<= ${row.maxValue}`
  return '-'
}

function ScoreConfigsPageContent() {
  const navigate = useNavigate()
  const { data: configs, isLoading } = useScoreConfigs()
  const deleteMutation = useDeleteScoreConfig()

  const [search, setSearch] = useState('')
  const [typeFilter, setTypeFilter] = useState('all')
  const [deleteTarget, setDeleteTarget] = useState<{
    id: string
    name: string
  } | null>(null)

  const allConfigs = configs ?? []
  const filteredConfigs = useMemo(() => {
    const query = search.trim().toLowerCase()
    return allConfigs.filter((config: any) => {
      const matchesType =
        typeFilter === 'all' || config.dataType?.toLowerCase() === typeFilter
      if (!matchesType) return false
      if (!query) return true
      return [config.name, config.description, config.dataType, config.id]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
        .includes(query)
    })
  }, [allConfigs, search, typeFilter])

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      toast.success('Score config deleted')
      setDeleteTarget(null)
    } catch {
      toast.error('Failed to delete score config')
    }
  }

  const columns: ColumnConfig<any>[] = useMemo(
    () => [
      {
        id: 'name',
        header: 'Name',
        width: 240,
        minWidth: 180,
        render: (row: any) => (
          <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
            {row.name}
          </span>
        ),
      },
      {
        id: 'type',
        header: 'Type',
        width: 140,
        minWidth: 110,
        render: (row: any) => dataTypeBadge(row.dataType),
      },
      {
        id: 'description',
        header: 'Description',
        width: 340,
        minWidth: 180,
        render: (row: any) => (
          <span className="truncate text-xs text-white/55 light:text-black/55">
            {row.description || '-'}
          </span>
        ),
      },
      {
        id: 'range',
        header: 'Range',
        width: 120,
        minWidth: 90,
        render: (row: any) => (
          <span className="font-mono text-xs tabular-nums text-white/70 light:text-black/70">
            {rangeLabel(row)}
          </span>
        ),
      },
      {
        id: 'runtime',
        header: 'Runtime',
        width: 130,
        minWidth: 110,
        render: (row: any) => (
          <span className="truncate text-xs text-white/50 light:text-black/55">
            {row.useSandbox ? 'Sandbox' : row.evalModel || '-'}
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
              placeholder="Search score configs..."
              className={`${evaluationInputClass} pl-7`}
            />
          </div>
          <div className="flex rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
            {typeFilters.map((filter) => (
              <button
                key={filter.value}
                type="button"
                onClick={() => setTypeFilter(filter.value)}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                  typeFilter === filter.value
                    ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                    : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                }`}
              >
                {filter.label}
              </button>
            ))}
          </div>
          <Button
            size="sm"
            onClick={() => void navigate({ to: '/evaluations/score-configs/new' })}
          >
            <Plus className="h-4 w-4" />
            New
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
          <Loader loaderText="Loading score configs..." />
        </div>
      ) : allConfigs.length === 0 ? (
        <div className="flex flex-1 items-center justify-center overflow-y-auto px-6 py-8 scrollbar-macos">
          <div className="mx-auto flex max-w-6xl flex-col gap-6">
            <div className="mx-auto max-w-xl text-center">
              <div className="mx-auto mb-4 flex h-9 w-9 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70 light:border-brand-main-200 light:bg-white">
                <Sparkles className="size-4 text-brand-secondary-300 light:text-brand-secondary-700" />
              </div>
              <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
                Build your scoring library
              </h3>
              <p className="text-sm leading-relaxed text-white/45 light:text-black/50">
                Start with a built-in evaluator or define a custom score for the
                checks your team cares about.
              </p>
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              <section className="rounded border border-brand-main-700 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
                <div className="mb-4 flex items-center gap-2">
                  <Sparkles className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
                  <h4 className="text-sm font-semibold text-white light:text-brand-main-50">
                    Built-in metrics
                  </h4>
                </div>
                <p className="mb-5 text-sm leading-relaxed text-white/45 light:text-black/50">
                  Use prebuilt rubric prompts for common quality, safety, and
                  RAG checks, then tune the prompt before saving.
                </p>
                <Button
                  onClick={() =>
                    void navigate({ to: '/evaluations/score-configs/new' })
                  }
                >
                  <Sparkles className="h-4 w-4" />
                  Add Built-in Metric
                </Button>
              </section>

              <section className="rounded border border-brand-main-700 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
                <div className="mb-4 flex items-center gap-2">
                  <Plus className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
                  <h4 className="text-sm font-semibold text-white light:text-brand-main-50">
                    Custom scorer
                  </h4>
                </div>
                <div className="mb-5 flex flex-wrap gap-1.5">
                  {typeFilters.slice(1).map((filter) => (
                    <span
                      key={filter.value}
                      className="rounded border border-brand-main-700 bg-brand-main-900 px-2 py-1 text-xs text-white/55 light:border-brand-main-200 light:bg-white light:text-black/55"
                    >
                      {filter.label}
                    </span>
                  ))}
                </div>
                <Button
                  variant="outline"
                  onClick={() =>
                    void navigate({ to: '/evaluations/score-configs/new' })
                  }
                >
                  <Plus className="h-4 w-4" />
                  Create Custom Config
                </Button>
              </section>
            </div>
          </div>
        </div>
      ) : (
        <ResponsiveTable
          tableId="evaluations-score-configs"
          columns={columns}
          data={filteredConfigs}
          rowKey={(row: any) => row.id}
          minTableWidth="100%"
          emptyMessage="No score configs match this view."
          rowActions={[
            {
              label: 'Edit',
              icon: <Pencil className="w-4 h-4" />,
              onClick: (row: any) => {
                void navigate({
                  to: '/evaluations/score-configs/$configId',
                  params: { configId: row.id },
                })
              },
            },
            {
              label: 'Delete',
              icon: <Trash2 className="w-4 h-4" />,
              variant: 'destructive',
              onClick: (row: any) => {
                setDeleteTarget({ id: row.id, name: row.name })
              },
            },
          ]}
        />
      )}

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Score Config</AlertDialogTitle>
            <AlertDialogDescription>
              Delete "{deleteTarget?.name}"? This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteMutation.isPending}>
              Cancel
            </AlertDialogCancel>
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
