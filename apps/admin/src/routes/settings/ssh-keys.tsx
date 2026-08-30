import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { SSHKeysSection } from '@/components/settings/profile/ssh-keys-section'

export const Route = createFileRoute('/settings/ssh-keys')({
    component: RouteComponent,
})

function RouteComponent() {
    const gate = useFeatureGate(FeatureKey.SANDBOX_SSH)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="SSH Keys"
                description="SSH access to running sandboxes for debugging and development."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return (
        <div className="flex flex-col h-full space-y-4 p-8 px-60 mx-auto w-full overflow-y-auto">
            <SSHKeysSection />
        </div>
    )
}
