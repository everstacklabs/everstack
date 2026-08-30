import { Iconify } from '@everstack/ui/icons'
import { useSandboxContext } from './sandbox-context'
import { sandboxStatusLabel } from './lifecycle'
import { useSandboxEvents } from '@/hooks/deployments/use-sandbox'

// DetailOverview: the at-a-glance panel for one sandbox. Facts on the
// left, recent lifecycle events on the right. Lifecycle ACTIONS live
// in the detail page header, not here, so there is exactly one place
// to act.

function formatMinutes(mins?: number): string {
  if (mins === undefined || mins === null) return 'default'
  if (mins < 0) return 'never'
  if (mins === 0) return 'disabled'
  if (mins % 1440 === 0) return `${mins / 1440}d`
  if (mins % 60 === 0) return `${mins / 60}h`
  return `${mins}m`
}

function formatDuration(seconds = 0): string {
  if (seconds < 60) return `${Math.max(0, seconds)}s`
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`
}

function formatUsd(value = 0): string {
  if (value <= 0) return '$0.00'
  if (value < 0.01) return '<$0.01'
  return value < 1 ? `$${value.toFixed(3)}` : `$${value.toFixed(2)}`
}

export function DetailOverview() {
  const { instances, activeSandboxId } = useSandboxContext()
  const inst = instances.find((i) => i.id === activeSandboxId)
  const { data: eventsData } = useSandboxEvents(activeSandboxId, { limit: 12 })

  if (!inst) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-white/50 light:text-black/50">
        Sandbox not found.
      </div>
    )
  }

  const config = (inst.config ?? {}) as Record<string, unknown>
  const cpu = config.cpu_limit ?? config.cpuLimit
  const memoryMb = config.memory_mb ?? config.memoryMB
  const diskMb = config.disk_mb ?? config.diskMB

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 max-w-5xl">
        {/* Facts */}
        <section className="rounded border border-brand-main-700 bg-brand-main-900/50 p-4">
          <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-3">
            Details
          </h3>
          <dl className="grid grid-cols-[130px_1fr] gap-y-2 text-sm">
            <dt className="text-white/45 light:text-black/45">State</dt>
            <dd className="text-white/85 light:text-black/85">
              {sandboxStatusLabel(inst)}
            </dd>
            {inst.errorReason && (
              <>
                <dt className="text-white/45 light:text-black/45">Error</dt>
                <dd className="text-brand-secondary-200 font-mono text-xs">
                  {inst.errorReason}
                </dd>
              </>
            )}
            <dt className="text-white/45 light:text-black/45">Image</dt>
            <dd className="text-white/85 light:text-black/85 font-mono text-xs break-all">
              {inst.image}
            </dd>
            <dt className="text-white/45 light:text-black/45">Backend</dt>
            <dd className="text-white/85 light:text-black/85">
              {inst.backend}
            </dd>
            {cpu || memoryMb || diskMb ? (
              <>
                <dt className="text-white/45 light:text-black/45">Resources</dt>
                <dd className="text-white/85 light:text-black/85">
                  {[
                    cpu ? `${cpu} vCPU` : null,
                    memoryMb ? `${memoryMb} MB` : null,
                    diskMb ? `${Number(diskMb) / 1024} GB disk` : null,
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </dd>
              </>
            ) : null}
            <dt className="text-white/45 light:text-black/45">Created</dt>
            <dd className="text-white/85 light:text-black/85">
              {inst.createdAt ? new Date(inst.createdAt).toLocaleString() : ''}
            </dd>
            <dt className="text-white/45 light:text-black/45">
              Compute billing
            </dt>
            <dd
              className={
                inst.billingStartedAt && !inst.billingEndedAt
                  ? 'text-brand-secondary-200'
                  : 'text-white/70 light:text-black/70'
              }
            >
              {inst.billingStartedAt
                ? inst.billingEndedAt
                  ? 'Finalizing · compute stopped'
                  : 'Accruing · allocated VM'
                : 'Not accruing'}
            </dd>
            {inst.billingStartedAt && (
              <>
                <dt className="text-white/45 light:text-black/45">
                  Current window
                </dt>
                <dd className="text-white/85 light:text-black/85">
                  {formatDuration(inst.currentComputeSeconds)} ·{' '}
                  {formatUsd(inst.currentComputeCostUsd)}
                </dd>
                <dt className="text-white/45 light:text-black/45">
                  {inst.billingEndedAt ? 'Window ended' : 'Window opened'}
                </dt>
                <dd className="text-white/85 light:text-black/85">
                  {new Date(
                    inst.billingEndedAt ?? inst.billingStartedAt,
                  ).toLocaleString()}
                </dd>
              </>
            )}
            {inst.lastUsedAt && (
              <>
                <dt className="text-white/45 light:text-black/45">
                  Last activity
                </dt>
                <dd className="text-white/85 light:text-black/85">
                  {new Date(inst.lastUsedAt).toLocaleString()}
                </dd>
              </>
            )}
            <dt className="text-white/45 light:text-black/45">Auto-stop</dt>
            <dd className="text-white/85 light:text-black/85">
              {formatMinutes(inst.autoStopInterval)}
            </dd>
            <dt className="text-white/45 light:text-black/45">Auto-archive</dt>
            <dd className="text-white/85 light:text-black/85">
              {formatMinutes(inst.autoArchiveInterval)}
            </dd>
            <dt className="text-white/45 light:text-black/45">Auto-delete</dt>
            <dd className="text-white/85 light:text-black/85">
              {formatMinutes(inst.autoDeleteInterval)}
            </dd>
            {Object.keys(inst.labels ?? {}).length > 0 && (
              <>
                <dt className="text-white/45 light:text-black/45">Labels</dt>
                <dd className="flex flex-wrap gap-1">
                  {Object.entries(inst.labels ?? {}).map(([k, v]) => (
                    <span
                      key={k}
                      className="px-1.5 py-0.5 rounded bg-brand-main-700/60 text-white/70 light:text-black/70 text-[11px] font-mono"
                    >
                      {k}={v}
                    </span>
                  ))}
                </dd>
              </>
            )}
          </dl>
          <p className="mt-4 border-t border-brand-main-700 pt-3 text-xs leading-relaxed text-white/55 light:text-black/60">
            Compute accrues per second while the VM is allocated, including idle
            time. It stops when the sandbox sleeps, stops, or is destroyed;
            retained storage is metered separately.
          </p>
        </section>

        {/* Recent events */}
        <section className="rounded border border-brand-main-700 bg-brand-main-900/50 p-4">
          <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-3">
            Recent events
          </h3>
          {!eventsData?.events?.length ? (
            <p className="text-xs text-white/40 light:text-black/40">
              No events recorded yet.
            </p>
          ) : (
            <ul className="space-y-2">
              {eventsData.events.map((evt) => (
                <li key={evt.id} className="flex items-start gap-2 text-xs">
                  <Iconify.Icon
                    icon="heroicons:bolt"
                    className="size-3.5 text-brand-secondary-400/70 mt-0.5 shrink-0"
                  />
                  <div className="min-w-0">
                    <span className="text-white/80 light:text-black/80">
                      {evt.eventType ?? evt.message}
                    </span>
                    {evt.message && evt.eventType && (
                      <span className="text-white/45 light:text-black/45">
                        {' '}
                        · {evt.message}
                      </span>
                    )}
                    <div className="text-white/30 light:text-black/30">
                      {evt.createdAt
                        ? new Date(evt.createdAt).toLocaleString()
                        : ''}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  )
}
