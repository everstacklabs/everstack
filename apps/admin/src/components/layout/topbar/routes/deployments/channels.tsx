import { useState } from 'react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { Button } from '@everstack/ui/components'
import { ChannelConfigSheet } from '@/components/deployments/channels/channel-config-sheet'

export const DeploymentsChannelsActions: ActionGroup[] = [
    {
        title: 'Channels',
        actions: [
            {
                type: 'search',
                key: 'search-channels',
                label: 'Search channels',
                placeholder: 'Search channels...',
                searchParam: 'search',
            },
        ],
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'create-channel',
                requiredPermission: 'resource:create',
                label: 'Add Channel',
                component: CreateChannelButton,
            },
        ],
    },
]

function CreateChannelButton() {
    const [open, setOpen] = useState(false)

    return (
        <>
            <Button variant="default" onClick={() => setOpen(true)}>
                Add Channel
            </Button>
            <ChannelConfigSheet
                open={open}
                onOpenChange={setOpen}
                mode="create"
            />
        </>
    )
}
