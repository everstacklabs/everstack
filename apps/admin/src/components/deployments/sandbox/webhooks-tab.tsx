import { useState } from 'react'
import { useSandboxWebhooks, useCreateWebhook, useDeleteWebhook } from '@/hooks/deployments/use-sandbox'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'

const { Button, Input, Label, Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, Switch } = ui

// ─── Helpers ──────────────────────────────────────────────────────────

function formatDate(ts?: string | null): string {
    if (!ts) return '--'
    return new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const STATUS_STYLES: Record<string, string> = {
    enabled: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    disabled: 'bg-gray-500/20 text-gray-400 light:text-gray-600 border-gray-500/30',
}

// ─── Main Component ─────────────────────────────────────────────────

export function WebhooksTab() {
    const [dialogOpen, setDialogOpen] = useState(false)
    const [secretDialogOpen, setSecretDialogOpen] = useState(false)
    const [createdSecret, setCreatedSecret] = useState('')
    const [newName, setNewName] = useState('')
    const [newCommand, setNewCommand] = useState('')
    const [newTimeout, setNewTimeout] = useState('300')
    const [newRateLimit, setNewRateLimit] = useState('60')
    const [newAutoRecreate, setNewAutoRecreate] = useState(false)

    const { activeSessionId: sessionId } = useSandboxContext()

    const { data: webhookData, isLoading, error } = useSandboxWebhooks(sessionId)
    const webhooks = webhookData?.webhooks ?? []
    const createMutation = useCreateWebhook()
    const deleteMutation = useDeleteWebhook()

    const handleCreate = () => {
        if (!newName.trim() || !newCommand.trim()) {
            toast.error('Name and command are required')
            return
        }
        // Generate a path slug from the name
        const pathSlug = newName.trim().toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/-+/g, '-')
        createMutation.mutate(
            {
                sessionId: sessionId!,
                name: newName.trim(),
                path: pathSlug,
                command: newCommand.trim(),
                timeoutSeconds: parseInt(newTimeout, 10) || 300,
                rateLimitRpm: parseInt(newRateLimit, 10) || 60,
                autoRecreate: newAutoRecreate,
            },
            {
                onSuccess: (data) => {
                    toast.success(`Webhook "${newName}" created`)
                    setDialogOpen(false)
                    resetForm()
                    // Show secret dialog
                    if (data.secret) {
                        setCreatedSecret(data.secret)
                        setSecretDialogOpen(true)
                    }
                },
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const handleDelete = (webhookId: string, name: string) => {
        deleteMutation.mutate(webhookId, {
            onSuccess: () => toast.success(`Webhook "${name}" deleted`),
            onError: (err) => toast.error(err.message),
        })
    }

    const handleCopySecret = () => {
        navigator.clipboard.writeText(createdSecret)
        toast.success('Secret copied to clipboard')
    }

    const handleCopyUrl = (url: string) => {
        navigator.clipboard.writeText(url)
        toast.success('URL copied to clipboard')
    }

    const resetForm = () => {
        setNewName('')
        setNewCommand('')
        setNewTimeout('300')
        setNewRateLimit('60')
        setNewAutoRecreate(false)
    }

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxSessionPicker />

                <div className="flex-1" />

                <button
                    onClick={() => setDialogOpen(true)}
                    disabled={!sessionId}
                    className="flex items-center gap-1 text-xs bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30 rounded px-2.5 py-1 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                >
                    <Iconify.Icon icon="heroicons:plus" className="size-3" />
                    New Webhook
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {!sessionId ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:bolt" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No session selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a running session to manage webhooks.
                        </p>
                    </div>
                ) : isLoading ? (
                    <div className="flex-1 flex items-center justify-center h-full">
                        <Loader loaderText="Loading webhooks..." />
                    </div>
                ) : error ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading webhooks: {error.message}
                    </div>
                ) : webhooks.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:bolt" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No webhooks</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a webhook to trigger sandbox commands via HTTP.
                        </p>
                    </div>
                ) : (
                    <div className="p-4">
                        <table className="w-full text-sm">
                            <thead>
                                <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                    <th className="text-left py-2 px-3 font-medium">Name</th>
                                    <th className="text-left py-2 px-3 font-medium">Path</th>
                                    <th className="text-left py-2 px-3 font-medium">Command</th>
                                    <th className="text-left py-2 px-3 font-medium">Status</th>
                                    <th className="text-left py-2 px-3 font-medium">Triggers</th>
                                    <th className="text-left py-2 px-3 font-medium">Rate Limit</th>
                                    <th className="text-left py-2 px-3 font-medium">Last Triggered</th>
                                    <th className="text-right py-2 px-3 font-medium">Actions</th>
                                </tr>
                            </thead>
                            <tbody>
                                {webhooks.map((wh) => (
                                    <tr
                                        key={wh.id}
                                        className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50"
                                    >
                                        <td className="py-2.5 px-3 text-white/80 light:text-black/80 font-medium">{wh.name}</td>
                                        <td className="py-2.5 px-3">
                                            {wh.path ? (
                                                <button
                                                    onClick={() => handleCopyUrl(`/wh/${wh.path}`)}
                                                    className="flex items-center gap-1 text-brand-secondary-400 hover:text-brand-secondary-300 font-mono text-xs group"
                                                    title="Click to copy"
                                                >
                                                    <span className="max-w-[180px] truncate">/wh/{wh.path}</span>
                                                    <Iconify.Icon icon="heroicons:clipboard" className="size-3 opacity-0 group-hover:opacity-100 transition-opacity" />
                                                </button>
                                            ) : (
                                                <span className="text-white/30 light:text-black/30 text-xs">--</span>
                                            )}
                                        </td>
                                        <td className="py-2.5 px-3 font-mono text-xs text-white/50 light:text-black/50 max-w-[160px] truncate" title={wh.command}>
                                            {wh.command}
                                        </td>
                                        <td className="py-2.5 px-3">
                                            <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${STATUS_STYLES[wh.enabled ? 'enabled' : 'disabled']}`}>
                                                {wh.enabled ? 'enabled' : 'disabled'}
                                            </span>
                                        </td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">
                                            {wh.triggerCount}
                                            {wh.errorCount > 0 && (
                                                <span className="text-red-400 light:text-red-600 ml-1">({wh.errorCount} err)</span>
                                            )}
                                        </td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{wh.rateLimitRpm}/min</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatDate(wh.lastTriggeredAt)}</td>
                                        <td className="py-2.5 px-3 text-right">
                                            <button
                                                onClick={() => handleDelete(String(wh.id), wh.name)}
                                                disabled={deleteMutation.isPending}
                                                className="p-1 rounded text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                                                title="Delete"
                                            >
                                                <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                            </button>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            {/* Create Webhook Dialog */}
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">New Webhook</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="webhookName" className="text-xs text-white/60 light:text-black/60">Name</Label>
                            <Input
                                id="webhookName"
                                value={newName}
                                onChange={(e) => setNewName(e.target.value)}
                                placeholder="e.g. deploy-hook"
                                className="w-full"
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="webhookCommand" className="text-xs text-white/60 light:text-black/60">Command</Label>
                            <Input
                                id="webhookCommand"
                                value={newCommand}
                                onChange={(e) => setNewCommand(e.target.value)}
                                placeholder="sh deploy.sh"
                                className="w-full font-mono"
                            />
                            <p className="text-[10px] text-white/30 light:text-black/30">Webhook body is available as $WEBHOOK_BODY env var</p>
                        </div>
                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-1.5">
                                <Label htmlFor="webhookTimeout" className="text-xs text-white/60 light:text-black/60">Timeout (seconds)</Label>
                                <Input
                                    id="webhookTimeout"
                                    type="number"
                                    value={newTimeout}
                                    onChange={(e) => setNewTimeout(e.target.value)}
                                    min={1}
                                    max={3600}
                                    className="w-full"
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label htmlFor="webhookRateLimit" className="text-xs text-white/60 light:text-black/60">Rate Limit (req/min)</Label>
                                <Input
                                    id="webhookRateLimit"
                                    type="number"
                                    value={newRateLimit}
                                    onChange={(e) => setNewRateLimit(e.target.value)}
                                    min={1}
                                    max={1000}
                                    className="w-full"
                                />
                            </div>
                        </div>
                        <div className="flex items-center gap-2">
                            <Switch
                                checked={newAutoRecreate}
                                onCheckedChange={setNewAutoRecreate}
                            />
                            <Label className="text-xs text-white/60 light:text-black/60">Auto-recreate sandbox if destroyed</Label>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => { setDialogOpen(false); resetForm() }}>
                            Cancel
                        </Button>
                        <Button
                            onClick={handleCreate}
                            disabled={createMutation.isPending || !newName || !newCommand}
                        >
                            {createMutation.isPending ? 'Creating...' : 'Create'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* Secret Display Dialog */}
            <Dialog open={secretDialogOpen} onOpenChange={setSecretDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">Webhook Secret</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-3 py-2">
                        <div className="flex items-center gap-2 p-3 bg-yellow-500/10 border border-yellow-500/30 rounded text-yellow-300 light:text-yellow-700 text-xs">
                            <Iconify.Icon icon="heroicons:exclamation-triangle" className="size-4 shrink-0" />
                            <p>Save this secret now. It will not be shown again.</p>
                        </div>
                        <div className="relative">
                            <pre className="p-3 bg-brand-main-800 border border-brand-main-600 rounded font-mono text-sm text-white/80 light:text-black/80 break-all pr-10">
                                {createdSecret}
                            </pre>
                            <button
                                onClick={handleCopySecret}
                                className="absolute top-2 right-2 p-1.5 rounded bg-brand-main-700 hover:bg-brand-main-600 text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50 transition-colors"
                                title="Copy to clipboard"
                            >
                                <Iconify.Icon icon="heroicons:clipboard" className="size-4" />
                            </button>
                        </div>
                        <p className="text-[11px] text-white/40 light:text-black/40">
                            Include this in your requests as: <code className="text-white/60 light:text-black/60">X-Webhook-Signature: sha256=hmac(secret, body)</code>
                        </p>
                    </div>
                    <DialogFooter>
                        <Button onClick={() => { setSecretDialogOpen(false); setCreatedSecret('') }}>
                            Done
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}
