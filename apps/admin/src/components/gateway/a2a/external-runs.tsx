import { useMemo, useState } from 'react'
import { create } from 'zustand'
import { ui } from '@everstack/ui'
import {
  describeInteropError,
  interopErrorCode,
  isRetryableInteropError,
  useExternalRun,
  useExternalRunAction,
  useExternalRuns,
  useStartExternalRun,
  type ExternalRun,
  type RemoteAgent,
} from '@/hooks/gateway/use-interop'

const {
  Button,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} = ui

const inputClass =
  'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
const selectTriggerClass =
  'w-full bg-brand-main-900/60 border-brand-main-600 text-white light:text-brand-main-50'
const selectContentClass =
  'bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50'
const sectionLabelClass =
  'text-[11px] uppercase tracking-wider text-white/45 light:text-black/45'

// The gateway measures the task in UTF-8 bytes, not characters.
const MESSAGE_LIMIT_BYTES = 65536
const utf8 = new TextEncoder()

const terminal = new Set<ExternalRun['state']>([
  'completed',
  'failed',
  'canceled',
  'rejected',
])

function stateLabel(state: ExternalRun['state']): string {
  if (state === 'unknown') return 'Outcome unknown'
  if (state === 'rejected') return 'Refused by remote'
  const label = state.replaceAll('-', ' ')
  return label.charAt(0).toUpperCase() + label.slice(1)
}

function stateTone(state: ExternalRun['state']): string {
  switch (state) {
    case 'completed':
      return 'text-success'
    case 'failed':
    case 'rejected':
      return 'text-danger'
    case 'unknown':
      return 'text-warning'
    default:
      return 'text-brand-main-300'
  }
}

// Same guarded fallback as the playground store: crypto.randomUUID only exists
// in secure contexts, and self-hosted admins are often served over plain HTTP.
const newIdempotencyKey = () =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`

type Submission = {
  remote: string
  message: string
  idempotency_key: string
  /** False once the outcome proves a fresh key is safe and needed. */
  reuseKey: boolean
  sending: boolean
  run?: { id: string; state: ExternalRun['state'] }
  failure?: unknown
}

// Module scope on purpose: the last submission and its idempotency key must
// outlive this tab unmounting (switching tabs), so retrying after an ambiguous
// failure cannot start a second run. It does not need to survive a reload.
const useSubmissionStore = create<{ submission: Submission | null }>(() => ({
  submission: null,
}))
const remember = (submission: Submission | null) =>
  useSubmissionStore.setState({ submission })

/** The connection of the last submission, so the page can restore it on return. */
export function pendingRunRemote(): string {
  return useSubmissionStore.getState().submission?.remote ?? ''
}

export function ExternalRuns({
  remotes,
  selected,
  onSelect,
}: {
  remotes: RemoteAgent[]
  selected: string
  onSelect: (name: string) => void
}) {
  const runs = useExternalRuns()
  const start = useStartExternalRun()
  const action = useExternalRunAction()
  const submission = useSubmissionStore((state) => state.submission)
  // Restore the draft so an unchanged retry can reuse the remembered key.
  const [message, setMessage] = useState(
    () => useSubmissionStore.getState().submission?.message ?? '',
  )
  const [selectedRun, setSelectedRun] = useState('')
  const detail = useExternalRun(selectedRun)

  // Every status below comes from the remembered submission, and only while
  // it matches the current connection and task. Changing either one, from
  // here or by picking another connection above, shows a fresh form.
  const current =
    submission?.remote === selected && submission.message === message
      ? submission
      : null
  const sending = Boolean(submission?.sending)
  const remoteExists = remotes.some((remote) => remote.name === selected)
  const messageBytes = useMemo(() => utf8.encode(message).length, [message])
  const tooLong = messageBytes > MESSAGE_LIMIT_BYTES
  const canSubmit = remoteExists && Boolean(message.trim()) && !tooLong

  const submit = () => {
    if (sending || !canSubmit) return
    if (current?.run && current.reuseKey) {
      setSelectedRun(current.run.id)
      return
    }
    const attempt: Submission = {
      remote: selected,
      message,
      idempotency_key: current?.reuseKey
        ? current.idempotency_key
        : newIdempotencyKey(),
      reuseKey: true,
      sending: true,
    }
    remember(attempt)
    // mutateAsync rather than mutate callbacks: the outcome must be recorded
    // even if this tab unmounts while the request is in flight.
    start
      .mutateAsync({
        remote: attempt.remote,
        message: attempt.message,
        idempotency_key: attempt.idempotency_key,
      })
      .then(
        (run) => {
          // A refusal proves the remote did no work, so the next send
          // (for example after fixing the protocol) may use a new key.
          remember({
            ...attempt,
            sending: false,
            reuseKey: run.state !== 'rejected',
            run: { id: run.id, state: run.state },
          })
          setSelectedRun(run.id)
        },
        (failure: unknown) => {
          remember({
            ...attempt,
            sending: false,
            reuseKey: interopErrorCode(failure) !== 'idempotency_conflict',
            failure,
          })
        },
      )
  }

  const submitLabel = sending
    ? 'Sending…'
    : current?.run
      ? current.reuseKey
        ? 'View existing run'
        : 'Send again'
      : current?.failure
        ? 'Retry submission'
        : 'Start run'

  const run = detail.data
  // The gateway also refuses when the endpoint or routing tenant changed; runs
  // expose only the protocol, so that is the part checked here.
  const connection = run
    ? remotes.find((remote) => remote.id === run.remote_id)
    : undefined
  const connectionChanged = run
    ? !connection || connection.protocol !== run.protocol
    : false
  const canAct = run
    ? Boolean(run.remote_task_id) &&
      !terminal.has(run.state) &&
      !connectionChanged
    : false

  return (
    <section className="space-y-4 border-t border-brand-main-600/40 pt-5">
      <div>
        <h2 className="text-sm font-medium text-brand-secondary-100">
          Run an external agent
        </h2>
        <p className="mt-1 text-xs leading-5 text-brand-main-300">
          Start a task, then use its saved run to check progress.
        </p>
      </div>
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault()
          submit()
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="external-remote">Connection</Label>
          <Select
            value={remoteExists ? selected : ''}
            // Inside a form, Radix mirrors the value into a hidden native
            // select that reports '' when the value changes before the options
            // mount (e.g. while connections load). Nobody can pick '', so ignore
            // it rather than clear the restored connection.
            onValueChange={(value) => {
              if (value) onSelect(value)
            }}
            disabled={sending || remotes.length === 0}
          >
            <SelectTrigger id="external-remote" className={selectTriggerClass}>
              <SelectValue placeholder="Select a remote agent" />
            </SelectTrigger>
            <SelectContent className={selectContentClass}>
              {remotes.map((remote) => (
                <SelectItem key={remote.id} value={remote.name}>
                  {remote.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor="external-message">Task</Label>
          <Textarea
            id="external-message"
            value={message}
            disabled={sending}
            onChange={(event) => setMessage(event.target.value)}
            placeholder="What should this agent do?"
            rows={3}
            aria-invalid={tooLong}
            aria-describedby={tooLong ? 'external-message-limit' : undefined}
            className={inputClass}
          />
          {tooLong && (
            <p id="external-message-limit" className="text-xs text-danger">
              This task is {messageBytes.toLocaleString()} bytes, over the
              65,536 byte limit.
            </p>
          )}
        </div>
        {current?.failure !== undefined && (
          <p role="alert" className="text-sm text-danger">
            {describeInteropError(current.failure)}
            {isRetryableInteropError(current.failure) &&
              ' Retrying this unchanged task uses the same key and will not send it twice.'}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" disabled={sending || !canSubmit}>
            {submitLabel}
          </Button>
          {current?.run && !sending && (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                remember(null)
                setMessage('')
              }}
            >
              New task
            </Button>
          )}
        </div>
      </form>
      <div className="grid gap-4 lg:grid-cols-[minmax(220px,1fr)_2fr]">
        <div className="rounded border border-brand-main-600/40 bg-brand-main-800/40">
          <h3
            className={`border-b border-brand-main-600/40 px-3 py-2 ${sectionLabelClass}`}
          >
            Recent runs · latest 50
          </h3>
          {runs.isLoading && (
            <p className="p-3 text-xs text-white/45 light:text-black/45">
              Loading runs…
            </p>
          )}
          {runs.isError && (
            <p role="alert" className="p-3 text-xs text-danger">
              Could not load runs.
            </p>
          )}
          {!runs.isLoading && !runs.isError && runs.data?.length === 0 && (
            <p className="p-3 text-xs text-white/45 light:text-black/45">
              No runs yet.
            </p>
          )}
          <div className="max-h-96 overflow-auto">
            {runs.data?.map((item) => (
              <button
                key={item.id}
                type="button"
                className={`block w-full border-b border-brand-main-600/40 px-3 py-3 text-left transition-colors last:border-b-0 hover:bg-brand-main-700/40 ${selectedRun === item.id ? 'bg-brand-main-700/40' : ''}`}
                onClick={() => {
                  setSelectedRun(item.id)
                  action.reset()
                }}
                aria-pressed={selectedRun === item.id}
              >
                <span className="flex justify-between gap-2 text-xs">
                  <span className="truncate font-medium text-brand-secondary-100">
                    {item.remote_name}
                  </span>
                  <span className={`shrink-0 ${stateTone(item.state)}`}>
                    {stateLabel(item.state)}
                  </span>
                </span>
                <time
                  className="mt-1 block text-[11px] text-brand-main-300"
                  dateTime={item.created_at}
                >
                  {new Date(item.created_at).toLocaleString()}
                </time>
              </button>
            ))}
          </div>
        </div>
        <div
          className="min-w-0 rounded border border-brand-main-600/40 bg-brand-main-800/40 p-4"
          aria-live="polite"
        >
          {!selectedRun && (
            <p className="text-sm text-white/45 light:text-black/45">
              Select a run to inspect its status and result.
            </p>
          )}
          {detail.isFetching && !run && (
            <p className="text-sm text-brand-main-300">Loading run…</p>
          )}
          {detail.isError && (
            <p role="alert" className="text-sm text-danger">
              Could not load this run.
            </p>
          )}
          {run && (
            <>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h3 className="text-sm font-medium text-brand-secondary-100">
                    {run.remote_name}
                  </h3>
                  <p className={`mt-1 text-xs ${stateTone(run.state)}`}>
                    {stateLabel(run.state)}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    disabled={!canAct || action.isPending}
                    onClick={() =>
                      action.mutate({ id: run.id, action: 'refresh' })
                    }
                  >
                    Refresh status
                  </Button>
                  <Button
                    variant="ghost"
                    disabled={!canAct || action.isPending}
                    onClick={() =>
                      action.mutate({ id: run.id, action: 'cancel' })
                    }
                  >
                    Request cancellation
                  </Button>
                </div>
              </div>
              <p className="mt-3 break-all font-mono text-[11px] text-brand-main-300">
                Run {run.id}
              </p>
              {run.state === 'rejected' && (
                <p className="mt-3 text-sm text-danger">
                  The remote agent refused this task, so no work was done.
                  {run.last_error && ` ${describeInteropError(run.last_error)}`}
                </p>
              )}
              {run.state === 'unknown' && (
                <p className="mt-3 text-sm text-warning">
                  The remote may have accepted this task. Do not submit it
                  again.{' '}
                  {run.remote_task_id
                    ? 'Refresh this run to reconcile its status.'
                    : 'No remote task handle was received, so check with the agent operator.'}
                </p>
              )}
              {(run.state === 'input-required' ||
                run.state === 'auth-required') && (
                <p className="mt-3 text-sm text-brand-main-300">
                  The remote needs intervention. Respond in the remote agent’s
                  interface, then refresh here. Continuing with input through
                  this gateway is not supported yet.
                </p>
              )}
              {connectionChanged && !terminal.has(run.state) && (
                <p className="mt-3 text-xs text-brand-main-300">
                  {describeInteropError('remote_connection_changed')}
                </p>
              )}
              {run.cancel_requested_at && run.state !== 'canceled' && (
                <p className="mt-3 text-xs text-brand-main-300">
                  Cancellation was requested, but the current state does not
                  confirm that it stopped.
                </p>
              )}
              {run.last_error && run.state !== 'rejected' && (
                <p className="mt-3 text-xs text-danger">
                  Last operation: {describeInteropError(run.last_error)}
                </p>
              )}
              {action.isError && (
                <p role="alert" className="mt-3 text-xs text-danger">
                  {describeInteropError(action.error)}
                </p>
              )}
              {run.output ? (
                <div className="mt-4 border-t border-brand-main-600/40 pt-3">
                  <h4 className={`mb-2 ${sectionLabelClass}`}>
                    {run.state === 'completed' ? 'Result' : 'Partial output'}
                  </h4>
                  <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-sm leading-6 text-brand-secondary-100">
                    {run.output}
                  </pre>
                </div>
              ) : (
                <p className="mt-4 text-xs text-white/45 light:text-black/45">
                  No text output received.
                </p>
              )}
            </>
          )}
        </div>
      </div>
    </section>
  )
}
