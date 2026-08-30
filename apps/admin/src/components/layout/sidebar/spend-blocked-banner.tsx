import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'

export const SpendBlockedBanner = () => {
    return (
        <div className="mx-2 mb-2 rounded-sm bg-amber-500/10 border border-amber-500/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-amber-500/20 text-amber-500">
                    <Icon icon="lucide:alert-triangle" className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-amber-500">
                    Usage Limit Reached
                </span>
            </div>
            <p className="text-xs text-amber-400/90 light:text-amber-700/90 mb-3 leading-relaxed">
                AI gateway requests are paused. Upgrade your plan or wait for limits to reset.
            </p>
            <Link
                to="/settings/billing"
                search={{ upgrade_success: false, plan: undefined }}
                className="flex w-full items-center justify-center rounded bg-amber-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-600 transition-colors"
            >
                Upgrade Plan
            </Link>
        </div>
    )
}
