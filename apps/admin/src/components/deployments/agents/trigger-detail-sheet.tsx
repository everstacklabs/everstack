import {
  useTriggerExecutions,
  useTestTrigger,
  useUpdateTrigger,
} from '@/hooks/deployments/use-agent-triggers'
import { ui } from '@everstack/ui'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { Iconify } from '@everstack/ui/icons'
import type { AgentTrigger } from '@/server/agent-triggers'

const {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetBody,
  Button,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} = ui

const STATUS_COLORS: Record<string, string> = {
  completed:
    'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25',
  failed: 'bg-red-500/15 text-red-400 border border-red-500/25',
  running:
    'bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/25',
  pending: 'bg-amber-500/15 text-amber-300 border border-amber-500/25',
  timeout: 'bg-amber-500/15 text-amber-300 border border-amber-500/25',
  skipped:
    'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25',
}

type Props = {
  trigger: AgentTrigger
  open: boolean
  onOpenChange: (open: boolean) => void
  onDelete: () => void
}

export function TriggerDetailSheet({
  trigger,
  open,
  onOpenChange,
  onDelete,
}: Props) {
  const { data: execData } = useTriggerExecutions(trigger.id, 20)
  const testTrigger = useTestTrigger()
  const updateTrigger = useUpdateTrigger()

  const executions = execData?.executions ?? []

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-[620px]"
      >
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {trigger.name}
            <span
              className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${trigger.enabled ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25' : 'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25'}`}
            >
              {trigger.enabled ? 'Active' : 'Disabled'}
            </span>
          </SheetTitle>
        </SheetHeader>

        <SheetBody className="flex-1 overflow-y-auto py-4 scrollbar-macos">
          <Tabs defaultValue="details" className="space-y-4">
            <div className="flex items-center justify-between gap-3">
              <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
                <TabsTrigger
                  className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                  value="details"
                >
                  Details
                </TabsTrigger>
                <TabsTrigger
                  className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                  value="circuit"
                >
                  Circuit Breaker
                </TabsTrigger>
                <TabsTrigger
                  className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                  value="executions"
                >
                  Executions ({execData?.total ?? 0})
                </TabsTrigger>
              </TabsList>

              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => testTrigger.mutate(trigger.id)}
                  disabled={testTrigger.isPending}
                >
                  {testTrigger.isPending ? 'Firing...' : 'Test Fire'}
                </Button>
              </div>
            </div>

            <TabsContent value="details" className="mt-0 space-y-4">
              <div className="grid grid-cols-2 gap-3">
                <ConfigField label="Type" value={trigger.triggerType} />
                {trigger.triggerType === 'cron' && (
                  <>
                    <ConfigField
                      label="Expression"
                      value={trigger.cronExpression}
                      mono
                    />
                    <ConfigField
                      label="Timezone"
                      value={trigger.cronTimezone || 'UTC'}
                    />
                  </>
                )}
                {trigger.triggerType === 'webhook' && trigger.webhookPath && (
                  <ConfigField
                    label="Webhook Path"
                    value={`/v1/triggers/webhook/${trigger.webhookPath}`}
                    mono
                  />
                )}
                {trigger.triggerType === 'event' && (
                  <>
                    <ConfigField
                      label="Source Agent"
                      value={trigger.eventSourceAgentId}
                      mono
                    />
                    <ConfigField
                      label="Event Type"
                      value={trigger.eventType}
                    />
                  </>
                )}
                <ConfigField
                  label="Timeout"
                  value={`${trigger.timeoutSeconds}s`}
                />
                <ConfigField
                  label="Max Retries"
                  value={String(trigger.maxRetries)}
                />
                <ConfigField
                  label="Max Concurrent"
                  value={String(trigger.maxConcurrent)}
                />
                <ConfigField
                  label="Created"
                  value={formatTimestamp(trigger.createdAt)}
                />
              </div>

              {trigger.inputTemplate && (
                <div className="space-y-2">
                  <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                    Input Template
                  </div>
                  <div className="whitespace-pre-wrap rounded border border-brand-main-600 bg-brand-main-900/50 p-3 font-mono text-xs text-brand-main-100">
                    {trigger.inputTemplate}
                  </div>
                </div>
              )}
            </TabsContent>

            <TabsContent value="circuit" className="mt-0 space-y-4">
              <div className="flex items-center gap-3">
                <span
                  className={`rounded px-2 py-1 text-xs font-medium ${
                    trigger.circuitState === 'closed'
                      ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25'
                      : trigger.circuitState === 'open'
                        ? 'bg-red-500/15 text-red-400 border border-red-500/25'
                        : 'bg-amber-500/15 text-amber-300 border border-amber-500/25'
                  }`}
                >
                  {trigger.circuitState || 'closed'}
                </span>
                <span className="text-xs text-white/40 light:text-black/40">
                  {trigger.consecutiveFailures} consecutive failures
                </span>
                {trigger.circuitState === 'open' && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-6 text-xs"
                    onClick={() =>
                      updateTrigger.mutate({
                        id: trigger.id,
                        enabled: trigger.enabled,
                      })
                    }
                  >
                    Reset
                  </Button>
                )}
              </div>
            </TabsContent>

            <TabsContent value="executions" className="mt-0 space-y-3">
              {executions.length === 0 ? (
                <div className="flex flex-col items-center justify-center rounded border border-dashed border-brand-main-600 bg-brand-main-900/30 py-8">
                  <div className="relative mb-4">
                    <div className="absolute inset-0 rounded-full bg-brand-secondary-500/20 blur-xl" />
                    <div className="relative rounded border border-brand-secondary-500/30 bg-brand-secondary-600/15 p-3">
                      <Iconify.Icon
                        icon="heroicons:play"
                        className="size-6 text-brand-secondary-400"
                      />
                    </div>
                  </div>
                  <h3 className="mb-1 text-sm font-medium text-white light:text-brand-main-50">
                    No executions yet
                  </h3>
                  <p className="max-w-xs text-center text-xs leading-relaxed text-white/50 light:text-black/50">
                    Executions will appear here when this trigger fires.
                  </p>
                </div>
              ) : (
                <div className="space-y-1.5">
                  {executions.map((exec) => (
                    <div
                      key={exec.id}
                      className="flex items-center justify-between rounded border border-brand-main-600 bg-brand-main-900/50 p-2 text-xs"
                    >
                      <div className="flex items-center gap-2">
                        <span
                          className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${STATUS_COLORS[exec.status] ?? ''}`}
                        >
                          {exec.status}
                        </span>
                        <span className="text-white/50 light:text-black/50">#{exec.attempt}</span>
                      </div>
                      <div className="flex items-center gap-3 text-white/40 light:text-black/40">
                        {exec.durationMs > 0 && (
                          <span>{exec.durationMs}ms</span>
                        )}
                        <span>{formatTimestamp(exec.startedAt)}</span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </SheetBody>

        <div className="border-t border-brand-main-700 px-6 py-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                updateTrigger.mutate({
                  id: trigger.id,
                  enabled: !trigger.enabled,
                })
              }
            >
              {trigger.enabled ? 'Disable' : 'Enable'}
            </Button>
            <Button size="sm" variant="destructive" onClick={onDelete}>
              Delete
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function ConfigField({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="space-y-0.5">
      <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div
        className={`truncate text-sm text-brand-main-100 ${mono ? 'font-mono text-xs' : ''}`}
      >
        {value || '-'}
      </div>
    </div>
  )
}
