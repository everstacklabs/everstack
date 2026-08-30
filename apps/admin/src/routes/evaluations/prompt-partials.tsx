import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { ComingSoonRoute } from '@/components/common/coming-soon-route'

export const Route = createFileRoute('/evaluations/prompt-partials')({
    component: EvaluationsPromptPartialsPage,
})

function EvaluationsPromptPartialsPage() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Prompt Management"
                description="Version and manage prompt templates and reusable partials."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return (
        <ComingSoonRoute
            title="Prompt Partials"
            description="Create and manage shared prompt fragments that can be composed into larger prompt templates."
        />
    )
}
