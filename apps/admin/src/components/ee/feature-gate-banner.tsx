import { Button } from '@everstack/ui/components'
import { Lock, ArrowUpRight } from '@everstack/ui/icons'

interface FeatureGateBannerProps {
    featureName: string
    description: string
    requiredTier: string
    upgradeUrl: string
    isCE?: boolean
}

/**
 * Full-page upgrade prompt for EE-gated features.
 * Renders a centered card with feature info and upgrade CTA.
 */
export function FeatureGateBanner({
    featureName,
    description,
    requiredTier,
    upgradeUrl,
    isCE,
}: FeatureGateBannerProps) {
    const isExternal = upgradeUrl.startsWith('http')

    return (
        <div className="flex h-full w-full flex-col items-center justify-center">
            <div className="relative mb-6">
                <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                    <Lock className="size-8 text-brand-secondary-400" />
                </div>
            </div>

            <h3 className="text-base font-medium text-white mb-2 light:text-brand-main-50">
                {featureName}
            </h3>

            <p className="text-sm text-white/50 max-w-sm text-center leading-relaxed mb-4 light:text-black/50">
                {description}
            </p>

            <div className="mb-5 rounded-md bg-brand-main-800/60 border border-brand-main-600/40 px-4 py-2.5 text-xs text-white/50 light:text-black/50">
                {isCE ? (
                    <>This feature is available on <span className="font-medium text-brand-secondary-400">Everstack Cloud</span> ({requiredTier}+ plan).</>
                ) : (
                    <>Requires the <span className="font-medium text-brand-secondary-400 capitalize">{requiredTier}</span> plan or higher.</>
                )}
            </div>

            {isExternal ? (
                <a href={upgradeUrl} target="_blank" rel="noopener noreferrer">
                    <Button className="gap-2">
                        Upgrade Now
                        <ArrowUpRight className="size-4" />
                    </Button>
                </a>
            ) : (
                <a href={upgradeUrl}>
                    <Button className="gap-2">
                        Upgrade Plan
                        <ArrowUpRight className="size-4" />
                    </Button>
                </a>
            )}
        </div>
    )
}

interface FeatureGateInfoBannerProps {
    message: string
    upgradeUrl: string
}

/**
 * Inline banner for tier-limited features (e.g. Pro tier with 24h limit).
 * Shows at the top of the page, not a full-page block.
 */
export function FeatureGateInfoBanner({ message, upgradeUrl }: FeatureGateInfoBannerProps) {
    const isExternal = upgradeUrl.startsWith('http')

    return (
        <div className="flex items-center justify-between gap-4 border-b border-amber-500/20 bg-amber-500/5 px-4 py-2">
            <p className="text-xs text-amber-300">
                {message}
            </p>
            {isExternal ? (
                <a href={upgradeUrl} target="_blank" rel="noopener noreferrer" className="shrink-0">
                    <Button variant="outline" size="sm" className="h-7 gap-1 text-xs border-amber-500/30 text-amber-300 hover:bg-amber-500/10">
                        Upgrade
                        <ArrowUpRight className="size-3" />
                    </Button>
                </a>
            ) : (
                <a href={upgradeUrl} className="shrink-0">
                    <Button variant="outline" size="sm" className="h-7 gap-1 text-xs border-amber-500/30 text-amber-300 hover:bg-amber-500/10">
                        Upgrade
                        <ArrowUpRight className="size-3" />
                    </Button>
                </a>
            )}
        </div>
    )
}
