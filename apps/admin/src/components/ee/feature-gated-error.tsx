import { AlertCircle } from 'lucide-react'
import type { FeatureKeyType } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { isFeatureGateError, cleanErrorMessage } from '@/lib/feature-gate-error'
import { FeatureGateBanner } from './feature-gate-banner'

interface FeatureGatedErrorProps {
    /** The query error to render. */
    error: unknown
    /** Feature this view is gated on — drives the upgrade URL / CE detection. */
    featureKey: FeatureKeyType
    /** Human-readable feature name (e.g. "Voice", "Alerts"). */
    featureName: string
    /** Upgrade-prompt blurb; falls back to a generic line. */
    description?: string
    /** Plan required for the feature. Most gated features are Pro+. */
    requiredTier?: string
}

/**
 * Renders a query error in a UI-friendly way:
 *   - a feature-gate denial (FailedPrecondition "requires a higher plan")
 *     becomes the upgrade banner instead of a raw red error string;
 *   - any other error becomes a clean, centered error card.
 *
 * Use this in the `if (error)` branch of a gated page's data view. It exists
 * because the frontend feature gate and the backend gate can disagree (e.g.
 * stale `availableFeatures`), so a gated page can render and fire a query that
 * the backend then denies — this turns that denial into the right prompt.
 */
export function FeatureGatedError({
    error,
    featureKey,
    featureName,
    description,
    requiredTier = 'Pro',
}: FeatureGatedErrorProps) {
    const gate = useFeatureGate(featureKey)

    if (isFeatureGateError(error)) {
        return (
            <FeatureGateBanner
                featureName={featureName}
                description={
                    description ??
                    `${featureName} isn't included in your current plan.`
                }
                requiredTier={requiredTier}
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return (
        <div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center">
            <div className="rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
                <AlertCircle className="size-6 text-red-400 light:text-red-600" />
            </div>
            <h3 className="text-sm font-medium text-white light:text-brand-main-50">
                Couldn&apos;t load {featureName.toLowerCase()}
            </h3>
            <p className="max-w-sm text-xs text-white/50 light:text-black/50">
                {cleanErrorMessage(error)}
            </p>
        </div>
    )
}
