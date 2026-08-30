import { Button } from '@everstack/ui/components'
import { Zap } from '@everstack/ui/icons'

export const CommunityEditionBanner = () => {
    return (
        <div className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded bg-brand-secondary-500/20 text-brand-secondary-500">
                    <Zap className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-brand-secondary-400">
                    Community Edition
                </span>
            </div>

            <p className="text-xs text-brand-secondary-400/70 mb-3">
                Unlock usage analytics, spend limits, and priority support with Enterprise.
            </p>

            <a
                href="https://everstack.ai/pricing"
                target="_blank"
                rel="noopener noreferrer"

            >
                <Button className='w-full text-xs'>
                    Upgrade to Enterprise
                </Button>
            </a>
        </div>
    )
}
