import {
  useQuery,
  useMutation,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { getLicenseStatus, refreshLicenseStatus } from '@/server/license'
import type { GetLicenseMonitorStatusResponse } from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import type {
  AvailableFeatures,
  FeatureCategoryType,
  FeatureStatusType,
} from '@/config/features'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

/**
 * Query key factory for license-related queries
 */
export const licenseKeys = {
  all: ['license'] as const,
  status: () => [...licenseKeys.all, 'status'] as const,
}

/**
 * Normalized license status for UI consumption
 * Converts proto response to a friendlier format
 */
export interface NormalizedLicenseStatus {
  license: {
    active: boolean
    tier: string
    status: string
    is_paid: boolean
    expires_at: string | null
    trial_expires: string | null
    fetched_at: string | null
    usage_limits: Array<{ type: string; limit: number }>
    tenant_id: string
    instance_id: string
    sandbox_billing_enabled: boolean
  }
  usage: {
    // Request rate metrics
    rpm: number
    rps: number
    rph: number
    total_requests: number
    last_reset: string | null
    requests_in_min: number
    requests_in_sec: number
    requests_in_hour: number
    // Token metrics
    total_input_tokens: number
    total_output_tokens: number
    total_tokens: number
    // Cost metrics (in USD)
    estimated_cost_usd: number
    cache_savings_usd: number
    // Cache performance metrics
    cache_hits: number
    cache_misses: number
  }
  gateway: {
    locked: boolean
    lock_reason: string
    features: Array<{
      name: string
      enabled: boolean
      required_tier: string
      locked_reason: string
    }>
    spend_blocked: boolean
    spend_blocked_reason: string
    /** Build edition: "dev" (development), "ce" (Community), or "ee" (Enterprise) */
    edition: 'dev' | 'ce' | 'ee'
  }
  // Available features from license service (tier-evaluated)
  availableFeatures: AvailableFeatures
}

/**
 * Convert proto response to normalized format for UI
 */
function normalizeResponse(
  data: GetLicenseMonitorStatusResponse,
): NormalizedLicenseStatus {
  const timestampToISO = (
    ts: { seconds?: bigint } | undefined,
  ): string | null => {
    if (!ts?.seconds) return null
    return new Date(
      (typeof ts.seconds === 'bigint'
        ? safeBigIntToNumber(ts.seconds)
        : Number(ts.seconds)) * 1000,
    ).toISOString()
  }

  return {
    license: {
      active: data.license?.active ?? false,
      tier: data.license?.tier ?? '',
      status: data.license?.status ?? '',
      is_paid: data.license?.isPaid ?? false,
      expires_at: timestampToISO(data.license?.expiresAt),
      trial_expires: timestampToISO(data.license?.trialExpires),
      fetched_at: timestampToISO(data.license?.fetchedAt),
      usage_limits: (data.license?.usageLimits ?? []).map((l) => ({
        type: l.type,
        limit: Number(l.limit),
      })),
      tenant_id: data.license?.tenantId ?? '',
      instance_id: data.license?.instanceId ?? '',
      sandbox_billing_enabled: data.license?.sandboxBillingEnabled ?? false,
    },
    usage: {
      // Request rate metrics
      rpm: Number(data.usage?.rpm ?? 0),
      rps: Number(data.usage?.rps ?? 0),
      rph: Number(data.usage?.rph ?? 0),
      total_requests: Number(data.usage?.totalRequests ?? 0),
      last_reset: timestampToISO(data.usage?.lastReset),
      requests_in_min: Number(data.usage?.requestsInMin ?? 0),
      requests_in_sec: Number(data.usage?.requestsInSec ?? 0),
      requests_in_hour: Number(data.usage?.requestsInHour ?? 0),
      // Token metrics
      total_input_tokens: Number(data.usage?.totalInputTokens ?? 0),
      total_output_tokens: Number(data.usage?.totalOutputTokens ?? 0),
      total_tokens: Number(data.usage?.totalTokens ?? 0),
      // Cost metrics
      estimated_cost_usd: Number(data.usage?.estimatedCostUsd ?? 0),
      cache_savings_usd: Number(data.usage?.cacheSavingsUsd ?? 0),
      // Cache performance metrics
      cache_hits: Number(data.usage?.cacheHits ?? 0),
      cache_misses: Number(data.usage?.cacheMisses ?? 0),
    },
    gateway: {
      locked: data.gateway?.locked ?? false,
      lock_reason: data.gateway?.lockReason ?? '',
      features: (data.gateway?.features ?? []).map((f) => ({
        name: f.name,
        enabled: f.enabled,
        required_tier: f.requiredTier,
        locked_reason: f.lockedReason,
      })),
      spend_blocked: data.gateway?.spendBlocked ?? false,
      spend_blocked_reason: data.gateway?.spendBlockedReason ?? '',
      edition: (data.gateway?.edition as 'dev' | 'ce' | 'ee') || 'dev',
    },
    // Convert proto available features to AvailableFeatures format
    availableFeatures: Object.fromEntries(
      Object.entries(data.availableFeatures ?? {}).map(([key, f]) => [
        key,
        {
          name: f.name,
          description: f.description,
          status: f.status as FeatureStatusType,
          categories: f.categories as FeatureCategoryType[],
        },
      ]),
    ),
  }
}

/**
 * Hook to fetch license status with smart polling
 *
 * Refresh strategy:
 * - Polls every 1 hour by default (can be customized)
 * - Automatically refetches when window regains focus
 * - Can be manually refreshed
 * - Pauses polling when window is not visible
 */
export function useLicenseStatus(options?: {
  /** Enable/disable automatic polling (default: true) */
  enablePolling?: boolean
  /** Polling interval in milliseconds (default: 3600000 = 1 hour) */
  pollingInterval?: number
}): UseQueryResult<NormalizedLicenseStatus, Error> {
  const enablePolling = options?.enablePolling ?? true
  const pollingInterval = options?.pollingInterval ?? 3600000 // 1 hour

  return useQuery<NormalizedLicenseStatus>({
    queryKey: licenseKeys.status(),
    queryFn: async () => {
      const response = await getLicenseStatus()
      return normalizeResponse(response)
    },
    // Polling configuration
    refetchInterval: enablePolling ? pollingInterval : false,
    // Refetch when window regains focus (user comes back to tab)
    refetchOnWindowFocus: true,
    // Keep data fresh
    staleTime: 10000, // Consider data stale after 10 seconds
    // Always keep license data in cache
    gcTime: Infinity,
    // Keep previous data during refetch to prevent UI flicker
    placeholderData: (previousData) => previousData,
    // Retry on failure
    retry: 3,
    retryDelay: (attemptIndex) => Math.min(1000 * 2 ** attemptIndex, 30000),
  })
}

/**
 * Hook to manually refresh license status from the license service
 *
 * This triggers an immediate sync with the license service,
 * bypassing the cached state.
 */
export function useRefreshLicense() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async () => {
      const response = await refreshLicenseStatus()
      // Normalize the refresh response (has same structure as status response)
      return normalizeResponse({
        license: response.license,
        usage: response.usage,
        gateway: response.gateway,
        availableFeatures: response.availableFeatures,
      } as GetLicenseMonitorStatusResponse)
    },
    onSuccess: (newData) => {
      // Merge with existing data to preserve fields that might not be in the response
      // This is especially important for availableFeatures during usage-only refreshes
      const existingData = queryClient.getQueryData<NormalizedLicenseStatus>(
        licenseKeys.status(),
      )

      queryClient.setQueryData(licenseKeys.status(), {
        ...existingData,
        ...newData,
        // Preserve availableFeatures if the new response doesn't have them
        availableFeatures:
          Object.keys(newData.availableFeatures || {}).length > 0
            ? newData.availableFeatures
            : (existingData?.availableFeatures ?? {}),
      })
    },
    // Show loading state for at least 500ms for better UX
    meta: {
      minLoadingTime: 500,
    },
  })
}

/**
 * Helper hook that combines license status with computed properties
 */
export function useLicenseInfo() {
  const { data, ...query } = useLicenseStatus()

  // Compute helpful derived values
  const isLocked = data?.gateway?.locked ?? false
  const isSpendBlocked = data?.gateway?.spend_blocked ?? false

  const expiresAtMs = data?.license?.expires_at
    ? new Date(data.license.expires_at).getTime()
    : null
  const trialExpiresMs = data?.license?.trial_expires
    ? new Date(data.license.trial_expires).getTime()
    : null

  const isExpiringSoon = expiresAtMs
    ? expiresAtMs - Date.now() < 7 * 24 * 60 * 60 * 1000 // 7 days
    : false
  const isTrialExpiringSoon = trialExpiresMs
    ? trialExpiresMs - Date.now() < 7 * 24 * 60 * 60 * 1000 // 7 days
    : false

  // Calculate usage percentages
  const usagePercentages =
    data?.usage && data.license?.usage_limits
      ? data.license.usage_limits.reduce(
          (acc, limit) => {
            if (limit.limit === -1) {
              // Unlimited
              acc[limit.type] = 0
            } else {
              let currentValue = 0
              switch (limit.type) {
                case 'RPM':
                  currentValue = data.usage?.requests_in_min ?? 0
                  break
                case 'RPS':
                  currentValue = data.usage?.requests_in_sec ?? 0
                  break
                case 'RPH':
                  currentValue = data.usage?.requests_in_hour ?? 0
                  break
                case 'REQUESTS':
                  currentValue = data.usage?.total_requests ?? 0
                  break
                case 'TOKENS':
                  currentValue = data.usage?.total_tokens ?? 0
                  break
              }
              acc[limit.type] = (currentValue / limit.limit) * 100
            }
            return acc
          },
          {} as Record<string, number>,
        )
      : {}

  // Check if approaching any limits (> 80%)
  const isApproachingLimit = Object.values(usagePercentages).some(
    (pct) => pct > 80,
  )
  const isOverLimit = Object.values(usagePercentages).some((pct) => pct >= 100)

  const isCommunityEdition = data?.gateway?.edition === 'ce'

  return {
    data,
    ...query,
    // Computed properties
    isCommunityEdition,
    isLocked,
    isSpendBlocked,
    isExpiringSoon,
    isTrialExpiringSoon,
    usagePercentages,
    isApproachingLimit,
    isOverLimit,
    // Helper methods
    getFeature: (featureName: string) =>
      data?.gateway?.features?.find((f) => f.name === featureName),
    isFeatureEnabled: (featureName: string) =>
      data?.gateway?.features?.find((f) => f.name === featureName)?.enabled ??
      false,
  }
}

/**
 * Simple hook to check if running Community Edition
 */
export function useIsCommunityEdition(): boolean {
  const { data } = useLicenseStatus()
  return data?.gateway?.edition === 'ce'
}

/**
 * Resolved plan cap for a usage limit type.
 *
 * `null` means uncapped: the plan grants unlimited (-1), the build is dev, or
 * the gateway has not reported that limit. Callers must treat `null` as "do
 * not block": the server-side gate is the authority, this is only for showing
 * a ceiling before a user hits it. Deliberately does not guess a limit the
 * gateway did not report, since a wrong ceiling reads as an upgrade prompt to
 * users who may have nothing to upgrade to.
 */
export function usePlanLimit(type: string): number | null {
  const { data } = useLicenseStatus()
  if (!data) return null
  if (data.gateway?.edition === 'dev') return null

  const reported = data.license?.usage_limits?.find((l) => l.type === type)
  if (!reported) return null
  return reported.limit < 0 ? null : reported.limit
}

/**
 * Hook that returns a formatted, human-readable license summary
 */
export function useLicenseSummary() {
  const { data } = useLicenseStatus()

  if (!data) return null

  const { license, usage, gateway } = data

  // Format expiry date
  const expiryDate = license?.expires_at ? new Date(license.expires_at) : null
  const trialExpiryDate = license?.trial_expires
    ? new Date(license.trial_expires)
    : null
  const daysUntilExpiry = expiryDate
    ? Math.ceil((expiryDate.getTime() - Date.now()) / (1000 * 60 * 60 * 24))
    : null
  const daysUntilTrialExpiry = trialExpiryDate
    ? Math.ceil(
        (trialExpiryDate.getTime() - Date.now()) / (1000 * 60 * 60 * 24),
      )
    : null

  // Calculate cache hit rate
  const totalCacheRequests =
    (usage?.cache_hits ?? 0) + (usage?.cache_misses ?? 0)
  const cacheHitRate =
    totalCacheRequests > 0
      ? ((usage?.cache_hits ?? 0) / totalCacheRequests) * 100
      : 0

  return {
    tier: license?.tier ?? '',
    status: license?.status ?? '',
    isPaid: license?.is_paid ?? false,
    isActive: license?.active ?? false,
    isLocked: gateway?.locked ?? false,
    lockReason: gateway?.lock_reason ?? '',
    expiryDate,
    trialExpiryDate,
    daysUntilExpiry,
    daysUntilTrialExpiry,
    // Request metrics
    currentRPM: usage?.requests_in_min ?? 0,
    currentRPS: usage?.requests_in_sec ?? 0,
    totalRequests: usage?.total_requests ?? 0,
    // Token metrics
    totalInputTokens: usage?.total_input_tokens ?? 0,
    totalOutputTokens: usage?.total_output_tokens ?? 0,
    totalTokens: usage?.total_tokens ?? 0,
    // Cost metrics
    estimatedCostUSD: usage?.estimated_cost_usd ?? 0,
    cacheSavingsUSD: usage?.cache_savings_usd ?? 0,
    // Cache metrics
    cacheHits: usage?.cache_hits ?? 0,
    cacheMisses: usage?.cache_misses ?? 0,
    cacheHitRate,
    limits: license?.usage_limits ?? [],
    features: gateway?.features ?? [],
  }
}
