import { Lock } from '@everstack/ui/icons'
import { Link } from '@tanstack/react-router'

export const GatewayLockedBanner = () => {
    return (
        <div className="mx-2 mb-2 rounded-sm bg-red-500/10 border border-red-500/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-red-500/20 text-red-500">
                    <Lock className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-red-500">
                    Gateway Locked
                </span>
            </div>
            <p className="text-xs text-red-400/90 light:text-red-600/90 mb-3 leading-relaxed">
                Your gateway is currently locked due to license limits. Upgrade to restore access.
            </p>
            <Link
                to="/settings/billing"
                search={{ upgrade_success: false, plan: undefined }}
                className="flex w-full items-center justify-center rounded bg-red-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-600 transition-colors"
            >
                Upgrade Plan
            </Link>
        </div>
    )
}
