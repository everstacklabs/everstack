import { Icon } from '@iconify/react'
import { Button } from '@everstack/ui/components'
import { useEffect } from 'react'

interface CloudBillingRedirectProps {
    billingUrl: string
}

/**
 * Shown in cloud-managed mode instead of the self-hosted billing page.
 * Redirects immediately to the owning organization's Cloud billing page and
 * keeps a manual fallback visible if browser navigation is interrupted.
 */
export function CloudBillingRedirect({ billingUrl }: CloudBillingRedirectProps) {
    useEffect(() => {
        location.replace(billingUrl)
    }, [billingUrl])

    return (
        <div className="flex-1 flex flex-col items-center justify-center gap-5 px-6 -mt-10">
            <div className="relative">
                <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                    <Icon icon="lucide:credit-card" className="size-8 text-brand-secondary-400" />
                </div>
            </div>

            <div className="text-center space-y-2">
                <h3 className="text-base font-medium text-white light:text-brand-main-50">Redirecting to Cloud billing</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm leading-relaxed">
                    This instance is managed through Everstack Cloud. If the billing page does not open automatically, continue below.
                </p>
            </div>

            <Button onClick={() => location.replace(billingUrl)}>
                <Icon icon="lucide:external-link" className="mr-2 size-4" />
                Continue to Billing
            </Button>
        </div>
    )
}
