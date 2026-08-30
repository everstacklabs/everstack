import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { CreateScoreConfigPage } from '@/components/evaluations/create-score-config-page'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

export const Route = createFileRoute('/evaluations/score-configs_/new')({
  component: NewScoreConfigRoute,
})

function NewScoreConfigRoute() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Score Configurations"
        description="Configure scoring criteria for evaluations and annotations."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <CreateScoreConfigPage />
}
