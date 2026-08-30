import plansConfig from '../../../../pkg/plans/plans.json'

export interface SandboxPricingConfig {
  enabled: boolean
  currency: string
  cpuPerHourUsd: number
  memoryGbPerHourUsd: number
  diskGbPerHourUsd: number
  platformFeePerHourUsd: number
  /** GiB of root disk included in the fixed-size compute rate. */
  includedDiskGib: number
  /** GiB above which the marginal storage rate rises. */
  diskTier2ThresholdGib: number
  /** Multiplier applied to the storage rate beyond the tier-2 threshold. */
  diskTier2Multiplier: number
  tierMultipliers: Record<string, number>
}

export interface SandboxMachineProfile {
  id: string
  label: string
  cpu: number
  memoryMb: number
  diskMb: number
}

const DEFAULT_TIER_MULTIPLIERS: Record<string, number> = {
  free: 1,
  basic: 1,
  pro: 0.93,
  enterprise: 0.88,
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function toNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  return fallback
}

function toBoolean(value: unknown, fallback: boolean): boolean {
  if (typeof value === 'boolean') return value
  return fallback
}

function parseTierMultipliers(value: unknown): Record<string, number> {
  const raw = asRecord(value)
  if (!raw) return DEFAULT_TIER_MULTIPLIERS

  const parsed: Record<string, number> = { ...DEFAULT_TIER_MULTIPLIERS }
  for (const [tier, multiplier] of Object.entries(raw)) {
    if (typeof multiplier === 'number' && Number.isFinite(multiplier)) {
      parsed[tier] = multiplier
    }
  }
  return parsed
}

const rawPlansPricing = (
  plansConfig as unknown as {
    sandbox_compute_pricing?: {
      currency?: string
      cpu_per_vcpu_hour?: number
      memory_per_gib_hour?: number
      disk_per_gib_hour?: number
      platform_per_sandbox_hour?: number
      included_disk_gib?: number
      disk_tier2_threshold_gib?: number
      disk_tier2_multiplier?: number
      sizes?: Array<{
        id?: string
        label?: string
        vcpu?: number
        memory_gib?: number
        disk_gib?: number
      }>
    }
  }
).sandbox_compute_pricing

const DEFAULT_MACHINE_PROFILES: SandboxMachineProfile[] = [
  { id: 'nano', label: 'Nano', cpu: 0.5, memoryMb: 512, diskMb: 20480 },
  { id: 'small', label: 'Small', cpu: 1, memoryMb: 1024, diskMb: 20480 },
  { id: 'medium', label: 'Medium', cpu: 2, memoryMb: 2048, diskMb: 20480 },
  { id: 'large', label: 'Large', cpu: 4, memoryMb: 4096, diskMb: 20480 },
  { id: 'xlarge', label: 'XL', cpu: 8, memoryMb: 8192, diskMb: 20480 },
]

function formatProfileLabel(profile: SandboxMachineProfile): string {
  const memory =
    profile.memoryMb < 1024
      ? `${profile.memoryMb} MB`
      : `${profile.memoryMb / 1024} GiB`
  return `${profile.label} (${profile.cpu} vCPU, ${memory}, ${profile.diskMb / 1024} GiB)`
}

const configuredMachineProfiles = rawPlansPricing?.sizes
  ?.filter(
    (size) =>
      typeof size.id === 'string' &&
      typeof size.label === 'string' &&
      typeof size.vcpu === 'number' &&
      Number.isFinite(size.vcpu) &&
      size.vcpu > 0 &&
      typeof size.memory_gib === 'number' &&
      Number.isFinite(size.memory_gib) &&
      size.memory_gib > 0 &&
      typeof size.disk_gib === 'number' &&
      Number.isFinite(size.disk_gib) &&
      size.disk_gib > 0,
  )
  .map((size) => ({
    id: size.id!,
    label: size.label!,
    cpu: size.vcpu!,
    memoryMb: size.memory_gib! * 1024,
    diskMb: size.disk_gib! * 1024,
  }))

/** The managed machine catalog, sourced from the same plan config as pricing. */
export const SANDBOX_MACHINE_PROFILES = (
  configuredMachineProfiles?.length
    ? configuredMachineProfiles
    : DEFAULT_MACHINE_PROFILES
).map((profile) => ({
  ...profile,
  label: formatProfileLabel(profile),
}))

const rawPlans = (
  plansConfig as unknown as {
    plans?: Record<
      string,
      {
        usage_limits?: Array<{ type?: string; value?: number }>
      }
    >
  }
).plans

/**
 * Returns the fixed machine sizes permitted by a platform tier. A negative
 * SANDBOX_MEMORY_MB limit means unlimited. Unknown tiers fail safely to the
 * Free limit in the UI; the runtime independently enforces the same policy.
 */
export function sandboxMachineProfilesForTier(
  tier: string,
): SandboxMachineProfile[] {
  const normalizedTier = tier.trim().toLowerCase()
  const plan = rawPlans?.[normalizedTier] ?? rawPlans?.free
  const memoryLimit = plan?.usage_limits?.find(
    (limit) => limit.type === 'SANDBOX_MEMORY_MB',
  )?.value

  if (memoryLimit === -1) return SANDBOX_MACHINE_PROFILES
  const effectiveLimit =
    typeof memoryLimit === 'number' && memoryLimit > 0 ? memoryLimit : 512
  return SANDBOX_MACHINE_PROFILES.filter(
    (profile) => profile.memoryMb <= effectiveLimit,
  )
}

const DEFAULT_SANDBOX_PRICING: SandboxPricingConfig = {
  enabled: true,
  currency: rawPlansPricing?.currency ?? 'USD',
  cpuPerHourUsd: rawPlansPricing?.cpu_per_vcpu_hour ?? 0.0504,
  memoryGbPerHourUsd: rawPlansPricing?.memory_per_gib_hour ?? 0.0162,
  diskGbPerHourUsd: rawPlansPricing?.disk_per_gib_hour ?? 0.000166644,
  platformFeePerHourUsd: rawPlansPricing?.platform_per_sandbox_hour ?? 0,
  includedDiskGib: rawPlansPricing?.included_disk_gib ?? 20,
  diskTier2ThresholdGib: rawPlansPricing?.disk_tier2_threshold_gib ?? 50,
  diskTier2Multiplier: rawPlansPricing?.disk_tier2_multiplier ?? 1.25,
  tierMultipliers: DEFAULT_TIER_MULTIPLIERS,
}

/**
 * estimateDiskHourlyUsd mirrors the backend tieredDiskCostUSD: the first
 * includedDiskGib are free, disk up to diskTier2ThresholdGib bills at the
 * base rate, and disk beyond it bills at base * diskTier2Multiplier (marginal
 * tiering). Keep this in lockstep with internal/sandbox/usage_meter.go so the
 * cost preview matches the bill.
 */
export function estimateDiskHourlyUsd(
  diskGib: number,
  pricing: SandboxPricingConfig,
): number {
  if (
    !Number.isFinite(diskGib) ||
    diskGib <= 0 ||
    pricing.diskGbPerHourUsd <= 0
  ) {
    return 0
  }
  const included = Math.max(0, pricing.includedDiskGib)
  const billable = diskGib - included
  if (billable <= 0) return 0

  const threshold = pricing.diskTier2ThresholdGib
  const mult = pricing.diskTier2Multiplier
  // No usable premium band -> all billable disk at the base rate.
  if (threshold <= included || mult <= 0) {
    return billable * pricing.diskGbPerHourUsd
  }
  const tier1 = Math.max(0, Math.min(diskGib, threshold) - included)
  const tier2 = Math.max(0, diskGib - threshold)
  return (tier1 + tier2 * mult) * pricing.diskGbPerHourUsd
}

export function resolveSandboxPricing(
  featuresConfig: unknown,
): SandboxPricingConfig {
  const root = asRecord(featuresConfig)
  const sandbox = asRecord(root?.sandbox)
  const pricing = asRecord(sandbox?.pricing)

  if (!pricing) {
    return DEFAULT_SANDBOX_PRICING
  }

  const currency =
    typeof pricing.currency === 'string'
      ? pricing.currency
      : DEFAULT_SANDBOX_PRICING.currency

  return {
    enabled: toBoolean(pricing.enabled, DEFAULT_SANDBOX_PRICING.enabled),
    currency,
    cpuPerHourUsd: toNumber(
      pricing.cpuPerHourUsd ?? pricing.cpu_per_hour_usd,
      DEFAULT_SANDBOX_PRICING.cpuPerHourUsd,
    ),
    memoryGbPerHourUsd: toNumber(
      pricing.memoryGbPerHourUsd ?? pricing.memory_gb_per_hour_usd,
      DEFAULT_SANDBOX_PRICING.memoryGbPerHourUsd,
    ),
    diskGbPerHourUsd: toNumber(
      pricing.diskGbPerHourUsd ?? pricing.disk_gb_per_hour_usd,
      DEFAULT_SANDBOX_PRICING.diskGbPerHourUsd,
    ),
    platformFeePerHourUsd: toNumber(
      pricing.platformFeePerHourUsd ?? pricing.platform_fee_per_hour_usd,
      DEFAULT_SANDBOX_PRICING.platformFeePerHourUsd,
    ),
    includedDiskGib: toNumber(
      pricing.includedDiskGib ?? pricing.included_disk_gib,
      DEFAULT_SANDBOX_PRICING.includedDiskGib,
    ),
    diskTier2ThresholdGib: toNumber(
      pricing.diskTier2ThresholdGib ?? pricing.disk_tier2_threshold_gib,
      DEFAULT_SANDBOX_PRICING.diskTier2ThresholdGib,
    ),
    diskTier2Multiplier: toNumber(
      pricing.diskTier2Multiplier ?? pricing.disk_tier2_multiplier,
      DEFAULT_SANDBOX_PRICING.diskTier2Multiplier,
    ),
    tierMultipliers: parseTierMultipliers(
      pricing.tierMultipliers ?? pricing.tier_multipliers,
    ),
  }
}
