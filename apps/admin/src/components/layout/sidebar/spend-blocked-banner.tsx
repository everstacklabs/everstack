import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'

export const SpendBlockedBanner = () => {
    return (
        <div className="mx-2 mb-2 rounded-sm bg-warning-surface border border-warning-border p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-warning-surface-strong text-warning">
                    <Icon icon="lucide:alert-triangle" className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-warning">
                    Usage Limit Reached
                </span>
            </div>
            <p className="text-xs text-warning/90 mb-3 leading-relaxed">
                AI gateway requests are paused. Upgrade your plan or wait for limits to reset.
            </p>
            <Link
                to="/settings/billing"
                search={{ upgrade_success: false, plan: undefined }}
                className="flex w-full items-center justify-center rounded bg-warning-solid px-3 py-1.5 text-xs font-medium text-warning-solid-foreground hover:bg-warning-solid/90 transition-colors"
            >
                Upgrade Plan
            </Link>
        </div>
    )
}
