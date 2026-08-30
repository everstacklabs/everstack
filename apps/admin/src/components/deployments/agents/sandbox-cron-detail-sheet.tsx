import { useEffect, useState } from 'react'
import { useSandboxTriggers } from '@/hooks/deployments/use-sandbox'
import { ui } from '@everstack/ui'
import { formatTimestamp } from '@everstack/utils/functions/index'
import type { SandboxCron } from '@/server/sandbox'

const {
  Button,
  Input,
  Label,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetBody,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} = ui

type Props = {
  cron: SandboxCron
  open: boolean
  onOpenChange: (open: boolean) => void
  onToggle: () => void
  onDelete: () => void
  onRunNow: () => void
  onSave: (values: {
    name: string
    schedule: string
    command: string
    workDir: string
    timeoutSeconds: number
    autoRecreate: boolean
  }) => void
  isUpdating?: boolean
  isDeleting?: boolean
  isRunning?: boolean
  isSaving?: boolean
}

export function SandboxCronDetailSheet({
  cron,
  open,
  onOpenChange,
  onToggle,
  onDelete,
  onRunNow,
  onSave,
  isUpdating,
  isDeleting,
  isRunning,
  isSaving,
}: Props) {
  const [isEditing, setIsEditing] = useState(false)
  const [name, setName] = useState(cron.name)
  const [schedule, setSchedule] = useState(cron.schedule)
  const [command, setCommand] = useState(cron.command)
  const [workDir, setWorkDir] = useState(cron.workDir || '/workspace')
  const [timeoutSeconds, setTimeoutSeconds] = useState(
    String(cron.timeoutSeconds),
  )
  const [autoRecreate, setAutoRecreate] = useState(cron.autoRecreate)
  const { data: triggerData } = useSandboxTriggers(cron.sandboxId, {
    triggerType: 'cron',
    limit: 20,
  })
  const executions = (triggerData?.triggers ?? []).filter(
    (trigger) => String(trigger.triggerId) === String(cron.id),
  )

  useEffect(() => {
    setName(cron.name)
    setSchedule(cron.schedule)
    setCommand(cron.command)
    setWorkDir(cron.workDir || '/workspace')
    setTimeoutSeconds(String(cron.timeoutSeconds))
    setAutoRecreate(cron.autoRecreate)
    setIsEditing(false)
  }, [cron])

  const handleSave = () => {
    onSave({
      name: name.trim(),
      schedule: schedule.trim(),
      command: command.trim(),
      workDir: workDir.trim(),
      timeoutSeconds: Number(timeoutSeconds) || cron.timeoutSeconds,
      autoRecreate,
    })
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-[620px]"
      >
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {cron.name}
            <span
              className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${cron.enabled ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25' : 'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25'}`}
            >
              {cron.enabled ? 'Enabled' : 'Disabled'}
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
                  value="activity"
                >
                  Activity
                </TabsTrigger>
                <TabsTrigger
                  className="relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50"
                  value="history"
                >
                  History ({executions.length})
                </TabsTrigger>
              </TabsList>

              {!isEditing && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setIsEditing(true)}
                >
                  Edit
                </Button>
              )}
            </div>

            <TabsContent value="details" className="mt-0 space-y-4">
              {isEditing ? (
                <div className="space-y-4">
                  <div className="space-y-2">
                    <Label htmlFor="cron-name">Name</Label>
                    <Input
                      id="cron-name"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label htmlFor="cron-schedule">Schedule</Label>
                      <Input
                        id="cron-schedule"
                        value={schedule}
                        onChange={(e) => setSchedule(e.target.value)}
                        className="font-mono"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="cron-timeout">Timeout (seconds)</Label>
                      <Input
                        id="cron-timeout"
                        type="number"
                        min={1}
                        value={timeoutSeconds}
                        onChange={(e) => setTimeoutSeconds(e.target.value)}
                      />
                    </div>
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="cron-command">Command</Label>
                    <Textarea
                      id="cron-command"
                      value={command}
                      onChange={(e) => setCommand(e.target.value)}
                      className="min-h-28 font-mono text-xs"
                    />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label htmlFor="cron-workdir">Work Dir</Label>
                      <Input
                        id="cron-workdir"
                        value={workDir}
                        onChange={(e) => setWorkDir(e.target.value)}
                        className="font-mono"
                      />
                    </div>
                    <div className="flex items-end">
                      <label className="flex items-center gap-3 rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2.5">
                        <Switch
                          checked={autoRecreate}
                          onCheckedChange={setAutoRecreate}
                        />
                        <div>
                          <div className="text-sm text-brand-main-100">
                            Auto Recreate
                          </div>
                          <div className="text-[10px] text-white/50 light:text-black/50">
                            Recreate if sandbox restarts
                          </div>
                        </div>
                      </label>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 pt-2">
                    <Button
                      variant="outline"
                      onClick={() => {
                        setName(cron.name)
                        setSchedule(cron.schedule)
                        setCommand(cron.command)
                        setWorkDir(cron.workDir || '/workspace')
                        setTimeoutSeconds(String(cron.timeoutSeconds))
                        setAutoRecreate(cron.autoRecreate)
                        setIsEditing(false)
                      }}
                    >
                      Cancel
                    </Button>
                    <Button
                      onClick={handleSave}
                      disabled={
                        isSaving ||
                        !name.trim() ||
                        !schedule.trim() ||
                        !command.trim()
                      }
                    >
                      {isSaving ? 'Saving...' : 'Save Changes'}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-4">
                  <div className="grid grid-cols-2 gap-3">
                    <ConfigField
                      label="Schedule"
                      value={cron.schedule}
                      mono
                    />
                    <ConfigField
                      label="Timeout"
                      value={`${cron.timeoutSeconds}s`}
                    />
                    <ConfigField
                      label="Session"
                      value={cron.sessionId}
                      mono
                    />
                    <ConfigField
                      label="Sandbox"
                      value={cron.sandboxId}
                      mono
                    />
                    <ConfigField
                      label="Auto Recreate"
                      value={cron.autoRecreate ? 'Yes' : 'No'}
                    />
                    <ConfigField
                      label="Created"
                      value={formatTimestamp(cron.createdAt)}
                    />
                    <ConfigField
                      label="Updated"
                      value={formatTimestamp(cron.updatedAt)}
                    />
                    <ConfigField
                      label="Next Run"
                      value={
                        cron.nextRunAt
                          ? formatTimestamp(cron.nextRunAt)
                          : '--'
                      }
                    />
                  </div>

                  <div className="space-y-2">
                    <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                      Command
                    </div>
                    <div className="whitespace-pre-wrap break-all rounded border border-brand-main-600 bg-brand-main-900/50 p-3 font-mono text-xs text-brand-main-100">
                      {cron.command}
                    </div>
                  </div>

                  <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                    Run Stats
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <ConfigField
                      label="Runs"
                      value={String(cron.runCount)}
                    />
                    <ConfigField
                      label="Errors"
                      value={String(cron.errorCount)}
                    />
                    <ConfigField
                      label="Last Run"
                      value={
                        cron.lastRunAt
                          ? formatTimestamp(cron.lastRunAt)
                          : '--'
                      }
                    />
                    <ConfigField
                      label="Work Dir"
                      value={cron.workDir || '/workspace'}
                      mono
                    />
                  </div>
                  {cron.lastError && (
                    <div className="space-y-2">
                      <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                        Last Error
                      </div>
                      <div className="whitespace-pre-wrap break-all rounded border border-red-500/25 bg-red-500/5 p-3 text-xs text-red-200">
                        {cron.lastError}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </TabsContent>

            <TabsContent value="activity" className="mt-0 space-y-3">
              <ActivityRow
                label="Last successful or attempted run"
                value={
                  cron.lastRunAt
                    ? formatTimestamp(cron.lastRunAt)
                    : 'No runs yet'
                }
              />
              <ActivityRow
                label="Next scheduled run"
                value={
                  cron.nextRunAt
                    ? formatTimestamp(cron.nextRunAt)
                    : 'Not scheduled'
                }
              />
              <ActivityRow
                label="Execution summary"
                value={`${cron.runCount} total run${cron.runCount === 1 ? '' : 's'}${cron.errorCount > 0 ? `, ${cron.errorCount} error${cron.errorCount === 1 ? '' : 's'}` : ', 0 errors'}`}
              />
              <ActivityRow
                label="Current state"
                value={
                  cron.enabled
                    ? 'Active and eligible to run on schedule'
                    : 'Disabled until re-enabled'
                }
              />
            </TabsContent>

            <TabsContent value="history" className="mt-0 space-y-3">
              {executions.length === 0 ? (
                <div className="rounded border border-dashed border-brand-main-600 bg-brand-main-900/30 px-3 py-8 text-center">
                  <div className="text-sm font-medium text-white light:text-brand-main-50">
                    No recorded executions yet
                  </div>
                  <div className="mt-1 text-xs text-white/50 light:text-black/50">
                    Executions will appear here when this schedule runs.
                  </div>
                </div>
              ) : (
                <div className="space-y-1.5">
                  {executions.map((execution) => (
                    <div
                      key={execution.id}
                      className="rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-3"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <span
                          className={`rounded px-2 py-0.5 text-[10px] font-medium ${execution.status === 'completed' ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/25' : 'bg-red-500/15 text-red-400 border border-red-500/25'}`}
                        >
                          {execution.status}
                        </span>
                        <span className="text-[11px] text-white/40 light:text-black/40">
                          {formatTimestamp(execution.createdAt)}
                        </span>
                      </div>
                      <div className="mt-2 text-xs text-white/50 light:text-black/50">
                        Duration: {execution.durationMs}ms
                      </div>
                      {execution.error && (
                        <div className="mt-2 whitespace-pre-wrap break-all rounded border border-red-500/25 bg-red-500/5 px-2 py-1.5 text-xs text-red-200">
                          {execution.error}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </SheetBody>

        <div className="border-t border-brand-main-700 px-6 py-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                onClick={onToggle}
                disabled={isUpdating || isEditing}
              >
                {cron.enabled ? 'Disable' : 'Enable'}
              </Button>
              <Button
                variant="outline"
                onClick={onRunNow}
                disabled={isRunning || isEditing}
              >
                {isRunning ? 'Running...' : 'Run Now'}
              </Button>
            </div>
            <Button
              variant="destructive"
              onClick={onDelete}
              disabled={isDeleting || isEditing}
            >
              Delete
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function ActivityRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-brand-main-600 bg-brand-main-900/50 px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
        {label}
      </div>
      <div className="mt-1 text-sm text-brand-main-100">{value}</div>
    </div>
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
