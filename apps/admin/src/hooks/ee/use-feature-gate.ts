import { useFeature } from '@/hooks/use-features'
import { useIsCommunityEdition, useLicenseStatus } from '@/hooks/license/use-license-status'
import type { FeatureKeyType } from '@/config/features'

export { useIsCommunityEdition }

export interface FeatureGateState {
    /** Feature available at current tier */
    isEnabled: boolean
    /** Community edition (no access to any EE features) */
    isCE: boolean
    /** Dev mode — everything unlocked */
    isDev: boolean
    /** No access at all — CE or cloud free/basic below required tier */
    isBlocked: boolean
    /** Current tier string ('free' | 'basic' | 'pro' | 'enterprise') */
    tier: string
    /** Where to send the user for upgrade */
    upgradeUrl: string
}

/**
 * Reusable hook for feature gate state.
 * Combines feature flag check, edition check, and tier info.
 *
 * In dev mode (edition === "dev"), everything is unlocked — isBlocked is always false.
 */
export function useFeatureGate(featureKey: FeatureKeyType): FeatureGateState {
    const isEnabled = useFeature(featureKey)
    const isCE = useIsCommunityEdition()
    const { data } = useLicenseStatus()
    const tier = data?.license?.tier?.toLowerCase() ?? 'free'
    const isDev = data?.gateway?.edition === 'dev'

    return {
        isEnabled: isDev || isEnabled,
        isCE,
        isDev,
        isBlocked: isDev ? false : (isCE || !isEnabled),
        tier,
        upgradeUrl: isCE ? 'https://everstack.ai/pricing' : '/settings/billing',
    }
}
