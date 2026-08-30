import { useState, useEffect } from 'react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useUpdateMcpServer } from '@/hooks/gateway/use-mcp'
import type { McpServer, McpTransportType, McpAuthType } from '@/server/mcp'

const {
    Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody,
    Button, Input, Label,
    Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
    Switch,
} = ui

const brandSelectTriggerClass = 'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandSelectContentClass = 'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandInputClass = 'bg-brand-main-900 border-brand-main-600'

const TRANSPORT_OPTIONS: { value: McpTransportType; label: string }[] = [
    { value: 'MCP_TRANSPORT_TYPE_SSE', label: 'SSE' },
    { value: 'MCP_TRANSPORT_TYPE_STREAMABLE_HTTP', label: 'Streamable HTTP' },
    { value: 'MCP_TRANSPORT_TYPE_STDIO', label: 'Stdio (subprocess)' },
]

const AUTH_OPTIONS: { value: McpAuthType; label: string }[] = [
    { value: 'MCP_AUTH_TYPE_NONE', label: 'None' },
    { value: 'MCP_AUTH_TYPE_API_KEY', label: 'API Key' },
    { value: 'MCP_AUTH_TYPE_BEARER', label: 'Bearer Token' },
    { value: 'MCP_AUTH_TYPE_OAUTH2', label: 'OAuth 2.0' },
]

interface EditMcpServerDialogProps {
    server: McpServer | null
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function EditMcpServerDialog({ server, open, onOpenChange }: EditMcpServerDialogProps) {
    const [name, setName] = useState('')
    const [url, setUrl] = useState('')
    const [transportType, setTransportType] = useState<McpTransportType>('MCP_TRANSPORT_TYPE_SSE')
    const [authType, setAuthType] = useState<McpAuthType>('MCP_AUTH_TYPE_NONE')
    const [authValue, setAuthValue] = useState('')
    const [apiKeyHeader, setApiKeyHeader] = useState('')
    const [enabled, setEnabled] = useState(true)
    const [stdioCommand, setStdioCommand] = useState('')
    const [stdioArgs, setStdioArgs] = useState('')
    const [stdioWorkDir, setStdioWorkDir] = useState('')

    const updateMutation = useUpdateMcpServer()
    const isStdio = transportType === 'MCP_TRANSPORT_TYPE_STDIO'
    const isOAuth = authType === 'MCP_AUTH_TYPE_OAUTH2'

    // Populate form when server changes
    useEffect(() => {
        if (server) {
            setName(server.name)
            setUrl(server.url)
            setTransportType(server.transportType)
            setAuthType(server.authType)
            setEnabled(server.enabled)
            setStdioCommand(server.stdioConfig?.command ?? '')
            setStdioArgs(server.stdioConfig?.args?.join(' ') ?? '')
            setStdioWorkDir(server.stdioConfig?.workingDir ?? '')
            // Don't populate auth secrets — they're not returned from the server
            setAuthValue('')
            setApiKeyHeader((server.authConfig?.api_key_header as string) ?? '')
        }
    }, [server])

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()
        if (!server || !name.trim()) return

        try {
            const authConfig: Record<string, unknown> | undefined =
                authValue ? {
                    token: authValue,
                    ...(authType === 'MCP_AUTH_TYPE_API_KEY' && apiKeyHeader ? { api_key_header: apiKeyHeader } : {}),
                } : undefined

            await updateMutation.mutateAsync({
                id: server.id,
                name: name.trim(),
                url: isStdio ? '' : url.trim(),
                transportType,
                authType,
                ...(authConfig ? { authConfig } : {}),
                enabled,
                stdioConfig: isStdio ? {
                    command: stdioCommand.trim(),
                    args: stdioArgs.trim() ? stdioArgs.trim().split(/\s+/) : undefined,
                    workingDir: stdioWorkDir.trim() || undefined,
                } : undefined,
            })

            toast.success(`Updated "${name}"`)
            onOpenChange(false)
        } catch (err) {
            toast.error(`Failed to update server: ${(err as Error).message}`)
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="w-full sm:max-w-[500px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle>Edit MCP Server</SheetTitle>
                </SheetHeader>
                <SheetBody className="py-4">
                    <form onSubmit={handleSubmit} className="space-y-5">
                        <div className="space-y-2">
                            <Label htmlFor="edit-mcp-name">Name *</Label>
                            <Input
                                id="edit-mcp-name"
                                placeholder="my-mcp-server"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                required
                                className={brandInputClass}
                            />
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="edit-mcp-transport">Transport</Label>
                            <Select value={transportType} onValueChange={(v) => setTransportType(v as McpTransportType)}>
                                <SelectTrigger className={brandSelectTriggerClass}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className={brandSelectContentClass}>
                                    {TRANSPORT_OPTIONS.map((opt) => (
                                        <SelectItem key={opt.value} value={opt.value}>
                                            {opt.label}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {isStdio ? (
                            <>
                                <div className="space-y-2">
                                    <Label htmlFor="edit-mcp-command">Command *</Label>
                                    <Input
                                        id="edit-mcp-command"
                                        placeholder="npx"
                                        value={stdioCommand}
                                        onChange={(e) => setStdioCommand(e.target.value)}
                                        required
                                        className={brandInputClass}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="edit-mcp-args">Arguments</Label>
                                    <Input
                                        id="edit-mcp-args"
                                        placeholder="-y @modelcontextprotocol/server-filesystem /path"
                                        value={stdioArgs}
                                        onChange={(e) => setStdioArgs(e.target.value)}
                                        className={brandInputClass}
                                    />
                                    <p className="text-xs text-brand-main-200">Space-separated command arguments</p>
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="edit-mcp-workdir">Working Directory</Label>
                                    <Input
                                        id="edit-mcp-workdir"
                                        placeholder="/opt/mcp"
                                        value={stdioWorkDir}
                                        onChange={(e) => setStdioWorkDir(e.target.value)}
                                        className={brandInputClass}
                                    />
                                </div>
                            </>
                        ) : (
                            <div className="space-y-2">
                                <Label htmlFor="edit-mcp-url">Server URL *</Label>
                                <Input
                                    id="edit-mcp-url"
                                    placeholder="https://mcp.example.com/sse"
                                    value={url}
                                    onChange={(e) => setUrl(e.target.value)}
                                    required={!isStdio}
                                    className={brandInputClass}
                                />
                            </div>
                        )}

                        <div className="space-y-2">
                            <Label htmlFor="edit-mcp-auth">Authentication</Label>
                            <Select value={authType} onValueChange={(v) => setAuthType(v as McpAuthType)}>
                                <SelectTrigger className={brandSelectTriggerClass}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className={brandSelectContentClass}>
                                    {AUTH_OPTIONS.map((opt) => (
                                        <SelectItem key={opt.value} value={opt.value}>
                                            {opt.label}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {authType === 'MCP_AUTH_TYPE_BEARER' && (
                            <div className="space-y-2">
                                <Label htmlFor="edit-mcp-auth-value">Bearer Token</Label>
                                <Input
                                    id="edit-mcp-auth-value"
                                    type="password"
                                    placeholder="Leave empty to keep existing token"
                                    value={authValue}
                                    onChange={(e) => setAuthValue(e.target.value)}
                                    className={brandInputClass}
                                />
                                <p className="text-xs text-brand-main-200">Leave empty to keep the current token</p>
                            </div>
                        )}

                        {authType === 'MCP_AUTH_TYPE_API_KEY' && (
                            <>
                                <div className="space-y-2">
                                    <Label htmlFor="edit-mcp-auth-value">API Key</Label>
                                    <Input
                                        id="edit-mcp-auth-value"
                                        type="password"
                                        placeholder="Leave empty to keep existing key"
                                        value={authValue}
                                        onChange={(e) => setAuthValue(e.target.value)}
                                        className={brandInputClass}
                                    />
                                    <p className="text-xs text-brand-main-200">Leave empty to keep the current key</p>
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="edit-mcp-api-key-header">Header Name</Label>
                                    <Input
                                        id="edit-mcp-api-key-header"
                                        placeholder="Authorization (default)"
                                        value={apiKeyHeader}
                                        onChange={(e) => setApiKeyHeader(e.target.value)}
                                        className={brandInputClass}
                                    />
                                    <p className="text-xs text-brand-main-200">Custom header name for the API key</p>
                                </div>
                            </>
                        )}

                        {isOAuth && (
                            <div className="rounded-md bg-brand-main-800/50 border border-brand-main-600 p-3">
                                <p className="text-sm text-brand-main-200">
                                    OAuth credentials are managed automatically. To re-authorize, delete and re-register this server.
                                </p>
                            </div>
                        )}

                        <div className="flex items-center justify-between">
                            <div>
                                <Label>Enabled</Label>
                                <p className="text-xs text-brand-main-200">Server is active and tools are available</p>
                            </div>
                            <Switch checked={enabled} onCheckedChange={setEnabled} />
                        </div>

                        <div className="flex justify-end gap-3 pt-4 border-t border-brand-main-700">
                            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)} disabled={updateMutation.isPending}>
                                Cancel
                            </Button>
                            <Button type="submit" disabled={updateMutation.isPending || !name.trim()}>
                                {updateMutation.isPending ? 'Saving...' : 'Save Changes'}
                            </Button>
                        </div>
                    </form>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
