import { useEffect, useMemo, useState } from 'react'
import { Icon } from '@iconify/react'
import { getPresignedDownloadURL } from '@/server/storage'
import type { ExecutionEvent } from '@/stores/execution-store'

interface BrowserRunPanelProps {
  events: ExecutionEvent[]
  tenantId: string
}

interface BrowserStep {
  event: ExecutionEvent
  sequence: number
  kind:
    | 'start'
    | 'ready'
    | 'navigate'
    | 'action'
    | 'snapshot'
    | 'error'
    | 'closed'
  title: string
  detail: string
  artifactId?: string
}

const browserEventTypes = new Set([
  'agent.browser.started',
  'agent.browser.ready',
  'agent.browser.navigate',
  'agent.browser.action',
  'agent.browser.snapshot',
  'agent.browser.error',
  'agent.browser.closed',
])

function toBrowserStep(event: ExecutionEvent, index: number): BrowserStep {
  const data = event.data ?? {}
  const sequence = Number.parseInt(data.sequence ?? '', 10) || index + 1

  switch (event.type) {
    case 'agent.browser.started':
      return {
        event,
        sequence,
        kind: 'start',
        title: 'Browser allocated',
        detail: 'Preparing an isolated browser session',
      }
    case 'agent.browser.ready':
      return {
        event,
        sequence,
        kind: 'ready',
        title: 'Browser ready',
        detail:
          data.headless === 'true' ? 'Headless session' : 'Interactive session',
      }
    case 'agent.browser.navigate':
      return {
        event,
        sequence,
        kind: 'navigate',
        title: data.title || 'Navigated',
        detail: data.url || 'Page changed',
      }
    case 'agent.browser.action':
      return {
        event,
        sequence,
        kind: 'action',
        title: formatAction(data.action),
        detail: data.selector || 'Current page',
      }
    case 'agent.browser.snapshot':
      return {
        event,
        sequence,
        kind: 'snapshot',
        title:
          data.auto === 'true' ? 'Automatic snapshot' : 'Snapshot captured',
        detail:
          data.snapshot_status === 'stored'
            ? `${formatBytes(data.size_bytes)} · retained with this run`
            : data.snapshot_error || 'Snapshot metadata recorded',
        artifactId: data.artifact_id,
      }
    case 'agent.browser.error':
      return {
        event,
        sequence,
        kind: 'error',
        title: 'Browser error',
        detail: event.error || data.error || 'Browser action failed',
      }
    default:
      return {
        event,
        sequence,
        kind: 'closed',
        title: 'Browser released',
        detail: 'Active runtime billing stopped',
      }
  }
}

function formatAction(action?: string) {
  if (!action) return 'Browser action'
  return action
    .split('_')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function formatBytes(value?: string) {
  const bytes = Number(value)
  if (!Number.isFinite(bytes) || bytes <= 0) return 'Snapshot'
  if (bytes < 1024) return `${bytes} B`
  return `${(bytes / 1024).toFixed(0)} KB`
}

function formatElapsed(
  timestamp: number | undefined,
  start: number | undefined,
) {
  if (!timestamp || !start) return '—'
  const elapsed = Math.max(0, timestamp - start)
  if (elapsed < 1000) return `${elapsed}ms`
  return `${(elapsed / 1000).toFixed(1)}s`
}

const stepIcons: Record<BrowserStep['kind'], string> = {
  start: 'lucide:power',
  ready: 'lucide:monitor-check',
  navigate: 'lucide:corner-down-right',
  action: 'lucide:mouse-pointer-click',
  snapshot: 'lucide:camera',
  error: 'lucide:triangle-alert',
  closed: 'lucide:circle-stop',
}

export function BrowserRunPanel({ events, tenantId }: BrowserRunPanelProps) {
  const steps = useMemo(
    () =>
      events
        .filter((event) => browserEventTypes.has(event.type))
        .map(toBrowserStep)
        .sort((a, b) => a.sequence - b.sequence),
    [events],
  )
  const snapshots = useMemo(
    () => steps.filter((step) => step.kind === 'snapshot' && step.artifactId),
    [steps],
  )
  const [selectedArtifactId, setSelectedArtifactId] = useState<string | null>(
    null,
  )
  const [snapshotUrl, setSnapshotUrl] = useState<string | null>(null)
  const [snapshotError, setSnapshotError] = useState<string | null>(null)
  const [snapshotLoading, setSnapshotLoading] = useState(false)

  useEffect(() => {
    const latest = snapshots.at(-1)?.artifactId ?? null
    setSelectedArtifactId(latest)
  }, [snapshots.length])

  useEffect(() => {
    if (!selectedArtifactId || !tenantId) {
      setSnapshotUrl(null)
      return
    }

    let active = true
    setSnapshotLoading(true)
    setSnapshotError(null)
    getPresignedDownloadURL({ tenantId, objectId: selectedArtifactId })
      .then((response) => {
        if (active) setSnapshotUrl(response.downloadUrl)
      })
      .catch((error) => {
        if (active) {
          setSnapshotUrl(null)
          setSnapshotError(
            error instanceof Error ? error.message : 'Snapshot unavailable',
          )
        }
      })
      .finally(() => {
        if (active) setSnapshotLoading(false)
      })
    return () => {
      active = false
    }
  }, [selectedArtifactId, tenantId])

  const startedAt = steps[0]?.event.timestamp
  const lastNavigation = [...steps]
    .reverse()
    .find((step) => step.kind === 'navigate')
  const isRunning =
    steps.length > 0 &&
    steps.at(-1)?.kind !== 'closed' &&
    steps.at(-1)?.kind !== 'error'
  const selectedSnapshot = snapshots.find(
    (step) => step.artifactId === selectedArtifactId,
  )

  if (steps.length === 0) {
    return (
      <div className="flex min-h-[320px] flex-col items-center justify-center p-8 text-center">
        <div className="flex size-10 items-center justify-center border border-brand-main-600 bg-brand-main-800 rounded">
          <Icon
            icon="lucide:mouse-pointer-click"
            className="size-5 text-brand-secondary-300"
          />
        </div>
        <p className="mt-4 text-sm font-medium text-brand-main-100">
          No browser run in this execution
        </p>
        <p className="mt-1 max-w-sm text-xs leading-5 text-brand-main-400">
          Enable Computer use on an inline agent, or select an agent with
          browser automation enabled. Actions and retained snapshots will appear
          here.
        </p>
      </div>
    )
  }

  return (
    <div className="flex min-h-full flex-col bg-brand-main-950">
      <div className="flex items-center justify-between border-b border-brand-main-700 px-3 py-2">
        <div className="flex items-center gap-2">
          <span
            className={`size-1.5 rounded-full ${isRunning ? 'bg-emerald-400 animate-pulse' : 'bg-brand-main-500'}`}
          />
          <span className="text-xs font-medium text-brand-main-100">
            Computer use run
          </span>
          <span className="text-[10px] text-brand-main-400">
            {steps.length} events
          </span>
        </div>
        <div className="flex items-center gap-1.5 text-[10px] text-brand-main-400">
          <Icon icon="lucide:camera" className="size-3" />
          <span>{snapshots.length} retained</span>
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-rows-[minmax(240px,1fr)_minmax(180px,.7fr)]">
        <div className="min-h-0 p-3">
          <div className="flex h-full min-h-[220px] flex-col overflow-hidden border border-brand-main-700 bg-black rounded">
            <div className="flex h-8 shrink-0 items-center gap-2 border-b border-brand-main-700 bg-brand-main-900 px-2.5">
              <div className="flex gap-1">
                <span className="size-1.5 rounded-full bg-brand-main-600" />
                <span className="size-1.5 rounded-full bg-brand-main-600" />
                <span className="size-1.5 rounded-full bg-brand-main-600" />
              </div>
              <div className="flex min-w-0 flex-1 items-center gap-1.5 border border-brand-main-700 bg-brand-main-950 px-2 py-1 rounded">
                <Icon
                  icon="lucide:lock-keyhole"
                  className="size-2.5 shrink-0 text-brand-main-500"
                />
                <span className="truncate font-mono text-[9px] text-brand-main-300">
                  {lastNavigation?.detail || 'about:blank'}
                </span>
              </div>
              <span className="border border-brand-main-700 px-1.5 py-0.5 text-[9px] text-brand-main-400 rounded">
                recorded
              </span>
            </div>

            <div className="relative flex min-h-0 flex-1 items-center justify-center">
              {snapshotLoading ? (
                <Icon
                  icon="lucide:loader-circle"
                  className="size-5 animate-spin text-brand-main-500"
                />
              ) : snapshotUrl ? (
                <img
                  src={snapshotUrl}
                  alt={`Browser snapshot ${selectedSnapshot?.sequence ?? ''}`}
                  className="h-full w-full object-contain"
                />
              ) : (
                <div className="flex flex-col items-center gap-2 px-5 text-center text-brand-main-500">
                  <Icon
                    icon={snapshotError ? 'lucide:image-off' : 'lucide:camera'}
                    className="size-6"
                  />
                  <span className="text-xs">
                    {snapshotError || 'Waiting for the first retained snapshot'}
                  </span>
                </div>
              )}
              {selectedSnapshot && (
                <div className="absolute bottom-2 left-2 border border-white/10 bg-black/75 px-2 py-1 font-mono text-[9px] text-white/70 backdrop-blur rounded">
                  step {selectedSnapshot.sequence} ·{' '}
                  {formatElapsed(selectedSnapshot.event.timestamp, startedAt)}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="min-h-0 border-t border-brand-main-700">
          <div className="flex h-8 items-center justify-between border-b border-brand-main-700 px-3">
            <span className="text-[10px] font-medium uppercase tracking-[0.14em] text-brand-main-400">
              Action ledger
            </span>
            <span className="font-mono text-[9px] text-brand-main-500">
              immutable with run
            </span>
          </div>
          <div className="h-[calc(100%-2rem)] overflow-y-auto">
            {steps.map((step) => {
              const selected =
                !!step.artifactId && step.artifactId === selectedArtifactId
              const interactive = !!step.artifactId
              return (
                <button
                  key={`${step.sequence}-${step.event.type}-${step.event.timestamp ?? 0}`}
                  type="button"
                  disabled={!interactive}
                  onClick={() =>
                    step.artifactId && setSelectedArtifactId(step.artifactId)
                  }
                  className={`grid w-full grid-cols-[2rem_1.25rem_minmax(0,1fr)_3rem] items-center border-b border-brand-main-800 px-2 py-2 text-left transition-colors ${
                    selected
                      ? 'bg-brand-secondary-500/10'
                      : interactive
                        ? 'hover:bg-brand-main-900'
                        : ''
                  }`}
                >
                  <span className="font-mono text-[9px] text-brand-main-500">
                    {String(step.sequence).padStart(2, '0')}
                  </span>
                  <span
                    className={`flex size-5 items-center justify-center rounded ${
                      step.kind === 'error'
                        ? 'bg-red-500/10 text-red-400'
                        : step.kind === 'snapshot'
                          ? 'bg-brand-secondary-500/10 text-brand-secondary-300'
                          : 'bg-brand-main-800 text-brand-main-300'
                    }`}
                  >
                    <Icon icon={stepIcons[step.kind]} className="size-3" />
                  </span>
                  <span className="min-w-0 px-2">
                    <span className="block truncate text-[11px] font-medium text-brand-main-100">
                      {step.title}
                    </span>
                    <span className="mt-0.5 block truncate text-[10px] text-brand-main-400">
                      {step.detail}
                    </span>
                  </span>
                  <span className="text-right font-mono text-[9px] text-brand-main-500">
                    {formatElapsed(step.event.timestamp, startedAt)}
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      </div>
    </div>
  )
}
