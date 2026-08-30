import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import type { Plan, PlansApiResponse } from '../../lib/plans'

/**
 * Return type for useBillingPlans hook
 */
export interface UseBillingPlansResult {
    data: Plan[] | null
    plansConfig: Record<string, Plan> | null | undefined
    isLoading: boolean
    isError: boolean
    error: Error | null
    refetch: UseQueryResult<Record<string, Plan> | null>['refetch']
}

/**
 * Query key factory for plans
 */
export const plansKeys = {
    all: ['billing', 'plans'] as const,
    config: () => [...plansKeys.all, 'config'] as const,
}

/**
 * Fetch plans from the billing API
 * Used by the cloud console billing UI.
 */
async function fetchPlansFromApi(): Promise<Record<string, Plan> | null> {
    try {
        const response = await fetch('/api/billing/plans', {
            credentials: 'include',
        })

        if (!response.ok) {
            return null
        }

        const data: PlansApiResponse = await response.json()

        // `synced` only indicates whether Stripe price IDs are provisioned.
        // Plan tiers can be rendered from `plans_config` alone, so return
        // them regardless of Stripe sync state. Checkout actions can still
        // gate on `synced` separately if needed.
        if (data.plans_config) {
            return data.plans_config
        }

        return null
    } catch {
        return null
    }
}

/**
 * Hook to fetch plans configuration from the billing API
 * Returns the plans config map (tier -> Plan)
 *
 * @example
 * const { data: plansConfig, isLoading } = usePlansConfig()
 * const basicPlan = plansConfig?.['basic']
 */
export function usePlansConfig() {
    return useQuery<Record<string, Plan> | null>({
        queryKey: plansKeys.config(),
        queryFn: fetchPlansFromApi,
        staleTime: 5 * 60 * 1000, // 5 minutes - plans don't change often
        gcTime: 10 * 60 * 1000, // 10 minutes
        retry: 1, // Only retry once since we have fallbacks
    })
}

/**
 * Hook to fetch plans as an ordered array
 * Useful for displaying plan lists
 *
 * @example
 * const { data: plans, isLoading } = useBillingPlans()
 * plans?.map(plan => <PlanCard key={plan.tier} plan={plan} />)
 */
export function useBillingPlans(): UseBillingPlansResult {
    const { data: plansConfig, isLoading, isError, error, refetch } = usePlansConfig()

    // Convert to ordered array
    const plans: Plan[] = []
    if (plansConfig) {
        const order = ['free', 'basic', 'pro', 'enterprise']
        for (const tier of order) {
            const plan = plansConfig[tier]
            if (plan) {
                plans.push(plan)
            }
        }
    }

    return {
        data: plans.length > 0 ? plans : null,
        plansConfig,
        isLoading,
        isError,
        error,
        refetch,
    }
}
