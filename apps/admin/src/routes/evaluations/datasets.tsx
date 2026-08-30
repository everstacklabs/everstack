import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useDatasets, useCreateDataset } from '@/hooks/evaluations/use-datasets'
import { usePermissions } from '@/hooks/auth'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader, toast } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { FilePlus2, Search } from 'lucide-react'
import { ui } from '@everstack/ui'
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
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetBody,
  Textarea,
} = ui

export const Route = createFileRoute('/evaluations/datasets')({
  component: DatasetsPage,
})

function getItemCount(dataset: any): number {
  return Number(dataset.itemCount ?? 0)
}

function metadataCount(dataset: any): number {
  const metadata = dataset.metadata
  return metadata && typeof metadata === 'object'
    ? Object.keys(metadata).length
    : 0
}

function slugFor(dataset: any): string {
  const metadataSlug = dataset.metadata?.url_slug ?? dataset.metadata?.slug
  if (typeof metadataSlug === 'string' && metadataSlug.trim())
    return metadataSlug
  return String(dataset.name ?? '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '')
}

function DatasetsPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Datasets"
        description="Create and manage evaluation datasets with input/expected output pairs."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <DatasetsPageContent />
}

function DatasetsPageContent() {
  const navigate = useNavigate()
  const { data: datasets, isLoading } = useDatasets()
  const createMutation = useCreateDataset()
  const { can } = usePermissions()
  const canCreate = can('resource:create')

  const [dialogOpen, setDialogOpen] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [search, setSearch] = useState('')

  const allDatasets = datasets ?? []
  const filteredDatasets = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return allDatasets
    return allDatasets.filter((dataset: any) => {
      const haystack = [
        dataset.name,
        dataset.description,
        dataset.id,
        JSON.stringify(dataset.metadata ?? {}),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(query)
    })
  }, [allDatasets, search])

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

  const columns: ColumnConfig<any>[] = [
    {
      id: 'name',
      header: 'Name',
      width: 260,
      minWidth: 220,
      render: (dataset: any) => (
        <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
          {dataset.name}
        </span>
      ),
    },
    {
      id: 'description',
      header: 'Description',
      width: 300,
      minWidth: 180,
      render: (dataset: any) => (
        <span className="truncate text-xs text-white/55 light:text-black/55">
          {dataset.description || '-'}
        </span>
      ),
    },
    {
      id: 'updatedAt',
      header: 'Updated',
      width: 180,
      minWidth: 150,
      render: (dataset: any) => (
        <span className="truncate text-xs text-brand-main-100">
          {dataset.updatedAt
            ? formatTimestamp(dataset.updatedAt)
            : dataset.createdAt
              ? formatTimestamp(dataset.createdAt)
              : '-'}
        </span>
      ),
    },
    {
      id: 'itemCount',
      header: 'Examples',
      width: 100,
      minWidth: 80,
      render: (dataset: any) => (
        <span className="font-mono text-xs tabular-nums text-white/75 light:text-brand-main-50">
          {getItemCount(dataset).toLocaleString()}
        </span>
      ),
    },
    {
      id: 'metadata',
      header: 'Metadata',
      width: 120,
      minWidth: 100,
      render: (dataset: any) => (
        <span className="truncate text-xs text-white/55 light:text-black/55">
          {metadataCount(dataset) > 0 ? metadataCount(dataset) : '-'}
        </span>
      ),
    },
    {
      id: 'urlSlug',
      header: 'url_slug',
      width: 170,
      minWidth: 130,
      render: (dataset: any) => (
        <span className="truncate text-xs text-white/70 light:text-black/70">
          {slugFor(dataset) || '-'}
        </span>
      ),
    },
    {
      id: 'tags',
      header: 'Tags',
      width: 140,
      minWidth: 100,
      render: (dataset: any) => (
        <span className="truncate text-xs text-white/45 light:text-black/45">
          {Array.isArray(dataset.metadata?.tags) &&
          dataset.metadata.tags.length > 0
            ? dataset.metadata.tags.join(', ')
            : '-'}
        </span>
      ),
    },
  ]

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      <div className="shrink-0 border-b border-brand-main-700/70 bg-brand-main-950 light:bg-white light:border-brand-main-200">
        <div className="flex items-center gap-3 px-4 py-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/35" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search datasets..."
              className="h-8 border-brand-main-700 bg-brand-main-900/60 pl-7 text-xs light:bg-white light:border-brand-main-200"
            />
          </div>
        </div>
      </div>

      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
          <Loader loaderText="Loading datasets..." />
        </div>
      ) : allDatasets.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center px-6">
          <div className="relative mb-5">
            <div className="absolute inset-0 rounded-full bg-brand-secondary-500/20 blur-xl" />
            <div className="relative rounded-lg border border-brand-main-600 bg-brand-main-800/80 p-4 light:bg-white light:border-brand-main-200">
              <Iconify.Icon
                icon="lucide:database"
                className="size-8 text-brand-secondary-400 light:text-brand-secondary-700"
              />
            </div>
          </div>
          <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
            No datasets yet
          </h3>
          <p className="mb-4 max-w-sm text-center text-sm leading-relaxed text-white/50 light:text-black/50">
            Create a dataset to collect evaluation inputs, expected outputs, and
            trace examples.
          </p>
          {canCreate && (
            <Button variant="default" onClick={() => setDialogOpen(true)}>
              <FilePlus2 className="h-4 w-4" />
              Create Dataset
            </Button>
          )}
        </div>
      ) : (
        <ResponsiveTable
          tableId="evaluations-datasets"
          columns={columns}
          data={filteredDatasets}
          enableResizing={true}
          minTableWidth="100%"
          emptyMessage="No datasets match this search."
          onRowClick={(dataset: any) =>
            navigate({
              to: '/evaluations/datasets/$datasetId',
              params: { datasetId: dataset.id },
            })
          }
          rowKey={(dataset: any) => dataset.id}
        />
      )}

      {canCreate && <Sheet open={dialogOpen} onOpenChange={setDialogOpen}>
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

              <EvaluationField label="Name" htmlFor="dataset-name">
                <Input
                  id="dataset-name"
                  placeholder="Support regression set"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className={evaluationInputClass}
                />
              </EvaluationField>
              <EvaluationField
                label="Description"
                htmlFor="dataset-description"
              >
                <Textarea
                  id="dataset-description"
                  placeholder="What behavior should this dataset protect?"
                  value={description}
                  onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                    setDescription(e.target.value)
                  }
                  rows={3}
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
      </Sheet>}
    </div>
  )
}
