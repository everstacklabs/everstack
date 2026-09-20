import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { OpenAISessionDetail } from '@/components/deployments/agents/openai-session-detail'

export const Route = createFileRoute(
  '/deployments/agents/openai-sessions/$sessionId',
)({
  component: OpenAISessionDetailPage,
})

function OpenAISessionDetailPage() {
  const { sessionId } = Route.useParams()
  const navigate = useNavigate()
  return (
    <OpenAISessionDetail
      key={sessionId}
      sessionId={sessionId}
      onDeleted={() =>
        navigate({ to: '/deployments/agents', search: { tab: 'sessions' } })
      }
    />
  )
}
