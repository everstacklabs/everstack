import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useAnnotationQueues,
  useDeleteQueue,
} from '@/hooks/evaluations/use-annotations'
import { Button, Loader, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { ClipboardCheck, Plus, Search, Trash2 } from 'lucide-react'
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

export const Route = createFileRoute('/evaluations/annotation-queues')({
  component: AnnotationQueuesPage,
})

function AnnotationQueuesPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Annotation Queues"
        description="Human review queues for data labeling and quality assurance."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <AnnotationQueuesPageContent />
}

function countLabel(value: number | undefined, label: string) {
  return `${Number(value ?? 0).toLocaleString()} ${label}`
}

function AnnotationQueuesPageContent() {
  const navigate = useNavigate()
  const { data: queues, isLoading } = useAnnotationQueues()
  const deleteMutation = useDeleteQueue()

  const [search, setSearch] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<{
    id: string
    name: string
  } | null>(null)

  const allQueues = queues ?? []
  const filteredQueues = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return allQueues
    return allQueues.filter((queue: any) =>
      [
        queue.name,
        queue.description,
        queue.id,
        String(queue.pendingCount ?? ''),
        String(queue.completedCount ?? ''),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
        .includes(query),
    )
  }, [allQueues, search])

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      toast.success('Annotation queue deleted')
      setDeleteTarget(null)
    } catch {
      toast.error('Failed to delete annotation queue')
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
        id: 'description',
        header: 'Description',
        width: 320,
        minWidth: 170,
        render: (row: any) => (
          <span className="truncate text-xs text-white/55 light:text-black/55">
            {row.description || '-'}
          </span>
        ),
      },
      {
        id: 'scoreConfigs',
        header: 'Scorers',
        width: 110,
        minWidth: 90,
        render: (row: any) => (
          <span className="font-mono text-xs tabular-nums text-white/70 light:text-black/70">
            {(row.scoreConfigIds?.length ?? 0).toLocaleString()}
          </span>
        ),
      },
      {
        id: 'items',
        header: 'Items',
        width: 220,
        minWidth: 170,
        render: (row: any) => (
          <div className="flex gap-1.5">
            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-amber-500/15 text-amber-300 light:text-amber-700">
              {countLabel(row.pendingCount, 'pending')}
            </span>
            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-500/15 text-emerald-300 light:text-emerald-600">
              {countLabel(row.completedCount, 'done')}
            </span>
          </div>
        ),
      },
      {
        id: 'created',
        header: 'Created',
        width: 170,
        minWidth: 140,
        render: (row: any) => (
          <span className="truncate text-xs text-brand-main-100">
            {row.createdAt ? formatTimestamp(row.createdAt) : '-'}
          </span>
        ),
      },
    ],
    [],
  )

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      <div className="shrink-0 border-b border-brand-main-700/70 bg-brand-main-950 light:border-brand-main-200 light:bg-white">
        <div className="flex items-center gap-3 px-4 py-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/35" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search annotation queues..."
              className={`${evaluationInputClass} pl-7`}
            />
          </div>
        </div>
      </div>

      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
          <Loader loaderText="Loading annotation queues..." />
        </div>
      ) : allQueues.length === 0 ? (
        <div className="flex flex-1 items-center justify-center overflow-y-auto px-6 py-8 scrollbar-macos">
          <div className="mx-auto flex max-w-6xl flex-col gap-6">
            <div className="mx-auto max-w-xl text-center">
              <div className="mx-auto mb-4 flex h-9 w-9 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70 light:border-brand-main-200 light:bg-white">
                <ClipboardCheck className="size-4 text-brand-secondary-300 light:text-brand-secondary-700" />
              </div>
              <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
                Set up human review
              </h3>
              <p className="text-sm leading-relaxed text-white/45 light:text-black/50">
                Queue trace or dataset items for annotation, attach scorers, and
                move review work out of spreadsheets.
              </p>
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              <section className="rounded border border-brand-main-700 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
                <div className="mb-4 flex items-center gap-2">
                  <Plus className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
                  <h4 className="text-sm font-semibold text-white light:text-brand-main-50">
                    Create a queue
                  </h4>
                </div>
                <p className="mb-5 text-sm leading-relaxed text-white/45 light:text-black/50">
                  Define the review lane, choose the scoring criteria, then
                  populate it from traces or datasets.
                </p>
                <Button
                  onClick={() =>
                    void navigate({ to: '/evaluations/annotation-queues/new' })
                  }
                >
                  <Plus className="h-4 w-4" />
                  Create Queue
                </Button>
              </section>

              <section className="rounded border border-brand-main-700 bg-brand-main-950 p-5 light:border-brand-main-200 light:bg-brand-main-50">
                <div className="mb-4 flex items-center gap-2">
                  <ClipboardCheck className="h-4 w-4 text-brand-secondary-300 light:text-brand-secondary-700" />
                  <h4 className="text-sm font-semibold text-white light:text-brand-main-50">
                    Review workflow
                  </h4>
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
            </div>
          </div>
        </div>
      ) : (
        <ResponsiveTable
          tableId="evaluations-annotation-queues"
          columns={columns}
          data={filteredQueues}
          enableResizing={true}
          rowKey={(row: any) => row.id}
          minTableWidth="100%"
          emptyMessage="No annotation queues match this search."
          onRowClick={(row: any) =>
            navigate({
              to: '/evaluations/annotation-queues/$queueId',
              params: { queueId: row.id },
            })
          }
          rowActions={[
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
            <AlertDialogTitle>Delete Annotation Queue</AlertDialogTitle>
            <AlertDialogDescription>
              Delete "{deleteTarget?.name}"? All items in the queue will be
              removed.
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
