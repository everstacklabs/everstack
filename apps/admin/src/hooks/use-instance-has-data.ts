import { useQuery } from '@tanstack/react-query'
import { streamLogs } from '@/server/logs'
import { streamTraces } from '@/server/traces'
import { timestampFromDate, create } from '@everstack/client'
import type { Code, ConnectError } from '@everstack/client'
import { ListTracesRequestSchema } from '@everstack/proto/everstack/traces/v1/traces_service_pb'

/**
 * Hook to check if the instance has any historical data (logs or traces)
 * Uses a prefetch query with long cache to avoid repeated checks
 * 
 * @param type - 'logs' or 'traces'
 * @returns Object with hasInstanceData (boolean | undefined) and isLoading
 */
export function useInstanceHasData(type: 'logs' | 'traces') {
    const sevenDaysAgo = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString()
    const now = new Date().toISOString()

    const { data: hasInstanceData, isLoading } = useQuery({
        queryKey: [`instance-has-${type}`],
        queryFn: async ({ signal }) => {
            try {
                if (type === 'logs') {
                    // Check if any logs exist in the last 7 days
                    for await (const log of streamLogs({
                        pageSize: 1,
                        from: sevenDaysAgo,
                        to: now,
                    }, { signal })) {
                        // If we get any log, instance has data
                        if (log.correlationId) {
                            return true
                        }
                    }
                    return false
                } else {
                    // Check if any traces exist in the last 7 days
                    const fromTime = timestampFromDate(new Date(sevenDaysAgo))
                    const toTime = timestampFromDate(new Date(now))

                    const request = create(ListTracesRequestSchema, {
                        from: fromTime,
                        to: toTime,
                        limit: 1,
                        offset: 0,
                        tenantId: '',
                        model: '',
                        provider: '',
                        statusCode: '',
                    })

                    for await (const trace of streamTraces(request, { signal })) {
                        // If we get any trace, instance has data
                        if (trace.traceId) {
                            return true
                        }
                    }
                    return false
                }
            } catch (err) {
                // AbortError/canceled is expected when component unmounts or query is canceled
                // @ts-expect-error - ConnectError is not typed
                if (err instanceof ConnectError && err.code === Code.Canceled) {
                    // Return undefined to indicate the check was canceled (not completed)
                    return undefined
                }

                // For other errors, assume instance has data (fail open)
                // This prevents showing onboarding state due to errors
                console.error(`Error checking instance ${type} data:`, err)
                return true
            }
        },
        staleTime: Infinity, // Never consider stale - cache forever until manually invalidated
        gcTime: Infinity, // Keep in cache for entire session
        refetchOnWindowFocus: false,
        refetchOnMount: false,
        refetchOnReconnect: false,
    })

    return {
        hasInstanceData,
        isLoading,
    }
}

