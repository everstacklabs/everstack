import { activateGateway, getGatewayStatus } from "@/server/license"
import { useMutation, useQuery, useQueryClient, type UseMutationResult, type UseQueryResult } from "@tanstack/react-query"
import type {
    GetGatewayInstanceStatusResponse,
    ActivateGatewayInstanceResponse
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import { licenseKeys } from './use-license-status'

/**
 * Query key factory for gateway license queries
 * Centralizes query keys for better type safety and maintenance
 */
export const gatewayLicenseKeys = {
    all: ['gateway', 'license'] as const,
    status: () => [...gatewayLicenseKeys.all, 'status'] as const,
} as const

/**
 * Hook to fetch and monitor gateway license activation status
 * 
 * Features:
 * - Caches status for 2 minutes to prevent excessive API calls
 * - Auto-polls every 5s when not activated
 * - Stops polling once gateway is activated
 * - Shared cache across all components
 * 
 * @returns Query result with gateway status and loading states
 */
export function useGatewayLicenseStatus(): UseQueryResult<GetGatewayInstanceStatusResponse, Error> {
    return useQuery({
        queryKey: gatewayLicenseKeys.status(),
        queryFn: getGatewayStatus,

        refetchOnWindowFocus: false, // Don't spam API on tab switches
        refetchOnReconnect: true, // Refetch when network reconnects
        staleTime: 2 * 60 * 1000,
        retry: false,
    })
}

/**
 * Mutation hook to activate the gateway with an activation token
 * 
 * Features:
 * - Automatically invalidates and refetches status after success
 * - Type-safe error handling
 * - Optimistic updates support
 * 
 * @returns Mutation result with activate function and states
 * 
 * @example
 * ```tsx
 * const activate = useActivateGatewayLicense()
 * 
 * activate.mutate('mf_act_token123', {
 *   onSuccess: (data) => console.log('Activated:', data.instanceId),
 *   onError: (error) => console.error('Failed:', error.message)
 * })
 * ```
 */
export function useActivateGatewayLicense(): UseMutationResult<
    ActivateGatewayInstanceResponse,
    Error,
    { activationToken: string; deviceFingerprintHash?: string; instanceId?: string },
    unknown
> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ activationToken, deviceFingerprintHash, instanceId }) =>
            activateGateway(activationToken, deviceFingerprintHash, instanceId),
        onSuccess: async () => {
            // Force refetch all license-related queries immediately after activation
            // Use refetchQueries instead of invalidateQueries to ensure immediate fetch
            await Promise.all([
                queryClient.refetchQueries({ queryKey: gatewayLicenseKeys.status() }),
                queryClient.refetchQueries({ queryKey: licenseKeys.status() }),
                // Also invalidate any billing-related queries
                queryClient.invalidateQueries({ queryKey: ['billing'] }),
                queryClient.invalidateQueries({ queryKey: ['plans'] }),
            ])
        },
        retry: false, // Don't retry activation (could mark token as used)
    })
}
