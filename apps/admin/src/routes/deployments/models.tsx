import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'

export const Route = createFileRoute('/deployments/models')({
    component: RouteComponent,
})

function RouteComponent() {
    const gate = useFeatureGate(FeatureKey.CUSTOM_MODELS)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Custom Models"
                description="Add and configure custom model deployments and routing."
                requiredTier="Free"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <div>Hello "/deployments/models"!</div>
}
