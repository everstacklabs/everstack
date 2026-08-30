import { createFileRoute } from '@tanstack/react-router'
import { ComingSoonRoute } from '@/components/common/coming-soon-route'

export const Route = createFileRoute('/settings/domain')({
  component: SettingsDomainPage,
})

function SettingsDomainPage() {
  return (
    <ComingSoonRoute
      title="Domain"
      description="Manage custom domains, DNS configuration, and verification settings for your instance."
    />
  )
}
