import { Icon } from '@iconify/react'

export function RateLimiterConfigForm() {
    return (
        <div className="space-y-3">
            <div className="flex items-start gap-2.5 rounded-md border border-brand-main-500/50 bg-brand-main-700/30 p-3">
                <Icon icon="lucide:info" className="mt-0.5 h-4 w-4 shrink-0 text-brand-secondary-400" />
                <div className="space-y-1.5">
                    <p className="text-sm text-brand-main-100">Provider-based rate limiting</p>
                    <p className="text-xs text-brand-main-400">
                        Rate limiting is automatically managed by monitoring upstream provider response headers. When a provider signals rate limits (via headers like <code className="text-brand-main-300">x-ratelimit-remaining</code> or <code className="text-brand-main-300">retry-after</code>), requests are automatically backed off.
                    </p>
                    <p className="text-xs text-brand-main-400">
                        No manual configuration is needed — add this node to your workflow to enable automatic rate limit protection.
                    </p>
                </div>
            </div>
        </div>
    )
}
