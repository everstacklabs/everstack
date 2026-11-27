import { Lock } from '@everstack/ui/icons'
import { Link } from '@tanstack/react-router'

export const GatewayLockedBanner = () => {
    return (
        <div className="mx-2 mb-2 rounded-sm bg-danger-surface border border-danger-border p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-danger-surface-strong text-danger">
                    <Lock className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-danger">
                    Gateway Locked
                </span>
            </div>
            <p className="text-xs text-danger/90 mb-3 leading-relaxed">
                Your gateway is currently locked due to license limits. Upgrade to restore access.
            </p>
            <Link
                to="/settings/billing"
                search={{ upgrade_success: false, plan: undefined }}
                className="flex w-full items-center justify-center rounded bg-danger-solid px-3 py-1.5 text-xs font-medium text-white hover:bg-danger-solid/90 transition-colors"
            >
                Upgrade Plan
            </Link>
        </div>
    )
}
