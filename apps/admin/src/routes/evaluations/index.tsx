import { useMemo } from 'react'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { useEvalRuns } from '@/hooks/evaluations/use-evals'
import { useDatasets } from '@/hooks/evaluations/use-datasets'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import { Button, Loader } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import {
  Activity,
  ArrowRight,
  Blocks,
  Check,
  Clock,
  Database,
  Flag,
  Hash,
  ListTodo,
  Play,
  Plus,
  X,
} from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'

export const Route = createFileRoute('/evaluations/')({
  component: EvaluationsHomePage,
})

/**
 * First-paint entrance per the evaluations motion standard (design doc §6.1):
 * fade always; the small translate rides behind `motion-safe:` so reduced
 * motion keeps the opacity change and drops the transform. Decorative only —
 * it never blocks interaction. Delays stagger 40ms per section, capped.
 */
const enterClass =
  'animate-in fade-in-0 motion-safe:slide-in-from-bottom-2 fill-mode-both duration-300 ease-out-strong'

function enterDelay(index: number) {
  return { animationDelay: `${Math.min(index, 8) * 40}ms` }
}

const cardClass =
  'rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50'

function EvaluationsHomePage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Evaluations"
        description="Grade model and agent behavior with datasets, scorers, and repeatable eval runs."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <EvaluationsHomeContent />
}

function RunStatusIcon({ status }: { status?: string }) {
  const s = status?.toLowerCase()
  if (s === 'completed')
    return (
      <Check className="h-3.5 w-3.5 text-emerald-400 light:text-emerald-600" />
    )
  if (s === 'running')
    return (
      <Activity className="h-3.5 w-3.5 animate-pulse text-blue-400 light:text-blue-600" />
    )
  if (s === 'failed')
    return <X className="h-3.5 w-3.5 text-red-400 light:text-red-600" />
  if (s === 'cancelled')
    return <X className="h-3.5 w-3.5 text-white/40 light:text-black/40" />
  return <Clock className="h-3.5 w-3.5 text-yellow-400 light:text-yellow-700" />
}

function EvaluationsHomeContent() {
  const { data: runs, isLoading: runsLoading } = useEvalRuns()
  const { data: datasets, isLoading: datasetsLoading } = useDatasets()
  const { data: scoreConfigs, isLoading: scorersLoading } = useScoreConfigs()

  const allRuns = runs ?? []
  const allDatasets = datasets ?? []
  const allScorers = scoreConfigs ?? []

  if (runsLoading || datasetsLoading || scorersLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading evaluations..." />
      </div>
    )
  }

  return (
    <div className="h-full w-full overflow-y-auto scrollbar-macos">
      {allRuns.length === 0 ? (
        <GuidedStart
          datasetCount={allDatasets.length}
          scorerCount={allScorers.length}
        />
      ) : (
        <Overview
          runs={allRuns}
          datasets={allDatasets}
          scorers={allScorers}
        />
      )}
    </div>
  )
}

/* ------------------------------------------------------------------------ */
/* First-run guided empty state: teach the dataset → scorer → run loop.     */
/* ------------------------------------------------------------------------ */

function GuidedStart({
  datasetCount,
  scorerCount,
}: {
  datasetCount: number
  scorerCount: number
}) {
  const navigate = useNavigate()

  const steps = [
    {
      key: 'dataset',
      title: 'Create a dataset',
      description:
        'Collect the inputs and expected outputs you want to grade against.',
      done: datasetCount > 0,
      doneNote: `${datasetCount} dataset${datasetCount === 1 ? '' : 's'} ready`,
      cta: 'Create dataset',
      onClick: () => void navigate({ to: '/evaluations/datasets' }),
    },
    {
      key: 'scorer',
      title: 'Add a scorer',
      description:
        'Define how outputs are graded: numeric, boolean, LLM judge, or code.',
      done: scorerCount > 0,
      doneNote: `${scorerCount} scorer${scorerCount === 1 ? '' : 's'} ready`,
      cta: 'Add scorer',
      onClick: () => void navigate({ to: '/evaluations/score-configs/new' }),
    },
    {
      key: 'run',
      title: 'Run your first eval',
      description:
        'Pick the dataset and scorers, choose a model or agent target, and start the run.',
      done: false,
      doneNote: '',
      cta: 'New eval run',
      onClick: () => void navigate({ to: '/evaluations/runs/new' }),
    },
  ]

  const currentIndex = steps.findIndex((step) => !step.done)

  return (
    <div className="flex min-h-full items-center justify-center px-6 py-10">
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-6">
        <div className={`text-center ${enterClass}`} style={enterDelay(0)}>
          <div className="mx-auto mb-4 flex h-9 w-9 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/70 light:border-brand-main-200 light:bg-white">
            <Iconify.Icon
              icon="lucide:flask-conical"
              className="size-4 text-brand-secondary-300 light:text-brand-secondary-700"
            />
          </div>
          <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
            Run your first eval
          </h3>
          <p className="mx-auto max-w-md text-sm leading-relaxed text-white/45 light:text-black/50">
            Evaluations grade your models and agents against a fixed dataset so
            you can see quality change over time. Three steps to your first
            result.
          </p>
        </div>

        <div className={`${cardClass} ${enterClass}`} style={enterDelay(1)}>
          {steps.map((step, index) => {
            const isCurrent = index === currentIndex
            return (
              <div
                key={step.key}
                className={`flex items-start gap-4 p-4 ${
                  index > 0
                    ? 'border-t border-brand-main-700/70 light:border-brand-main-200'
                    : ''
                }`}
              >
                <div
                  className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-xs font-medium ${
                    step.done
                      ? 'border-emerald-500/40 bg-emerald-500/15 text-emerald-400 light:text-emerald-600'
                      : isCurrent
                        ? 'border-brand-secondary-500/50 bg-brand-secondary-500/15 text-brand-secondary-300 light:text-brand-secondary-700'
                        : 'border-brand-main-600 text-white/40 light:border-brand-main-300 light:text-black/40'
                  }`}
                >
                  {step.done ? <Check className="h-3.5 w-3.5" /> : index + 1}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <h4 className="text-sm font-medium text-white light:text-brand-main-50">
                      {step.title}
                    </h4>
                    {step.done && (
                      <span className="text-xs text-emerald-400/80 light:text-emerald-600">
                        {step.doneNote}
                      </span>
                    )}
                  </div>
                  <p className="mt-1 text-xs leading-relaxed text-white/45 light:text-black/50">
                    {step.description}
                  </p>
                </div>
                <div className="shrink-0 pt-0.5">
                  {step.done ? (
                    <Button variant="ghost" size="sm" onClick={step.onClick}>
                      View
                    </Button>
                  ) : (
                    <Button
                      variant={isCurrent ? 'default' : 'outline'}
                      size="sm"
                      onClick={step.onClick}
                    >
                      {step.cta}
                    </Button>
                  )}
                </div>
              </div>
            )
          })}
        </div>

        <Link
          to="/evaluations/playgrounds"
          className={`group flex items-center gap-3 rounded border border-brand-main-700 bg-brand-main-950 p-4 transition-colors hover:border-brand-secondary-500/50 light:border-brand-main-200 light:bg-brand-main-50 ${enterClass}`}
          style={enterDelay(2)}
        >
          <Blocks className="h-4 w-4 shrink-0 text-brand-secondary-300 light:text-brand-secondary-700" />
          <div className="min-w-0 flex-1">
            <span className="text-sm font-medium text-white light:text-brand-main-50">
              Prefer to explore first?
            </span>
            <p className="mt-0.5 text-xs leading-relaxed text-white/45 light:text-black/50">
              Playgrounds let you iterate on prompts, models, and scorers side
              by side before formalizing an eval.
            </p>
          </div>
          <ArrowRight className="h-4 w-4 shrink-0 text-white/30 transition-colors group-hover:text-white/60 light:text-black/30 light:group-hover:text-black/60" />
        </Link>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------------ */
/* Power-user overview: summaries, recent runs, and jump-off points.        */
/* ------------------------------------------------------------------------ */

const RECENT_RUN_LIMIT = 8

function Overview({
  runs,
  datasets,
  scorers,
}: {
  runs: any[]
  datasets: any[]
  scorers: any[]
}) {
  const navigate = useNavigate()

  const runningCount = useMemo(
    () =>
      runs.filter((run: any) => run.status?.toLowerCase() === 'running').length,
    [runs],
  )
  const datasetItemTotal = useMemo(
    () =>
      datasets.reduce(
        (sum: number, dataset: any) => sum + Number(dataset.itemCount ?? 0),
        0,
      ),
    [datasets],
  )
  const scorerBreakdown = useMemo(() => {
    const judges = scorers.filter(
      (config: any) => config.dataType?.toLowerCase() === 'llm_judge',
    ).length
    const code = scorers.filter(
      (config: any) => config.dataType?.toLowerCase() === 'code_scorer',
    ).length
    const parts = [
      judges > 0 ? `${judges} LLM judge` : null,
      code > 0 ? `${code} code` : null,
    ].filter(Boolean)
    return parts.length > 0 ? parts.join(' · ') : 'Custom metrics library'
  }, [scorers])

  const recentRuns = useMemo(
    () => runs.slice(0, RECENT_RUN_LIMIT),
    [runs],
  )

  const summaries = [
    {
      key: 'runs',
      label: 'Eval runs',
      count: runs.length,
      sub:
        runningCount > 0
          ? `${runningCount} running now`
          : 'All runs settled',
      to: '/evaluations/runs' as const,
      icon: Play,
      live: runningCount > 0,
    },
    {
      key: 'datasets',
      label: 'Datasets',
      count: datasets.length,
      sub: `${datasetItemTotal} item${datasetItemTotal === 1 ? '' : 's'} total`,
      to: '/evaluations/datasets' as const,
      icon: Database,
      live: false,
    },
    {
      key: 'scorers',
      label: 'Scorers',
      count: scorers.length,
      sub: scorerBreakdown,
      to: '/evaluations/score-configs' as const,
      icon: Hash,
      live: false,
    },
  ]

  const exploreLinks = [
    {
      key: 'playgrounds',
      name: 'Playgrounds',
      description: 'Iterate on prompts, models, and scorers side by side.',
      to: '/evaluations/playgrounds' as const,
      icon: Blocks,
    },
    {
      key: 'online-evals',
      name: 'Online Evals',
      description: 'Sample live traces and score production continuously.',
      to: '/evaluations/online-evals' as const,
      icon: Activity,
    },
    {
      key: 'annotation-queues',
      name: 'Annotation Queues',
      description: 'Route outputs to humans for review and labeling.',
      to: '/evaluations/annotation-queues' as const,
      icon: ListTodo,
    },
  ]

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4 px-6 py-6">
      <div className="grid gap-4 sm:grid-cols-3">
        {summaries.map((summary, index) => (
          <Link
            key={summary.key}
            to={summary.to}
            className={`group ${cardClass} p-4 transition-colors hover:border-brand-secondary-500/50 ${enterClass}`}
            style={enterDelay(index)}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-2 text-xs font-medium text-white/55 light:text-black/55">
                <summary.icon className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
                {summary.label}
              </span>
              <ArrowRight className="h-3.5 w-3.5 text-white/25 transition-colors group-hover:text-white/60 light:text-black/25 light:group-hover:text-black/60" />
            </div>
            <div className="mt-3 text-2xl font-semibold tabular-nums text-white light:text-brand-main-50">
              {summary.count}
            </div>
            <div className="mt-1 flex items-center gap-1.5 text-xs text-white/45 light:text-black/50">
              {summary.live && (
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-blue-400 light:bg-blue-600" />
              )}
              {summary.sub}
            </div>
          </Link>
        ))}
      </div>

      <section className={`${cardClass} ${enterClass}`} style={enterDelay(3)}>
        <div className="flex items-center justify-between gap-3 border-b border-brand-main-700/70 px-4 py-3 light:border-brand-main-200">
          <h4 className="text-sm font-semibold text-white light:text-brand-main-50">
            Recent runs
          </h4>
          <div className="flex items-center gap-2">
            <Link
              to="/evaluations/runs"
              className="text-xs text-white/45 transition-colors hover:text-white light:text-black/45 light:hover:text-black"
            >
              View all
            </Link>
            <Button
              size="sm"
              onClick={() => void navigate({ to: '/evaluations/runs/new' })}
            >
              <Plus className="h-3.5 w-3.5" />
              New eval run
            </Button>
          </div>
        </div>
        <div>
          {recentRuns.map((run: any, index: number) => (
            <Link
              key={run.id}
              to="/evaluations/runs/$runId"
              params={{ runId: run.id }}
              className={`flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-brand-main-900 light:hover:bg-brand-main-100 ${
                index > 0
                  ? 'border-t border-brand-main-700/40 light:border-brand-main-200/70'
                  : ''
              }`}
            >
              <RunStatusIcon status={run.status} />
              <span className="flex min-w-0 flex-1 items-center gap-1.5">
                <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
                  {run.name}
                </span>
                {run.isBaseline && (
                  <span className="inline-flex shrink-0 items-center gap-0.5 rounded border border-amber-500/30 bg-amber-500/20 px-1 py-0.5 text-[10px] font-medium text-amber-400 light:text-amber-700">
                    <Flag className="h-2.5 w-2.5" /> Baseline
                  </span>
                )}
              </span>
              <span className="hidden max-w-[180px] truncate text-xs text-white/45 sm:block light:text-black/50">
                {run.datasetName ?? run.datasetId}
              </span>
              <span className="hidden font-mono text-xs tabular-nums text-white/45 sm:block light:text-black/50">
                {run.completedItems ?? 0}/{run.totalItems ?? 0}
              </span>
              <span className="hidden shrink-0 text-xs text-white/40 md:block light:text-black/45">
                {run.createdAt ? formatTimestamp(run.createdAt) : '-'}
              </span>
            </Link>
          ))}
        </div>
      </section>

      <div className="grid gap-4 sm:grid-cols-3">
        {exploreLinks.map((link, index) => (
          <Link
            key={link.key}
            to={link.to}
            className={`group ${cardClass} p-4 transition-colors hover:border-brand-secondary-500/50 ${enterClass}`}
            style={enterDelay(4 + index)}
          >
            <div className="mb-2 flex items-center gap-2">
              <link.icon className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
              <span className="text-sm font-medium text-white light:text-brand-main-50">
                {link.name}
              </span>
            </div>
            <p className="text-xs leading-relaxed text-white/45 light:text-black/50">
              {link.description}
            </p>
          </Link>
        ))}
      </div>
    </div>
  )
}
