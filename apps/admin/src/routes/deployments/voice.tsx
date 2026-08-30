import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { VoiceProfilesList } from '@/components/deployments/voice/voice-profiles-list'

export const Route = createFileRoute('/deployments/voice')({
    component: VoicePage,
})

function VoicePage() {
    const gate = useFeatureGate(FeatureKey.VOICE)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Voice"
                description="Voice cloning, text-to-speech, and speech-to-text capabilities for your agents and workflows."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <VoiceProfilesList />
}
