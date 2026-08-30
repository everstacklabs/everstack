import { useEffect, useMemo, useRef, useState } from 'react'
import { useAgent } from '@/hooks/deployments/use-agents'
import {
  useAgentTriggers,
  useDeleteTrigger,
  useUpdateTrigger,
  useTestTrigger,
} from '@/hooks/deployments/use-agent-triggers'
import {
  useDeleteCron,
  useRunCronNow,
  useSandboxCrons,
  useSandboxTriggers,
  useUpdateCron,
} from '@/hooks/deployments/use-sandbox'
import { CreateTriggerDialog } from './create-trigger-dialog'
import { SandboxCronDetailSheet } from './sandbox-cron-detail-sheet'
import { TriggerDetailSheet } from './trigger-detail-sheet'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { Loader2, Pause, Play, PlayCircle, Trash2 } from 'lucide-react'
import type { AgentTrigger } from '@/server/agent-triggers'
import type { SandboxCron } from '@/server/sandbox'

const { Button, Checkbox, Tabs, TabsList, TabsTrigger, Tooltip, TooltipProvider } = ui

const TAB_CLASS =
  'relative flex items-center gap-1.5 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 text-xs light:hover:text-brand-main-50'

const TRIGGER_TYPE_ICONS: Record<string, string> = {
  cron: 'heroicons:clock',
  webhook: 'heroicons:globe-alt',
  event: 'heroicons:bolt',
}

const TRIGGER_TYPE_LABELS: Record<string, string> = {
  cron: 'Cron',
  webhook: 'Webhook',
  event: 'Event',
}

const CIRCUIT_STATE_COLORS: Record<string, string> = {
  closed: 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25',
  open: 'bg-red-500/15 text-red-400 border border-red-500/25',
  half_open: 'bg-amber-500/15 text-amber-300 border border-amber-500/25',
}

function formatDate(ts?: string | null): string {
  if (!ts) return '--'
  return new Date(ts).toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function formatRelativeTime(ts: string): string {
  const diff = new Date(ts).getTime() - Date.now()
  if (diff < 0) return 'overdue'
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'in < 1m'
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `in ${days}d ${hours % 24}h`
}

function getPersistentAutomationSessionId(
  agentId: string,
  lifecycleMode?: number,
): string | undefined {
  return lifecycleMode === 2 ? `trp-${agentId}` : undefined
}


export function AgentTriggersTab({ agentId }: { agentId: string }) {
  const { data: agent } = useAgent(agentId)
  const { data: triggers = [], isLoading: triggersLoading } =
    useAgentTriggers(agentId)
  const trooperSessionId = getPersistentAutomationSessionId(
    agentId,
    agent?.lifecycleMode,
  )
  const {
    data: cronData,
    isLoading: cronsLoading,
    error: cronsError,
  } = useSandboxCrons(trooperSessionId)
  const crons = cronData?.crons ?? []

  // Fetch recent execution history for this agent's crons
  const activeSandboxId = crons[0]?.sandboxId
  const { data: cronTriggerData } = useSandboxTriggers(activeSandboxId, {
    triggerType: 'cron',
    limit: 20,
  })

  const cronNameById = useMemo(
    () => new Map(crons.map((c) => [String(c.id), c.name])),
    [crons],
  )

  const cronScheduleById = useMemo(
    () => new Map(crons.map((c) => [String(c.id), c.schedule])),
    [crons],
  )

  const allRuns = useMemo(() => {
    return (cronTriggerData?.triggers ?? [])
      .filter((t) => cronNameById.has(String(t.triggerId)))
      .sort(
        (a, b) =>
          new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
      )
      .map((t) => ({
        ...t,
        cronName: cronNameById.get(String(t.triggerId)) || 'Scheduled job',
        cronSchedule: cronScheduleById.get(String(t.triggerId)) || '',
      }))
  }, [cronNameById, cronScheduleById, cronTriggerData?.triggers])

  const failedRuns = useMemo(
    () => allRuns.filter((r) => r.status === 'failed'),
    [allRuns],
  )

  const upcomingCrons = useMemo(
    () =>
      crons
        .filter((c) => c.enabled && c.nextRunAt)
        .sort(
          (a, b) =>
            new Date(a.nextRunAt!).getTime() - new Date(b.nextRunAt!).getTime(),
        )
        .slice(0, 5),
    [crons],
  )

  const [createOpen, setCreateOpen] = useState(false)
  const [viewFilter, setViewFilter] = useState<
    'all' | 'triggers' | 'schedules' | 'runs'
  >('all')
  const [search, setSearch] = useState('')

  // Auto-switch to Runs tab when failures are first detected
  const hasAutoSwitched = useRef(false)
  useEffect(() => {
    if (failedRuns.length > 0 && !hasAutoSwitched.current) {
      hasAutoSwitched.current = true
      setViewFilter('runs')
    }
  }, [failedRuns.length])
  const [selectedTriggerIds, setSelectedTriggerIds] = useState<string[]>([])
  const [selectedCronIds, setSelectedCronIds] = useState<string[]>([])
  const [selectedTrigger, setSelectedTrigger] = useState<AgentTrigger | null>(
    null,
  )
  const [selectedCron, setSelectedCron] = useState<SandboxCron | null>(null)
  const [runningCronId, setRunningCronId] = useState<string | null>(null)
  const deleteTrigger = useDeleteTrigger()
  const updateTrigger = useUpdateTrigger()
  const testTrigger = useTestTrigger()
  const updateCron = useUpdateCron()
  const deleteCron = useDeleteCron()
  const runCronNow = useRunCronNow()

  const counts = useMemo(
    () => ({
      triggers: triggers.length,
      activeTriggers: triggers.filter((t) => t.enabled).length,
      crons: crons.length,
      activeCrons: crons.filter((c) => c.enabled).length,
    }),
    [crons, triggers],
  )

  const query = search.trim().toLowerCase()
  const filteredTriggers = useMemo(
    () =>
      triggers.filter((trigger) => {
        if (!query) return true
        return [
          trigger.name,
          trigger.triggerType,
          trigger.cronExpression,
          trigger.eventType,
          trigger.webhookPath,
        ]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(query))
      }),
    [query, triggers],
  )

  const filteredCrons = useMemo(
    () =>
      crons.filter((cron) => {
        if (!query) return true
        return [cron.name, cron.schedule, cron.command, cron.lastError]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(query))
      }),
    [crons, query],
  )

  if (triggersLoading) {
    return (
      <div className="h-full flex items-center justify-center text-sm text-white/50 light:text-black/50">
        <Loader loaderText="Loading automations..." />
      </div>
    )
  }

  const handleCronToggle = (cron: SandboxCron) => {
    updateCron.mutate(
      { cronId: String(cron.id), enabled: !cron.enabled },
      {
        onSuccess: () =>
          toast.success(
            cron.enabled ? 'Schedule disabled' : 'Schedule enabled',
          ),
        onError: (err) => toast.error(err.message),
      },
    )
  }

  const handleCronDelete = (cron: SandboxCron) => {
    deleteCron.mutate(String(cron.id), {
      onSuccess: () => toast.success(`Schedule "${cron.name}" deleted`),
      onError: (err) => toast.error(err.message),
    })
  }

  const handleCronSave = (
    cron: SandboxCron,
    values: {
      name: string
      schedule: string
      command: string
      workDir: string
      timeoutSeconds: number
      autoRecreate: boolean
    },
  ) => {
    updateCron.mutate(
      {
        cronId: String(cron.id),
        name: values.name,
        schedule: values.schedule,
        command: values.command,
        workDir: values.workDir,
        timeoutSeconds: values.timeoutSeconds,
        autoRecreate: values.autoRecreate,
      },
      {
        onSuccess: ({ cron: updatedCron }) => {
          toast.success(`Schedule "${updatedCron.name}" updated`)
          setSelectedCron(updatedCron)
        },
        onError: (err) => toast.error(err.message),
      },
    )
  }

  const cronColumns: ColumnConfig<SandboxCron>[] = [
    {
      id: 'select',
      header: '',
      width: 36,
      minWidth: 36,
      maxWidth: 36,
      resizable: false,
      className: '!px-2',
      render: (cron) => (
        <div data-row-actions>
          <Checkbox
            checked={selectedCronIds.includes(String(cron.id))}
            onCheckedChange={(checked) =>
              toggleCronSelection(String(cron.id), checked === true)
            }
            onClick={(e) => e.stopPropagation()}
            className="border-brand-secondary-500/40"
          />
        </div>
      ),
    },
    {
      id: 'name',
      header: 'Name',
      width: 200,
      minWidth: 140,
      render: (cron) => (
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-white light:text-brand-main-50">
            {cron.name}
          </div>
          <div className="mt-0.5 text-[11px] text-white/40 light:text-black/40">
            Updated {formatDate(cron.updatedAt)}
          </div>
        </div>
      ),
    },
    {
      id: 'schedule',
      header: 'Schedule',
      width: 110,
      minWidth: 90,
      render: (cron) => (
        <span className="truncate font-mono text-xs text-white/60 light:text-black/60">
          {cron.schedule}
        </span>
      ),
    },
    {
      id: 'command',
      header: 'Command',
      width: 280,
      minWidth: 120,
      render: (cron) => (
        <span
          className="block truncate font-mono text-xs text-white/50 light:text-black/50"
          title={cron.command}
        >
          {cron.command}
        </span>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      width: 80,
      minWidth: 70,
      maxWidth: 90,
      render: (cron) => (
        <span
          className={`inline-flex rounded px-2 py-0.5 text-[10px] font-medium ${cron.enabled ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25' : 'bg-brand-main-500/30 text-brand-main-200'}`}
        >
          {cron.enabled ? 'Enabled' : 'Disabled'}
        </span>
      ),
    },
    {
      id: 'runs',
      header: 'Runs',
      width: 70,
      minWidth: 60,
      maxWidth: 90,
      render: (cron) => (
        <span className="text-xs text-white/55 light:text-black/55">
          {cron.runCount}
          {cron.errorCount > 0 && (
            <span className="ml-1 text-red-400">({cron.errorCount} err)</span>
          )}
        </span>
      ),
    },
    {
      id: 'nextRun',
      header: 'Next Run',
      width: 120,
      minWidth: 100,
      maxWidth: 140,
      render: (cron) => (
        <span className="text-xs text-white/50 light:text-black/50">
          {formatDate(cron.nextRunAt)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      width: 60,
      maxWidth: 80,
      resizable: false,
      render: (cron) => (
        <div data-row-actions className="flex items-center justify-end gap-0.5 pr-3">
          <TooltipProvider>
            <Tooltip content={cron.enabled ? 'Disable' : 'Enable'}>
              <button
                type="button"
                className="rounded p-1.5 text-white/40 transition-colors hover:bg-brand-main-800/60 hover:text-white/70 disabled:opacity-50 light:text-black/40 light:hover:text-black/70"
                onClick={(e) => {
                  e.stopPropagation()
                  handleCronToggle(cron)
                }}
                disabled={updateCron.isPending}
              >
                {cron.enabled ? (
                  <Pause className="size-3.5" />
                ) : (
                  <Play className="size-3.5" />
                )}
              </button>
            </Tooltip>
            <Tooltip
              content={
                runningCronId === String(cron.id) ?'Running...' : 'Run now'
              }
            >
              <button
                type="button"
                className="rounded p-1.5 text-brand-secondary-400 transition-colors hover:bg-brand-secondary-500/10 hover:text-brand-secondary-300 disabled:opacity-50"
                onClick={(e) => {
                  e.stopPropagation()
                  const id = String(cron.id)
                  setRunningCronId(id)
                  runCronNow.mutate(id, {
                    onSuccess: () => {
                      toast.success(`Schedule "${cron.name}" started`)
                      setRunningCronId(null)
                    },
                    onError: (err) => {
                      toast.error(err.message)
                      setRunningCronId(null)
                    },
                  })
                }}
                disabled={runningCronId === String(cron.id)}
              >
                {runningCronId === String(cron.id) ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <PlayCircle className="size-3.5" />
                )}
              </button>
            </Tooltip>
            <Tooltip content="Delete">
              <button
                type="button"
                className="rounded p-1.5 text-red-400/60 transition-colors hover:bg-red-500/10 hover:text-red-300 disabled:opacity-50"
                onClick={(e) => {
                  e.stopPropagation()
                  handleCronDelete(cron)
                }}
                disabled={deleteCron.isPending}
              >
                <Trash2 className="size-3.5" />
              </button>
            </Tooltip>
          </TooltipProvider>
        </div>
      ),
    },
  ]

  const showTriggers = viewFilter === 'all' || viewFilter === 'triggers'
  const showSchedules = viewFilter === 'all' || viewFilter === 'schedules'
  const showRuns = viewFilter === 'runs'

  const toggleTriggerSelection = (id: string, checked: boolean) => {
    setSelectedTriggerIds((current) =>
      checked
        ? [...current.filter((item) => item !== id), id]
        : current.filter((item) => item !== id),
    )
  }

  const toggleCronSelection = (id: string, checked: boolean) => {
    setSelectedCronIds((current) =>
      checked
        ? [...current.filter((item) => item !== id), id]
        : current.filter((item) => item !== id),
    )
  }

  const bulkUpdateTriggers = (enabled: boolean) => {
    for (const id of selectedTriggerIds) {
      updateTrigger.mutate({ id, enabled })
    }
    toast.success(
      enabled ? 'Selected triggers enabled' : 'Selected triggers disabled',
    )
    setSelectedTriggerIds([])
  }

  const bulkDeleteTriggers = () => {
    for (const id of selectedTriggerIds) {
      deleteTrigger.mutate(id)
    }
    toast.success('Selected triggers deleted')
    setSelectedTriggerIds([])
  }

  const bulkUpdateCrons = (enabled: boolean) => {
    for (const id of selectedCronIds) {
      updateCron.mutate({ cronId: id, enabled })
    }
    toast.success(
      enabled ? 'Selected schedules enabled' : 'Selected schedules disabled',
    )
    setSelectedCronIds([])
  }

  const bulkDeleteCrons = () => {
    for (const id of selectedCronIds) {
      deleteCron.mutate(id)
    }
    toast.success('Selected schedules deleted')
    setSelectedCronIds([])
  }

  return (
    <div className="flex flex-col h-full">
      {/* ── Toolbar ─────────────────────────────────────────────── */}
      <div className="shrink-0 flex flex-wrap items-center justify-between gap-3 border-b border-brand-main-700/60 px-4 py-2.5">
        <div className="flex items-center gap-3">
          {/* Stat badges */}
          <StatBadge
            label="Triggers"
            value={counts.triggers}
            active={counts.activeTriggers}
          />
          <StatBadge
            label="Schedules"
            value={counts.crons}
            active={counts.activeCrons}
          />

          {/* Divider */}
          <div className="h-5 w-px bg-brand-main-700/60" />

          <Tabs
            value={viewFilter}
            onValueChange={(v) =>
              setViewFilter(
                v as 'all' | 'triggers' | 'schedules' | 'runs',
              )
            }
          >
            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
              <TabsTrigger value="all" className={TAB_CLASS}>
                All
              </TabsTrigger>
              <TabsTrigger value="triggers" className={TAB_CLASS}>
                Triggers
              </TabsTrigger>
              <TabsTrigger value="schedules" className={TAB_CLASS}>
                Schedules
              </TabsTrigger>
              <TabsTrigger value="runs" className={TAB_CLASS}>
                Runs
                {failedRuns.length > 0 && (
                  <span className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-red-400/20 px-1 text-[10px] font-medium text-red-300">
                    {failedRuns.length}
                  </span>
                )}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>

        <div className="flex items-center gap-2">
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search..."
            className="h-8 w-48 rounded border border-brand-main-600 bg-brand-main-800/50 px-2.5 text-xs text-white placeholder:text-white/30 focus:border-brand-secondary-500/40 focus:outline-none light:text-brand-main-50 light:placeholder:text-black/30"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={() => setCreateOpen(true)}
            className="h-8 text-xs border-brand-main-600 hover:border-brand-secondary-500/40"
          >
            + New Trigger
          </Button>
        </div>
      </div>

      {/* ── Content ─────────────────────────────────────────────── */}
      <div className="flex-1 min-h-0 overflow-y-auto">
        {showTriggers && (
          <section>
            <SectionHeader
              icon="heroicons:bolt"
              title={`Agent Triggers (${filteredTriggers.length})`}
            />

            {filteredTriggers.length === 0 ? (
              <EmptyAutomationState
                icon="heroicons:bolt"
                title={
                  triggers.length === 0
                    ?'No triggers configured'
                    : 'No matching triggers'
                }
                description={
                  triggers.length === 0
                    ? 'Create a cron schedule, webhook, or event-driven trigger to make this agent proactive.'
                    : 'Try a different search or filter.'
                }
              />
            ) : (
              <div>
                <BulkActionBar
                  count={selectedTriggerIds.length}
                  noun="triggers"
                  onEnable={() => bulkUpdateTriggers(true)}
                  onDisable={() => bulkUpdateTriggers(false)}
                  onDelete={bulkDeleteTriggers}
                />
                {filteredTriggers.map((t, i) => (
                  <div
                    key={t.id}
                    className={`flex cursor-pointer items-center justify-between px-4 py-3 transition-colors hover:bg-brand-main-800/40 ${i !== filteredTriggers.length - 1 ? 'border-b border-brand-main-700/40' : ''}`}
                    onClick={() => setSelectedTrigger(t)}
                  >
                    <div className="flex min-w-0 items-center gap-3">
                      <Checkbox
                        checked={selectedTriggerIds.includes(t.id)}
                        onCheckedChange={(checked) =>
                          toggleTriggerSelection(t.id, checked === true)
                        }
                        onClick={(e) => e.stopPropagation()}
                        className="border-brand-secondary-500/40"
                      />
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded border border-brand-secondary-500/30 bg-brand-secondary-600/15">
                        <Iconify.Icon
                          icon={
                            TRIGGER_TYPE_ICONS[t.triggerType] ??
                            'heroicons:cog-6-tooth'
                          }
                          className="size-3.5 text-brand-secondary-300"
                        />
                      </span>
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium text-white light:text-brand-main-50">
                          {t.name}
                        </div>
                        <div className="flex items-center gap-2 text-[11px] text-white/40 light:text-black/40">
                          <span className="uppercase">
                            {TRIGGER_TYPE_LABELS[t.triggerType] ?? t.triggerType}
                          </span>
                          {t.triggerType === 'cron' && t.cronExpression && (
                            <span className="font-mono">
                              {t.cronExpression}
                            </span>
                          )}
                          {t.triggerType === 'webhook' && t.webhookPath && (
                            <span className="font-mono">/{t.webhookPath}</span>
                          )}
                          {t.triggerType === 'event' && t.eventType && (
                            <span>{t.eventType}</span>
                          )}
                        </div>
                      </div>
                    </div>

                    <div className="flex shrink-0 items-center gap-2">
                      {t.circuitState && t.circuitState !== 'closed' && (
                        <span
                          className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${CIRCUIT_STATE_COLORS[t.circuitState] ?? ''}`}
                        >
                          {t.circuitState}
                        </span>
                      )}
                      <span
                        className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${t.enabled ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25' : 'bg-brand-main-500/30 text-brand-main-200'}`}
                      >
                        {t.enabled ? 'Active' : 'Disabled'}
                      </span>
                      <button
                        type="button"
                        className="rounded px-2 py-1 text-xs text-white/50 transition-colors hover:bg-brand-main-800/60 hover:text-white/70 light:text-black/50 light:hover:text-black/70"
                        onClick={(e) => {
                          e.stopPropagation()
                          updateTrigger.mutate({
                            id: t.id,
                            enabled: !t.enabled,
                          })
                        }}
                      >
                        {t.enabled ?'Disable' : 'Enable'}
                      </button>
                      <button
                        type="button"
                        className="rounded px-2 py-1 text-xs text-brand-secondary-300 transition-colors hover:bg-brand-secondary-500/10 hover:text-brand-secondary-200"
                        onClick={(e) => {
                          e.stopPropagation()
                          testTrigger.mutate(t.id)
                        }}
                      >
                        Test
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>
        )}

        {showSchedules && (
          <section>
            <SectionHeader
              icon="heroicons:clock"
              title={`Sandbox Schedules (${filteredCrons.length})`}
            />

            {!trooperSessionId ? (
              <EmptyAutomationState
                icon="heroicons:clock"
                title="Sandbox schedules need a persistent agent"
                description="This section appears for persistent agents, where cron jobs can live inside the long-running sandbox."
              />
            ) : cronsLoading ? (
              <div className="py-8 text-center text-sm text-white/50 light:text-black/50">
                Loading schedules...
              </div>
            ) : cronsError ? (
              <div className="mx-4 my-3 rounded border border-red-500/20 bg-red-500/5 px-4 py-3 text-sm text-red-300">
                Failed to load sandbox schedules: {cronsError.message}
              </div>
            ) : filteredCrons.length === 0 ? (
              <EmptyAutomationState
                icon="heroicons:clock"
                title={
                  crons.length === 0
                    ? 'No sandbox schedules yet'
                    : 'No matching schedules'
                }
                description={
                  crons.length === 0
                    ? 'Agent-created cron jobs will show up here once the sandbox creates them.'
                    : 'Try a different search or filter.'
                }
              />
            ) : (
              <div>
                <BulkActionBar
                  count={selectedCronIds.length}
                  noun="schedules"
                  onEnable={() => bulkUpdateCrons(true)}
                  onDisable={() => bulkUpdateCrons(false)}
                  onDelete={bulkDeleteCrons}
                />
                <ResponsiveTable
                  columns={cronColumns}
                  data={filteredCrons}
                  minTableWidth="100%"
                  rowKey={(cron) => cron.id}
                  onRowClick={(cron) => setSelectedCron(cron)}
                />
              </div>
            )}
          </section>
        )}

        {/* ── Runs view ─────────────────────────────────────── */}
        {showRuns && (
          <>
            {/* Failures — always pinned at top */}
            {failedRuns.length > 0 && (
              <section>
                <div className="flex items-center gap-2.5 border-b border-red-400/10 bg-red-400/[0.03] px-4 py-2">
                  <Iconify.Icon
                    icon="heroicons:exclamation-triangle"
                    className="size-3.5 text-red-400/70"
                  />
                  <span className="text-xs font-medium text-red-300/80">
                    Failures ({failedRuns.length})
                  </span>
                </div>
                {failedRuns.map((run, i) => (
                  <div
                    key={run.id}
                    className={`px-4 py-2.5 ${i !== failedRuns.length - 1 ? 'border-b border-brand-main-700/40' : 'border-b border-brand-main-700/60'}`}
                  >
                    <div className="flex items-center justify-between gap-4">
                      <div className="min-w-0 flex-1">
                        <div className="text-sm text-white/80 light:text-black/80">
                          {run.cronName}
                        </div>
                        <div className="mt-0.5 flex items-center gap-2 text-[11px] text-white/35 light:text-black/35">
                          <span>{new Date(run.createdAt).toLocaleString()}</span>
                          {run.cronSchedule && (
                            <span className="font-mono text-white/25 light:text-black/25">{run.cronSchedule}</span>
                          )}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2 text-xs">
                        <span className="rounded border border-red-500/25 bg-red-500/15 px-2 py-0.5 text-[10px] font-medium text-red-300">
                          failed
                        </span>
                        <span className="text-white/30 light:text-black/30">
                          {run.durationMs}ms
                        </span>
                      </div>
                    </div>
                    {run.error && (
                      <div className="mt-1.5 rounded border border-red-400/10 bg-red-400/[0.03] px-2 py-1 text-xs text-red-200/70 whitespace-pre-wrap break-all">
                        {run.error}
                      </div>
                    )}
                  </div>
                ))}
              </section>
            )}

            {/* Upcoming schedules */}
            {upcomingCrons.length > 0 && (
              <section>
                <SectionHeader
                  icon="heroicons:calendar"
                  title={`Upcoming (${upcomingCrons.length})`}
                />
                {upcomingCrons.map((cron, i) => (
                  <div
                    key={cron.id}
                    className={`flex items-center justify-between gap-4 px-4 py-2.5 ${i !== upcomingCrons.length - 1 ? 'border-b border-brand-main-700/40' : 'border-b border-brand-main-700/60'}`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="text-sm text-white/80 light:text-black/80">{cron.name}</div>
                      <div className="mt-0.5 font-mono text-[11px] text-white/35 light:text-black/35">
                        {cron.schedule}
                      </div>
                    </div>
                    <span className="shrink-0 text-xs text-white/50 light:text-black/50">
                      {formatRelativeTime(cron.nextRunAt!)}
                    </span>
                  </div>
                ))}
              </section>
            )}

            {/* All recent runs */}
            <section>
              <SectionHeader
                icon="heroicons:clock"
                title={`Recent (${allRuns.length})`}
              />
              {allRuns.length === 0 ? (
                <EmptyAutomationState
                  icon="heroicons:clock"
                  title="No run history yet"
                  description="Execution history will appear here once your schedules start running."
                />
              ) : (
                allRuns.slice(0, 30).map((run, i) => (
                  <div
                    key={run.id}
                    className={`px-4 py-2.5 ${i !== Math.min(allRuns.length, 30) - 1 ? 'border-b border-brand-main-700/40' : ''}`}
                  >
                    <div className="flex items-center justify-between gap-4">
                      <div className="min-w-0 flex-1">
                        <div className="text-sm text-white/80 light:text-black/80">
                          {run.cronName}
                        </div>
                        <div className="mt-0.5 flex items-center gap-2 text-[11px] text-white/35 light:text-black/35">
                          <span>{new Date(run.createdAt).toLocaleString()}</span>
                          {run.cronSchedule && (
                            <span className="font-mono text-white/25 light:text-black/25">{run.cronSchedule}</span>
                          )}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2 text-xs">
                        <span
                          className={`rounded px-2 py-0.5 text-[10px] font-medium ${run.status === 'completed' ? 'border border-emerald-500/25 bg-emerald-500/15 text-emerald-300' : 'border border-red-400/15 bg-red-400/10 text-red-300/80'}`}
                        >
                          {run.status}
                        </span>
                        <span className="text-white/30 light:text-black/30">
                          {run.durationMs}ms
                        </span>
                      </div>
                    </div>
                    {run.error && (
                      <div className="mt-1.5 rounded border border-red-400/10 bg-red-400/[0.03] px-2 py-1 text-xs text-red-200/70 whitespace-pre-wrap break-all">
                        {run.error}
                      </div>
                    )}
                  </div>
                ))
              )}
            </section>
          </>
        )}
      </div>

      <CreateTriggerDialog
        agentId={agentId}
        open={createOpen}
        onOpenChange={setCreateOpen}
      />

      {selectedTrigger && (
        <TriggerDetailSheet
          trigger={selectedTrigger}
          open={!!selectedTrigger}
          onOpenChange={(open) => !open && setSelectedTrigger(null)}
          onDelete={() => {
            deleteTrigger.mutate(selectedTrigger.id)
            setSelectedTrigger(null)
          }}
        />
      )}

      {selectedCron && (
        <SandboxCronDetailSheet
          cron={selectedCron}
          open={!!selectedCron}
          onOpenChange={(open) => !open && setSelectedCron(null)}
          onToggle={() => handleCronToggle(selectedCron)}
          onSave={(values) => handleCronSave(selectedCron, values)}
          onDelete={() => {
            handleCronDelete(selectedCron)
            setSelectedCron(null)
          }}
          onRunNow={() =>
            runCronNow.mutate(String(selectedCron.id), {
              onSuccess: () =>
                toast.success(`Schedule "${selectedCron.name}" started`),
              onError: (err) => toast.error(err.message),
            })
          }
          isUpdating={updateCron.isPending}
          isDeleting={deleteCron.isPending}
          isRunning={runCronNow.isPending}
          isSaving={updateCron.isPending}
        />
      )}
    </div>
  )
}

// ── Sub-components ────────────────────────────────────────────────────

function StatBadge({
  label,
  value,
  active,
}: {
  label: string
  value: number
  active: number
}) {
  return (
    <div className="flex items-center gap-1.5 text-xs text-white/50 light:text-black/50">
      <span className="text-white/70 font-medium light:text-black/70">{value}</span>
      <span>{label}</span>
      {active > 0 && (
        <span className="text-emerald-400/70">({active} active)</span>
      )}
    </div>
  )
}

function SectionHeader({ icon, title }: { icon: string; title: string }) {
  return (
    <div className="flex items-center gap-2.5 border-b border-brand-main-700/40 bg-brand-main-900/30 px-4 py-2">
      <Iconify.Icon icon={icon} className="size-3.5 text-brand-secondary-400" />
      <span className="text-xs font-medium text-white/70 light:text-black/70">{title}</span>
    </div>
  )
}

function EmptyAutomationState({
  icon,
  title,
  description,
}: {
  icon: string
  title: string
  description: string
}) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
      <div className="relative mb-6">
        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
          <Iconify.Icon icon={icon} className="size-8 text-brand-secondary-400" />
        </div>
      </div>
      <h3 className="text-base font-medium text-white mb-2 light:text-brand-main-50">{title}</h3>
      <p className="text-sm text-white/50 max-w-sm leading-relaxed light:text-black/50">
        {description}
      </p>
    </div>
  )
}

function BulkActionBar({
  count,
  noun,
  onEnable,
  onDisable,
  onDelete,
}: {
  count: number
  noun: string
  onEnable: () => void
  onDisable: () => void
  onDelete: () => void
}) {
  if (count === 0) return null

  return (
    <div className="flex items-center gap-2 border-b border-brand-main-700/40 bg-brand-main-800/30 px-4 py-1.5 text-xs">
      <span className="text-white/45 light:text-black/45">{count} selected</span>
      <button
        type="button"
        onClick={onEnable}
        className="rounded px-2 py-0.5 text-white/60 transition-colors hover:bg-brand-main-700/40 hover:text-white light:text-black/60 light:hover:text-brand-main-50"
      >
        Enable {noun}
      </button>
      <button
        type="button"
        onClick={onDisable}
        className="rounded px-2 py-0.5 text-white/60 transition-colors hover:bg-brand-main-700/40 hover:text-white light:text-black/60 light:hover:text-brand-main-50"
      >
        Disable {noun}
      </button>
      <button
        type="button"
        onClick={onDelete}
        className="rounded px-2 py-0.5 text-red-300 transition-colors hover:bg-red-500/10 hover:text-red-200"
      >
        Delete {noun}
      </button>
    </div>
  )
}
