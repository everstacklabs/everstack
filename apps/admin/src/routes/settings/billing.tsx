import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { BillingPage } from '@/components/settings/billing/billing-page'
import { isCloudManaged, getCloudBillingUrl } from '@/lib/cloud-mode'
import { CloudBillingRedirect } from '@/components/settings/billing/cloud-billing-redirect'
import { useIsCommunityEdition } from '@/hooks/license/use-license-status'
import { getGatewayStatus } from '@/server/license'
import { buildInstanceConnectUrl, createInstanceConnectSession } from '@/server/instance-connect'
import { Zap } from '@everstack/ui/icons'
import { Button } from '@everstack/ui/components'

export const Route = createFileRoute('/settings/billing')({
    component: CloudAwareBillingPage,
    validateSearch: (search: Record<string, unknown>) => {
        return {
            upgrade_success: search.upgrade_success === 'true' || search.upgrade_success === true,
            plan: search.plan as string | undefined,
        }
    },
})

function CommunityEditionBillingPage() {
    const [isConnecting, setIsConnecting] = useState(false)
    const [error, setError] = useState<string | null>(null)

    const handleConnect = async () => {
        try {
            setIsConnecting(true)
            setError(null)

            const status = await getGatewayStatus()
            const instanceName = window.location.hostname || 'Local Everstack Instance'
            const session = await createInstanceConnectSession({
                instanceName,
                instanceUrl: window.location.origin,
                instanceId: status.instanceId || undefined,
            })

            window.location.href = buildInstanceConnectUrl(session.sessionId)
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to start cloud connection')
            setIsConnecting(false)
        }
    }

    return (
        <div className="flex-1 flex flex-col items-center justify-center">
            <div className="relative mb-6">
                <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                    <Zap className="size-8 text-brand-secondary-400" />
                </div>
            </div>
            <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">Community Edition</h3>
            <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                Connect this instance to Everstack Cloud first. Once connected, authentication and billing can be managed seamlessly from the cloud-backed flow.
            </p>
            <Button onClick={handleConnect} disabled={isConnecting}>
                {isConnecting ? 'Connecting...' : 'Connect to Cloud'}
            </Button>
            {error ? <p className="mt-3 text-sm text-red-300 light:text-red-600">{error}</p> : null}
        </div>
    )
}

function CloudAwareBillingPage() {
    const isCE = useIsCommunityEdition()

    if (isCE) {
        return <CommunityEditionBillingPage />
    }
    if (isCloudManaged()) {
        return <CloudBillingRedirect billingUrl={getCloudBillingUrl()} />
    }
    return <BillingPage />
}
