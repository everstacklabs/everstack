import { useMemo, useState } from 'react'
import { Link, useLocation, useNavigate } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { type ActionGroup } from '@/components/layout/topbar/types'
import {
  useAnnotationQueue,
  usePopulateFromDataset,
  usePopulateFromTraces,
} from '@/hooks/evaluations/use-annotations'
import { useDatasets } from '@/hooks/evaluations/use-datasets'
import { useMetricsDimensionOptions } from '@/hooks/observability/use-metrics'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import {
  EvaluationField,
  evaluationInputClass,
  evaluationSelectContentClass,
  evaluationSelectTriggerClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetFooterClass,
} from '@/components/evaluations/evaluation-form'

const {
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} = ui

function useQueueIdFromPath(): string {
  const { pathname } = useLocation()
  const segments = pathname.split('/').filter(Boolean)
  const queueIdIndex = segments.indexOf('annotation-queues')
  return queueIdIndex >= 0 && segments.length > queueIdIndex + 1
    ? segments[queueIdIndex + 1]
    : ''
}

function QueueBreadcrumb() {
  const queueId = useQueueIdFromPath()
  const { data: queue, isLoading } = useAnnotationQueue(queueId)

  return (
    <div className="flex items-center gap-1.5">
      <Link
        to="/evaluations/annotation-queues"
        className="text-sm font-normal text-brand-main-300 transition-colors hover:text-white/80 light:hover:text-black/80"
      >
        Annotation Queues
      </Link>
      {queueId && (
        <>
          <span className="text-sm text-brand-main-400">/</span>
          {isLoading ? (
            <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
          ) : (
            <span className="text-sm font-normal text-white light:text-brand-main-50">
              {(queue as any)?.name ?? `${queueId.substring(0, 12)}...`}
            </span>
          )}
        </>
      )}
    </div>
  )
}

function StartAnnotatingButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const queueId = useQueueIdFromPath()

  if (gate.isBlocked) return null
  if (pathname.includes('/annotate')) return null

  return (
    <Button
      variant="default"
      onClick={() =>
        navigate({
          to: '/evaluations/annotation-queues/$queueId/annotate',
          params: { queueId },
        })
      }
    >
      Start Annotating
    </Button>
  )
}

function PopulateButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const { pathname } = useLocation()
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<string>('traces')
  const [model, setModel] = useState('')
  const [provider, setProvider] = useState('')
  const [statusCode, setStatusCode] = useState('')
  const [maxItems, setMaxItems] = useState(100)
  const [datasetId, setDatasetId] = useState('')
  const [datasetMaxItems, setDatasetMaxItems] = useState(1000)

  const queueId = useQueueIdFromPath()
  const populateTraces = usePopulateFromTraces()
  const populateDataset = usePopulateFromDataset()
  const { data: datasets } = useDatasets()
  const { data: modelOptionsResp } = useMetricsDimensionOptions({}, 'model')
  const { data: providerOptionsResp } = useMetricsDimensionOptions(
    {},
    'provider',
  )

  const modelOptions = useMemo(() => {
    const options = modelOptionsResp?.series?.map((series) => series.metricName)
    return (options ?? [])
      .filter((option) => option && option !== 'unknown')
      .sort()
  }, [modelOptionsResp])

  const providerOptions = useMemo(() => {
    const options = providerOptionsResp?.series?.map(
      (series) => series.metricName,
    )
    return (options ?? [])
      .filter((option) => option && option !== 'unknown')
      .sort()
  }, [providerOptionsResp])

  if (gate.isBlocked) return null
  if (pathname.includes('/annotate')) return null

  const isPending = populateTraces.isPending || populateDataset.isPending

  const resetForm = () => {
    setModel('')
    setProvider('')
    setStatusCode('')
    setMaxItems(100)
    setDatasetId('')
    setDatasetMaxItems(1000)
  }

  const handleSubmitTraces = async () => {
    const filter: Record<string, unknown> = {}
    if (model && model !== 'all') filter.model = model
    if (provider && provider !== 'all') filter.provider = provider
    if (statusCode && statusCode !== 'all') filter.status_code = statusCode

    try {
      const result = await populateTraces.mutateAsync({
        queueId,
        traceFilter:
          Object.keys(filter).length > 0 ? JSON.stringify(filter) : undefined,
        maxItems,
      })
      const count = (result as any)?.addedCount ?? 0
      toast.success(`Added ${count} trace${count !== 1 ? 's' : ''}`)
      setOpen(false)
      resetForm()
    } catch {
      toast.error('Failed to populate from traces')
    }
  }

  const handleSubmitDataset = async () => {
    if (!datasetId) {
      toast.error('Please select a dataset')
      return
    }

    try {
      const result = await populateDataset.mutateAsync({
        queueId,
        datasetId,
        maxItems: datasetMaxItems,
      })
      if (result.addedCount === 0) {
        toast.warning('No items with linked traces found in this dataset')
      } else {
        toast.success(
          `Added ${result.addedCount} item${result.addedCount !== 1 ? 's' : ''}`,
        )
      }
      setOpen(false)
      resetForm()
    } catch {
      toast.error('Failed to populate from dataset')
    }
  }

  const actionDisabled =
    isPending || (tab === 'dataset' && !datasetId) || !queueId
  const actionLabel =
    tab === 'traces'
      ? populateTraces.isPending
        ? 'Adding...'
        : 'Add Traces'
      : populateDataset.isPending
        ? 'Adding...'
        : 'Add from Dataset'

  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        Populate Queue
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="right"
          className={`${evaluationSheetContentClass} sm:max-w-[500px]`}
        >
          <SheetHeader>
            <SheetTitle>Populate Queue</SheetTitle>
          </SheetHeader>

          <SheetBody className={evaluationSheetBodyClass}>
            <Tabs value={tab} onValueChange={setTab}>
              <TabsList className="grid h-9 w-full grid-cols-2 rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
                <TabsTrigger value="traces" className="text-xs">
                  From Traces
                </TabsTrigger>
                <TabsTrigger value="dataset" className="text-xs">
                  From Dataset
                </TabsTrigger>
              </TabsList>

              <TabsContent value="traces" className="mt-5 space-y-5">
                <EvaluationField label="Model">
                  <Select value={model} onValueChange={setModel}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue placeholder="All models" />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      <SelectItem value="all">All models</SelectItem>
                      {modelOptions.map((option) => (
                        <SelectItem key={option} value={option}>
                          {option}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </EvaluationField>

                <EvaluationField label="Provider">
                  <Select value={provider} onValueChange={setProvider}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue placeholder="All providers" />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      <SelectItem value="all">All providers</SelectItem>
                      {providerOptions.map((option) => (
                        <SelectItem key={option} value={option}>
                          {option}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </EvaluationField>

                <EvaluationField label="Status">
                  <Select value={statusCode} onValueChange={setStatusCode}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue placeholder="Any status" />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      <SelectItem value="all">Any status</SelectItem>
                      <SelectItem value="OK">OK</SelectItem>
                      <SelectItem value="ERROR">Error</SelectItem>
                    </SelectContent>
                  </Select>
                </EvaluationField>

                <EvaluationField label="Max items" htmlFor="trace-max-items">
                  <Input
                    id="trace-max-items"
                    type="number"
                    min={1}
                    max={1000}
                    value={maxItems}
                    onChange={(event) =>
                      setMaxItems(Number(event.target.value) || 100)
                    }
                    className={evaluationInputClass}
                  />
                </EvaluationField>
              </TabsContent>

              <TabsContent value="dataset" className="mt-5 space-y-5">
                <EvaluationField label="Dataset">
                  <Select value={datasetId} onValueChange={setDatasetId}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue placeholder="Select a dataset" />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      {(datasets ?? []).map((dataset: any) => (
                        <SelectItem key={dataset.id} value={dataset.id}>
                          {dataset.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </EvaluationField>

                <EvaluationField label="Max items" htmlFor="dataset-max-items">
                  <Input
                    id="dataset-max-items"
                    type="number"
                    min={1}
                    max={1000}
                    value={datasetMaxItems}
                    onChange={(event) =>
                      setDatasetMaxItems(Number(event.target.value) || 1000)
                    }
                    className={evaluationInputClass}
                  />
                </EvaluationField>
              </TabsContent>
            </Tabs>
          </SheetBody>

          <SheetFooter className={evaluationSheetFooterClass}>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={
                tab === 'traces' ? handleSubmitTraces : handleSubmitDataset
              }
              disabled={actionDisabled}
            >
              {actionLabel}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </>
  )
}

export const EvaluationsAnnotationQueuesDetailActions: ActionGroup[] = [
  {
    title: <QueueBreadcrumb />,
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'populate-queue',
        label: 'Populate Queue',
        component: PopulateButton,
      },
      {
        type: 'custom',
        key: 'start-annotating',
        label: 'Start Annotating',
        component: StartAnnotatingButton,
      },
    ],
  },
]
