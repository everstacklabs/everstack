import { useQuery } from '@tanstack/react-query'
import { getTrialStatus } from '@/server/license'
import type { GetTrialStatusResponse } from '@everstack/proto/everstack/gateway/v1/gateway_pb'

/**
 * Query key factory for trial-related queries
 */
export const trialKeys = {
    all: ['trial'] as const,
    status: () => [...trialKeys.all, 'status'] as const,
}

/**
 * Hook to fetch trial status with smart polling
 */
export function useTrialStatus(options?: {
    /** Enable/disable automatic polling (default: false - only poll when explicitly needed) */
    enablePolling?: boolean
    /** Polling interval in milliseconds (default: 60000 = 60 seconds) */
    pollingInterval?: number
}) {
    const enablePolling = options?.enablePolling ?? false // Disable polling by default
    const pollingInterval = options?.pollingInterval ?? 60000 // 60 seconds when enabled

    return useQuery<GetTrialStatusResponse>({
        queryKey: trialKeys.status(),
        queryFn: getTrialStatus,
        refetchInterval: enablePolling ? pollingInterval : false,
        refetchOnWindowFocus: true,
        staleTime: 60000, // Consider data stale after 60 seconds
        gcTime: 5 * 60 * 1000, // Keep in cache for 5 minutes
        retry: false, // Don't retry on failure - assume licensed mode
    })
}

/**
 * Helper hook to check if gateway is in trial mode
 */
export function useIsTrialMode(): boolean {
    const { data } = useTrialStatus()
    return data?.mode === 'trial' && data?.active === true && !data?.expired
}

/**
 * Helper hook to get trial days remaining
 */
export function useTrialDaysRemaining(): number | null {
    const { data } = useTrialStatus()
    if (data?.mode !== 'trial' || !data?.active) return null
    return data.daysRemaining ?? null
}

/**
 * Helper hook to get trial usage info
 */
export function useTrialUsage(): { used: number; limit: number } | null {
    const { data } = useTrialStatus()
    if (data?.mode !== 'trial' || !data?.active) return null
    return {
        used: data.dailyUsed ?? 0,
        limit: data.dailyLimit ?? 100
    }
}
