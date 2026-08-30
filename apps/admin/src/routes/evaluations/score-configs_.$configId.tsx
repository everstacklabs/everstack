import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { ScoreConfigEditorPage } from '@/components/evaluations/score-config-editor'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

export const Route = createFileRoute('/evaluations/score-configs_/$configId')({
  component: EditScoreConfigRoute,
})

function EditScoreConfigRoute() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const { configId } = Route.useParams()

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

  return <ScoreConfigEditorPage configId={configId} />
}
