// Builders for the copy-paste configs that connect external clients (Claude
// Desktop, Cursor, Google ADK) to Everstack's MCP server. Pure functions — the
// endpoint URL is derived from the admin's own origin (the /mcp endpoint is on
// the same host), so no backend round-trip is needed.

export type McpClient = 'claude' | 'cursor' | 'adk' | 'raw'

export const MCP_CLIENTS: { id: McpClient; label: string; lang: string }[] = [
    { id: 'claude', label: 'Claude Desktop', lang: 'json' },
    { id: 'cursor', label: 'Cursor', lang: 'json' },
    { id: 'adk', label: 'Google ADK', lang: 'python' },
    { id: 'raw', label: 'Raw', lang: 'bash' },
]

const KEY_PLACEHOLDER = '<YOUR_EVERSTACK_API_KEY>'

/** The MCP server endpoint, on the same origin as the admin app. */
export function mcpEndpointUrl(): string {
    if (typeof window === 'undefined') return '/mcp'
    return `${window.location.origin}/mcp`
}

/** Build the connection snippet for a given client. */
export function buildMcpClientConfig(client: McpClient, url: string, apiKey: string): string {
    const key = apiKey || KEY_PLACEHOLDER
    switch (client) {
        case 'claude':
        case 'cursor':
            return JSON.stringify(
                {
                    mcpServers: {
                        everstack: {
                            url,
                            headers: { Authorization: `Bearer ${key}` },
                        },
                    },
                },
                null,
                2,
            )
        case 'adk':
            return [
                '# Use Everstack as an MCP toolset in a Google ADK agent',
                'from google.adk.tools.mcp_tool import McpToolset, StreamableHTTPConnectionParams',
                '',
                'everstack_tools = McpToolset(',
                '    connection_params=StreamableHTTPConnectionParams(',
                `        url="${url}",`,
                `        headers={"Authorization": "Bearer ${key}"},`,
                '    )',
                ')',
            ].join('\n')
        case 'raw':
        default:
            return [
                `curl -X POST ${url} \\`,
                `  -H "Authorization: Bearer ${key}" \\`,
                `  -H "Content-Type: application/json" \\`,
                `  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`,
            ].join('\n')
    }
}

/** Agent Card URL for a published A2A agent. */
export function a2aAgentCardUrl(agentId: string): string {
    const origin = typeof window === 'undefined' ? '' : window.location.origin
    return `${origin}/a2a/agents/${agentId}/.well-known/agent.json`
}

/** A2A service endpoint for a published agent. */
export function a2aEndpointUrl(agentId: string): string {
    const origin = typeof window === 'undefined' ? '' : window.location.origin
    return `${origin}/a2a/agents/${agentId}`
}
