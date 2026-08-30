import { useState } from 'react'
import { ui } from '@everstack/ui'
import { useDeploymentKeys, useCreateDeploymentKey, useRevokeDeploymentKey } from '@/hooks/deployments/use-agent-deployments'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { Iconify } from '@everstack/ui/icons'

const { Button, Input, Label, Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody } = ui

interface DeploymentKeysSectionProps {
    deploymentId: string
}

export function DeploymentKeysSection({ deploymentId }: DeploymentKeysSectionProps) {
    const { data: keys = [] } = useDeploymentKeys(deploymentId)
    const createKeyMutation = useCreateDeploymentKey()
    const revokeKeyMutation = useRevokeDeploymentKey()

    const [showCreateSheet, setShowCreateSheet] = useState(false)
    const [keyName, setKeyName] = useState('')
    const [rawKey, setRawKey] = useState<string | null>(null)
    const [copied, setCopied] = useState(false)

    const handleCreateKey = () => {
        createKeyMutation.mutate(
            { deploymentId, name: keyName || undefined },
            {
                onSuccess: (resp) => {
                    setRawKey(resp.rawKey)
                    setKeyName('')
                },
            }
        )
    }

    const handleCopyKey = () => {
        if (rawKey) {
            navigator.clipboard.writeText(rawKey)
            setCopied(true)
            setTimeout(() => setCopied(false), 2000)
        }
    }

    const handleCloseCreateSheet = () => {
        setRawKey(null)
        setShowCreateSheet(false)
    }

    return (
        <div className="space-y-3">
            <div className="flex items-center justify-between">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">API Keys</div>
                <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setShowCreateSheet(true)}
                    className="h-7 text-xs"
                >
                    Create Key
                </Button>
            </div>

            {keys.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8">
                    <div className="relative mb-4">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
                            <Iconify.Icon icon="heroicons:key" className="size-6 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-sm font-medium text-white light:text-brand-main-50 mb-1">No API keys created yet</h3>
                    <p className="text-xs text-white/50 light:text-black/50 max-w-xs text-center leading-relaxed">
                        Create an API key to authenticate external clients.
                    </p>
                </div>
            ) : (
                <div className="space-y-2">
                    {keys.map((key) => (
                        <div
                            key={key.id}
                            className="flex items-center justify-between rounded-md bg-brand-main-800/50 px-3 py-2 border border-brand-main-700/30"
                        >
                            <div className="flex items-center gap-3">
                                <span className="font-mono text-xs text-brand-main-100">{key.keyPrefix}...</span>
                                {key.name && <span className="text-xs text-brand-main-200">{key.name}</span>}
                                <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${key.isActive ? 'bg-green-500/20 text-green-300 light:text-green-600' : 'bg-red-500/20 text-red-300 light:text-red-600'}`}>
                                    {key.isActive ? 'Active' : 'Revoked'}
                                </span>
                                {key.lastUsedAt && (
                                    <span className="text-[10px] text-brand-main-300">
                                        Last used: {formatTimestamp(key.lastUsedAt)}
                                    </span>
                                )}
                            </div>
                            {key.isActive && (
                                <Button
                                    size="sm"
                                    variant="ghost"
                                    onClick={() => revokeKeyMutation.mutate(key.id)}
                                    disabled={revokeKeyMutation.isPending}
                                    className="h-6 text-[10px] text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-700 hover:bg-red-500/10"
                                >
                                    Revoke
                                </Button>
                            )}
                        </div>
                    ))}
                </div>
            )}

            {/* Create Key Sheet */}
            <Sheet open={showCreateSheet} onOpenChange={handleCloseCreateSheet}>
                <SheetContent side="right" className="w-full sm:max-w-[400px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                    <SheetHeader>
                        <SheetTitle>{rawKey ? 'API Key Created' : 'Create API Key'}</SheetTitle>
                    </SheetHeader>

                    <SheetBody className="py-4">
                        {rawKey ? (
                            <div className="space-y-3">
                                <p className="text-sm text-amber-400 light:text-amber-700">
                                    Copy this key now. You won't be able to see it again.
                                </p>
                                <div className="flex items-center gap-2">
                                    <code className="flex-1 rounded bg-brand-main-800 px-3 py-2 text-xs text-green-300 light:text-green-700 font-mono break-all border border-brand-main-700/40">
                                        {rawKey}
                                    </code>
                                    <Button size="sm" variant="outline" onClick={handleCopyKey} className="shrink-0">
                                        {copied ? 'Copied' : 'Copy'}
                                    </Button>
                                </div>
                                <div className="flex justify-end pt-2 border-t border-brand-main-700/60">
                                    <Button onClick={handleCloseCreateSheet}>
                                        Done
                                    </Button>
                                </div>
                            </div>
                        ) : (
                            <div className="space-y-4">
                                <div className="space-y-1">
                                    <Label className="text-xs">Name (optional)</Label>
                                    <Input
                                        value={keyName}
                                        onChange={(e) => setKeyName(e.target.value)}
                                        placeholder="e.g., Production, CI/CD"
                                    />
                                </div>
                                <div className="flex justify-end gap-3 pt-2 border-t border-brand-main-700/60">
                                    <Button type="button" variant="outline" onClick={() => setShowCreateSheet(false)}>
                                        Cancel
                                    </Button>
                                    <Button onClick={handleCreateKey} disabled={createKeyMutation.isPending}>
                                        {createKeyMutation.isPending ? 'Creating...' : 'Create'}
                                    </Button>
                                </div>
                            </div>
                        )}
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </div>
    )
}
