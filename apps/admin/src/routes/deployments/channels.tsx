import { createFileRoute } from '@tanstack/react-router'
import { ChannelList } from '@/components/deployments/channels/channel-list'

export const Route = createFileRoute('/deployments/channels')({
    component: ChannelsPage,
})

function ChannelsPage() {
    return <ChannelList />
}
