import { createFileRoute } from '@tanstack/react-router'
import { ComingSoonRoute } from '@/components/common/coming-soon-route'

export const Route = createFileRoute('/settings/gateway')({
  component: SettingsGatewayPage,
})

function SettingsGatewayPage() {
  return (
    <ComingSoonRoute
      title="Gateway Settings"
      description="Configure gateway-level defaults and behavior for your instance."
    />
  )
}
