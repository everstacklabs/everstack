import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { CreateAnnotationQueuePage } from '@/components/evaluations/create-annotation-queue-page'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

export const Route = createFileRoute('/evaluations/annotation-queues_/new')({
  component: NewAnnotationQueueRoute,
})

function NewAnnotationQueueRoute() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Annotation Queues"
        description="Human review queues for data labeling and quality assurance."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <CreateAnnotationQueuePage />
}
