import { createContext, useContext, useMemo, useRef, type ReactNode } from 'react'
import {
    FeatureSet,
    createFeatureSet,
    type AvailableFeatures,
    type FeatureKeyType,
    type FeatureCategoryType,
    FeatureKey,
    FeatureCategory,
} from '@/config/features'

// Re-export everything from config/features for convenience
export {
    FeatureKey,
    FeatureCategory,
    FeatureSet,
    createFeatureSet,
    type FeatureKeyType,
    type FeatureCategoryType,
    type AvailableFeatures,
}

/**
 * Context for the global FeatureSet
 */
const FeatureSetContext = createContext<FeatureSet | null>(null)

interface FeatureProviderProps {
    /** Available features from the license API response */
    availableFeatures?: AvailableFeatures
    children: ReactNode
}

/**
 * Provider component that makes the FeatureSet available throughout the app.
 *
 * Usage:
 * ```tsx
 * // In your root/layout component:
 * const { data } = useLicenseStatus()
 *
 * return (
 *   <FeatureProvider availableFeatures={data?.availableFeatures}>
 *     <App />
 *   </FeatureProvider>
 * )
 * ```
 */
export function FeatureProvider({ availableFeatures, children }: FeatureProviderProps) {
    // Use a ref to store the last valid featureSet
    const lastValidFeatureSetRef = useRef<FeatureSet>(createFeatureSet())

    // Keep previous featureSet during updates to prevent UI flicker
    const featureSet = useMemo(
        () => {
            // availableFeatures is undefined when:
            // - Initial load (data not fetched yet)
            // - data?.availableFeatures is undefined (should use cached to prevent flicker)
            if (availableFeatures === undefined) {
                // Use cached featureSet to prevent UI flicker during refetches
                return lastValidFeatureSetRef.current
            }

            // availableFeatures is defined (could be empty {} for free tier or populated for paid)
            // This is real data from the API - use it (handles both upgrades and downgrades)
            const newFeatureSet = createFeatureSet(availableFeatures)
            lastValidFeatureSetRef.current = newFeatureSet
            return newFeatureSet
        },
        [availableFeatures]
    )

    return (
        <FeatureSetContext.Provider value={featureSet}>
            {children}
        </FeatureSetContext.Provider>
    )
}

/**
 * Hook to access the FeatureSet instance.
 * Must be used within a FeatureProvider.
 *
 * Usage:
 * ```tsx
 * const features = useFeatureSet()
 *
 * if (features.has(FeatureKey.SPEND_LIMITS)) {
 *   // Show spend limits UI
 * }
 * ```
 */
export function useFeatureSet(): FeatureSet {
    const featureSet = useContext(FeatureSetContext)

    if (!featureSet) {
        // Return empty FeatureSet if provider not found (graceful degradation)
        // This can happen during initial load or if features aren't available yet
        return createFeatureSet()
    }

    return featureSet
}

/**
 * Hook to check if a specific feature is enabled.
 * Convenience wrapper around useFeatureSet().has()
 *
 * Usage:
 * ```tsx
 * const hasSpendLimits = useFeature(FeatureKey.SPEND_LIMITS)
 *
 * if (hasSpendLimits) {
 *   // Show spend limits UI
 * }
 * ```
 */
export function useFeature(key: FeatureKeyType): boolean {
    const featureSet = useFeatureSet()
    return featureSet.has(key)
}

/**
 * Hook to check multiple features at once.
 * Returns an object with feature keys as keys and booleans as values.
 *
 * Usage:
 * ```tsx
 * const { spend_limits, team_management } = useFeatures([
 *   FeatureKey.SPEND_LIMITS,
 *   FeatureKey.TEAM_MANAGEMENT,
 * ])
 * ```
 */
export function useFeatures<K extends FeatureKeyType>(keys: K[]): Record<K, boolean> {
    const featureSet = useFeatureSet()

    return useMemo(() => {
        const result = {} as Record<K, boolean>
        for (const key of keys) {
            result[key] = featureSet.has(key)
        }
        return result
    }, [featureSet, keys])
}

/**
 * Hook to get all features for a specific category.
 *
 * Usage:
 * ```tsx
 * const dashboardFeatures = useFeaturesByCategory(FeatureCategory.DASHBOARD)
 * ```
 */
export function useFeaturesByCategory(category: FeatureCategoryType): FeatureKeyType[] {
    const featureSet = useFeatureSet()
    return useMemo(() => featureSet.byCategory(category), [featureSet, category])
}

/**
 * Component that renders children only if a feature is enabled.
 *
 * Usage:
 * ```tsx
 * <Feature flag={FeatureKey.SPEND_LIMITS}>
 *   <SpendLimitsSettings />
 * </Feature>
 *
 * // With fallback:
 * <Feature flag={FeatureKey.SSO} fallback={<UpgradePrompt />}>
 *   <SSOSettings />
 * </Feature>
 * ```
 */
export function Feature({
    flag,
    children,
    fallback = null
}: {
    flag: FeatureKeyType
    children: ReactNode
    fallback?: ReactNode
}) {
    const isEnabled = useFeature(flag)
    return <>{isEnabled ? children : fallback}</>
}

/**
 * Component that renders children only if ANY of the specified features are enabled.
 *
 * Usage:
 * ```tsx
 * <FeatureAny flags={[FeatureKey.SSO, FeatureKey.TEAM_MANAGEMENT]}>
 *   <EnterpriseSettings />
 * </FeatureAny>
 * ```
 */
export function FeatureAny({
    flags,
    children,
    fallback = null
}: {
    flags: FeatureKeyType[]
    children: ReactNode
    fallback?: ReactNode
}) {
    const featureSet = useFeatureSet()
    const isAnyEnabled = flags.some(flag => featureSet.has(flag))
    return <>{isAnyEnabled ? children : fallback}</>
}

/**
 * Component that renders children only if ALL specified features are enabled.
 *
 * Usage:
 * ```tsx
 * <FeatureAll flags={[FeatureKey.TEAM_MANAGEMENT, FeatureKey.SSO]}>
 *   <EnterpriseSSOTeamSettings />
 * </FeatureAll>
 * ```
 */
export function FeatureAll({
    flags,
    children,
    fallback = null
}: {
    flags: FeatureKeyType[]
    children: ReactNode
    fallback?: ReactNode
}) {
    const featureSet = useFeatureSet()
    const isAllEnabled = flags.every(flag => featureSet.has(flag))
    return <>{isAllEnabled ? children : fallback}</>
}
