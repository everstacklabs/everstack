import { useMemo } from 'react'
import {
  useSandboxInstances,
  useSandboxOverview,
} from '@/hooks/deployments/use-sandbox'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useNavigate } from '@tanstack/react-router'
import {
  isSandboxRunning,
  isSandboxStopped,
  sandboxStatusLabel,
} from './lifecycle'

function formatRelative(value?: string): string {
  if (!value) return 'No activity yet'
  const ts = new Date(value).getTime()
  if (!Number.isFinite(ts)) return 'No activity yet'
  const diff = Date.now() - ts
  if (diff < 60_000) return 'Just now'
  const minutes = Math.floor(diff / 60_000)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

function formatDuration(seconds: number): string {
  if (seconds <= 0) return '0s'
  if (seconds < 60) return `${Math.floor(seconds)}s`
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${Math.max(1, minutes)}m`
}

function formatUsd(usd: number): string {
  if (usd <= 0) return '$0.00'
  if (usd < 0.01) return '<$0.01'
  if (usd < 1) return `$${usd.toFixed(3)}`
  return `$${usd.toFixed(2)}`
}

export function OverviewTab() {
  const {
    data: overview,
    isLoading: overviewLoading,
    error,
  } = useSandboxOverview()
  const { data: instanceData, isLoading: instancesLoading } =
    useSandboxInstances()
  const navigate = useNavigate({ from: '/deployments/sandboxes/' })

  const instances = instanceData?.instances ?? []
  const running = instances.filter(isSandboxRunning)
  const sleeping = instances.filter(isSandboxStopped)
  const failed = instances.filter(
    (i) => sandboxStatusLabel(i) === 'failed' || i.status === 'failed',
  )
  const maxSandboxes = overview?.maxSandboxes ?? 0
  const hasFiniteCapacity = maxSandboxes > 0
  const capacityUsed = hasFiniteCapacity
    ? (running.length / maxSandboxes) * 100
    : 0
  const activeBillingWindows = instances.filter((instance) =>
    Boolean(instance.billingStartedAt && !instance.billingEndedAt),
  ).length
  const finalizingBillingWindows = instances.filter((instance) =>
    Boolean(instance.billingStartedAt && instance.billingEndedAt),
  ).length
  const lifetimeCostUsd = overview?.lifetimeCostUsd ?? 0

  const recent = useMemo(
    () =>
      [...instances]
        .sort(
          (a, b) =>
            new Date(b.lastUsedAt || b.createdAt).getTime() -
            new Date(a.lastUsedAt || a.createdAt).getTime(),
        )
        .slice(0, 5),
    [instances],
  )

  if (overviewLoading || instancesLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading sandbox overview..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
        Error loading overview: {error.message}
      </div>
    )
  }

  if (!overview) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center pb-24">
        <Iconify.Icon
          icon="heroicons:cube-transparent"
          className="size-8 text-brand-secondary-400 mb-4"
        />
        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
          Sandbox not available
        </h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
          Sandbox runtime is not available. Contact your administrator.
        </p>
      </div>
    )
  }

  return (
    <div className="p-4 space-y-5 overflow-y-auto">
      <div className="grid grid-cols-2 lg:grid-cols-6 gap-3">
        <SummaryTile
          label="Health"
          value={overview.healthy ? 'Healthy' : 'Needs attention'}
          tone={overview.healthy ? 'good' : 'bad'}
          subtitle={
            overview.healthy ? 'All systems normal' : 'Service degraded'
          }
        />
        <SummaryTile
          label="Running"
          value={`${running.length}`}
          subtitle={
            hasFiniteCapacity
              ? `${overview.maxSandboxes} concurrent slots`
              : 'Custom concurrency limit'
          }
        />
        <SummaryTile
          label="Sleeping"
          value={`${sleeping.length}`}
          subtitle="Snapshot retained, no compute billed"
          tone="quiet"
        />
        <SummaryTile
          label="Unsettled compute"
          value={formatDuration(overview.activeComputeSeconds)}
          subtitle={[
            `${activeBillingWindows} accruing`,
            finalizingBillingWindows > 0
              ? `${finalizingBillingWindows} finalizing`
              : '',
          ]
            .filter(Boolean)
            .join(' · ')}
          tone={activeBillingWindows > 0 ? 'warn' : 'quiet'}
        />
        <SummaryTile
          label="Compute used"
          value={formatDuration(overview.lifetimeComputeSeconds)}
          subtitle="Closed and currently open compute windows"
          tone={overview.lifetimeComputeSeconds > 0 ? 'warn' : 'quiet'}
        />
        <SummaryTile
          label="Estimated cost"
          value={formatUsd(lifetimeCostUsd)}
          subtitle={`${formatUsd(overview.activeCostUsd)} not yet ledgered`}
          tone={lifetimeCostUsd > 0 ? 'warn' : 'quiet'}
        />
      </div>

      {(!overview.healthy || capacityUsed >= 80 || failed.length > 0) && (
        <div className="rounded-md border border-brand-secondary-400/40 bg-brand-secondary-400/10 px-4 py-3 text-sm text-brand-secondary-200">
          {!overview.healthy && (
            <p>
              Sandbox service is unhealthy. New shells and lifecycle actions may
              fail.
            </p>
          )}
          {capacityUsed >= 80 && (
            <p>
              Sandbox capacity is almost full. Stop idle sandboxes to free room.
            </p>
          )}
          {failed.length > 0 && (
            <p>
              {failed.length} sandbox{failed.length === 1 ? '' : 'es'} need
              attention.
            </p>
          )}
        </div>
      )}

      <div className="rounded-md border border-brand-main-600 bg-brand-main-800/50 p-4 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-medium text-white/85 light:text-black/85">
              Capacity
            </h3>
            <p className="text-xs text-white/45 light:text-black/45">
              {overview.totalInstances} total sandboxes, {running.length}{' '}
              currently running
            </p>
          </div>
          <button
            onClick={() =>
              navigate({
                search: (prev) => ({ ...prev, tab: 'instances' as const }),
              })
            }
            className="inline-flex items-center gap-1.5 rounded border border-brand-main-600 px-2.5 py-1 text-xs text-brand-secondary-300 hover:bg-brand-main-700"
          >
            <Iconify.Icon icon="heroicons:server-stack" className="size-3.5" />
            Manage
          </button>
        </div>
        <div className="h-2 rounded bg-brand-main-700 overflow-hidden">
          <div
            className={`h-full ${capacityUsed >= 80 ? 'bg-brand-secondary-300' : 'bg-brand-secondary-500'}`}
            style={{
              width: hasFiniteCapacity
                ? `${Math.min(100, capacityUsed)}%`
                : '0%',
            }}
          />
        </div>
        <div className="flex items-center justify-between text-xs text-white/45 light:text-black/45">
          <span>
            {hasFiniteCapacity
              ? `${capacityUsed.toFixed(0)}% used`
              : 'Enterprise capacity'}
          </span>
          <span>
            {hasFiniteCapacity
              ? `${Math.max(0, overview.maxSandboxes - running.length)} slots available`
              : 'Custom limit'}
          </span>
        </div>
      </div>

      <div className="rounded-md border border-brand-main-600 bg-brand-main-800/50 p-4">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-medium text-white/85 light:text-black/85">
            Recent activity
          </h3>
          <button
            onClick={() =>
              navigate({
                search: (prev) => ({ ...prev, tab: 'instances' as const }),
              })
            }
            className="text-xs text-brand-secondary-300 hover:text-brand-secondary-200"
          >
            View sandboxes
          </button>
        </div>
        {recent.length === 0 ? (
          <p className="text-sm text-white/40 light:text-black/40">
            Sandboxes will appear here after an agent uses one.
          </p>
        ) : (
          <div className="divide-y divide-brand-main-700">
            {recent.map((inst) => (
              <div
                key={inst.id}
                className="flex items-center justify-between gap-4 py-2"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm text-white/80 light:text-black/80">
                    {inst.name?.trim() || inst.id}
                  </p>
                  <p className="truncate text-xs text-white/40 light:text-black/40">
                    {inst.image}
                  </p>
                </div>
                <div className="shrink-0 text-right">
                  <p className="text-xs text-white/60 light:text-black/60 capitalize">
                    {sandboxStatusLabel(inst)}
                  </p>
                  <p className="text-xs text-white/35 light:text-black/35">
                    {formatRelative(inst.lastUsedAt || inst.createdAt)}
                  </p>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function SummaryTile({
  label,
  value,
  subtitle,
  tone = 'neutral',
}: {
  label: string
  value: string
  subtitle?: string
  tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'quiet'
}) {
  // Status colors stay inside the brand palette so the page reads as one
  // product surface. Distinction is by weight/opacity, not hue.
  const toneClass =
    tone === 'good'
      ? 'text-brand-secondary-300'
      : tone === 'warn'
        ? 'text-brand-secondary-200'
        : tone === 'bad'
          ? 'text-brand-secondary-200 font-bold'
          : tone === 'quiet'
            ? 'text-white/55 light:text-black/55'
            : 'text-white light:text-brand-main-50'
  return (
    <div className="rounded-md border border-brand-main-600 bg-brand-main-800/50 p-3">
      <p className="text-xs text-white/45 light:text-black/45 mb-1">{label}</p>
      <p className={`text-xl font-semibold truncate ${toneClass}`}>{value}</p>
      {subtitle && (
        <p className="text-[11px] text-white/35 light:text-black/35 truncate">
          {subtitle}
        </p>
      )}
    </div>
  )
}
