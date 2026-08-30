import { createFileRoute } from '@tanstack/react-router'
import { AccountSettingsPage } from '@/components/settings/account/account-settings-page'

export const Route = createFileRoute('/account/profile')({
    component: AccountSettingsPage,
})
