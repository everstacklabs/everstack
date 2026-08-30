import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { CreateEvalRunPage } from '@/components/evaluations/create-eval-run-page'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

export const Route = createFileRoute('/evaluations/runs_/new')({
  component: NewEvalRunRoute,
})

function NewEvalRunRoute() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Evaluation Runs"
        description="Run evaluations with scheduled runs, regression detection, and scoring."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <CreateEvalRunPage />
}
