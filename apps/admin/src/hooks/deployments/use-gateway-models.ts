import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { listGatewayModels, type ProviderModels } from '@/server/gateway'

const GATEWAY_MODELS_QUERY_KEY = ['gateway-models']

/**
 * React Query hook that fetches available models grouped by provider from the gateway.
 * Models change infrequently so staleTime is set to 5 minutes.
 */
export function useGatewayModels(): UseQueryResult<ProviderModels[], Error> {
    return useQuery({
        queryKey: GATEWAY_MODELS_QUERY_KEY,
        queryFn: listGatewayModels,
        staleTime: 5 * 60 * 1000,
    })
}
