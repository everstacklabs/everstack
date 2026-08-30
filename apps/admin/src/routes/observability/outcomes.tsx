import { useMemo, type ReactNode } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useAgents } from '@/hooks/deployments/use-agents'
import {
  useOutcomeDashboard,
  useOutcomeTimeSeries,
  type OutcomeFilterOptions,
} from '@/hooks/observability/use-outcomes'
import { TIME_RANGE_LABELS } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import dayjs from 'dayjs'
import {
  Activity,
  AlertTriangle,
  ArrowDownRight,
  ArrowUpRight,
  BarChart3,
  CheckCircle,
  ChevronRight,
  Container,
  Gauge,
  HeartPulse,
  Info,
  MessageSquare,
  Search,
  Shield,
  TrendingUp,
  Users,
  Wrench,
} from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { z } from 'zod'

type ProtoTimestamp = {
  seconds?: bigint | number
  nanos?: number
  toDate?: () => Date
}

type ScoreFormat = 'percent' | 'number' | 'boolean'
type ScoreCategory = 'Completion' | 'Tooling' | 'Safety' | 'Looping' | 'Sandbox'
type HealthStatus = 'healthy' | 'watch' | 'critical' | 'unknown'

type OutcomeDashboardData = {
  taskCompletionRate?: number
  toolSuccessRate?: number
  policyComplianceRate?: number
  loopHealthRate?: number
  iterationEfficiency?: number
  sandboxSuccessRate?: number
  totalEvaluations?: bigint | number
  uniqueSessions?: bigint | number
}

type OutcomeScoreSummary = {
  scoreName: string
  dataType?: string
  count?: bigint | number
  mean?: number
  min?: number
  max?: number
  p50?: number
  p95?: number
  passRate?: number
}

type ScoreMeta = {
  label: string
  shortLabel?: string
  category: ScoreCategory
  format: ScoreFormat
  higherIsBetter: boolean
  description: string
}

type DerivedScore = {
  meta: ScoreMeta
  scoreName: string
  score?: OutcomeScoreSummary
  previous?: OutcomeScoreSummary
  value: number | null
  previousValue: number | null
  healthValue: number | null
  previousHealthValue: number | null
  delta: number | null
  healthDelta: number | null
  count: number
  coverage: number | null
  status: HealthStatus
}

const {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Separator,
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  Tabs,
  TabsList,
  TabsTrigger,
  Tooltip: UiTooltip,
  TooltipProvider,
} = ui

const { EChart, brandTooltip, timeAxis, valueAxis, baseGrid, areaGradient, useChartMode } = ui

const CHART_COLOR = '#8b5cf6'

const SCORE_METADATA: Record<string, ScoreMeta> = {
  'task_completion.finished': {
    label: 'Task Completion',
    category: 'Completion',
    format: 'boolean',
    higherIsBetter: true,
    description:
      'Whether the turn ended normally instead of timing out, stalling, or exhausting iterations.',
  },
  'task_completion.responsive': {
    label: 'Task Responsiveness',
    category: 'Completion',
    format: 'boolean',
    higherIsBetter: true,
    description:
      'Whether the assistant response actually addressed the user input, not just produced output.',
  },
  'task_completion.efficiency': {
    label: 'Iteration Efficiency',
    category: 'Completion',
    format: 'percent',
    higherIsBetter: true,
    description:
      'Ratio of productive iterations to total iterations, penalizing retries and wasted steps.',
  },
  'tool_quality.success_rate': {
    label: 'Tool Success Rate',
    category: 'Tooling',
    format: 'percent',
    higherIsBetter: true,
    description:
      'Share of tool calls that succeeded without returning an execution error.',
  },
  'tool_quality.args_valid': {
    label: 'Tool Args Validity',
    category: 'Tooling',
    format: 'boolean',
    higherIsBetter: true,
    description: 'Whether tool calls used parseable, well-formed arguments.',
  },
  'tool_quality.result_used': {
    label: 'Tool Result Usage',
    category: 'Tooling',
    format: 'percent',
    higherIsBetter: true,
    description:
      'How often tool outputs are actually used in the assistant response.',
  },
  'policy.compliant': {
    label: 'Policy Compliance',
    category: 'Safety',
    format: 'boolean',
    higherIsBetter: true,
    description:
      'Whether the turn completed without policy violations such as blocked patterns or safety issues.',
  },
  'loop_health.looping': {
    label: 'Looping Risk',
    category: 'Looping',
    format: 'boolean',
    higherIsBetter: false,
    description:
      'Whether the agent repeated the same tool pattern instead of making progress.',
  },
  'loop_health.stalled': {
    label: 'Stall Risk',
    category: 'Looping',
    format: 'boolean',
    higherIsBetter: false,
    description:
      'Whether the turn ended due to making no progress or returning no useful result.',
  },
  'loop_health.tool_density': {
    label: 'Tool Density',
    category: 'Looping',
    format: 'number',
    higherIsBetter: false,
    description:
      'Average tool calls per iteration. Higher density can signal over-tooling or churn.',
  },
  'sandbox_hygiene.exit_code_rate': {
    label: 'Sandbox Success',
    category: 'Sandbox',
    format: 'percent',
    higherIsBetter: true,
    description:
      'Share of sandbox commands that exited successfully with code 0.',
  },
  'sandbox_hygiene.stderr_volume': {
    label: 'Stderr Noise Risk',
    category: 'Sandbox',
    format: 'boolean',
    higherIsBetter: false,
    description:
      'Whether runs produced heavy stderr output, which often correlates with instability or misuse.',
  },
}

const HEALTH_PILLARS = [
  {
    key: 'completion',
    label: 'Completion Health',
    icon: CheckCircle,
    description: 'Do turns finish and respond appropriately?',
    scoreNames: [
      'task_completion.finished',
      'task_completion.responsive',
      'task_completion.efficiency',
    ],
  },
  {
    key: 'tooling',
    label: 'Tool Quality',
    icon: Wrench,
    description: 'Are tools succeeding and producing usable results?',
    scoreNames: [
      'tool_quality.success_rate',
      'tool_quality.args_valid',
      'tool_quality.result_used',
    ],
  },
  {
    key: 'safety',
    label: 'Safety & Compliance',
    icon: Shield,
    description: 'Are turns staying within expected policy boundaries?',
    scoreNames: ['policy.compliant'],
  },
  {
    key: 'looping',
    label: 'Loop & Stall Risk',
    icon: HeartPulse,
    description: 'Are agents getting stuck or wasting cycles?',
    scoreNames: [
      'loop_health.looping',
      'loop_health.stalled',
      'loop_health.tool_density',
    ],
  },
  {
    key: 'sandbox',
    label: 'Execution Reliability',
    icon: Container,
    description: 'Is the execution layer helping or hurting outcomes?',
    scoreNames: [
      'sandbox_hygiene.exit_code_rate',
      'sandbox_hygiene.stderr_volume',
    ],
  },
] as const

const SCORE_CHARTS = [
  'task_completion.finished',
  'task_completion.responsive',
  'task_completion.efficiency',
  'tool_quality.success_rate',
  'tool_quality.args_valid',
  'tool_quality.result_used',
  'policy.compliant',
  'loop_health.looping',
  'loop_health.stalled',
  'loop_health.tool_density',
  'sandbox_hygiene.exit_code_rate',
  'sandbox_hygiene.stderr_volume',
] as const

const SCORE_SORT_OPTIONS = {
  impact: 'Biggest regressions',
  value: 'Current value',
  delta: 'Delta vs previous',
  coverage: 'Coverage',
  count: 'Sample size',
  name: 'Name',
} as const

const outcomesSearchSchema = z.object({
  tab: z.enum(['overview', 'trends', 'scores']).optional().default('overview'),
  range: z
    .enum(
      Object.keys(TIME_RANGE_LABELS).filter((k) => k !== 'custom') as [
        string,
        ...string[],
      ],
    )
    .optional()
    .default('24h'),
  agent: z.string().optional().default('all'),
  // Score explorer filters
  q: z.string().optional(),
  category: z
    .enum(['all', 'Completion', 'Tooling', 'Safety', 'Looping', 'Sandbox'])
    .optional()
    .default('all'),
  status: z
    .enum(['all', 'healthy', 'watch', 'critical', 'unknown'])
    .optional()
    .default('all'),
  sort: z
    .enum(['impact', 'value', 'delta', 'coverage', 'count', 'name'])
    .optional()
    .default('impact'),
  score: z.string().optional(),
})

export const Route = createFileRoute('/observability/outcomes')({
  component: OutcomeDashboardPage,
  validateSearch: outcomesSearchSchema,
})

function tsToISO(ts: ProtoTimestamp | undefined): string {
  if (!ts) return ''
  if (typeof ts.toDate === 'function') return ts.toDate().toISOString()
  const seconds =
    typeof ts.seconds === 'bigint'
      ? Number(ts.seconds)
      : Number(ts.seconds ?? 0)
  return new Date(seconds * 1000).toISOString()
}

function toNumber(value: bigint | number | undefined | null): number {
  if (typeof value === 'bigint') return Number(value)
  return Number(value ?? 0)
}

function clamp01(value: number): number {
  return Math.max(0, Math.min(1, value))
}

function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}

function formatSignedPoints(value: number | null): string {
  if (value === null) return '--'
  const sign = value > 0 ? '+' : value < 0 ? '' : ''
  return `${sign}${(value * 100).toFixed(1)} pp`
}

function formatDelta(value: number | null, format: ScoreFormat): string {
  if (value === null) return '--'
  if (format === 'number') {
    const sign = value > 0 ? '+' : value < 0 ? '' : ''
    return `${sign}${formatNumber(value)}`
  }
  return formatSignedPoints(value)
}

function formatNumber(value: number): string {
  if (value === 0) return '0'
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  if (Number.isInteger(value)) return value.toFixed(0)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}

function formatValue(value: number | null, format: ScoreFormat): string {
  if (value === null) return '--'
  if (format === 'percent' || format === 'boolean') return formatPercent(value)
  return formatNumber(value)
}

function statusFromHealth(value: number | null): HealthStatus {
  if (value === null) return 'unknown'
  if (value >= 0.9) return 'healthy'
  if (value >= 0.75) return 'watch'
  return 'critical'
}

function confidenceLabel(totalEvaluations: number): string {
  if (totalEvaluations >= 1000) return 'Very high confidence'
  if (totalEvaluations >= 250) return 'High confidence'
  if (totalEvaluations >= 50) return 'Moderate confidence'
  if (totalEvaluations > 0) return 'Low confidence'
  return 'No data yet'
}

function getMetricClassName(status: HealthStatus): string {
  if (status === 'healthy') return 'text-brand-secondary-300'
  if (status === 'watch') return 'text-white light:text-brand-main-50'
  if (status === 'critical') return 'text-white light:text-brand-main-50'
  return 'text-white/40 light:text-black/40'
}

function getStatusBadgeClass(status: HealthStatus): string {
  if (status === 'healthy')
    return 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600 border-emerald-500/25'
  if (status === 'watch')
    return 'bg-amber-500/15 text-amber-300 light:text-amber-700 border-amber-500/25'
  if (status === 'critical')
    return 'bg-red-500/15 text-red-400 light:text-red-600 border-red-500/25'
  return 'bg-brand-main-500/30 text-brand-main-200 border-brand-main-500/25'
}

function getStatusLabel(status: HealthStatus): string {
  if (status === 'healthy') return 'Healthy'
  if (status === 'watch') return 'Watch'
  if (status === 'critical') return 'Critical'
  return 'Unknown'
}

function getScoreValue(
  score: OutcomeScoreSummary | undefined,
  meta: ScoreMeta,
): number | null {
  if (!score) return null
  if (meta.format === 'boolean') {
    return typeof score.passRate === 'number'
      ? score.passRate
      : typeof score.mean === 'number'
        ? score.mean
        : null
  }
  return typeof score.mean === 'number' ? score.mean : null
}

function getHealthValue(
  scoreName: string,
  value: number | null,
  meta: ScoreMeta,
): number | null {
  if (value === null) return null
  if (scoreName === 'loop_health.tool_density') {
    return clamp01(1 - Math.max(value - 1, 0) / 2)
  }
  if (meta.format === 'number' && meta.higherIsBetter) return clamp01(value)
  if (meta.format === 'number' && !meta.higherIsBetter)
    return clamp01(1 - value)
  return meta.higherIsBetter ? clamp01(value) : clamp01(1 - value)
}

function shiftFilters(filters: OutcomeFilterOptions): OutcomeFilterOptions {
  const start = new Date(filters.startTime ?? 0)
  const end = new Date(filters.endTime ?? 0)
  const duration = end.getTime() - start.getTime()
  return {
    ...filters,
    startTime: new Date(start.getTime() - duration).toISOString(),
    endTime: start.toISOString(),
  }
}

function average(values: Array<number | null | undefined>): number | null {
  const valid = values.filter(
    (value): value is number => typeof value === 'number',
  )
  if (valid.length === 0) return null
  return valid.reduce((sum, value) => sum + value, 0) / valid.length
}

function OutcomeDashboardPage() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const timeRange = search.range || '24h'
  const activeTab = search.tab || 'overview'
  const selectedAgentId = search.agent || 'all'
  const { data: agents = [] } = useAgents({ limit: 200 })

  const baseFilters = useMemo(() => {
    const msMap: Record<string, number> = {
      '15m': 15 * 60 * 1000,
      '6h': 6 * 60 * 60 * 1000,
      '12h': 12 * 60 * 60 * 1000,
      '24h': 24 * 60 * 60 * 1000,
      '3d': 3 * 24 * 60 * 60 * 1000,
      '7d': 7 * 24 * 60 * 60 * 1000,
      '14d': 14 * 24 * 60 * 60 * 1000,
      '30d': 30 * 24 * 60 * 60 * 1000,
      '90d': 90 * 24 * 60 * 60 * 1000,
    }
    const rangeMs = msMap[timeRange] ?? 24 * 60 * 60 * 1000
    const now = new Date()
    now.setSeconds(0, 0)

    return {
      startTime: new Date(now.getTime() - rangeMs).toISOString(),
      endTime: now.toISOString(),
      interval:
        rangeMs <= 6 * 60 * 60 * 1000
          ? '5minute'
          : rangeMs <= 24 * 60 * 60 * 1000
            ? 'hour'
            : rangeMs <= 7 * 24 * 60 * 60 * 1000
              ? '6hour'
              : 'day',
    } satisfies OutcomeFilterOptions
  }, [timeRange])

  const filters = useMemo(
    () =>
      selectedAgentId === 'all'
        ? baseFilters
        : { ...baseFilters, agentId: selectedAgentId },
    [baseFilters, selectedAgentId],
  )

  const previousFilters = useMemo(() => shiftFilters(filters), [filters])
  const globalPreviousFilters = useMemo(
    () => shiftFilters(baseFilters),
    [baseFilters],
  )
  const { data: currentResp, isLoading } = useOutcomeDashboard(filters)
  const { data: previousResp } = useOutcomeDashboard(previousFilters)
  const { data: globalResp } = useOutcomeDashboard(baseFilters)
  const { data: globalPreviousResp } = useOutcomeDashboard(
    globalPreviousFilters,
  )

  const currentDashboard = currentResp?.dashboard as
    | OutcomeDashboardData
    | undefined
  const previousDashboard = previousResp?.dashboard as
    | OutcomeDashboardData
    | undefined
  const globalDashboard = globalResp?.dashboard as
    | OutcomeDashboardData
    | undefined
  const globalPreviousDashboard = globalPreviousResp?.dashboard as
    | OutcomeDashboardData
    | undefined
  const currentScores = (currentResp?.scores ?? []) as OutcomeScoreSummary[]
  const previousScores = (previousResp?.scores ?? []) as OutcomeScoreSummary[]
  const selectedAgent =
    selectedAgentId === 'all'
      ? null
      : (agents.find((agent) => agent.id === selectedAgentId) ?? null)

  const currentScoreMap = useMemo(
    () => new Map(currentScores.map((score) => [score.scoreName, score])),
    [currentScores],
  )
  const previousScoreMap = useMemo(
    () => new Map(previousScores.map((score) => [score.scoreName, score])),
    [previousScores],
  )

  const totalEvaluations = toNumber(currentDashboard?.totalEvaluations)
  const uniqueSessions = toNumber(currentDashboard?.uniqueSessions)

  const derivedScores = useMemo<DerivedScore[]>(() => {
    const scoreNames = Array.from(
      new Set([
        ...Object.keys(SCORE_METADATA),
        ...currentScoreMap.keys(),
        ...previousScoreMap.keys(),
      ]),
    )

    return scoreNames.map((scoreName) => {
      const meta = SCORE_METADATA[scoreName] ?? {
        label: scoreName,
        category: 'Completion' as ScoreCategory,
        format: 'number' as ScoreFormat,
        higherIsBetter: true,
        description: scoreName,
      }
      const score = currentScoreMap.get(scoreName)
      const previous = previousScoreMap.get(scoreName)
      const value = getScoreValue(score, meta)
      const previousValue = getScoreValue(previous, meta)
      const healthValue = getHealthValue(scoreName, value, meta)
      const previousHealthValue = getHealthValue(scoreName, previousValue, meta)
      const count = toNumber(score?.count)
      const coverage = totalEvaluations > 0 ? count / totalEvaluations : null

      return {
        meta,
        scoreName,
        score,
        previous,
        value,
        previousValue,
        healthValue,
        previousHealthValue,
        delta:
          value !== null && previousValue !== null
            ? value - previousValue
            : null,
        healthDelta:
          healthValue !== null && previousHealthValue !== null
            ? healthValue - previousHealthValue
            : null,
        count,
        coverage,
        status: statusFromHealth(healthValue),
      }
    })
  }, [currentScoreMap, previousScoreMap, totalEvaluations])

  const pillarCards = useMemo(() => {
    return HEALTH_PILLARS.map((pillar) => {
      const scores = pillar.scoreNames
        .map((scoreName) =>
          derivedScores.find((entry) => entry.scoreName === scoreName),
        )
        .filter((entry): entry is DerivedScore => Boolean(entry))
      const value = average(scores.map((entry) => entry.healthValue))
      const previousValue = average(
        scores.map((entry) => entry.previousHealthValue),
      )
      return {
        ...pillar,
        value,
        previousValue,
        delta:
          value !== null && previousValue !== null
            ? value - previousValue
            : null,
        status: statusFromHealth(value),
        scores,
      }
    })
  }, [derivedScores])

  const overallHealth = useMemo(() => {
    const current = average(pillarCards.map((pillar) => pillar.value))
    const previous = average(pillarCards.map((pillar) => pillar.previousValue))
    return {
      value: current,
      previousValue: previous,
      delta: current !== null && previous !== null ? current - previous : null,
      status: statusFromHealth(current),
    }
  }, [pillarCards])

  const regressions = useMemo(
    () =>
      derivedScores
        .filter((entry) => entry.healthDelta !== null)
        .sort((a, b) => (a.healthDelta ?? 0) - (b.healthDelta ?? 0))
        .slice(0, 4),
    [derivedScores],
  )

  const improvements = useMemo(
    () =>
      derivedScores
        .filter((entry) => entry.healthDelta !== null)
        .sort((a, b) => (b.healthDelta ?? 0) - (a.healthDelta ?? 0))
        .slice(0, 4),
    [derivedScores],
  )

  const newlyCritical = useMemo(
    () =>
      derivedScores.filter(
        (entry) =>
          entry.status === 'critical' &&
          entry.previousHealthValue !== null &&
          statusFromHealth(entry.previousHealthValue) !== 'critical',
      ),
    [derivedScores],
  )

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      <div className="flex-shrink-0 border-b border-brand-main-700 bg-brand-main-900/50 px-3 py-1.5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Tabs
            value={activeTab}
            onValueChange={(value) =>
              navigate({
                search: (prev) => ({ ...prev, tab: value as 'overview' | 'trends' | 'scores' }),
                replace: true,
              })
            }
            className="w-auto"
          >
            <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
              <TabsTrigger
                className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                value="overview"
              >
                Overview
              </TabsTrigger>
              <TabsTrigger
                className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                value="trends"
              >
                Trends
              </TabsTrigger>
              <TabsTrigger
                className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                value="scores"
              >
                Score Explorer
              </TabsTrigger>
            </TabsList>
          </Tabs>

          <div className="flex flex-wrap items-center gap-2">
            <Select
              value={timeRange}
              onValueChange={(value) =>
                navigate({
                  search: (prev) => ({ ...prev, range: value }),
                  replace: true,
                })
              }
            >
              <SelectTrigger className="h-8 w-[160px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="border-brand-main-600 bg-brand-main-900">
                {Object.entries(TIME_RANGE_LABELS)
                  .filter(([key]) => key !== 'custom')
                  .map(([key, label]) => (
                    <SelectItem
                      key={key}
                      value={key}
                      className="text-sm text-white/70 light:text-black/70 focus:bg-brand-main-800 focus:text-white light:focus:text-brand-main-50"
                    >
                      {label}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>

            <Select
              value={selectedAgentId}
              onValueChange={(value) =>
                navigate({
                  search: (prev) => ({ ...prev, agent: value }),
                  replace: true,
                })
              }
            >
              <SelectTrigger className="h-8 w-[220px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
                <SelectValue placeholder="All agents" />
              </SelectTrigger>
              <SelectContent className="border-brand-main-600 bg-brand-main-900">
                <SelectItem value="all">All agents</SelectItem>
                {agents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.id}>
                    {agent.name || agent.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {activeTab === 'overview' && (
          <OverviewTab
            dashboard={currentDashboard}
            previousDashboard={previousDashboard}
            globalDashboard={globalDashboard}
            globalPreviousDashboard={globalPreviousDashboard}
            isLoading={isLoading}
            totalEvaluations={totalEvaluations}
            uniqueSessions={uniqueSessions}
            overallHealth={overallHealth}
            pillarCards={pillarCards}
            regressions={regressions}
            improvements={improvements}
            newlyCritical={newlyCritical}
            selectedAgent={selectedAgent}
            onOpenTraces={() =>
              navigate({
                to: '/observability/traces',
                search: (prev) => ({
                  ...prev,
                  live: 'false',
                  range: timeRange as TimeRangePreset,
                }),
              })
            }
            onOpenSessions={() =>
              selectedAgent
                ? navigate({
                  to: '/deployments/agents/$agentId/sessions',
                  params: { agentId: selectedAgent.id },
                })
                : navigate({ to: '/observability/sessions' })
            }
          />
        )}
        {activeTab === 'trends' && (
          <TrendsTab
            filters={filters}
            previousFilters={previousFilters}
            timeRange={timeRange}
            derivedScores={derivedScores}
          />
        )}
        {activeTab === 'scores' && (
          <ScoreBreakdownTab
            scores={derivedScores}
            totalEvaluations={totalEvaluations}
            filters={filters}
            previousFilters={previousFilters}
            timeRange={timeRange}
            isLoading={isLoading}
            selectedAgent={selectedAgent}
            searchQuery={search.q ?? ''}
            categoryFilter={(search.category ?? 'all') as 'all' | ScoreCategory}
            statusFilter={(search.status ?? 'all') as 'all' | HealthStatus}
            sortBy={(search.sort ?? 'impact') as keyof typeof SCORE_SORT_OPTIONS}
            selectedScoreName={search.score ?? null}
            onSearchChange={(value) =>
              navigate({
                search: (prev) => ({ ...prev, q: value || undefined }),
                replace: true,
              })
            }
            onCategoryChange={(value) =>
              navigate({
                search: (prev) => ({ ...prev, category: value as 'all' | ScoreCategory }),
                replace: true,
              })
            }
            onStatusChange={(value) =>
              navigate({
                search: (prev) => ({ ...prev, status: value as 'all' | HealthStatus }),
                replace: true,
              })
            }
            onSortChange={(value) =>
              navigate({
                search: (prev) => ({ ...prev, sort: value as keyof typeof SCORE_SORT_OPTIONS }),
                replace: true,
              })
            }
            onScoreSelect={(scoreName) =>
              navigate({
                search: (prev) => ({ ...prev, score: scoreName ?? undefined }),
                replace: true,
              })
            }
            onOpenTraces={() =>
              navigate({
                to: '/observability/traces',
                search: (prev) => ({
                  ...prev,
                  live: 'false',
                  range: timeRange as TimeRangePreset,
                }),
              })
            }
            onOpenSessions={() =>
              selectedAgent
                ? navigate({
                  to: '/deployments/agents/$agentId/sessions',
                  params: { agentId: selectedAgent.id },
                })
                : navigate({ to: '/observability/sessions' })
            }
          />
        )}
      </div>
    </div>
  )
}

function OverviewTab({
  dashboard,
  previousDashboard,
  globalDashboard,
  globalPreviousDashboard,
  isLoading,
  totalEvaluations,
  uniqueSessions,
  overallHealth,
  pillarCards,
  regressions,
  improvements,
  newlyCritical,
  selectedAgent,
  onOpenTraces,
  onOpenSessions,
}: {
  dashboard?: OutcomeDashboardData
  previousDashboard?: OutcomeDashboardData
  globalDashboard?: OutcomeDashboardData
  globalPreviousDashboard?: OutcomeDashboardData
  isLoading: boolean
  totalEvaluations: number
  uniqueSessions: number
  overallHealth: {
    value: number | null
    previousValue: number | null
    delta: number | null
    status: HealthStatus
  }
  pillarCards: Array<{
    key: string
    label: string
    icon: typeof CheckCircle
    description: string
    value: number | null
    previousValue: number | null
    delta: number | null
    status: HealthStatus
    scores: DerivedScore[]
  }>
  regressions: DerivedScore[]
  improvements: DerivedScore[]
  newlyCritical: DerivedScore[]
  selectedAgent: { id: string; name?: string } | null
  onOpenTraces: () => void
  onOpenSessions: () => void
}) {
  const kpiCards = [
    {
      label: 'Task Completion',
      icon: CheckCircle,
      value: dashboard?.taskCompletionRate ?? 0,
      previousValue: previousDashboard?.taskCompletionRate ?? 0,
      description: 'Share of turns that ended normally.',
    },
    {
      label: 'Tool Success',
      icon: Wrench,
      value: dashboard?.toolSuccessRate ?? 0,
      previousValue: previousDashboard?.toolSuccessRate ?? 0,
      description: 'Share of successful tool calls.',
    },
    {
      label: 'Policy Compliance',
      icon: Shield,
      value: dashboard?.policyComplianceRate ?? 0,
      previousValue: previousDashboard?.policyComplianceRate ?? 0,
      description: 'Share of turns without policy violations.',
    },
    {
      label: 'Loop Health',
      icon: HeartPulse,
      value: dashboard?.loopHealthRate ?? 0,
      previousValue: previousDashboard?.loopHealthRate ?? 0,
      description: 'How often turns avoid looping behavior.',
    },
    {
      label: 'Iteration Efficiency',
      icon: Gauge,
      value: dashboard?.iterationEfficiency ?? 0,
      previousValue: previousDashboard?.iterationEfficiency ?? 0,
      description: 'How efficiently turns use their iterations.',
    },
    {
      label: 'Sandbox Success',
      icon: Container,
      value: dashboard?.sandboxSuccessRate ?? 0,
      previousValue: previousDashboard?.sandboxSuccessRate ?? 0,
      description: 'How often sandbox commands succeed.',
    },
  ]

  const globalHealthBaseline = average([
    globalDashboard?.taskCompletionRate ?? null,
    globalDashboard?.toolSuccessRate ?? null,
    globalDashboard?.policyComplianceRate ?? null,
    globalDashboard?.loopHealthRate ?? null,
    globalDashboard?.iterationEfficiency ?? null,
    globalDashboard?.sandboxSuccessRate ?? null,
  ])

  const globalHealthBaselineDelta = average([
    globalPreviousDashboard?.taskCompletionRate ?? null,
    globalPreviousDashboard?.toolSuccessRate ?? null,
    globalPreviousDashboard?.policyComplianceRate ?? null,
    globalPreviousDashboard?.loopHealthRate ?? null,
    globalPreviousDashboard?.iterationEfficiency ?? null,
    globalPreviousDashboard?.sandboxSuccessRate ?? null,
  ])

  return (
    <div className="space-y-3 p-3">
      {selectedAgent && (
        <Card className="border-brand-main-600 bg-brand-main-900/50">
          <CardContent className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div>
              <div className="flex items-center gap-2">
                <Badge variant="secondary">Scoped agent</Badge>
                <span className="text-sm font-medium text-white light:text-brand-main-50">
                  {selectedAgent.name || selectedAgent.id}
                </span>
              </div>
              <div className="mt-1 text-xs text-white/50 light:text-black/50">
                This view isolates one agent and compares it against
                instance-wide outcome health.
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="rounded border border-brand-main-700 bg-brand-main-800/35 py-1 px-2 text-right">
                <div className="text-[11px] uppercase tracking-wide text-white/40 light:text-black/40">
                  vs instance baseline
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">
                  {overallHealth.value !== null && globalHealthBaseline !== null
                    ? formatSignedPoints(
                      overallHealth.value - globalHealthBaseline,
                    )
                    : '--'}
                </div>
              </div>
              <TooltipProvider>
                <UiTooltip content="Agent sessions">
                  <Button variant="ghost" className="h-8 w-8 p-0" onClick={onOpenSessions}>
                    <MessageSquare className="h-4 w-4" />
                  </Button>
                </UiTooltip>
              </TooltipProvider>
              <TooltipProvider>
                <UiTooltip content="Open traces">
                  <Button variant="outline" className="h-8 w-8 p-0" onClick={onOpenTraces}>
                    <Activity className="h-4 w-4" />
                  </Button>
                </UiTooltip>
              </TooltipProvider>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-3 lg:grid-cols-[1.5fr,1fr]">
        <Card className="border-brand-main-600 bg-brand-main-900/50">
          <CardHeader>
            <div className="flex items-start justify-between -mx-2 -mb-4">
              <div>
                <CardTitle className="text-sm font-medium text-white light:text-brand-main-50">
                  Agent Outcome Health
                </CardTitle>
                <div className="mt-1 text-xs text-white/50 light:text-black/50">
                  Quality view of how agents behave, not just whether
                  infrastructure is up.
                </div>
              </div>
              <Badge className={getStatusBadgeClass(overallHealth.status)}>
                {getStatusLabel(overallHealth.status)}
              </Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap items-end justify-between gap-3 rounded border border-brand-main-700 bg-brand-main-800/40 px-2 py-3">
              <div>
                <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                  Composite outcome score
                </div>
                <div
                  className={cn(
                    'mt-1 text-xl font-semibold',
                    getMetricClassName(overallHealth.status),
                  )}
                >
                  {formatValue(overallHealth.value, 'percent')}
                </div>
              </div>
              <div className="space-y-0.5 text-right">
                <div className="text-xs text-white/70 light:text-black/70">
                  {formatSignedPoints(overallHealth.delta)} vs previous period
                </div>
                <div className="text-[11px] text-white/45 light:text-black/45">
                  {confidenceLabel(totalEvaluations)} from{' '}
                  {formatNumber(totalEvaluations)} evaluations across{' '}
                  {formatNumber(uniqueSessions)} sessions
                </div>
                {selectedAgent && globalHealthBaseline !== null && (
                  <div className="text-xs text-white/35 light:text-black/35">
                    Instance baseline {formatPercent(globalHealthBaseline)}
                    {globalHealthBaselineDelta !== null && (
                      <span>
                        {' '}
                        (
                        {formatSignedPoints(
                          globalHealthBaseline - globalHealthBaselineDelta,
                        )}
                        )
                      </span>
                    )}
                  </div>
                )}
              </div>
            </div>

            <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
              {pillarCards.map((pillar) => {
                const Icon = pillar.icon
                return (
                  <div
                    key={pillar.key}
                    className="rounded border border-brand-main-700 bg-brand-main-800/30 p-2.5"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <div className="rounded border border-brand-secondary-500/30 bg-brand-secondary-600/15 p-1.5 text-brand-secondary-300">
                          <Icon className="h-3.5 w-3.5" />
                        </div>
                        <div>
                          <div className="text-xs font-medium text-white light:text-brand-main-50">
                            {pillar.label}
                          </div>
                          <div className="text-[10px] text-white/45 light:text-black/45">
                            {pillar.description}
                          </div>
                        </div>
                      </div>
                      <Badge className={getStatusBadgeClass(pillar.status)}>
                        {getStatusLabel(pillar.status)}
                      </Badge>
                    </div>
                    <div className="mt-2 flex items-end justify-between gap-3">
                      <div
                        className={cn(
                          'text-lg font-semibold',
                          getMetricClassName(pillar.status),
                        )}
                      >
                        {formatValue(pillar.value, 'percent')}
                      </div>
                      <div className="text-right text-[11px] text-white/45 light:text-black/45">
                        <div>{formatSignedPoints(pillar.delta)}</div>
                        <div>{pillar.scores.length} signals</div>
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>
          </CardContent>
        </Card>

        <Card className="border-brand-main-600 bg-brand-main-900/50">
          <CardHeader>
            <CardTitle className="text-sm font-medium text-white light:text-brand-main-50 -mx-2 -mb-4">
              What Changed
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <ChangeList
              title="Biggest regressions"
              icon={<ArrowDownRight className="h-4 w-4 text-white/60 light:text-black/60" />}
              items={regressions}
              emptyText="No regressions detected in this period."
            />
            <Separator className="bg-brand-main-700" />
            <ChangeList
              title="Biggest improvements"
              icon={
                <ArrowUpRight className="h-4 w-4 text-brand-secondary-300" />
              }
              items={improvements}
              emptyText="No improvements detected in this period."
            />
            {newlyCritical.length > 0 && (
              <>
                <Separator className="bg-brand-main-700" />
                <div>
                  <div className="mb-2 flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-white/70 light:text-black/70">
                    <AlertTriangle className="h-3.5 w-3.5" />
                    Newly critical
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {newlyCritical.slice(0, 4).map((entry) => (
                      <Badge key={entry.scoreName} className="bg-red-500/15 text-red-400 light:text-red-600 border-red-500/25">
                        {entry.meta.label}
                      </Badge>
                    ))}
                  </div>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-2 grid-cols-3">
        {kpiCards.map((metric) => (
          <MetricCard
            key={metric.label}
            icon={<metric.icon className="h-4 w-4" />}
            label={metric.label}
            value={metric.value}
            previousValue={metric.previousValue}
            isLoading={isLoading}
          />
        ))}
        <StatCard
          icon={<BarChart3 className="h-4 w-4" />}
          label="Total Evaluations"
          value={formatNumber(totalEvaluations)}
        />
        <StatCard
          icon={<Users className="h-4 w-4" />}
          label="Unique Sessions"
          value={formatNumber(uniqueSessions)}
        />
        <StatCard
          icon={<TrendingUp className="h-4 w-4" />}
          label="Coverage Confidence"
          value={confidenceLabel(totalEvaluations)}
        />
      </div>

      {totalEvaluations === 0 && !isLoading && (
        <div className="flex flex-col items-center justify-center py-12">
          <div className="relative mb-6">
            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
              <Iconify.Icon icon="lucide:target" className="size-8 text-brand-secondary-400" />
            </div>
          </div>
          <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No evaluation data yet</h3>
          <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
            Outcome scores are generated automatically from agent turns. Run some agent sessions to see outcome health, regressions, and score explorer data here.
          </p>
        </div>
      )}
    </div>
  )
}

function TrendsTab({
  filters,
  previousFilters,
  timeRange,
  derivedScores,
}: {
  filters: OutcomeFilterOptions
  previousFilters: OutcomeFilterOptions
  timeRange: string
  derivedScores: DerivedScore[]
}) {
  return (
    <div className="space-y-3 p-3">
      <Card className="border-brand-main-600 bg-brand-main-900/50">
        <CardHeader>
          <div className="flex items-center justify-between -mx-2 -mb-2">
            <div>
              <CardTitle className="text-sm font-medium text-white light:text-brand-main-50">
                Trends That Explain Behavior
              </CardTitle>
              <div className="mt-1 text-xs text-white/50 light:text-black/50">
                Every chart compares the latest score against the previous period so
                flat lines still have context. Coverage and score descriptions stay
                visible so low-volume trends do not overstate confidence.
              </div>
            </div>
          </div>
        </CardHeader>
      </Card>

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-2">
        {SCORE_CHARTS.map((scoreName) => {
          const meta = SCORE_METADATA[scoreName]
          const derived = derivedScores.find(
            (entry) => entry.scoreName === scoreName,
          )
          return (
            <ScoreTrendChart
              key={scoreName}
              filters={filters}
              previousFilters={previousFilters}
              scoreName={scoreName}
              label={meta.label}
              format={meta.format}
              timeRange={timeRange}
              description={meta.description}
              aggregation={meta.format === 'boolean' ? 'rate_true' : 'avg'}
              coverage={derived?.coverage ?? null}
              latestValue={derived?.value ?? null}
              latestDelta={derived?.delta ?? null}
              status={derived?.status ?? 'unknown'}
            />
          )
        })}
      </div>
    </div>
  )
}

function ScoreTrendChart({
  filters,
  previousFilters,
  scoreName,
  label,
  aggregation,
  format,
  timeRange,
  description,
  coverage,
  latestValue,
  latestDelta,
  status,
}: {
  filters: OutcomeFilterOptions
  previousFilters: OutcomeFilterOptions
  scoreName: string
  label: string
  aggregation: string
  format: ScoreFormat
  timeRange: string
  description?: string
  coverage: number | null
  latestValue: number | null
  latestDelta: number | null
  status: HealthStatus
}) {
  const mode = useChartMode()
  const { data: currentTsResp } = useOutcomeTimeSeries(
    filters,
    scoreName,
    aggregation,
  )
  const { data: previousTsResp } = useOutcomeTimeSeries(
    previousFilters,
    scoreName,
    aggregation,
  )
  const currentSeries = currentTsResp?.series ?? []
  const previousSeries = previousTsResp?.series ?? []

  const chartData = useMemo(() => {
    const buckets = currentSeries[0]?.buckets ?? []
    return buckets
      .map((bucket: { timestamp?: ProtoTimestamp; value?: number }) => ({
        timestamp: new Date(tsToISO(bucket.timestamp)).getTime(),
        value: bucket.value ?? 0,
      }))
      .sort(
        (a: { timestamp: number }, b: { timestamp: number }) =>
          a.timestamp - b.timestamp,
      )
  }, [currentSeries])

  const previousLatest = useMemo(() => {
    const buckets = previousSeries[0]?.buckets ?? []
    if (buckets.length === 0) return null
    const latest = buckets[buckets.length - 1]
    return latest?.value ?? null
  }, [previousSeries])

  const timeFmt =
    timeRange === '15m' || timeRange === '6h' || timeRange === '12h' || timeRange === '24h'
      ? 'HH:mm'
      : 'MMM D'

  const option = useMemo(
    () => ({
      grid: baseGrid({ left: 8, right: 8, top: 8, bottom: 0 }),
      tooltip: brandTooltip({
        headerFormatter: (v) => dayjs(Number(v)).format('MMM D, HH:mm'),
        valueFormatter: (val) => formatValue(val, format),
      }),
      xAxis: timeAxis((v) => dayjs(v).format(timeFmt)),
      yAxis: valueAxis(
        (v) =>
          format === 'number' ? formatNumber(v) : `${(v * 100).toFixed(0)}%`,
        { position: 'left', ...(format === 'number' ? {} : { min: 0, max: 1 }) },
      ),
      series: [
        {
          name: label,
          type: 'line',
          smooth: true,
          symbol: 'none',
          data: chartData.map((d) => [d.timestamp, d.value]),
          lineStyle: { width: 1.5, color: CHART_COLOR },
          itemStyle: { color: CHART_COLOR },
          areaStyle: areaGradient(CHART_COLOR, 0.3, 0),
        },
      ],
    }),
    // `mode` rebuilds the option when the theme toggles (tooltip/axis getters are theme-aware)
    [chartData, timeFmt, format, label, mode],
  )

  return (
    <Card className="border-brand-main-600 bg-brand-main-900/50">
      <CardHeader className="!px-3 !pb-1 !pt-2.5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-1">
              <CardTitle className="text-xs font-medium text-white light:text-brand-main-50">
                {label}
              </CardTitle>
              {description && (
                <TooltipProvider>
                  <UiTooltip
                    content={
                      <div className="max-w-[220px] text-xs text-white/90 light:text-black/90">
                        {description}
                      </div>
                    }
                  >
                    <Info className="h-3 w-3 shrink-0 cursor-help text-white/25 light:text-black/25" />
                  </UiTooltip>
                </TooltipProvider>
              )}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-[10px] text-white/45 light:text-black/45">
              <span>
                Coverage {coverage !== null ? formatPercent(coverage) : '--'}
              </span>
              <span>Prev {formatValue(previousLatest, format)}</span>
            </div>
          </div>
          <div className="text-right">
            <div
              className={cn('text-xs font-mono', getMetricClassName(status))}
            >
              {formatValue(latestValue, format)}
            </div>
            <div className="text-[10px] text-white/40 light:text-black/40">
              {formatDelta(latestDelta, format)} vs prev
            </div>
          </div>
        </div>
      </CardHeader>
      <CardContent className="!px-2 !pb-2 !pt-0">
        {chartData.length > 0 ? (
          <EChart option={option} height={160} />
        ) : (
          <div className="flex h-[160px] flex-col items-center justify-center">
            <div className="relative mb-3">
              <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-lg" />
              <div className="relative rounded-lg border border-brand-main-600 bg-brand-main-800/80 p-2.5">
                <Iconify.Icon icon="heroicons:chart-bar" className="size-5 text-brand-secondary-400" />
              </div>
            </div>
            <span className="text-xs text-white/50 light:text-black/50">No data</span>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ScoreBreakdownTab({
  scores,
  totalEvaluations,
  filters,
  previousFilters,
  timeRange,
  isLoading,
  selectedAgent,
  searchQuery,
  categoryFilter,
  statusFilter,
  sortBy,
  selectedScoreName,
  onSearchChange,
  onCategoryChange,
  onStatusChange,
  onSortChange,
  onScoreSelect,
  onOpenTraces,
  onOpenSessions,
}: {
  scores: DerivedScore[]
  totalEvaluations: number
  filters: OutcomeFilterOptions
  previousFilters: OutcomeFilterOptions
  timeRange: string
  isLoading: boolean
  selectedAgent: { id: string; name?: string } | null
  searchQuery: string
  categoryFilter: 'all' | ScoreCategory
  statusFilter: 'all' | HealthStatus
  sortBy: keyof typeof SCORE_SORT_OPTIONS
  selectedScoreName: string | null
  onSearchChange: (value: string) => void
  onCategoryChange: (value: string) => void
  onStatusChange: (value: string) => void
  onSortChange: (value: string) => void
  onScoreSelect: (scoreName: string | null) => void
  onOpenTraces: () => void
  onOpenSessions: () => void
}) {
  const search = searchQuery
  const category = categoryFilter
  const status = statusFilter

  const filteredScores = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase()
    const visible = scores.filter((entry) => {
      const matchesSearch =
        normalizedSearch.length === 0 ||
        entry.scoreName.toLowerCase().includes(normalizedSearch) ||
        entry.meta.label.toLowerCase().includes(normalizedSearch)
      const matchesCategory =
        category === 'all' || entry.meta.category === category
      const matchesStatus = status === 'all' || entry.status === status
      return matchesSearch && matchesCategory && matchesStatus
    })

    return visible.sort((a, b) => {
      if (sortBy === 'name') return a.meta.label.localeCompare(b.meta.label)
      if (sortBy === 'value')
        return (b.healthValue ?? -1) - (a.healthValue ?? -1)
      if (sortBy === 'delta') return (a.healthDelta ?? 1) - (b.healthDelta ?? 1)
      if (sortBy === 'coverage') return (b.coverage ?? -1) - (a.coverage ?? -1)
      if (sortBy === 'count') return b.count - a.count
      return (a.healthDelta ?? 1) - (b.healthDelta ?? 1)
    })
  }, [category, scores, search, sortBy, status])

  const selectedScore =
    filteredScores.find((entry) => entry.scoreName === selectedScoreName) ??
    scores.find((entry) => entry.scoreName === selectedScoreName) ??
    null

  if (isLoading) {
    return (
      <div className="space-y-2 p-3">
        {[...Array(6)].map((_, index) => (
          <div
            key={index}
            className="h-16 animate-pulse rounded bg-brand-main-800/40"
          />
        ))}
      </div>
    )
  }

  if (scores.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center flex-1 h-full pb-24">
        <div className="relative mb-6">
          <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Iconify.Icon icon="heroicons:presentation-chart-line" className="size-8 text-brand-secondary-400" />
          </div>
        </div>
        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No score data available</h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
          Score data will appear here once agent evaluations have been processed.
        </p>
      </div>
    )
  }

  return (
    <>
      <div className="space-y-3">
        {(selectedAgent || filteredScores.length > 0) && (
          <Card className="border-brand-main-600 bg-brand-main-900/50 m-3">
            <CardContent className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  {selectedAgent && (
                    <Badge variant="secondary">
                      {selectedAgent.name || selectedAgent.id}
                    </Badge>
                  )}
                  <span className="text-sm font-medium text-white light:text-brand-main-50">
                    Investigation shortcuts
                  </span>
                </div>
                <div className="mt-1 text-xs text-white/50 light:text-black/50">
                  Jump from a score diagnosis into traces or sessions to inspect
                  the affected behavior in context.
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <TooltipProvider>
                  <UiTooltip content={selectedAgent ? 'Agent sessions' : 'All sessions'}>
                    <Button variant="ghost" className="h-8 w-8 p-0" onClick={onOpenSessions}>
                      <MessageSquare className="h-4 w-4" />
                    </Button>
                  </UiTooltip>
                </TooltipProvider>
                <TooltipProvider>
                  <UiTooltip content="Open traces">
                    <Button variant="outline" className="h-8 w-8 p-0" onClick={onOpenTraces}>
                      <Activity className="h-4 w-4" />
                    </Button>
                  </UiTooltip>
                </TooltipProvider>
              </div>
            </CardContent>
          </Card>
        )}

        <Card className="border-brand-main-600 bg-brand-main-900/50 m-3">
          <CardHeader>
            <div className="flex flex-col gap-3 -mx-3 -mb-4 xl:flex-row xl:items-center xl:justify-between">
              <div>
                <CardTitle className="text-sm font-medium text-white light:text-brand-main-50">
                  Score Explorer
                </CardTitle>
                <div className="mt-1 text-xs text-white/50 light:text-black/50">
                  Sort by regression, filter by category, and open any score for
                  a detailed view.
                </div>
              </div>
              <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
                <div className="relative min-w-[220px]">
                  <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/30" />
                  <Input
                    value={search}
                    onChange={(event) => onSearchChange(event.target.value)}
                    placeholder="Search scores"
                    className="border-brand-main-600 bg-brand-main-800/50 pl-8 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                  />
                </div>
                <Select
                  value={category}
                  onValueChange={onCategoryChange}
                >
                  <SelectTrigger className="h-9 w-[170px] border-brand-main-600 bg-brand-main-800/50 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="border-brand-main-600 bg-brand-main-900">
                    <SelectItem value="all">All categories</SelectItem>
                    {(
                      [
                        'Completion',
                        'Tooling',
                        'Safety',
                        'Looping',
                        'Sandbox',
                      ] as ScoreCategory[]
                    ).map((option) => (
                      <SelectItem key={option} value={option}>
                        {option}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select
                  value={status}
                  onValueChange={onStatusChange}
                >
                  <SelectTrigger className="h-9 w-[150px] border-brand-main-600 bg-brand-main-800/50 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="border-brand-main-600 bg-brand-main-900">
                    <SelectItem value="all">All statuses</SelectItem>
                    <SelectItem value="healthy">Healthy</SelectItem>
                    <SelectItem value="watch">Watch</SelectItem>
                    <SelectItem value="critical">Critical</SelectItem>
                    <SelectItem value="unknown">Unknown</SelectItem>
                  </SelectContent>
                </Select>
                <Select
                  value={sortBy}
                  onValueChange={onSortChange}
                >
                  <SelectTrigger className="h-9 w-[180px] border-brand-main-600 bg-brand-main-800/50 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="border-brand-main-600 bg-brand-main-900">
                    {Object.entries(SCORE_SORT_OPTIONS).map(
                      ([value, label]) => (
                        <SelectItem key={value} value={value}>
                          {label}
                        </SelectItem>
                      ),
                    )}
                  </SelectContent>
                </Select>
              </div>
            </div>
          </CardHeader>
          <CardContent className="pt-0! px-0!">
            <div className="mb-2 px-3 flex flex-wrap items-center gap-2 text-xs text-white/45 light:text-black/45">
              <span>{filteredScores.length} scores shown</span>
            </div>
          </CardContent>
        </Card>
        <div className='px-3 mb-2 text-xs text-white/45 light:text-black/45'>
          Coverage baseline {formatNumber(totalEvaluations)} evaluations
        </div>
        <ScoreExplorerTable
          scores={filteredScores}
          onSelect={onScoreSelect}
        />
      </div >

      <Sheet
        open={selectedScore !== null}
        onOpenChange={(open) => { if (!open) onScoreSelect(null) }}
      >
        <SheetContent
          side="right"
          className="w-full border-brand-main-600 sm:max-w-2xl"
        >
          {selectedScore && (
            <>
              <SheetHeader className="flex-row justify-between w-full items-center gap-1 py-3">
                <div className="flex w-full items-center justify-between gap-3">
                  <div>
                    <SheetTitle className='flex items-center gap-2 text-sm font-medium text-white light:text-brand-main-50'>
                      {selectedScore.meta.label}
                    </SheetTitle>
                    <SheetDescription>
                      {selectedScore.scoreName}
                    </SheetDescription>
                  </div>
                </div>
                <div className='flex items-center gap-2 mr-2'>
                  <Badge className={getStatusBadgeClass(selectedScore.status)}>
                    {getStatusLabel(selectedScore.status)}
                  </Badge>
                </div>
              </SheetHeader>
              <SheetBody className="space-y-2 py-2">
                <Card className='border-none'>
                  <CardContent className="grid gap-3 md:grid-cols-2 p-0 border-none">
                    <DetailStat
                      label="Current"
                      value={formatValue(
                        selectedScore.value,
                        selectedScore.meta.format,
                      )}
                    />
                    <DetailStat
                      label="Delta vs previous"
                      value={formatDelta(
                        selectedScore.delta,
                        selectedScore.meta.format,
                      )}
                    />
                    <DetailStat
                      label="Coverage"
                      value={
                        selectedScore.coverage !== null
                          ? formatPercent(selectedScore.coverage)
                          : '--'
                      }
                    />
                    <DetailStat
                      label="Sample size"
                      value={formatNumber(selectedScore.count)}
                    />
                    <DetailStat
                      label="Median (P50)"
                      value={
                        selectedScore.score
                          ? formatValue(
                            selectedScore.score.p50 ?? null,
                            selectedScore.meta.format,
                          )
                          : '--'
                      }
                    />
                    <DetailStat
                      label="Tail (P95)"
                      value={
                        selectedScore.score
                          ? formatValue(
                            selectedScore.score.p95 ?? null,
                            selectedScore.meta.format,
                          )
                          : '--'
                      }
                    />
                  </CardContent>
                </Card>

                <Card className="border-brand-main-600 bg-brand-main-900/50">
                  <CardHeader className="px-3! pb-2!">
                    <CardTitle className="text-sm font-medium text-white light:text-brand-main-50">
                      Why It Matters
                    </CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-2 -mt-8 text-sm text-white/70 light:text-black/70">
                    <p>{selectedScore.meta.description}</p>
                    <p className="text-xs text-white/45 light:text-black/45">
                      This view is outcome-focused: it tells you whether the
                      agent is making progress, using tools effectively, and
                      staying reliable from the user perspective.
                    </p>
                    <div className="flex flex-wrap gap-2">
                      <TooltipProvider>
                        <UiTooltip content={selectedAgent ? 'Open agent sessions' : 'Open sessions'}>
                          <Button variant="ghost" className="h-8 w-8 p-0" onClick={onOpenSessions}>
                            <MessageSquare className="h-4 w-4" />
                          </Button>
                        </UiTooltip>
                      </TooltipProvider>
                      <TooltipProvider>
                        <UiTooltip content="Open traces in this range">
                          <Button variant="outline" className="h-8 w-8 p-0" onClick={onOpenTraces}>
                            <Activity className="h-4 w-4" />
                          </Button>
                        </UiTooltip>
                      </TooltipProvider>
                    </div>
                  </CardContent>
                </Card>

                <ScoreTrendChart
                  filters={filters}
                  previousFilters={previousFilters}
                  scoreName={selectedScore.scoreName}
                  label={selectedScore.meta.label}
                  format={selectedScore.meta.format}
                  timeRange={timeRange}
                  description={selectedScore.meta.description}
                  aggregation={
                    selectedScore.meta.format === 'boolean'
                      ? 'rate_true'
                      : 'avg'
                  }
                  coverage={selectedScore.coverage}
                  latestValue={selectedScore.value}
                  latestDelta={selectedScore.delta}
                  status={selectedScore.status}
                />
              </SheetBody>
            </>
          )}
        </SheetContent>
      </Sheet>
    </>
  )
}

function ScoreExplorerTable({
  scores,
  onSelect,
}: {
  scores: DerivedScore[]
  onSelect: (scoreName: string) => void
}) {
  const columns: ColumnConfig<DerivedScore>[] = [
    {
      id: 'name',
      header: 'Score',
      width: 220,
      minWidth: 160,
      render: (entry: DerivedScore) => (
        <div className="flex flex-col">
          <span className="truncate text-xs font-medium text-brand-secondary-100">{entry.meta.label}</span>
          <span className="truncate text-xs text-brand-main-100">{entry.scoreName}</span>
        </div>
      ),
    },
    {
      id: 'category',
      header: 'Category',
      width: 100,
      minWidth: 80,
      render: (entry: DerivedScore) => (
        <Badge variant="secondary">{entry.meta.category}</Badge>
      ),
    },
    {
      id: 'current',
      header: 'Current',
      width: 90,
      minWidth: 70,
      render: (entry: DerivedScore) => (
        <span className={cn('text-xs font-mono', getMetricClassName(entry.status))}>
          {formatValue(entry.value, entry.meta.format)}
        </span>
      ),
    },
    {
      id: 'delta',
      header: 'Delta',
      width: 90,
      minWidth: 70,
      render: (entry: DerivedScore) => (
        <span className="text-xs font-mono text-white/70 light:text-black/70">
          {formatDelta(entry.delta, entry.meta.format)}
        </span>
      ),
    },
    {
      id: 'coverage',
      header: 'Coverage',
      width: 80,
      minWidth: 70,
      render: (entry: DerivedScore) => (
        <span className="text-xs font-mono text-white/70 light:text-black/70">
          {entry.coverage !== null ? formatPercent(entry.coverage) : '--'}
        </span>
      ),
    },
    {
      id: 'count',
      header: 'Count',
      width: 70,
      minWidth: 60,
      render: (entry: DerivedScore) => (
        <span className="text-xs font-mono text-white/70 light:text-black/70">
          {formatNumber(entry.count)}
        </span>
      ),
    },
    {
      id: 'p50',
      header: 'P50',
      width: 70,
      minWidth: 60,
      render: (entry: DerivedScore) => (
        <span className="text-xs font-mono text-white/70 light:text-black/70">
          {entry.score ? formatValue(entry.score.p50 ?? null, entry.meta.format) : '--'}
        </span>
      ),
    },
    {
      id: 'p95',
      header: 'P95',
      width: 70,
      minWidth: 60,
      render: (entry: DerivedScore) => (
        <span className="text-xs font-mono text-white/70 light:text-black/70">
          {entry.score ? formatValue(entry.score.p95 ?? null, entry.meta.format) : '--'}
        </span>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      width: 100,
      minWidth: 80,
      render: (entry: DerivedScore) => (
        <div className="inline-flex items-center gap-1.5">
          <Badge className={getStatusBadgeClass(entry.status)}>
            {getStatusLabel(entry.status)}
          </Badge>
          <ChevronRight className="h-3.5 w-3.5 text-white/25 light:text-black/25" />
        </div>
      ),
    },
  ]

  return (
    <ResponsiveTable
      columns={columns}
      data={scores}
      enableResizing={true}
      minTableWidth="100%"
      onRowClick={(entry) => onSelect(entry.scoreName)}
      rowKey={(entry) => entry.scoreName}
      emptyMessage={
        <div className="flex flex-col items-center justify-center py-12">
          <div className="relative mb-6">
            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
              <Iconify.Icon icon="heroicons:presentation-chart-line" className="size-8 text-brand-secondary-400" />
            </div>
          </div>
          <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No matching scores</h3>
          <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
            Try adjusting the filters to see more scores.
          </p>
        </div>
      }
    />
  )
}

function ChangeList({
  title,
  icon,
  items,
  emptyText,
}: {
  title: string
  icon: ReactNode
  items: DerivedScore[]
  emptyText: string
}) {
  return (
    <div>
      <div className="mb-2 flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-white/60 light:text-black/60">
        {icon}
        {title}
      </div>
      <div className="space-y-2">
        {items.length > 0 ? (
          items.map((entry) => (
            <div
              key={entry.scoreName}
              className="flex items-center justify-between gap-3 rounded border border-brand-main-700 bg-brand-main-800/30 px-3 py-2"
            >
              <div className="min-w-0">
                <div className="truncate text-sm text-white light:text-brand-main-50">
                  {entry.meta.label}
                </div>
                <div className="text-[11px] text-white/35 light:text-black/35">
                  {entry.meta.category}
                </div>
              </div>
              <div className="text-right">
                <div
                  className={cn(
                    'text-xs font-mono',
                    getMetricClassName(entry.status),
                  )}
                >
                  {formatDelta(entry.delta, entry.meta.format)}
                </div>
                <div className="text-[11px] text-white/40 light:text-black/40">
                  {formatValue(entry.value, entry.meta.format)}
                </div>
              </div>
            </div>
          ))
        ) : (
          <div className="flex items-center gap-3 rounded border border-dashed border-brand-main-700 px-3 py-4">
            <div className="relative shrink-0">
              <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-md" />
              <div className="relative rounded-lg border border-brand-main-600 bg-brand-main-800/80 p-1.5">
                <Iconify.Icon icon="heroicons:check-circle" className="size-4 text-brand-secondary-400" />
              </div>
            </div>
            <span className="text-xs text-white/50 light:text-black/50">{emptyText}</span>
          </div>
        )}
      </div>
    </div>
  )
}

function MetricCard({
  icon,
  label,
  value,
  previousValue,
  isLoading,
}: {
  icon: ReactNode
  label: string
  value: number
  previousValue: number
  isLoading?: boolean
}) {
  const delta = value - previousValue
  const status = statusFromHealth(value)

  return (
    <Card className="rounded border-brand-main-600 bg-brand-main-900/50">
      <CardContent>
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            <div className="p-2 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
              <div className="text-brand-secondary-300">{icon}</div>
            </div>
            <div className="min-w-0">
              <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide truncate">
                {label}
              </div>
              <div className="text-[10px] text-white/40 light:text-black/40">
                {formatSignedPoints(delta)}
              </div>
            </div>
          </div>
          {isLoading ? (
            <div className="h-4 w-10 bg-brand-main-800/60 rounded animate-pulse" />
          ) : (
            <div
              className={cn(
                'text-sm font-semibold',
                getMetricClassName(status),
              )}
            >
              {formatPercent(value)}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function StatCard({
  icon,
  label,
  value,
}: {
  icon: ReactNode
  label: string
  value: string
}) {
  return (
    <Card className="rounded border-brand-main-600 bg-brand-main-900/50">
      <CardContent>
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            <div className="p-2 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30 text-brand-secondary-300">
              {icon}
            </div>
            <div className="min-w-0">
              <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide truncate">
                {label}
              </div>
            </div>
          </div>
          <div className="text-sm font-semibold text-white light:text-brand-main-50">{value}</div>
        </div>
      </CardContent>
    </Card>
  )
}

function DetailStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-brand-main-500 bg-brand-main-800/15 px-3 py-2">
      <div className="text-[11px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div className="mt-1 text-sm font-medium text-white light:text-brand-main-50">{value}</div>
    </div>
  )
}
