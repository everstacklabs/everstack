import { createFileRoute } from '@tanstack/react-router'
import { UsagePage } from '@/components/settings/usage/usage-page'

export const Route = createFileRoute('/license/status-and-usage')({
    component: UsagePage,
})

