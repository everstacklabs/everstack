import { useEffect, useMemo, useState } from 'react'
import {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { estimateDiskHourlyUsd, resolveSandboxPricing } from '@/lib/sandbox-pricing'
import { DeploymentTimeNote } from './deployment-time-note'
import { type FeaturesConfig } from './types'

interface SandboxFormProps {
  config: FeaturesConfig
  onChange: (config: FeaturesConfig) => void
  isSelfHosted?: boolean
}

type BillingUnit = 'gib_hour' | 'gib_second'

function formatCurrency(amount: number, currency: string): string {
  const decimals = Math.abs(amount) >= 1 ? 2 : 4
  if (currency === 'USD') {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    }).format(amount)
  }
  return `${currency} ${new Intl.NumberFormat('en-US', {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(amount)}`
}

function formatNumber(value: number, decimals = 2): string {
  if (!Number.isFinite(value)) return '0'
  return value.toFixed(decimals)
}

function formatRate(amount: number, currency: string, decimals = 4): string {
  if (currency === 'USD') {
    return `$${amount.toFixed(decimals)}`
  }
  return `${currency} ${amount.toFixed(decimals)}`
}

function withCurrentOption(options: number[], current: number): number[] {
  if (!Number.isFinite(current)) return options
  if (options.includes(current)) return options
  return [...options, current].sort((a, b) => a - b)
}

function SectionHeader({
  title,
  description,
  open,
  onToggle,
}: {
  title: string
  description: string
  open: boolean
  onToggle: () => void
}) {
  return (
    <CollapsibleTrigger
      onClick={onToggle}
      className="flex items-center justify-between w-full py-2.5 group"
    >
      <div className="text-left">
        <h4 className="text-sm font-semibold text-white light:text-brand-main-50">{title}</h4>
        <p className="text-xs text-brand-main-200 mt-0.5">{description}</p>
      </div>
      <Iconify.Icon
        icon="mdi:chevron-down"
        className={`h-5 w-5 text-brand-main-300 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
      />
    </CollapsibleTrigger>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      {children}
      {hint ? (
        <p className="text-[11px] text-brand-main-300 mt-1.5">{hint}</p>
      ) : (
        <div className="h-[18px]" />
      )}
    </div>
  )
}

function SwitchField({
  label,
  hint,
  checked,
  onCheckedChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex flex-col">
      <Label className="text-brand-main-300 text-xs mb-1.5">{label}</Label>
      <div className="flex items-center justify-between border border-brand-main-600/50 rounded-md px-3 h-9">
        {hint && <p className="text-[11px] text-brand-main-300">{hint}</p>}
        {!hint && <span />}
        <Switch checked={checked} onCheckedChange={onCheckedChange} />
      </div>
      <div className="h-[18px]" />
    </div>
  )
}

export function SandboxForm({
  config,
  onChange,
  isSelfHosted = true,
}: SandboxFormProps) {
  const [billingUnit, setBillingUnit] = useState<BillingUnit>('gib_hour')
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    general: true,
    backend: true,
    network: false,
    portExposure: false,
    estimateBreakdown: false,
    additionalSetup: false,
  })

  const toggle = (key: string) =>
    setOpenSections((prev) => ({ ...prev, [key]: !prev[key] }))

  const updateConfig = (updates: Partial<FeaturesConfig>) => {
    onChange({ ...config, ...updates })
  }

  const updateSandbox = (
    updates: Partial<NonNullable<FeaturesConfig['sandbox']>>,
  ) => {
    updateConfig({ sandbox: { ...config.sandbox, ...updates } })
  }

  const updateSandboxNested = (
    key: 'docker' | 'kubernetes' | 'ssh' | 'portExposure' | 'firecracker',
    updates: Record<string, unknown>,
  ) => {
    const current =
      (config.sandbox?.[key] as Record<string, unknown> | undefined) ?? {}
    updateSandbox({ [key]: { ...current, ...updates } } as Partial<
      NonNullable<FeaturesConfig['sandbox']>
    >)
  }

  const updateSandboxPortExposureNested = (
    key: 'tls' | 'cors',
    updates: Record<string, unknown>,
  ) => {
    const current =
      (config.sandbox?.portExposure?.[key] as
        | Record<string, unknown>
        | undefined) ?? {}
    updateSandboxNested('portExposure', { [key]: { ...current, ...updates } })
  }

  const parseInteger = (value: string, fallback: number) => {
    const n = Number.parseInt(value, 10)
    return Number.isFinite(n) ? n : fallback
  }

  const parseCsv = (value: string) => {
    return value
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
  }

  const backend = config.sandbox?.backend ?? 'kubernetes'
  const isCloudManaged = !isSelfHosted
  const maxCpu = config.sandbox?.maxCpu ?? 4
  const maxMemoryMb = config.sandbox?.maxMemoryMb ?? 4096
  const maxDiskMb = config.sandbox?.maxDiskMb ?? 4096
  const maxTimeoutSeconds = config.sandbox?.maxTimeoutSeconds ?? 3600

  const cpuOptions = useMemo(
    () => withCurrentOption([0.25, 0.5, 1, 2, 4, 8, 16], maxCpu),
    [maxCpu],
  )
  const memoryOptions = useMemo(
    () =>
      withCurrentOption(
        [256, 512, 1024, 2048, 4096, 8192, 16384, 32768],
        maxMemoryMb,
      ),
    [maxMemoryMb],
  )
  const diskOptions = useMemo(
    () =>
      withCurrentOption([512, 1024, 2048, 4096, 8192, 16384, 32768], maxDiskMb),
    [maxDiskMb],
  )
  const timeoutOptions = useMemo(
    () =>
      withCurrentOption(
        [60, 120, 300, 600, 1800, 3600, 7200, 21600],
        maxTimeoutSeconds,
      ),
    [maxTimeoutSeconds],
  )

  const sandboxPricing = useMemo(() => resolveSandboxPricing(config), [config])

  const estimatedPricing = useMemo(() => {
    const cpu = Math.max(0, maxCpu)
    const memoryGb = Math.max(0, maxMemoryMb / 1024)
    const diskGb = Math.max(0, maxDiskMb / 1024)

    const cpuHourlyRaw = cpu * sandboxPricing.cpuPerHourUsd
    const memoryHourlyRaw = memoryGb * sandboxPricing.memoryGbPerHourUsd
    // Tiered: first includedDiskGib free, +25% beyond the tier-2 threshold.
    const diskHourlyRaw = estimateDiskHourlyUsd(diskGb, sandboxPricing)
    const platformHourlyRaw = sandboxPricing.platformFeePerHourUsd
    const subtotalHourlyRaw =
      cpuHourlyRaw + memoryHourlyRaw + diskHourlyRaw + platformHourlyRaw

    const hourly = sandboxPricing.enabled ? subtotalHourlyRaw : 0
    const daily = hourly * 24
    const monthly = daily * 30

    return {
      hourly,
      daily,
      monthly,
      breakdown: {
        cpu,
        memoryGb,
        diskGb,
        memoryGiBSecondsPerHour: memoryGb * 3600,
        cpuHourlyRaw,
        memoryHourlyRaw,
        diskHourlyRaw,
        platformHourlyRaw,
        subtotalHourlyRaw,
      },
    }
  }, [maxCpu, maxDiskMb, maxMemoryMb, sandboxPricing])

  const isGiBSecondUnit = billingUnit === 'gib_second'
  const usageUnitLabel = isGiBSecondUnit ? 'GiB-sec/hr' : 'GiB-hr/hr'
  const rateUnitLabel = isGiBSecondUnit ? '/GiB-sec' : '/GiB-hr'
  const rateDecimals = isGiBSecondUnit ? 8 : 4
  const memoryUsageDisplay = isGiBSecondUnit
    ? estimatedPricing.breakdown.memoryGiBSecondsPerHour
    : estimatedPricing.breakdown.memoryGb
  const diskUsageDisplay = isGiBSecondUnit
    ? estimatedPricing.breakdown.diskGb * 3600
    : estimatedPricing.breakdown.diskGb
  const memoryRateDisplay = isGiBSecondUnit
    ? sandboxPricing.memoryGbPerHourUsd / 3600
    : sandboxPricing.memoryGbPerHourUsd
  const diskRateDisplay = isGiBSecondUnit
    ? sandboxPricing.diskGbPerHourUsd / 3600
    : sandboxPricing.diskGbPerHourUsd

  useEffect(() => {
    // Cloud-managed instances should not run Docker sandboxes.
    if (isCloudManaged && backend === 'docker') {
      updateSandbox({ backend: 'kubernetes' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isCloudManaged, backend])

  return (
    <div>
      <DeploymentTimeNote>
        Resource caps (max CPU, memory, disk, timeout) take effect per
        tenant. Backend selection, network defaults, and host paths are
        set at deployment time and won&rsquo;t change for a single tenant
        on a shared gateway.
      </DeploymentTimeNote>
      {/* Enable Toggle — always visible */}
      <div className="flex items-center justify-between py-2.5">
        <div>
          <span className="text-sm text-white light:text-brand-main-50">Enable Sandbox</span>
          <p className="text-xs text-brand-main-200 mt-0.5">
            Run agent code in isolated sandbox environments
          </p>
        </div>
        <Switch
          checked={config.sandbox?.enabled ?? false}
          onCheckedChange={(checked) => updateSandbox({ enabled: checked })}
        />
      </div>

      {/* ── General ── */}
      <Collapsible open={openSections.general} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="General"
            description="Backend selection, default image, and resource limits"
            open={!!openSections.general}
            onToggle={() => toggle('general')}
          />
        </div>
        <CollapsibleContent>
          <div className="pb-4 space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <Field
                label="Backend"
                hint={
                  isSelfHosted
                    ? 'Container runtime to use for sandboxes'
                    : 'Cloud instances support Kubernetes and isolated sandbox backends'
                }
              >
                <Select
                  value={backend}
                  onValueChange={(value) => updateSandbox({ backend: value })}
                >
                  <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {isSelfHosted && (
                      <SelectItem value="docker">Docker</SelectItem>
                    )}
                    <SelectItem value="kubernetes">Kubernetes</SelectItem>
                    <SelectItem value="firecracker">Isolated Sandbox</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Default Image"
                hint="Base container image for new sandboxes"
              >
                <Input
                  value={config.sandbox?.defaultImage ?? ''}
                  onChange={(e) =>
                    updateSandbox({ defaultImage: e.target.value })
                  }
                  placeholder="everstack/sandbox:base"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>

            <div className="grid grid-cols-4 gap-4">
              <Field label="Max CPU" hint="vCPU cores">
                <Select
                  value={String(maxCpu)}
                  onValueChange={(value) =>
                    updateSandbox({ maxCpu: Number.parseFloat(value) })
                  }
                >
                  <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {cpuOptions.map((cpu) => (
                      <SelectItem key={cpu} value={String(cpu)}>
                        {cpu} vCPU
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Max Memory" hint="Megabytes">
                <Select
                  value={String(maxMemoryMb)}
                  onValueChange={(value) =>
                    updateSandbox({ maxMemoryMb: parseInteger(value, 4096) })
                  }
                >
                  <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {memoryOptions.map((memory) => (
                      <SelectItem key={memory} value={String(memory)}>
                        {memory >= 1024
                          ? `${memory / 1024} GB (${memory} MB)`
                          : `${memory} MB`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Max Disk" hint="Megabytes">
                <Select
                  value={String(maxDiskMb)}
                  onValueChange={(value) =>
                    updateSandbox({ maxDiskMb: parseInteger(value, 4096) })
                  }
                >
                  <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {diskOptions.map((disk) => (
                      <SelectItem key={disk} value={String(disk)}>
                        {disk >= 1024
                          ? `${disk / 1024} GB (${disk} MB)`
                          : `${disk} MB`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Max Timeout" hint="Seconds">
                <Select
                  value={String(maxTimeoutSeconds)}
                  onValueChange={(value) =>
                    updateSandbox({
                      maxTimeoutSeconds: parseInteger(value, 3600),
                    })
                  }
                >
                  <SelectTrigger className="w-full bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {timeoutOptions.map((timeout) => (
                      <SelectItem key={timeout} value={String(timeout)}>
                        {timeout >= 3600
                          ? `${timeout / 3600}h (${timeout}s)`
                          : `${timeout}s`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>

            <div className="rounded-md border border-brand-secondary-500/40 bg-gradient-to-br from-brand-main-900/70 via-brand-main-800/40 to-brand-secondary-900/10 p-3">
              <div className="flex items-center justify-between gap-3">
                <p className="text-xs font-semibold text-brand-secondary-200">
                  Estimated cost at configured max sandbox size
                </p>
                <p className="text-[11px] text-brand-secondary-100/80">
                  Per active sandbox runtime
                </p>
              </div>
              <p className="mt-1 text-[11px] text-brand-main-200">
                Based on max CPU, memory, disk, and platform runtime rate.
              </p>
              <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
                <div className="flex items-center justify-between gap-3 rounded border border-brand-main-600/35 bg-brand-main-900/20 px-2.5 py-2">
                  <p className="text-[11px] text-brand-main-200">
                    Base hourly estimate
                  </p>
                  <p className="text-sm font-semibold text-emerald-200 light:text-emerald-600">
                    {formatCurrency(
                      estimatedPricing.hourly,
                      sandboxPricing.currency,
                    )}
                  </p>
                </div>
                <div className="flex items-center justify-between gap-3 rounded border border-brand-main-600/35 bg-brand-main-900/20 px-2.5 py-2">
                  <p className="text-[11px] text-brand-main-200">
                    Est. monthly total (30 days)
                  </p>
                  <p className="text-sm font-semibold text-sky-200 light:text-sky-700">
                    {formatCurrency(
                      estimatedPricing.monthly,
                      sandboxPricing.currency,
                    )}
                  </p>
                </div>
              </div>
              <div className="mt-3 rounded border border-brand-main-600/40 bg-brand-main-900/30 p-2.5">
                <button
                  type="button"
                  onClick={() => toggle('estimateBreakdown')}
                  className="flex w-full items-center justify-between text-left"
                >
                  <div className="flex w-full items-center justify-between gap-3">
                    <p className="text-[11px] font-semibold text-brand-main-100">
                      Additional pricing options
                    </p>
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] uppercase tracking-wide text-brand-main-300">
                        Units
                      </span>
                      <Select
                        value={billingUnit}
                        onValueChange={(value) =>
                          setBillingUnit(value as BillingUnit)
                        }
                      >
                        <SelectTrigger
                          className="h-7 w-[120px] bg-brand-main-800/70 border-brand-main-600 text-brand-main-100"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="gib_hour">GiB-hr</SelectItem>
                          <SelectItem value="gib_second">GiB-sec</SelectItem>
                        </SelectContent>
                      </Select>
                      <Iconify.Icon
                        icon="mdi:chevron-down"
                        className={`h-4 w-4 text-brand-main-300 transition-transform ${
                          openSections.estimateBreakdown ? 'rotate-180' : ''
                        }`}
                      />
                    </div>
                  </div>
                </button>
                <Collapsible open={openSections.estimateBreakdown}>
                  <CollapsibleContent>
                    <div className="mt-2 space-y-3">
                      <div className="rounded border border-brand-main-600/35 bg-brand-main-800/20 p-2">
                        <p className="text-[10px] uppercase tracking-wide text-brand-main-300">
                          Window Totals
                        </p>
                        <div className="mt-1 grid grid-cols-3 gap-2">
                          <div className="rounded border border-emerald-500/30 bg-emerald-500/10 px-2 py-1.5">
                            <p className="text-[10px] text-emerald-200 light:text-emerald-600">
                              Hourly
                            </p>
                            <p className="text-xs font-semibold text-emerald-100 light:text-emerald-900">
                              {formatCurrency(
                                estimatedPricing.hourly,
                                sandboxPricing.currency,
                              )}
                            </p>
                          </div>
                          <div className="rounded border border-sky-500/30 bg-sky-500/10 px-2 py-1.5">
                            <p className="text-[10px] text-sky-200 light:text-sky-700">24 Hours</p>
                            <p className="text-xs font-semibold text-sky-100 light:text-sky-900">
                              {formatCurrency(
                                estimatedPricing.daily,
                                sandboxPricing.currency,
                              )}
                            </p>
                          </div>
                          <div className="rounded border border-violet-500/30 bg-violet-500/10 px-2 py-1.5">
                            <p className="text-[10px] text-violet-200 light:text-violet-600">
                              30 Days
                            </p>
                            <p className="text-xs font-semibold text-violet-100 light:text-violet-900">
                              {formatCurrency(
                                estimatedPricing.monthly,
                                sandboxPricing.currency,
                              )}
                            </p>
                          </div>
                        </div>
                      </div>

                      <div className="space-y-1.5">
                        <div className="grid grid-cols-4 gap-2 text-[10px] uppercase tracking-wide text-brand-main-300">
                          <p>Component</p>
                          <p>Usage unit</p>
                          <p>Rate</p>
                          <p>Cost / hour</p>
                        </div>
                        <div className="grid grid-cols-4 gap-2 text-[11px] text-brand-main-100">
                          <p>CPU</p>
                          <p>
                            {formatNumber(estimatedPricing.breakdown.cpu)}{' '}
                            vCPU-hr/hr
                          </p>
                          <p>
                            {formatCurrency(
                              sandboxPricing.cpuPerHourUsd,
                              sandboxPricing.currency,
                            )}
                            /vCPU-hr
                          </p>
                          <p>
                            {formatCurrency(
                              estimatedPricing.breakdown.cpuHourlyRaw,
                              sandboxPricing.currency,
                            )}
                          </p>
                        </div>
                        <div className="grid grid-cols-4 gap-2 text-[11px] text-brand-main-100">
                          <p>Memory</p>
                          <p>
                            {formatNumber(memoryUsageDisplay)} {usageUnitLabel}
                          </p>
                          <p>
                            {formatRate(
                              memoryRateDisplay,
                              sandboxPricing.currency,
                              rateDecimals,
                            )}
                            {rateUnitLabel}
                          </p>
                          <p>
                            {formatCurrency(
                              estimatedPricing.breakdown.memoryHourlyRaw,
                              sandboxPricing.currency,
                            )}
                          </p>
                        </div>
                        <div className="grid grid-cols-4 gap-2 text-[11px] text-brand-main-100">
                          <p>Disk</p>
                          <p>
                            {formatNumber(diskUsageDisplay)} {usageUnitLabel}
                          </p>
                          <p>
                            {formatRate(
                              diskRateDisplay,
                              sandboxPricing.currency,
                              rateDecimals,
                            )}
                            {rateUnitLabel}
                          </p>
                          <p>
                            {formatCurrency(
                              estimatedPricing.breakdown.diskHourlyRaw,
                              sandboxPricing.currency,
                            )}
                          </p>
                        </div>
                        <div className="grid grid-cols-4 gap-2 text-[11px] text-brand-main-100">
                          <p>Platform</p>
                          <p>1 sandbox-hr/hr</p>
                          <p>
                            {formatCurrency(
                              sandboxPricing.platformFeePerHourUsd,
                              sandboxPricing.currency,
                            )}
                            /sandbox-hr
                          </p>
                          <p>
                            {formatCurrency(
                              estimatedPricing.breakdown.platformHourlyRaw,
                              sandboxPricing.currency,
                            )}
                          </p>
                        </div>
                        <div className="grid grid-cols-4 gap-2 border-t border-brand-main-600/40 pt-1.5 text-[11px] font-semibold text-white light:text-brand-main-50">
                          <p>Total</p>
                          <p>
                            {formatNumber(
                              isGiBSecondUnit
                                ? estimatedPricing.breakdown
                                    .memoryGiBSecondsPerHour
                                : estimatedPricing.breakdown.memoryGb,
                            )}{' '}
                            {usageUnitLabel}
                          </p>
                          <p>-</p>
                          <p>
                            {formatCurrency(
                              sandboxPricing.enabled
                                ? estimatedPricing.breakdown.subtotalHourlyRaw
                                : 0,
                              sandboxPricing.currency,
                            )}
                          </p>
                        </div>
                      </div>
                    </div>
                  </CollapsibleContent>
                </Collapsible>
              </div>
              {!sandboxPricing.enabled && (
                <p className="mt-2 text-[11px] text-amber-300 light:text-amber-700">
                  Internal pricing is currently disabled, so estimates resolve
                  to zero.
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-4">
              <button
                type="button"
                onClick={() => toggle('additionalSetup')}
                className="col-span-2 flex items-center justify-between rounded border border-brand-main-600/40 bg-brand-main-900/30 px-3 py-2 text-left"
              >
                <div>
                  <p className="text-xs font-semibold text-brand-main-100">
                    Additional setup options
                  </p>
                  <p className="text-[11px] text-brand-main-300">
                    Optional allowlists and warm pool tuning
                  </p>
                </div>
                <Iconify.Icon
                  icon="mdi:chevron-down"
                  className={`h-4 w-4 text-brand-main-300 transition-transform ${
                    openSections.additionalSetup ? 'rotate-180' : ''
                  }`}
                />
              </button>
            </div>

            <Collapsible open={openSections.additionalSetup}>
              <CollapsibleContent>
                <div className="grid grid-cols-2 gap-4">
                  <Field
                    label="Allowed Images"
                    hint="Comma-separated list of allowed container images"
                  >
                    <Input
                      value={(config.sandbox?.allowedImages ?? []).join(', ')}
                      onChange={(e) =>
                        updateSandbox({
                          allowedImages: parseCsv(e.target.value),
                        })
                      }
                      placeholder="everstack/sandbox:base, ghcr.io/everstacklabs/runtime:*"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field
                    label="DNS Servers"
                    hint="Comma-separated DNS resolver addresses"
                  >
                    <Input
                      value={(config.sandbox?.dnsServers ?? []).join(', ')}
                      onChange={(e) =>
                        updateSandbox({ dnsServers: parseCsv(e.target.value) })
                      }
                      placeholder="1.1.1.1, 8.8.8.8"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>

                <div className="mt-4 grid grid-cols-2 gap-4">
                  <Field
                    label="Keep-Warm Idle"
                    hint="Seconds to keep idle sandboxes warm (0 = disabled)"
                  >
                    <Input
                      type="number"
                      value={config.sandbox?.defaultKeepWarmIdleSeconds ?? 0}
                      onChange={(e) =>
                        updateSandbox({
                          defaultKeepWarmIdleSeconds: parseInteger(
                            e.target.value,
                            0,
                          ),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Max Concurrent Creates" hint="0 = unlimited">
                    <Input
                      type="number"
                      value={config.sandbox?.maxConcurrentCreates ?? 0}
                      onChange={(e) =>
                        updateSandbox({
                          maxConcurrentCreates: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              </CollapsibleContent>
            </Collapsible>
          </div>
        </CollapsibleContent>
      </Collapsible>

      {/* ── Backend-specific ── */}
      <Collapsible open={openSections.backend} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title={`${backend.charAt(0).toUpperCase() + backend.slice(1)} Backend`}
            description={
              backend === 'docker'
                ? 'Docker daemon connection and image management'
                : backend === 'kubernetes'
                  ? 'Kubernetes namespace, auth, and scheduling'
                  : 'Isolated sandbox paths and pool sizing'
            }
            open={!!openSections.backend}
            onToggle={() => toggle('backend')}
          />
        </div>
        <CollapsibleContent>
          <div className="pb-4 space-y-4">
            {backend === 'docker' && (
              <>
                <Field
                  label="Docker Host"
                  hint="Docker daemon socket or TCP address"
                >
                  <Input
                    value={config.sandbox?.docker?.host ?? ''}
                    onChange={(e) =>
                      updateSandboxNested('docker', { host: e.target.value })
                    }
                    placeholder="unix:///var/run/docker.sock"
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
                <div className="grid grid-cols-2 gap-4">
                  <SwitchField
                    label="Auto Pull"
                    hint="Pull images automatically"
                    checked={config.sandbox?.docker?.autoPull ?? true}
                    onCheckedChange={(checked) =>
                      updateSandboxNested('docker', { autoPull: checked })
                    }
                  />
                  <SwitchField
                    label="Auto Build"
                    hint="Build images if missing"
                    checked={config.sandbox?.docker?.autoBuild ?? true}
                    onCheckedChange={(checked) =>
                      updateSandboxNested('docker', { autoBuild: checked })
                    }
                  />
                </div>
              </>
            )}

            {backend === 'kubernetes' && (
              <>
                <div className="grid grid-cols-2 gap-4">
                  <Field
                    label="Namespace"
                    hint="Kubernetes namespace for sandbox pods"
                  >
                    <Input
                      value={config.sandbox?.kubernetes?.namespace ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('kubernetes', {
                          namespace: e.target.value,
                        })
                      }
                      placeholder="everstack-sandboxes"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Image Pull Policy">
                    <Input
                      value={config.sandbox?.kubernetes?.imagePullPolicy ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('kubernetes', {
                          imagePullPolicy: e.target.value,
                        })
                      }
                      placeholder="IfNotPresent"
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Service Account">
                    <Input
                      value={config.sandbox?.kubernetes?.serviceAccount ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('kubernetes', {
                          serviceAccount: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field
                    label="Kubeconfig"
                    hint="Path to kubeconfig file (blank = in-cluster)"
                  >
                    <Input
                      value={config.sandbox?.kubernetes?.kubeconfig ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('kubernetes', {
                          kubeconfig: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
                <Field
                  label="Node Selector"
                  hint="key=value pairs, comma-separated"
                >
                  <Input
                    value={Object.entries(
                      config.sandbox?.kubernetes?.nodeSelector ?? {},
                    )
                      .map(([k, v]) => `${k}=${v}`)
                      .join(', ')}
                    onChange={(e) => {
                      const entries = parseCsv(e.target.value)
                      const nodeSelector = entries.reduce<
                        Record<string, string>
                      >((acc, item) => {
                        const [k, ...rest] = item.split('=')
                        if (!k) return acc
                        const key = k.trim()
                        const value = rest.join('=').trim()
                        if (key && value) acc[key] = value
                        return acc
                      }, {})
                      updateSandboxNested('kubernetes', { nodeSelector })
                    }}
                    placeholder="kubernetes.io/os=linux, node-type=general"
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                  />
                </Field>
              </>
            )}

            {backend === 'firecracker' && (
              <>
                <div className="grid grid-cols-2 gap-4">
                  <Field
                    label="Binary Path"
                    hint="Path to the sandbox runtime binary"
                  >
                    <Input
                      value={config.sandbox?.firecracker?.binaryPath ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          binaryPath: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field
                    label="Kernel Path"
                    hint="Path to the guest kernel image"
                  >
                    <Input
                      value={config.sandbox?.firecracker?.kernelPath ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          kernelPath: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Rootfs Directory">
                    <Input
                      value={config.sandbox?.firecracker?.rootfsDir ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          rootfsDir: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Work Directory">
                    <Input
                      value={config.sandbox?.firecracker?.workDir ?? ''}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          workDir: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>

                <h5 className="text-xs font-medium text-brand-main-100 pt-2">
                  Pool Configuration
                </h5>
                <div className="grid grid-cols-4 gap-4">
                  <Field label="Min Size">
                    <Input
                      type="number"
                      value={config.sandbox?.firecracker?.poolMinSize ?? 0}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          poolMinSize: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Max Size">
                    <Input
                      type="number"
                      value={config.sandbox?.firecracker?.poolMaxSize ?? 0}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          poolMaxSize: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Max Total">
                    <Input
                      type="number"
                      value={config.sandbox?.firecracker?.poolMaxTotal ?? 0}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          poolMaxTotal: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Replenish Batch">
                    <Input
                      type="number"
                      value={config.sandbox?.firecracker?.replenishBatch ?? 0}
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          replenishBatch: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Replenish Interval" hint="Milliseconds">
                    <Input
                      type="number"
                      value={
                        config.sandbox?.firecracker?.replenishIntervalMs ?? 0
                      }
                      onChange={(e) =>
                        updateSandboxNested('firecracker', {
                          replenishIntervalMs: parseInteger(e.target.value, 0),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <SwitchField
                    label="Warmup On Start"
                    hint="Pre-create VMs at startup"
                    checked={
                      config.sandbox?.firecracker?.warmupOnStart ?? false
                    }
                    onCheckedChange={(checked) =>
                      updateSandboxNested('firecracker', {
                        warmupOnStart: checked,
                      })
                    }
                  />
                </div>
              </>
            )}
          </div>
        </CollapsibleContent>
      </Collapsible>

      {/* ── SSH ── */}
      <Collapsible open={openSections.network} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="SSH Access"
            description="SSH connectivity for sandbox instances"
            open={!!openSections.network}
            onToggle={() => toggle('network')}
          />
        </div>
        <CollapsibleContent>
          <div className="pb-4 space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <Field
                label="Listen Address"
                hint="Address the SSH server binds to"
              >
                <Input
                  value={config.sandbox?.ssh?.listenAddr ?? ''}
                  onChange={(e) =>
                    updateSandboxNested('ssh', { listenAddr: e.target.value })
                  }
                  placeholder=":2222"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Host" hint="External hostname for SSH connections">
                <Input
                  value={config.sandbox?.ssh?.host ?? ''}
                  onChange={(e) =>
                    updateSandboxNested('ssh', { host: e.target.value })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>
          </div>
        </CollapsibleContent>
      </Collapsible>

      {/* ── Port Exposure ── */}
      <Collapsible open={openSections.portExposure} className="w-full">
        <div className="border-t border-brand-main-600/30">
          <SectionHeader
            title="Port Exposure"
            description="Expose sandbox ports via a reverse proxy with optional TLS and CORS"
            open={!!openSections.portExposure}
            onToggle={() => toggle('portExposure')}
          />
        </div>
        <CollapsibleContent>
          <div className="pb-4 space-y-4">
            {/* Enable + core settings */}
            <SwitchField
              label="Enable Port Exposure"
              hint="Expose sandbox ports externally"
              checked={config.sandbox?.portExposure?.enabled ?? false}
              onCheckedChange={(checked) =>
                updateSandboxNested('portExposure', { enabled: checked })
              }
            />

            <SwitchField
              label="Require Signed Preview URLs"
              hint="Reject unsigned subdomain previews; users must open signed preview links"
              checked={
                config.sandbox?.portExposure?.requirePreviewToken ?? false
              }
              onCheckedChange={(checked) =>
                updateSandboxNested('portExposure', {
                  requirePreviewToken: checked,
                })
              }
            />

            <div className="grid grid-cols-3 gap-4">
              <Field
                label="Base Domain"
                hint="Wildcard domain for exposed ports"
              >
                <Input
                  value={config.sandbox?.portExposure?.baseDomain ?? ''}
                  onChange={(e) =>
                    updateSandboxNested('portExposure', {
                      baseDomain: e.target.value,
                    })
                  }
                  placeholder="*.sandbox.example.com"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Listen Address">
                <Input
                  value={config.sandbox?.portExposure?.listenAddr ?? ''}
                  onChange={(e) =>
                    updateSandboxNested('portExposure', {
                      listenAddr: e.target.value,
                    })
                  }
                  placeholder=":8443"
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Max Ports / Sandbox">
                <Input
                  type="number"
                  value={config.sandbox?.portExposure?.maxPortsPerSandbox ?? 5}
                  onChange={(e) =>
                    updateSandboxNested('portExposure', {
                      maxPortsPerSandbox: parseInteger(e.target.value, 5),
                    })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <Field label="Request Timeout" hint="Seconds">
                <Input
                  type="number"
                  value={
                    config.sandbox?.portExposure?.requestTimeoutSeconds ?? 120
                  }
                  onChange={(e) =>
                    updateSandboxNested('portExposure', {
                      requestTimeoutSeconds: parseInteger(e.target.value, 120),
                    })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
              <Field label="Max Request Body" hint="Megabytes">
                <Input
                  type="number"
                  value={config.sandbox?.portExposure?.maxRequestBodyMb ?? 50}
                  onChange={(e) =>
                    updateSandboxNested('portExposure', {
                      maxRequestBodyMb: parseInteger(e.target.value, 50),
                    })
                  }
                  className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
              </Field>
            </div>

            {/* TLS sub-section */}
            <div className="border-t border-brand-main-600/30 pt-4">
              <h5 className="text-xs font-medium text-brand-main-100 mb-3">
                TLS
              </h5>
              <div className="space-y-3">
                <div className="grid grid-cols-2 gap-4">
                  <SwitchField
                    label="TLS Enabled"
                    checked={
                      config.sandbox?.portExposure?.tls?.enabled ?? false
                    }
                    onCheckedChange={(checked) =>
                      updateSandboxPortExposureNested('tls', {
                        enabled: checked,
                      })
                    }
                  />
                  <SwitchField
                    label="Autocert"
                    hint="Auto-provision certificates"
                    checked={
                      config.sandbox?.portExposure?.tls?.autocert ?? false
                    }
                    onCheckedChange={(checked) =>
                      updateSandboxPortExposureNested('tls', {
                        autocert: checked,
                      })
                    }
                  />
                </div>
                <div className="grid grid-cols-3 gap-4">
                  <Field label="Cert Path">
                    <Input
                      value={config.sandbox?.portExposure?.tls?.certPath ?? ''}
                      onChange={(e) =>
                        updateSandboxPortExposureNested('tls', {
                          certPath: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Key Path">
                    <Input
                      value={config.sandbox?.portExposure?.tls?.keyPath ?? ''}
                      onChange={(e) =>
                        updateSandboxPortExposureNested('tls', {
                          keyPath: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Autocert Directory">
                    <Input
                      value={
                        config.sandbox?.portExposure?.tls?.autocertDir ?? ''
                      }
                      onChange={(e) =>
                        updateSandboxPortExposureNested('tls', {
                          autocertDir: e.target.value,
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              </div>
            </div>

            {/* CORS sub-section */}
            <div className="border-t border-brand-main-600/30 pt-4">
              <h5 className="text-xs font-medium text-brand-main-100 mb-3">
                CORS
              </h5>
              <div className="space-y-3">
                <SwitchField
                  label="CORS Enabled"
                  checked={config.sandbox?.portExposure?.cors?.enabled ?? true}
                  onCheckedChange={(checked) =>
                    updateSandboxPortExposureNested('cors', {
                      enabled: checked,
                    })
                  }
                />
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Allowed Origins" hint="Comma-separated">
                    <Input
                      value={(
                        config.sandbox?.portExposure?.cors?.allowedOrigins ?? []
                      ).join(', ')}
                      onChange={(e) =>
                        updateSandboxPortExposureNested('cors', {
                          allowedOrigins: parseCsv(e.target.value),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Allowed Methods" hint="Comma-separated">
                    <Input
                      value={(
                        config.sandbox?.portExposure?.cors?.allowedMethods ?? []
                      ).join(', ')}
                      onChange={(e) =>
                        updateSandboxPortExposureNested('cors', {
                          allowedMethods: parseCsv(e.target.value),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <Field label="Allowed Headers" hint="Comma-separated">
                    <Input
                      value={(
                        config.sandbox?.portExposure?.cors?.allowedHeaders ?? []
                      ).join(', ')}
                      onChange={(e) =>
                        updateSandboxPortExposureNested('cors', {
                          allowedHeaders: parseCsv(e.target.value),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                  <Field label="Max Age" hint="Seconds">
                    <Input
                      type="number"
                      value={
                        config.sandbox?.portExposure?.cors?.maxAgeSeconds ??
                        3600
                      }
                      onChange={(e) =>
                        updateSandboxPortExposureNested('cors', {
                          maxAgeSeconds: parseInteger(e.target.value, 3600),
                        })
                      }
                      className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    />
                  </Field>
                </div>
              </div>
            </div>
          </div>
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}
