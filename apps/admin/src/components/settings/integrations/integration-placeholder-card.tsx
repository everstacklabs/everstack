import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { IntegrationCard, type IntegrationStatus } from './integration-card'

const { Button } = ui

type IntegrationPlaceholderCardProps = {
    name: string
    icon: string
    category: string
    description: string
    status: IntegrationStatus
    capabilities?: string[]
}

export function IntegrationPlaceholderCard(props: IntegrationPlaceholderCardProps) {
    return (
        <IntegrationCard
            name={props.name}
            icon={props.icon}
            category={props.category}
            description={props.description}
            status={props.status}
            capabilities={props.capabilities}
            action={
                <Button size="default" variant="ghost" disabled>
                    <Iconify.Icon icon="mdi:clock-outline" className="w-4 h-4" />
                    Planned
                </Button>
            }
        >
            <div className="rounded border border-dashed border-brand-main-500/40 bg-brand-main-800/30 p-2.5 text-xs text-zinc-400 light:text-zinc-600">
                On the roadmap. This will activate once backend APIs are available.
            </div>
        </IntegrationCard>
    )
}
