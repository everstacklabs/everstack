import { createFileRoute } from '@tanstack/react-router'
import { ComingSoonRoute } from '@/components/common/coming-soon-route'

export const Route = createFileRoute('/settings/catalog')({
    component: SettingsCatalogPage,
})

function SettingsCatalogPage() {
    return (
        <ComingSoonRoute
            title="Catalog"
            description="Control model and provider catalog behavior, sync policies, and visibility."
        />
    )
}
