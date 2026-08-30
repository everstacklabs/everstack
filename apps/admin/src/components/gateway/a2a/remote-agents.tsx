import { useState } from 'react'
import { ui } from '@everstack/ui'
import { useRemoteAgents, useUpsertRemoteAgent, useDeleteRemoteAgent } from '@/hooks/gateway/use-interop'

const { Button, Dialog, DialogContent, DialogTitle, DialogDescription, Input, Label } = ui

/** Registry of external A2A agents the tenant's agents can call by name via the
 *  call_external_agent tool. */
export function RemoteAgents() {
    const { data: remotes } = useRemoteAgents()
    const upsert = useUpsertRemoteAgent()
    const del = useDeleteRemoteAgent()

    const [open, setOpen] = useState(false)
    const [name, setName] = useState('')
    const [endpoint, setEndpoint] = useState('')
    const [token, setToken] = useState('')

    const reset = () => {
        setName('')
        setEndpoint('')
        setToken('')
    }

    const save = () => {
        if (!name.trim() || !endpoint.trim()) return
        upsert.mutate(
            { name: name.trim(), endpoint: endpoint.trim(), auth_token: token || undefined },
            {
                onSuccess: () => {
                    setOpen(false)
                    reset()
                },
            },
        )
    }

    const list = remotes ?? []

    return (
        <div className="space-y-3 p-4">
            <p className="text-xs leading-5 text-brand-main-300">
                External A2A agents your agents can call with the{' '}
                <code className="text-brand-main-100">call_external_agent</code> tool by name.
            </p>

            <div className="space-y-2">
                {list.map((r) => (
                    <div
                        key={r.id}
                        className="flex items-center justify-between gap-4 rounded bg-brand-main-800/40 px-3 py-2 border border-brand-main-600/40"
                    >
                        <div className="flex min-w-0 flex-col">
                            <span className="text-sm text-brand-secondary-100">{r.name}</span>
                            <span className="truncate text-[11px] text-brand-main-300">{r.endpoint}</span>
                        </div>
                        <Button variant="ghost" onClick={() => del.mutate({ id: r.id })}>
                            Remove
                        </Button>
                    </div>
                ))}
                {list.length === 0 ? <div className="px-1 py-2 text-xs text-white/45 light:text-black/45">No saved remote agents.</div> : null}
            </div>

            <Button variant="outline" onClick={() => setOpen(true)}>
                Add remote agent
            </Button>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className="w-[560px]">
                    <DialogTitle>Add remote A2A agent</DialogTitle>
                    <DialogDescription>Save an external A2A endpoint your agents can call by name.</DialogDescription>
                    <div className="mt-4 space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="ra-name">Name</Label>
                            <Input
                                id="ra-name"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                placeholder="support-bot"
                                className="bg-brand-main-900 border-brand-main-600"
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="ra-endpoint">Endpoint URL</Label>
                            <Input
                                id="ra-endpoint"
                                value={endpoint}
                                onChange={(e) => setEndpoint(e.target.value)}
                                placeholder="https://…/a2a/agents/…"
                                className="bg-brand-main-900 border-brand-main-600"
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="ra-token">Bearer token (optional)</Label>
                            <Input
                                id="ra-token"
                                type="password"
                                value={token}
                                onChange={(e) => setToken(e.target.value)}
                                className="bg-brand-main-900 border-brand-main-600"
                            />
                        </div>
                        <div className="flex justify-end gap-3 pt-2">
                            <Button variant="outline" onClick={() => setOpen(false)}>
                                Cancel
                            </Button>
                            <Button onClick={save}>Save</Button>
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}
