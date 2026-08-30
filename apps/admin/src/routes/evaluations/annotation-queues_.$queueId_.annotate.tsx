import { useState, useCallback, useEffect, useRef } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useAnnotationQueue,
  useQueueStats,
  useSubmitAnnotation,
} from '@/hooks/evaluations/use-annotations'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import { getNextItem, skipItem } from '@/server/annotations'
import { useSession } from '@/hooks/auth'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { Loader, Button } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import {
  Clock,
  CheckCircle,
  SkipForward,
  ClipboardList,
  ExternalLink,
  Timer,
  Keyboard,
} from 'lucide-react'

const { Card, CardContent, Input, Label, Textarea } = ui

export const Route = createFileRoute(
  '/evaluations/annotation-queues_/$queueId_/annotate',
)({
  component: AnnotateTrooperPage,
})

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

function AnnotateTrooperPage() {
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

  return <AnnotateTrooperPageContent />
}

function AnnotateTrooperPageContent() {
  const { queueId } = Route.useParams()
  const navigate = useNavigate()
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''
  const { data: queue } = useAnnotationQueue(queueId)
  const { data: stats } = useQueueStats(queueId)
  const { data: scoreConfigs } = useScoreConfigs()
  const submitMutation = useSubmitAnnotation()
  const queryClient = useQueryClient()

  const [scores, setScores] = useState<Record<string, number>>({})
  const [comment, setComment] = useState('')
  const [isSkipping, setIsSkipping] = useState(false)
  const [timeOnItemMs, setTimeOnItemMs] = useState(0)
  const itemStartedAtRef = useRef<number>(Date.now())
  const commentRef = useRef<HTMLTextAreaElement | null>(null)

  // Fetch next item
  const {
    data: nextItemData,
    isLoading: loadingNext,
    refetch: refetchNext,
  } = useQuery({
    queryKey: ['annotation-next-item', orgId, queueId],
    queryFn: async () => {
      const response = await getNextItem({ tenantId: orgId, queueId })
      return response
    },
    enabled: !!orgId && !!queueId,
    refetchOnWindowFocus: false,
    staleTime: 0,
  })

  const currentItem = (nextItemData as any)?.item

  // Reset the time-on-item clock when a new item loads.
  useEffect(() => {
    if (currentItem?.id) {
      itemStartedAtRef.current = Date.now()
      setTimeOnItemMs(0)
    }
  }, [currentItem?.id])

  // Wall-clock tick for the time-on-item indicator. Annotators care
  // about "how long am I spending on this item" more than any absolute
  // SLA — the number shows up in the metric strip.
  useEffect(() => {
    if (!currentItem) return
    const id = setInterval(
      () => setTimeOnItemMs(Date.now() - itemStartedAtRef.current),
      1000,
    )
    return () => clearInterval(id)
  }, [currentItem])

  const handleScoreChange = useCallback((configId: string, value: number) => {
    setScores((prev) => ({ ...prev, [configId]: value }))
  }, [])

  const handleSubmit = async () => {
    if (!currentItem) return
    try {
      await submitMutation.mutateAsync({
        queueId,
        itemId: currentItem.id,
        scores,
        comment: comment || undefined,
      })
      setScores({})
      setComment('')
      await refetchNext()
      queryClient.invalidateQueries({ queryKey: ['annotation-queue-stats'] })
    } catch {
      // Error handled by mutation
    }
  }

  const handleSkip = async () => {
    if (!currentItem) return
    setIsSkipping(true)
    try {
      await skipItem({ tenantId: orgId, queueId, itemId: currentItem.id })
      setScores({})
      setComment('')
      await refetchNext()
      queryClient.invalidateQueries({ queryKey: ['annotation-queue-stats'] })
    } finally {
      setIsSkipping(false)
    }
  }

  // Get the score configs relevant to this queue
  const queueScoreConfigIds = (queue as any)?.scoreConfigIds ?? []
  const relevantConfigs =
    scoreConfigs?.filter((sc: any) => queueScoreConfigIds.includes(sc.id)) ?? []

  // Keyboard shortcuts for high-volume annotators.
  //   Enter / Cmd+Enter   submit
  //   S                   skip
  //   T                   open trace in new tab
  //   1-9                 set first numeric scorer to N/10 (0.1..0.9)
  //                       (Boolean scorers: 1 = Yes, 0 = No)
  //   ?                   toggle help (TODO: surface as overlay)
  //
  // Skipped when the user is typing in the comment textarea.
  useEffect(() => {
    if (!currentItem) return
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      const isText =
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLInputElement
      // Allow Cmd/Ctrl+Enter to submit even from textarea.
      const isSubmitCombo = e.key === 'Enter' && (e.metaKey || e.ctrlKey)

      if (isText && !isSubmitCombo) return

      if (isSubmitCombo) {
        e.preventDefault()
        handleSubmit()
        return
      }
      switch (e.key) {
        case 'Enter':
          e.preventDefault()
          handleSubmit()
          return
        case 's':
        case 'S':
          e.preventDefault()
          handleSkip()
          return
        case 't':
        case 'T':
          if (currentItem?.traceId) {
            e.preventDefault()
            window.open(
              `/observability/traces?trace=${encodeURIComponent(currentItem.traceId)}`,
              '_blank',
            )
          }
          return
      }
      // 0-9: shortcut to set the first scorer's value.
      if (e.key >= '0' && e.key <= '9' && relevantConfigs.length > 0) {
        e.preventDefault()
        const first = relevantConfigs[0] as any
        const n = Number(e.key)
        if (first.dataType === 'boolean') {
          handleScoreChange(first.id, n === 0 ? 0 : 1)
        } else {
          const min = Number(first.minValue ?? 0)
          const max = Number(first.maxValue ?? 1)
          const v = min + ((max - min) * n) / 10
          handleScoreChange(first.id, Number(v.toFixed(3)))
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [currentItem, relevantConfigs]) // eslint-disable-line react-hooks/exhaustive-deps

  const totalItems = (stats as any)?.totalItems ?? 0
  const pendingItems = (stats as any)?.pendingItems ?? 0
  const completedItems = (stats as any)?.completedItems ?? 0
  const skippedItems = (stats as any)?.skippedItems ?? 0

  return (
    <div className="flex flex-col h-full w-full overflow-hidden">
      {/* Stats bar */}
      <div className="flex-shrink-0 p-3 pb-0">
        <div className="grid grid-cols-2 md:grid-cols-5 gap-2">
          <MetricCard
            icon={<ClipboardList className="h-4 w-4" />}
            label="Total"
            value={totalItems}
          />
          <MetricCard
            icon={<Clock className="h-4 w-4" />}
            label="Remaining"
            value={pendingItems}
          />
          <MetricCard
            icon={<CheckCircle className="h-4 w-4" />}
            label="Completed"
            value={completedItems}
          />
          <MetricCard
            icon={<SkipForward className="h-4 w-4" />}
            label="Skipped"
            value={skippedItems}
          />
          <MetricCard
            icon={<Timer className="h-4 w-4" />}
            label="Time on item"
            value={currentItem ? `${Math.floor(timeOnItemMs / 1000)}s` : '—'}
          />
        </div>
        <div className="mt-1 flex items-center justify-end gap-1 text-[10px] text-white/30 light:text-black/30">
          <Keyboard className="h-3 w-3" />
          <span>
            <kbd className="font-mono">Enter</kbd> submit ·{' '}
            <kbd className="font-mono">S</kbd> skip ·{' '}
            <kbd className="font-mono">T</kbd> open trace ·{' '}
            <kbd className="font-mono">0-9</kbd> set first score
          </span>
        </div>
      </div>

      {/* Main trooper */}
      <div className="flex-1 flex min-h-0 overflow-hidden">
        {/* Left panel: Trace viewer */}
        <div className="flex-1 border-r border-brand-main-600 overflow-auto p-4">
          {loadingNext ? (
            <div className="flex items-center justify-center h-full">
              <Loader loaderText="Loading next item..." />
            </div>
          ) : !currentItem ? (
            <div className="flex flex-col items-center justify-center h-full gap-4">
              <span className="bg-brand-secondary-200 rounded-md p-2 inline-block mb-2">
                <Iconify.Icon
                  icon="heroicons:check-circle"
                  className="size-10 text-brand-secondary-700"
                />
              </span>
              <h3 className="text-lg font-medium text-white light:text-brand-main-50">
                All items annotated!
              </h3>
              <p className="text-sm text-white/60 light:text-black/60 text-center">
                There are no more pending items in this queue.
              </p>
              <Button
                variant="outline"
                className="border-brand-main-600 text-brand-main-100 hover:text-white light:hover:text-brand-main-50"
                onClick={() =>
                  navigate({
                    to: '/evaluations/annotation-queues/$queueId',
                    params: { queueId },
                  })
                }
              >
                Back to Queue
              </Button>
            </div>
          ) : (
            <div className="space-y-3">
              <div className="flex items-center gap-4 text-xs">
                <div className="flex items-center gap-1.5">
                  <span className="text-white/40 light:text-black/40">
                    Trace ID
                  </span>
                  <Link
                    to="/observability/traces"
                    search={{ trace: currentItem.traceId } as any}
                    target="_blank"
                    className="inline-flex items-center gap-1 font-mono text-brand-secondary-300 hover:text-brand-secondary-200 hover:underline"
                  >
                    {currentItem.traceId}
                    <ExternalLink className="h-3 w-3" />
                  </Link>
                </div>
                {currentItem.observationId && (
                  <div className="flex items-center gap-1.5">
                    <span className="text-white/40 light:text-black/40">
                      Observation
                    </span>
                    <span className="font-mono text-white light:text-brand-main-50">
                      {currentItem.observationId}
                    </span>
                  </div>
                )}
              </div>

              <Card className="border-brand-main-600 bg-brand-main-900/50">
                <CardContent>
                  <div className="bg-brand-main-950 rounded p-4 border border-brand-main-600 min-h-[300px]">
                    <p className="text-white/40 light:text-black/40 text-sm italic">
                      Trace viewer will be integrated here. The trace data for{' '}
                      {currentItem.traceId} would be displayed, showing the full
                      request/response, model used, and any observations.
                    </p>
                    {currentItem.metadata && (
                      <pre className="mt-4 text-xs text-white/60 light:text-black/60 font-mono overflow-auto">
                        {JSON.stringify(currentItem.metadata, null, 2)}
                      </pre>
                    )}
                  </div>
                </CardContent>
              </Card>
            </div>
          )}
        </div>

        {/* Right panel: Score form */}
        <div className="w-[380px] overflow-auto p-4 bg-brand-main-950/50">
          {currentItem && (
            <div className="space-y-5">
              <h3 className="text-white light:text-brand-main-50 text-sm font-medium uppercase tracking-wide">
                Scores
              </h3>

              {relevantConfigs.length === 0 ? (
                <p className="text-white/40 light:text-black/40 text-sm">
                  No score configs assigned to this queue.
                </p>
              ) : (
                relevantConfigs.map((config: any) => (
                  <div key={config.id} className="space-y-2">
                    <Label className="text-brand-main-100">{config.name}</Label>
                    {config.description && (
                      <p className="text-white/40 light:text-black/40 text-xs">
                        {config.description}
                      </p>
                    )}

                    {config.dataType === 'boolean' ? (
                      <div className="flex gap-2">
                        <Button
                          type="button"
                          variant={
                            scores[config.id] === 1 ? 'default' : 'outline'
                          }
                          className={
                            scores[config.id] === 1
                              ? 'bg-emerald-600 hover:bg-emerald-500'
                              : 'border-brand-main-600 text-brand-main-100 hover:text-white light:hover:text-brand-main-50'
                          }
                          onClick={() => handleScoreChange(config.id, 1)}
                        >
                          Yes
                        </Button>
                        <Button
                          type="button"
                          variant={
                            scores[config.id] === 0 ? 'default' : 'outline'
                          }
                          className={
                            scores[config.id] === 0
                              ? 'bg-red-600 hover:bg-red-500'
                              : 'border-brand-main-600 text-brand-main-100 hover:text-white light:hover:text-brand-main-50'
                          }
                          onClick={() => handleScoreChange(config.id, 0)}
                        >
                          No
                        </Button>
                      </div>
                    ) : (
                      <Input
                        type="number"
                        min={config.minValue ?? 0}
                        max={config.maxValue ?? 1}
                        step={0.1}
                        value={scores[config.id] ?? ''}
                        onChange={(e) =>
                          handleScoreChange(config.id, Number(e.target.value))
                        }
                        placeholder={`${config.minValue ?? 0} - ${config.maxValue ?? 1}`}
                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50 w-full"
                      />
                    )}
                  </div>
                ))
              )}

              <div className="space-y-2">
                <Label className="text-brand-main-100">
                  Comment (optional)
                </Label>
                <Textarea
                  ref={commentRef}
                  value={comment}
                  onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                    setComment(e.target.value)
                  }
                  rows={3}
                  placeholder="Any additional notes... (Cmd+Enter to submit)"
                  className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                />
              </div>

              <div className="flex gap-2 pt-2">
                <Button
                  variant="outline"
                  className="flex-1 border-brand-main-600 text-brand-main-100 hover:text-white light:hover:text-brand-main-50"
                  onClick={handleSkip}
                  disabled={isSkipping}
                >
                  {isSkipping ? 'Skipping...' : 'Skip'}
                </Button>
                <Button
                  className="flex-1"
                  onClick={handleSubmit}
                  disabled={submitMutation.isPending}
                >
                  {submitMutation.isPending ? 'Submitting...' : 'Submit'}
                </Button>
              </div>

              {submitMutation.error && (
                <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600">
                  {(submitMutation.error as Error).message}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
