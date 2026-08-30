import { type ActionGroup } from '@/components/layout/topbar/types'

export const GatewayMcpGatewayActions: ActionGroup[] = [
    {
        title: 'MCP Gateway',
        actions: [
            {
                type: 'search',
                key: 'search-mcp',
                label: 'Search',
                searchParam: 'search',
                placeholder: 'Search servers or tools...',
                debounceMs: 300,
            },
        ],
    },
    {
        actions: [
            {
                type: 'button',
                key: 'register-mcp-server',
                requiredPermission: 'resource:create',
                label: 'Register Server',
                variant: 'default',
                onClick: (setDialogOpen: (open: boolean) => void) => () => setDialogOpen(true),
            },
        ],
    },
]
