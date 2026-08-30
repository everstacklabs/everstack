import { Icon } from '@iconify/react'
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { AuthConfig } from '../../types'

interface Props { config: AuthConfig; onChange: (config: AuthConfig) => void }

export function AuthConfigForm({ config, onChange }: Props) {
    const addWebhookHeader = () => {
        const headers = { ...(config.webhookHeaders ?? {}), '': '' }
        onChange({ ...config, webhookHeaders: headers })
    }
    const removeWebhookHeader = (key: string) => {
        const headers = { ...(config.webhookHeaders ?? {}) }
        delete headers[key]
        onChange({ ...config, webhookHeaders: headers })
    }
    const updateWebhookHeader = (oldKey: string, newKey: string, value: string) => {
        const headers = { ...(config.webhookHeaders ?? {}) }
        if (oldKey !== newKey) {
            delete headers[oldKey]
        }
        headers[newKey] = value
        onChange({ ...config, webhookHeaders: headers })
    }

    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Mode</Label>
                <Select value={config.mode} onValueChange={(v) => onChange({ ...config, mode: v as AuthConfig['mode'] })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="api_key">API Key</SelectItem>
                        <SelectItem value="jwt">JWT</SelectItem>
                        <SelectItem value="webhook">Webhook</SelectItem>
                        <SelectItem value="none">None (Passthrough)</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            {config.mode !== 'none' && (
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Header Name</Label>
                    <Input value={config.headerName} onChange={(e) => onChange({ ...config, headerName: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="Authorization" />
                </div>
            )}

            {config.mode === 'jwt' && (
                <>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Issuer</Label>
                        <Input value={config.issuer ?? ''} onChange={(e) => onChange({ ...config, issuer: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="https://auth.example.com" />
                        <p className="text-xs text-brand-main-400">Expected &quot;iss&quot; claim value. Leave empty to skip issuer validation.</p>
                    </div>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Audience</Label>
                        <Input value={config.audience ?? ''} onChange={(e) => onChange({ ...config, audience: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="my-api" />
                        <p className="text-xs text-brand-main-400">Expected &quot;aud&quot; claim value. Leave empty to skip audience validation.</p>
                    </div>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">JWKS URL</Label>
                        <Input value={config.jwksUrl ?? ''} onChange={(e) => onChange({ ...config, jwksUrl: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="https://auth.example.com/.well-known/jwks.json" />
                        <p className="text-xs text-brand-main-400">JWKS endpoint for signature verification (coming soon). Currently validates structure, expiry, issuer, and audience only.</p>
                    </div>
                </>
            )}

            {config.mode === 'webhook' && (
                <>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Webhook URL</Label>
                        <Input value={config.webhookUrl ?? ''} onChange={(e) => onChange({ ...config, webhookUrl: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="https://auth.example.com/validate" />
                    </div>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">HTTP Method</Label>
                        <Select value={config.webhookMethod ?? 'POST'} onValueChange={(v) => onChange({ ...config, webhookMethod: v })}>
                            <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="POST">POST</SelectItem>
                                <SelectItem value="GET">GET</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-2">
                        <div className="flex items-center justify-between">
                            <Label className="text-sm text-brand-main-200">Custom Headers</Label>
                            <button onClick={addWebhookHeader} className="flex items-center gap-1 text-xs text-brand-secondary-400 hover:text-brand-secondary-300">
                                <Icon icon="lucide:plus" className="h-3 w-3" /> Add
                            </button>
                        </div>
                        {Object.entries(config.webhookHeaders ?? {}).map(([key, value]) => (
                            <div key={key} className="flex gap-2 items-center">
                                <Input value={key} onChange={(e) => updateWebhookHeader(key, e.target.value, value)} placeholder="Header" className="flex-1 bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                                <Input value={value} onChange={(e) => updateWebhookHeader(key, key, e.target.value)} placeholder="Value" className="flex-1 bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                                <button onClick={() => removeWebhookHeader(key)} className="p-1 text-brand-main-400 hover:text-red-400 light:hover:text-red-600">
                                    <Icon icon="lucide:x" className="h-3.5 w-3.5" />
                                </button>
                            </div>
                        ))}
                        <p className="text-xs text-brand-main-400">
                            The credential is sent as {`{ "token": "..." }`} in the request body.
                            Expects {`{ "authenticated": true, "user": {...} }`} or HTTP 200/401.
                        </p>
                    </div>
                </>
            )}

            {config.mode === 'none' && (
                <p className="text-xs text-brand-main-400">
                    Authentication is skipped. All requests will pass through.
                </p>
            )}
        </div>
    )
}
