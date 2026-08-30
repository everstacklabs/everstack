import { MonitorUp, Rows3 } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import type { InterfaceDensity } from '@/lib/interface-density'

type DensityOption = {
  value: InterfaceDensity
  label: string
  description: string
  helper: string
  icon: typeof MonitorUp
}

const DENSITY_OPTIONS: DensityOption[] = [
  {
    value: 'comfortable',
    label: 'Comfortable',
    description: 'Roomier controls and calmer scans.',
    helper: 'Setup and review',
    icon: MonitorUp,
  },
  {
    value: 'compact',
    label: 'Compact',
    description: 'Denser controls for daily operations.',
    helper: 'Tables and dashboards',
    icon: Rows3,
  },
]

function DensityPreview({ density }: { density: InterfaceDensity }) {
  const rows = density === 'compact' ? 4 : 3

  return (
    <div
      aria-hidden
      className={cn(
        'mt-2 h-[3.35rem] overflow-hidden rounded border border-brand-main-700 bg-brand-main-950/70 p-1.5 light:border-brand-main-700 light:bg-brand-main-950',
        density === 'compact' ? 'space-y-1' : 'space-y-1.5',
      )}
    >
      <div className="mb-1 flex items-center justify-between">
        <span className="h-1 w-9 rounded-full bg-brand-main-500 light:bg-brand-main-700" />
        <span className="h-1 w-4 rounded-full bg-brand-secondary-400/80" />
      </div>
      {Array.from({ length: rows }).map((_, index) => (
        <div
          key={index}
          className={cn(
            'grid items-center gap-1.5 rounded-sm border border-brand-main-800 bg-brand-main-900 light:border-brand-main-700 light:bg-brand-main-900',
            density === 'compact'
              ? 'grid-cols-[0.6rem_1fr_1rem] px-1.5 py-0.5'
              : 'grid-cols-[0.8rem_1fr_1.35rem] px-2 py-0.5',
          )}
        >
          <span
            className={cn(
              'rounded-full bg-brand-secondary-400/70',
              density === 'compact' ? 'size-1' : 'size-1.5',
            )}
          />
          <span className="h-1 rounded-full bg-brand-main-600 light:bg-brand-main-700" />
          <span className="h-1 rounded-full bg-brand-main-700 light:bg-brand-main-800" />
        </div>
      ))}
    </div>
  )
}

export function InterfaceDensityCards({
  value,
  onChange,
  className,
}: {
  value: InterfaceDensity | null | undefined
  onChange: (density: InterfaceDensity) => void
  className?: string
}) {
  return (
    <div
      className={cn(
        'grid w-full max-w-[32rem] grid-cols-1 gap-2 sm:grid-cols-2',
        className,
      )}
    >
      {DENSITY_OPTIONS.map((option) => {
        const Icon = option.icon
        const selected = value === option.value

        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={selected}
            onClick={() => onChange(option.value)}
            className={cn(
              'group flex h-[8.25rem] min-w-0 flex-col overflow-hidden rounded-md border p-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-brand-secondary-500',
              selected
                ? 'border-brand-secondary-400 bg-brand-secondary-500/10'
                : 'border-brand-main-600 bg-brand-main-800/50 hover:border-brand-main-500 hover:bg-brand-main-800/75',
              selected
                ? 'light:border-brand-secondary-300 light:bg-brand-secondary-100'
                : 'light:border-brand-main-700 light:bg-white light:hover:border-brand-main-500 light:hover:bg-brand-main-900',
            )}
          >
            <div className="flex min-h-10 items-start gap-2">
              <span
                className={cn(
                  'inline-flex size-7 shrink-0 items-center justify-center rounded border',
                  selected
                    ? 'border-brand-secondary-500/35 bg-brand-secondary-500/15 text-brand-secondary-200'
                    : 'border-brand-main-700 bg-brand-main-900 text-brand-main-200',
                  selected
                    ? 'light:border-brand-secondary-300 light:bg-brand-secondary-200 light:text-brand-secondary-900'
                    : 'light:border-brand-main-700 light:bg-brand-main-900 light:text-brand-main-100',
                )}
              >
                <Icon className="size-3.5" />
              </span>
              <span className="min-w-0">
                <span className="flex items-center gap-1.5 text-[13px] font-medium leading-4 text-white light:text-brand-main-50">
                  <span className="truncate">{option.label}</span>
                  <span
                    aria-hidden={!selected}
                    className={cn(
                      'inline-flex h-4 shrink-0 items-center rounded px-1.5 text-[10px] leading-none',
                      selected
                        ? 'bg-brand-secondary-500/15 text-brand-secondary-200 light:bg-brand-secondary-200 light:text-brand-secondary-900'
                        : 'bg-transparent text-transparent',
                    )}
                  >
                    Active
                  </span>
                </span>
                <span className="mt-0.5 block text-[11px] leading-4 text-white/55 light:text-black/65">
                  {option.description}
                </span>
              </span>
            </div>
            <DensityPreview density={option.value} />
            <p className="mt-auto pt-1.5 text-[10px] uppercase leading-4 tracking-wide text-white/40 light:text-black/50">
              {option.helper}
            </p>
          </button>
        )
      })}
    </div>
  )
}
