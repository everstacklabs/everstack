import { useState, useEffect, useCallback } from 'react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useRegisterMcpServer, useInitiateMcpOAuth } from '@/hooks/gateway/use-mcp'
import type { McpTransportType, McpAuthType } from '@/server/mcp'

const {
    Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody,
    Button, Input, Label,
    Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
    Switch,
} = ui

const brandSelectTriggerClass = 'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandSelectContentClass = 'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const brandInputClass = 'bg-brand-main-900 border-brand-main-600'

interface RegisterMcpServerDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

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

export function RegisterMcpServerDialog({ open, onOpenChange }: RegisterMcpServerDialogProps) {
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
    const [oauthPending, setOauthPending] = useState(false)

    const registerMutation = useRegisterMcpServer()
    const oauthMutation = useInitiateMcpOAuth()
    const isStdio = transportType === 'MCP_TRANSPORT_TYPE_STDIO'
    const isOAuth = authType === 'MCP_AUTH_TYPE_OAUTH2'

    // Listen for OAuth popup completion
    const handleOAuthMessage = useCallback((event: MessageEvent) => {
        if (event.data?.type === 'mcp-oauth-success') {
            setOauthPending(false)
            toast.success('OAuth authorization successful')
            handleClose()
        } else if (event.data?.type === 'mcp-oauth-error') {
            setOauthPending(false)
            toast.error(`OAuth authorization failed: ${event.data.error}`)
        }
    }, []) // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
        window.addEventListener('message', handleOAuthMessage)
        return () => window.removeEventListener('message', handleOAuthMessage)
    }, [handleOAuthMessage])

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()
        if (!name.trim()) return

        try {
            const authConfig: Record<string, unknown> | undefined =
                authValue ? {
                    token: authValue,
                    ...(authType === 'MCP_AUTH_TYPE_API_KEY' && apiKeyHeader ? { api_key_header: apiKeyHeader } : {}),
                } : undefined

            const result = await registerMutation.mutateAsync({
                name: name.trim(),
                url: isStdio ? '' : url.trim(),
                transportType,
                authType,
                authConfig,
                enabled,
                stdioConfig: isStdio ? {
                    command: stdioCommand.trim(),
                    args: stdioArgs.trim() ? stdioArgs.trim().split(/\s+/) : undefined,
                    workingDir: stdioWorkDir.trim() || undefined,
                } : undefined,
            })

            // If OAuth2 is selected, initiate the OAuth flow after registration
            if (isOAuth) {
                try {
                    setOauthPending(true)
                    const oauthResp = await oauthMutation.mutateAsync(result.server.id)
                    // Open popup for authorization
                    const popup = window.open(
                        oauthResp.authorization_url,
                        'mcp-oauth',
                        'width=600,height=700,scrollbars=yes'
                    )
                    if (!popup) {
                        setOauthPending(false)
                        toast.error('Pop-up blocked. Please allow pop-ups for this site.')
                    }
                } catch (oauthErr) {
                    setOauthPending(false)
                    toast.error(`OAuth initiation failed: ${(oauthErr as Error).message}`)
                    // Server is still registered, just without OAuth tokens
                    toast.success(`Registered "${name}" (OAuth pending)`)
                    handleClose()
                }
                return
            }

            toast.success(`Registered "${name}" with ${result.tools.length} tools discovered`)
            handleClose()
        } catch (err) {
            toast.error(`Failed to register server: ${(err as Error).message}`)
        }
    }

    const handleClose = () => {
        setName('')
        setUrl('')
        setTransportType('MCP_TRANSPORT_TYPE_SSE')
        setAuthType('MCP_AUTH_TYPE_NONE')
        setAuthValue('')
        setApiKeyHeader('')
        setEnabled(true)
        setStdioCommand('')
        setStdioArgs('')
        setStdioWorkDir('')
        setOauthPending(false)
        onOpenChange(false)
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="w-full sm:max-w-[500px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle>Register MCP Server</SheetTitle>
                </SheetHeader>
                <SheetBody className="py-4">
                    <form onSubmit={handleSubmit} className="space-y-5">
                        <div className="space-y-2">
                            <Label htmlFor="mcp-name">Name *</Label>
                            <Input
                                id="mcp-name"
                                placeholder="my-mcp-server"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                required
                                className={brandInputClass}
                            />
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="mcp-transport">Transport</Label>
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
                                    <Label htmlFor="mcp-command">Command *</Label>
                                    <Input
                                        id="mcp-command"
                                        placeholder="npx"
                                        value={stdioCommand}
                                        onChange={(e) => setStdioCommand(e.target.value)}
                                        required
                                        className={brandInputClass}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="mcp-args">Arguments</Label>
                                    <Input
                                        id="mcp-args"
                                        placeholder="-y @modelcontextprotocol/server-filesystem /path"
                                        value={stdioArgs}
                                        onChange={(e) => setStdioArgs(e.target.value)}
                                        className={brandInputClass}
                                    />
                                    <p className="text-xs text-brand-main-200">Space-separated command arguments</p>
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="mcp-workdir">Working Directory</Label>
                                    <Input
                                        id="mcp-workdir"
                                        placeholder="/opt/mcp"
                                        value={stdioWorkDir}
                                        onChange={(e) => setStdioWorkDir(e.target.value)}
                                        className={brandInputClass}
                                    />
                                </div>
                            </>
                        ) : (
                            <div className="space-y-2">
                                <Label htmlFor="mcp-url">Server URL *</Label>
                                <Input
                                    id="mcp-url"
                                    placeholder="https://mcp.example.com/sse"
                                    value={url}
                                    onChange={(e) => setUrl(e.target.value)}
                                    required={!isStdio}
                                    className={brandInputClass}
                                />
                            </div>
                        )}

                        <div className="space-y-2">
                            <Label htmlFor="mcp-auth">Authentication</Label>
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
                                <Label htmlFor="mcp-auth-value">Bearer Token</Label>
                                <Input
                                    id="mcp-auth-value"
                                    type="password"
                                    placeholder="Enter bearer token"
                                    value={authValue}
                                    onChange={(e) => setAuthValue(e.target.value)}
                                    className={brandInputClass}
                                />
                            </div>
                        )}

                        {authType === 'MCP_AUTH_TYPE_API_KEY' && (
                            <>
                                <div className="space-y-2">
                                    <Label htmlFor="mcp-auth-value">API Key</Label>
                                    <Input
                                        id="mcp-auth-value"
                                        type="password"
                                        placeholder="Enter API key"
                                        value={authValue}
                                        onChange={(e) => setAuthValue(e.target.value)}
                                        className={brandInputClass}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="mcp-api-key-header">Header Name</Label>
                                    <Input
                                        id="mcp-api-key-header"
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
                                    After registering, you will be redirected to the server&apos;s authorization page to complete the OAuth 2.0 flow.
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
                            <Button type="button" variant="ghost" onClick={handleClose} disabled={registerMutation.isPending || oauthPending}>
                                Cancel
                            </Button>
                            <Button type="submit" disabled={registerMutation.isPending || oauthPending || !name.trim()}>
                                {oauthPending ? 'Waiting for OAuth...' :
                                 registerMutation.isPending ? 'Registering...' :
                                 isOAuth ? 'Register & Connect with OAuth' :
                                 'Register Server'}
                            </Button>
                        </div>
                    </form>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
