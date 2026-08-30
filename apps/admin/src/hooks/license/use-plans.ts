import { useQuery } from '@tanstack/react-query'
import { getPlans } from '@/server/license'
import type { Plan } from '@/config/plans'

export function useGatewayPlans() {
    return useQuery({
        queryKey: ['license', 'plans'],
        queryFn: async (): Promise<Plan[]> => {
            const response = await getPlans()

            // Convert proto response to Plan[] with ordering
            const plansMap = new Map<string, Plan>()
            for (const p of response.plans) {
                plansMap.set(p.tier, {
                    tier: p.tier,
                    name: p.name,
                    description: p.description,
                    trial_duration_days: p.trialDurationDays,
                    pricing: {
                        monthly: p.pricing?.monthly ?? '',
                        yearly: p.pricing?.yearly ?? '',
                        discounted: p.pricing?.discounted || undefined,
                        suggested: p.pricing?.suggested || undefined,
                        per_seat: p.pricing?.perSeat ? {
                            monthly: p.pricing.perSeat.monthly,
                            yearly: p.pricing.perSeat.yearly,
                            subText: p.pricing.perSeat.subText || undefined,
                        } : undefined,
                    },
                    highlight: p.highlight,
                    seat_limit: p.seatLimit ?? 0,
                    features: p.features.map(f => ({
                        name: f.name,
                        enabled: f.enabled,
                    })),
                    usage_limits: p.usageLimits.map(u => ({
                        type: u.type,
                        value: Number(u.value),
                        subText: u.subText || undefined,
                    })),
                })
            }

            // Return in order
            const order = ['free', 'basic', 'pro', 'enterprise']
            return order
                .map(tier => plansMap.get(tier))
                .filter((p): p is Plan => p !== undefined)
        },
        staleTime: 5 * 60 * 1000, // 5 minutes - plans don't change often
        gcTime: 10 * 60 * 1000, // 10 minutes
    })
}
