import { createFileRoute } from '@tanstack/react-router'
import { BotsPanel } from '@/components/gateway/bots/bots-panel'

export const Route = createFileRoute('/gateway/bots')({
    component: BotsPage,
})

function BotsPage() {
    return (
        <div className="flex h-full w-full flex-col overflow-auto">
            <BotsPanel />
        </div>
    )
}
